package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
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
	defaultTimeout = 10 * time.Second
	modeSimple     = "simple"
	modeDigest     = "digest"
)

const (
	defaultRetries    = httpretry.DefaultRetries
	defaultRetryDelay = httpretry.DefaultRetryDelay
)

func init() {
	notifier.Register("webhook", New)
}

// Notifier POSTs update batches to an HTTP endpoint.
type Notifier struct {
	endpoint     *url.URL
	host         string
	instanceName string
	mode         string
	headers      http.Header
	proxyURL     string
	tmpl         *template.Template
	client       *http.Client
	maxAttempts  int
	retryDelay   time.Duration
	sleep        httpretry.SleepFunc
}

// New constructs a webhook notifier from plugin configuration.
func New(cfg map[string]any) (notifier.Notifier, error) {
	rawURL, err := requireString(cfg, "url")
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("webhook config: url: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("webhook config: url scheme must be http or https")
	}
	if endpoint.Host == "" {
		return nil, fmt.Errorf("webhook config: url must include a host")
	}

	mode := optionalString(cfg, "mode", modeDigest)
	switch mode {
	case modeSimple, modeDigest:
	default:
		return nil, fmt.Errorf(`webhook config: mode must be %q or %q`, modeSimple, modeDigest)
	}

	var tmpl *template.Template
	if body := optionalTemplate(cfg, "template"); body != "" {
		tmpl, err = template.New("webhook").Option("missingkey=zero").Parse(body)
		if err != nil {
			return nil, fmt.Errorf("webhook config: template: %w", err)
		}
	}

	headers, err := parseHeaders(cfg)
	if err != nil {
		return nil, err
	}

	timeout, err := optionalDuration(cfg, "timeout", defaultTimeout)
	if err != nil {
		return nil, err
	}
	retries, err := optionalInt(cfg, "retries", defaultRetries)
	if err != nil {
		return nil, err
	}
	retryDelay, err := optionalDuration(cfg, "retry_delay", defaultRetryDelay)
	if err != nil {
		return nil, err
	}

	proxyURL := optionalString(cfg, "proxy", "")
	client, err := netutil.BuildHTTPClient(proxyURL, timeout)
	if err != nil {
		return nil, fmt.Errorf("webhook proxy: %w", err)
	}

	return &Notifier{
		endpoint:     endpoint,
		host:         endpoint.Host,
		instanceName: optionalString(cfg, "instance_name", ""),
		mode:         mode,
		headers:      headers,
		proxyURL:     proxyURL,
		tmpl:         tmpl,
		client:       client,
		maxAttempts:  maxAttemptsFromRetries(retries),
		retryDelay:   retryDelay,
		sleep:        httpretry.SleepContext,
	}, nil
}

// Notify delivers updates via HTTP POST.
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
	body, err := n.renderBody(events)
	if err != nil {
		slog.Error("webhook render failed", "host", n.host, "error", err)
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}

	if err := n.postWithRetry(ctx, body); err != nil {
		slog.Error("webhook notify failed",
			"host", n.host,
			"count", len(events),
			"error", err,
		)
		return err
	}
	return nil
}

func (n *Notifier) renderBody(events []model.UpdateAvailable) ([]byte, error) {
	if n.tmpl == nil {
		payload := format.PayloadFromEvents(n.instanceName, events)
		return json.Marshal(payload)
	}

	var buf bytes.Buffer
	switch n.mode {
	case modeSimple:
		data := format.ItemFromEvent(n.instanceName, events[0], false)
		if err := n.tmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("execute template: %w", err)
		}
	default:
		payload := format.PayloadFromEvents(n.instanceName, events)
		if err := n.tmpl.Execute(&buf, payload); err != nil {
			return nil, fmt.Errorf("execute template: %w", err)
		}
	}
	return buf.Bytes(), nil
}

func (n *Notifier) postWithRetry(ctx context.Context, body []byte) error {
	cfg := httpretry.Config{
		MaxAttempts:       n.maxAttempts,
		RetryDelay:        n.retryDelay,
		MaxRetryAfter:     httpretry.DefaultMaxRetryAfter,
		BackoffMultiplier: httpretry.DefaultBackoffMultiplier,
		Sleep:             n.sleep,
		LogComponent:      "webhook",
		LogFields:         []any{"host", n.host},
	}

	return httpretry.Do(ctx, cfg, httpretry.DefaultClassifier, func(ctx context.Context) httpretry.AttemptResult {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return httpretry.AttemptResult{Err: fmt.Errorf("create request: %w", err)}
		}

		n.applyHeaders(req)
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}

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

func (n *Notifier) applyHeaders(req *http.Request) {
	for k, vals := range n.headers {
		req.Header.Del(k)
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
}

func parseHeaders(cfg map[string]any) (http.Header, error) {
	raw, ok := cfg["headers"]
	if !ok || raw == nil {
		return make(http.Header), nil
	}

	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("webhook config: headers must be a map")
	}

	header := make(http.Header)
	for k, v := range m {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		val, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("webhook config: headers.%s must be a string", k)
		}
		header.Set(key, os.ExpandEnv(strings.TrimSpace(val)))
	}
	return header, nil
}

func requireString(cfg map[string]any, key string) (string, error) {
	v, ok := cfg[key]
	if !ok || v == nil {
		return "", fmt.Errorf("webhook config: %s is required", key)
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("webhook config: %s must be a non-empty string", key)
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

func optionalTemplate(cfg map[string]any, key string) string {
	v, ok := cfg[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return ""
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
		return 0, fmt.Errorf("webhook config: %s must be a duration string (e.g. \"10s\")", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("webhook config: %s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("webhook config: %s must be positive", key)
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
		return validateRetries(n, key)
	case int64:
		return validateRetries(int(n), key)
	case float64:
		return validateRetries(int(n), key)
	default:
		return 0, fmt.Errorf("webhook config: %s must be an integer", key)
	}
}

func validateRetries(n int, key string) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("webhook config: %s must be non-negative", key)
	}
	return n, nil
}

func maxAttemptsFromRetries(retries int) int {
	if retries <= 1 {
		return 1
	}
	return retries
}
