package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/notifier"
	"github.com/BlackRaincoat/versentry/internal/notifier/httpretry"
)

const (
	defaultTimeout = 10 * time.Second
	modeSimple     = "simple"
	modeDigest     = "digest"
)

type sleepFunc = httpretry.SleepFunc

const (
	defaultRetries    = httpretry.DefaultRetries
	defaultRetryDelay = httpretry.DefaultRetryDelay
)

func init() {
	notifier.Register("telegram", New)
}

// Notifier sends update events to a Telegram chat via Bot API.
type Notifier struct {
	token        string
	chatID       string
	parseMode    string
	apiURL       string
	proxyURL     string
	instanceName string
	mode         string
	itemTmpl     *template.Template
	digestTmpl   *template.Template
	client       *http.Client
	maxAttempts  int
	retryDelay   time.Duration
	sleep        sleepFunc
}

// New constructs a Telegram notifier from plugin configuration.
func New(cfg map[string]any) (notifier.Notifier, error) {
	token, err := requireString(cfg, "token")
	if err != nil {
		return nil, err
	}
	chatID, err := requireChatID(cfg)
	if err != nil {
		return nil, err
	}

	parseMode := optionalString(cfg, "parse_mode", "HTML")
	apiURL := strings.TrimRight(optionalString(cfg, "api_url", "https://api.telegram.org"), "/")
	proxyURL := optionalString(cfg, "proxy", "")
	mode := optionalString(cfg, "mode", modeDigest)
	switch mode {
	case modeSimple, modeDigest:
	default:
		return nil, fmt.Errorf(`telegram config: mode must be %q or %q`, modeSimple, modeDigest)
	}

	// Preserve trailing newlines in templates (YAML |+ keep-chomp).
	itemBody := optionalTemplate(cfg, "item_template", defaultItemTemplate)
	digestBody := optionalTemplate(cfg, "digest_template", defaultDigestTemplate)

	itemTmpl, err := compileTemplate("item", itemBody)
	if err != nil {
		return nil, fmt.Errorf("telegram config: item_template: %w", err)
	}
	digestTmpl, err := compileTemplate("digest", digestBody)
	if err != nil {
		return nil, fmt.Errorf("telegram config: digest_template: %w", err)
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

	client, err := buildHTTPClient(proxyURL, timeout)
	if err != nil {
		return nil, fmt.Errorf("telegram proxy: %w", err)
	}

	return &Notifier{
		token:        token,
		chatID:       chatID,
		parseMode:    parseMode,
		apiURL:       apiURL,
		proxyURL:     proxyURL,
		instanceName: optionalString(cfg, "instance_name", ""),
		mode:         mode,
		itemTmpl:     itemTmpl,
		digestTmpl:   digestTmpl,
		client:       client,
		maxAttempts:  maxAttemptsFromRetries(retries),
		retryDelay:   retryDelay,
		sleep:        httpretry.SleepContext,
	}, nil
}

// Notify delivers a batch of updates. Simple mode sends one message per update
// (each as a one-item digest); digest mode sends a single summary message.
func (n *Notifier) Notify(ctx context.Context, events []model.UpdateAvailable) error {
	if len(events) == 0 {
		return nil
	}

	switch n.mode {
	case modeDigest:
		return n.sendBatch(ctx, events)
	default: // simple — one message per update (one-item digest: instance header + item line)
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
		slog.Error("telegram render failed",
			"proxy", maskProxyURL(n.proxyURL),
			"error", err,
		)
		return err
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}

	if err := n.sendMessageWithRetry(ctx, text); err != nil {
		err = n.redactErr(err)
		slog.Error("telegram notify failed",
			"count", len(events),
			"proxy", maskProxyURL(n.proxyURL),
			"error", err,
		)
		return err
	}
	return nil
}

func requireString(cfg map[string]any, key string) (string, error) {
	v, ok := cfg[key]
	if !ok || v == nil {
		return "", fmt.Errorf("telegram config: %s is required", key)
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("telegram config: %s must be a non-empty string", key)
	}
	return strings.TrimSpace(s), nil
}

func requireChatID(cfg map[string]any) (string, error) {
	v, ok := cfg["chat_id"]
	if !ok || v == nil {
		return "", fmt.Errorf("telegram config: chat_id is required")
	}
	switch id := v.(type) {
	case string:
		if strings.TrimSpace(id) == "" {
			return "", fmt.Errorf("telegram config: chat_id must be non-empty")
		}
		return strings.TrimSpace(id), nil
	case int:
		return fmt.Sprintf("%d", id), nil
	case int64:
		return fmt.Sprintf("%d", id), nil
	case float64:
		return fmt.Sprintf("%.0f", id), nil
	default:
		return "", fmt.Errorf("telegram config: chat_id must be a string or number")
	}
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

// optionalTemplate reads a template string without stripping trailing newlines,
// so YAML block scalars with |+ can keep a blank line between digest items.
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
		return 0, fmt.Errorf("telegram config: %s must be a duration string (e.g. \"10s\")", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("telegram config: %s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("telegram config: %s must be positive", key)
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
		return 0, fmt.Errorf("telegram config: %s must be an integer", key)
	}
}

func validateRetries(n int, key string) (int, error) {
	if n < 0 {
		return 0, fmt.Errorf("telegram config: %s must be non-negative", key)
	}
	return n, nil
}

func maxAttemptsFromRetries(retries int) int {
	if retries <= 1 {
		return 1
	}
	return retries
}
