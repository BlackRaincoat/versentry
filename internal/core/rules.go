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
	labelMode    = "versentry.mode"
)

// RuleModeDigest forces digest detection for an image/container.
const RuleModeDigest = "digest"

// Rule is a resolved rule for one image: optional include filter and/or mode.
type Rule struct {
	Image   string
	Include *regexp.Regexp // nil when unset (e.g. mode-only digest rule)
	Mode    string         // "" or RuleModeDigest
}

// RuleQuery is the input for rule resolution: image identity plus container labels.
type RuleQuery struct {
	Host   string
	Image  string
	Labels map[string]string
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
func NewConfigRuleResolver(rules []config.RuleConfig) (*ConfigRuleResolver, error) {
	byImage := make(map[string]*Rule, len(rules))
	for i, rc := range rules {
		var re *regexp.Regexp
		if rc.Include != "" {
			compiled, err := regexp.Compile(rc.Include)
			if err != nil {
				return nil, fmt.Errorf("rules[%d]: invalid include regex: %w", i, err)
			}
			re = compiled
		}
		mode := strings.TrimSpace(rc.Mode)
		byImage[rc.Image] = &Rule{
			Image:   rc.Image,
			Include: re,
			Mode:    mode,
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

// LabelRuleResolver resolves rules from versentry.include / versentry.mode labels.
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
// Invalid include or mode is logged and ignored so one bad label cannot break the pass.
func (r *LabelRuleResolver) RuleFor(q RuleQuery) *Rule {
	if r == nil || len(q.Labels) == 0 {
		return nil
	}

	var include *regexp.Regexp
	if raw := q.Labels[labelInclude]; raw != "" {
		re, err := regexp.Compile(raw)
		if err != nil {
			r.log.Warn("invalid versentry.include label, ignoring",
				"image", q.Image,
				"include", raw,
				"error", err,
			)
		} else {
			include = re
		}
	}

	mode := ""
	if raw := strings.TrimSpace(q.Labels[labelMode]); raw != "" {
		if raw != RuleModeDigest {
			r.log.Warn("invalid versentry.mode label, ignoring",
				"image", q.Image,
				"mode", raw,
			)
		} else {
			mode = RuleModeDigest
		}
	}

	if include == nil && mode == "" {
		return nil
	}
	return &Rule{
		Image:   q.Image,
		Include: include,
		Mode:    mode,
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
