package state

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
)

var remindNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func remindStore(notifiedAt time.Time, remindAfter time.Duration) *Store {
	s := &Store{
		data: fileData{
			Version: fileVersion,
			Entries: map[string]EntryState{
				"web|index.docker.io/library/nginx": {Mode: "semver", Value: "1.27.2", NotifiedAt: notifiedAt},
			},
		},
		now: func() time.Time { return remindNow },
	}
	s.SetRemindAfter(remindAfter)
	return s
}

func nginxUpdate(latest string) model.UpdateAvailable {
	return model.UpdateAvailable{
		Container:  model.Container{Name: "web"},
		Host:       "index.docker.io",
		Repo:       "library/nginx",
		CurrentTag: "1.27.1",
		LatestTag:  latest,
	}
}

func TestRemindAfterResendsUnchangedTargetOnceDue(t *testing.T) {
	s := remindStore(remindNow.Add(-8*24*time.Hour), 7*24*time.Hour)
	toNotify, suppressed := filter(s, []model.UpdateAvailable{nginxUpdate("1.27.2")})
	if len(toNotify) != 1 || len(suppressed) != 0 || !toNotify[0].Reminder {
		t.Fatalf("toNotify=%+v suppressed=%d, want one reminder", toNotify, len(suppressed))
	}
}

func TestRemindAfterNotDueStaysSuppressed(t *testing.T) {
	s := remindStore(remindNow.Add(-6*24*time.Hour), 7*24*time.Hour)
	toNotify, suppressed := filter(s, []model.UpdateAvailable{nginxUpdate("1.27.2")})
	if len(toNotify) != 0 || len(suppressed) != 1 {
		t.Fatalf("toNotify=%d suppressed=%d, want suppressed", len(toNotify), len(suppressed))
	}
}

func TestRemindAfterOffNeverResends(t *testing.T) {
	s := remindStore(remindNow.Add(-365*24*time.Hour), 0)
	if toNotify, _ := filter(s, []model.UpdateAvailable{nginxUpdate("1.27.2")}); len(toNotify) != 0 {
		t.Fatalf("remind_after=0 must keep the old once-per-target behavior, got %+v", toNotify)
	}
}

func TestRemindAfterNewTargetIsNotAReminder(t *testing.T) {
	s := remindStore(remindNow.Add(-30*24*time.Hour), 7*24*time.Hour)
	toNotify, _ := filter(s, []model.UpdateAvailable{nginxUpdate("1.27.3")})
	if len(toNotify) != 1 || toNotify[0].Reminder {
		t.Fatalf("new target must be a normal notification, got %+v", toNotify)
	}
}

func TestRemindAfterRecordRestartsClock(t *testing.T) {
	s := remindStore(remindNow.Add(-8*24*time.Hour), 7*24*time.Hour)
	s.path = filepath.Join(t.TempDir(), "state.json")
	toNotify, _ := filter(s, []model.UpdateAvailable{nginxUpdate("1.27.2")})
	if err := afterPass(s, []string{"web|index.docker.io/library/nginx"}, toNotify, true); err != nil {
		t.Fatal(err)
	}
	if got := s.data.Entries["web|index.docker.io/library/nginx"].NotifiedAt; !got.Equal(remindNow) {
		t.Fatalf("NotifiedAt = %v, want %v (reminder restarts the clock)", got, remindNow)
	}
	// Next pass right after: not due again.
	if toNotify, _ := filter(s, []model.UpdateAvailable{nginxUpdate("1.27.2")}); len(toNotify) != 0 {
		t.Fatalf("reminder repeated immediately: %+v", toNotify)
	}
}

func TestRemindAfterSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := remindStore(remindNow.Add(-8*24*time.Hour), 7*24*time.Hour)
	s.path = path
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	loaded := Load(path, nil)
	loaded.now = func() time.Time { return remindNow }
	loaded.SetRemindAfter(7 * 24 * time.Hour)
	toNotify, _ := filter(loaded, []model.UpdateAvailable{nginxUpdate("1.27.2")})
	if len(toNotify) != 1 || !toNotify[0].Reminder {
		t.Fatalf("notified_at from the existing state file must drive reminders, got %+v", toNotify)
	}
}
