// Package heartbeat pings an external dead-man's-switch (Healthchecks.io,
// Uptime Kuma push monitor, Better Stack, Cronitor, …) after scheduled checks,
// so silence from versentry itself becomes an alert.
package heartbeat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/netutil"
	"github.com/BlackRaincoat/versentry/internal/notifier/httpretry"
)

const (
	defaultTimeout = 10 * time.Second
	maxAttempts    = 2
)

// Pinger sends the success / failure ping. A nil *Pinger is valid and does nothing.
type Pinger struct {
	okURL   string
	failURL string
	host    string
	client  *http.Client
	log     *slog.Logger
	sleep   httpretry.SleepFunc
}

// New returns nil when no heartbeat URL is configured.
func New(cfg config.HeartbeatConfig, log *slog.Logger) (*Pinger, error) {
	if cfg.URL == "" {
		return nil, nil
	}
	if log == nil {
		log = slog.Default()
	}
	timeout := cfg.Timeout.Duration
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	client, err := netutil.BuildHTTPClient(cfg.Proxy, timeout)
	if err != nil {
		return nil, fmt.Errorf("heartbeat proxy: %w", err)
	}
	host := ""
	if u, err := url.Parse(cfg.URL); err == nil {
		host = u.Host
	}
	return &Pinger{
		okURL:   cfg.URL,
		failURL: cfg.FailURL,
		host:    host,
		client:  client,
		log:     log,
		sleep:   httpretry.SleepContext,
	}, nil
}

// Report pings url on success or fail_url on failure, with summary as a
// text/plain POST body (Healthchecks.io shows it; push monitors ignore it).
// Without fail_url a failure sends nothing: the missed success ping alerts
// after the monitor's grace period. Delivery problems are logged, never fatal.
// The URLs carry secrets (check UUID, push token): only the host is logged.
func (p *Pinger) Report(ctx context.Context, ok bool, summary string) {
	if p == nil || ctx.Err() != nil {
		return
	}
	target, status := p.okURL, "ok"
	if !ok {
		if p.failURL == "" {
			p.log.Info("heartbeat: check failed, no fail_url — the missed ping will alert", "host", p.host)
			return
		}
		target, status = p.failURL, "fail"
	}

	cfg := httpretry.Config{
		MaxAttempts:  maxAttempts,
		RetryDelay:   time.Second,
		Sleep:        p.sleep,
		LogComponent: "heartbeat",
		LogFields:    []any{"host", p.host},
	}
	err := httpretry.Do(ctx, cfg, retryTimeouts(ctx), func(ctx context.Context) httpretry.AttemptResult {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader([]byte(summary)))
		if err != nil {
			return httpretry.AttemptResult{Err: fmt.Errorf("create request: %w", err)}
		}
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		resp, err := p.client.Do(req)
		if err != nil {
			return httpretry.AttemptResult{Err: err}
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return httpretry.AttemptResult{StatusCode: resp.StatusCode, Body: body, RetryAfter: resp.Header.Get("Retry-After")}
	})
	if err != nil {
		p.log.Warn("heartbeat ping failed", "host", p.host, "status", status, "error", err)
		return
	}
	p.log.Info("heartbeat sent", "host", p.host, "status", status)
}

// retryTimeouts is DefaultClassifier plus a retry when one attempt hit the
// client timeout while the pass is still live. DefaultClassifier never retries
// a deadline: for notifiers the POST may already have been delivered and a
// repeat would duplicate the message. A repeated heartbeat ping is harmless,
// and a slow response is its most likely transient failure (seen on ntfy.sh).
// Parent cancel (SIGTERM) still stops at once.
func retryTimeouts(ctx context.Context) httpretry.Classifier {
	return func(status int, body []byte, retryAfter string, netErr error) httpretry.Outcome {
		out := httpretry.DefaultClassifier(status, body, retryAfter, netErr)
		if out.Err != nil && !out.Retryable && netErr != nil && ctx.Err() == nil &&
			errors.Is(netErr, context.DeadlineExceeded) {
			out.Retryable = true
		}
		return out
	}
}
