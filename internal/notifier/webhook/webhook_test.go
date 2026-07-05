package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/model"
)

func TestDefaultDigestPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Instance string `json:"instance"`
			Count    int    `json:"count"`
			Updates  []struct {
				Container  string `json:"container"`
				Mode       string `json:"mode"`
				LatestTag  string `json:"latest_tag"`
				CurrentTag string `json:"current_tag"`
			} `json:"updates"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("json: %v body=%s", err, body)
		}
		if payload.Instance != "prod" || payload.Count != 2 || len(payload.Updates) != 2 {
			t.Fatalf("payload: %+v", payload)
		}
		if payload.Updates[0].Mode != "semver" {
			t.Fatalf("mode = %q", payload.Updates[0].Mode)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL, modeDigest, "")
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, Host: "ghcr.io", Repo: "org/app", CurrentTag: "1.0", LatestTag: "1.1"},
		{Container: model.Container{Name: "b"}, Host: "ghcr.io", Repo: "org/app2", CurrentTag: "2.0", LatestTag: "2.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSimpleModeMultiplePosts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Count   int `json:"count"`
			Updates []struct {
				Container string `json:"container"`
			} `json:"updates"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Count != 1 || len(payload.Updates) != 1 {
			t.Fatalf("expected single-item envelope, got %s", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL, modeSimple, "")
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
		{Container: model.Container{Name: "b"}, CurrentTag: "3", LatestTag: "4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestHeaderEnvExpansion(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("WEBHOOK_TOKEN", "secret-value")
	n, err := New(map[string]any{
		"url": srv.URL,
		"headers": map[string]any{
			"Authorization": "Bearer ${WEBHOOK_TOKEN}",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret-value" {
		t.Fatalf("auth = %q", auth)
	}
}

func TestCustomTemplateSimple(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL, modeSimple, `{"container":"{{.Container}}","change":"{{.Change}}"}`)
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "dashy"}, CurrentTag: "4.3.12", LatestTag: "4.3.14"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"container":"dashy"`) || !strings.Contains(body, "4.3.12 → 4.3.14") {
		t.Fatalf("body = %s", body)
	}
}

func TestInvalidURLFailsAtNew(t *testing.T) {
	_, err := New(map[string]any{"url": "://bad"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInvalidTemplateFailsAtNew(t *testing.T) {
	_, err := New(map[string]any{
		"url":      "https://example.com/hook",
		"template": "{{.Container",
	})
	if err == nil || !strings.Contains(err.Error(), "template") {
		t.Fatalf("err = %v", err)
	}
}

func TestEmptyBatchNoPost(t *testing.T) {
	n := newTestNotifier(t, "https://example.com/hook", modeDigest, "")
	if err := n.Notify(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestLogsHostNotFullURL(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(old) })

	n := newTestNotifier(t, "https://hooks.example.com/secret/token/path", modeDigest, "")
	n.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader("denied")),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		}),
	}

	_ = n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	})

	out := buf.String()
	if strings.Contains(out, "/secret/token/path") {
		t.Fatalf("log leaked URL path: %s", out)
	}
	if !strings.Contains(out, "hooks.example.com") {
		t.Fatalf("expected host in log: %s", out)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newTestNotifier(t *testing.T, url, mode, tmpl string) *Notifier {
	t.Helper()
	cfg := map[string]any{"url": url, "mode": mode}
	if tmpl != "" {
		cfg["template"] = tmpl
	}
	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wh := n.(*Notifier)
	wh.instanceName = "prod"
	return wh
}
