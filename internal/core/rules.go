package core

import (
	"fmt"
	"log/slog"
	"regexp"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/imageref"
)

// Label key for a per-container include regex (same semantics as rules[].include).
const labelInclude = "versentry.include"

// Rule is a resolved, compiled tag filter for one image.
type Rule struct {
	Image   string
	Include *regexp.Regexp
}

// RuleQuery is the input for rule resolution: image identity plus container labels.
type RuleQuery struct {
	Host   string
	Image  string
	Labels map[string]string
}

// RuleResolver finds the effective rule for a container/image.
// Priority (first match wins): config → labels → nil (default semver).
type RuleResolver interface {
	RuleFor(q RuleQuery) *Rule
}

// ConfigRuleResolver resolves rules from the Versentry config only.
type ConfigRuleResolver struct {
	byImage map[string]*Rule
}

// NewConfigRuleResolver builds a resolver from validated config rules.
func NewConfigRuleResolver(rules []config.RuleConfig) (*ConfigRuleResolver, error) {
	byImage := make(map[string]*Rule, len(rules))
	for i, rc := range rules {
		re, err := regexp.Compile(rc.Include)
		if err != nil {
			return nil, fmt.Errorf("rules[%d]: invalid include regex: %w", i, err)
		}
		byImage[rc.Image] = &Rule{
			Image:   rc.Image,
			Include: re,
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

// LabelRuleResolver resolves rules from the versentry.include container label.
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

// RuleFor returns a rule from versentry.include, or nil if absent/invalid.
// Invalid regex is logged and ignored so one bad label cannot break the pass.
func (r *LabelRuleResolver) RuleFor(q RuleQuery) *Rule {
	if r == nil || len(q.Labels) == 0 {
		return nil
	}
	raw := q.Labels[labelInclude]
	if raw == "" {
		return nil
	}
	re, err := regexp.Compile(raw)
	if err != nil {
		r.log.Warn("invalid versentry.include label, ignoring",
			"image", q.Image,
			"include", raw,
			"error", err,
		)
		return nil
	}
	return &Rule{
		Image:   q.Image,
		Include: re,
	}
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
