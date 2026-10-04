package core

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
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

func TestChainLabelOverridesConfigPerField(t *testing.T) {
	week := config.Duration{Duration: 7 * 24 * time.Hour}
	cfg, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Include: "^9", MinAge: &week},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	chain := NewChainRuleResolver(cfg, NewLabelRuleResolver(nil))
	q := RuleQuery{Host: imageref.DockerHubHost, Image: "valkey/valkey"}

	// Label track: digest overrides; config min_age is inherited.
	q.Labels = map[string]string{labelTrack: "digest"}
	rule := chain.RuleFor(q)
	if rule == nil || rule.Track != RuleTrackDigest || rule.MinAge == nil || *rule.MinAge != week.Duration {
		t.Fatalf("rule = %+v, want label track + inherited min_age", rule)
	}
	if strings.Join(rule.Overridden, ",") != "track" {
		t.Fatalf("Overridden = %v, want [track]", rule.Overridden)
	}

	// Label include replaces config include.
	q.Labels = map[string]string{labelInclude: "^8"}
	rule = chain.RuleFor(q)
	if rule == nil || rule.Include.String() != "^8" || strings.Join(rule.Overridden, ",") != "include" {
		t.Fatalf("rule = %+v, want label include ^8 overriding ^9", rule)
	}

	// No labels: config rule unchanged, nothing overridden.
	q.Labels = nil
	rule = chain.RuleFor(q)
	if rule == nil || rule.Include.String() != "^9" || len(rule.Overridden) != 0 {
		t.Fatalf("rule = %+v, want plain config rule", rule)
	}
}

func TestChainDoesNotMutateConfigRule(t *testing.T) {
	cfg, err := NewConfigRuleResolver([]config.RuleConfig{{Image: "valkey/valkey", Include: "^9"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	chain := NewChainRuleResolver(cfg, NewLabelRuleResolver(nil))
	chain.RuleFor(RuleQuery{Host: imageref.DockerHubHost, Image: "valkey/valkey", Labels: map[string]string{labelInclude: "^8"}})
	base := cfg.RuleFor(RuleQuery{Host: imageref.DockerHubHost, Image: "valkey/valkey"})
	if base.Include.String() != "^9" || len(base.Overridden) != 0 {
		t.Fatalf("config rule mutated by a container's labels: %+v", base)
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

func TestCheckContainerLabelOverridesConfigInclude(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	cfgRules, err := NewConfigRuleResolver([]config.RuleConfig{{Image: "postgres", Include: `^17\.`}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg := &modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"16.4", "16.9", "17.2", "17.6"}}
	eng := NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, log, NewChainRuleResolver(cfgRules, NewLabelRuleResolver(log)), nil)
	eng.registries = append(eng.registries, reg)

	check := func(c model.Container) containerResult {
		t.Helper()
		res, err := eng.checkContainer(context.Background(), c, registrypass.New(log))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	if res := check(model.Container{Name: "db17", ImageRef: "postgres:17.2"}); res.LatestTag != "17.6" {
		t.Fatalf("config rule: latest = %q, want 17.6", res.LatestTag)
	}
	res := check(model.Container{Name: "db16", ImageRef: "postgres:16.4", Labels: map[string]string{labelInclude: `^16\.`}})
	if res.Status != statusUpdate || res.LatestTag != "16.9" {
		t.Fatalf("label override: status=%s latest=%q, want 16.9", res.Status, res.LatestTag)
	}
	if !strings.Contains(buf.String(), "container labels override config rule") || !strings.Contains(buf.String(), "fields=include") {
		t.Fatalf("missing override INFO: %s", buf.String())
	}
}

func TestLabelTrackAutoOverridesConfigDigest(t *testing.T) {
	cfg, err := NewConfigRuleResolver([]config.RuleConfig{{Image: "valkey/valkey", Track: "digest"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	chain := NewChainRuleResolver(cfg, NewLabelRuleResolver(nil))
	rule := chain.RuleFor(RuleQuery{Host: imageref.DockerHubHost, Image: "valkey/valkey", Labels: map[string]string{labelTrack: "auto"}})
	if rule == nil || rule.Track != RuleTrackAuto || strings.Join(rule.Overridden, ",") != "track" {
		t.Fatalf("rule = %+v, want track auto overriding digest", rule)
	}
	mode, _, _ := resolveTrackingMode(chain, imageref.DockerHubHost, "valkey/valkey", "9.0.1", "cache",
		map[string]string{labelTrack: "auto"})
	if mode != "semver" {
		t.Fatalf("mode = %q, want semver after track=auto", mode)
	}
	mode, _, _ = resolveTrackingMode(chain, imageref.DockerHubHost, "valkey/valkey", "9.0.1", "cache2", nil)
	if mode != "digest" {
		t.Fatalf("other containers keep config digest, got %q", mode)
	}
}

func TestLabelMinAge(t *testing.T) {
	week := config.Duration{Duration: 7 * 24 * time.Hour}
	cfg, err := NewConfigRuleResolver([]config.RuleConfig{{Image: "grafana/grafana", MinAge: &week}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	chain := NewChainRuleResolver(cfg, NewLabelRuleResolver(nil))
	q := RuleQuery{Host: imageref.DockerHubHost, Image: "grafana/grafana"}

	for raw, want := range map[string]time.Duration{"0": 0, "1d12h": 36 * time.Hour, "2h": 2 * time.Hour} {
		q.Labels = map[string]string{labelMinAge: raw}
		rule := chain.RuleFor(q)
		if rule == nil || rule.MinAge == nil || *rule.MinAge != want || strings.Join(rule.Overridden, ",") != "min_age" {
			t.Fatalf("label %q: rule = %+v, want min_age %v overriding config", raw, rule, want)
		}
	}

	var buf bytes.Buffer
	labels := NewLabelRuleResolver(slog.New(slog.NewTextHandler(&buf, nil)))
	for _, bad := range []string{"soon", "-1d"} {
		if rule := labels.RuleFor(RuleQuery{Image: "x", Labels: map[string]string{labelMinAge: bad}}); rule != nil {
			t.Fatalf("invalid %q must be ignored, got %+v", bad, rule)
		}
	}
	if !strings.Contains(buf.String(), "invalid versentry.min_age label") {
		t.Fatalf("missing WARN: %s", buf.String())
	}

	// Label-only min_age without any config rule applies by itself.
	rule := NewChainRuleResolver(nil, NewLabelRuleResolver(nil)).RuleFor(RuleQuery{Image: "x", Labels: map[string]string{labelMinAge: "3d"}})
	if rule == nil || rule.MinAge == nil || *rule.MinAge != 72*time.Hour {
		t.Fatalf("rule = %+v", rule)
	}
}

func TestLabelTrackOverrideCountedOnlyWhenDetectionChanges(t *testing.T) {
	cfg, err := NewConfigRuleResolver([]config.RuleConfig{{Image: "nginx", Include: `^1\.`}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	chain := NewChainRuleResolver(cfg, NewLabelRuleResolver(nil))
	q := RuleQuery{Host: imageref.DockerHubHost, Image: "library/nginx"}

	q.Labels = map[string]string{labelTrack: "auto"}
	if rule := chain.RuleFor(q); rule == nil || len(rule.Overridden) != 0 {
		t.Fatalf("auto over a rule without track changes nothing, got Overridden=%v", rule.Overridden)
	}
	q.Labels = map[string]string{labelTrack: "digest"}
	if rule := chain.RuleFor(q); rule == nil || strings.Join(rule.Overridden, ",") != "track" {
		t.Fatalf("digest over an include-only rule changes detection, got %+v", rule)
	}
}
