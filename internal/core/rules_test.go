package core

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/imageref"
)

func TestConfigRuleResolverDockerHubShortName(t *testing.T) {
	res, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "postgres", Include: "^17\\."},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	rule := res.RuleFor(RuleQuery{
		Host:  imageref.DockerHubHost,
		Image: "library/postgres",
	})
	if rule == nil {
		t.Fatal("expected rule for library/postgres via postgres key")
	}
	if !rule.Include.MatchString("17.10-alpine3.24") {
		t.Fatal("wrong include regex")
	}
}

func TestConfigRuleResolverDockerHubLibraryNameBackwardCompat(t *testing.T) {
	res, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "library/postgres", Include: "^17\\."},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	rule := res.RuleFor(RuleQuery{
		Host:  imageref.DockerHubHost,
		Image: "library/postgres",
	})
	if rule == nil {
		t.Fatal("expected rule for library/postgres key")
	}
}

func TestConfigRuleResolverNoDockerHubAlias(t *testing.T) {
	res, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "postgres", Include: "^17\\."},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if rule := res.RuleFor(RuleQuery{Host: "ghcr.io", Image: "gethomepage/homepage"}); rule != nil {
		t.Fatal("postgres rule must not match unrelated ghcr repo")
	}
	if rule := res.RuleFor(RuleQuery{Host: "registry.example.com", Image: "library/postgres"}); rule != nil {
		t.Fatal("postgres rule must not match library/postgres on non-Docker-Hub host")
	}
	if rule := res.RuleFor(RuleQuery{Host: "registry.example.com", Image: "postgres"}); rule == nil {
		t.Fatal("expected exact postgres match on private registry")
	}
}

func TestConfigRuleResolverSlashRepoUnchanged(t *testing.T) {
	res, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "chatwoot/chatwoot", Include: "^v"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if rule := res.RuleFor(RuleQuery{Host: imageref.DockerHubHost, Image: "chatwoot/chatwoot"}); rule == nil {
		t.Fatal("expected chatwoot rule on docker hub")
	}
	if rule := res.RuleFor(RuleQuery{Host: "ghcr.io", Image: "gethomepage/homepage"}); rule != nil {
		t.Fatal("chatwoot rule must not match other repos")
	}
}

func TestConfigRuleResolverTrackDigestOnly(t *testing.T) {
	res, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Track: "digest"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rule := res.RuleFor(RuleQuery{Host: imageref.DockerHubHost, Image: "valkey/valkey"})
	if rule == nil {
		t.Fatal("expected track-only rule")
	}
	if rule.Track != RuleTrackDigest {
		t.Fatalf("track = %q", rule.Track)
	}
	if rule.Include != nil {
		t.Fatal("expected nil include for track-only rule")
	}
}

func TestConfigRuleResolverModeDigestAliasWarns(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	res, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Mode: "digest"},
	}, log)
	if err != nil {
		t.Fatal(err)
	}
	rule := res.RuleFor(RuleQuery{Host: imageref.DockerHubHost, Image: "valkey/valkey"})
	if rule == nil || rule.Track != RuleTrackDigest {
		t.Fatalf("rule = %+v", rule)
	}
	out := buf.String()
	if !strings.Contains(out, "rules[].mode is deprecated, use track instead") {
		t.Fatalf("expected deprecation WARN, got %q", out)
	}
	if !strings.Contains(out, "valkey/valkey") {
		t.Fatalf("expected image in WARN, got %q", out)
	}
}

func TestLabelRuleResolverTrackDigest(t *testing.T) {
	res := NewLabelRuleResolver(nil)
	rule := res.RuleFor(RuleQuery{
		Container: "cache",
		Image:     "valkey/valkey",
		Labels:    map[string]string{labelTrack: "digest"},
	})
	if rule == nil || rule.Track != RuleTrackDigest {
		t.Fatalf("rule = %+v", rule)
	}
}

func TestLabelRuleResolverModeDigestAliasWarns(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	res := NewLabelRuleResolver(log)
	rule := res.RuleFor(RuleQuery{
		Container: "cache",
		Image:     "valkey/valkey",
		Labels:    map[string]string{labelMode: "digest"},
	})
	if rule == nil || rule.Track != RuleTrackDigest {
		t.Fatalf("rule = %+v", rule)
	}
	out := buf.String()
	if !strings.Contains(out, "versentry.mode is deprecated, use versentry.track instead") {
		t.Fatalf("expected deprecation WARN, got %q", out)
	}
	if !strings.Contains(out, "cache") {
		t.Fatalf("expected container in WARN, got %q", out)
	}
}

func TestLabelRuleResolverBothTrackAndModeUsesTrackWarns(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	res := NewLabelRuleResolver(log)
	rule := res.RuleFor(RuleQuery{
		Container: "cache",
		Image:     "valkey/valkey",
		Labels: map[string]string{
			labelTrack: "digest",
			labelMode:  "digest",
		},
	})
	if rule == nil || rule.Track != RuleTrackDigest {
		t.Fatalf("rule = %+v", rule)
	}
	out := buf.String()
	if !strings.Contains(out, "versentry.mode and versentry.track both set; using track") {
		t.Fatalf("expected both-set WARN, got %q", out)
	}
}

func TestLabelRuleResolverInvalidTrackIgnored(t *testing.T) {
	res := NewLabelRuleResolver(nil)
	rule := res.RuleFor(RuleQuery{
		Image:  "valkey/valkey",
		Labels: map[string]string{labelTrack: "semver"},
	})
	if rule != nil {
		t.Fatalf("expected nil rule for invalid track alone, got %+v", rule)
	}
}

func TestLabelRuleResolverInvalidModeIgnored(t *testing.T) {
	res := NewLabelRuleResolver(nil)
	rule := res.RuleFor(RuleQuery{
		Image:  "valkey/valkey",
		Labels: map[string]string{labelMode: "semver"},
	})
	if rule != nil {
		t.Fatalf("expected nil rule for invalid mode alone, got %+v", rule)
	}
}

func TestLabelRuleResolverInvalidModeKeepsInclude(t *testing.T) {
	res := NewLabelRuleResolver(nil)
	rule := res.RuleFor(RuleQuery{
		Image: "chatwoot/chatwoot",
		Labels: map[string]string{
			labelInclude: "^v",
			labelMode:    "foo",
		},
	})
	if rule == nil || rule.Include == nil || rule.Track != "" {
		t.Fatalf("expected include-only rule, got %+v", rule)
	}
}

func TestChainConfigRuleBlocksLabelTrack(t *testing.T) {
	cfg, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Include: "^9"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	chain := NewChainRuleResolver(cfg, NewLabelRuleResolver(nil))
	rule := chain.RuleFor(RuleQuery{
		Host:   imageref.DockerHubHost,
		Image:  "valkey/valkey",
		Labels: map[string]string{labelTrack: "digest"},
	})
	if rule == nil {
		t.Fatal("expected config rule")
	}
	if rule.Track != "" || rule.Include == nil {
		t.Fatalf("config include rule must win whole; got track=%q include=%v", rule.Track, rule.Include != nil)
	}
}

func TestConfigRuleResolverGHCRRepo(t *testing.T) {
	res, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "gethomepage/homepage", Include: "^v"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if rule := res.RuleFor(RuleQuery{Host: "ghcr.io", Image: "gethomepage/homepage"}); rule == nil {
		t.Fatal("expected ghcr repo match")
	}
	if rule := res.RuleFor(RuleQuery{Host: imageref.DockerHubHost, Image: "gethomepage/homepage"}); rule == nil {
		t.Fatal("expected same repo path on docker hub if ever used")
	}
}
