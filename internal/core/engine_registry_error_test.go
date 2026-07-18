package core

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
)

type flakyListRegistry struct {
	host     string
	listFn   func(ctx context.Context, repo string) ([]string, error)
	digestFn func(ctx context.Context, repo, tag string) (string, error)
}

func (r *flakyListRegistry) Host() string { return r.host }

func (r *flakyListRegistry) ListTags(ctx context.Context, repo string) ([]string, error) {
	if r.listFn != nil {
		return r.listFn(ctx, repo)
	}
	return nil, errors.New("unexpected ListTags")
}

func (r *flakyListRegistry) TagDigest(ctx context.Context, repo, tag string) (string, error) {
	if r.digestFn != nil {
		return r.digestFn(ctx, repo, tag)
	}
	return "", errors.New("unexpected TagDigest")
}

func registryTimeouts() config.Timeouts {
	return config.Timeouts{
		Provider: config.Duration{Duration: 5 * time.Second},
		Registry: config.Duration{Duration: 5 * time.Second},
	}
}

func TestListTagsTimeoutSkipsWhenParentAlive(t *testing.T) {
	reg := &flakyListRegistry{
		host: imageref.DockerHubHost,
		listFn: func(ctx context.Context, repo string) ([]string, error) {
			return nil, context.DeadlineExceeded
		},
	}
	eng := NewEngine(&stubProvider{}, nil, registryTimeouts(), slog.Default(), nil, nil)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "shared-postgres", ImageRef: "postgres:16"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(slog.Default()))
	if err != nil {
		t.Fatalf("unexpected fatal err: %v", err)
	}
	if result.Status != statusSkipped {
		t.Fatalf("status = %s, want skipped", result.Status)
	}
	if !strings.Contains(result.Reason, "timeout/network") {
		t.Fatalf("reason = %q", result.Reason)
	}
}

func TestListTagsNetworkErrorSkips(t *testing.T) {
	reg := &flakyListRegistry{
		host: imageref.DockerHubHost,
		listFn: func(ctx context.Context, repo string) ([]string, error) {
			return nil, &net.OpError{Op: "read", Err: errors.New("connection reset")}
		},
	}
	eng := NewEngine(&stubProvider{}, nil, registryTimeouts(), slog.Default(), nil, nil)
	eng.registries = append(eng.registries, reg)

	result, err := eng.checkContainer(
		context.Background(),
		model.Container{Name: "pg", ImageRef: "postgres:16"},
		registrypass.New(slog.Default()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != statusSkipped {
		t.Fatalf("status = %s", result.Status)
	}
}

func TestListTagsParentCancelPropagates(t *testing.T) {
	reg := &flakyListRegistry{
		host: imageref.DockerHubHost,
		listFn: func(ctx context.Context, repo string) ([]string, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	eng := NewEngine(&stubProvider{}, nil, registryTimeouts(), slog.Default(), nil, nil)
	eng.registries = append(eng.registries, reg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := eng.checkContainer(
		ctx,
		model.Container{Name: "pg", ImageRef: "postgres:16"},
		registrypass.New(slog.Default()),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestTagDigestTimeoutSkips(t *testing.T) {
	reg := &flakyListRegistry{
		host: imageref.DockerHubHost,
		digestFn: func(ctx context.Context, repo, tag string) (string, error) {
			return "", context.DeadlineExceeded
		},
	}
	eng := NewEngine(
		&modeTestProvider{localDigest: "sha256:localaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		nil,
		registryTimeouts(),
		slog.Default(),
		nil,
		nil,
	)
	eng.registries = append(eng.registries, reg)

	result, err := eng.checkContainer(
		context.Background(),
		model.Container{Name: "app", ImageRef: "library/app:latest"},
		registrypass.New(slog.Default()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != statusSkipped {
		t.Fatalf("status = %s", result.Status)
	}
	if !strings.Contains(result.Reason, "timeout/network") {
		t.Fatalf("reason = %q", result.Reason)
	}
}

func TestRunOnceContinuesAfterListTagsTimeout(t *testing.T) {
	var listed []string
	reg := &flakyListRegistry{
		host: imageref.DockerHubHost,
		listFn: func(ctx context.Context, repo string) ([]string, error) {
			listed = append(listed, repo)
			if repo == "library/postgres" {
				return nil, context.DeadlineExceeded
			}
			return []string{"1.25.0", "1.25.1"}, nil
		},
	}
	prov := &stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
		return []model.Container{
			{Name: "shared-postgres", ImageRef: "postgres:16"},
			{Name: "nginx", ImageRef: "nginx:1.25.0"},
		}, nil
	}}
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	eng := NewEngine(prov, nil, registryTimeouts(), log, nil, nil)
	eng.registries = append(eng.registries, reg)

	updates, _, _, err := eng.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce must not fail on per-image timeout: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed repos = %v, want both containers checked", listed)
	}
	if len(updates) != 1 || updates[0].Container.Name != "nginx" {
		t.Fatalf("updates = %+v", updates)
	}
	if !strings.Contains(logBuf.String(), "check complete") {
		t.Fatalf("expected check complete in logs:\n%s", logBuf.String())
	}
}

func TestRunOnceParentCancelStopsPass(t *testing.T) {
	started := make(chan struct{})
	reg := &flakyListRegistry{
		host: imageref.DockerHubHost,
		listFn: func(ctx context.Context, repo string) ([]string, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	prov := &stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
		return []model.Container{
			{Name: "slow", ImageRef: "postgres:16"},
			{Name: "never", ImageRef: "nginx:1.25.0"},
		}, nil
	}}
	eng := NewEngine(prov, nil, registryTimeouts(), slog.Default(), nil, nil)
	eng.registries = append(eng.registries, reg)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	_, _, _, err := eng.RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRunOnceSafetyNetSkipsUnexpectedCheckError(t *testing.T) {
	var calls int
	reg := &flakyListRegistry{
		host: imageref.DockerHubHost,
		listFn: func(ctx context.Context, repo string) ([]string, error) {
			calls++
			if repo == "library/postgres" {
				return nil, errors.New("weird registry bug")
			}
			return []string{"1.25.0"}, nil
		},
	}
	prov := &stubProvider{listFn: func(ctx context.Context) ([]model.Container, error) {
		return []model.Container{
			{Name: "bad", ImageRef: "postgres:16"},
			{Name: "ok", ImageRef: "nginx:1.25.0"},
		}, nil
	}}

	eng := NewEngine(prov, nil, registryTimeouts(), slog.Default(), nil, nil)
	eng.registries = append(eng.registries, reg)

	_, _, _, err := eng.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected fatal: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}
