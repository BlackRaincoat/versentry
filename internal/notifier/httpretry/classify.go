package httpretry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultClassifier applies generic HTTP retry rules (network, 5xx, 429).
func DefaultClassifier(status int, body []byte, retryAfterHeader string, netErr error) Outcome {
	if netErr != nil {
		if errors.Is(netErr, context.Canceled) || errors.Is(netErr, context.DeadlineExceeded) {
			return Outcome{Err: fmt.Errorf("request: %w", netErr), Retryable: false}
		}
		return Outcome{Err: fmt.Errorf("request: %w", netErr), Retryable: true}
	}

	switch {
	case status >= 200 && status < 300:
		return Outcome{}
	case status == http.StatusTooManyRequests:
		return rateLimitOutcome(retryAfterHeader, DefaultMaxRetryAfter, "rate limited (HTTP 429)")
	case status == http.StatusBadRequest:
		return clientOutcome(status, body, "bad request")
	case status == http.StatusUnauthorized:
		return clientOutcome(status, body, "unauthorized")
	case status == http.StatusForbidden:
		return clientOutcome(status, body, "forbidden")
	case status == http.StatusNotFound:
		return clientOutcome(status, body, "not found")
	case status >= 500 && status < 600:
		return Outcome{Err: fmt.Errorf("HTTP %d: server error", status), Retryable: true}
	case status >= 400 && status < 500:
		return clientOutcome(status, body, "client error")
	default:
		if status == 0 {
			return Outcome{Err: errors.New("empty HTTP response"), Retryable: false}
		}
		return Outcome{Err: fmt.Errorf("HTTP %d", status), Retryable: false}
	}
}

func rateLimitOutcome(retryAfterHeader string, maxRetryAfter time.Duration, kind string) Outcome {
	retryAfter := parseRetryAfter(retryAfterHeader)
	if retryAfter > maxRetryAfter {
		return Outcome{
			Err:       fmt.Errorf("%s for %s (exceeds %s cap)", kind, retryAfter, maxRetryAfter),
			Retryable: false,
		}
	}
	if retryAfter <= 0 {
		retryAfter = DefaultRetryDelay
	}
	return Outcome{
		Err:        fmt.Errorf("%s", kind),
		Retryable:  true,
		RetryAfter: retryAfter,
	}
}

func clientOutcome(status int, body []byte, kind string) Outcome {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = kind
	}
	return Outcome{Err: fmt.Errorf("HTTP %d: %s", status, msg), Retryable: false}
}

func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		d := time.Until(when)
		if d > 0 {
			return d
		}
	}
	return 0
}
