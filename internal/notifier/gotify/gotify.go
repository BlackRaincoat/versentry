package gotify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"text/template"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/netutil"
	"github.com/BlackRaincoat/versentry/internal/notifier"
	"github.com/BlackRaincoat/versentry/internal/notifier/format"
	"github.com/BlackRaincoat/versentry/internal/notifier/httpretry"
)

const (
	defaultTimeout  = 10 * time.Second
	defaultPriority = 5
	modeSimple      = "simple"
	modeDigest      = "digest"

	// Default markdown templates (values are escaped for markdown; URL left raw).
	defaultItemTemplate = `**{{.Container}}**: {{.Change}}{{if .URL}}
[{{.URL}}]({{.URL}}){{end}}

`
	defaultDigestTemplate = `{{.Items}}`
)

const (
	defaultRetries    = httpretry.DefaultRetries
	defaultRetryDelay = httpretry.DefaultRetryDelay
)

// markdownSpecial are common markdown metacharacters escaped in field values.
var markdownSpecial = []string{"\\", "*", "_", "~", "`", "[", "]"}

func init() {
	notifier.Register("gotify", New)
}

// Notifier POSTs update messages to a Gotify server.
type Notifier struct {
	endpoint     *url.URL
	host         string
	token        string
	mode         string
	priority     int
	proxyURL     string
	instanceName string
	itemTmpl     *template.Template
	digestTmpl   *template.Template
	client       *http.Client
	maxAttempts  int
	retryDelay   time.Duration
	sleep        httpretry.SleepFunc
}

type messagePayload struct {
	Title    string         `json:"title"`
	Message  string         `json:"message"`
	Priority int            `json:"priority"`
	Extras   map[string]any `json:"extras,omitempty"`
}

// New constructs a Gotify notifier from plugin configuration.
func New(cfg map[string]any) (notifier.Notifier, error) {
	rawURL, err := requireString(cfg, "url")
	if err != nil {
		return nil, err
	}
	token, err := requireString(cfg, "token")
	if err != nil {
		return nil, err
	}

	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("gotify config: url: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("gotify config: url scheme must be http or https")
	}
	if base.Host == "" {
		return nil, fmt.Errorf("gotify config: url must include a host")
	}
	endpoint := resolveMessageURL(base)

	mode := optionalString(cfg, "mode", modeDigest)
	switch mode {
	case modeSimple, modeDigest:
	default:
		return nil, fmt.Errorf(`gotify config: mode must be %q or %q`, modeSimple, modeDigest)
	}

	priority, err := optionalInt(cfg, "priority", defaultPriority)
	if err != nil {
		return nil, err
	}
	if priority < 0 || priority > 10 {
		return nil, fmt.Errorf("gotify config: priority must be between 0 and 10")
	}

	// Preserve trailing newlines in templates (YAML |+ keep-chomp), like telegram.
	itemBody := optionalTemplate(cfg, "item_template", defaultItemTemplate)
	digestBody := optionalTemplate(cfg, "digest_template", defaultDigestTemplate)

	itemTmpl, err := compileTemplate("item", itemBody)
	if err != nil {
		return nil, fmt.Errorf("gotify config: item_template: %w", err)
	}
	digestTmpl, err := compileTemplate("digest", digestBody)
	if err != nil {
		return nil, fmt.Errorf("gotify config: digest_template: %w", err)
	}

	timeout, err := optionalDuration(cfg, "timeout", defaultTimeout)
	if err != nil {
		return nil, err
	}
	retries, err := optionalInt(cfg, "retries", defaultRetries)
	if err != nil {
		return nil, err
	}
	if retries < 0 {
		return nil, fmt.Errorf("gotify config: retries must be non-negative")
	}
	retryDelay, err := optionalDuration(cfg, "retry_delay", defaultRetryDelay)
	if err != nil {
		return nil, err
	}

	proxyURL := optionalString(cfg, "proxy", "")
	client, err := netutil.BuildHTTPClient(proxyURL, timeout)
	if err != nil {
		return nil, fmt.Errorf("gotify proxy: %w", err)
	}

	return &Notifier{
		endpoint:     endpoint,
		host:         endpoint.Host,
		token:        token,
		mode:         mode,
		priority:     priority,
		proxyURL:     proxyURL,
		instanceName: optionalString(cfg, "instance_name", ""),
		itemTmpl:     itemTmpl,
		digestTmpl:   digestTmpl,
		client:       client,
		maxAttempts:  maxAttemptsFromRetries(retries),
		retryDelay:   retryDelay,
		sleep:        httpretry.SleepContext,
	}, nil
}

// Notify delivers updates via Gotify POST /message.
// Simple mode sends one push per update (each as a one-item digest); digest mode
// sends a single summary — same pattern as telegram.
func (n *Notifier) Notify(ctx context.Context, events []model.UpdateAvailable) error {
	if len(events) == 0 {
		return nil
	}

	switch n.mode {
	case modeDigest:
		return n.sendBatch(ctx, events)
	default:
		for _, event := range events {
			if err := n.sendBatch(ctx, []model.UpdateAvailable{event}); err != nil {
				return err
			}
		}
		return nil
	}
}

func (n *Notifier) sendBatch(ctx context.Context, events []model.UpdateAvailable) error {
	text, err := n.renderDigest(events)
	if err != nil {
		slog.Error("gotify render failed", "host", n.host, "error", err)
		return err
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}

	body, err := json.Marshal(messagePayload{
		Title:    messageTitle(len(events)),
		Message:  text,
		Priority: n.priority,
		Extras: map[string]any{
			"client::display": map[string]any{
				"contentType": "text/markdown",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("gotify encode: %w", err)
	}

	if err := n.postWithRetry(ctx, body); err != nil {
		slog.Error("gotify notify failed",
			"host", n.host,
			"count", len(events),
			"error", err,
		)
		return err
	}
	return nil
}

func compileTemplate(name, body string) (*template.Template, error) {
	return template.New(name).Option("missingkey=zero").Parse(body)
}

func (n *Notifier) renderDigest(events []model.UpdateAvailable) (string, error) {
	items := make([]string, 0, len(events))
	for _, event := range events {
		item, err := n.renderItem(event)
		if err != nil {
			return "", err
		}
		if item != "" {
			items = append(items, item)
		}
	}

	var buf bytes.Buffer
	err := n.digestTmpl.Execute(&buf, format.DigestData{
		Instance: escapeMarkdown(n.instanceName),
		Count:    len(events),
		Items:    strings.Join(items, ""),
	})
	if err != nil {
		return "", fmt.Errorf("execute digest_template: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func (n *Notifier) renderItem(event model.UpdateAvailable) (string, error) {
	data := itemDataMarkdown(n.instanceName, event)
	var buf bytes.Buffer
	if err := n.itemTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute item_template: %w", err)
	}
	return buf.String(), nil
}

// itemDataMarkdown is like format.ItemFromEvent with HTML escape, but escapes
// field values for markdown and leaves URL raw for [url](url) links.
func itemDataMarkdown(instanceName string, event model.UpdateAvailable) format.ItemData {
	data := format.ItemFromEvent(instanceName, event, false)
	rawURL := data.URL
	data.Instance = escapeMarkdown(data.Instance)
	data.Container = escapeMarkdown(data.Container)
	data.Image = escapeMarkdown(data.Image)
	data.Tag = escapeMarkdown(data.Tag)
	data.Change = escapeMarkdown(data.Change)
	data.CurrentTag = escapeMarkdown(data.CurrentTag)
	data.LatestTag = escapeMarkdown(data.LatestTag)
	data.Host = escapeMarkdown(data.Host)
	data.URL = rawURL
	return data
}

func (n *Notifier) postWithRetry(ctx context.Context, body []byte) error {
	cfg := httpretry.Config{
		MaxAttempts:       n.maxAttempts,
		RetryDelay:        n.retryDelay,
		MaxRetryAfter:     httpretry.DefaultMaxRetryAfter,
		BackoffMultiplier: httpretry.DefaultBackoffMultiplier,
		Sleep:             n.sleep,
		LogComponent:      "gotify",
		LogFields:         []any{"host", n.host},
	}

	return httpretry.Do(ctx, cfg, httpretry.DefaultClassifier, func(ctx context.Context) httpretry.AttemptResult {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return httpretry.AttemptResult{Err: fmt.Errorf("create request: %w", err)}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Gotify-Key", n.token)

		resp, err := n.client.Do(req)
		if err != nil {
			return httpretry.AttemptResult{Err: err}
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return httpretry.AttemptResult{Err: fmt.Errorf("read response: %w", err)}
		}

		return httpretry.AttemptResult{
			StatusCode: resp.StatusCode,
			Body:       respBody,
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	})
}

func resolveMessageURL(base *url.URL) *url.URL {
	u := *base
	u.RawQuery = ""
	u.Fragment = ""
	path := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(path, "/message") {
		u.Path = path
		return &u
	}
	if path == "" {
		u.Path = "/message"
		return &u
	}
	u.Path = path + "/message"
	return &u
}

func messageTitle(count int) string {
	if count <= 1 {
		return "Versentry"
	}
	return fmt.Sprintf("Versentry: %d updates", count)
}

func escapeMarkdown(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		ch := string(r)
		for _, special := range markdownSpecial {
			if ch == special {
				b.WriteByte('\\')
				break
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

func requireString(cfg map[string]any, key string) (string, error) {
	v, ok := cfg[key]
	if !ok || v == nil {
		return "", fmt.Errorf("gotify config: %s is required", key)
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("gotify config: %s must be a non-empty string", key)
	}
	return strings.TrimSpace(s), nil
}

func optionalString(cfg map[string]any, key, fallback string) string {
	v, ok := cfg[key]
	if !ok || v == nil {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}

// optionalTemplate preserves trailing newlines (unlike optionalString).
func optionalTemplate(cfg map[string]any, key, fallback string) string {
	v, ok := cfg[key]
	if !ok || v == nil {
		return fallback
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func optionalDuration(cfg map[string]any, key string, fallback time.Duration) (time.Duration, error) {
	v, ok := cfg[key]
	if !ok || v == nil {
		return fallback, nil
	}
	s, ok := v.(string)
	if !ok {
		return 0, fmt.Errorf("gotify config: %s must be a duration string (e.g. \"10s\")", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("gotify config: %s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("gotify config: %s must be positive", key)
	}
	return d, nil
}

func optionalInt(cfg map[string]any, key string, fallback int) (int, error) {
	v, ok := cfg[key]
	if !ok || v == nil {
		return fallback, nil
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("gotify config: %s must be an integer", key)
	}
}

func maxAttemptsFromRetries(retries int) int {
	if retries < 0 {
		return 1
	}
	if retries <= 1 {
		return 1
	}
	return retries
}
