package apprise

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestNotifier(t *testing.T, cfg map[string]any) *Notifier {
	t.Helper()
	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	an := n.(*Notifier)
	an.sleep = func(ctx context.Context, d time.Duration) error { return nil }
	return an
}

func captureWarn(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

var twoUpdates = []model.UpdateAvailable{
	{Container: model.Container{Name: "plex"}, Host: "index.docker.io", Repo: "linuxserver/plex", CurrentTag: "1.41.3", LatestTag: "1.41.4", Bump: model.BumpPatch},
	{Container: model.Container{Name: "web_1"}, Host: "index.docker.io", Repo: "library/nginx", CurrentTag: "1.27.1", LatestTag: "1.27.2", Bump: model.BumpPatch},
}

func TestStatefulPayloadPathAndAuth(t *testing.T) {
	var got notifyPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apprise/notify/versentry" {
			t.Errorf("path = %q, want base path kept + /notify/{key}", r.URL.Path)
		}
		if user, pass, ok := r.BasicAuth(); !ok || user != "bot" || pass != "s3cret" {
			t.Errorf("basic auth = %q/%q/%v", user, pass, ok)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("json: %v", err)
		}
		if strings.Contains(string(body), `"urls"`) {
			t.Errorf("stateful payload must not carry urls: %s", body)
		}
	}))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{
		"url": srv.URL + "/apprise/", "key": "versentry", "tag": "updates",
		"username": "bot", "password": "s3cret", "instance_name": "prod",
	})
	if err := n.Notify(context.Background(), twoUpdates); err != nil {
		t.Fatal(err)
	}
	if got.Title != "prod: 2 updates available" || got.Type != "info" || got.Format != "markdown" || got.Tag != "updates" {
		t.Fatalf("payload = %+v", got)
	}
	if !strings.Contains(got.Body, "**plex**: 1.41.3 → 1.41.4") || !strings.Contains(got.Body, `**web\_1**`) {
		t.Fatalf("body = %q (markdown items, escaped names)", got.Body)
	}
}

func TestStatelessURLsFromList(t *testing.T) {
	var got notifyPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notify" {
			t.Errorf("path = %q, want /notify", r.URL.Path)
		}
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("no credentials configured, none expected")
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
	}))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{
		"url":  srv.URL,
		"urls": []any{"mailto://u:p@example.com", " pover://user@token "},
	})
	if err := n.Notify(context.Background(), twoUpdates[:1]); err != nil {
		t.Fatal(err)
	}
	if got.URLs != "mailto://u:p@example.com, pover://user@token" || got.Tag != "" {
		t.Fatalf("payload = %+v", got)
	}
	if got.Title != "Versentry: update available" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestMessageTypeWarningForMajorOrReminder(t *testing.T) {
	cases := []struct {
		name string
		ev   model.UpdateAvailable
		want string
	}{
		{"patch", model.UpdateAvailable{Bump: model.BumpPatch}, typeInfo},
		{"digest", model.UpdateAvailable{}, typeInfo},
		{"major", model.UpdateAvailable{Bump: model.BumpMajor}, typeWarning},
		{"reminder", model.UpdateAvailable{Bump: model.BumpPatch, Reminder: true}, typeWarning},
	}
	for _, tc := range cases {
		if got := messageType([]model.UpdateAvailable{{Bump: model.BumpPatch}, tc.ev}); got != tc.want {
			t.Fatalf("%s: type = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPartialFailure424IsDeliveredWithWarn(t *testing.T) {
	logs := captureWarn(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusFailedDependency)
		_, _ = w.Write([]byte("One or more notification could not be sent."))
	}))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{"url": srv.URL, "key": "versentry", "retries": 3})
	if err := n.Notify(context.Background(), twoUpdates); err != nil {
		t.Fatalf("424 must count as delivered (no duplicates on retry), got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (424 is not retried)", calls.Load())
	}
	out := logs.String()
	if !strings.Contains(out, "HTTP 424") || !strings.Contains(out, "could not be sent") {
		t.Fatalf("missing WARN with apprise-api response: %s", out)
	}
}

func TestServerErrorRetriedThenFails(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{"url": srv.URL, "key": "versentry", "retries": 3})
	if err := n.Notify(context.Background(), twoUpdates); err == nil {
		t.Fatal("expected error: nothing was sent, so retry next pass is safe")
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestClientErrorsFailWithoutRetry(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadRequest} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
		}))
		n := newTestNotifier(t, map[string]any{"url": srv.URL, "key": "versentry", "retries": 3})
		err := n.Notify(context.Background(), twoUpdates)
		srv.Close()
		if err == nil || calls.Load() != 1 {
			t.Fatalf("status %d: err=%v calls=%d, want error after one call", status, err, calls.Load())
		}
	}
}

func TestSimpleModeOneRequestPerUpdate(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{"url": srv.URL, "key": "k", "mode": "simple"})
	if err := n.Notify(context.Background(), twoUpdates); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestNetworkErrorDoesNotLeakKeyOrCredentials(t *testing.T) {
	logs := captureWarn(t)
	n := newTestNotifier(t, map[string]any{
		"url": "https://apprise.example.com", "key": "secret-key-123",
		"username": "bot", "password": "p4ss", "retries": 2,
	})
	n.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	})}

	err := n.Notify(context.Background(), twoUpdates)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, s := range []string{err.Error(), logs.String()} {
		if strings.Contains(s, "secret-key-123") || strings.Contains(s, "p4ss") {
			t.Fatalf("leaked key or password: %s", s)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	bad := []map[string]any{
		{"key": "k"}, // no url
		{"url": "ftp://apprise", "key": "k"},
		{"url": "http://apprise"}, // neither key nor urls
		{"url": "http://apprise", "key": "k", "urls": "mailto://x"}, // both
		{"url": "http://apprise", "urls": "mailto://x", "tag": "t"}, // tag needs key
		{"url": "http://apprise", "key": "a/b"},
		{"url": "http://apprise", "key": "k", "username": "u"}, // user without password
		{"url": "http://apprise", "key": "k", "mode": "loud"},
		{"url": "http://apprise", "key": "k", "item_template": "{{.Nope"},
		{"url": "http://apprise", "urls": 42},
	}
	for i, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Fatalf("case %d %v: expected error", i, cfg)
		}
	}
	if _, err := New(map[string]any{"url": "http://apprise:8000", "key": "versentry", "password": "only-password"}); err != nil {
		t.Fatalf("password without username is valid Basic Auth: %v", err)
	}
}
