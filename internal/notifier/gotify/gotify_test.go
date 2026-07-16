package gotify

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
	"text/template"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/notifier/httpretry"
)

func TestDigestPayloadHeadersAndExtras(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message" {
			t.Fatalf("path = %q, want /message", r.URL.Path)
		}
		if r.Header.Get("X-Gotify-Key") != "app-token" {
			t.Fatalf("X-Gotify-Key = %q", r.Header.Get("X-Gotify-Key"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		var payload messagePayload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("json: %v body=%s", err, body)
		}
		if payload.Title != "Versentry: 2 updates" {
			t.Fatalf("title = %q", payload.Title)
		}
		if payload.Priority != 5 {
			t.Fatalf("priority = %d", payload.Priority)
		}
		display, _ := payload.Extras["client::display"].(map[string]any)
		if display["contentType"] != "text/markdown" {
			t.Fatalf("extras = %+v", payload.Extras)
		}
		if !strings.Contains(payload.Message, "**a**:") || !strings.Contains(payload.Message, "1.0 → 1.1") {
			t.Fatalf("message = %q", payload.Message)
		}
		if !strings.Contains(payload.Message, "**b**:") {
			t.Fatalf("message missing second item: %q", payload.Message)
		}
		// Blank line between items (default item_template trailing newline).
		if !strings.Contains(payload.Message, "1.0 → 1.1\n\n**b**") {
			t.Fatalf("expected blank line between items: %q", payload.Message)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL, "app-token", modeDigest, 5)
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, Host: "ghcr.io", Repo: "org/app", CurrentTag: "1.0", LatestTag: "1.1"},
		{Container: model.Container{Name: "b"}, Host: "ghcr.io", Repo: "org/app2", CurrentTag: "2.0", LatestTag: "2.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSimpleModeMultiplePostsAndSingleTitle(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		var payload messagePayload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Title != "Versentry" {
			t.Fatalf("title = %q", payload.Title)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL, "tok", modeSimple, 5)
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

func TestCustomItemTemplate(t *testing.T) {
	var msg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload messagePayload
		_ = json.Unmarshal(body, &payload)
		msg = payload.Message
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := New(map[string]any{
		"url":           srv.URL,
		"token":         "tok",
		"item_template": "- {{.Container}}: {{.Change}}\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "web"}, CurrentTag: "1", LatestTag: "2"},
	}); err != nil {
		t.Fatal(err)
	}
	if msg != "- web: 1 → 2" {
		t.Fatalf("message = %q", msg)
	}
}

func TestCustomDigestTemplate(t *testing.T) {
	var msg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload messagePayload
		_ = json.Unmarshal(body, &payload)
		msg = payload.Message
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := New(map[string]any{
		"url":             srv.URL,
		"token":           "tok",
		"instance_name":   "prod",
		"item_template":   "{{.Container}}\n",
		"digest_template": "{{.Instance}} ({{.Count}})\n{{.Items}}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
		{Container: model.Container{Name: "b"}, CurrentTag: "3", LatestTag: "4"},
	}); err != nil {
		t.Fatal(err)
	}
	want := "prod (2)\na\nb"
	if msg != want {
		t.Fatalf("message = %q, want %q", msg, want)
	}
}

func TestURLAlreadyEndsWithMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL+"/message", "tok", modeDigest, 5)
	if err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPriorityCustomAndValidation(t *testing.T) {
	var got int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload messagePayload
		_ = json.Unmarshal(body, &payload)
		got = payload.Priority
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL, "tok", modeDigest, 8)
	if err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	}); err != nil {
		t.Fatal(err)
	}
	if got != 8 {
		t.Fatalf("priority = %d", got)
	}

	_, err := New(map[string]any{
		"url":      "https://push.example.com",
		"token":    "tok",
		"priority": 11,
	})
	if err == nil || !strings.Contains(err.Error(), "priority") {
		t.Fatalf("err = %v", err)
	}
}

func TestEscapeMarkdownLeavesURLRaw(t *testing.T) {
	const wantURL = "https://github.com/example/app/releases"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload messagePayload
		_ = json.Unmarshal(body, &payload)
		if !strings.Contains(payload.Message, `**c\*ache**:`) {
			t.Fatalf("container not escaped: %q", payload.Message)
		}
		link := "[" + wantURL + "](" + wantURL + ")"
		if !strings.Contains(payload.Message, link) {
			t.Fatalf("expected markdown link %q in message: %q", link, payload.Message)
		}
		if strings.Contains(payload.Message, wantURL+`\*`) {
			t.Fatalf("url must not be escaped: %q", payload.Message)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL, "tok", modeDigest, 5)
	err := n.Notify(context.Background(), []model.UpdateAvailable{{
		Container: model.Container{
			Name: "c*ache",
			Labels: map[string]string{
				"org.opencontainers.image.source": "https://github.com/example/app",
			},
		},
		Host:       "index.docker.io",
		Repo:       "example/app",
		CurrentTag: "1.0.0",
		LatestTag:  "1.0.1",
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEmptyURLOmitsLinkLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload messagePayload
		_ = json.Unmarshal(body, &payload)
		if strings.Contains(payload.Message, "http") || strings.Contains(payload.Message, "](") {
			t.Fatalf("expected no link line, got %q", payload.Message)
		}
		if !strings.Contains(payload.Message, "**app**:") {
			t.Fatalf("message = %q", payload.Message)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL, "tok", modeDigest, 5)
	if err := n.Notify(context.Background(), []model.UpdateAvailable{{
		Container:  model.Container{Name: "app"},
		Host:       "ghcr.io",
		Repo:       "org/app",
		CurrentTag: "1.0.0",
		LatestTag:  "1.0.1",
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidItemTemplateFailsAtNew(t *testing.T) {
	_, err := New(map[string]any{
		"url":           "https://push.example.com",
		"token":         "tok",
		"item_template": "{{.Container",
	})
	if err == nil || !strings.Contains(err.Error(), "item_template") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderDigestUsesDigestWrapperForSimpleBatch(t *testing.T) {
	// One-item digest (simple mode path) still runs digest_template.
	itemTmpl, err := template.New("item").Parse("{{.Container}}\n")
	if err != nil {
		t.Fatal(err)
	}
	digestTmpl, err := template.New("digest").Parse("batch={{.Count}}\n{{.Items}}")
	if err != nil {
		t.Fatal(err)
	}
	n := &Notifier{
		instanceName: "host",
		itemTmpl:     itemTmpl,
		digestTmpl:   digestTmpl,
	}
	text, err := n.renderDigest([]model.UpdateAvailable{
		{Container: model.Container{Name: "only"}, CurrentTag: "1", LatestTag: "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "batch=1\nonly" {
		t.Fatalf("text = %q", text)
	}
}

func TestRetriesOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL, "tok", modeDigest, 5)
	n.sleep = func(ctx context.Context, d time.Duration) error { return nil }
	if err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestClientErrorNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	n := newTestNotifier(t, srv.URL, "bad", modeDigest, 5)
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestEmptyBatchNoPost(t *testing.T) {
	n := newTestNotifier(t, "https://push.example.com", "tok", modeDigest, 5)
	if err := n.Notify(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []map[string]any{
		{"token": "t"},
		{"url": "https://push.example.com"},
		{"url": "://bad", "token": "t"},
		{"url": "ftp://push.example.com", "token": "t"},
		{"url": "https://push.example.com", "token": "t", "mode": "batch"},
	}
	for _, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Fatalf("expected error for cfg=%v", cfg)
		}
	}
}

func TestLogsHostNotToken(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(old) })

	n := newTestNotifier(t, "https://push.example.com", "super-secret-token", modeDigest, 5)
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
	if strings.Contains(out, "super-secret-token") {
		t.Fatalf("log leaked token: %s", out)
	}
	if !strings.Contains(out, "push.example.com") {
		t.Fatalf("expected host in log: %s", out)
	}
}

func TestMessageTitleHelper(t *testing.T) {
	if got := messageTitle(1); got != "Versentry" {
		t.Fatalf("title(1) = %q", got)
	}
	if got := messageTitle(3); got != "Versentry: 3 updates" {
		t.Fatalf("title(3) = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newTestNotifier(t *testing.T, rawURL, token, mode string, priority int) *Notifier {
	t.Helper()
	n, err := New(map[string]any{
		"url":      rawURL,
		"token":    token,
		"mode":     mode,
		"priority": priority,
		"retries":  3,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := n.(*Notifier)
	out.sleep = httpretry.SleepContext
	return out
}
