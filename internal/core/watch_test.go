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
	c := model.Container{Name: "c"}
	if !ShouldMonitor(c, nil, nil) {
		t.Fatal("nil labels should monitor")
	}
	c.Labels = map[string]string{}
	if !ShouldMonitor(c, nil, nil) {
		t.Fatal("empty labels should monitor")
	}
}

func TestShouldMonitorExplicitLabelValues(t *testing.T) {
	cases := []struct {
		value string
		want  bool
		warn  bool
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

			c := model.Container{Name: "svc", Labels: map[string]string{labelWatch: tc.value}}
			got := ShouldMonitor(c, nil, log)
			if got != tc.want {
				t.Fatalf("ShouldMonitor(label=%q) = %v, want %v", tc.value, got, tc.want)
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

func TestShouldMonitorExcludeListORLabel(t *testing.T) {
	exclude := map[string]struct{}{"chatwoot-notify": {}}

	listed := model.Container{Name: "chatwoot-notify", Labels: map[string]string{}}
	if ShouldMonitor(listed, exclude, nil) {
		t.Fatal("name in exclude_containers must be excluded")
	}

	byLabel := model.Container{
		Name:   "other",
		Labels: map[string]string{labelWatch: "false"},
	}
	if ShouldMonitor(byLabel, exclude, nil) {
		t.Fatal("label watch=false must still exclude")
	}

	// Label says true, but name is excluded → still excluded (OR of opt-outs)
	listedWithTrue := model.Container{
		Name:   "chatwoot-notify",
		Labels: map[string]string{labelWatch: "true"},
	}
	if ShouldMonitor(listedWithTrue, exclude, nil) {
		t.Fatal("exclude_containers must exclude even when label is true")
	}

	ok := model.Container{Name: "app", Labels: map[string]string{}}
	if !ShouldMonitor(ok, exclude, nil) {
		t.Fatal("name not excluded and no label must be monitored")
	}
}

func TestFilterByWatchLabelOnly(t *testing.T) {
	containers := []model.Container{
		{Name: "app", ImageRef: "nginx:latest", Labels: map[string]string{}},
		{Name: "sidecar", ImageRef: "redis:7", Labels: map[string]string{labelWatch: "false"}},
		{Name: "worker", ImageRef: "app:1.0", Labels: map[string]string{labelWatch: "true"}},
	}

	monitored, excluded := filterByWatch(containers, nil, testLogger())
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

func TestFilterByWatchExcludeList(t *testing.T) {
	containers := []model.Container{
		{Name: "chatwoot-notify", ImageRef: "postgres:16", Labels: map[string]string{}},
		{Name: "app", ImageRef: "nginx:latest", Labels: map[string]string{}},
	}
	exclude := map[string]struct{}{"chatwoot-notify": {}}
	monitored, excluded := filterByWatch(containers, exclude, testLogger())
	if excluded != 1 || len(monitored) != 1 || monitored[0].Name != "app" {
		t.Fatalf("monitored=%v excluded=%d", monitored, excluded)
	}
}

func TestWarnMissingExcludeContainers(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fleet := []model.Container{{Name: "app"}}
	warnMissingExcludeContainers(fleet, map[string]struct{}{
		"gone": {},
		"app":  {},
	}, log)
	out := buf.String()
	if !strings.Contains(out, "exclude_containers name not found") || !strings.Contains(out, "gone") {
		t.Fatalf("expected WARN for gone, got: %s", out)
	}
	if strings.Count(out, "exclude_containers name not found") != 1 {
		t.Fatalf("should WARN only for missing name, log: %s", out)
	}
}
