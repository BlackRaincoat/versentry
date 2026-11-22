package core

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/state"
)

// checkInPass runs checkContainer with the engine's pass, as RunOnce does,
// and returns the found update.
func checkInPass(t *testing.T, eng *Engine, c model.Container) model.UpdateAvailable {
	t.Helper()
	if eng.lastPass == nil {
		eng.lastPass = registrypass.New(slog.Default())
	}
	res, err := eng.checkContainer(context.Background(), c, eng.lastPass)
	if err != nil {
		t.Fatal(err)
	}
	if res.Update == nil {
		t.Fatalf("no update: %+v", res)
	}
	return *res.Update
}

func digestApp(labels map[string]string) model.Container {
	return model.Container{Name: "app", ImageRef: "org/app:latest", Labels: labels}
}

func TestDescribeDigestUpdate(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, remoteDigest: "sha256:remote"},
		created:          map[string]time.Time{"latest": daysAgo(3)},
		versions:         map[string]string{"latest": "2.4.0"},
	}
	eng := newMinAgeEngine(t, reg, 0, nil, "sha256:local")
	u := checkInPass(t, eng, digestApp(map[string]string{ociVersionLabel: "2.3.1"}))
	if len(reg.createdCalls) != 0 {
		t.Fatalf("detection must not look up image info without min_age: %v", reg.createdCalls)
	}

	batch := []model.UpdateAvailable{u}
	eng.DescribeUpdates(context.Background(), batch)
	got := batch[0]
	if got.CurrentVersion != "2.3.1" || got.LatestVersion != "2.4.0" || !got.LatestCreated.Equal(daysAgo(3)) {
		t.Fatalf("described = %+v", got)
	}
	if len(reg.createdCalls) != 1 {
		t.Fatalf("lookups = %v, want 1", reg.createdCalls)
	}

	// A second notifier's batch with the same update: served from the pass cache.
	again := []model.UpdateAvailable{u}
	eng.DescribeUpdates(context.Background(), again)
	if again[0].LatestVersion != "2.4.0" || len(reg.createdCalls) != 1 {
		t.Fatalf("second batch = %+v, lookups %v", again[0], reg.createdCalls)
	}
}

func TestDescribeReusesMinAgeLookup(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, remoteDigest: "sha256:remote"},
		created:          map[string]time.Time{"latest": daysAgo(5)},
		versions:         map[string]string{"latest": "2.4.0"},
	}
	eng := newMinAgeEngine(t, reg, 3*24*time.Hour, nil, "sha256:local")
	u := checkInPass(t, eng, digestApp(nil))
	batch := []model.UpdateAvailable{u}
	eng.DescribeUpdates(context.Background(), batch)
	if batch[0].LatestVersion != "2.4.0" || len(reg.createdCalls) != 1 {
		t.Fatalf("described = %+v, lookups %v; want min_age lookup reused", batch[0], reg.createdCalls)
	}
}

func TestDescribeSkipsWhenTagMovedAgain(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, remoteDigest: "sha256:remote"},
		created:          map[string]time.Time{"latest": daysAgo(1)},
		versions:         map[string]string{"latest": "2.5.0"},
	}
	eng := newMinAgeEngine(t, reg, 0, nil, "sha256:local")
	u := checkInPass(t, eng, digestApp(nil))
	reg.remoteDigest = "sha256:newer" // ImageInfo now reports a different digest
	batch := []model.UpdateAvailable{u}
	eng.DescribeUpdates(context.Background(), batch)
	if batch[0].LatestVersion != "" || !batch[0].LatestCreated.IsZero() {
		t.Fatalf("details of another image attached: %+v", batch[0])
	}
}

func TestDescribeLookupCap(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, remoteDigest: "sha256:remote"},
		created:          map[string]time.Time{},
		versions:         map[string]string{},
	}
	eng := newMinAgeEngine(t, reg, 0, nil, "sha256:local")
	eng.lastPass = registrypass.New(slog.Default())
	var batch []model.UpdateAvailable
	for i := 0; i < maxDescribeLookups+3; i++ {
		tag := "t" + string(rune('a'+i))
		reg.versions[tag] = "1.0." + string(rune('0'+i))
		batch = append(batch, model.UpdateAvailable{
			Container: model.Container{Name: tag}, Host: imageref.DockerHubHost, Repo: "org/app",
			CurrentTag: tag, RemoteDigest: "sha256:remote",
		})
	}
	eng.DescribeUpdates(context.Background(), batch)
	if len(reg.createdCalls) != maxDescribeLookups {
		t.Fatalf("lookups = %d, want cap %d", len(reg.createdCalls), maxDescribeLookups)
	}
	if batch[len(batch)-1].LatestVersion != "" {
		t.Fatal("update beyond the cap must keep plain digests")
	}
}

func TestDescribeSemverUsesCacheOnly(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.0.0", "1.2.0"}},
		created:          map[string]time.Time{"1.2.0": daysAgo(4)},
	}
	// Without min_age: no lookup, no build time.
	eng := newMinAgeEngine(t, reg, 0, nil, "")
	u := checkInPass(t, eng, model.Container{Name: "app", ImageRef: "org/app:1.0.0"})
	batch := []model.UpdateAvailable{u}
	eng.DescribeUpdates(context.Background(), batch)
	if len(reg.createdCalls) != 0 || !batch[0].LatestCreated.IsZero() {
		t.Fatalf("semver must not trigger a lookup: %+v, %v", batch[0], reg.createdCalls)
	}

	// With min_age the age is already known: shown for free.
	eng = newMinAgeEngine(t, reg, 3*24*time.Hour, nil, "")
	u = checkInPass(t, eng, model.Container{Name: "app", ImageRef: "org/app:1.0.0"})
	batch = []model.UpdateAvailable{u}
	eng.DescribeUpdates(context.Background(), batch)
	if !batch[0].LatestCreated.Equal(daysAgo(4)) || len(reg.createdCalls) != 1 {
		t.Fatalf("semver build time = %v, lookups %v", batch[0].LatestCreated, reg.createdCalls)
	}
}

func TestCleanVersion(t *testing.T) {
	for in, want := range map[string]string{
		"2.4.0":                  "2.4.0",
		"4.0.14.2939-ls123":      "4.0.14.2939-ls123",
		"v1.2.3+build.5":         "v1.2.3+build.5",
		"":                       "",
		"1.0 <b>x</b>":           "",
		"[x](https://evil.test)": "",
		"line\nbreak":            "",
		"-leading-dash":          "",
	} {
		if got := cleanVersion(in); got != want {
			t.Errorf("cleanVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunPassDescribesOnlySentUpdates(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, remoteDigest: "sha256:remote"},
		created:          map[string]time.Time{"latest": time.Now().Add(-72 * time.Hour)},
		versions:         map[string]string{"latest": "2.4.0"},
	}
	prov := &fleetProvider{
		modeTestProvider: modeTestProvider{localDigest: "sha256:local"},
		containers: []model.Container{{Name: "app", ImageRef: "org/app:latest",
			Labels: map[string]string{ociVersionLabel: "2.3.1"}}},
	}
	eng := NewEngine(prov, nil, config.Timeouts{
		Provider: config.Duration{Duration: time.Second},
		Registry: config.Duration{Duration: time.Second},
	}, log, nil, nil)
	eng.registries = append(eng.registries, reg)
	rec := &recordingNotifier{}
	app := &App{engine: eng, log: log, notifiers: []notifierSlot{{typ: "rec", n: rec}}}
	store := state.Load(statePath, log)
	scheduled := passMode{suppress: true, updateState: true}

	if err := app.runPass(context.Background(), store, scheduled); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 || rec.got[0][0].LatestVersion != "2.4.0" || rec.got[0][0].CurrentVersion != "2.3.1" {
		t.Fatalf("sent = %+v", rec.got)
	}
	if len(reg.createdCalls) != 1 {
		t.Fatalf("lookups = %v, want 1", reg.createdCalls)
	}

	// Next pass: same update is suppressed by state — no registry lookup.
	if err := app.runPass(context.Background(), store, scheduled); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 || len(reg.createdCalls) != 1 {
		t.Fatalf("suppressed update was described: batches=%d lookups=%v", len(rec.got), reg.createdCalls)
	}
}

func TestVersionBump(t *testing.T) {
	cases := []struct{ cur, latest, want string }{
		{"2.3.1", "2.3.2", model.BumpPatch},
		{"2.3.1", "2.4.0", model.BumpMinor},
		{"2.3.1", "3.0.0", model.BumpMajor},
		{"v1.11.0", "v1.12.0", model.BumpMinor},
		{"1.27", "1.28", model.BumpMinor},
		{"2.3.1", "2.3.1", ""},   // rebuilt
		{"2.4.0", "2.3.1", ""},   // downgrade
		{"", "2.4.0", ""},        // running image unlabeled
		{"2.3.1", "", ""},        // new image unlabeled
		{"nightly", "2.4.0", ""}, // not semver
		{"2024.10.1", "2025.1.0", model.BumpMajor},
	}
	for _, tc := range cases {
		if got := versionBump(tc.cur, tc.latest); got != tc.want {
			t.Errorf("versionBump(%q, %q) = %q, want %q", tc.cur, tc.latest, got, tc.want)
		}
	}
}

func TestDescribeDigestUpdateSetsBump(t *testing.T) {
	reg := &agedRegistry{
		modeTestRegistry: modeTestRegistry{host: imageref.DockerHubHost, remoteDigest: "sha256:remote"},
		versions:         map[string]string{"latest": "3.0.0"},
	}
	eng := newMinAgeEngine(t, reg, 0, nil, "sha256:local")
	batch := []model.UpdateAvailable{checkInPass(t, eng, digestApp(map[string]string{ociVersionLabel: "2.9.1"}))}
	eng.DescribeUpdates(context.Background(), batch)
	if batch[0].Bump != model.BumpMajor {
		t.Fatalf("bump = %q, want major", batch[0].Bump)
	}
}
