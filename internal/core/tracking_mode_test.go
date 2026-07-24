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

func TestResolveTrackingModeDigestRule(t *testing.T) {
	rules, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Mode: "digest"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mode, _, cause := resolveTrackingMode(rules, "index.docker.io", "valkey/valkey", "9-alpine", "cache", nil)
	if mode != imageweb.ModeDigest || cause != digestCauseRule {
		t.Fatalf("mode=%q cause=%q", mode, cause)
	}
	if linksDisplayMode(mode, cause) != LinksModeDigestRule {
		t.Fatalf("display = %q", linksDisplayMode(mode, cause))
	}
}

func TestResolveTrackingModeSemverParsable(t *testing.T) {
	mode, _, cause := resolveTrackingMode(nil, "index.docker.io", "library/nginx", "1.25.0", "web", nil)
	if mode != imageweb.ModeSemver || cause != "" {
		t.Fatalf("mode=%q cause=%q", mode, cause)
	}
}

func TestResolveTrackingModeNonSemverAuto(t *testing.T) {
	mode, _, cause := resolveTrackingMode(nil, "index.docker.io", "pgvector/pgvector", "pg17-trixie", "db", nil)
	if mode != imageweb.ModeDigest || cause != digestCauseAuto {
		t.Fatalf("mode=%q cause=%q", mode, cause)
	}
	if linksDisplayMode(mode, cause) != LinksModeDigestAuto {
		t.Fatalf("display = %q", linksDisplayMode(mode, cause))
	}
}

func TestResolveTrackingModeFourSegmentTagIsNumeric(t *testing.T) {
	mode, _, cause := resolveTrackingMode(nil, "index.docker.io", "metabase/metabase", "v0.63.1.3", "metabase", nil)
	if mode != imageweb.ModeNumeric || cause != "" {
		t.Fatalf("mode=%q cause=%q, want numeric", mode, cause)
	}
}

func TestLogTrackingDiagnosticsDigestAutoOnce(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	eng := NewEngine(&stubProvider{}, nil, config.Timeouts{}, log, nil, nil)

	eng.logTrackingDiagnostics("metabase", "metabase/metabase", "v0.63.1.3", imageweb.ModeDigest, digestCauseAuto, nil)
	eng.logTrackingDiagnostics("metabase", "metabase/metabase", "v0.63.1.3", imageweb.ModeDigest, digestCauseAuto, nil)

	out := buf.String()
	if !strings.Contains(out, "tag is not semver; tracking by digest") {
		t.Fatalf("expected fallback WARN, got %q", out)
	}
	if strings.Count(out, "tag is not semver; tracking by digest") != 1 {
		t.Fatalf("expected one-time WARN, got %q", out)
	}
	if !strings.Contains(out, "newer version tags will not be detected") {
		t.Fatalf("expected clear no-new-versions wording, got %q", out)
	}
}

func TestLogTrackingDiagnosticsLatestSkipsDigestAutoWarn(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	eng := NewEngine(&stubProvider{}, nil, config.Timeouts{}, log, nil, nil)

	mode, _, cause := resolveTrackingMode(nil, "index.docker.io", "library/nginx", "latest", "web", nil)
	if mode != imageweb.ModeDigest || cause != digestCauseAuto {
		t.Fatalf("mode=%q cause=%q", mode, cause)
	}
	if linksDisplayMode(mode, cause) != LinksModeDigestAuto {
		t.Fatalf("links MODE = %q, want digest(auto)", linksDisplayMode(mode, cause))
	}

	eng.logTrackingDiagnostics("web", "library/nginx", "latest", mode, cause, nil)
	if out := buf.String(); strings.Contains(out, "tag is not semver; tracking by digest") {
		t.Fatalf("latest must not emit digest-auto WARN, got %q", out)
	}
}

func TestLogTrackingDiagnosticsIncludeIgnoredOnAutoDigest(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	rules, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "pgvector/pgvector", Include: "^pg17-trixie$"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(&stubProvider{}, nil, config.Timeouts{}, log, rules, nil)

	mode, rule, cause := resolveTrackingMode(rules, "index.docker.io", "pgvector/pgvector", "pg17-trixie", "db", nil)
	eng.logTrackingDiagnostics("db", "pgvector/pgvector", "pg17-trixie", mode, cause, rule)
	eng.logTrackingDiagnostics("db", "pgvector/pgvector", "pg17-trixie", mode, cause, rule)

	out := buf.String()
	if !strings.Contains(out, "include rule ignored") {
		t.Fatalf("expected include-ignored WARN, got %q", out)
	}
	if !strings.Contains(out, "digest=auto") {
		t.Fatalf("expected digest=auto attr, got %q", out)
	}
	if strings.Count(out, "include rule ignored") != 1 {
		t.Fatalf("expected one-time include WARN, got %q", out)
	}
}

func TestCheckContainerDigestAutoWarns(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	reg := &modeTestRegistry{
		host:         imageref.DockerHubHost,
		remoteDigest: "sha256:same",
	}
	eng := NewEngine(
		&modeTestProvider{localDigest: "sha256:same"},
		nil,
		config.Timeouts{},
		log,
		nil,
		nil,
	)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "db", ImageRef: "pgvector/pgvector:pg17-trixie"}
	if _, err := eng.checkContainer(context.Background(), c, registrypass.New(log)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "tag is not semver; tracking by digest") {
		t.Fatalf("expected fallback WARN on check, got %q", buf.String())
	}
}
