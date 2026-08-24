package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestTextHandlerFormat(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(&buf, slog.LevelInfo)

	ts := time.Date(2026, 7, 5, 22, 20, 54, 0, time.FixedZone("MSK", 3*3600))
	record := slog.NewRecord(ts, slog.LevelInfo, "starting periodic checks", 0)
	record.AddAttrs(
		slog.Duration("interval", time.Hour),
		slog.String("schedule", ""),
		slog.String("state_file", "/etc/versentry/state.json"),
	)

	if err := h.Handle(t.Context(), record); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	want := `2026-07-05 22:20:54 INFO starting periodic checks interval=1h0m0s schedule="" state_file=/etc/versentry/state.json`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTextHandlerUnquotedValues(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(&buf, slog.LevelInfo)

	record := slog.NewRecord(time.Now(), slog.LevelWarn, "container skipped", 0)
	record.AddAttrs(
		slog.String("container", "dashy"),
		slog.String("reason", "no registry configured for host registry.example.com"),
	)

	if err := h.Handle(t.Context(), record); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	if strings.Contains(got, `container="dashy"`) {
		t.Fatalf("container should be unquoted: %q", got)
	}
	wantReason := `reason="no registry configured for host registry.example.com"`
	if !strings.Contains(got, wantReason) {
		t.Fatalf("got %q, want substring %q", got, wantReason)
	}
	if strings.Contains(got, `\"`) {
		t.Fatalf("reason should not escape quotes: %q", got)
	}
}

func TestTextHandlerQuotesInvalidUTF8(t *testing.T) {
	var buf bytes.Buffer
	h := NewHandler(&buf, slog.LevelInfo)

	ts := time.Date(2026, 8, 18, 11, 0, 29, 0, time.UTC)
	bad := slog.NewRecord(ts, slog.LevelInfo, "first", 0)
	bad.AddAttrs(slog.String("container", "ok\x80name"))
	if err := h.Handle(t.Context(), bad); err != nil {
		t.Fatal(err)
	}
	next := slog.NewRecord(ts, slog.LevelWarn, "second", 0)
	if err := h.Handle(t.Context(), next); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	if strings.Contains(got, "ok\x80name") {
		t.Fatalf("invalid UTF-8 must be quoted, got %q", got)
	}
	if !strings.Contains(got, `\x80`) {
		t.Fatalf("expected quoted invalid byte, got %q", got)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), got)
	}
	if !strings.HasPrefix(lines[1], "2026-08-18 11:00:29 WARN second") {
		t.Fatalf("next line must start with timestamp, got %q", lines[1])
	}
}
