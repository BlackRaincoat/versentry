package core

import (
	"context"
	"log/slog"
	"strings"

	"github.com/BlackRaincoat/versentry/internal/imageweb"
	"github.com/Masterminds/semver/v3"
)

// Why digest mode was chosen (empty when mode is semver or numeric).
const (
	digestCauseRule = "rule" // explicit track: digest
	digestCauseAuto = "auto" // tag is neither semver nor strict dotted numeric
)

// LinksModeDigestRule / LinksModeDigestAuto are MODE column values for versentry links.
const (
	LinksModeDigestRule     = "digest(rule)"
	LinksModeDigestAuto     = "digest(auto)"
	LinksModeSemverFloating = "semver(floating)"
)

// resolveTrackingMode chooses semver / numeric / digest without contacting the registry.
// Shared by checkContainer and Links so track rules stay in one place.
// digestCause is digestCauseRule, digestCauseAuto, or "" when mode is semver/numeric.
// Does not log — callers use logTrackingDiagnostics for one-time WARN.
func resolveTrackingMode(
	rules RuleResolver,
	host, repo, tag, container string,
	labels map[string]string,
) (mode string, rule *Rule, digestCause string) {
	if rules != nil {
		rule = rules.RuleFor(RuleQuery{Host: host, Image: repo, Container: container, Labels: labels})
	}
	if rule != nil && rule.Track == RuleTrackDigest {
		return imageweb.ModeDigest, rule, digestCauseRule
	}
	if _, err := parseContainerSemver(tag); err == nil {
		return imageweb.ModeSemver, rule, ""
	}
	// Masterminds rejected the tag: only then try strict dotted numeric (typically 4+ segments).
	if _, ok := parseNumericVersion(tag); ok {
		return imageweb.ModeNumeric, rule, ""
	}
	return imageweb.ModeDigest, rule, digestCauseAuto
}

// linksDisplayMode is the MODE column for versentry links.
// A bare-major semver tag (16, 7-alpine) is marked floating: links makes no
// registry calls, so the two-component case (1.27 vs a pinned postgres 16.4)
// is left to the check-time hint, which has the tag list as evidence.
func linksDisplayMode(mode, digestCause, tag string) string {
	switch mode {
	case imageweb.ModeSemver:
		if tagCoreComponents(tag) == 1 {
			return LinksModeSemverFloating
		}
		return imageweb.ModeSemver
	case imageweb.ModeNumeric:
		return imageweb.ModeNumeric
	case imageweb.ModeDigest:
		if digestCause == digestCauseRule {
			return LinksModeDigestRule
		}
		return LinksModeDigestAuto
	default:
		return mode
	}
}

// logTrackingDiagnostics emits one-time WARN for silent digest traps (does not change detection).
func (e *Engine) logTrackingDiagnostics(container, repo, tag, mode, digestCause string, rule *Rule) {
	if e == nil || e.log == nil {
		return
	}
	if rule != nil && len(rule.Overridden) > 0 {
		e.logOnce(slog.LevelInfo,
			"label-override:"+container+"|"+repo+"|"+strings.Join(rule.Overridden, ","),
			"container labels override config rule",
			"container", container,
			"image", repo,
			"fields", strings.Join(rule.Overridden, ","),
		)
	}
	if mode != imageweb.ModeDigest {
		return
	}

	// latest is Docker's well-known default floating tag — digest tracking is expected,
	// not a silent trap. Skip WARN only for this exact tag (not a growing suffix list).
	if digestCause == digestCauseAuto && tag != "latest" {
		e.warnOnce(
			"digest-auto:"+container+"|"+repo+"|"+tag,
			"tag is not semver; tracking by digest — newer version tags will not be detected",
			"container", container,
			"image", repo,
			"tag", tag,
		)
	}

	if rule != nil && rule.Include != nil {
		e.warnOnce(
			"include-ignored-digest:"+container+"|"+repo,
			"include rule ignored: container tracked by digest (include applies only in semver mode)",
			"container", container,
			"image", repo,
			"tag", tag,
			"digest", digestCause,
		)
	}
	if rule != nil && rule.Exclude != nil {
		e.warnOnce(
			"exclude-ignored-digest:"+container+"|"+repo,
			"exclude rule ignored: container tracked by digest (exclude applies only in semver mode)",
			"container", container,
			"image", repo,
			"tag", tag,
			"digest", digestCause,
		)
	}
}

// logFloatingTagHint emits a one-time INFO when the registry proves the current
// semver tag is a moving line tag: it has more specific tags under the same
// numeric core (16 → 16.10, 1.27-alpine → 1.27.3-alpine). Semver reports newer
// versions (often a new major) but never "this tag was rebuilt" — the update
// docker pull would actually fetch. Detection is unchanged.
func (e *Engine) logFloatingTagHint(container, repo, tag string, tags []string) {
	example := floatingLineExample(tag, tags)
	if example == "" {
		return
	}
	e.logOnce(slog.LevelInfo,
		"floating:"+container+"|"+repo+"|"+tag,
		"floating tag on semver path: newer versions are reported, rebuilds of this tag are not; add track: digest for rebuild alerts",
		"container", container,
		"image", repo,
		"tag", tag,
		"line_tag", example,
	)
}

func (e *Engine) warnOnce(key, msg string, args ...any) {
	e.logOnce(slog.LevelWarn, key, msg, args...)
}

func (e *Engine) logOnce(level slog.Level, key, msg string, args ...any) {
	if e == nil || e.log == nil {
		return
	}
	if _, loaded := e.warnOnceKeys.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	e.log.Log(context.Background(), level, msg, args...)
}

// tagCoreComponents counts dot-separated parts of the numeric core
// (v-prefix and -/+ suffix stripped): 16 → 1, 1.27-alpine → 2, v8.2.2 → 3.
func tagCoreComponents(tag string) int {
	return len(splitTagCore(tag))
}

func splitTagCore(tag string) []string {
	s := strings.TrimSpace(tag)
	if hasVPrefix(s) {
		s = s[1:]
	}
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return nil
	}
	return strings.Split(s, ".")
}

// floatingLineExample returns the newest registry tag whose numeric core
// extends the current one with more components, preferring the current
// suffix (7-alpine → 7.4.2-alpine over 7.4.2). "" when current already has
// three or more components or no such tag exists (a pinned postgres 16.4
// has no 16.4.x tags, so it is not flagged).
func floatingLineExample(current string, tags []string) string {
	cur := splitTagCore(current)
	if len(cur) == 0 || len(cur) >= 3 {
		return ""
	}
	prefix := strings.Join(cur, ".") + "."
	curSuffix := ""
	if v, err := semver.NewVersion(current); err == nil {
		curSuffix = v.Prerelease()
	}

	var best, bestSame *semver.Version
	var bestTag, bestSameTag string
	for _, raw := range tags {
		core := splitTagCore(raw)
		if len(core) <= len(cur) || !strings.HasPrefix(strings.Join(core, ".")+".", prefix) {
			continue
		}
		v, err := semver.NewVersion(raw)
		if err != nil {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best, bestTag = v, raw
		}
		if v.Prerelease() == curSuffix && (bestSame == nil || v.GreaterThan(bestSame)) {
			bestSame, bestSameTag = v, raw
		}
	}
	if bestSameTag != "" {
		return bestSameTag
	}
	return bestTag
}
