package registrypass

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

type mockRegistry struct {
	host string

	listTagsCalls atomic.Int32
	tagDigestCalls atomic.Int32

	listTagsFn  func(ctx context.Context, repo string) ([]string, error)
	tagDigestFn func(ctx context.Context, repo, tag string) (string, error)
}

func (m *mockRegistry) Host() string { return m.host }

func (m *mockRegistry) ListTags(ctx context.Context, repo string) ([]string, error) {
	m.listTagsCalls.Add(1)
	if m.listTagsFn != nil {
		return m.listTagsFn(ctx, repo)
	}
	return nil, nil
}

func (m *mockRegistry) TagDigest(ctx context.Context, repo, tag string) (string, error) {
	m.tagDigestCalls.Add(1)
	if m.tagDigestFn != nil {
		return m.tagDigestFn(ctx, repo, tag)
	}
	return "", nil
}

func serverError(code int) error {
	return &transport.Error{StatusCode: code}
}

func rateLimitError(retryAfter time.Duration) error {
	return &registry.RateLimitError{RetryAfter: retryAfter}
}

func TestListTagsDedupWithinPass(t *testing.T) {
	reg := &mockRegistry{
		host: "index.docker.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"1.0.0", "1.1.0"}, nil
		},
	}

	pass := New(nil)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		tags, err := pass.ListTags(ctx, reg, reg.host, "library/nginx")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if len(tags) != 2 {
			t.Fatalf("call %d: tags = %v", i, tags)
		}
	}

	if got := reg.listTagsCalls.Load(); got != 1 {
		t.Fatalf("ListTags calls = %d, want 1", got)
	}
}

func TestTagDigestSeparateKeysPerTag(t *testing.T) {
	reg := &mockRegistry{host: "ghcr.io"}
	var calls atomic.Int32
	reg.tagDigestFn = func(ctx context.Context, repo, tag string) (string, error) {
		calls.Add(1)
		return tag + "-digest", nil
	}

	pass := New(nil)
	ctx := context.Background()

	d1, err := pass.TagDigest(ctx, reg, reg.host, "org/app", "latest")
	if err != nil || d1 != "latest-digest" {
		t.Fatalf("latest: digest=%q err=%v", d1, err)
	}
	d2, err := pass.TagDigest(ctx, reg, reg.host, "org/app", "stable")
	if err != nil || d2 != "stable-digest" {
		t.Fatalf("stable: digest=%q err=%v", d2, err)
	}
	d3, err := pass.TagDigest(ctx, reg, reg.host, "org/app", "latest")
	if err != nil || d3 != "latest-digest" {
		t.Fatalf("latest cached: digest=%q err=%v", d3, err)
	}

	if got := calls.Load(); got != 2 {
		t.Fatalf("TagDigest calls = %d, want 2", got)
	}
}

func Test429ShortRetryAfterSucceeds(t *testing.T) {
	var calls atomic.Int32
	reg := &mockRegistry{
		host: "index.docker.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			if calls.Add(1) == 1 {
				return nil, rateLimitError(3 * time.Second)
			}
			return []string{"1.0.0"}, nil
		},
	}

	var slept time.Duration
	pass := New(nil)
	pass.sleep = func(ctx context.Context, d time.Duration) error {
		slept = d
		return nil
	}

	tags, err := pass.ListTags(context.Background(), reg, reg.host, "library/redis")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 {
		t.Fatalf("tags = %v", tags)
	}
	if slept != 3*time.Second {
		t.Fatalf("sleep = %v, want 3s", slept)
	}
	if reg.listTagsCalls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", reg.listTagsCalls.Load())
	}
}

func Test429ShortThen429MarksHost(t *testing.T) {
	reg := &mockRegistry{
		host: "index.docker.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return nil, rateLimitError(2 * time.Second)
		},
	}

	pass := New(nil)
	pass.sleep = func(ctx context.Context, d time.Duration) error { return nil }

	ctx := context.Background()
	_, err := pass.ListTags(ctx, reg, reg.host, "library/redis")
	if !errors.Is(err, registry.ErrRateLimited) {
		t.Fatalf("first repo err = %v", err)
	}

	_, err = pass.ListTags(ctx, reg, reg.host, "library/nginx")
	if !errors.Is(err, registry.ErrRateLimited) {
		t.Fatalf("second repo err = %v", err)
	}
	if reg.listTagsCalls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 (no extra after host marked)", reg.listTagsCalls.Load())
	}
}

func Test429LongRetryAfterMarksHostImmediately(t *testing.T) {
	reg := &mockRegistry{
		host: "index.docker.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return nil, rateLimitError(30 * time.Second)
		},
	}

	var slept bool
	pass := New(nil)
	pass.sleep = func(ctx context.Context, d time.Duration) error {
		slept = true
		return nil
	}

	_, err := pass.ListTags(context.Background(), reg, reg.host, "library/redis")
	if !errors.Is(err, registry.ErrRateLimited) {
		t.Fatal(err)
	}
	if slept {
		t.Fatal("should not sleep on long Retry-After")
	}
	if reg.listTagsCalls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", reg.listTagsCalls.Load())
	}
}

func Test429MissingRetryAfterMarksHost(t *testing.T) {
	reg := &mockRegistry{
		host: "index.docker.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return nil, rateLimitError(0)
		},
	}

	pass := New(nil)
	_, err := pass.ListTags(context.Background(), reg, reg.host, "library/redis")
	if !errors.Is(err, registry.ErrRateLimited) {
		t.Fatal(err)
	}
}

func Test5xxExhaustedDoesNotMarkHost(t *testing.T) {
	reg := &mockRegistry{
		host: "index.docker.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return nil, serverError(http.StatusBadGateway)
		},
	}

	pass := New(nil)
	pass.sleep = func(ctx context.Context, d time.Duration) error { return nil }

	ctx := context.Background()
	_, err := pass.ListTags(ctx, reg, reg.host, "library/redis")
	if !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("redis err = %v", err)
	}

	reg.listTagsCalls.Store(0)
	_, err = pass.ListTags(ctx, reg, reg.host, "library/nginx")
	if !errors.Is(err, registry.ErrUnavailable) {
		t.Fatalf("nginx err = %v", err)
	}
	if reg.listTagsCalls.Load() != 2 {
		t.Fatalf("nginx should still query registry, calls = %d", reg.listTagsCalls.Load())
	}
}

func Test5xxPassAttempts(t *testing.T) {
	var calls atomic.Int32
	reg := &mockRegistry{
		host: "index.docker.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			calls.Add(1)
			return nil, serverError(http.StatusServiceUnavailable)
		},
	}

	pass := New(nil)
	pass.sleep = func(ctx context.Context, d time.Duration) error { return nil }

	_, err := pass.ListTags(context.Background(), reg, reg.host, "library/redis")
	if !errors.Is(err, registry.ErrUnavailable) {
		t.Fatal(err)
	}
	if got := calls.Load(); got != int32(registry5xxMaxAttempts) {
		t.Fatalf("calls = %d, want %d", got, registry5xxMaxAttempts)
	}
}

func TestRateLimitedHostDoesNotAffectOtherHost(t *testing.T) {
	hub := &mockRegistry{
		host: "index.docker.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return nil, rateLimitError(0)
		},
	}
	ghcr := &mockRegistry{
		host: "ghcr.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"1.0.0"}, nil
		},
	}

	pass := New(nil)
	ctx := context.Background()

	_, err := pass.ListTags(ctx, hub, hub.host, "library/redis")
	if !errors.Is(err, registry.ErrRateLimited) {
		t.Fatal(err)
	}

	tags, err := pass.ListTags(ctx, ghcr, ghcr.host, "org/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 {
		t.Fatalf("tags = %v", tags)
	}
}

func TestContextCancelDuringRateLimitSleep(t *testing.T) {
	reg := &mockRegistry{
		host: "index.docker.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return nil, rateLimitError(5 * time.Second)
		},
	}

	pass := New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := pass.ListTags(ctx, reg, reg.host, "library/redis")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestNotFoundCached(t *testing.T) {
	var calls atomic.Int32
	reg := &mockRegistry{
		host: "index.docker.io",
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			calls.Add(1)
			return nil, registry.ErrNotFound
		},
	}

	pass := New(nil)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		_, err := pass.ListTags(ctx, reg, reg.host, "missing/repo")
		if !errors.Is(err, registry.ErrNotFound) {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}
