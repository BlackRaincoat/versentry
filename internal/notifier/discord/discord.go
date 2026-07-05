package discord

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
	"github.com/BlackRaincoat/versentry/internal/notifier"
	"github.com/BlackRaincoat/versentry/internal/notifier/format"
	"github.com/BlackRaincoat/versentry/internal/notifier/httpretry"
)

const (
	defaultTimeout = 10 * time.Second
	modeSimple     = "simple"
	modeDigest     = "digest"
	formatEmbed    = "embed"
	formatContent  = "content"
)

const (
	defaultRetries    = httpretry.DefaultRetries
	defaultRetryDelay = httpretry.DefaultRetryDelay
)

func init() {
	notifier.Register("discord", New)
}

// Notifier POSTs update batches to a Discord webhook URL.
type Notifier struct {
	endpoint     *url.URL
	host         string
	instanceName string
	mode         string
	format       string
	color        int
	username     string
	tmpl         *template.Template
	client       *http.Client
	maxAttempts  int
	retryDelay   time.Duration
	sleep        httpretry.SleepFunc
}

// New constructs a Discord notifier from plugin configuration.
func New(cfg map[string]any) (notifier.Notifier, error) {
	rawURL, err := requireString(cfg, "url")
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("discord config: url: %w", err)
	}
	if endpoint.Scheme != "https" {
		return nil, fmt.Errorf("discord config: url scheme must be https")
	}
	if endpoint.Host == "" {
		return nil, fmt.Errorf("discord config: url must include a host")
	}
	if !isDiscordHost(endpoint.Hostname()) {
		return nil, fmt.Errorf("discord config: url host must be a Discord domain")
	}

	mode := optionalString(cfg, "mode", modeDigest)
	switch mode {
	case modeSimple, modeDigest:
	default:
		return nil, fmt.Errorf(`discord config: mode must be %q or %q`, modeSimple, modeDigest)
	}

	formatName := optionalString(cfg, "format", formatEmbed)
	switch formatName {
	case formatEmbed, formatContent:
	default:
		return nil, fmt.Errorf(`discord config: format must be %q or %q`, formatEmbed, formatContent)
	}

	color, err := optionalColor(cfg, "color", defaultEmbedColor)
	if err != nil {
		return nil, err
	}

	var tmpl *template.Template
	if body := optionalTemplate(cfg, "template"); body != "" {
		tmpl, err = template.New("discord").Option("missingkey=zero").Parse(body)
		if err != nil {
			return nil, fmt.Errorf("discord config: template: %w", err)
		}
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

	client := &http.Client{Timeout: timeout}

	return &Notifier{
		endpoint:     endpoint,
		host:         endpoint.Hostname(),
		instanceName: optionalString(cfg, "instance_name", ""),
		mode:         mode,
		format:       formatName,
		color:        color,
		username:     optionalString(cfg, "username", ""),
		tmpl:         tmpl,
		client:       client,
		maxAttempts:  maxAttemptsFromRetries(retries),
		retryDelay:   retryDelay,
		sleep:        httpretry.SleepContext,
	}, nil
}

// Notify delivers updates via Discord webhook POST.
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
	bodies, err := n.renderBodies(events)
	if err != nil {
		slog.Error("discord render failed", "host", n.host, "error", err)
		return err
	}
	for _, body := range bodies {
		if len(bytes.TrimSpace(body)) == 0 {
			continue
		}
		if err := n.postWithRetry(ctx, body); err != nil {
			slog.Error("discord notify failed",
				"host", n.host,
				"count", len(events),
				"error", err,
			)
			return err
		}
	}
	return nil
}

func (n *Notifier) renderBodies(events []model.UpdateAvailable) ([][]byte, error) {
	if n.tmpl != nil {
		return n.renderTemplateBodies(events)
	}

	simple := n.mode == modeSimple
	if n.format == formatContent {
		return buildContentMessages(events, n.instanceName, n.username, simple)
	}

	var embeds []Embed
	if simple {
		embeds = buildSimpleEmbeds(events, n.instanceName, n.color)
	} else {
		embeds = buildDigestEmbeds(events, n.instanceName, n.color)
	}

	batches := packEmbedsIntoMessages(embeds)
	out := make([][]byte, 0, len(batches))
	for _, batch := range batches {
		body, err := payloadFromEmbeds(batch, n.username)
		if err != nil {
			return nil, err
		}
		out = append(out, body)
	}
	return out, nil
}

func (n *Notifier) renderTemplateBodies(events []model.UpdateAvailable) ([][]byte, error) {
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
	return [][]byte{buf.Bytes()}, nil
}

func (n *Notifier) postWithRetry(ctx context.Context, body []byte) error {
	cfg := httpretry.Config{
		MaxAttempts:       n.maxAttempts,
		RetryDelay:        n.retryDelay,
		MaxRetryAfter:     httpretry.DefaultMaxRetryAfter,
		BackoffMultiplier: httpretry.DefaultBackoffMultiplier,
		Sleep:             n.sleep,
		LogComponent:      "discord",
		LogFields:         []any{"host", n.host},
	}

	return httpretry.Do(ctx, cfg, discordClassifier, func(ctx context.Context) httpretry.AttemptResult {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return httpretry.AttemptResult{Err: fmt.Errorf("create request: %w", err)}
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := n.client.Do(req)
		if err != nil {
			return httpretry.AttemptResult{Err: err}
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return httpretry.AttemptResult{Err: fmt.Errorf("read response: %w", err)}
		}

		retryAfter := resp.Header.Get("Retry-After")
		if retryAfter == "" && resp.StatusCode == http.StatusTooManyRequests {
			retryAfter = retryAfterFromJSON(respBody)
		}

		return httpretry.AttemptResult{
			StatusCode: resp.StatusCode,
			Body:       respBody,
			RetryAfter: retryAfter,
		}
	})
}

type discordRateLimitResponse struct {
	RetryAfter float64 `json:"retry_after"`
}

func discordClassifier(status int, body []byte, retryAfterHeader string, netErr error) httpretry.Outcome {
	return httpretry.DefaultClassifier(status, body, retryAfterHeader, netErr)
}

func retryAfterFromJSON(body []byte) string {
	var parsed discordRateLimitResponse
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.RetryAfter <= 0 {
		return ""
	}
	seconds := int(parsed.RetryAfter)
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d", seconds)
}

func isDiscordHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "discord.com" ||
		host == "discordapp.com" ||
		strings.HasSuffix(host, ".discord.com") ||
		strings.HasSuffix(host, ".discordapp.com")
}

func requireString(cfg map[string]any, key string) (string, error) {
	v, ok := cfg[key]
	if !ok || v == nil {
		return "", fmt.Errorf("discord config: %s is required", key)
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("discord config: %s must be a non-empty string", key)
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
		return 0, fmt.Errorf("discord config: %s must be a duration string (e.g. \"10s\")", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("discord config: %s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("discord config: %s must be positive", key)
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
		return 0, fmt.Errorf("discord config: %s must be an integer", key)
	}
}

func optionalColor(cfg map[string]any, key string, fallback int) (int, error) {
	v, ok := cfg[key]
	if !ok || v == nil {
		return fallback, nil
	}
	switch n := v.(type) {
	case int:
		return validateColor(n, key)
	case int64:
		return validateColor(int(n), key)
	case float64:
		return validateColor(int(n), key)
	default:
		return 0, fmt.Errorf("discord config: %s must be an integer", key)
	}
}

func validateColor(n int, key string) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("discord config: %s must be non-negative", key)
	}
	return n, nil
}

func validateRetries(n int, key string) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("discord config: %s must be non-negative", key)
	}
	return n, nil
}

func maxAttemptsFromRetries(retries int) int {
	if retries <= 1 {
		return 1
	}
	return retries
}
