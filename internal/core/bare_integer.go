package core

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Bare integer tags (optional v, digits only — no dot). Masterminds coerces
// "8400" → 8400.0.0, so CI/build numbers can beat real versions after
// same-major was removed. They are excluded from semver/numeric *candidates*
// only; a container whose *current* tag is bare (postgres:16) stays on the
// semver path.
var bareIntegerTagRE = regexp.MustCompile(`^v?\d+$`)

const bareIntegerTagListLimit = 10

func isBareIntegerTag(tag string) bool {
	return bareIntegerTagRE.MatchString(strings.TrimSpace(tag))
}

func collectBareIntegerTags(tags []string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, raw := range tags {
		raw = strings.TrimSpace(raw)
		if !isBareIntegerTag(raw) {
			continue
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		out = append(out, raw)
	}
	return out
}

// collectBareIntegerTagsNewerThan returns bare integers that coerce to a version
// strictly greater than current — the only ones that "claimed" the update slot.
func collectBareIntegerTagsNewerThan(tags []string, current *semver.Version) []string {
	if current == nil {
		return nil
	}
	var out []string
	for _, raw := range collectBareIntegerTags(tags) {
		v, err := semver.NewVersion(raw)
		if err != nil {
			continue
		}
		if v.GreaterThan(current) {
			out = append(out, raw)
		}
	}
	return out
}

// bareIntegersAffectingSelection keeps claiming bares that would have beaten
// selected if allowed as candidates. Bares already covered by selected
// (e.g. bare 3 under selected 3.2.1) are cosmetic — omitting them did not
// change the result.
func bareIntegersAffectingSelection(claiming []string, selected *semver.Version) []string {
	if selected == nil {
		return claiming
	}
	var out []string
	for _, raw := range claiming {
		v, err := semver.NewVersion(raw)
		if err != nil {
			continue
		}
		if v.GreaterThan(selected) {
			out = append(out, raw)
		}
	}
	return out
}

// formatBareIntegerTagList joins tags for a log attr; long lists are truncated.
// Example: "7974,8392,8400" or "a,b,… (25 total)" when over limit.
func formatBareIntegerTagList(tags []string, limit int) string {
	if limit <= 0 {
		limit = bareIntegerTagListLimit
	}
	n := len(tags)
	if n == 0 {
		return ""
	}
	if n <= limit {
		return strings.Join(tags, ",")
	}
	return strings.Join(tags[:limit], ",") + ",… (" + strconv.Itoa(n) + " total)"
}

// adjacentBareMajor reports a skipped bare integer whose major is exactly
// current.Major+1 (e.g. current 16.9, bare 17 — floating next major with no 17.x).
func adjacentBareMajor(bares []string, current *semver.Version) bool {
	if current == nil {
		return false
	}
	want := current.Major() + 1
	for _, raw := range bares {
		v, err := semver.NewVersion(raw)
		if err != nil {
			continue
		}
		if v.Major() == want {
			return true
		}
	}
	return false
}

// logSkippedBareSemverCandidates logs only bare integers that formally claim a
// newer version than current (one collapsed line per image):
//   - DEBUG: omitted bare would have beaten selected (e.g. FreshRSS 8400 > 1.29.1)
//   - WARN once: next major exists only as bare integer (16.9 + 17, no 17.x)
// Silent when older/equal to current, or when selected already covers the bare
// (e.g. 3 under 3.2.1) — filter did not change the result.
func (e *Engine) logSkippedBareSemverCandidates(
	container, repo string,
	current, selected *semver.Version,
	tags []string,
) {
	if e == nil || e.log == nil || current == nil {
		return
	}
	claiming := collectBareIntegerTagsNewerThan(tags, current)
	if len(claiming) == 0 {
		return
	}

	hasNewerNonBare := selected != nil && selected.GreaterThan(current)
	selectedStr := ""
	if selected != nil {
		selectedStr = selected.Original()
	}

	// Degenerate: next major exists only as a bare integer; point releases absent.
	if !hasNewerNonBare && adjacentBareMajor(claiming, current) {
		e.warnOnce(
			"bare-integer-gap:"+repo,
			"skipped bare integer tags with no newer non-bare candidate",
			"count", len(claiming),
			"container", container,
			"image", repo,
			"tags", formatBareIntegerTagList(claiming, bareIntegerTagListLimit),
			"current", current.Original(),
			"selected", selectedStr,
		)
		return
	}

	affecting := bareIntegersAffectingSelection(claiming, selected)
	if len(affecting) == 0 || selected == nil || !e.log.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	e.log.Debug("omitted ambiguous newer bare-integer tag(s)",
		"count", len(affecting),
		"container", container,
		"image", repo,
		"tags", formatBareIntegerTagList(affecting, bareIntegerTagListLimit),
		"selected", selectedStr,
	)
}

// logSkippedBareNumericCandidates is the numeric-path counterpart.
func (e *Engine) logSkippedBareNumericCandidates(
	container, repo string,
	current, selected numericVersion,
	tags []string,
) {
	if e == nil || e.log == nil {
		return
	}
	curVer, err := semver.NewVersion(current.original)
	if err != nil {
		return
	}
	claiming := collectBareIntegerTagsNewerThan(tags, curVer)
	if len(claiming) == 0 {
		return
	}

	var selVer *semver.Version
	if selected.original != "" {
		selVer, _ = semver.NewVersion(selected.original)
	}

	hasNewerNonBare := selVer != nil && selVer.GreaterThan(curVer)
	selectedStr := selected.original

	if !hasNewerNonBare && adjacentBareMajor(claiming, curVer) {
		e.warnOnce(
			"bare-integer-gap:"+repo,
			"skipped bare integer tags with no newer non-bare candidate",
			"count", len(claiming),
			"container", container,
			"image", repo,
			"tags", formatBareIntegerTagList(claiming, bareIntegerTagListLimit),
			"current", current.original,
			"selected", selectedStr,
		)
		return
	}

	affecting := bareIntegersAffectingSelection(claiming, selVer)
	if len(affecting) == 0 || selected.original == "" || !e.log.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	e.log.Debug("omitted ambiguous newer bare-integer tag(s)",
		"count", len(affecting),
		"container", container,
		"image", repo,
		"tags", formatBareIntegerTagList(affecting, bareIntegerTagListLimit),
		"selected", selectedStr,
	)
}
