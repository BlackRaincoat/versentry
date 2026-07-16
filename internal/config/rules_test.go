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

func TestValidateRulesTrackDigestOnlyOK(t *testing.T) {
	if err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Track: "digest"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRulesModeDigestAliasOK(t *testing.T) {
	if err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Mode: "digest"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRulesModeAndTrackFails(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Mode: "digest", Track: "digest"},
	})
	if err == nil {
		t.Fatal("expected conflict when mode and track both set")
	}
	if !strings.Contains(err.Error(), "use track only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRulesTrackDigestWithIncludeOK(t *testing.T) {
	if err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Track: "digest", Include: "^9-alpine$"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRulesUnknownTrackFails(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Track: "semver"},
	})
	if err == nil {
		t.Fatal("expected unknown track error")
	}
	if !strings.Contains(err.Error(), `only "digest" supported`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRulesUnknownModeAliasFails(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Mode: "semver"},
	})
	if err == nil {
		t.Fatal("expected unknown track error for deprecated mode alias")
	}
	if !strings.Contains(err.Error(), `only "digest" supported`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRulesEmptyIncludeWithoutTrackFails(t *testing.T) {
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

func TestEffectiveRuleTrackPrefersTrackOverMode(t *testing.T) {
	got := EffectiveRuleTrack(RuleConfig{Track: "digest", Mode: "digest"})
	if got != "digest" {
		t.Fatalf("got %q", got)
	}
}

func TestRuleUsesDeprecatedMode(t *testing.T) {
	if !RuleUsesDeprecatedMode(RuleConfig{Mode: "digest"}) {
		t.Fatal("expected deprecated mode")
	}
	if RuleUsesDeprecatedMode(RuleConfig{Track: "digest"}) {
		t.Fatal("track field is not deprecated")
	}
}
