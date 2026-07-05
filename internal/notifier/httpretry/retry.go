package httpretry

import (
	"context"
	"log/slog"
	"time"
)

const (
	DefaultRetries           = 3
	DefaultRetryDelay        = time.Second
	DefaultMaxRetryAfter     = 60 * time.Second
	DefaultBackoffMultiplier = 2
)

// SleepFunc waits for d or until ctx is canceled.
type SleepFunc func(ctx context.Context, d time.Duration) error

// Config controls HTTP retry behavior.
type Config struct {
	MaxAttempts       int
	RetryDelay        time.Duration
	MaxRetryAfter     time.Duration
	BackoffMultiplier int
	Sleep             SleepFunc
	LogComponent      string
	LogFields         []any
}

// AttemptResult is the outcome of one HTTP attempt.
type AttemptResult struct {
	StatusCode int
	Body       []byte
	RetryAfter string // raw Retry-After header value
	Err        error
}

// Outcome classifies a single HTTP attempt.
type Outcome struct {
	Err        error
	Retryable  bool
	RetryAfter time.Duration
}

// Classifier evaluates one HTTP attempt. Success is Outcome{Err: nil}.
type Classifier func(status int, body []byte, retryAfterHeader string, netErr error) Outcome

// Do executes attempt up to MaxAttempts times with exponential backoff and 429 Retry-After.
func Do(ctx context.Context, cfg Config, classify Classifier, attempt func(ctx context.Context) AttemptResult) error {
	cfg = cfg.withDefaults()
	var lastErr error

	for try := 1; try <= cfg.MaxAttempts; try++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		res := attempt(ctx)
		out := classify(res.StatusCode, res.Body, res.RetryAfter, res.Err)
		if out.Err == nil {
			return nil
		}
		lastErr = out.Err

		if !out.Retryable || try == cfg.MaxAttempts {
			break
		}

		delay := delayForAttempt(cfg, try, out.RetryAfter)
		fields := append([]any{
			"component", cfg.LogComponent,
			"attempt", try,
			"max_attempts", cfg.MaxAttempts,
			"delay", delay,
			"error", lastErr,
		}, cfg.LogFields...)
		slog.Warn(cfg.LogComponent+" attempt failed, retrying", fields...)

		if err := cfg.Sleep(ctx, delay); err != nil {
			return err
		}
	}

	return lastErr
}

func (cfg Config) withDefaults() Config {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultRetries
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = DefaultRetryDelay
	}
	if cfg.MaxRetryAfter <= 0 {
		cfg.MaxRetryAfter = DefaultMaxRetryAfter
	}
	if cfg.BackoffMultiplier <= 0 {
		cfg.BackoffMultiplier = DefaultBackoffMultiplier
	}
	if cfg.Sleep == nil {
		cfg.Sleep = SleepContext
	}
	if cfg.LogComponent == "" {
		cfg.LogComponent = "http"
	}
	return cfg
}

func delayForAttempt(cfg Config, attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	delay := cfg.RetryDelay
	for i := 1; i < attempt; i++ {
		delay *= time.Duration(cfg.BackoffMultiplier)
	}
	return delay
}

// SleepContext waits for d or until ctx is canceled.
func SleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
