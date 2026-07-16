package core

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/imageref"
)

// Label keys for per-container rules (same semantics as config rules[]).
const (
	labelInclude = "versentry.include"
	labelTrack   = "versentry.track"
	labelMode    = "versentry.mode" // deprecated alias for labelTrack
)

// RuleTrackDigest forces digest detection for an image/container.
const RuleTrackDigest = "digest"

// Rule is a resolved rule for one image: optional include filter and/or track.
type Rule struct {
	Image   string
	Include *regexp.Regexp // nil when unset (e.g. track-only digest rule)
	Track   string         // "" or RuleTrackDigest
}

// RuleQuery is the input for rule resolution: image identity plus container labels.
type RuleQuery struct {
	Host      string
	Image     string
	Container string
	Labels    map[string]string
}

// RuleResolver finds the effective rule for a container/image.
// Priority (first match wins): config → labels → nil (default detection).
type RuleResolver interface {
	RuleFor(q RuleQuery) *Rule
}

// ConfigRuleResolver resolves rules from the Versentry config only.
type ConfigRuleResolver struct {
	byImage map[string]*Rule
}

// NewConfigRuleResolver builds a resolver from validated config rules.
func NewConfigRuleResolver(rules []config.RuleConfig, log *slog.Logger) (*ConfigRuleResolver, error) {
	byImage := make(map[string]*Rule, len(rules))
	for i, rc := range rules {
		if config.RuleUsesDeprecatedMode(rc) && log != nil {
			log.Warn("rules[].mode is deprecated, use track instead",
				"image", rc.Image,
			)
		}
		var re *regexp.Regexp
		if rc.Include != "" {
			compiled, err := regexp.Compile(rc.Include)
			if err != nil {
				return nil, fmt.Errorf("rules[%d]: invalid include regex: %w", i, err)
			}
			re = compiled
		}
		track := config.EffectiveRuleTrack(rc)
		byImage[rc.Image] = &Rule{
			Image:   rc.Image,
			Include: re,
			Track:   track,
		}
	}
	return &ConfigRuleResolver{byImage: byImage}, nil
}

// RuleFor returns the config rule for the image, or nil if none is defined.
func (r *ConfigRuleResolver) RuleFor(q RuleQuery) *Rule {
	if r == nil {
		return nil
	}
	for _, key := range imageref.RuleLookupKeys(q.Host, q.Image) {
		if rule := r.byImage[key]; rule != nil {
			return rule
		}
	}
	return nil
}

// LabelRuleResolver resolves rules from versentry.include / versentry.track labels.
type LabelRuleResolver struct {
	log *slog.Logger
}

// NewLabelRuleResolver builds a label-based rule resolver.
func NewLabelRuleResolver(log *slog.Logger) *LabelRuleResolver {
	if log == nil {
		log = slog.Default()
	}
	return &LabelRuleResolver{log: log}
}

// RuleFor returns a rule from container labels, or nil if absent/invalid.
// Invalid include or track is logged and ignored so one bad label cannot break the pass.
func (r *LabelRuleResolver) RuleFor(q RuleQuery) *Rule {
	if r == nil || len(q.Labels) == 0 {
		return nil
	}

	var include *regexp.Regexp
	if raw := q.Labels[labelInclude]; raw != "" {
		re, err := regexp.Compile(raw)
		if err != nil {
			r.log.Warn("invalid versentry.include label, ignoring",
				"container", q.Container,
				"image", q.Image,
				"include", raw,
				"error", err,
			)
		} else {
			include = re
		}
	}

	track := resolveLabelTrack(r.log, q, q.Labels[labelTrack], q.Labels[labelMode])

	if include == nil && track == "" {
		return nil
	}
	return &Rule{
		Image:   q.Image,
		Include: include,
		Track:   track,
	}
}

func resolveLabelTrack(log *slog.Logger, q RuleQuery, trackRaw, modeRaw string) string {
	trackRaw = strings.TrimSpace(trackRaw)
	modeRaw = strings.TrimSpace(modeRaw)

	track := ""
	if trackRaw != "" {
		if trackRaw != RuleTrackDigest {
			log.Warn("invalid versentry.track label, ignoring",
				"container", q.Container,
				"image", q.Image,
				"track", trackRaw,
			)
		} else {
			track = RuleTrackDigest
		}
	}

	if modeRaw == "" {
		return track
	}
	if modeRaw != RuleTrackDigest {
		log.Warn("invalid versentry.mode label, ignoring",
			"container", q.Container,
			"image", q.Image,
			"mode", modeRaw,
		)
		return track
	}
	if track != "" {
		log.Warn("versentry.mode and versentry.track both set; using track",
			"container", q.Container,
			"image", q.Image,
		)
		return track
	}
	log.Warn("versentry.mode is deprecated, use versentry.track instead",
		"container", q.Container,
		"image", q.Image,
	)
	return RuleTrackDigest
}

// ChainRuleResolver tries sources in order; first non-nil rule wins.
type ChainRuleResolver struct {
	sources []RuleResolver
}

// NewChainRuleResolver builds a priority chain of rule sources.
func NewChainRuleResolver(sources ...RuleResolver) *ChainRuleResolver {
	return &ChainRuleResolver{sources: sources}
}

// RuleFor returns the first non-nil rule from the chain.
func (c *ChainRuleResolver) RuleFor(q RuleQuery) *Rule {
	for _, src := range c.sources {
		if src == nil {
			continue
		}
		if rule := src.RuleFor(q); rule != nil {
			return rule
		}
	}
	return nil
}

// filterTags keeps only tags that match the include regex.
func filterTags(tags []string, include *regexp.Regexp) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if include.MatchString(tag) {
			out = append(out, tag)
		}
	}
	return out
}
