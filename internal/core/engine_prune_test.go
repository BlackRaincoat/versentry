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

func TestRunOnceActiveKeysPerContainer(t *testing.T) {
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
	if len(activeKeys) != 2 {
		t.Fatalf("activeKeys = %v, want two container keys", activeKeys)
	}
	want := map[string]bool{
		"chatwoot|index.docker.io/chatwoot/chatwoot": true,
		"sidekiq|index.docker.io/chatwoot/chatwoot":  true,
	}
	for _, k := range activeKeys {
		if !want[k] {
			t.Fatalf("unexpected active key %q in %v", k, activeKeys)
		}
	}
}

func TestRunOnceSameImageDifferentLinesSeparateKeys(t *testing.T) {
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return []model.Container{
				{Name: "umami_db", ImageRef: "postgres:16-alpine"},
				{Name: "remnawave-db", ImageRef: "postgres:17"},
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
	if !canPrune || len(activeKeys) != 2 {
		t.Fatalf("canPrune=%v activeKeys=%v", canPrune, activeKeys)
	}
	want := map[string]bool{
		"umami_db|index.docker.io/library/postgres":     true,
		"remnawave-db|index.docker.io/library/postgres": true,
	}
	for _, k := range activeKeys {
		if !want[k] {
			t.Fatalf("unexpected active key %q in %v", k, activeKeys)
		}
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
	if activeKeys[0] != "sidekiq|index.docker.io/chatwoot/chatwoot" {
		t.Fatalf("activeKeys[0] = %q", activeKeys[0])
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
	if len(activeKeys) != 1 || activeKeys[0] != "db|index.docker.io/library/postgres" {
		t.Fatalf("activeKeys = %v", activeKeys)
	}
}

func TestRunOnceTagChangeKeepsContainerKey(t *testing.T) {
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
	if len(activeKeys) != 1 || activeKeys[0] != "app|index.docker.io/chatwoot/chatwoot" {
		t.Fatalf("activeKeys = %v", activeKeys)
	}
}

func TestRunOnceEmptyNameFallsBackToShortID(t *testing.T) {
	eng := NewEngine(
		&stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
			return []model.Container{
				{ID: "abcdef0123456789deadbeef", Name: "", ImageRef: "nginx:1.27"},
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
	want := "abcdef012345|index.docker.io/library/nginx"
	if len(activeKeys) != 1 || activeKeys[0] != want {
		t.Fatalf("activeKeys = %v, want %q", activeKeys, want)
	}
}
