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
