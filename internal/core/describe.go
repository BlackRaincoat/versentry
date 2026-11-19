package core

import (
	"context"
	"log/slog"
	"regexp"
	"time"

	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/registry"
)

// ociVersionLabel carries the packaged software version (OCI image spec).
const ociVersionLabel = "org.opencontainers.image.version"

// maxDescribeLookups caps registry lookups per pass for notification details.
// Each is one Docker Hub pull (anonymous budget shared with the host's
// docker pull); a first run with no state can notify the whole fleet at once.
// Updates beyond the cap, and lookups min_age already made, cost nothing.
const maxDescribeLookups = 5

// versionPattern keeps registry-provided label values safe to place in
// HTML/markdown messages and short enough for one line.
var versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`)

// DescribeUpdates adds version and build-time details to updates about to be
// notified, so a moved floating tag reads "2.3.1 → 2.4.0 (built 3 days ago)"
// instead of two digests. Digest updates: current version from the running
// image's labels (no request), new version and build time from the new image's
// config blob — shared with min_age through the last pass cache, else one
// lookup (capped per pass). Semver updates only get a build time min_age
// already fetched. Detection and state are unaffected; failures leave the
// update as it was. Call after RunOnce, only for updates being sent.
func (e *Engine) DescribeUpdates(ctx context.Context, updates []model.UpdateAvailable) {
	pass := e.lastPass
	if pass == nil {
		pass = registrypass.New(e.log)
		e.lastPass, e.describeLookups = pass, 0
	}
	for i := range updates {
		if ctx.Err() != nil {
			return
		}
		u := &updates[i]
		if u.LatestTag != "" {
			if info, ok := pass.CachedImageInfo(u.Host, u.Repo, u.LatestTag); ok {
				u.LatestCreated = e.usableCreated(info.Created)
			}
			continue
		}

		u.CurrentVersion = cleanVersion(u.Container.Labels[ociVersionLabel])
		info, ok := pass.CachedImageInfo(u.Host, u.Repo, u.CurrentTag)
		if !ok {
			info, ok = e.lookupImageInfo(ctx, pass, *u)
			if !ok {
				continue
			}
		}
		// The tag may have moved again since detection: details must describe
		// the digest being notified, or nothing.
		if info.Digest != "" && normalizeDigest(info.Digest) != normalizeDigest(u.RemoteDigest) {
			e.log.Debug("notification details skipped: tag moved since the check",
				"image", u.Repo, "tag", u.CurrentTag)
			continue
		}
		u.LatestVersion = cleanVersion(info.Version)
		u.LatestCreated = e.usableCreated(info.Created)
	}
}

func (e *Engine) lookupImageInfo(ctx context.Context, pass *registrypass.Pass, u model.UpdateAvailable) (registry.ImageInfo, bool) {
	if e.describeLookups >= maxDescribeLookups {
		e.logOnce(slog.LevelDebug, "describe-cap", "notification details: lookup cap reached, remaining digest updates show digests",
			"cap", maxDescribeLookups)
		return registry.ImageInfo{}, false
	}
	reg, err := e.registryForHost(u.Host)
	if err != nil {
		return registry.ImageInfo{}, false
	}
	e.describeLookups++
	lookupCtx, cancel := ctx, func() {}
	if d := e.timeouts.Registry.Duration; d > 0 {
		lookupCtx, cancel = context.WithTimeout(ctx, d)
	}
	info, err := pass.ImageInfo(lookupCtx, reg, u.Host, u.Repo, u.CurrentTag)
	cancel()
	if err != nil {
		e.log.Debug("notification details unavailable",
			"image", u.Repo, "tag", u.CurrentTag, "error", err)
		return registry.ImageInfo{}, false
	}
	return info, true
}

// usableCreated drops timestamps min_age also treats as unknown.
func (e *Engine) usableCreated(t time.Time) time.Time {
	if t.IsZero() || t.Year() < minCreatedYear || t.After(e.now().Add(maxCreatedSkew)) {
		return time.Time{}
	}
	return t
}

func cleanVersion(v string) string {
	if !versionPattern.MatchString(v) {
		return ""
	}
	return v
}
