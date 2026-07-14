package core

import (
	"testing"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/imageref"
)

func TestConfigRuleResolverDockerHubShortName(t *testing.T) {
	res, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "postgres", Include: "^17\\."},
	})
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
	})
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
	})
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
	})
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

func TestConfigRuleResolverModeDigestOnly(t *testing.T) {
	res, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Mode: "digest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rule := res.RuleFor(RuleQuery{Host: imageref.DockerHubHost, Image: "valkey/valkey"})
	if rule == nil {
		t.Fatal("expected mode-only rule")
	}
	if rule.Mode != RuleModeDigest {
		t.Fatalf("mode = %q", rule.Mode)
	}
	if rule.Include != nil {
		t.Fatal("expected nil include for mode-only rule")
	}
}

func TestLabelRuleResolverModeDigest(t *testing.T) {
	res := NewLabelRuleResolver(nil)
	rule := res.RuleFor(RuleQuery{
		Image:  "valkey/valkey",
		Labels: map[string]string{labelMode: "digest"},
	})
	if rule == nil || rule.Mode != RuleModeDigest {
		t.Fatalf("rule = %+v", rule)
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
	if rule == nil || rule.Include == nil || rule.Mode != "" {
		t.Fatalf("expected include-only rule, got %+v", rule)
	}
}

func TestChainConfigRuleBlocksLabelMode(t *testing.T) {
	cfg, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Include: "^9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	chain := NewChainRuleResolver(cfg, NewLabelRuleResolver(nil))
	rule := chain.RuleFor(RuleQuery{
		Host:   imageref.DockerHubHost,
		Image:  "valkey/valkey",
		Labels: map[string]string{labelMode: "digest"},
	})
	if rule == nil {
		t.Fatal("expected config rule")
	}
	if rule.Mode != "" || rule.Include == nil {
		t.Fatalf("config include rule must win whole; got mode=%q include=%v", rule.Mode, rule.Include != nil)
	}
}

func TestConfigRuleResolverGHCRRepo(t *testing.T) {
	res, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "gethomepage/homepage", Include: "^v"},
	})
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

