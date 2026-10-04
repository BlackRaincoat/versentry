package oci

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/BlackRaincoat/versentry/internal/netutil"
	"github.com/BlackRaincoat/versentry/internal/registry"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func init() {
	registry.Register("oci", New)
}

var _ registry.ImageCreatedReader = (*Registry)(nil)

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

	rt, err := netutil.BuildTransport(optionalString(cfg, "proxy"))
	if err != nil {
		return nil, fmt.Errorf("oci transport: %w", err)
	}
	rt = applyRegistryIOTimeouts(rt)

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
// Same per-attempt dial/header timeouts as ListTags.
func (r *Registry) TagDigest(ctx context.Context, repo, tag string) (string, error) {
	return r.tagDigest(ctx, repo, tag, slog.Default())
}

func (r *Registry) tagDigest(ctx context.Context, repo, tag string, log *slog.Logger) (string, error) {
	digest, err := r.tagDigestOnce(ctx, repo, tag, log)
	if err != nil {
		return "", mapRegistryError(err)
	}
	return digest, nil
}

// tagDigestOnce issues HEAD first: Docker Hub counts GET on /manifests/ as a
// pull against the (per-IP, anonymous) rate limit shared with the host's own
// docker pull; HEAD is not counted. Both send the same Accept list, so the
// digest is the same top-level object (index for multi-arch) as a GET.
// GET is used only when the registry cannot answer HEAD usefully.
func (r *Registry) tagDigestOnce(ctx context.Context, repo, tag string, log *slog.Logger) (string, error) {
	ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", r.host, repo, tag), r.nameOpts()...)
	if err != nil {
		return "", fmt.Errorf("parse reference %s/%s:%s: %w", r.host, repo, tag, err)
	}

	opts := r.remoteOpts(ctx)
	head, err := remote.Head(ref, opts...)
	if err == nil {
		return strings.TrimPrefix(head.Digest.String(), "sha256:"), nil
	}
	if !headNeedsGetFallback(err) {
		return "", err
	}
	if log != nil {
		log.Debug("tag_digest head fallback",
			"repo", repo,
			"tag", tag,
			"err", sanitizeRegistryErr(err),
		)
	}

	desc, err := remote.Get(ref, opts...)
	if err != nil {
		return "", err
	}

	return strings.TrimPrefix(desc.Digest.String(), "sha256:"), nil
}

// ImageCreated returns the config blob "created" time for repo:tag. For a
// multi-arch index it reads the child matching this process's platform
// (versentry runs on the Docker host), else the first real platform — all
// children of one tag are built together. Costs one index GET + one child
// manifest GET (one Docker Hub pull) + one config blob; callers use it only
// for update candidates when min_age is set.
func (r *Registry) ImageCreated(ctx context.Context, repo, tag string) (time.Time, error) {
	created, err := r.imageCreatedOnce(ctx, repo, tag)
	if err != nil {
		return time.Time{}, mapRegistryError(err)
	}
	return created, nil
}

func (r *Registry) imageCreatedOnce(ctx context.Context, repo, tag string) (time.Time, error) {
	ref, err := name.ParseReference(fmt.Sprintf("%s/%s:%s", r.host, repo, tag), r.nameOpts()...)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse reference %s/%s:%s: %w", r.host, repo, tag, err)
	}

	desc, err := remote.Get(ref, r.remoteOpts(ctx)...)
	if err != nil {
		return time.Time{}, err
	}

	var img v1.Image
	if desc.MediaType.IsIndex() {
		idx, err := desc.ImageIndex()
		if err != nil {
			return time.Time{}, err
		}
		manifest, err := idx.IndexManifest()
		if err != nil {
			return time.Time{}, err
		}
		child, ok := pickPlatformChild(manifest.Manifests, runtime.GOOS, runtime.GOARCH)
		if !ok {
			return time.Time{}, fmt.Errorf("index %s/%s:%s has no image manifests", r.host, repo, tag)
		}
		img, err = idx.Image(child.Digest)
		if err != nil {
			return time.Time{}, err
		}
	} else {
		img, err = desc.Image()
		if err != nil {
			return time.Time{}, err
		}
	}

	cfg, err := img.ConfigFile()
	if err != nil {
		return time.Time{}, err
	}
	return cfg.Created.Time, nil
}

// pickPlatformChild prefers os/arch, else the first image manifest with a real
// platform. Attestation manifests (platform unknown/unknown) are never chosen.
func pickPlatformChild(children []v1.Descriptor, goos, goarch string) (v1.Descriptor, bool) {
	var first *v1.Descriptor
	for i := range children {
		c := children[i]
		if !c.MediaType.IsImage() {
			continue
		}
		if c.Platform != nil && (c.Platform.OS == "unknown" || c.Platform.Architecture == "unknown") {
			continue
		}
		if c.Platform != nil && c.Platform.OS == goos && c.Platform.Architecture == goarch {
			return c, true
		}
		if first == nil {
			first = &children[i]
		}
	}
	if first == nil {
		return v1.Descriptor{}, false
	}
	return *first, true
}

// headNeedsGetFallback reports a registry that answered HEAD but not usefully:
// method not supported, or required headers stripped (old Artifactory/Nexus,
// header-rewriting proxies). 404/401/403/429, network errors, timeouts, and
// cancel return as-is: GET would fail the same way, and a second request would
// double the wait inside timeouts.registry or spend more of a 429 budget.
func headNeedsGetFallback(err error) bool {
	var terr *transport.Error
	if errors.As(err, &terr) {
		switch terr.StatusCode {
		case http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusNotImplemented:
			return true
		}
		return false
	}
	return strings.Contains(err.Error(), "response did not include")
}

// ListTags returns all tags for a repository.
// Uses Puller.Lister (same pagination as remote.List). Overall ctx is the
// engine timeouts.registry ceiling; per-attempt dial/header timeouts live on
// the HTTP transport.
func (r *Registry) ListTags(ctx context.Context, repo string) ([]string, error) {
	return r.listTags(ctx, repo, slog.Default())
}

func (r *Registry) listTags(ctx context.Context, repo string, log *slog.Logger) ([]string, error) {
	tags, err := r.listTagsOnce(ctx, repo, log)
	if err != nil {
		return nil, mapRegistryError(err)
	}
	return tags, nil
}

func (r *Registry) listTagsOnce(ctx context.Context, repo string, log *slog.Logger) ([]string, error) {
	ref, err := name.NewRepository(fmt.Sprintf("%s/%s", r.host, repo), r.nameOpts()...)
	if err != nil {
		return nil, fmt.Errorf("parse repository %s/%s: %w", r.host, repo, err)
	}

	debug := log != nil && log.Enabled(ctx, slog.LevelDebug)
	var sink *httpTripSink
	if debug {
		sink = &httpTripSink{}
		ctx = withHTTPTripSink(ctx, sink, log)
	}

	puller, err := remote.NewPuller(r.remoteOpts(ctx)...)
	if err != nil {
		return nil, err
	}

	var start time.Time
	var sumPage, maxPage time.Duration
	pages, nTags := 0, 0
	if debug {
		start = time.Now()
	}
	recordPage := func(d time.Duration) {
		sumPage += d
		if d > maxPage {
			maxPage = d
		}
	}

	var pageStart time.Time
	if debug {
		pageStart = time.Now()
	}
	// Lister fetches the first page before returning; time that as page 1.
	lister, err := puller.Lister(ctx, ref)
	if debug {
		recordPage(time.Since(pageStart))
	}
	if err != nil {
		if debug {
			attrs := []any{
				"repo", repo,
				"pages", 0,
				"tags", 0,
				"duration", time.Since(start),
				"err", sanitizeRegistryErr(err),
			}
			attrs = append(attrs, sink.slogAttrs()...)
			log.Debug("list_tags incomplete", attrs...)
		}
		return nil, err
	}

	var all []string
	for lister.HasNext() {
		// First Next returns the page already fetched by Lister (no HTTP); time only later pages.
		if debug && pages > 0 {
			pageStart = time.Now()
		}
		page, nextErr := lister.Next(ctx)
		if debug && pages > 0 {
			recordPage(time.Since(pageStart))
		}
		if nextErr != nil {
			if debug {
				attrs := []any{
					"repo", repo,
					"pages", pages,
					"tags", nTags,
					"duration", time.Since(start),
					"err", sanitizeRegistryErr(nextErr),
				}
				attrs = append(attrs, sink.slogAttrs()...)
				log.Debug("list_tags incomplete", attrs...)
			}
			return nil, nextErr
		}
		pages++
		nTags += len(page.Tags)
		all = append(all, page.Tags...)
	}

	if debug {
		avg := time.Duration(0)
		if pages > 0 {
			avg = sumPage / time.Duration(pages)
		}
		log.Debug("list_tags",
			"repo", repo,
			"pages", pages,
			"tags", nTags,
			"duration", time.Since(start),
			"page_max_ms", maxPage.Milliseconds(),
			"page_avg_ms", avg.Milliseconds(),
		)
	}
	return all, nil
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
		base = applyRegistryIOTimeouts(remote.DefaultTransport)
	}
	return []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuth(r.auth),
		remote.WithTransport(withHangTrace(withRetryAfterCapture(base))),
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
