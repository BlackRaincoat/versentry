package state

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
)

func TestMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if !MissingFile(path) {
		t.Fatal("expected missing before first save")
	}

	s := Load(path, slog.Default())
	s.Record([]model.UpdateAvailable{{
		Container: model.Container{Name: "web"},
		Host:      "index.docker.io",
		Repo:      "library/nginx",
		LatestTag: "1.27.2",
	}})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if MissingFile(path) {
		t.Fatal("expected file to exist after save")
	}
}

func TestEntryKeyUsesContainerAndImage(t *testing.T) {
	u := model.UpdateAvailable{
		Container: model.Container{Name: "umami_db"},
		Host:      "index.docker.io",
		Repo:      "library/postgres",
	}
	got := EntryKey(u)
	want := "umami_db|index.docker.io/library/postgres"
	if got != want {
		t.Fatalf("EntryKey = %q, want %q", got, want)
	}
}

func TestEntryKeyFallsBackToShortID(t *testing.T) {
	got := FormatEntryKey("", "abcdef0123456789deadbeef", "index.docker.io", "library/nginx")
	want := "abcdef012345|index.docker.io/library/nginx"
	if got != want {
		t.Fatalf("FormatEntryKey = %q, want %q", got, want)
	}

	got = FormatEntryKey("", "", "ghcr.io", "org/app")
	want = "unknown|ghcr.io/org/app"
	if got != want {
		t.Fatalf("FormatEntryKey empty id = %q, want %q", got, want)
	}
}

func TestLoadResetsV1State(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	v1 := map[string]any{
		"version": 1,
		"images": map[string]any{
			"index.docker.io/library/nginx": map[string]any{
				"mode":  "semver",
				"value": "1.27.2",
			},
		},
	}
	raw, err := json.MarshalIndent(v1, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	var warnBuf strings.Builder
	log := slog.New(slog.NewTextHandler(&warnBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s := Load(path, log)
	if s.EntryCount() != 0 {
		t.Fatalf("expected empty store after v1 reset, got %d", s.EntryCount())
	}
	if !strings.Contains(warnBuf.String(), "state format changed (v1→v2)") {
		t.Fatalf("expected v1→v2 WARN, got %q", warnBuf.String())
	}

	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded := Load(path, slog.Default())
	if reloaded.data.Version != fileVersion {
		t.Fatalf("saved version = %d, want %d", reloaded.data.Version, fileVersion)
	}
	raw2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw2), `"images"`) {
		t.Fatal("v2 state must use entries, not images")
	}
	if !strings.Contains(string(raw2), `"entries"`) {
		t.Fatal("expected entries field in saved state")
	}
}

func TestFilterNotifiesOnTrackingModeChange(t *testing.T) {
	key := "chatwoot|index.docker.io/chatwoot/chatwoot"
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Entries: map[string]EntryState{
				key: {
					Mode:  "digest",
					Value: "abc123deadbeef",
				},
			},
		},
	}

	event := model.UpdateAvailable{
		Container:  model.Container{Name: "chatwoot"},
		Host:       "index.docker.io",
		Repo:       "chatwoot/chatwoot",
		CurrentTag: "v3.24.1-ce",
		LatestTag:  "v3.24.2-ce",
	}

	toNotify, suppressed := s.Filter([]model.UpdateAvailable{event})
	if len(toNotify) != 1 {
		t.Fatalf("expected notify on digest→semver mode change, got notify=%d suppressed=%d", len(toNotify), len(suppressed))
	}
	if len(suppressed) != 0 {
		t.Fatalf("expected 0 suppressed, got %d", len(suppressed))
	}

	s.Record(toNotify)
	stored := s.data.Entries[key]
	if stored.Mode != "semver" || stored.Value != "v3.24.2-ce" {
		t.Fatalf("state after record: mode=%q value=%q, want semver/v3.24.2-ce", stored.Mode, stored.Value)
	}

	toNotify, suppressed = s.Filter([]model.UpdateAvailable{event})
	if len(toNotify) != 0 || len(suppressed) != 1 {
		t.Fatalf("after record: expected suppressed, got notify=%d suppressed=%d", len(toNotify), len(suppressed))
	}
}

func TestFilterModeChangeIgnoresStoredValue(t *testing.T) {
	// Stored digest value must not suppress a semver target when values collide.
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Entries: map[string]EntryState{
				"app|ghcr.io/org/app": {
					Mode:  "digest",
					Value: "3.24.2",
				},
			},
		},
	}

	event := model.UpdateAvailable{
		Container:  model.Container{Name: "app"},
		Host:       "ghcr.io",
		Repo:       "org/app",
		CurrentTag: "3.24.1",
		LatestTag:  "3.24.2",
	}

	toNotify, _ := s.Filter([]model.UpdateAvailable{event})
	if len(toNotify) != 1 {
		t.Fatal("expected notify when mode differs even if stored digest value equals semver tag")
	}
}

func TestFilterSuppressesKnownTarget(t *testing.T) {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Entries: map[string]EntryState{
				"web|index.docker.io/library/nginx": {
					Mode:  "semver",
					Value: "1.27.2",
				},
			},
		},
	}

	event := model.UpdateAvailable{
		Container:  model.Container{Name: "web"},
		Host:       "index.docker.io",
		Repo:       "library/nginx",
		CurrentTag: "1.27.1",
		LatestTag:  "1.27.2",
	}

	toNotify, suppressed := s.Filter([]model.UpdateAvailable{event})
	if len(toNotify) != 0 {
		t.Fatalf("expected 0 to notify, got %d", len(toNotify))
	}
	if len(suppressed) != 1 {
		t.Fatalf("expected 1 suppressed, got %d", len(suppressed))
	}
}

func TestFilterSameImageDifferentContainersIndependent(t *testing.T) {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Entries: map[string]EntryState{
				"umami_db|index.docker.io/library/postgres": {
					Mode:  "semver",
					Value: "16.4-alpine",
				},
			},
		},
	}

	other := model.UpdateAvailable{
		Container:  model.Container{Name: "remnawave-db"},
		Host:       "index.docker.io",
		Repo:       "library/postgres",
		CurrentTag: "17.1",
		LatestTag:  "17.2",
	}
	toNotify, suppressed := s.Filter([]model.UpdateAvailable{other})
	if len(toNotify) != 1 || len(suppressed) != 0 {
		t.Fatalf("expected independent notify for second container, notify=%d suppressed=%d", len(toNotify), len(suppressed))
	}

	same := model.UpdateAvailable{
		Container:  model.Container{Name: "umami_db"},
		Host:       "index.docker.io",
		Repo:       "library/postgres",
		CurrentTag: "16.3-alpine",
		LatestTag:  "16.4-alpine",
	}
	toNotify, suppressed = s.Filter([]model.UpdateAvailable{same})
	if len(toNotify) != 0 || len(suppressed) != 1 {
		t.Fatalf("expected first container suppressed, notify=%d suppressed=%d", len(toNotify), len(suppressed))
	}
}

func TestFilterNotifiesNewTarget(t *testing.T) {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Entries: map[string]EntryState{
				"web|index.docker.io/library/nginx": {
					Mode:  "semver",
					Value: "1.27.2",
				},
			},
		},
	}

	event := model.UpdateAvailable{
		Container:  model.Container{Name: "web"},
		Host:       "index.docker.io",
		Repo:       "library/nginx",
		CurrentTag: "1.27.2",
		LatestTag:  "1.27.3",
	}

	toNotify, suppressed := s.Filter([]model.UpdateAvailable{event})
	if len(toNotify) != 1 {
		t.Fatalf("expected 1 to notify, got %d", len(toNotify))
	}
	if len(suppressed) != 0 {
		t.Fatalf("expected 0 suppressed, got %d", len(suppressed))
	}
}

func TestPruneRemovesStaleEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := Load(path, slog.Default())
	s.data.Entries["web|index.docker.io/library/nginx"] = EntryState{Mode: "semver", Value: "1.27.2"}
	s.data.Entries["app|ghcr.io/org/app"] = EntryState{Mode: "semver", Value: "2.0.0"}

	if err := s.AfterPass([]string{"web|index.docker.io/library/nginx"}, nil, true); err != nil {
		t.Fatalf("AfterPass: %v", err)
	}

	reloaded := Load(path, slog.Default())
	if _, ok := reloaded.data.Entries["web|index.docker.io/library/nginx"]; !ok {
		t.Fatal("expected nginx entry to remain")
	}
	if _, ok := reloaded.data.Entries["app|ghcr.io/org/app"]; ok {
		t.Fatal("expected stale ghcr entry to be pruned")
	}
}

func TestAfterPassSkipsPruneWhenCannotPrune(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := Load(path, slog.Default())
	s.data.Entries["web|index.docker.io/library/nginx"] = EntryState{Mode: "semver", Value: "1.27.2"}
	s.data.Entries["app|ghcr.io/org/app"] = EntryState{Mode: "semver", Value: "2.0.0"}

	if err := s.AfterPass(nil, nil, false); err != nil {
		t.Fatalf("AfterPass: %v", err)
	}

	reloaded := Load(path, slog.Default())
	if len(reloaded.data.Entries) != 2 {
		t.Fatalf("expected both entries preserved, got %d", len(reloaded.data.Entries))
	}
}

func TestPruneKeepsEntryWhenKeyStillActive(t *testing.T) {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Entries: map[string]EntryState{
				"chatwoot|index.docker.io/chatwoot/chatwoot": {Mode: "semver", Value: "v3.24.2-ce"},
			},
		},
	}

	s.Prune([]string{"chatwoot|index.docker.io/chatwoot/chatwoot"})
	if len(s.data.Entries) != 1 {
		t.Fatalf("expected entry to remain, got %d entries", len(s.data.Entries))
	}
}

func TestPruneRemovesEntryWhenNoActiveKey(t *testing.T) {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Entries: map[string]EntryState{
				"chatwoot|index.docker.io/chatwoot/chatwoot": {Mode: "semver", Value: "v3.24.2-ce"},
			},
		},
	}

	s.Prune(nil)
	if len(s.data.Entries) != 0 {
		t.Fatalf("expected entry pruned, got %d entries", len(s.data.Entries))
	}
}

func TestAfterPassPrunesExcludedFleet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := Load(path, slog.Default())
	s.data.Entries["web|index.docker.io/library/nginx"] = EntryState{Mode: "semver", Value: "1.27.2"}

	if err := s.AfterPass(nil, nil, true); err != nil {
		t.Fatalf("AfterPass: %v", err)
	}

	reloaded := Load(path, slog.Default())
	if len(reloaded.data.Entries) != 0 {
		t.Fatalf("expected prune when fleet exists but active keys empty, got %d", len(reloaded.data.Entries))
	}
}

func TestLoadCorruptJSONStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := Load(path, slog.Default())
	if len(s.data.Entries) != 0 {
		t.Fatalf("expected empty state, got %d entries", len(s.data.Entries))
	}
}

func TestSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "state.json")

	s := Load(path, slog.Default())
	s.Record([]model.UpdateAvailable{{
		Container: model.Container{Name: "web"},
		Host:      "index.docker.io",
		Repo:      "library/nginx",
		LatestTag: "1.27.2",
		CheckedAt: time.Now().UTC(),
	}})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file missing: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file should not remain after save")
	}
}

func TestDigestModeState(t *testing.T) {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Entries: map[string]EntryState{
				"diun|index.docker.io/crazymax/diun": {
					Mode:  "digest",
					Value: "abc123",
				},
			},
		},
	}

	same := model.UpdateAvailable{
		Container:    model.Container{Name: "diun"},
		Host:         "index.docker.io",
		Repo:         "crazymax/diun",
		CurrentTag:   "latest",
		RemoteDigest: "sha256:abc123",
	}
	toNotify, _ := s.Filter([]model.UpdateAvailable{same})
	if len(toNotify) != 0 {
		t.Fatal("expected digest match to be suppressed")
	}

	changed := model.UpdateAvailable{
		Container:    model.Container{Name: "diun"},
		Host:         "index.docker.io",
		Repo:         "crazymax/diun",
		CurrentTag:   "latest",
		RemoteDigest: "sha256:def456",
	}
	toNotify, _ = s.Filter([]model.UpdateAvailable{changed})
	if len(toNotify) != 1 {
		t.Fatal("expected changed digest to notify")
	}
}
