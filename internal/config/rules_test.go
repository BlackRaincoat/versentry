package config

import (
	"strings"
	"testing"
)

func TestValidateRulesDockerHubAliasConflict(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "postgres", Include: "^17"},
		{Image: "library/postgres", Include: "^16"},
	})
	if err == nil {
		t.Fatal("expected conflict between postgres and library/postgres")
	}
	if !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRulesDistinctImagesOK(t *testing.T) {
	if err := validateRules([]RuleConfig{
		{Image: "postgres", Include: "^17"},
		{Image: "chatwoot/chatwoot", Include: "^v"},
		{Image: "gethomepage/homepage", Include: "^v"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRulesModeDigestOnlyOK(t *testing.T) {
	if err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Mode: "digest"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRulesModeDigestWithIncludeOK(t *testing.T) {
	if err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Mode: "digest", Include: "^9-alpine$"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRulesUnknownModeFails(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Mode: "semver"},
	})
	if err == nil {
		t.Fatal("expected unknown mode error")
	}
	if !strings.Contains(err.Error(), `only "digest" supported`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRulesEmptyIncludeWithoutModeFails(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "valkey/valkey"},
	})
	if err == nil {
		t.Fatal("expected include required error")
	}
	if !strings.Contains(err.Error(), "include is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
