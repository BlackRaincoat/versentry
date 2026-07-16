package core

import (
	"log/slog"

	"github.com/BlackRaincoat/versentry/internal/imageweb"
)

// resolveTrackingMode chooses semver vs digest without contacting the registry.
// Shared by checkContainer and Links so track rules stay in one place.
func resolveTrackingMode(
	rules RuleResolver,
	log *slog.Logger,
	host, repo, tag, container string,
	labels map[string]string,
) (mode string, rule *Rule) {
	if rules != nil {
		rule = rules.RuleFor(RuleQuery{Host: host, Image: repo, Container: container, Labels: labels})
	}
	if rule != nil && rule.Track == RuleTrackDigest {
		if rule.Include != nil && log != nil {
			log.Warn("include ignored: track=digest (include applies only in semver mode)",
				"container", container,
				"image", repo,
			)
		}
		return imageweb.ModeDigest, rule
	}
	if _, err := parseContainerSemver(tag); err != nil {
		return imageweb.ModeDigest, rule
	}
	return imageweb.ModeSemver, rule
}
