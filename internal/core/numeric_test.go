package core

import (
	"context"
	"log/slog"
	"regexp"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/imageweb"
	"github.com/BlackRaincoat/versentry/internal/model"
)

func TestParseNumericVersion(t *testing.T) {
	cases := []struct {
		tag  string
		ok   bool
		segs []int
	}{
		{"v0.63.1.3", true, []int{0, 63, 1, 3}},
		{"0.63.1.3", true, []int{0, 63, 1, 3}},
		{"1.2.3.4", true, []int{1, 2, 3, 4}},
		{"1.2", true, []int{1, 2}},
		{"17.10-alpine3.24", false, nil},
		{"9-alpine", false, nil},
		{"1.2.3.4-rc1", false, nil},
		{"1.2.3.4a", false, nil},
		{"v1.2.3", true, []int{1, 2, 3}}, // regex ok; runtime uses Masterminds first for 3-seg
	}
	for _, tc := range cases {
		v, ok := parseNumericVersion(tc.tag)
		if ok != tc.ok {
			t.Fatalf("%q: ok=%v want %v", tc.tag, ok, tc.ok)
		}
		if !ok {
			continue
		}
		if len(v.segments) != len(tc.segs) {
			t.Fatalf("%q: segs=%v want %v", tc.tag, v.segments, tc.segs)
		}
		for i := range tc.segs {
			if v.segments[i] != tc.segs[i] {
				t.Fatalf("%q: segs=%v want %v", tc.tag, v.segments, tc.segs)
			}
		}
	}
}

func TestCompareNumericPadsMissing(t *testing.T) {
	a, _ := parseNumericVersion("1.2.3")
	b, _ := parseNumericVersion("1.2.3.4")
	if compareNumeric(a, b) >= 0 {
		t.Fatal("1.2.3 should be less than 1.2.3.4 (pad with 0)")
	}
	if compareNumeric(b, a) <= 0 {
		t.Fatal("1.2.3.4 should be greater than 1.2.3")
	}
}

func TestSelectNumericTagNewestSameMajor(t *testing.T) {
	cur, ok := parseNumericVersion("0.63.1.3")
	if !ok {
		t.Fatal("parse current")
	}
	tag, latest, ok := selectNumericTag(cur, []string{"0.63.1.4", "0.63.1.5", "0.63.2.0", "1.0.0.0"})
	if !ok || tag != "0.63.2.0" {
		t.Fatalf("got %q %+v ok=%v, want 0.63.2.0", tag, latest, ok)
	}
}

func TestSelectNumericTagIncludeFilter(t *testing.T) {
	cur, _ := parseNumericVersion("0.63.1.3")
	re := regexp.MustCompile(`^0\.63\.1\.\d+$`)
	tags := filterTags([]string{"0.63.1.4", "0.63.1.5", "0.63.2.0"}, re)
	tag, _, ok := selectNumericTag(cur, tags)
	if !ok || tag != "0.63.1.5" {
		t.Fatalf("got %q ok=%v, want 0.63.1.5 after include", tag, ok)
	}
}

func TestPreferEqualDottedTagVPrefix(t *testing.T) {
	if !preferEqualDottedTag("v0.63.1.3", "v0.63.1.3", "0.63.1.3") {
		t.Fatal("should prefer matching v-prefix")
	}
}

func TestAlpineStillSemverNotNumeric(t *testing.T) {
	mode, _, _ := resolveTrackingMode(nil, "index.docker.io", "library/postgres", "17.10-alpine3.24", "db", nil)
	if mode != imageweb.ModeSemver {
		t.Fatalf("mode=%q, want semver for alpine suffix tag", mode)
	}
}

func TestFloatingSuffixStillDigestAuto(t *testing.T) {
	mode, _, cause := resolveTrackingMode(nil, "index.docker.io", "pgvector/pgvector", "pg17-trixie", "db", nil)
	if mode != imageweb.ModeDigest || cause != digestCauseAuto {
		t.Fatalf("mode=%q cause=%q", mode, cause)
	}
}

func TestLetterSuffixNotNumeric(t *testing.T) {
	if _, ok := parseNumericVersion("1.2.3.4a"); ok {
		t.Fatal("1.2.3.4a must not be numeric")
	}
	if _, ok := parseNumericVersion("1.2.3.4-rc1"); ok {
		t.Fatal("1.2.3.4-rc1 must not be numeric")
	}
	mode, _, cause := resolveTrackingMode(nil, "index.docker.io", "example/app", "1.2.3.4-rc1", "app", nil)
	if mode != imageweb.ModeDigest || cause != digestCauseAuto {
		t.Fatalf("mode=%q cause=%q for 1.2.3.4-rc1", mode, cause)
	}
}

func TestCheckContainerNumericUpdate(t *testing.T) {
	reg := &modeTestRegistry{
		host:     imageref.DockerHubHost,
		listTags: []string{"0.63.1.4", "0.63.1.5", "0.63.2.0", "v0.63.1.3"},
	}
	eng := NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, slog.Default(), nil, nil)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "metabase", ImageRef: "metabase/metabase:v0.63.1.3"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(slog.Default()))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != statusUpdate || result.LatestTag != "0.63.2.0" {
		t.Fatalf("status=%s latest=%q", result.Status, result.LatestTag)
	}
	if reg.digestCalls != 0 {
		t.Fatalf("numeric path must not call TagDigest, got %d", reg.digestCalls)
	}
}
