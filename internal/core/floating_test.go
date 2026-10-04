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

func TestFloatingLineExample(t *testing.T) {
	cases := []struct {
		name    string
		current string
		tags    []string
		want    string
	}{
		{"bare major picks newest line tag", "16", []string{"15.10", "16.1", "16.10", "16.9", "17.6", "16"}, "16.10"},
		{"bare major with suffix prefers same suffix", "7-alpine", []string{"7.4.2", "7.4.2-alpine", "7.2.5-alpine", "8.0.1-alpine"}, "7.4.2-alpine"},
		{"bare major falls back to other suffix", "7-alpine", []string{"7.4.2", "8.0.1"}, "7.4.2"},
		{"major.minor floating (nginx)", "1.27", []string{"1.27.0", "1.27.3", "1.28.0", "1.27"}, "1.27.3"},
		{"v-prefix normalized", "v2", []string{"v2.1.0", "2.3.0", "v3.0.0"}, "2.3.0"},
		{"pinned postgres 16.4 has no 16.4.x", "16.4", []string{"16.4", "16.5", "17.0", "16.4-alpine"}, ""},
		{"three components never floating", "1.27.3", []string{"1.27.3.1", "1.27.4"}, ""},
		{"prefix must match whole component", "1", []string{"10.0.0", "11.2"}, ""},
		{"no tags", "16", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := floatingLineExample(tc.current, tc.tags); got != tc.want {
				t.Fatalf("floatingLineExample(%q) = %q, want %q", tc.current, got, tc.want)
			}
		})
	}
}

func TestLinksDisplayModeMarksBareMajorOnly(t *testing.T) {
	cases := map[string]string{
		"16":       LinksModeSemverFloating,
		"7-alpine": LinksModeSemverFloating,
		"v2":       LinksModeSemverFloating,
		"1.27":     imageweb.ModeSemver, // needs registry evidence (vs pinned postgres 16.4)
		"16.4":     imageweb.ModeSemver,
		"1.27.3":   imageweb.ModeSemver,
	}
	for tag, want := range cases {
		if got := linksDisplayMode(imageweb.ModeSemver, "", tag); got != want {
			t.Fatalf("linksDisplayMode(semver, %q) = %q, want %q", tag, got, want)
		}
	}
	if got := linksDisplayMode(imageweb.ModeNumeric, "", "16"); got != imageweb.ModeNumeric {
		t.Fatalf("numeric must stay numeric, got %q", got)
	}
}

func TestLogFloatingTagHintOnceAtInfo(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	eng := NewEngine(&stubProvider{}, nil, config.Timeouts{}, log, nil, nil)

	tags := []string{"16.9", "16.10", "17.6"}
	eng.logFloatingTagHint("db", "library/postgres", "16", tags)
	eng.logFloatingTagHint("db", "library/postgres", "16", tags)

	out := buf.String()
	if strings.Count(out, "floating tag on semver path") != 1 {
		t.Fatalf("expected one INFO, got %q", out)
	}
	for _, want := range []string{"level=INFO", "track: digest", "container=db", "tag=16", "line_tag=16.10"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestLogFloatingTagHintSilentForPinned(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	eng := NewEngine(&stubProvider{}, nil, config.Timeouts{}, log, nil, nil)

	eng.logFloatingTagHint("db", "library/postgres", "16.4", []string{"16.4", "16.5", "17.0"})
	eng.logFloatingTagHint("web", "library/nginx", "1.27.3", []string{"1.27.3", "1.27.4"})
	if buf.Len() != 0 {
		t.Fatalf("pinned tags must not log, got %q", buf.String())
	}
}

func TestCheckContainerFloatingHintKeepsSemverResult(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	reg := &modeTestRegistry{
		host:     imageref.DockerHubHost,
		listTags: []string{"16", "16.9", "16.10", "17", "17.6"},
	}
	eng := NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, log, nil, nil)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "db", ImageRef: "postgres:16"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(log))
	if err != nil {
		t.Fatal(err)
	}
	// Detection unchanged: semver still offers the newest line, no digest call.
	if result.Status != statusUpdate || result.LatestTag != "17.6" || reg.digestCalls != 0 {
		t.Fatalf("status=%s latest=%q digestCalls=%d", result.Status, result.LatestTag, reg.digestCalls)
	}
	if !strings.Contains(buf.String(), "floating tag on semver path") || !strings.Contains(buf.String(), "line_tag=16.10") {
		t.Fatalf("expected floating hint, got %q", buf.String())
	}
}
