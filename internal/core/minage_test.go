package core

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/registry"
)

var minAgeNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// agedRegistry adds ImageInfo (registry.ImageInfoReader) to modeTestRegistry.
type agedRegistry struct {
	modeTestRegistry
	created      map[string]time.Time // tag → created; missing = zero (unknown)
	versions     map[string]string    // tag → org.opencontainers.image.version
	createdErr   error
	createdCalls []string
	index        bool     // ImageInfo.Index
	diffIDs      []string // ImageInfo.DiffIDs
}

func (r *agedRegistry) ImageInfo(ctx context.Context, repo, tag string) (registry.ImageInfo, error) {
	r.createdCalls = append(r.createdCalls, tag)
	if r.createdErr != nil {
		return registry.ImageInfo{}, r.createdErr
	}
	return registry.ImageInfo{
		Digest:  strings.TrimPrefix(r.remoteDigest, "sha256:"),
		Created: r.created[tag],
		Version: r.versions[tag],
		Index:   r.index,
		DiffIDs: r.diffIDs,
	}, nil
}

func daysAgo(d float64) time.Time {
	return minAgeNow.Add(-time.Duration(d * float64(24*time.Hour)))
}

func newMinAgeEngine(t *testing.T, reg *agedRegistry, minAge time.Duration, rules []config.RuleConfig, localDigest string) *Engine {
	t.Helper()
	var resolver RuleResolver
	if rules != nil {
		r, err := NewConfigRuleResolver(rules, nil)
		if err != nil {
			t.Fatal(err)
		}
		resolver = r
	}
	eng := NewEngine(&modeTestProvider{localDigest: localDigest}, nil, config.Timeouts{
		Provider: config.Duration{Duration: time.Second},
		Registry: config.Duration{Duration: time.Second},
	}, slog.Default(), resolver, nil)
	eng.registries = append(eng.registries, reg)
	eng.SetMinAge(minAge)
	eng.now = func() time.Time { return minAgeNow }
	return eng
}

func checkOne(t *testing.T, eng *Engine, imageRef string) containerResult {
	t.Helper()
	res, err := eng.checkContainer(context.Background(), model.Container{Name: "app", ImageRef: imageRef}, registrypass.New(slog.Default()))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestMinAgeSemverFallsBackToOlderCandidate(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.0.0", "1.1.0", "1.2.0"}},
		created:          map[string]time.Time{"1.2.0": daysAgo(1), "1.1.0": daysAgo(10)},
	}
	res := checkOne(t, newMinAgeEngine(t, reg, 3*24*time.Hour, nil, ""), "org/app:1.0.0")

	if res.Status != statusUpdate || res.LatestTag != "1.1.0" || res.Update == nil || res.Update.LatestTag != "1.1.0" {
		t.Fatalf("status=%s latest=%q update=%+v, want update to 1.1.0", res.Status, res.LatestTag, res.Update)
	}
	if res.Update.Bump != model.BumpMinor {
		t.Fatalf("bump = %q, want minor (recomputed for the gated tag)", res.Update.Bump)
	}
	if got := strings.Join(reg.createdCalls, ","); got != "1.2.0,1.1.0" {
		t.Fatalf("age lookups = %s, want newest first then older", got)
	}
}

func TestMinAgeAllCandidatesYoungIsPending(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.0.0", "1.1.0", "1.2.0"}},
		created:          map[string]time.Time{"1.2.0": daysAgo(0.5), "1.1.0": daysAgo(2)},
	}
	res := checkOne(t, newMinAgeEngine(t, reg, 3*24*time.Hour, nil, ""), "org/app:1.0.0")

	if res.Status != statusPending || res.Update != nil {
		t.Fatalf("status=%s update=%+v, want pending without update", res.Status, res.Update)
	}
	if res.LatestTag != "1.2.0" || !strings.Contains(res.Reason, "1.2.0 is 12h old, min_age 3d") {
		t.Fatalf("latest=%q reason=%q", res.LatestTag, res.Reason)
	}
	// 12h old of 3d: eligible 2.5 days after the check (2026-10-01 12:00 UTC).
	if !strings.HasSuffix(res.Reason, ", eligible 2026-10-04 00:00") {
		t.Fatalf("reason = %q, want the eligible date", res.Reason)
	}
}

func TestMinAgeDropsAllSpellingsOfYoungVersion(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"8.2.0", "8.2.5", "8.3", "8.3.0"}},
		created:          map[string]time.Time{"8.3.0": daysAgo(1), "8.3": daysAgo(1), "8.2.5": daysAgo(9)},
	}
	res := checkOne(t, newMinAgeEngine(t, reg, 3*24*time.Hour, nil, ""), "org/app:8.2.0")

	if res.LatestTag != "8.2.5" {
		t.Fatalf("latest = %q, want 8.2.5", res.LatestTag)
	}
	if len(reg.createdCalls) != 2 {
		t.Fatalf("age lookups = %v, want 2 (8.3 and 8.3.0 are one version)", reg.createdCalls)
	}
}

func TestMinAgeProbeCap(t *testing.T) {
	tags := []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0", "1.4.0", "1.5.0", "1.6.0", "1.7.0"}
	created := map[string]time.Time{}
	for _, tag := range tags[1:] {
		created[tag] = daysAgo(0.1)
	}
	created["1.1.0"] = daysAgo(30) // old enough, but beyond the probe cap
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: tags},
		created:          created,
	}
	res := checkOne(t, newMinAgeEngine(t, reg, 24*time.Hour, nil, ""), "org/app:1.0.0")

	if res.Status != statusPending || res.LatestTag != "1.7.0" {
		t.Fatalf("status=%s latest=%q, want pending on 1.7.0", res.Status, res.LatestTag)
	}
	if len(reg.createdCalls) != minAgeProbes {
		t.Fatalf("age lookups = %d, want cap %d", len(reg.createdCalls), minAgeProbes)
	}
}

func TestMinAgeUnknownCreatedIsNotDelayed(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.0.0", "1.2.0"}},
		created:          map[string]time.Time{"1.2.0": time.Unix(0, 0)}, // reproducible build
	}
	res := checkOne(t, newMinAgeEngine(t, reg, 3*24*time.Hour, nil, ""), "org/app:1.0.0")
	if res.Status != statusUpdate || res.LatestTag != "1.2.0" {
		t.Fatalf("status=%s latest=%q, want update to 1.2.0", res.Status, res.LatestTag)
	}
}

func TestMinAgeFutureCreated(t *testing.T) {
	cases := []struct {
		name    string
		created time.Time
		want    checkStatus
	}{
		// Build host clock slightly ahead: brand new, still delayed.
		{"small skew is brand new", minAgeNow.Add(2 * time.Hour), statusPending},
		// Bogus far-future timestamp: unknown age, would otherwise pend forever.
		{"far future is unknown", time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), statusUpdate},
		{"just past the skew window", minAgeNow.Add(maxCreatedSkew + time.Minute), statusUpdate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &agedRegistry{
				modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.0.0", "1.2.0"}},
				created:          map[string]time.Time{"1.2.0": tc.created},
			}
			res := checkOne(t, newMinAgeEngine(t, reg, 3*24*time.Hour, nil, ""), "org/app:1.0.0")
			if res.Status != tc.want {
				t.Fatalf("status=%s latest=%q, want %s", res.Status, res.LatestTag, tc.want)
			}
		})
	}
}

func TestMinAgeRuleZeroDisablesForImage(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.0.0", "1.2.0"}},
		created:          map[string]time.Time{"1.2.0": daysAgo(0.1)},
	}
	zero := config.Duration{}
	eng := newMinAgeEngine(t, reg, 3*24*time.Hour, []config.RuleConfig{{Image: "org/app", MinAge: &zero}}, "")
	res := checkOne(t, eng, "org/app:1.0.0")
	if res.Status != statusUpdate || res.LatestTag != "1.2.0" || len(reg.createdCalls) != 0 {
		t.Fatalf("status=%s latest=%q calls=%v, want immediate update and no age lookup", res.Status, res.LatestTag, reg.createdCalls)
	}
}

func TestMinAgeRuleEnablesWithoutGlobal(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.0.0", "1.2.0"}},
		created:          map[string]time.Time{"1.2.0": daysAgo(1)},
	}
	week := config.Duration{Duration: 7 * 24 * time.Hour}
	eng := newMinAgeEngine(t, reg, 0, []config.RuleConfig{{Image: "org/app", MinAge: &week}}, "")
	if res := checkOne(t, eng, "org/app:1.0.0"); res.Status != statusPending {
		t.Fatalf("status = %s, want pending via rule min_age", res.Status)
	}
}

func TestMinAgeOffMakesNoAgeLookups(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.0.0", "1.2.0"}},
		created:          map[string]time.Time{"1.2.0": daysAgo(0.1)},
	}
	res := checkOne(t, newMinAgeEngine(t, reg, 0, nil, ""), "org/app:1.0.0")
	if res.Status != statusUpdate || len(reg.createdCalls) != 0 {
		t.Fatalf("status=%s calls=%v, want update without age lookups", res.Status, reg.createdCalls)
	}
}

func TestMinAgeDigestPendingThenUpdate(t *testing.T) {
	for _, tc := range []struct {
		name string
		age  float64
		want checkStatus
	}{
		{"young rebuild pending", 1, statusPending},
		{"old rebuild notified", 5, statusUpdate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := &agedRegistry{
				modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, remoteDigest: "sha256:remote"},
				created:          map[string]time.Time{"latest": daysAgo(tc.age)},
			}
			res := checkOne(t, newMinAgeEngine(t, reg, 3*24*time.Hour, nil, "sha256:local"), "org/app:latest")
			if res.Status != tc.want {
				t.Fatalf("status = %s, want %s (%s)", res.Status, tc.want, res.Reason)
			}
			if tc.want == statusPending && (res.Update != nil || res.RemoteDigest != "sha256:remote") {
				t.Fatalf("pending digest result = %+v", res)
			}
		})
	}
}

func TestMinAgeDigestUpToDateMakesNoAgeLookup(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, remoteDigest: "sha256:same"},
	}
	res := checkOne(t, newMinAgeEngine(t, reg, 3*24*time.Hour, nil, "sha256:same"), "org/app:latest")
	if res.Status != statusUpToDate || len(reg.createdCalls) != 0 {
		t.Fatalf("status=%s calls=%v", res.Status, reg.createdCalls)
	}
}

func TestMinAgeNumericFallsBack(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"v0.63.1.3", "v0.63.1.4", "v0.63.2.0"}},
		created:          map[string]time.Time{"v0.63.2.0": daysAgo(0.2), "v0.63.1.4": daysAgo(8)},
	}
	res := checkOne(t, newMinAgeEngine(t, reg, 3*24*time.Hour, nil, ""), "metabase/metabase:v0.63.1.3")
	if res.Status != statusUpdate || res.LatestTag != "v0.63.1.4" {
		t.Fatalf("status=%s latest=%q, want numeric update to v0.63.1.4", res.Status, res.LatestTag)
	}
}

func TestMinAgeLookupTimeoutSkipsContainer(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.0.0", "1.2.0"}},
		createdErr:       context.DeadlineExceeded,
	}
	res := checkOne(t, newMinAgeEngine(t, reg, 3*24*time.Hour, nil, ""), "org/app:1.0.0")
	if res.Status != statusSkipped || res.Update != nil || !strings.Contains(res.Reason, "image age") {
		t.Fatalf("status=%s reason=%q, want skipped until next pass", res.Status, res.Reason)
	}
}

func TestFormatAge(t *testing.T) {
	cases := map[time.Duration]string{
		0:                             "0m",
		20 * time.Minute:              "20m",
		5*time.Hour + 20*time.Minute:  "5h20m",
		28 * time.Hour:                "1d4h",
		72 * time.Hour:                "3d",
		72*time.Hour + 10*time.Minute: "3d",
		-time.Hour:                    "0m",
	}
	for d, want := range cases {
		if got := formatAge(d); got != want {
			t.Fatalf("formatAge(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestMinAgeOnlyConfigRuleDoesNotShadowLabels(t *testing.T) {
	week := config.Duration{Duration: 7 * 24 * time.Hour}
	cfgRules, err := NewConfigRuleResolver([]config.RuleConfig{{Image: "valkey/valkey", MinAge: &week}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	chain := NewChainRuleResolver(cfgRules, NewLabelRuleResolver(nil))

	rule := chain.RuleFor(RuleQuery{
		Host:   imageref.DockerHubHost,
		Image:  "valkey/valkey",
		Labels: map[string]string{labelTrack: "digest"},
	})
	if rule == nil || rule.Track != RuleTrackDigest {
		t.Fatalf("rule = %+v, want label track: digest to survive a min_age-only config rule", rule)
	}
	if rule.MinAge == nil || *rule.MinAge != week.Duration {
		t.Fatalf("min_age not carried onto label rule: %+v", rule)
	}

	// No labels: the min_age-only rule itself applies.
	rule = chain.RuleFor(RuleQuery{Host: imageref.DockerHubHost, Image: "valkey/valkey"})
	if rule == nil || rule.MinAge == nil || rule.Include != nil || rule.Track != "" {
		t.Fatalf("rule = %+v, want min_age-only rule", rule)
	}
}
