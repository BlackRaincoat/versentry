package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/provider"
	"github.com/BlackRaincoat/versentry/internal/registry"
)

const (
	layerA = "sha256:aaaa"
	layerB = "sha256:bbbb"
	layerC = "sha256:cccc"
)

// layeredProvider adds LocalImage (provider.LocalImageReader) to modeTestProvider.
type layeredProvider struct {
	modeTestProvider
	image provider.LocalImage
	err   error
	calls int
}

func (p *layeredProvider) LocalImage(ctx context.Context, c model.Container) (provider.LocalImage, error) {
	p.calls++
	img := p.image
	img.ID += "-" + c.Name // one image per container (distinct cache keys)
	return img, p.err
}

// newLayerEngine: local image runs layers A,B; the tag's remote digest moved.
func newLayerEngine(t *testing.T, reg *agedRegistry, minAge time.Duration) (*Engine, *layeredProvider) {
	t.Helper()
	eng := newMinAgeEngine(t, reg, minAge, nil, "sha256:localindex")
	prov := &layeredProvider{
		modeTestProvider: modeTestProvider{localDigest: "sha256:localindex"},
		image:            provider.LocalImage{ID: "sha256:img1", Layers: []string{layerA, layerB}},
	}
	eng.provider = prov
	return eng, prov
}

func movedIndex(index bool, diffIDs ...string) *agedRegistry {
	return &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, remoteDigest: "sha256:remoteindex"},
		index:            index,
		diffIDs:          diffIDs,
	}
}

// checkPass runs one container through a fresh pass, like one RunOnce.
func checkPass(t *testing.T, eng *Engine, c model.Container) containerResult {
	t.Helper()
	res, err := eng.checkContainer(context.Background(), c, registrypass.New(slog.Default()))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestMovedIndexSameLayersIsNotAnUpdate(t *testing.T) {
	reg := movedIndex(true, layerA, layerB)
	eng, _ := newLayerEngine(t, reg, 0)

	res := checkPass(t, eng, digestApp(nil))
	if res.Status != statusUpToDate || res.Reason != sameLayersReason || res.Update != nil {
		t.Fatalf("result = %+v, want up-to-date (same layers)", res)
	}
	if len(reg.createdCalls) != 1 {
		t.Fatalf("lookups = %v, want 1", reg.createdCalls)
	}

	// Next pass: the index is still moved; the verdict is cached, no new pull.
	if res := checkPass(t, eng, digestApp(nil)); res.Status != statusUpToDate {
		t.Fatalf("second pass = %+v", res)
	}
	if len(reg.createdCalls) != 1 {
		t.Fatalf("lookups = %v, want cached verdict", reg.createdCalls)
	}
}

func TestMovedIndexChangedLayersIsAnUpdate(t *testing.T) {
	reg := movedIndex(true, layerA, layerC)
	eng, _ := newLayerEngine(t, reg, 0)
	if res := checkPass(t, eng, digestApp(nil)); res.Status != statusUpdate || res.Update == nil {
		t.Fatalf("result = %+v, want update", res)
	}
	// "Different" is cached too: the update is re-detected without another pull.
	checkPass(t, eng, digestApp(nil))
	if len(reg.createdCalls) != 1 {
		t.Fatalf("lookups = %v, want 1", reg.createdCalls)
	}
}

func TestMovedSingleManifestIsAnUpdateEvenWithSameLayers(t *testing.T) {
	// A single-platform manifest moved: its config changed even if layers did not.
	reg := movedIndex(false, layerA, layerB)
	eng, _ := newLayerEngine(t, reg, 0)
	if res := checkPass(t, eng, digestApp(nil)); res.Status != statusUpdate {
		t.Fatalf("result = %+v, want update", res)
	}
}

func TestLayerCheckFailsOpen(t *testing.T) {
	t.Run("registry error, not cached", func(t *testing.T) {
		reg := movedIndex(true, layerA, layerB)
		reg.createdErr = errors.New("boom")
		eng, _ := newLayerEngine(t, reg, 0)
		if res := checkPass(t, eng, digestApp(nil)); res.Status != statusUpdate {
			t.Fatalf("result = %+v, want update", res)
		}
		reg.createdErr = nil
		if res := checkPass(t, eng, digestApp(nil)); res.Status != statusUpToDate {
			t.Fatalf("after recovery = %+v, want a fresh check", res)
		}
	})
	t.Run("local image unreadable", func(t *testing.T) {
		reg := movedIndex(true, layerA, layerB)
		eng, prov := newLayerEngine(t, reg, 0)
		prov.err = errors.New("inspect failed")
		if res := checkPass(t, eng, digestApp(nil)); res.Status != statusUpdate || len(reg.createdCalls) != 0 {
			t.Fatalf("result = %+v, lookups %v", res, reg.createdCalls)
		}
	})
	t.Run("registry cannot read config", func(t *testing.T) {
		reg := movedIndex(true) // no diff IDs
		eng, _ := newLayerEngine(t, reg, 0)
		if res := checkPass(t, eng, digestApp(nil)); res.Status != statusUpdate {
			t.Fatalf("result = %+v, want update", res)
		}
	})
	t.Run("tag moved again between HEAD and GET", func(t *testing.T) {
		// HEAD saw remoteindex; ImageInfo reads a newer index: no verdict.
		eng, _ := newLayerEngine(t, movedIndex(true, layerA, layerB), 0)
		eng.registries[0] = &movedAgainRegistry{agedRegistry: eng.registries[0].(*agedRegistry)}
		if res := checkPass(t, eng, digestApp(nil)); res.Status != statusUpdate {
			t.Fatalf("result = %+v, want update", res)
		}
		if len(eng.movedFacts) != 0 {
			t.Fatalf("facts = %v, want none cached", eng.movedFacts)
		}
	})
}

// movedAgainRegistry reports an ImageInfo digest other than TagDigest's.
type movedAgainRegistry struct{ *agedRegistry }

func (r *movedAgainRegistry) ImageInfo(ctx context.Context, repo, tag string) (registry.ImageInfo, error) {
	info, err := r.agedRegistry.ImageInfo(ctx, repo, tag)
	info.Digest = "newerindex"
	return info, err
}

func TestLayerCheckSharesMinAgeLookup(t *testing.T) {
	reg := movedIndex(true, layerA, layerB)
	reg.created = map[string]time.Time{"latest": daysAgo(1)} // too young for min_age
	eng, _ := newLayerEngine(t, reg, 3*24*time.Hour)
	// Same layers: up to date, not pending — and one lookup serves both.
	if res := checkPass(t, eng, digestApp(nil)); res.Status != statusUpToDate {
		t.Fatalf("result = %+v, want up-to-date", res)
	}
	if len(reg.createdCalls) != 1 {
		t.Fatalf("lookups = %v, want 1", reg.createdCalls)
	}
}

func TestLayerCheckCapPerPass(t *testing.T) {
	reg := movedIndex(true, layerA, layerB)
	eng, _ := newLayerEngine(t, reg, 0)
	pass := registrypass.New(slog.Default())
	eng.movedSeen, eng.movedLookups = make(map[string]struct{}), 0
	var upToDate, updates int
	for i := 0; i < maxMovedImageLookups+2; i++ {
		c := model.Container{Name: fmt.Sprintf("app%d", i), ImageRef: fmt.Sprintf("org/app%d:latest", i)}
		res, err := eng.checkContainer(context.Background(), c, pass)
		if err != nil {
			t.Fatal(err)
		}
		switch res.Status {
		case statusUpToDate:
			upToDate++
		case statusUpdate:
			updates++
		}
	}
	if len(reg.createdCalls) != maxMovedImageLookups || upToDate != maxMovedImageLookups || updates != 2 {
		t.Fatalf("lookups=%d up-to-date=%d updates=%d", len(reg.createdCalls), upToDate, updates)
	}
}

func TestPruneMovedFacts(t *testing.T) {
	eng, _ := newLayerEngine(t, movedIndex(true, layerA, layerB), 0)
	eng.movedFacts = map[string]registry.ImageInfo{"gone@x": {}, "kept@y": {}}
	eng.movedSeen = map[string]struct{}{"kept@y": {}}
	eng.pruneMovedFacts()
	if _, ok := eng.movedFacts["gone@x"]; ok || len(eng.movedFacts) != 1 {
		t.Fatalf("facts = %v", eng.movedFacts)
	}
}

// --- ignore_rebuilds ---

// newRebuildEngine: provider without layer info (version check only).
func newRebuildEngine(t *testing.T, reg *agedRegistry, global bool, rules []config.RuleConfig) *Engine {
	t.Helper()
	eng := newMinAgeEngine(t, reg, 0, rules, "sha256:localindex")
	eng.SetIgnoreRebuilds(global)
	if rules != nil {
		eng.rules = NewChainRuleResolver(eng.rules, NewLabelRuleResolver(slog.Default()))
	} else {
		eng.rules = NewLabelRuleResolver(slog.Default())
	}
	return eng
}

func rebuiltTo(version string) *agedRegistry {
	reg := movedIndex(true)
	reg.versions = map[string]string{"latest": version}
	return reg
}

func TestIgnoreRebuilds(t *testing.T) {
	running := map[string]string{ociVersionLabel: "2.3.1"}
	yes, no := true, false
	cases := []struct {
		name   string
		reg    *agedRegistry
		global bool
		rules  []config.RuleConfig
		labels map[string]string
		want   checkStatus
		calls  int
	}{
		{"off by default: rebuild is an update, no lookup", rebuiltTo("2.3.1"), false, nil, running, statusUpdate, 0},
		{"global: same version is not an update", rebuiltTo("2.3.1"), true, nil, running, statusUpToDate, 1},
		{"global: new version is an update", rebuiltTo("2.4.0"), true, nil, running, statusUpdate, 1},
		{"running image unlabeled: update, no lookup", rebuiltTo("2.3.1"), true, nil, nil, statusUpdate, 0},
		{"new image unlabeled: update", rebuiltTo(""), true, nil, running, statusUpdate, 1},
		{"rule enables", rebuiltTo("2.3.1"), false, []config.RuleConfig{{Image: "org/app", IgnoreRebuilds: &yes}}, running, statusUpToDate, 1},
		{"rule disables the global", rebuiltTo("2.3.1"), true, []config.RuleConfig{{Image: "org/app", IgnoreRebuilds: &no}}, running, statusUpdate, 0},
		{"label overrides the rule", rebuiltTo("2.3.1"), false, []config.RuleConfig{{Image: "org/app", IgnoreRebuilds: &yes}},
			map[string]string{ociVersionLabel: "2.3.1", labelIgnoreRebuilds: "false"}, statusUpdate, 0},
		{"label enables", rebuiltTo("2.3.1"), false, nil,
			map[string]string{ociVersionLabel: "2.3.1", labelIgnoreRebuilds: "true"}, statusUpToDate, 1},
		{"invalid label ignored", rebuiltTo("2.3.1"), true, nil,
			map[string]string{ociVersionLabel: "2.3.1", labelIgnoreRebuilds: "maybe"}, statusUpToDate, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newRebuildEngine(t, tc.reg, tc.global, tc.rules)
			res := checkPass(t, eng, digestApp(tc.labels))
			if res.Status != tc.want || len(tc.reg.createdCalls) != tc.calls {
				t.Fatalf("status=%s reason=%q lookups=%v; want %s, %d lookups", res.Status, res.Reason, tc.reg.createdCalls, tc.want, tc.calls)
			}
			if tc.want == statusUpToDate && res.Reason != sameVersionReason {
				t.Fatalf("reason = %q", res.Reason)
			}
		})
	}
}

func TestIgnoreRebuildsCachedAcrossPassesAndReusedByDescribe(t *testing.T) {
	reg := rebuiltTo("2.4.0")
	eng := newRebuildEngine(t, reg, true, nil)
	running := map[string]string{ociVersionLabel: "2.3.1"}
	checkPass(t, eng, digestApp(running))
	res := checkPass(t, eng, digestApp(running)) // next pass: facts cached
	if res.Update == nil || len(reg.createdCalls) != 1 {
		t.Fatalf("result = %+v, lookups %v; want update from cached facts", res, reg.createdCalls)
	}
	// Notification details come from the same facts, no new pull.
	eng.lastPass = registrypass.New(slog.Default())
	batch := []model.UpdateAvailable{*res.Update}
	eng.DescribeUpdates(context.Background(), batch)
	if batch[0].LatestVersion != "2.4.0" || batch[0].Bump != model.BumpMinor || len(reg.createdCalls) != 1 {
		t.Fatalf("described = %+v, lookups %v", batch[0], reg.createdCalls)
	}
}
