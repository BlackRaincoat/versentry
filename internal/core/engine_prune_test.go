package core

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/model"
)

type stubProvider struct {
	listFn func(ctx context.Context) ([]model.Container, error)
}

func (p *stubProvider) ListRunning(ctx context.Context) ([]model.Container, error) {
	if p.listFn != nil {
		return p.listFn(ctx)
	}
	return nil, nil
}

func (p *stubProvider) LocalDigest(ctx context.Context, c model.Container, repo string) (string, error) {
	return "", nil
}

func (p *stubProvider) Ping(ctx context.Context) error {
	return nil
}

func TestRunOnceEmptyFleetSkipsPrune(t *testing.T) {
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return nil, nil
		}},
		nil,
		config.Timeouts{},
		slog.Default(),
		nil,
	)

	_, _, canPrune, err := eng.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if canPrune {
		t.Fatal("expected canPrune=false for empty fleet")
	}
}

func TestRunOnceListErrorReturnsNoPrune(t *testing.T) {
	listErr := errors.New("docker.sock unavailable")
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return nil, listErr
		}},
		nil,
		config.Timeouts{},
		slog.Default(),
		nil,
	)

	_, _, canPrune, err := eng.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if canPrune {
		t.Fatal("expected canPrune=false on list error")
	}
}

func TestRunOnceActiveKeysDedupByImage(t *testing.T) {
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return []model.Container{
				{Name: "chatwoot", ImageRef: "chatwoot/chatwoot:v3.24.1-ce"},
				{Name: "sidekiq", ImageRef: "chatwoot/chatwoot:v3.24.1-ce"},
			}, nil
		}},
		nil,
		config.Timeouts{},
		slog.Default(),
		nil,
	)

	_, activeKeys, canPrune, err := eng.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !canPrune {
		t.Fatal("expected canPrune=true")
	}
	if len(activeKeys) != 1 {
		t.Fatalf("activeKeys = %v, want one host/repo key", activeKeys)
	}
	if activeKeys[0] != "index.docker.io/chatwoot/chatwoot" {
		t.Fatalf("activeKeys[0] = %q", activeKeys[0])
	}
}

func TestRunOnceOneOfTwoSameImageStillActive(t *testing.T) {
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return []model.Container{
				{Name: "sidekiq", ImageRef: "chatwoot/chatwoot:v3.24.1-ce"},
			}, nil
		}},
		nil,
		config.Timeouts{},
		slog.Default(),
		nil,
	)

	_, activeKeys, canPrune, err := eng.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !canPrune || len(activeKeys) != 1 {
		t.Fatalf("canPrune=%v activeKeys=%v", canPrune, activeKeys)
	}
}

func TestRunOnceExcludedNotInActiveKeys(t *testing.T) {
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return []model.Container{
				{Name: "app", ImageRef: "nginx:latest", Labels: map[string]string{labelWatch: "false"}},
				{Name: "db", ImageRef: "postgres:17", Labels: map[string]string{}},
			}, nil
		}},
		nil,
		config.Timeouts{},
		slog.Default(),
		nil,
	)

	_, activeKeys, canPrune, err := eng.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !canPrune {
		t.Fatal("expected canPrune=true when containers exist")
	}
	if len(activeKeys) != 1 || activeKeys[0] != "index.docker.io/library/postgres" {
		t.Fatalf("activeKeys = %v", activeKeys)
	}
}

func TestRunOnceTagChangeKeepsImageKey(t *testing.T) {
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return []model.Container{
				{Name: "app", ImageRef: "chatwoot/chatwoot:v3.24.2-ce"},
			}, nil
		}},
		nil,
		config.Timeouts{},
		slog.Default(),
		nil,
	)

	_, activeKeys, _, err := eng.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(activeKeys) != 1 || activeKeys[0] != "index.docker.io/chatwoot/chatwoot" {
		t.Fatalf("activeKeys = %v", activeKeys)
	}
}
