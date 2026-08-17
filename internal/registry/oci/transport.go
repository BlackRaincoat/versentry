package oci

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Per-attempt I/O budgets for registry HTTP. Separate from timeouts.registry
// (overall ListTags/TagDigest ceiling in the engine): a stuck TCP/TLS socket
// must not burn the full 30s when siblings to the same host succeed.
var (
	registryDialTimeout           = 5 * time.Second
	registryTLSHandshakeTimeout   = 8 * time.Second
	registryResponseHeaderTimeout = 10 * time.Second
)

// applyRegistryIOTimeouts clones an *http.Transport and sets short dial /
// TLS / first-response timeouts. Slow-but-alive bodies are unaffected once
// headers arrive (ResponseHeaderTimeout stops at headers). IdleConnTimeout
// is left as cloned (gcr default 90s). Existing DialContext (e.g. SOCKS from
// netutil.BuildTransport) is wrapped, not replaced.
func applyRegistryIOTimeouts(base http.RoundTripper) http.RoundTripper {
	var tr *http.Transport
	switch b := base.(type) {
	case *http.Transport:
		tr = b.Clone()
	default:
		def, ok := remote.DefaultTransport.(*http.Transport)
		if !ok {
			return base
		}
		tr = def.Clone()
	}

	innerDial := tr.DialContext
	if innerDial == nil {
		innerDial = (&net.Dialer{
			Timeout:   registryDialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext
	} else {
		prev := innerDial
		innerDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			dctx, cancel := context.WithTimeout(ctx, registryDialTimeout)
			defer cancel()
			return prev(dctx, network, addr)
		}
	}
	tr.DialContext = innerDial
	tr.TLSHandshakeTimeout = registryTLSHandshakeTimeout
	tr.ResponseHeaderTimeout = registryResponseHeaderTimeout
	return tr
}
