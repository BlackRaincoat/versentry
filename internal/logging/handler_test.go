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