package core

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/logging"
	"github.com/BlackRaincoat/versentry/internal/model"
)

const bareImageDigest = "sha256:e0e67ea9c4bb0000000000000000000000000000000000000000000000000000"

func TestRunOnceBareDigestSkipsWithoutSemverWarn(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, slog.LevelDebug)
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return []model.Container{{
				Name:     "buildx_buildkit_builder-c1120f41-b5e3-4612-98e3-0e0635a209070",
				ImageRef: bareImageDigest,
			}}, nil
		}},
		nil,
		config.Timeouts{},
		log,
		nil,
		nil,
	)

	updates, keys, _, err := eng.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 0 {
		t.Fatalf("updates = %d, want 0", len(updates))
	}
	if len(keys) != 0 {
		t.Fatalf("active keys %v, want none (no image name to track)", keys)
	}

	out := buf.String()
	if strings.Contains(out, "tag is not semver") {
		t.Fatalf("bare digest must not emit digest-auto WARN, got %q", out)
	}
	if strings.Contains(out, "library/sha256") {
		t.Fatalf("must not invent library/sha256, got %q", out)
	}
	if strings.Contains(out, "WARN container skipped") {
		t.Fatalf("bare digest skip must not be WARN, got %q", out)
	}
	if !strings.Contains(out, "container runs from bare digest, no image ref to track") {
		t.Fatalf("expected DEBUG bare-digest skip, got %q", out)
	}
}

func TestRunOnceNamedDigestPinStillWarns(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, slog.LevelDebug)
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return []model.Container{{
				Name:     "pinned",
				ImageRef: "nginx@" + bareImageDigest,
			}}, nil
		}},
		nil,
		config.Timeouts{},
		log,
		nil,
		nil,
	)
	if _, _, _, err := eng.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "WARN container skipped") {
		t.Fatalf("named digest pin should WARN skip, got %q", out)
	}
	if !strings.Contains(out, "digest-only reference") {
		t.Fatalf("reason missing, got %q", out)
	}
	if strings.Contains(out, "container runs from bare digest") {
		t.Fatalf("named pin is not a bare digest, got %q", out)
	}
}
