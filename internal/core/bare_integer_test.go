package core

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/imageweb"
	"github.com/BlackRaincoat/versentry/internal/model"
)

func TestIsBareIntegerTag(t *testing.T) {
	for _, tag := range []string{"8400", "v2102", "16", "7", "0"} {
		if !isBareIntegerTag(tag) {
			t.Fatalf("%q should be bare integer", tag)
		}
	}
	for _, tag := range []string{"8.3", "1.29.1", "17.6", "16.9", "9-alpine", "v1.2.3"} {
		if isBareIntegerTag(tag) {
			t.Fatalf("%q must not be bare integer", tag)
		}
	}
}

func TestFormatBareIntegerTagListTruncates(t *testing.T) {
	tags := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}
	got := formatBareIntegerTagList(tags, 10)
	if !strings.HasPrefix(got, "1,2,3,4,5,6,7,8,9,10,…") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "(12 total)") {
		t.Fatalf("want total count, got %q", got)
	}
	short := formatBareIntegerTagList([]string{"8400", "8392"}, 10)
	if short != "8400,8392" {
		t.Fatalf("got %q", short)
	}
}

func TestCollectBareIntegerTagsNewerThan(t *testing.T) {
	cur := mustVer(t, "1.29.1")
	got := collectBareIntegerTagsNewerThan([]string{"1", "7974", "8392", "8400", "1.29.1"}, cur)
	if len(got) != 3 || got[0] != "7974" || got[1] != "8392" || got[2] != "8400" {
		t.Fatalf("got %v, want [7974 8392 8400] (not 1)", got)
	}
	cur34 := mustVer(t, "34.0.2")
	if n := collectBareIntegerTagsNewerThan([]string{"10", "11", "19", "34"}, cur34); len(n) != 0 {
		t.Fatalf("older/equal majors must be empty, got %v", n)
	}
	cur439 := mustVer(t, "4.39.20")
	if n := collectBareIntegerTagsNewerThan([]string{"4"}, cur439); len(n) != 0 {
		t.Fatalf("bare 4 is not > 4.39.20, got %v", n)
	}
}

func TestBareIntegersAffectingSelection(t *testing.T) {
	sel := mustVer(t, "3.2.1")
	got := bareIntegersAffectingSelection([]string{"3", "8400"}, sel)
	if len(got) != 1 || got[0] != "8400" {
		t.Fatalf("got %v, want [8400] (3 covered by 3.2.1)", got)
	}
	if n := bareIntegersAffectingSelection([]string{"3"}, sel); len(n) != 0 {
		t.Fatalf("bare 3 under 3.2.1 must be empty, got %v", n)
	}
	sel129 := mustVer(t, "1.29.1")
	got = bareIntegersAffectingSelection([]string{"7974", "8400"}, sel129)
	if len(got) != 2 {
		t.Fatalf("CI bares beat 1.29.1, got %v", got)
	}
}

func TestSelectSemverSkipsBareIntegerCINoise(t *testing.T) {
	current := mustVer(t, "1.29.1")
	got, _, ok := DefaultTagSelector{}.Select(current, []string{
		"1.28.0", "1.29.1", "1.29.2", "8400", "8392", "latest",
	})
	if !ok || got != "1.29.2" {
		t.Fatalf("got %q ok=%v, want 1.29.2 (8400/8392 excluded)", got, ok)
	}
}

func TestSelectSemverPostgresBareMajorCurrentKeepsPointRelease(t *testing.T) {
	current := mustVer(t, "16")
	got, ver, ok := DefaultTagSelector{}.Select(current, []string{"16.9", "17", "17.6", "15.10"})
	if !ok || got != "17.6" {
		t.Fatalf("got %q ok=%v, want 17.6", got, ok)
	}
	if bump := semverBump(current, ver); bump != model.BumpMajor {
		t.Fatalf("bump=%q, want major", bump)
	}
}

func TestSelectSemverTwoSegmentStillPasses(t *testing.T) {
	current := mustVer(t, "8.2.2")
	got, _, ok := DefaultTagSelector{}.Select(current, []string{"8.2.2", "8.3", "8.2.1"})
	if !ok || got != "8.3" {
		t.Fatalf("got %q ok=%v, want 8.3", got, ok)
	}
}

func TestCurrentBareIntegerStaysSemverMode(t *testing.T) {
	mode, _, cause := resolveTrackingMode(nil, "index.docker.io", "library/postgres", "16", "db", nil)
	if mode != imageweb.ModeSemver || cause != "" {
		t.Fatalf("mode=%q cause=%q, want semver for current=16", mode, cause)
	}
}

func TestBareIntegerDebugOnlyClaimingNewer(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reg := &modeTestRegistry{
		host: imageref.DockerHubHost,
		listTags: []string{
			"1.29.1", "1", "7974", "8392", "8400",
		},
	}
	eng := NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, log, nil, nil)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "freshrss", ImageRef: "freshrss/freshrss:1.29.1"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(log))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != statusUpToDate || result.LatestTag != "1.29.1" {
		t.Fatalf("status=%s latest=%q", result.Status, result.LatestTag)
	}
	out := buf.String()
	if strings.Contains(out, "level=WARN") {
		t.Fatalf("must not WARN, got %q", out)
	}
	if strings.Count(out, "omitted ambiguous newer bare-integer tag") != 1 {
		t.Fatalf("want one DEBUG line, got %q", out)
	}
	if strings.Contains(out, "tags=") && strings.Contains(out, ",1,") || strings.Contains(out, "tags=1,") || strings.Contains(out, ",1\"") {
		t.Fatalf("must not log older bare 1, got %q", out)
	}
	if !strings.Contains(out, "7974") || !strings.Contains(out, "8400") {
		t.Fatalf("want claiming CI tags, got %q", out)
	}
	if !strings.Contains(out, "selected=1.29.1") {
		t.Fatalf("want selected, got %q", out)
	}
}

func TestBareIntegerOlderOrEqualSilent(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cases := []struct {
		name, image string
		tags        []string
		wantLatest  string
	}{
		{"nextcloud", "nextcloud:34.0.2", []string{"10", "11", "19", "34", "34.0.2"}, "34.0.2"},
		{"authelia", "authelia/authelia:4.39.20", []string{"4", "4.39.20"}, "4.39.20"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			reg := &modeTestRegistry{host: imageref.DockerHubHost, listTags: tc.tags}
			eng := NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, log, nil, nil)
			eng.registries = append(eng.registries, reg)
			c := model.Container{Name: tc.name, ImageRef: tc.image}
			result, err := eng.checkContainer(context.Background(), c, registrypass.New(log))
			if err != nil {
				t.Fatal(err)
			}
			if result.LatestTag != tc.wantLatest {
				t.Fatalf("latest=%q, want %q", result.LatestTag, tc.wantLatest)
			}
			if strings.Contains(buf.String(), "bare-integer") || strings.Contains(buf.String(), "omitted ambiguous") {
				t.Fatalf("older/equal bares must be silent, got %q", buf.String())
			}
		})
	}
}

func TestBareIntegerDegenerateOnlyBareNewerWarns(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	reg := &modeTestRegistry{
		host:     imageref.DockerHubHost,
		listTags: []string{"16.9", "17"},
	}
	eng := NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, log, nil, nil)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "db", ImageRef: "postgres:16.9"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(log))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != statusUpToDate || result.LatestTag != "16.9" {
		t.Fatalf("status=%s latest=%q", result.Status, result.LatestTag)
	}
	out := buf.String()
	if !strings.Contains(out, "no newer non-bare candidate") {
		t.Fatalf("expected degenerate WARN, got %q", out)
	}
	if !strings.Contains(out, "tags=17") {
		t.Fatalf("want tags=17, got %q", out)
	}
}

func TestBareIntegerNoWarnWhenPointReleaseCoversMajor(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	reg := &modeTestRegistry{
		host:     imageref.DockerHubHost,
		listTags: []string{"16.9", "17", "17.6"},
	}
	eng := NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, log, nil, nil)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "db", ImageRef: "postgres:16"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(log))
	if err != nil {
		t.Fatal(err)
	}
	if result.LatestTag != "17.6" {
		t.Fatalf("latest=%q, want 17.6", result.LatestTag)
	}
	if strings.Contains(buf.String(), "non-bare candidate") {
		t.Fatalf("17.6 covers major — must not WARN, got %q", buf.String())
	}
}

func TestBareIntegerDebugSilentWhenSelectedCoversBare(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reg := &modeTestRegistry{
		host:     imageref.DockerHubHost,
		listTags: []string{"2.8.1", "3", "3.2.1"},
	}
	eng := NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, log, nil, nil)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "panel", ImageRef: "example/app:2.8.1"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(log))
	if err != nil {
		t.Fatal(err)
	}
	if result.LatestTag != "3.2.1" {
		t.Fatalf("latest=%q, want 3.2.1", result.LatestTag)
	}
	out := buf.String()
	if strings.Contains(out, "omitted ambiguous") || strings.Contains(out, "bare-integer") {
		t.Fatalf("bare 3 covered by 3.2.1 — must be silent, got %q", out)
	}
}

func TestBareIntegerOnlyBareNextMajorStillWarns(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	reg := &modeTestRegistry{
		host:     imageref.DockerHubHost,
		listTags: []string{"2.8.1", "3"},
	}
	eng := NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, log, nil, nil)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "panel", ImageRef: "example/app:2.8.1"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(log))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != statusUpToDate || result.LatestTag != "2.8.1" {
		t.Fatalf("status=%s latest=%q", result.Status, result.LatestTag)
	}
	out := buf.String()
	if !strings.Contains(out, "no newer non-bare candidate") {
		t.Fatalf("bare 3 alone must WARN, got %q", out)
	}
	if !strings.Contains(out, "tags=3") {
		t.Fatalf("want tags=3, got %q", out)
	}
}

func TestNumericUnaffectedByBareFilter(t *testing.T) {
	cur, _ := parseNumericVersion("0.63.1.3")
	tag, _, ok := selectNumericTag(cur, []string{"0.63.1.4", "8400", "0.63.2.0"})
	if !ok || tag != "0.63.2.0" {
		t.Fatalf("got %q ok=%v, want 0.63.2.0", tag, ok)
	}
}
