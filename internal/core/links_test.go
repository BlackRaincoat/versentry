package core

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/imageweb"
	"github.com/BlackRaincoat/versentry/internal/model"
)

func TestResolveTrackingModeDigestRule(t *testing.T) {
	rules, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Mode: "digest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mode, _ := resolveTrackingMode(rules, nil, "index.docker.io", "valkey/valkey", "9-alpine", nil)
	if mode != imageweb.ModeDigest {
		t.Fatalf("mode = %q", mode)
	}
}

func TestResolveTrackingModeSemverParsable(t *testing.T) {
	mode, _ := resolveTrackingMode(nil, nil, "index.docker.io", "library/nginx", "1.25.0", nil)
	if mode != imageweb.ModeSemver {
		t.Fatalf("mode = %q", mode)
	}
}

func TestResolveTrackingModeNonSemver(t *testing.T) {
	mode, _ := resolveTrackingMode(nil, nil, "index.docker.io", "pgvector/pgvector", "pg17-trixie", nil)
	if mode != imageweb.ModeDigest {
		t.Fatalf("mode = %q", mode)
	}
}

func TestWriteLinksTable(t *testing.T) {
	rules, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Mode: "digest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return []model.Container{
				{
					Name:     "cache",
					ImageRef: "valkey/valkey:9-alpine",
					Labels: map[string]string{
						"org.opencontainers.image.source": "https://github.com/valkey-io/valkey",
					},
				},
				{
					Name:     "web",
					ImageRef: "nginx:1.25.3",
				},
				{
					Name:     "skip-me",
					ImageRef: "busybox:latest",
					Labels:   map[string]string{labelWatch: "false"},
				},
			}, nil
		}},
		nil,
		config.Timeouts{},
		slog.Default(),
		rules,
	)

	var buf bytes.Buffer
	if err := eng.WriteLinks(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "CONTAINER") || !strings.Contains(out, "MODE") {
		t.Fatalf("missing header: %q", out)
	}
	if !strings.Contains(out, "cache") || !strings.Contains(out, "digest") {
		t.Fatalf("expected valkey digest row: %q", out)
	}
	if !strings.Contains(out, "hub.docker.com/r/valkey/valkey?tag=9-alpine") {
		t.Fatalf("expected hub url for digest valkey: %q", out)
	}
	if !strings.Contains(out, "web") || !strings.Contains(out, "semver") {
		t.Fatalf("expected nginx semver row: %q", out)
	}
	if strings.Contains(out, "skip-me") {
		t.Fatalf("excluded container must not appear: %q", out)
	}
}

func TestLinkRowNoURL(t *testing.T) {
	eng := NewEngine(
		&stubProvider{},
		nil,
		config.Timeouts{},
		slog.Default(),
		nil,
	)
	row := linkRowFor(eng, model.Container{
		Name:     "app",
		ImageRef: "ghcr.io/org/app:1.0.0",
	})
	if row.Mode != imageweb.ModeSemver {
		t.Fatalf("mode = %q", row.Mode)
	}
	if row.URL != "(no url)" {
		t.Fatalf("url = %q", row.URL)
	}
}
