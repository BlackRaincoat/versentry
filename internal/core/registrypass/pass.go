package registrypass

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/BlackRaincoat/versentry/internal/registry"
)

// Pass caches registry results and tracks rate-limited hosts for a single RunOnce pass.
type Pass struct {
	log *slog.Logger
	mu  sync.Mutex

	listTags    map[string]listTagsResult
	tagDigests  map[string]tagDigestResult
	imageInfo   map[string]imageInfoResult
	rateLimited map[string]struct{}

	sleep sleepFunc
}

type listTagsResult struct {
	tags []string
	err  error
}

type tagDigestResult struct {
	digest string
	err    error
}

type imageInfoResult struct {
	info registry.ImageInfo
	err  error
}

// New creates a per-pass registry layer. It must not be reused across RunOnce calls.
func New(log *slog.Logger) *Pass {
	if log == nil {
		log = slog.Default()
	}
	return &Pass{
		log:         log,
		listTags:    make(map[string]listTagsResult),
		tagDigests:  make(map[string]tagDigestResult),
		imageInfo:   make(map[string]imageInfoResult),
		rateLimited: make(map[string]struct{}),
		sleep:       sleepContext,
	}
}

// ListTags returns tags for host/repo, deduplicating calls within the pass.
func (p *Pass) ListTags(ctx context.Context, reg registry.Registry, host, repo string) ([]string, error) {
	key := host + "/" + repo

	if res, ok := p.cachedListTags(key, host, repo); ok {
		return res.tags, res.err
	}

	tags, err := p.fetchListTags(ctx, reg, host, repo)
	p.storeListTags(key, listTagsResult{tags: tags, err: err})
	return tags, err
}

// TagDigest returns the digest for host/repo#tag, deduplicating calls within the pass.
func (p *Pass) TagDigest(ctx context.Context, reg registry.Registry, host, repo, tag string) (string, error) {
	key := host + "/" + repo + "#" + tag

	if res, ok := p.cachedTagDigest(key, host); ok {
		return res.digest, res.err
	}

	digest, err := p.fetchTagDigest(ctx, reg, host, repo, tag)
	p.storeTagDigest(key, tagDigestResult{digest: digest, err: err})
	return digest, err
}

// ImageCreated returns when the image behind host/repo:tag was built (see ImageInfo).
// Zero time with nil error: the registry does not implement
// registry.ImageInfoReader or had no usable timestamp (caller treats age as unknown).
func (p *Pass) ImageCreated(ctx context.Context, reg registry.Registry, host, repo, tag string) (time.Time, error) {
	info, err := p.ImageInfo(ctx, reg, host, repo, tag)
	return info.Created, err
}

// ImageInfo returns the config-blob facts for host/repo:tag, deduplicating calls
// within the pass (min_age and notification details share one lookup). A zero
// ImageInfo with nil error: the registry does not implement registry.ImageInfoReader.
func (p *Pass) ImageInfo(ctx context.Context, reg registry.Registry, host, repo, tag string) (registry.ImageInfo, error) {
	reader, ok := reg.(registry.ImageInfoReader)
	if !ok {
		return registry.ImageInfo{}, nil
	}
	key := host + "/" + repo + ":" + tag

	p.mu.Lock()
	if p.isRateLimitedLocked(host) {
		p.mu.Unlock()
		return registry.ImageInfo{}, registry.ErrRateLimited
	}
	if res, ok := p.imageInfo[key]; ok {
		p.mu.Unlock()
		p.log.Debug("registry cache hit", "op", "image_info", "host", host, "key", key)
		return res.info, res.err
	}
	p.mu.Unlock()

	info, err := fetchWithRetry(ctx, p, host, func() (registry.ImageInfo, error) {
		return reader.ImageInfo(ctx, repo, tag)
	})
	p.mu.Lock()
	p.imageInfo[key] = imageInfoResult{info: info, err: err}
	p.mu.Unlock()
	return info, err
}

// CachedImageInfo returns an ImageInfo already fetched in this pass without
// making a request (false when absent or the lookup failed).
func (p *Pass) CachedImageInfo(host, repo, tag string) (registry.ImageInfo, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	res, ok := p.imageInfo[host+"/"+repo+":"+tag]
	if !ok || res.err != nil {
		return registry.ImageInfo{}, false
	}
	return res.info, true
}

func (p *Pass) cachedListTags(key, host, repo string) (listTagsResult, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.isRateLimitedLocked(host) {
		return listTagsResult{err: registry.ErrRateLimited}, true
	}
	res, ok := p.listTags[key]
	if ok {
		p.log.Debug("registry cache hit", "op", "list_tags", "host", host, "repo", repo)
	}
	return res, ok
}

func (p *Pass) cachedTagDigest(key, host string) (tagDigestResult, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.isRateLimitedLocked(host) {
		return tagDigestResult{err: registry.ErrRateLimited}, true
	}
	res, ok := p.tagDigests[key]
	if ok {
		p.log.Debug("registry cache hit", "op", "tag_digest", "host", host, "key", key)
	}
	return res, ok
}

func (p *Pass) storeListTags(key string, res listTagsResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listTags[key] = res
}

func (p *Pass) storeTagDigest(key string, res tagDigestResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tagDigests[key] = res
}

func (p *Pass) isRateLimitedLocked(host string) bool {
	_, ok := p.rateLimited[host]
	return ok
}

func (p *Pass) markRateLimited(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.rateLimited[host]; ok {
		return
	}
	p.rateLimited[host] = struct{}{}
	p.log.Warn("registry host rate limited for pass", "host", host)
}

func (p *Pass) fetchListTags(ctx context.Context, reg registry.Registry, host, repo string) ([]string, error) {
	return fetchWithRetry(ctx, p, host, func() ([]string, error) {
		return reg.ListTags(ctx, repo)
	})
}

func (p *Pass) fetchTagDigest(ctx context.Context, reg registry.Registry, host, repo, tag string) (string, error) {
	return fetchWithRetry(ctx, p, host, func() (string, error) {
		return reg.TagDigest(ctx, repo, tag)
	})
}

func fetchWithRetry[T any](ctx context.Context, p *Pass, host string, call func() (T, error)) (T, error) {
	var zero T

	value, err := call()
	if err == nil {
		return value, nil
	}

	if errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrUnauthorized) {
		return zero, err
	}

	if isRateLimit(err) {
		return handleRateLimit(ctx, p, host, call, err)
	}

	if isServerError(err) {
		return retryServerError(ctx, p, host, call, err)
	}

	return zero, err
}

func handleRateLimit[T any](ctx context.Context, p *Pass, host string, call func() (T, error), firstErr error) (T, error) {
	var zero T

	retryAfter, _ := registry.RetryAfterFromError(firstErr)
	if retryAfter <= 0 || retryAfter > maxRegistryRetryAfter {
		p.markRateLimited(host)
		return zero, registry.ErrRateLimited
	}

	p.log.Warn("registry rate limited, retrying",
		"host", host,
		"retry_after", retryAfter,
	)

	if err := p.sleep(ctx, retryAfter); err != nil {
		return zero, err
	}

	value, err := call()
	if err == nil {
		return value, nil
	}
	if isRateLimit(err) {
		p.markRateLimited(host)
		return zero, registry.ErrRateLimited
	}
	if isServerError(err) {
		return retryServerError(ctx, p, host, call, err)
	}

	return zero, err
}

func retryServerError[T any](ctx context.Context, p *Pass, host string, call func() (T, error), firstErr error) (T, error) {
	var zero T
	lastErr := firstErr

	for attempt := 1; attempt <= registry5xxMaxAttempts; attempt++ {
		if !isServerError(lastErr) {
			return zero, lastErr
		}
		if attempt == registry5xxMaxAttempts {
			break
		}

		delay := serverBackoff(attempt)
		p.log.Warn("registry attempt failed, retrying",
			"host", host,
			"attempt", attempt,
			"max_attempts", registry5xxMaxAttempts,
			"delay", delay,
			"error", lastErr,
		)

		if err := p.sleep(ctx, delay); err != nil {
			return zero, err
		}

		value, err := call()
		if err == nil {
			return value, nil
		}
		lastErr = err

		if errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrUnauthorized) {
			return zero, err
		}
		if isRateLimit(err) {
			return handleRateLimit(ctx, p, host, call, err)
		}
	}

	if isServerError(lastErr) {
		return zero, registry.ErrUnavailable
	}
	return zero, lastErr
}
