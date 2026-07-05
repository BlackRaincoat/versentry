package oci

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func TestRetryAfterCapturePromotedToRateLimitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errors":[{"code":"TOOMANYREQUESTS","message":"rate limited"}]}`))
	}))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	reg, err := New(map[string]any{"host": host, "insecure": true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = reg.ListTags(context.Background(), "any/repo")
	if err == nil {
		t.Fatal("expected error")
	}

	var rle *registry.RateLimitError
	if !asRateLimitError(err, &rle) {
		t.Fatalf("expected RateLimitError, got %T: %v", err, err)
	}
	if rle.RetryAfter != 7*time.Second {
		t.Fatalf("RetryAfter = %v, want 7s", rle.RetryAfter)
	}
}

func asRateLimitError(err error, target **registry.RateLimitError) bool {
	for err != nil {
		if rle, ok := err.(*registry.RateLimitError); ok {
			*target = rle
			return true
		}
		err = unwrapOne(err)
	}
	return false
}

func unwrapOne(err error) error {
	type unwrapper interface {
		Unwrap() error
	}
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}

func TestParseRetryAfterHeader(t *testing.T) {
	if got := parseRetryAfterHeader("5"); got != 5*time.Second {
		t.Fatalf("got %v", got)
	}
	if got := parseRetryAfterHeader(""); got != 0 {
		t.Fatalf("empty: got %v", got)
	}
}

func TestMapRegistryError429UsesRequestRetryAfter(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://example/v2/", nil)
	ctx := context.WithValue(req.Context(), retryAfterKey{}, 4*time.Second)
	req = req.WithContext(ctx)

	terr := &transport.Error{StatusCode: http.StatusTooManyRequests, Request: req}
	mapped := mapRegistryError(terr)

	rle, ok := mapped.(*registry.RateLimitError)
	if !ok {
		t.Fatalf("got %T", mapped)
	}
	if rle.RetryAfter != 4*time.Second {
		t.Fatalf("RetryAfter = %v", rle.RetryAfter)
	}
}
