package core

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
)

type modeTestProvider struct {
	localDigest string
}

func (p *modeTestProvider) ListRunning(ctx context.Context) ([]model.Container, error) {
	return nil, nil
}

func (p *modeTestProvider) LocalDigest(ctx context.Context, c model.Container, repo string) (string, error) {
	return p.localDigest, nil
}

func (p *modeTestProvider) Ping(ctx context.Context) error { return nil }

type modeTestRegistry struct {
	host         string
	listTags     []string
	listCalls    int
	remoteDigest string
	digestCalls  int
}

func (r *modeTestRegistry) Host() string { return r.host }

func (r *modeTestRegistry) ListTags(ctx context.Context, repo string) ([]string, error) {
	r.listCalls++
	return r.listTags, nil
}

func (r *modeTestRegistry) TagDigest(ctx context.Context, repo, tag string) (string, error) {
	r.digestCalls++
	return r.remoteDigest, nil
}

func TestModeDigestPath(t *testing.T) {
	reg := &modeTestRegistry{
		host:         imageref.DockerHubHost,
		listTags:     []string{"9-alpine", "9.1", "9.0"},
		remoteDigest: "sha256:remotebbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	rules, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Mode: "digest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(
		&modeTestProvider{localDigest: "sha256:localaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		nil,
		config.Timeouts{},
		slog.Default(),
		rules,
	)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "valkey", ImageRef: "valkey/valkey:9-alpine"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(slog.Default()))
	if err != nil {
		t.Fatal(err)
	}
	if reg.listCalls != 0 {
		t.Fatalf("ListTags calls = %d, want 0 (forced digest)", reg.listCalls)
	}
	if reg.digestCalls != 1 {
		t.Fatalf("TagDigest calls = %d, want 1", reg.digestCalls)
	}
	if result.Status != statusUpdate {
		t.Fatalf("status = %s, want update", result.Status)
	}
	if result.Update == nil || result.Update.LatestTag != "" {
		t.Fatalf("expected digest update without LatestTag, got %+v", result.Update)
	}
}

func TestModeAbsentKeepsSemverForAlpineTag(t *testing.T) {
	reg := &modeTestRegistry{
		host:     imageref.DockerHubHost,
		listTags: []string{"9-alpine", "9.1", "9.0"},
	}
	eng := NewEngine(
		&modeTestProvider{},
		nil,
		config.Timeouts{},
		slog.Default(),
		nil,
	)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "valkey", ImageRef: "valkey/valkey:9-alpine"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(slog.Default()))
	if err != nil {
		t.Fatal(err)
	}
	if reg.listCalls != 1 {
		t.Fatalf("ListTags calls = %d, want 1", reg.listCalls)
	}
	if reg.digestCalls != 0 {
		t.Fatalf("TagDigest calls = %d, want 0", reg.digestCalls)
	}
	if result.Status != statusUpdate || result.LatestTag != "9.1" {
		t.Fatalf("expected semver update to 9.1, got status=%s latest=%q", result.Status, result.LatestTag)
	}
}

func TestModeDigestWithIncludeWarnsAndIgnoresInclude(t *testing.T) {
	var warnBuf strings.Builder
	log := slog.New(slog.NewTextHandler(&warnBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	reg := &modeTestRegistry{
		host:         imageref.DockerHubHost,
		listTags:     []string{"9-alpine", "9.1"},
		remoteDigest: "sha256:same",
	}
	rules, err := NewConfigRuleResolver([]config.RuleConfig{
		{Image: "valkey/valkey", Mode: "digest", Include: "^9-alpine$"},
	})
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(
		&modeTestProvider{localDigest: "sha256:same"},
		nil,
		config.Timeouts{},
		log,
		rules,
	)
	eng.registries = append(eng.registries, reg)

	c := model.Container{Name: "valkey", ImageRef: "valkey/valkey:9-alpine"}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(log))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != statusUpToDate {
		t.Fatalf("status = %s", result.Status)
	}
	if reg.listCalls != 0 {
		t.Fatal("include must not trigger ListTags under mode=digest")
	}
	if !strings.Contains(warnBuf.String(), "include ignored: mode=digest") {
		t.Fatalf("expected include-ignored WARN, got %q", warnBuf.String())
	}
}

func TestLabelModeDigestForcesDigest(t *testing.T) {
	reg := &modeTestRegistry{
		host:         imageref.DockerHubHost,
		remoteDigest: "sha256:remote",
	}
	eng := NewEngine(
		&modeTestProvider{localDigest: "sha256:local"},
		nil,
		config.Timeouts{},
		slog.Default(),
		NewLabelRuleResolver(slog.Default()),
	)
	eng.registries = append(eng.registries, reg)

	c := model.Container{
		Name:     "valkey",
		ImageRef: "valkey/valkey:9-alpine",
		Labels:   map[string]string{labelMode: "digest"},
	}
	result, err := eng.checkContainer(context.Background(), c, registrypass.New(slog.Default()))
	if err != nil {
		t.Fatal(err)
	}
	if reg.digestCalls != 1 || reg.listCalls != 0 {
		t.Fatalf("digestCalls=%d listCalls=%d", reg.digestCalls, reg.listCalls)
	}
	if result.Status != statusUpdate {
		t.Fatalf("status = %s", result.Status)
	}
}
