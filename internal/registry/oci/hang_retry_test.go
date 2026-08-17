package oci

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func hungThenOKHandler(t *testing.T, repo string, hits *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
			w.WriteHeader(http.StatusOK)
			return
		case strings.HasPrefix(r.URL.Path, "/v2/"+repo+"/tags/list"):
			if hits.Add(1) == 1 {
				<-r.Context().Done()
				return
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{"name":%q,"tags":["7.2.0"]}`, repo)))
			return
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func tlsHTTP2Registry(t *testing.T, handler http.Handler) *Registry {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "https://")
	raw, err := New(map[string]any{"host": host})
	if err != nil {
		t.Fatal(err)
	}
	reg := raw.(*Registry)
	reg.transport = applyRegistryIOTimeouts(srv.Client().Transport)
	return reg
}

func TestListTagsEvictsHungConnBeforeGcrRetry(t *testing.T) {
	prev := registryResponseHeaderTimeout
	registryResponseHeaderTimeout = 150 * time.Millisecond
	t.Cleanup(func() { registryResponseHeaderTimeout = prev })

	const repo = "library/redis"
	var hits atomic.Int32
	reg := tlsHTTP2Registry(t, hungThenOKHandler(t, repo, &hits))

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	tags, err := reg.listTags(context.Background(), repo, log)
	if err != nil {
		t.Fatalf("gcr retry should succeed after eviction, got %v\nlogs: %s", err, buf.String())
	}
	if len(tags) != 1 || tags[0] != "7.2.0" {
		t.Fatalf("tags = %v", tags)
	}
	if hits.Load() < 2 {
		t.Fatalf("want ≥2 tag-list hits (hang + fresh dial), got %d", hits.Load())
	}

	out := buf.String()
	if !strings.Contains(out, "evicted hung http2 connection") {
		t.Fatalf("want eviction debug line, got %q", out)
	}
	if !strings.Contains(out, "repo="+repo) && !strings.Contains(out, `repo="`+repo+`"`) {
		t.Fatalf("want repo on eviction line, got %q", out)
	}
}

func TestListTagsEvictsHungConnWithoutDebugLog(t *testing.T) {
	prev := registryResponseHeaderTimeout
	registryResponseHeaderTimeout = 150 * time.Millisecond
	t.Cleanup(func() { registryResponseHeaderTimeout = prev })

	const repo = "library/redis"
	var hits atomic.Int32
	reg := tlsHTTP2Registry(t, hungThenOKHandler(t, repo, &hits))

	tags, err := reg.listTags(context.Background(), repo, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err != nil {
		t.Fatalf("eviction must run at info log level, got %v", err)
	}
	if len(tags) != 1 || tags[0] != "7.2.0" {
		t.Fatalf("tags = %v", tags)
	}
	if hits.Load() < 2 {
		t.Fatalf("want ≥2 hits, got %d", hits.Load())
	}
}

func TestListTagsHeaderHangTracesWithoutOurReconnect(t *testing.T) {
	prev := registryResponseHeaderTimeout
	registryResponseHeaderTimeout = 150 * time.Millisecond
	t.Cleanup(func() { registryResponseHeaderTimeout = prev })

	const repo = "library/redis"
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
			w.WriteHeader(http.StatusOK)
			return
		case strings.HasPrefix(r.URL.Path, "/v2/"+repo+"/tags/list"):
			n := hits.Add(1)
			if n == 1 {
				// Stuck first response: no headers until client gives up.
				<-r.Context().Done()
				return
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{"name":%q,"tags":["7.2.0"]}`, repo)))
			return
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	reg := testRegistry(t, host)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	start := time.Now()
	_, err := reg.listTags(context.Background(), repo, log)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("call took too long: %v", time.Since(start))
	}

	out := buf.String()
	if strings.Contains(out, "list_tags retry after connection hang") {
		t.Fatalf("our hang reconnect loop must be gone, got %q", out)
	}
	if strings.Contains(out, `\"`) {
		t.Fatalf("err still has escaped quotes: %q", out)
	}
	if !strings.Contains(out, "registry_http") {
		t.Fatalf("want registry_http trace, got %q", out)
	}
	if !strings.Contains(out, "last_phase=waiting_headers") {
		t.Fatalf("want last_phase=waiting_headers on incomplete, got %q", out)
	}
	if err == nil {
		return // gcr Temporary retry recovered; our loop still must not have run
	}
	if !strings.Contains(err.Error(), "timeout awaiting response headers") {
		t.Fatalf("want header timeout without our reconnect, got %v", err)
	}
}

func TestListTagsNoRetryOnSlowAliveFirstPage(t *testing.T) {
	prev := registryResponseHeaderTimeout
	registryResponseHeaderTimeout = 2 * time.Second
	t.Cleanup(func() { registryResponseHeaderTimeout = prev })

	const repo = "library/postgres"
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
			w.WriteHeader(http.StatusOK)
			return
		case strings.HasPrefix(r.URL.Path, "/v2/"+repo+"/tags/list"):
			hits.Add(1)
			time.Sleep(200 * time.Millisecond) // slow but under header timeout
			_, _ = w.Write([]byte(fmt.Sprintf(`{"name":%q,"tags":["16"]}`, repo)))
			return
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	reg := testRegistry(t, host)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	tags, err := reg.listTags(context.Background(), repo, log)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0] != "16" {
		t.Fatalf("tags = %v", tags)
	}
	if hits.Load() != 1 {
		t.Fatalf("slow-alive must not retry, hits=%d", hits.Load())
	}
	if strings.Contains(buf.String(), "retry after connection hang") {
		t.Fatalf("unexpected retry: %q", buf.String())
	}
	if strings.Contains(buf.String(), "evicted hung http2 connection") {
		t.Fatalf("slow-alive must not evict, got %q", buf.String())
	}
}

func TestListTagsDoesNotRetryWhenParentCtxDead(t *testing.T) {
	prev := registryResponseHeaderTimeout
	registryResponseHeaderTimeout = 2 * time.Second
	t.Cleanup(func() { registryResponseHeaderTimeout = prev })

	const repo = "library/redis"
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/" || r.URL.Path == "/v2":
			w.WriteHeader(http.StatusOK)
			return
		case strings.HasPrefix(r.URL.Path, "/v2/"+repo+"/tags/list"):
			hits.Add(1)
			<-r.Context().Done()
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	reg := testRegistry(t, host)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, err := reg.listTags(ctx, repo, log)
	if err == nil {
		t.Fatal("expected error")
	}
	if hits.Load() != 1 {
		t.Fatalf("parent deadline must not extra-retry, hits=%d", hits.Load())
	}
	if strings.Contains(buf.String(), "evicted hung http2 connection") {
		t.Fatalf("canceled/deadline must not evict, got %q", buf.String())
	}
}
