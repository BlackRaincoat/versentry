package core

import (
	"github.com/Masterminds/semver/v3"
)

// TagSelector chooses the best candidate tag from a registry tag list.
// The default implementation applies same-major, stable-only selection.
// Per-image rules can replace this later without changing the engine.
type TagSelector interface {
	Select(current *semver.Version, tags []string) (tag string, version *semver.Version, ok bool)
}

// DefaultTagSelector picks the newest stable tag within the current major version.
type DefaultTagSelector struct{}

// Select implements the default tag selection strategy.
func (DefaultTagSelector) Select(current *semver.Version, tags []string) (string, *semver.Version, bool) {
	var best *semver.Version
	var bestTag string

	for _, raw := range tags {
		v, err := semver.NewVersion(raw)
		if err != nil {
			continue
		}
		if v.Prerelease() != "" {
			continue
		}
		if v.Major() != current.Major() {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best = v
			bestTag = raw
		}
	}

	if best == nil {
		return "", nil, false
	}
	return bestTag, best, true
}

// RuleTagSelector picks the newest semver tag within the current major version.
// Unlike DefaultTagSelector, it does not drop prerelease/suffix tags: after an
// include filter, suffixes are part of the valid tag line (e.g. 17.10-alpine3.24).
// Same-major is kept for softer include patterns that do not pin the major.
type RuleTagSelector struct{}

// Select implements tag selection for include-filtered tag sets.
func (RuleTagSelector) Select(current *semver.Version, tags []string) (string, *semver.Version, bool) {
	var best *semver.Version
	var bestTag string

	for _, raw := range tags {
		v, err := semver.NewVersion(raw)
		if err != nil {
			continue
		}
		if v.Major() != current.Major() {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best = v
			bestTag = raw
		}
	}

	if best == nil {
		return "", nil, false
	}
	return bestTag, best, true
}

// parseContainerSemver parses a container image tag as semver.
func parseContainerSemver(tag string) (*semver.Version, error) {
	return semver.NewVersion(tag)
}
