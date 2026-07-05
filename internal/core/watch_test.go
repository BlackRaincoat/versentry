package core

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/BlackRaincoat/versentry/internal/model"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestShouldMonitorDefaults(t *testing.T) {
	if !ShouldMonitor(nil, nil, "c") {
		t.Fatal("nil labels should monitor")
	}
	if !ShouldMonitor(map[string]string{}, nil, "c") {
		t.Fatal("empty labels should monitor")
	}
}

func TestShouldMonitorExplicitValues(t *testing.T) {
	cases := []struct {
		value  string
		want   bool
		warn   bool
	}{
		{"true", true, false},
		{"false", false, false},
		{"1", true, false},
		{"0", false, false},
		{"t", true, false},
		{"f", false, false},
		{"T", true, false},
		{"F", false, false},
		{"TRUE", true, false},
		{"FALSE", false, false},
		{"yes", true, true},
		{"on", true, true},
		{"", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			got := ShouldMonitor(map[string]string{labelWatch: tc.value}, log, "svc")
			if got != tc.want {
				t.Fatalf("ShouldMonitor(%q) = %v, want %v", tc.value, got, tc.want)
			}

			out := buf.String()
			if tc.warn && !strings.Contains(out, "invalid versentry.watch label value") {
				t.Fatalf("expected WARN for %q, log: %s", tc.value, out)
			}
			if !tc.warn && strings.Contains(out, "invalid versentry.watch label value") {
				t.Fatalf("unexpected WARN for %q, log: %s", tc.value, out)
			}
		})
	}
}

func TestFilterByWatch(t *testing.T) {
	containers := []model.Container{
		{Name: "app", ImageRef: "nginx:latest", Labels: map[string]string{}},
		{Name: "sidecar", ImageRef: "redis:7", Labels: map[string]string{labelWatch: "false"}},
		{Name: "worker", ImageRef: "app:1.0", Labels: map[string]string{labelWatch: "true"}},
	}

	monitored, excluded := filterByWatch(containers, testLogger())
	if excluded != 1 {
		t.Fatalf("excluded = %d, want 1", excluded)
	}
	if len(monitored) != 2 {
		t.Fatalf("monitored = %d, want 2", len(monitored))
	}
	if monitored[0].Name != "app" || monitored[1].Name != "worker" {
		t.Fatalf("monitored names = %q, %q", monitored[0].Name, monitored[1].Name)
	}
}
