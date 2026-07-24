package core

import (
	"context"
	"errors"
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

func TestFinishRunSignalCancelIsClean(t *testing.T) {
	app := &App{log: slog.Default()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := app.finishRun(ctx, ctx.Err()); err != nil {
		t.Fatalf("expected nil on canceled ctx, got %v", err)
	}
	if err := app.finishRun(ctx, context.Canceled); err != nil {
		t.Fatalf("expected nil for context.Canceled when ctx canceled, got %v", err)
	}
}

func TestFinishRunPreservesRealErrors(t *testing.T) {
	app := &App{log: slog.Default()}
	ctx := context.Background()
	want := errors.New("load config: boom")

	if err := app.finishRun(ctx, want); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestFinishRunCanceledWithoutParentCancelPassesThrough(t *testing.T) {
	app := &App{log: slog.Default()}
	ctx := context.Background()

	if err := app.finishRun(ctx, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled when parent ctx is still live", err)
	}
}

func TestFinishRunDeadlineExceededNotClean(t *testing.T) {
	app := &App{log: slog.Default()}
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	<-ctx.Done()

	if err := app.finishRun(ctx, ctx.Err()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded", err)
	}
}
