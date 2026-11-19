package core

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
)

func excludeEngine(t *testing.T, rules []config.RuleConfig, tags []string, log *slog.Logger) *Engine {
	t.Helper()
	if log == nil {
		log = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	}
	cfgRules, err := NewConfigRuleResolver(rules, nil)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, log, NewChainRuleResolver(cfgRules, NewLabelRuleResolver(log)), nil)
	eng.registries = append(eng.registries, &modeTestRegistry{host: imageref.DockerHubHost, listTags: tags})
	return eng
}

func checkExclude(t *testing.T, eng *Engine, c model.Container) containerResult {
	t.Helper()
	res, err := eng.checkContainer(context.Background(), c, registrypass.New(slog.Default()))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestExcludeOnlyDropsUnwantedLine(t *testing.T) {
	// 2.0.0 is a different flavor line we never want; default selection would pick it.
	eng := excludeEngine(t, []config.RuleConfig{{Image: "org/app", Exclude: `^2\.`}},
		[]string{"1.4.0", "1.5.0", "1.5.1", "2.0.0"}, nil)
	res := checkExclude(t, eng, model.Container{Name: "app", ImageRef: "org/app:1.4.0"})
	if res.Status != statusUpdate || res.LatestTag != "1.5.1" {
		t.Fatalf("status=%s latest=%q, want 1.5.1", res.Status, res.LatestTag)
	}
}

func TestIncludeThenExclude(t *testing.T) {
	// include keeps the -alpine flavor (and its prereleases); exclude drops release candidates.
	eng := excludeEngine(t, []config.RuleConfig{{Image: "org/app", Include: `-alpine$`, Exclude: `-rc`}},
		[]string{"1.0.0-alpine", "1.1.0-alpine", "1.2.0-rc1-alpine", "1.2.0", "1.3.0-rc2-alpine"}, nil)
	res := checkExclude(t, eng, model.Container{Name: "app", ImageRef: "org/app:1.0.0-alpine"})
	if res.LatestTag != "1.1.0-alpine" {
		t.Fatalf("latest = %q, want 1.1.0-alpine (rc tags excluded, non-alpine not included)", res.LatestTag)
	}
}

func TestExcludeEverythingSkips(t *testing.T) {
	eng := excludeEngine(t, []config.RuleConfig{{Image: "org/app", Exclude: `.`}}, []string{"1.0.0", "1.1.0"}, nil)
	res := checkExclude(t, eng, model.Container{Name: "app", ImageRef: "org/app:1.0.0"})
	if res.Status != statusSkipped || !strings.Contains(res.Reason, "after exclude") {
		t.Fatalf("status=%s reason=%q", res.Status, res.Reason)
	}
}

func TestExcludeDoesNotFilterRunningTag(t *testing.T) {
	// Running a release candidate that the rule excludes still finds the newer stable release.
	eng := excludeEngine(t, []config.RuleConfig{{Image: "org/app", Exclude: `-rc`}},
		[]string{"1.0.0-rc1", "1.0.0", "1.1.0-rc1"}, nil)
	res := checkExclude(t, eng, model.Container{Name: "app", ImageRef: "org/app:1.0.0-rc1"})
	if res.Status != statusUpdate || res.LatestTag != "1.0.0" {
		t.Fatalf("status=%s latest=%q, want 1.0.0", res.Status, res.LatestTag)
	}
}

func TestExcludeLabelOverridesConfig(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	eng := excludeEngine(t, []config.RuleConfig{{Image: "org/app", Exclude: `^2\.`}},
		[]string{"1.4.0", "1.5.0", "2.0.0", "3.0.0"}, log)
	res := checkExclude(t, eng, model.Container{Name: "app", ImageRef: "org/app:1.4.0",
		Labels: map[string]string{labelExclude: `^3\.`}})
	if res.LatestTag != "2.0.0" {
		t.Fatalf("latest = %q, want 2.0.0 (label exclude ^3 replaces config ^2)", res.LatestTag)
	}
	if !strings.Contains(buf.String(), "fields=exclude") {
		t.Fatalf("missing override INFO: %s", buf.String())
	}
}

func TestInvalidExcludeLabelIgnored(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	eng := excludeEngine(t, []config.RuleConfig{{Image: "org/app", Exclude: `^2\.`}},
		[]string{"1.4.0", "1.5.0", "2.0.0"}, log)
	res := checkExclude(t, eng, model.Container{Name: "app", ImageRef: "org/app:1.4.0",
		Labels: map[string]string{labelExclude: `(`}})
	if res.LatestTag != "1.5.0" || !strings.Contains(buf.String(), "invalid versentry.exclude label") {
		t.Fatalf("latest=%q log=%s; want config exclude kept and a WARN", res.LatestTag, buf.String())
	}
}

func TestExcludeIgnoredUnderDigestWarns(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	eng := excludeEngine(t, []config.RuleConfig{{Image: "org/app", Exclude: `-rc`, Track: "digest"}}, nil, log)
	eng.logTrackingDiagnostics("app", "org/app", "latest", "digest", digestCauseRule,
		eng.rules.RuleFor(RuleQuery{Host: imageref.DockerHubHost, Image: "org/app"}))
	if !strings.Contains(buf.String(), "exclude rule ignored") {
		t.Fatalf("missing WARN: %s", buf.String())
	}
}
