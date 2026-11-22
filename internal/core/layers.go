package core

import (
	"context"
	"slices"

	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/provider"
	"github.com/BlackRaincoat/versentry/internal/registry"
)

// maxMovedImageLookups caps registry lookups per pass for moved digest tags
// (layer check, ignore_rebuilds). Each is one Docker Hub pull; results are
// cached across passes, so the cap only matters on the first pass after start
// (or when many tags move at once). Over the cap the digest change counts as
// an update, as before these checks existed.
const maxMovedImageLookups = 5

// Reasons a moved digest tag is not reported (DEBUG "container checked").
const (
	sameLayersReason  = "digest changed, same layers for this platform"
	sameVersionReason = "digest changed, same version (ignore_rebuilds)"
)

// unchangedReason decides whether a moved digest tag (local ≠ remote digest)
// is really an update. It returns a non-empty reason when it is not:
//
//   - same layers: the tag is a multi-arch index that moved because another
//     architecture was rebuilt or an attestation was attached, while the child
//     for this platform has the same rootfs.diff_ids as the running image
//     (seen by dockcheck on traefik/amd64). A moved single-platform manifest is
//     always an update (its config changed). Config-only changes on identical
//     layers read as unchanged — accepted, as in dockcheck;
//   - same version: ignoreRebuilds and the new image carries the same
//     org.opencontainers.image.version as the running one.
//
// Fails open: missing data, a lookup error, or the per-pass cap returns "" and
// the change is reported as an update. Only a cancelled parent ctx errors.
func (e *Engine) unchangedReason(
	ctx context.Context,
	c model.Container,
	parsed imageref.Parsed,
	reg registry.Registry,
	pass *registrypass.Pass,
	remoteDigest string,
	ignoreRebuilds bool,
) (string, error) {
	var localLayers []string
	if reader, ok := e.provider.(provider.LocalImageReader); ok {
		localCtx, cancel := context.WithTimeout(ctx, e.timeouts.Provider.Duration)
		local, err := reader.LocalImage(localCtx, c)
		cancel()
		if err == nil {
			localLayers = local.Layers
		}
	}
	localVersion := ""
	if ignoreRebuilds {
		localVersion = cleanVersion(c.Labels[ociVersionLabel])
	}
	if len(localLayers) == 0 && localVersion == "" {
		return "", nil // nothing to compare: no lookup
	}

	info, ok, err := e.movedImage(ctx, c, parsed, reg, pass, remoteDigest)
	if err != nil || !ok {
		return "", err
	}
	if info.Index && len(localLayers) > 0 && slices.Equal(localLayers, info.DiffIDs) {
		return sameLayersReason, nil
	}
	if localVersion != "" && cleanVersion(info.Version) == localVersion {
		return sameVersionReason, nil
	}
	return "", nil
}

// movedImage returns the registry facts (ImageInfo) for the image a moved tag
// now points to. They depend only on host/repo@digest, so they are cached
// across passes in Engine.movedFacts: the lookup is a Docker Hub pull, and a
// tag that moved without a real update stays moved until the user pulls, so
// without the cache every pass would spend one. The pass cache is shared with
// min_age and DescribeUpdates (one pull serves all).
//
// ok is false (nothing cached) when the lookup failed or hit the per-pass cap,
// the registry cannot read config blobs, or the tag moved again since HEAD.
func (e *Engine) movedImage(
	ctx context.Context,
	c model.Container,
	parsed imageref.Parsed,
	reg registry.Registry,
	pass *registrypass.Pass,
	remoteDigest string,
) (registry.ImageInfo, bool, error) {
	key := movedKey(parsed.Host, parsed.Repo, remoteDigest)
	e.movedSeen[key] = struct{}{}
	if info, ok := e.movedFacts[key]; ok {
		return info, true, nil
	}

	info, ok := pass.CachedImageInfo(parsed.Host, parsed.Repo, parsed.Tag)
	if !ok {
		if e.movedLookups >= maxMovedImageLookups {
			e.log.Debug("moved tag check skipped: per-pass lookup cap reached",
				"container", c.Name, "image", c.ImageRef, "cap", maxMovedImageLookups)
			return registry.ImageInfo{}, false, nil
		}
		e.movedLookups++
		remoteCtx, cancel := context.WithTimeout(ctx, e.timeouts.Registry.Duration)
		var err error
		info, err = pass.ImageInfo(remoteCtx, reg, parsed.Host, parsed.Repo, parsed.Tag)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return registry.ImageInfo{}, false, ctx.Err()
			}
			e.log.Debug("moved tag check failed, reporting the digest change",
				"container", c.Name, "image", c.ImageRef, "error", err)
			return registry.ImageInfo{}, false, nil
		}
	}
	// Zero ImageInfo (no config reader) or a tag that moved again: no facts.
	if normalizeDigest(info.Digest) != normalizeDigest(remoteDigest) {
		return registry.ImageInfo{}, false, nil
	}
	e.movedFacts[key] = info
	return info, true, nil
}

func movedKey(host, repo, digest string) string {
	return host + "/" + repo + "@" + normalizeDigest(digest)
}

// pruneMovedFacts keeps only facts used in the pass that just ran, so the
// cache tracks the running fleet instead of growing with every rebuild.
func (e *Engine) pruneMovedFacts() {
	for k := range e.movedFacts {
		if _, ok := e.movedSeen[k]; !ok {
			delete(e.movedFacts, k)
		}
	}
}
