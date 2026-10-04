package core

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/notifier/format"
)

type recordingNotifier struct {
	got [][]model.UpdateAvailable
	err error
}

func (r *recordingNotifier) Notify(_ context.Context, events []model.UpdateAvailable) error {
	r.got = append(r.got, events)
	return r.err
}

func TestSampleUpdatesCoverSemverAndDigest(t *testing.T) {
	batch := SampleUpdates(time.Unix(0, 0))
	if len(batch) != 2 {
		t.Fatalf("len = %d, want 2 (digest header shows count only when >1)", len(batch))
	}

	semverItem := format.ItemFromEvent("prod", batch[0], false)
	if semverItem.Change != "1.4.0 → 1.5.0" {
		t.Fatalf("semver change = %q", semverItem.Change)
	}
	if !strings.Contains(semverItem.URL, "github.com/BlackRaincoat/versentry/releases") {
		t.Fatalf("semver URL = %q, want GitHub releases", semverItem.URL)
	}

	digestItem := format.ItemFromEvent("prod", batch[1], false)
	if format.TrackingMode(batch[1]) != "digest" || !strings.HasPrefix(digestItem.Change, "digest changed: ") {
		t.Fatalf("digest item = %+v", digestItem)
	}
	if digestItem.URL == "" {
		t.Fatal("digest sample has no URL")
	}

	for _, u := range batch {
		if !strings.HasPrefix(u.Container.Name, "versentry-test-") {
			t.Fatalf("sample container %q not recognizable as a test", u.Container.Name)
		}
	}
}

func TestNotifyTestDeliversToAllNotifiers(t *testing.T) {
	a, b := &recordingNotifier{}, &recordingNotifier{}
	var buf bytes.Buffer
	app := &App{
		notifiers: []notifierSlot{{typ: "a", n: a}, {typ: "b", n: b}},
		log:       slog.New(slog.NewTextHandler(&buf, nil)),
	}

	if err := app.NotifyTest(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, n := range map[string]*recordingNotifier{"a": a, "b": b} {
		if len(n.got) != 1 || len(n.got[0]) != 2 {
			t.Fatalf("notifier %s got %v, want one batch of 2", name, n.got)
		}
	}
	if !strings.Contains(buf.String(), "notifier delivered") {
		t.Fatalf("missing delivery log: %s", buf.String())
	}
}

func TestNotifyTestFailsWhenAnyNotifierFails(t *testing.T) {
	ok, bad := &recordingNotifier{}, &recordingNotifier{err: errors.New("boom")}
	app := &App{
		notifiers: []notifierSlot{{typ: "bad", n: bad}, {typ: "ok", n: ok}},
		log:       slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	}

	if err := app.NotifyTest(context.Background()); err == nil {
		t.Fatal("expected error when a notifier fails")
	}
	if len(ok.got) != 1 {
		t.Fatal("remaining notifiers must still be tried after a failure")
	}
}

func TestNotifyTestNoNotifiers(t *testing.T) {
	app := &App{log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
	if err := app.NotifyTest(context.Background()); err == nil {
		t.Fatal("expected error with no notifiers")
	}
}
