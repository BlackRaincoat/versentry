package core

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/state"
)

func TestForceCheckModeDoesNotUseStateStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := state.Load(path, slog.Default())
	s.Record([]model.UpdateAvailable{{
		Host:      "index.docker.io",
		Repo:      "library/nginx",
		LatestTag: "1.27.2",
	}})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	store := state.Load(path, slog.Default())
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	forceCheck := passMode{suppress: false, updateState: false}
	var st *state.Store
	if forceCheck.suppress || forceCheck.updateState {
		st = store
	}
	if st != nil {
		t.Fatal("SIGUSR2-equivalent mode must not touch state store")
	}

	scheduled := passMode{suppress: true, updateState: true}
	st = nil
	if scheduled.suppress || scheduled.updateState {
		st = store
	}
	if st == nil {
		t.Fatal("scheduled mode must use state store")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("force-check path must not modify state file")
	}
}
