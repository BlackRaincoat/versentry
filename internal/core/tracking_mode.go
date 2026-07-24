package core

import (
	"github.com/BlackRaincoat/versentry/internal/imageweb"
)

// Why digest mode was chosen (empty when mode is semver or numeric).
const (
	digestCauseRule = "rule" // explicit track: digest
	digestCauseAuto = "auto" // tag is neither semver nor strict dotted numeric
)

// LinksModeDigestRule / LinksModeDigestAuto are MODE column values for versentry links.
const (
	LinksModeDigestRule = "digest(rule)"
	LinksModeDigestAuto = "digest(auto)"
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
func linksDisplayMode(mode, digestCause string) string {
	switch mode {
	case imageweb.ModeSemver:
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
	if e == nil || e.log == nil || mode != imageweb.ModeDigest {
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
}

func (e *Engine) warnOnce(key, msg string, args ...any) {
	if e == nil || e.log == nil {
		return
	}
	if _, loaded := e.warnOnceKeys.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	e.log.Warn(msg, args...)
}
