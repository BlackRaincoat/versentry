package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/heartbeat"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/state"
)

type fleetProvider struct {
	modeTestProvider
	containers []model.Container
}

func (p *fleetProvider) ListRunning(ctx context.Context) ([]model.Container, error) {
	return p.containers, nil
}

func TestRunPassSendsReminderOnceDue(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	// Notified 8 days ago about nginx 1.27.2; still running 1.27.1.
	old := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(statePath, []byte(`{"version":2,"entries":{"web|index.docker.io/library/nginx":{"mode":"semver","value":"1.27.2","notified_at":"`+old+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	reg := &modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.27.1", "1.27.2"}}
	eng := NewEngine(&fleetProvider{containers: []model.Container{{Name: "web", ImageRef: "nginx:1.27.1"}}}, nil, config.Timeouts{
		Provider: config.Duration{Duration: time.Second},
		Registry: config.Duration{Duration: time.Second},
	}, log, nil, nil)
	eng.registries = append(eng.registries, reg)
	rec := &recordingNotifier{}
	app := &App{engine: eng, notifiers: []notifierSlot{{typ: "rec", n: rec}}, log: log}

	scheduled := passMode{suppress: true, updateState: true}

	// remind_after off: the 8-day-old target stays suppressed (old behavior).
	if err := app.runPass(context.Background(), state.Load(statePath, log), scheduled); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 0 {
		t.Fatalf("remind_after off must not re-notify: %+v", rec.got)
	}

	store := state.Load(statePath, log)
	store.SetRemindAfter(7 * 24 * time.Hour)

	if err := app.runPass(context.Background(), store, scheduled); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 || len(rec.got[0]) != 1 || !rec.got[0][0].Reminder {
		t.Fatalf("got %+v, want one reminder", rec.got)
	}
	if !strings.Contains(logBuf.String(), "reminders due") {
		t.Fatalf("missing reminders log: %s", logBuf.String())
	}

	// Clock restarted: the next scheduled pass stays quiet.
	if err := app.runPass(context.Background(), store, scheduled); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 {
		t.Fatalf("reminder repeated right away: %d batches", len(rec.got))
	}

}

func TestRunPassRetriesOnlyTheFailedNotifier(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	reg := &modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.27.1", "1.27.2"}}
	eng := NewEngine(&fleetProvider{containers: []model.Container{{Name: "web", ImageRef: "nginx:1.27.1"}}}, nil, config.Timeouts{
		Provider: config.Duration{Duration: time.Second},
		Registry: config.Duration{Duration: time.Second},
	}, log, nil, nil)
	eng.registries = append(eng.registries, reg)

	good := &recordingNotifier{}
	flaky := &recordingNotifier{err: errors.New("webhook down")}
	app := &App{engine: eng, log: log, notifiers: []notifierSlot{
		{id: "telegram", typ: "telegram", n: good},
		{id: "discord", typ: "discord", n: flaky},
	}}
	store := state.Load(statePath, log)
	scheduled := passMode{suppress: true, updateState: true}

	// Pass 1: telegram delivers, discord fails.
	if err := app.runPass(context.Background(), store, scheduled); err != nil {
		t.Fatal(err)
	}
	if len(good.got) != 1 || len(flaky.got) != 1 {
		t.Fatalf("pass 1: good=%d flaky=%d attempts", len(good.got), len(flaky.got))
	}

	// Pass 2: discord recovered — only discord gets it; telegram gets no duplicate.
	flaky.err = nil
	if err := app.runPass(context.Background(), store, scheduled); err != nil {
		t.Fatal(err)
	}
	if len(good.got) != 1 {
		t.Fatalf("telegram received a duplicate: %d batches", len(good.got))
	}
	if len(flaky.got) != 2 || flaky.got[1][0].Reminder {
		t.Fatalf("discord must catch up once (not as reminder): %+v", flaky.got)
	}

	// Pass 3: both delivered — silence, also after reloading the state file.
	reloaded := state.Load(statePath, log)
	if err := app.runPass(context.Background(), reloaded, scheduled); err != nil {
		t.Fatal(err)
	}
	if len(good.got) != 1 || len(flaky.got) != 2 {
		t.Fatalf("pass 3 must be silent: good=%d flaky=%d", len(good.got), len(flaky.got))
	}
}

func TestHeartbeatReportOutcome(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		paths = append(paths, r.URL.Path+" "+string(b))
		mu.Unlock()
	}))
	defer srv.Close()
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	pinger, err := heartbeat.New(config.HeartbeatConfig{URL: srv.URL + "/u", FailURL: srv.URL + "/u/fail"}, log)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{engine: NewEngine(&modeTestProvider{}, nil, config.Timeouts{}, log, nil, nil), log: log}
	app.engine.lastStats = PassStats{Checked: 5, Updates: 2, Skipped: 1}

	app.heartbeatReport(context.Background(), pinger, nil)
	app.lastFailedNotifiers = 1
	app.heartbeatReport(context.Background(), pinger, nil)
	app.lastFailedNotifiers = 0
	app.heartbeatReport(context.Background(), pinger, errors.New("list containers: socket gone"))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	app.heartbeatReport(canceled, pinger, context.Canceled)

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 3 {
		t.Fatalf("pings = %v, want 3 (canceled context sends none)", paths)
	}
	if !strings.HasPrefix(paths[0], "/u versentry: checked=5 up-to-date=0 updates=2 pending=0 skipped=1") {
		t.Fatalf("ok ping = %q", paths[0])
	}
	if !strings.HasPrefix(paths[1], "/u/fail ") || !strings.Contains(paths[1], "failed_notifiers=1") {
		t.Fatalf("notifier failure must ping fail_url: %q", paths[1])
	}
	if paths[2] != "/u/fail versentry: check failed: list containers: socket gone" {
		t.Fatalf("pass error ping = %q", paths[2])
	}
}

func TestRunPassCountsFailedNotifiers(t *testing.T) {
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	reg := &modeTestRegistry{host: imageref.DockerHubHost, listTags: []string{"1.27.1", "1.27.2"}}
	eng := NewEngine(&fleetProvider{containers: []model.Container{{Name: "web", ImageRef: "nginx:1.27.1"}}}, nil, config.Timeouts{
		Provider: config.Duration{Duration: time.Second},
		Registry: config.Duration{Duration: time.Second},
	}, log, nil, nil)
	eng.registries = append(eng.registries, reg)
	app := &App{engine: eng, log: log, notifiers: []notifierSlot{
		{id: "ok", n: &recordingNotifier{}},
		{id: "bad", n: &recordingNotifier{err: errors.New("down")}},
	}}
	store := state.Load(filepath.Join(t.TempDir(), "state.json"), log)
	if err := app.runPass(context.Background(), store, passMode{suppress: true, updateState: true}); err != nil {
		t.Fatal(err)
	}
	if app.lastFailedNotifiers != 1 || app.engine.LastStats().Updates != 1 {
		t.Fatalf("failed=%d stats=%+v", app.lastFailedNotifiers, app.engine.LastStats())
	}
}
