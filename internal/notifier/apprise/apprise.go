package apprise

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
	defaultTimeout = 10 * time.Second
	modeSimple     = "simple"
	modeDigest     = "digest"

	// Markdown; Apprise converts it per destination (HTML mail, plain SMS, …).
	// [url](url): bare https is not clickable everywhere (same as gotify/ntfy).
	defaultItemTemplate = `**{{.Container}}**: {{.Change}}{{if .URL}}
[{{.URL}}]({{.URL}}){{end}}

`
	defaultDigestTemplate = `{{.Items}}`

	typeInfo    = "info"
	typeWarning = "warning"

	// maxLoggedBody caps apprise-api response text in WARN/ERROR lines.
	maxLoggedBody = 300
)

const (
	defaultRetries    = httpretry.DefaultRetries
	defaultRetryDelay = httpretry.DefaultRetryDelay
)

var markdownSpecial = []string{"\\", "*", "_", "~", "`", "[", "]"}

func init() {
	notifier.Register("apprise", New)
}

// Notifier POSTs update messages to an Apprise API server (caronc/apprise-api),
// which fans them out to the services configured there (mail, Slack, Pushover, …).
// Stateful: POST {url}/notify/{key} using URLs saved under key (optional tag).
// Stateless: POST {url}/notify with urls in the payload.
type Notifier struct {
	endpoint     *url.URL
	host         string
	key          string
	urls         string
	tag          string
	username     string
	password     string
	mode         string
	instanceName string
	itemTmpl     *template.Template
	digestTmpl   *template.Template
	client       *http.Client
	maxAttempts  int
	retryDelay   time.Duration
	sleep        httpretry.SleepFunc
}

type notifyPayload struct {
	URLs   string `json:"urls,omitempty"`
	Title  string `json:"title,omitempty"`
	Body   string `json:"body"`
	Type   string `json:"type"`
	Format string `json:"format"`
	Tag    string `json:"tag,omitempty"`
}

// New constructs an Apprise notifier from plugin configuration.
func New(cfg map[string]any) (notifier.Notifier, error) {
	rawURL, err := requireString(cfg, "url")
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("apprise config: url: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("apprise config: url scheme must be http or https")
	}
	if base.Host == "" {
		return nil, fmt.Errorf("apprise config: url must include a host")
	}

	key := optionalString(cfg, "key", "")
	urls, err := optionalURLList(cfg, "urls")
	if err != nil {
		return nil, err
	}
	tag := optionalString(cfg, "tag", "")
	switch {
	case key == "" && urls == "":
		return nil, fmt.Errorf("apprise config: set key (configuration saved in apprise-api) or urls (stateless)")
	case key != "" && urls != "":
		return nil, fmt.Errorf("apprise config: key and urls are mutually exclusive")
	case urls != "" && tag != "":
		return nil, fmt.Errorf("apprise config: tag applies only with key (stateless urls have no tags)")
	}
	if strings.ContainsAny(key, "/?#") {
		return nil, fmt.Errorf("apprise config: key must not contain '/', '?' or '#'")
	}

	username := optionalString(cfg, "username", "")
	password := optionalString(cfg, "password", "")
	if username != "" && password == "" {
		return nil, fmt.Errorf("apprise config: password is required when username is set")
	}

	mode := optionalString(cfg, "mode", modeDigest)
	switch mode {
	case modeSimple, modeDigest:
	default:
		return nil, fmt.Errorf(`apprise config: mode must be %q or %q`, modeSimple, modeDigest)
	}

	itemTmpl, err := compileTemplate("item", optionalTemplate(cfg, "item_template", defaultItemTemplate))
	if err != nil {
		return nil, fmt.Errorf("apprise config: item_template: %w", err)
	}
	digestTmpl, err := compileTemplate("digest", optionalTemplate(cfg, "digest_template", defaultDigestTemplate))
	if err != nil {
		return nil, fmt.Errorf("apprise config: digest_template: %w", err)
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
		return nil, fmt.Errorf("apprise config: retries must be non-negative")
	}
	retryDelay, err := optionalDuration(cfg, "retry_delay", defaultRetryDelay)
	if err != nil {
		return nil, err
	}

	client, err := netutil.BuildHTTPClient(optionalString(cfg, "proxy", ""), timeout)
	if err != nil {
		return nil, fmt.Errorf("apprise proxy: %w", err)
	}

	return &Notifier{
		endpoint:     notifyEndpoint(base, key),
		host:         base.Host,
		key:          key,
		urls:         urls,
		tag:          tag,
		username:     username,
		password:     password,
		mode:         mode,
		instanceName: optionalString(cfg, "instance_name", ""),
		itemTmpl:     itemTmpl,
		digestTmpl:   digestTmpl,
		client:       client,
		maxAttempts:  maxAttemptsFromRetries(retries),
		retryDelay:   retryDelay,
		sleep:        httpretry.SleepContext,
	}, nil
}

// notifyEndpoint is {base}/notify/{key} (stateful) or {base}/notify (stateless).
// A base path is kept (apprise-api behind a reverse proxy under /apprise).
func notifyEndpoint(base *url.URL, key string) *url.URL {
	u := *base
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/") + "/notify"
	if key != "" {
		u.Path += "/" + url.PathEscape(key)
	}
	return &u
}

// Notify sends updates through apprise-api. Digest mode: one message per pass;
// simple mode: one message per update.
func (n *Notifier) Notify(ctx context.Context, events []model.UpdateAvailable) error {
	if len(events) == 0 {
		return nil
	}
	if n.mode == modeDigest {
		return n.sendBatch(ctx, events)
	}
	for _, event := range events {
		if err := n.sendBatch(ctx, []model.UpdateAvailable{event}); err != nil {
			return err
		}
	}
	return nil
}

func (n *Notifier) sendBatch(ctx context.Context, events []model.UpdateAvailable) error {
	text, err := n.renderDigest(events)
	if err != nil {
		slog.Error("apprise render failed", "host", n.host, "error", err)
		return err
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}

	body, err := json.Marshal(notifyPayload{
		URLs:   n.urls,
		Title:  messageTitle(n.instanceName, len(events)),
		Body:   text,
		Type:   messageType(events),
		Format: "markdown",
		Tag:    n.tag,
	})
	if err != nil {
		return fmt.Errorf("apprise encode: %w", err)
	}

	partial, err := n.postWithRetry(ctx, body)
	if err != nil {
		slog.Error("apprise notify failed", "host", n.host, "count", len(events), "error", err)
		return err
	}
	if partial != "" {
		// 424: apprise-api ran the fan-out but at least one destination failed
		// (or no URL matched the tag). Treated as delivered: re-sending would
		// duplicate the message on every destination that did work.
		slog.Warn("apprise: some destinations failed (HTTP 424), not retried to avoid duplicates; check apprise-api logs",
			"host", n.host,
			"count", len(events),
			"response", partial,
		)
	}
	return nil
}

// postWithRetry returns a non-empty partial (response text) on HTTP 424.
func (n *Notifier) postWithRetry(ctx context.Context, body []byte) (string, error) {
	cfg := httpretry.Config{
		MaxAttempts:       n.maxAttempts,
		RetryDelay:        n.retryDelay,
		MaxRetryAfter:     httpretry.DefaultMaxRetryAfter,
		BackoffMultiplier: httpretry.DefaultBackoffMultiplier,
		Sleep:             n.sleep,
		LogComponent:      "apprise",
		LogFields:         []any{"host", n.host},
	}

	partial := ""
	classify := func(status int, respBody []byte, retryAfter string, netErr error) httpretry.Outcome {
		if netErr == nil && status == http.StatusFailedDependency {
			partial = responseSummary(respBody)
			return httpretry.Outcome{}
		}
		return httpretry.DefaultClassifier(status, respBody, retryAfter, netErr)
	}

	err := httpretry.Do(ctx, cfg, classify, func(ctx context.Context) httpretry.AttemptResult {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return httpretry.AttemptResult{Err: fmt.Errorf("create request: %w", err)}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if n.password != "" {
			req.SetBasicAuth(n.username, n.password)
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
	return partial, err
}

// messageTitle names the host; destinations without titles (SMS) drop it.
func messageTitle(instance string, count int) string {
	name := instance
	if name == "" {
		name = "Versentry"
	}
	if count <= 1 {
		return name + ": update available"
	}
	return fmt.Sprintf("%s: %d updates available", name, count)
}

// messageType is warning when the batch has a major bump or a reminder, so
// destinations that style by type (icon, color, priority) set those apart.
func messageType(events []model.UpdateAvailable) string {
	for _, e := range events {
		if e.Bump == model.BumpMajor || e.Reminder {
			return typeWarning
		}
	}
	return typeInfo
}

func responseSummary(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if s == "" {
		return "no details"
	}
	if len(s) > maxLoggedBody {
		s = s[:maxLoggedBody] + "…"
	}
	return s
}

func compileTemplate(name, body string) (*template.Template, error) {
	return template.New(name).Option("missingkey=zero").Parse(body)
}

func (n *Notifier) renderDigest(events []model.UpdateAvailable) (string, error) {
	items := make([]string, 0, len(events))
	for _, event := range events {
		var buf bytes.Buffer
		if err := n.itemTmpl.Execute(&buf, itemDataMarkdown(n.instanceName, event)); err != nil {
			return "", fmt.Errorf("execute item_template: %w", err)
		}
		if buf.Len() > 0 {
			items = append(items, buf.String())
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

// itemDataMarkdown escapes field values for markdown; URL stays raw for [url](url).
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
		return "", fmt.Errorf("apprise config: %s is required", key)
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("apprise config: %s must be a non-empty string", key)
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

// optionalURLList accepts a string ("a://…, b://…") or a YAML list; returns the
// comma-joined form apprise-api expects.
func optionalURLList(cfg map[string]any, key string) (string, error) {
	v, ok := cfg[key]
	if !ok || v == nil {
		return "", nil
	}
	var parts []string
	switch t := v.(type) {
	case string:
		parts = []string{t}
	case []string:
		parts = t
	case []any:
		for i, item := range t {
			s, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("apprise config: %s[%d] must be a string", key, i)
			}
			parts = append(parts, s)
		}
	default:
		return "", fmt.Errorf("apprise config: %s must be a string or a list of strings", key)
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", "), nil
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
		return 0, fmt.Errorf("apprise config: %s must be a duration string (e.g. \"10s\")", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("apprise config: %s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("apprise config: %s must be positive", key)
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
		return 0, fmt.Errorf("apprise config: %s must be an integer", key)
	}
}

func maxAttemptsFromRetries(retries int) int {
	if retries <= 1 {
		return 1
	}
	return retries
}
