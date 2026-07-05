package oci

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

type retryAfterKey struct{}

// withRetryAfterCapture wraps a transport so HTTP 429 responses attach Retry-After
// to the request context; mapRegistryError promotes it into registry.RateLimitError.
func withRetryAfterCapture(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &retryAfterCapture{base: base}
}

type retryAfterCapture struct {
	base http.RoundTripper
}

func (t *retryAfterCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return resp, err
	}

	ra := parseRetryAfterHeader(resp.Header.Get("Retry-After"))
	ctx := context.WithValue(req.Context(), retryAfterKey{}, ra)
	resp.Request = req.WithContext(ctx)
	return resp, err
}

func parseRetryAfterHeader(raw string) time.Duration {
	raw = trimHeaderValue(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		d := time.Until(when)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

func trimHeaderValue(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func retryAfterFromRequest(req *http.Request) time.Duration {
	if req == nil {
		return 0
	}
	ra, ok := req.Context().Value(retryAfterKey{}).(time.Duration)
	if !ok {
		return 0
	}
	return ra
}
