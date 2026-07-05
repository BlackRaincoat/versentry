package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
)

const testWebhookURL = "https://discord.com/api/webhooks/123456789/abcdefghijklmnopqrstuvwxyz"

func TestDigestEmbedPayload(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, modeDigest, formatEmbed, 0)
	wireTestServer(n, srv)
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "dashy"}, CurrentTag: "4.3.12", LatestTag: "4.3.14"},
		{Container: model.Container{Name: "nginx"}, CurrentTag: "1.25", LatestTag: "1.26"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var payload struct {
		Embeds []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Color       int    `json:"color"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("json: %v body=%s", err, body)
	}
	if len(payload.Embeds) != 1 {
		t.Fatalf("embeds = %d", len(payload.Embeds))
	}
	if payload.Embeds[0].Color != defaultEmbedColor {
		t.Fatalf("color = %d", payload.Embeds[0].Color)
	}
	if !strings.Contains(payload.Embeds[0].Description, `dashy`) {
		t.Fatalf("description = %q", payload.Embeds[0].Description)
	}
	if !strings.Contains(payload.Embeds[0].Description, `nginx`) {
		t.Fatalf("description = %q", payload.Embeds[0].Description)
	}
}

func TestSimpleModeMultiplePosts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, modeSimple, formatEmbed, 0)
	wireTestServer(n, srv)
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

func TestMarkdownEscapingInPayload(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, modeSimple, formatEmbed, 0)
	n.instanceName = "prod_host"
	wireTestServer(n, srv)
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "my_service_v2"}, CurrentTag: "1", LatestTag: "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Embeds []struct {
			Title string `json:"title"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload.Embeds[0].Title, `prod\_host`) {
		t.Fatalf("title = %q", payload.Embeds[0].Title)
	}
}

func TestContentFormat(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, modeDigest, formatContent, 0)
	wireTestServer(n, srv)
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Content string `json:"content"`
		Embeds  any    `json:"embeds"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Content == "" {
		t.Fatal("expected content")
	}
	if payload.Embeds != nil {
		t.Fatalf("expected no embeds, got %v", payload.Embeds)
	}
}

func TestCustomColor(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, modeSimple, formatEmbed, 999)
	wireTestServer(n, srv)
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"color":999`) {
		t.Fatalf("body = %s", body)
	}
}

func TestInvalidURLFailsAtNew(t *testing.T) {
	_, err := New(map[string]any{"url": "http://example.com/hook"})
	if err == nil {
		t.Fatal("expected error")
	}
	_, err = New(map[string]any{"url": "https://example.com/hook"})
	if err == nil {
		t.Fatal("expected error for non-discord host")
	}
}

func TestLogsHostNotFullURL(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(old) })

	n := newTestNotifier(t, modeDigest, formatEmbed, 0)
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
	if strings.Contains(out, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("log leaked webhook token: %s", out)
	}
	if !strings.Contains(out, "discord.com") {
		t.Fatalf("expected host in log: %s", out)
	}
}

func Test429JSONRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"rate limited","retry_after":0.01}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, modeDigest, formatEmbed, 0)
	n.sleep = func(ctx context.Context, d time.Duration) error {
		return nil
	}
	wireTestServer(n, srv)

	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func wireTestServer(n *Notifier, srv *httptest.Server) {
	target, _ := url.Parse(srv.URL)
	n.client = &http.Client{
		Timeout: defaultTimeout,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.URL.Scheme = target.Scheme
			r.URL.Host = target.Host
			return http.DefaultTransport.RoundTrip(r)
		}),
	}
}

func newTestNotifier(t *testing.T, mode, format string, color int) *Notifier {
	t.Helper()
	cfg := map[string]any{
		"url":    testWebhookURL,
		"mode":   mode,
		"format": format,
	}
	if color != 0 {
		cfg["color"] = color
	}
	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d := n.(*Notifier)
	d.instanceName = "prod"
	return d
}
