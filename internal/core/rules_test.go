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
