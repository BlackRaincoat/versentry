package core

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
)

func TestWatchPolicyOptIn(t *testing.T) {
	exclude := map[string]struct{}{"blocked": {}, "listed-and-excluded": {}}
	watch := map[string]struct{}{"listed": {}, "listed-label-false": {}}
	label := func(v string) map[string]string { return map[string]string{labelWatch: v} }
	cases := []struct {
		name   string
		c      model.Container
		optIn  bool // watch_by_default: false (+ watch_containers)
		optOut bool // watch_by_default: true (unchanged behavior)
	}{
		{"no label", model.Container{Name: "a"}, false, true},
		{"watch=true", model.Container{Name: "a", Labels: label("true")}, true, true},
		{"watch=false", model.Container{Name: "a", Labels: label("false")}, false, false},
		{"invalid label falls back to default", model.Container{Name: "a", Labels: label("maybe")}, false, true},
		{"exclude beats watch=true", model.Container{Name: "blocked", Labels: label("true")}, false, false},
		{"in watch_containers", model.Container{Name: "listed"}, true, true},
		{"label false beats watch_containers", model.Container{Name: "listed-label-false", Labels: label("false")}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := watchPolicy{exclude: exclude, watch: watch, watchByDefault: false}
			if got := in.monitors(tc.c, nil); got != tc.optIn {
				t.Fatalf("opt-in: got %v, want %v", got, tc.optIn)
			}
			out := watchPolicy{exclude: exclude, watchByDefault: true}
			if got := out.monitors(tc.c, nil); got != tc.optOut {
				t.Fatalf("opt-out: got %v, want %v", got, tc.optOut)
			}
		})
	}
}

func TestFilterByWatchPolicyOptIn(t *testing.T) {
	fleet := []model.Container{
		{Name: "db", Labels: map[string]string{labelWatch: "true"}},
		{Name: "cache"},
		{Name: "web"},
		{Name: "tmp", Labels: map[string]string{labelWatch: "false"}},
	}
	policy := watchPolicy{watch: map[string]struct{}{"cache": {}}, watchByDefault: false}
	monitored, excluded := filterByWatchPolicy(fleet, policy, nil)
	if len(monitored) != 2 || monitored[0].Name != "db" || monitored[1].Name != "cache" || excluded != 2 {
		t.Fatalf("monitored=%v excluded=%d, want db (label) and cache (watch_containers)", monitored, excluded)
	}
}

func TestFilterByWatchPolicyOptInNothingSelectedWarns(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	monitored, _ := filterByWatchPolicy([]model.Container{{Name: "web"}}, watchPolicy{watchByDefault: false}, log)
	if len(monitored) != 0 || !strings.Contains(buf.String(), "nothing to check") {
		t.Fatalf("monitored=%v log=%q", monitored, buf.String())
	}

	buf.Reset()
	filterByWatchPolicy([]model.Container{{Name: "web", Labels: map[string]string{labelWatch: "false"}}}, watchPolicy{watchByDefault: true}, log)
	if strings.Contains(buf.String(), "nothing to check") {
		t.Fatalf("opt-out must not warn: %q", buf.String())
	}
}

func TestRunOnceOptInChecksOnlyLabeled(t *testing.T) {
	var logBuf bytes.Buffer
	reg := &modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.0.0", "1.1.0"}}
	eng := NewEngine(&fleetProvider{containers: []model.Container{
		{Name: "watched", ImageRef: "org/a:1.0.0", Labels: map[string]string{labelWatch: "true"}},
		{Name: "ignored", ImageRef: "org/b:1.0.0"},
		{Name: "listed", ImageRef: "org/c:1.0.0"},
	}}, nil, config.Timeouts{
		Provider: config.Duration{Duration: time.Second},
		Registry: config.Duration{Duration: time.Second},
	}, slog.New(slog.NewTextHandler(&logBuf, nil)), nil, nil)
	eng.registries = append(eng.registries, reg)
	eng.SetWatchByDefault(false)
	eng.SetWatchContainers(map[string]struct{}{"listed": {}, "gone": {}})

	updates, activeKeys, _, err := eng.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 || updates[0].Container.Name != "watched" || updates[1].Container.Name != "listed" {
		t.Fatalf("updates = %+v, want the labeled and the listed container", updates)
	}
	if len(activeKeys) != 2 {
		t.Fatalf("activeKeys = %v (unwatched containers are not active for state)", activeKeys)
	}
	if !strings.Contains(logBuf.String(), "watch_containers name not found among running containers") || !strings.Contains(logBuf.String(), "name=gone") {
		t.Fatalf("missing WARN for absent watch_containers name: %s", logBuf.String())
	}
}
