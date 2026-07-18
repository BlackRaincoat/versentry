package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/config"
)

func TestDefaultConfigPathFlag(t *testing.T) {
	root := newRootCmd()
	f := root.PersistentFlags().Lookup("config")
	if f == nil {
		t.Fatal("missing --config flag")
	}
	if f.DefValue != DefaultConfigPath {
		t.Fatalf("DefValue = %q, want %q", f.DefValue, DefaultConfigPath)
	}
	if err := root.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}
	got, err := root.PersistentFlags().GetString("config")
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultConfigPath {
		t.Fatalf("without -c: config = %q, want %q", got, DefaultConfigPath)
	}
}

func TestConfigFlagOverride(t *testing.T) {
	root := newRootCmd()
	custom := "/custom/versentry.yaml"
	if err := root.ParseFlags([]string{"-c", custom}); err != nil {
		t.Fatal(err)
	}
	got, err := root.PersistentFlags().GetString("config")
	if err != nil {
		t.Fatal(err)
	}
	if got != custom {
		t.Fatalf("with -c: config = %q, want %q", got, custom)
	}
}

func TestMissingConfigPathClearError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-config.yaml")
	_, err := config.Load(missing)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	msg := err.Error()
	if !strings.Contains(msg, "read config") {
		t.Fatalf("error should mention read config, got: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) && !strings.Contains(msg, missing) {
		t.Fatalf("error should wrap not-exist or include path, got: %v", err)
	}
}
