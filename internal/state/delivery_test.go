package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
)

const nginxKey = "web|index.docker.io/library/nginx"

func deliveryStore(entries map[string]EntryState, ids ...string) *Store {
	s := &Store{
		data: fileData{Version: fileVersion, Entries: entries},
		now:  func() time.Time { return remindNow },
	}
	s.SetNotifiers(ids)
	return s
}

func batchNames(plan DeliveryPlan, id string) []string {
	var out []string
	for _, u := range plan.Batches[id] {
		out = append(out, u.LatestTag)
	}
	return out
}

func TestPlanLegacyEntryCountsAsDeliveredEverywhere(t *testing.T) {
	// State written before per-notifier tracking: an upgrade must re-send nothing.
	s := deliveryStore(map[string]EntryState{
		nginxKey: {Mode: "semver", Value: "1.27.2", NotifiedAt: remindNow.Add(-time.Hour)},
	}, "telegram", "discord")
	plan := s.Plan([]model.UpdateAvailable{nginxUpdate("1.27.2")})
	if len(plan.Batches) != 0 || plan.Suppressed != 1 || plan.CatchUp != 0 {
		t.Fatalf("plan = %+v, want everything suppressed", plan)
	}
}

func TestPartialFailureRetriesOnlyFailedNotifier(t *testing.T) {
	s := deliveryStore(map[string]EntryState{}, "telegram", "discord")
	u := nginxUpdate("1.27.2")

	plan := s.Plan([]model.UpdateAvailable{u})
	if len(plan.Batches["telegram"]) != 1 || len(plan.Batches["discord"]) != 1 {
		t.Fatalf("new target must go to every notifier: %+v", plan.Batches)
	}
	s.RecordDelivery("telegram", plan.Batches["telegram"]) // discord failed

	plan = s.Plan([]model.UpdateAvailable{u})
	if len(plan.Batches["telegram"]) != 0 {
		t.Fatalf("telegram already delivered, got duplicate: %v", batchNames(plan, "telegram"))
	}
	if len(plan.Batches["discord"]) != 1 || plan.Batches["discord"][0].Reminder || plan.CatchUp != 1 {
		t.Fatalf("discord must catch up (not as reminder): %+v", plan)
	}
	s.RecordDelivery("discord", plan.Batches["discord"])

	plan = s.Plan([]model.UpdateAvailable{u})
	if len(plan.Batches) != 0 || plan.Suppressed != 1 {
		t.Fatalf("both delivered, want silence: %+v", plan)
	}
}

func TestNewTargetResetsPerNotifierDelivery(t *testing.T) {
	s := deliveryStore(map[string]EntryState{
		nginxKey: {Mode: "semver", Value: "1.27.2", NotifiedAt: remindNow,
			Notifiers: map[string]time.Time{"telegram": remindNow, "discord": remindNow}},
	}, "telegram", "discord")
	newer := nginxUpdate("1.27.3")
	plan := s.Plan([]model.UpdateAvailable{newer})
	if len(plan.Batches["telegram"]) != 1 || len(plan.Batches["discord"]) != 1 || plan.CatchUp != 0 {
		t.Fatalf("new target goes to all: %+v", plan)
	}
	s.RecordDelivery("telegram", plan.Batches["telegram"])
	entry := s.data.Entries[nginxKey]
	if entry.Value != "1.27.3" || len(entry.Notifiers) != 1 {
		t.Fatalf("entry = %+v, want reset to the new target with telegram only", entry)
	}
}

func TestRecordDeliveryExpandsLegacyEntry(t *testing.T) {
	old := remindNow.Add(-10 * 24 * time.Hour)
	s := deliveryStore(map[string]EntryState{
		nginxKey: {Mode: "semver", Value: "1.27.2", NotifiedAt: old},
	}, "telegram", "discord")
	s.RecordDelivery("telegram", []model.UpdateAvailable{nginxUpdate("1.27.2")})
	got := s.data.Entries[nginxKey].Notifiers
	if !got["telegram"].Equal(remindNow) || !got["discord"].Equal(old) {
		t.Fatalf("notifiers = %v, want telegram=now, discord kept at legacy time", got)
	}
}

func TestRemindAfterIsPerNotifier(t *testing.T) {
	s := deliveryStore(map[string]EntryState{
		nginxKey: {Mode: "semver", Value: "1.27.2", NotifiedAt: remindNow,
			Notifiers: map[string]time.Time{
				"telegram": remindNow.Add(-8 * 24 * time.Hour),
				"discord":  remindNow.Add(-1 * 24 * time.Hour),
			}},
	}, "telegram", "discord")
	s.SetRemindAfter(7 * 24 * time.Hour)
	plan := s.Plan([]model.UpdateAvailable{nginxUpdate("1.27.2")})
	if len(plan.Batches["telegram"]) != 1 || !plan.Batches["telegram"][0].Reminder {
		t.Fatalf("telegram is due a reminder: %+v", plan.Batches)
	}
	if len(plan.Batches["discord"]) != 0 || plan.Reminders != 1 {
		t.Fatalf("discord not due yet: %+v", plan)
	}
}

func TestAddedNotifierCatchesUpOnce(t *testing.T) {
	s := deliveryStore(map[string]EntryState{
		nginxKey: {Mode: "semver", Value: "1.27.2", NotifiedAt: remindNow,
			Notifiers: map[string]time.Time{"telegram": remindNow}},
	}, "telegram", "apprise")
	plan := s.Plan([]model.UpdateAvailable{nginxUpdate("1.27.2")})
	if len(plan.Batches["apprise"]) != 1 || len(plan.Batches["telegram"]) != 0 {
		t.Fatalf("new notifier gets the pending update once: %+v", plan.Batches)
	}
}

func TestRemovedNotifierPrunedOnWrite(t *testing.T) {
	s := deliveryStore(map[string]EntryState{
		nginxKey: {Mode: "semver", Value: "1.27.2", NotifiedAt: remindNow,
			Notifiers: map[string]time.Time{"telegram": remindNow, "gone": remindNow}},
	}, "telegram")
	s.RecordDelivery("telegram", []model.UpdateAvailable{nginxUpdate("1.27.2")})
	if _, ok := s.data.Entries[nginxKey].Notifiers["gone"]; ok {
		t.Fatal("removed notifier must be pruned from the entry")
	}
}

func TestPerNotifierStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := deliveryStore(map[string]EntryState{}, "telegram", "discord")
	s.path = path
	s.RecordDelivery("telegram", []model.UpdateAvailable{nginxUpdate("1.27.2")})
	if err := s.Finish([]string{nginxKey}, true); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"notifiers"`) || !strings.Contains(string(raw), `"version": 2`) {
		t.Fatalf("state file = %s", raw)
	}

	loaded := Load(path, nil)
	loaded.now = func() time.Time { return remindNow }
	loaded.SetNotifiers([]string{"telegram", "discord"})
	plan := loaded.Plan([]model.UpdateAvailable{nginxUpdate("1.27.2")})
	if len(plan.Batches["discord"]) != 1 || len(plan.Batches["telegram"]) != 0 {
		t.Fatalf("after reload discord still owed: %+v", plan.Batches)
	}
}

func TestLegacyEntryJSONHasNoNotifiersField(t *testing.T) {
	raw, err := json.Marshal(EntryState{Mode: "semver", Value: "1", NotifiedAt: remindNow})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "notifiers") {
		t.Fatalf("nil map must be omitted (downgrade-safe): %s", raw)
	}
}
