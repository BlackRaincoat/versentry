package oci

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEvictHungHTTP2ConnClosesSocket(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })

	rec := &httpTripRecorder{
		conn:   c1,
		remote: "192.0.2.1:443",
		path:   "/v2/library/redis/tags/list",
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	req, err := http.NewRequest(http.MethodGet, "https://example.com/v2/library/redis/tags/list", nil)
	if err != nil {
		t.Fatal(err)
	}

	evictHungHTTP2Conn(req, rec, errors.New("http2: timeout awaiting response headers"), log)

	_ = c2.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := c2.Read(b[:]); err == nil {
		t.Fatal("want read error after eviction closed the peer")
	}
	out := buf.String()
	if !strings.Contains(out, "evicted hung http2 connection") {
		t.Fatalf("want eviction log, got %q", out)
	}
	if !strings.Contains(out, "192.0.2.1:443") {
		t.Fatalf("want remote, got %q", out)
	}
	if !strings.Contains(out, "library/redis") {
		t.Fatalf("want repo, got %q", out)
	}
}

func TestEvictHungHTTP2ConnSkipsCanceled(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/v2/library/redis/tags/list", nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	evictHungHTTP2Conn(req, &httpTripRecorder{conn: c1, path: "/v2/library/redis/tags/list"},
		errors.New("http2: timeout awaiting response headers"), log)
	if strings.Contains(buf.String(), "evicted hung http2 connection") {
		t.Fatalf("canceled request must not evict, got %q", buf.String())
	}
	done := make(chan struct{})
	go func() {
		_, _ = c2.Read(make([]byte, 1))
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("canceled path closed the conn")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEvictHungHTTP2ConnSkipsTLSTimeout(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })
	req, err := http.NewRequest(http.MethodGet, "https://example.com/v2/library/redis/tags/list", nil)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	evictHungHTTP2Conn(req, &httpTripRecorder{conn: c1, path: "/v2/library/redis/tags/list"},
		errors.New("net/http: TLS handshake timeout"), log)
	if strings.Contains(buf.String(), "evicted hung http2 connection") {
		t.Fatalf("TLS timeout must not evict, got %q", buf.String())
	}
	_ = c2
}
