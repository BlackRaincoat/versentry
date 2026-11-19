package core

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/imageref"
)

// Label keys for per-container rules (same semantics as config rules[]).
const (
	labelInclude = "versentry.include"
	labelExclude = "versentry.exclude"
	labelTrack   = "versentry.track"
	labelMode    = "versentry.mode" // deprecated alias for labelTrack
	labelMinAge  = "versentry.min_age"
)

// RuleTrackDigest forces digest detection for an image/container.
const RuleTrackDigest = "digest"

// RuleTrackAuto (label only) restores default detection (semver → numeric →
// digest) for one container, overriding a config rule's track: digest.
const RuleTrackAuto = "auto"

// Rule is a resolved rule for one image: optional include filter and/or track.
type Rule struct {
	Image   string
	Include *regexp.Regexp // nil when unset (e.g. track-only digest rule)
	Exclude *regexp.Regexp // nil when unset; drops matching registry tags
	Track   string         // "" or RuleTrackDigest
	MinAge  *time.Duration // nil = global min_age; 0 = off for this image (config rules only)
	// Overridden lists config-rule fields replaced by container labels (ChainRuleResolver).
	Overridden []string
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
		var ex *regexp.Regexp
		if rc.Exclude != "" {
			compiled, err := regexp.Compile(rc.Exclude)
			if err != nil {
				return nil, fmt.Errorf("rules[%d]: invalid exclude regex: %w", i, err)
			}
			ex = compiled
		}
		track := config.EffectiveRuleTrack(rc)
		var minAge *time.Duration
		if rc.MinAge != nil {
			d := rc.MinAge.Duration
			minAge = &d
		}
		byImage[rc.Image] = &Rule{
			Image:   rc.Image,
			Include: re,
			Exclude: ex,
			Track:   track,
			MinAge:  minAge,
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

	include := labelRegex(r.log, q, labelInclude, "include")
	exclude := labelRegex(r.log, q, labelExclude, "exclude")
	track := resolveLabelTrack(r.log, q, q.Labels[labelTrack], q.Labels[labelMode])
	minAge := resolveLabelMinAge(r.log, q, q.Labels[labelMinAge])

	if include == nil && exclude == nil && track == "" && minAge == nil {
		return nil
	}
	return &Rule{
		Image:   q.Image,
		Include: include,
		Exclude: exclude,
		Track:   track,
		MinAge:  minAge,
	}
}

// labelRegex compiles a regex label; invalid values log WARN and are ignored.
func labelRegex(log *slog.Logger, q RuleQuery, key, field string) *regexp.Regexp {
	raw := q.Labels[key]
	if raw == "" {
		return nil
	}
	re, err := regexp.Compile(raw)
	if err != nil {
		log.Warn("invalid "+key+" label, ignoring",
			"container", q.Container,
			"image", q.Image,
			field, raw,
			"error", err,
		)
		return nil
	}
	return re
}

func resolveLabelTrack(log *slog.Logger, q RuleQuery, trackRaw, modeRaw string) string {
	trackRaw = strings.TrimSpace(trackRaw)
	modeRaw = strings.TrimSpace(modeRaw)

	track := ""
	if trackRaw != "" {
		if trackRaw != RuleTrackDigest && trackRaw != RuleTrackAuto {
			log.Warn("invalid versentry.track label, ignoring",
				"container", q.Container,
				"image", q.Image,
				"track", trackRaw,
			)
		} else {
			track = trackRaw
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

// effectiveTrack folds "" and "auto" (both default detection) together.
func effectiveTrack(track string) string {
	if track == RuleTrackDigest {
		return RuleTrackDigest
	}
	return RuleTrackAuto
}

// resolveLabelMinAge parses versentry.min_age ("3d", "36h", "0" = off). Invalid
// or negative values log WARN and are ignored (the config value, if any, applies).
func resolveLabelMinAge(log *slog.Logger, q RuleQuery, raw string) *time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if raw == "0" {
		zero := time.Duration(0)
		return &zero
	}
	d, err := config.ParseDuration(raw)
	if err != nil || d < 0 {
		log.Warn("invalid versentry.min_age label, ignoring",
			"container", q.Container,
			"image", q.Image,
			"min_age", raw,
		)
		return nil
	}
	return &d
}

// ChainRuleResolver merges rule sources field by field, in increasing priority:
// a later source overrides each field it sets (include, track, min_age) and
// inherits the rest. Versentry wires config rules first, container labels last,
// so a label is a per-container override of the central image rule.
type ChainRuleResolver struct {
	sources []RuleResolver
}

// NewChainRuleResolver builds a merge chain; later sources win per field.
func NewChainRuleResolver(sources ...RuleResolver) *ChainRuleResolver {
	return &ChainRuleResolver{sources: sources}
}

// RuleFor merges all non-nil rules for the query. Overridden lists the fields a
// later source replaced on an earlier one (for a one-time INFO).
func (c *ChainRuleResolver) RuleFor(q RuleQuery) *Rule {
	var merged *Rule
	for _, src := range c.sources {
		if src == nil {
			continue
		}
		rule := src.RuleFor(q)
		if rule == nil {
			continue
		}
		if merged == nil {
			cp := *rule
			cp.Overridden = nil
			merged = &cp
			continue
		}
		if rule.Include != nil {
			if merged.Include != nil {
				merged.Overridden = append(merged.Overridden, "include")
			}
			merged.Include = rule.Include
		}
		if rule.Exclude != nil {
			if merged.Exclude != nil {
				merged.Overridden = append(merged.Overridden, "exclude")
			}
			merged.Exclude = rule.Exclude
		}
		if rule.Track != "" {
			// Count it only when detection actually changes: "auto" is the default,
			// so auto over a rule without track: digest changes nothing.
			if effectiveTrack(merged.Track) != effectiveTrack(rule.Track) {
				merged.Overridden = append(merged.Overridden, "track")
			}
			merged.Track = rule.Track
		}
		if rule.MinAge != nil {
			if merged.MinAge != nil {
				merged.Overridden = append(merged.Overridden, "min_age")
			}
			merged.MinAge = rule.MinAge
		}
	}
	return merged
}

// excludeTags drops tags that match the exclude regex.
func excludeTags(tags []string, exclude *regexp.Regexp) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if !exclude.MatchString(tag) {
			out = append(out, tag)
		}
	}
	return out
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
