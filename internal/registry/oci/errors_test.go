package oci

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestSanitizeRegistryErrStripsURLQuotes(t *testing.T) {
	err := &url.Error{
		Op:  "Get",
		URL: "https://index.docker.io/v2/library/redis/tags/list",
		Err: context.DeadlineExceeded,
	}
	got := sanitizeRegistryErr(err)
	if strings.Contains(got, `\"`) || strings.Contains(got, `"`) {
		t.Fatalf("sanitize still has quotes: %q", got)
	}
	if !strings.Contains(got, "Get https://index.docker.io/v2/library/redis/tags/list") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "context deadline exceeded") {
		t.Fatalf("want cause, got %q", got)
	}
}

func TestIsResponseHeaderTimeout(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"http1", errors.New("net/http: timeout awaiting response headers"), true},
		{"http2", errors.New("http2: timeout awaiting response headers"), true},
		{"wrapped", &url.Error{Op: "Get", URL: "https://example.com/v2/", Err: errors.New("http2: timeout awaiting response headers")}, true},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"tls", errors.New("net/http: TLS handshake timeout"), false},
		{"dial", errors.New("dial tcp: i/o timeout"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isResponseHeaderTimeout(tc.err); got != tc.want {
				t.Fatalf("isResponseHeaderTimeout(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRepoFromRegistryPath(t *testing.T) {
	cases := []struct {
		path, want string
	}{
		{"/v2/library/redis/tags/list", "library/redis"},
		{"/v2/library/postgres/manifests/16", "library/postgres"},
		{"/v2/foo/bar/blobs/sha256:abc", "foo/bar"},
	}
	for _, tc := range cases {
		if got := repoFromRegistryPath(tc.path); got != tc.want {
			t.Fatalf("repoFromRegistryPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestApplyRegistryIOTimeoutsWrapsDial(t *testing.T) {
	called := false
	base := &http.Transport{}
	base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		called = true
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("wrapped dial context missing deadline")
		} else if time.Until(deadline) > registryDialTimeout+time.Second {
			t.Errorf("dial deadline too loose: until %v", time.Until(deadline))
		}
		return nil, errors.New("dial stub")
	}

	out, ok := applyRegistryIOTimeouts(base).(*http.Transport)
	if !ok {
		t.Fatalf("want *http.Transport, got %T", applyRegistryIOTimeouts(base))
	}
	if out.ResponseHeaderTimeout != registryResponseHeaderTimeout {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", out.ResponseHeaderTimeout, registryResponseHeaderTimeout)
	}
	if out.TLSHandshakeTimeout != registryTLSHandshakeTimeout {
		t.Fatalf("TLSHandshakeTimeout = %v", out.TLSHandshakeTimeout)
	}

	_, _ = out.DialContext(context.Background(), "tcp", "example.com:443")
	if !called {
		t.Fatal("expected wrapped DialContext to call original dialer")
	}
}

func TestApplyRegistryIOTimeoutsKeepsIdleConnTimeout(t *testing.T) {
	base, ok := remote.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("default transport is not *http.Transport")
	}
	out, ok := applyRegistryIOTimeouts(base).(*http.Transport)
	if !ok {
		t.Fatal("want *http.Transport")
	}
	if out.IdleConnTimeout != base.IdleConnTimeout {
		t.Fatalf("IdleConnTimeout changed: got %v want %v", out.IdleConnTimeout, base.IdleConnTimeout)
	}
}
