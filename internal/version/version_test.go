package version

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestDisplayVersionEmpty(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = ""
	if got := DisplayVersion(); got != "dev" {
		t.Fatalf("DisplayVersion() = %q, want dev", got)
	}
}

func TestShortCommit(t *testing.T) {
	orig := Commit
	t.Cleanup(func() { Commit = orig })

	Commit = ""
	if got := ShortCommit(); got != "unknown" {
		t.Fatalf("empty Commit: got %q, want unknown", got)
	}
	Commit = "unknown"
	if got := ShortCommit(); got != "unknown" {
		t.Fatalf("unknown Commit: got %q", got)
	}
	Commit = "a13c7d95b1e2f3a4b5c6d7e8f901234567890abc"
	if got := ShortCommit(); got != "a13c7d95" {
		t.Fatalf("full sha: got %q, want a13c7d95", got)
	}
	Commit = "abcd"
	if got := ShortCommit(); got != "abcd" {
		t.Fatalf("short: got %q, want abcd", got)
	}
}

func TestLogStartup(t *testing.T) {
	origV, origC := Version, Commit
	t.Cleanup(func() { Version, Commit = origV, origC })
	Version = "1.1.0"
	Commit = "a13c7d95deadbeef"

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	LogStartup(log)
	out := buf.String()
	if !strings.Contains(out, "versentry starting") ||
		!strings.Contains(out, "version=1.1.0") ||
		!strings.Contains(out, "commit=a13c7d95") {
		t.Fatalf("log output missing fields: %q", out)
	}
}
