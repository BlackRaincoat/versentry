package state

import (
	"log/slog"
	"os"
	"path/filepath"
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

func TestFilterNotifiesOnTrackingModeChange(t *testing.T) {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Images: map[string]ImageState{
				"index.docker.io/chatwoot/chatwoot": {
					Mode:  "digest",
					Value: "abc123deadbeef",
				},
			},
		},
	}

	event := model.UpdateAvailable{
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
	stored := s.data.Images["index.docker.io/chatwoot/chatwoot"]
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
			Images: map[string]ImageState{
				"ghcr.io/org/app": {
					Mode:  "digest",
					Value: "3.24.2",
				},
			},
		},
	}

	event := model.UpdateAvailable{
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
			Images: map[string]ImageState{
				"index.docker.io/library/nginx": {
					Mode:  "semver",
					Value: "1.27.2",
				},
			},
		},
	}

	event := model.UpdateAvailable{
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

func TestFilterNotifiesNewTarget(t *testing.T) {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Images: map[string]ImageState{
				"index.docker.io/library/nginx": {
					Mode:  "semver",
					Value: "1.27.2",
				},
			},
		},
	}

	event := model.UpdateAvailable{
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

func TestPruneRemovesStaleImages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := Load(path, slog.Default())
	s.data.Images["index.docker.io/library/nginx"] = ImageState{Mode: "semver", Value: "1.27.2"}
	s.data.Images["ghcr.io/org/app"] = ImageState{Mode: "semver", Value: "2.0.0"}

	if err := s.AfterPass([]string{"index.docker.io/library/nginx"}, nil, true); err != nil {
		t.Fatalf("AfterPass: %v", err)
	}

	reloaded := Load(path, slog.Default())
	if _, ok := reloaded.data.Images["index.docker.io/library/nginx"]; !ok {
		t.Fatal("expected nginx entry to remain")
	}
	if _, ok := reloaded.data.Images["ghcr.io/org/app"]; ok {
		t.Fatal("expected stale ghcr entry to be pruned")
	}
}

func TestAfterPassSkipsPruneWhenCannotPrune(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := Load(path, slog.Default())
	s.data.Images["index.docker.io/library/nginx"] = ImageState{Mode: "semver", Value: "1.27.2"}
	s.data.Images["ghcr.io/org/app"] = ImageState{Mode: "semver", Value: "2.0.0"}

	if err := s.AfterPass(nil, nil, false); err != nil {
		t.Fatalf("AfterPass: %v", err)
	}

	reloaded := Load(path, slog.Default())
	if len(reloaded.data.Images) != 2 {
		t.Fatalf("expected both entries preserved, got %d", len(reloaded.data.Images))
	}
}

func TestPruneKeepsImageWhenKeyStillActive(t *testing.T) {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Images: map[string]ImageState{
				"index.docker.io/chatwoot/chatwoot": {Mode: "semver", Value: "v3.24.2-ce"},
			},
		},
	}

	s.Prune([]string{"index.docker.io/chatwoot/chatwoot"})
	if len(s.data.Images) != 1 {
		t.Fatalf("expected entry to remain, got %d images", len(s.data.Images))
	}
}

func TestPruneRemovesImageWhenNoActiveKey(t *testing.T) {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Images: map[string]ImageState{
				"index.docker.io/chatwoot/chatwoot": {Mode: "semver", Value: "v3.24.2-ce"},
			},
		},
	}

	s.Prune(nil)
	if len(s.data.Images) != 0 {
		t.Fatalf("expected entry pruned, got %d images", len(s.data.Images))
	}
}

func TestAfterPassPrunesExcludedFleet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := Load(path, slog.Default())
	s.data.Images["index.docker.io/library/nginx"] = ImageState{Mode: "semver", Value: "1.27.2"}

	if err := s.AfterPass(nil, nil, true); err != nil {
		t.Fatalf("AfterPass: %v", err)
	}

	reloaded := Load(path, slog.Default())
	if len(reloaded.data.Images) != 0 {
		t.Fatalf("expected prune when fleet exists but active keys empty, got %d", len(reloaded.data.Images))
	}
}

func TestLoadCorruptJSONStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := Load(path, slog.Default())
	if len(s.data.Images) != 0 {
		t.Fatalf("expected empty state, got %d entries", len(s.data.Images))
	}
}

func TestSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "state.json")

	s := Load(path, slog.Default())
	s.Record([]model.UpdateAvailable{{
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
			Images: map[string]ImageState{
				"index.docker.io/crazymax/diun": {
					Mode:  "digest",
					Value: "abc123",
				},
			},
		},
	}

	same := model.UpdateAvailable{
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
