package core

import (
	"regexp"
	"strconv"
	"strings"
)

// Strict dotted numeric tags: optional v, then digits and dots only (no suffix).
// Used only when Masterminds semver rejects the tag (typically 4+ segments).
var numericTagRE = regexp.MustCompile(`^v?\d+(\.\d+)+$`)

// numericVersion is a strictly numeric dotted version (e.g. v0.63.1.3 → [0,63,1,3]).
type numericVersion struct {
	segments []int
	original string
}

func (v numericVersion) major() int {
	if len(v.segments) == 0 {
		return 0
	}
	return v.segments[0]
}

// parseNumericVersion accepts only ^v?\d+(\.\d+)+$.
func parseNumericVersion(tag string) (numericVersion, bool) {
	raw := strings.TrimSpace(tag)
	if !numericTagRE.MatchString(raw) {
		return numericVersion{}, false
	}
	s := raw
	if s[0] == 'v' {
		s = s[1:]
	}
	parts := strings.Split(s, ".")
	segs := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return numericVersion{}, false
		}
		segs[i] = n
	}
	return numericVersion{segments: segs, original: raw}, true
}

// compareNumeric compares dotted versions; missing trailing segments count as 0.
// Returns -1 if a < b, 0 if equal, +1 if a > b.
func compareNumeric(a, b numericVersion) int {
	n := len(a.segments)
	if len(b.segments) > n {
		n = len(b.segments)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(a.segments) {
			av = a.segments[i]
		}
		if i < len(b.segments) {
			bv = b.segments[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// selectNumericTag picks the newest same-major numeric tag (missing segments = 0).
func selectNumericTag(current numericVersion, tags []string) (string, numericVersion, bool) {
	var best numericVersion
	var bestTag string
	found := false
	currentRaw := current.original

	for _, raw := range tags {
		v, ok := parseNumericVersion(raw)
		if !ok {
			continue
		}
		if v.major() != current.major() {
			continue
		}
		if !found {
			best = v
			bestTag = raw
			found = true
			continue
		}
		cmp := compareNumeric(v, best)
		if cmp > 0 || (cmp == 0 && preferEqualDottedTag(currentRaw, raw, bestTag)) {
			best = v
			bestTag = raw
		}
	}
	if !found {
		return "", numericVersion{}, false
	}
	return bestTag, best, true
}

// preferEqualDottedTag chooses between equal numeric versions.
// Same principle as preferEqualSemverTag: prefer the form of the current tag
// (v-prefix, then component count), then more specific / longer / lex smaller.
func preferEqualDottedTag(current, a, b string) bool {
	if decisive, preferA := preferMatchingVPrefix(current, a, b); decisive {
		return preferA
	}
	curN, aN, bN := dottedComponentCount(current), dottedComponentCount(a), dottedComponentCount(b)
	if (aN == curN) != (bN == curN) {
		return aN == curN
	}
	if aN != bN {
		return aN > bN
	}
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a < b
}

func dottedComponentCount(raw string) int {
	v, ok := parseNumericVersion(raw)
	if !ok {
		return 0
	}
	return len(v.segments)
}
