package oci

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BlackRaincoat/versentry/internal/netutil"
	"github.com/BlackRaincoat/versentry/internal/registry"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func init() {
	registry.Register("oci", New)
}

// Registry talks to any OCI Distribution Spec / Docker Registry v2 host.
// Host and credentials come from plugin configuration.
type Registry struct {
	host      string
	auth      authn.Authenticator
	insecure  bool
	transport http.RoundTripper
}

// New constructs a parameterized OCI registry client.
// host is required. Optional username+token enable private images.
// insecure enables HTTP (no TLS) for the configured host.
func New(cfg map[string]any) (registry.Registry, error) {
	host := optionalString(cfg, "host")
	if host == "" {
		return nil, fmt.Errorf("oci config: host is required")
	}

	auth, err := buildAuth(cfg)
	if err != nil {
		return nil, err
	}

	var rt http.RoundTripper
	if proxy := optionalString(cfg, "proxy"); proxy != "" {
		rt, err = netutil.BuildTransport(proxy)
		if err != nil {
			return nil, fmt.Errorf("oci config proxy: %w", err)
		}
	}

	return &Registry{
		host:      host,
		auth:      auth,
		insecure:  optionalBool(cfg, "insecure", false),
		transport: rt,
	}, nil
}

// Host returns the configured registry host.
func (r *Registry) Host() string {
	return r.host
}

// TagDigest resolves the manifest digest for repo:tag.
func (r *Registry) TagDigest(ctx context.Context, repo, tag string) (string, error) {
	ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", r.host, repo, tag), r.nameOpts()...)
	if err != nil {
		return "", fmt.Errorf("parse reference %s/%s:%s: %w", r.host, repo, tag, err)
	}

	desc, err := remote.Get(ref, r.remoteOpts(ctx)...)
	if err != nil {
		return "", mapRegistryError(err)
	}

	return strings.TrimPrefix(desc.Digest.String(), "sha256:"), nil
}

// ListTags returns all tags for a repository.
func (r *Registry) ListTags(ctx context.Context, repo string) ([]string, error) {
	ref, err := name.NewRepository(fmt.Sprintf("%s/%s", r.host, repo), r.nameOpts()...)
	if err != nil {
		return nil, fmt.Errorf("parse repository %s/%s: %w", r.host, repo, err)
	}

	tags, err := remote.List(ref, r.remoteOpts(ctx)...)
	if err != nil {
		return nil, mapRegistryError(err)
	}

	return tags, nil
}

func (r *Registry) nameOpts() []name.Option {
	opts := []name.Option{name.WeakValidation}
	if r.insecure {
		opts = append(opts, name.Insecure)
	}
	return opts
}

func (r *Registry) remoteOpts(ctx context.Context) []remote.Option {
	base := r.transport
	if base == nil {
		base = http.DefaultTransport
	}
	return []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuth(r.auth),
		remote.WithTransport(withRetryAfterCapture(base)),
	}
}

func buildAuth(cfg map[string]any) (authn.Authenticator, error) {
	if cfg == nil {
		return authn.Anonymous, nil
	}

	username := optionalString(cfg, "username")
	token := optionalString(cfg, "token")

	switch {
	case username == "" && token == "":
		return authn.Anonymous, nil
	case username == "" || token == "":
		return nil, fmt.Errorf("oci config: username and token must both be set for private images")
	default:
		return authn.FromConfig(authn.AuthConfig{
			Username: username,
			Password: token,
		}), nil
	}
}

func optionalString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	v, ok := cfg[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func optionalBool(cfg map[string]any, key string, fallback bool) bool {
	if cfg == nil {
		return fallback
	}
	v, ok := cfg[key]
	if !ok || v == nil {
		return fallback
	}
	b, ok := v.(bool)
	if !ok {
		return fallback
	}
	return b
}

// mapRegistryError maps transport errors to registry sentinels.
// Some registries return 401 for missing or inaccessible repos (not only 404).
func mapRegistryError(err error) error {
	var terr *transport.Error
	if errors.As(err, &terr) {
		switch terr.StatusCode {
		case 404:
			return registry.ErrNotFound
		case 401, 403:
			return registry.ErrUnauthorized
		case 429:
			return &registry.RateLimitError{
				RetryAfter: retryAfterFromRequest(terr.Request),
				Err:        err,
			}
		}
	}
	return err
}
