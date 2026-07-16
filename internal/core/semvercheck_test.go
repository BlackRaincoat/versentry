package core

import (
	"testing"

	"github.com/Masterminds/semver/v3"
)

func mustVer(t *testing.T, tag string) *semver.Version {
	t.Helper()
	v, err := semver.NewVersion(tag)
	if err != nil {
		t.Fatalf("NewVersion(%q): %v", tag, err)
	}
	return v
}

func TestDefaultTagSelector_PreferFullSemverOverFloatingMinor(t *testing.T) {
	current := mustVer(t, "8.2.2")
	tags := []string{"8.3", "8.3.0", "8", "latest"}
	got, ver, ok := DefaultTagSelector{}.Select(current, tags)
	if !ok {
		t.Fatal("expected a selection")
	}
	if got != "8.3.0" {
		t.Fatalf("got tag %q, want 8.3.0", got)
	}
	if !ver.Equal(mustVer(t, "8.3.0")) {
		t.Fatalf("got version %s, want 8.3.0", ver)
	}

	// Opposite registry order must yield the same tag.
	rev := []string{"latest", "8", "8.3.0", "8.3"}
	got2, _, ok := DefaultTagSelector{}.Select(current, rev)
	if !ok || got2 != "8.3.0" {
		t.Fatalf("reversed order: got %q ok=%v, want 8.3.0", got2, ok)
	}
}

func TestDefaultTagSelector_PreferTwoComponentWhenCurrentIsTwoComponent(t *testing.T) {
	current := mustVer(t, "8.3")
	for _, tags := range [][]string{
		{"8.4.0", "8.4"},
		{"8.4", "8.4.0"},
	} {
		got, _, ok := DefaultTagSelector{}.Select(current, tags)
		if !ok || got != "8.4" {
			t.Fatalf("tags %v: got %q ok=%v, want 8.4", tags, got, ok)
		}
	}
}

func TestDefaultTagSelector_PreferMatchingVPrefix(t *testing.T) {
	current := mustVer(t, "v0.107.77")
	for _, tags := range [][]string{
		{"v0.107.78", "0.107.78"},
		{"0.107.78", "v0.107.78"},
	} {
		got, _, ok := DefaultTagSelector{}.Select(current, tags)
		if !ok || got != "v0.107.78" {
			t.Fatalf("tags %v: got %q ok=%v, want v0.107.78", tags, got, ok)
		}
	}
}

func TestPreferEqualSemverTag_VPrefixBeatsComponentCount(t *testing.T) {
	// Priority list: v-prefix match wins over component-count match.
	// current v1.2.3; a=v1.3 (v match, 2 comps); b=1.3.0 (no v, 3 comps).
	if !preferEqualSemverTag("v1.2.3", "v1.3", "1.3.0") {
		t.Fatal("expected v1.3 over 1.3.0 when current is v1.2.3")
	}
	if preferEqualSemverTag("v1.2.3", "1.3.0", "v1.3") {
		t.Fatal("expected 1.3.0 not preferred over v1.3 when current is v1.2.3")
	}

	current := mustVer(t, "v1.2.3")
	for _, tags := range [][]string{
		{"v1.3", "1.3.0"},
		{"1.3.0", "v1.3"},
	} {
		got, _, ok := DefaultTagSelector{}.Select(current, tags)
		if !ok || got != "v1.3" {
			t.Fatalf("tags %v: got %q ok=%v, want v1.3 (v-prefix priority)", tags, got, ok)
		}
	}
}

func TestDefaultTagSelector_StillPicksStrictlyNewer(t *testing.T) {
	current := mustVer(t, "8.2.2")
	got, _, ok := DefaultTagSelector{}.Select(current, []string{"8.2.2", "8.2.1", "7.9.0"})
	if !ok || got != "8.2.2" {
		t.Fatalf("got %q ok=%v, want current 8.2.2 as best same-major", got, ok)
	}
}

func TestRuleTagSelector_KeepsPrereleaseAndAppliesTieBreak(t *testing.T) {
	current := mustVer(t, "1.2.3-alpine")
	tags := []string{"1.3-alpine", "1.3.0-alpine"}
	got, _, ok := RuleTagSelector{}.Select(current, tags)
	if !ok || got != "1.3.0-alpine" {
		t.Fatalf("got %q ok=%v, want 1.3.0-alpine (3 components + suffix match)", got, ok)
	}
}

func TestParseTagForm(t *testing.T) {
	cases := []struct {
		raw        string
		hasV       bool
		components int
		suffix     string
	}{
		{"8.2.2", false, 3, ""},
		{"8.3", false, 2, ""},
		{"v0.107.77", true, 3, ""},
		{"17.10-alpine3.24", false, 2, "alpine3.24"},
	}
	for _, tc := range cases {
		f := parseTagForm(tc.raw)
		if f.hasV != tc.hasV || f.components != tc.components || f.suffix != tc.suffix {
			t.Fatalf("%q: got %+v, want hasV=%v components=%d suffix=%q",
				tc.raw, f, tc.hasV, tc.components, tc.suffix)
		}
	}
}
