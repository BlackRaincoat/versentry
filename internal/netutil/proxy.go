package netutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"
	xproxy "golang.org/x/net/proxy"
)

// BuildTransport returns an HTTP transport for registry/API clients.
// proxyURL supports socks5/socks and http/https schemes. Empty proxy uses
// go-containerregistry defaults (including HTTP_PROXY env for direct HTTP proxies).
func BuildTransport(proxyURL string) (http.RoundTripper, error) {
	base, ok := remote.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("unexpected default transport type %T", remote.DefaultTransport)
	}
	transport := base.Clone()

	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return transport, nil
	}

	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy URL: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks":
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		socksDialer, err := xproxy.FromURL(u, dialer)
		if err != nil {
			return nil, fmt.Errorf("create socks proxy dialer: %w", err)
		}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if cd, ok := socksDialer.(xproxy.ContextDialer); ok {
				return cd.DialContext(ctx, network, addr)
			}
			return socksDialer.Dial(network, addr)
		}
	case "http", "https":
		transport.Proxy = http.ProxyURL(u)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}

	return transport, nil
}

// BuildHTTPClient wraps BuildTransport with a request timeout.
func BuildHTTPClient(proxyURL string, timeout time.Duration) (*http.Client, error) {
	transport, err := BuildTransport(proxyURL)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}, nil
}

// MaskProxyURL redacts the password in a proxy URL for safe logging.
func MaskProxyURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "***"
	}
	if u.User == nil {
		return u.String()
	}
	user := u.User.Username()
	if _, hasPass := u.User.Password(); hasPass {
		u.User = url.UserPassword(user, "***")
	}
	return u.String()
}
