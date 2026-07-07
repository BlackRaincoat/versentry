package imageref

import (
	"slices"
	"testing"
)

func TestRuleLookupKeys(t *testing.T) {
	cases := []struct {
		host string
		repo string
		want []string
	}{
		{DockerHubHost, "library/postgres", []string{"library/postgres", "postgres"}},
		{DockerHubHost, "postgres", []string{"postgres", "library/postgres"}},
		{DockerHubHost, "library/caddy", []string{"library/caddy", "caddy"}},
		{DockerHubHost, "chatwoot/chatwoot", []string{"chatwoot/chatwoot"}},
		{DockerHubHost, "blackraincoat/versentry", []string{"blackraincoat/versentry"}},
		{"ghcr.io", "gethomepage/homepage", []string{"gethomepage/homepage"}},
		{"registry.example.com", "myorg/myapp", []string{"myorg/myapp"}},
		{"registry.example.com", "myapp", []string{"myapp"}},
		{"registry.example.com", "library/postgres", []string{"library/postgres"}},
	}
	for _, tc := range cases {
		got := RuleLookupKeys(tc.host, tc.repo)
		if !slices.Equal(got, tc.want) {
			t.Errorf("RuleLookupKeys(%q, %q) = %v, want %v", tc.host, tc.repo, got, tc.want)
		}
	}
}

func TestRuleConfigKeys(t *testing.T) {
	if !slices.Equal(RuleConfigKeys("postgres"), []string{"postgres", "library/postgres"}) {
		t.Fatalf("RuleConfigKeys(postgres) = %v", RuleConfigKeys("postgres"))
	}
	if !slices.Equal(RuleConfigKeys("library/postgres"), []string{"library/postgres", "postgres"}) {
		t.Fatalf("RuleConfigKeys(library/postgres) = %v", RuleConfigKeys("library/postgres"))
	}
	if !slices.Equal(RuleConfigKeys("chatwoot/chatwoot"), []string{"chatwoot/chatwoot"}) {
		t.Fatalf("RuleConfigKeys(chatwoot/chatwoot) = %v", RuleConfigKeys("chatwoot/chatwoot"))
	}
}
