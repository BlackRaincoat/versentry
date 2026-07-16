package ntfy

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

func TestDigestPayloadHeadersAndMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "" {
			t.Fatalf("path = %q, want server base (JSON publish)", r.URL.Path)
		}
		if strings.Contains(r.URL.Path, "secret-topic") {
			t.Fatalf("topic must not be in request path: %q", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Fatalf("Authorization = %q, want empty (no token)", auth)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		var payload publishPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("json: %v body=%s", err, body)
		}
		if payload.Topic != "secret-topic" {
			t.Fatalf("topic = %q", payload.Topic)
		}
		if payload.Title != "Versentry: 2 updates" {
			t.Fatalf("title = %q", payload.Title)
		}
		if payload.Priority != 3 {
			t.Fatalf("priority = %d", payload.Priority)
		}
		if !payload.Markdown {
			t.Fatal("markdown not set")
		}
		if len(payload.Tags) != 1 || payload.Tags[0] != "package" {
			t.Fatalf("tags = %#v", payload.Tags)
		}
		if payload.Click != "" {
			t.Fatalf("click = %q, want empty in digest", payload.Click)
		}
		if !strings.Contains(payload.Message, "**a**:") || !strings.Contains(payload.Message, "1.0 → 1.1") {
			t.Fatalf("message = %q", payload.Message)
		}
		if !strings.Contains(payload.Message, "**b**:") {
			t.Fatalf("message missing second item: %q", payload.Message)
		}
		if !strings.Contains(payload.Message, "1.0 → 1.1\n\n**b**") {
			t.Fatalf("expected blank line between items: %q", payload.Message)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{
		"url":   srv.URL,
		"topic": "secret-topic",
		"mode":  modeDigest,
	})
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, Host: "ghcr.io", Repo: "org/app", CurrentTag: "1.0", LatestTag: "1.1"},
		{Container: model.Container{Name: "b"}, Host: "ghcr.io", Repo: "org/app2", CurrentTag: "2.0", LatestTag: "2.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSimpleModeClickAndMultiplePosts(t *testing.T) {
	const wantURL = "https://github.com/example/app/releases"
	var calls atomic.Int32
	var clicks []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		var payload publishPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Title != "Versentry" {
			t.Fatalf("title = %q", payload.Title)
		}
		clicks = append(clicks, payload.Click)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{
		"url":   srv.URL,
		"topic": "t",
		"mode":  modeSimple,
	})
	err := n.Notify(context.Background(), []model.UpdateAvailable{
		{
			Container: model.Container{
				Name: "a",
				Labels: map[string]string{
					"org.opencontainers.image.source": "https://github.com/example/app",
				},
			},
			Host:       "index.docker.io",
			Repo:       "example/app",
			CurrentTag: "1.0.0",
			LatestTag:  "1.0.1",
		},
		{Container: model.Container{Name: "b"}, CurrentTag: "3", LatestTag: "4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	if len(clicks) != 2 {
		t.Fatalf("clicks = %#v", clicks)
	}
	if clicks[0] != wantURL {
		t.Fatalf("first click = %q, want %q", clicks[0], wantURL)
	}
	if clicks[1] != "" {
		t.Fatalf("second click = %q, want empty (no URL)", clicks[1])
	}
}

func TestDigestNeverSetsClickEvenForOneUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload publishPayload
		_ = json.Unmarshal(body, &payload)
		if payload.Click != "" {
			t.Fatalf("digest click = %q", payload.Click)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{
		"url":   srv.URL,
		"topic": "t",
		"mode":  modeDigest,
	})
	if err := n.Notify(context.Background(), []model.UpdateAvailable{{
		Container: model.Container{
			Name: "a",
			Labels: map[string]string{
				"org.opencontainers.image.source": "https://github.com/example/app",
			},
		},
		Host:       "index.docker.io",
		Repo:       "example/app",
		CurrentTag: "1.0.0",
		LatestTag:  "1.0.1",
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestBearerTokenOptional(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tk_secret" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{
		"url":   srv.URL,
		"topic": "t",
		"token": "tk_secret",
	})
	if err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCustomTagsAndPriority(t *testing.T) {
	var got publishPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{
		"url":      srv.URL,
		"topic":    "t",
		"priority": 5,
		"tags":     []any{"warning", "package"},
	})
	if err := n.Notify(context.Background(), []model.UpdateAvailable{
		{Container: model.Container{Name: "a"}, CurrentTag: "1", LatestTag: "2"},
	}); err != nil {
		t.Fatal(err)
	}
	if got.Priority != 5 {
		t.Fatalf("priority = %d", got.Priority)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "warning" || got.Tags[1] != "package" {
		t.Fatalf("tags = %#v", got.Tags)
	}
}

func TestPriorityValidation(t *testing.T) {
	_, err := New(map[string]any{
		"url":      "https://ntfy.sh",
		"topic":    "t",
		"priority": 0,
	})
	if err == nil || !strings.Contains(err.Error(), "priority") {
		t.Fatalf("err = %v", err)
	}
	_, err = New(map[string]any{
		"url":      "https://ntfy.sh",
		"topic":    "t",
		"priority": 6,
	})
	if err == nil || !strings.Contains(err.Error(), "priority") {
		t.Fatalf("err = %v", err)
	}
}

func TestCustomItemTemplate(t *testing.T) {
	var msg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload publishPayload
		_ = json.Unmarshal(body, &payload)
		msg = payload.Message
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := New(map[string]any{
		"url":           srv.URL,
		"topic":         "t",
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
		var payload publishPayload
		_ = json.Unmarshal(body, &payload)
		msg = payload.Message
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n, err := New(map[string]any{
		"url":             srv.URL,
		"topic":           "t",
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

func TestMarkdownLinkInDefaultTemplate(t *testing.T) {
	const wantURL = "https://github.com/example/app/releases"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload publishPayload
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

	n := newTestNotifier(t, map[string]any{"url": srv.URL, "topic": "t"})
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
		var payload publishPayload
		_ = json.Unmarshal(body, &payload)
		if strings.Contains(payload.Message, "http") {
			t.Fatalf("expected no link line, got %q", payload.Message)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newTestNotifier(t, map[string]any{"url": srv.URL, "topic": "t"})
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
		"url":           "https://ntfy.sh",
		"topic":         "t",
		"item_template": "{{.Container",
	})
	if err == nil || !strings.Contains(err.Error(), "item_template") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderDigestUsesDigestWrapperForSimpleBatch(t *testing.T) {
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

	n := newTestNotifier(t, map[string]any{
		"url":     srv.URL,
		"topic":   "t",
		"retries": 3,
	})
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

	n := newTestNotifier(t, map[string]any{
		"url":   srv.URL,
		"topic": "t",
		"token": "bad",
	})
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
	n := newTestNotifier(t, map[string]any{
		"url":   "https://ntfy.sh",
		"topic": "t",
	})
	if err := n.Notify(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidConfig(t *testing.T) {
	cases := []map[string]any{
		{"topic": "t"},
		{"url": "https://ntfy.sh"},
		{"url": "://bad", "topic": "t"},
		{"url": "ftp://ntfy.sh", "topic": "t"},
		{"url": "https://ntfy.sh", "topic": "t", "mode": "batch"},
		{"url": "https://ntfy.sh", "topic": "t", "tags": "package"},
	}
	for _, cfg := range cases {
		if _, err := New(cfg); err == nil {
			t.Fatalf("expected error for cfg=%v", cfg)
		}
	}
}

func TestLogsHostNotTopicOrToken(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(old) })

	const secretTopic = "my-super-secret-topic-xyz"
	const secretToken = "tk_super_secret_token"
	n := newTestNotifier(t, map[string]any{
		"url":   "https://ntfy.example.com",
		"topic": secretTopic,
		"token": secretToken,
	})
	n.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.String(), secretTopic) {
				t.Fatalf("request URL contained topic: %s", r.URL.String())
			}
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
	if strings.Contains(out, secretTopic) {
		t.Fatalf("log leaked topic: %s", out)
	}
	if strings.Contains(out, secretToken) {
		t.Fatalf("log leaked token: %s", out)
	}
	if !strings.Contains(out, "ntfy.example.com") {
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

func newTestNotifier(t *testing.T, cfg map[string]any) *Notifier {
	t.Helper()
	if _, ok := cfg["retries"]; !ok {
		cfg["retries"] = 3
	}
	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out := n.(*Notifier)
	out.sleep = httpretry.SleepContext
	return out
}
