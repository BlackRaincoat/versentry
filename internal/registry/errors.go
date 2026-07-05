package registry

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrRateLimited means the registry host is rate-limited for the current pass.
	ErrRateLimited = errors.New("registry: rate limited")
	// ErrUnavailable means a registry request failed after retries (e.g. persistent 5xx).
	ErrUnavailable = errors.New("registry: temporarily unavailable")
)

// RateLimitError is returned for HTTP 429 before a host is marked rate-limited for the pass.
type RateLimitError struct {
	RetryAfter time.Duration // zero when Retry-After header is missing
	Err        error
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("registry: rate limited (retry after %s)", e.RetryAfter)
	}
	return "registry: rate limited"
}

func (e *RateLimitError) Unwrap() error {
	return e.Err
}

// RetryAfterFromError returns Retry-After carried by a RateLimitError.
func RetryAfterFromError(err error) (time.Duration, bool) {
	var rle *RateLimitError
	if errors.As(err, &rle) {
		return rle.RetryAfter, true
	}
	return 0, false
}
