package core

import (
	"strings"

	"github.com/Masterminds/semver/v3"
)

// TagSelector chooses the best candidate tag from a registry tag list.
// The default implementation applies same-major, stable-only selection.
// Per-image rules can replace this later without changing the engine.
type TagSelector interface {
	Select(current *semver.Version, tags []string) (tag string, version *semver.Version, ok bool)
}

// DefaultTagSelector picks the newest stable tag within the current major version.
type DefaultTagSelector struct{}

// Select implements the default tag selection strategy.
func (DefaultTagSelector) Select(current *semver.Version, tags []string) (string, *semver.Version, bool) {
	return selectSemverTag(current, tags, true)
}

// RuleTagSelector picks the newest semver tag within the current major version.
// Unlike DefaultTagSelector, it does not drop prerelease/suffix tags: after an
// include filter, suffixes are part of the valid tag line (e.g. 17.10-alpine3.24).
// Same-major is kept for softer include patterns that do not pin the major.
type RuleTagSelector struct{}

// Select implements tag selection for include-filtered tag sets.
func (RuleTagSelector) Select(current *semver.Version, tags []string) (string, *semver.Version, bool) {
	return selectSemverTag(current, tags, false)
}

func selectSemverTag(current *semver.Version, tags []string, skipPrerelease bool) (string, *semver.Version, bool) {
	var best *semver.Version
	var bestTag string
	currentRaw := current.Original()

	for _, raw := range tags {
		v, err := semver.NewVersion(raw)
		if err != nil {
			continue
		}
		if skipPrerelease && v.Prerelease() != "" {
			continue
		}
		if v.Major() != current.Major() {
			continue
		}
		if best == nil || v.GreaterThan(best) || (v.Equal(best) && preferEqualSemverTag(currentRaw, raw, bestTag)) {
			best = v
			bestTag = raw
		}
	}

	if best == nil {
		return "", nil, false
	}
	return bestTag, best, true
}

// tagForm describes the pinning shape of a registry tag string.
type tagForm struct {
	hasV       bool
	components int
	suffix     string
}

func parseTagForm(raw string) tagForm {
	v, err := semver.NewVersion(raw)
	if err != nil {
		return tagForm{}
	}
	s := strings.TrimSpace(raw)
	hasV := hasVPrefix(s)
	if hasV {
		s = s[1:]
	}
	core := s
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	components := 0
	for _, p := range strings.Split(core, ".") {
		if p != "" {
			components++
		}
	}
	return tagForm{
		hasV:       hasV,
		components: components,
		suffix:     v.Prerelease(),
	}
}

// hasVPrefix reports a leading lowercase "v" (Masterminds / dotted numeric convention).
func hasVPrefix(raw string) bool {
	s := strings.TrimSpace(raw)
	return len(s) > 0 && s[0] == 'v'
}

// preferMatchingVPrefix is the first tie-break shared by semver and dotted numeric:
// prefer the candidate whose v-prefix presence matches the current tag.
func preferMatchingVPrefix(current, a, b string) (decisive bool, preferA bool) {
	cur, fa, fb := hasVPrefix(current), hasVPrefix(a), hasVPrefix(b)
	if (fa == cur) != (fb == cur) {
		return true, fa == cur
	}
	return false, false
}

// preferEqualSemverTag reports whether candidate a is a better pick than b when
// both parse to the same semver version.
//
// Form match uses a priority list (not a sum of matches):
//  1. same v/V prefix presence as current
//  2. same numeric component count as current (8.2.2 → 3, 8.3 → 2)
//  3. same prerelease/suffix as current
//
// Rationale: v-prefix is an intentional pinning convention (e.g. Traefik); a
// sum score could let a non-v three-part tag beat a two-part v-tag when the
// container uses v*. After form, prefer a more specific tag, then longer, then
// lexicographically smaller — all independent of ListTags order.
func preferEqualSemverTag(current, a, b string) bool {
	if decisive, preferA := preferMatchingVPrefix(current, a, b); decisive {
		return preferA
	}

	cur := parseTagForm(current)
	fa := parseTagForm(a)
	fb := parseTagForm(b)

	if (fa.components == cur.components) != (fb.components == cur.components) {
		return fa.components == cur.components
	}
	if (fa.suffix == cur.suffix) != (fb.suffix == cur.suffix) {
		return fa.suffix == cur.suffix
	}

	if fa.components != fb.components {
		return fa.components > fb.components
	}
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a < b
}

// parseContainerSemver parses a container image tag as semver.
func parseContainerSemver(tag string) (*semver.Version, error) {
	return semver.NewVersion(tag)
}
