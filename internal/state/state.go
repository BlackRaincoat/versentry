package state

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
)

const fileVersion = 2

// EntryState records the last notified update target for a container+image.
// Notifiers holds the last successful delivery of that target per notifier ID;
// nil (state written before per-notifier tracking) means "delivered to every
// notifier at NotifiedAt", so an upgrade re-sends nothing.
type EntryState struct {
	Mode       string               `json:"mode"`
	Value      string               `json:"value"`
	NotifiedAt time.Time            `json:"notified_at"`
	Notifiers  map[string]time.Time `json:"notifiers,omitempty"`
}

type fileData struct {
	Version int                   `json:"version"`
	Entries map[string]EntryState `json:"entries"`
}

// Store persists last-notified targets per container+image in a JSON file.
type Store struct {
	path string
	log  *slog.Logger
	data fileData

	remindAfter time.Duration    // 0 = never re-notify an unchanged target
	now         func() time.Time // clock (tests)
	notifierIDs []string         // configured notifiers (Plan / RecordDelivery)
}

// SetNotifiers sets the configured notifier IDs. Plan and RecordDelivery track
// delivery per ID so one failing channel never re-sends to the working ones.
func (s *Store) SetNotifiers(ids []string) {
	s.notifierIDs = append([]string(nil), ids...)
}

// DeliveryPlan is the per-notifier batch for one pass.
type DeliveryPlan struct {
	// Batches maps notifier ID → updates to send it (Reminder set per channel).
	Batches map[string][]model.UpdateAvailable
	// Suppressed counts updates that no notifier needs this pass.
	Suppressed int
	// CatchUp counts (update, notifier) pairs re-sent because that notifier
	// missed an already-notified target (earlier failure, or newly added).
	CatchUp int
	// Reminders counts updates sent as a reminder to at least one notifier.
	Reminders int
}

// Plan decides, per notifier, which updates to send:
//   - new target (no entry, or a different version/digest) → every notifier;
//   - same target, notifier has not delivered it → that notifier only (catch-up);
//   - same target, delivered ≥ remind_after ago → that notifier, Reminder=true;
//   - otherwise suppressed for that notifier.
func (s *Store) Plan(batch []model.UpdateAvailable) DeliveryPlan {
	plan := DeliveryPlan{Batches: make(map[string][]model.UpdateAvailable, len(s.notifierIDs))}
	now := s.clock()
	for _, u := range batch {
		entry, ok := s.data.Entries[EntryKey(u)]
		mode, value := targetFor(u)
		fresh := !ok || entry.Mode != mode || entry.Value != value

		sent, reminded := false, false
		for _, id := range s.notifierIDs {
			out := u
			if !fresh {
				last, delivered := entry.deliveredAt(id)
				switch {
				case !delivered:
					plan.CatchUp++
				case s.remindAfter > 0 && now.Sub(last) >= s.remindAfter:
					out.Reminder = true
					reminded = true
				default:
					continue
				}
			}
			plan.Batches[id] = append(plan.Batches[id], out)
			sent = true
		}
		if !sent {
			plan.Suppressed++
		}
		if reminded {
			plan.Reminders++
		}
	}
	return plan
}

// deliveredAt reports when notifier id last received this entry's target.
func (e EntryState) deliveredAt(id string) (time.Time, bool) {
	if e.Notifiers == nil {
		return e.NotifiedAt, true // pre per-notifier state: delivered everywhere
	}
	t, ok := e.Notifiers[id]
	return t, ok
}

// RecordDelivery marks updates as delivered to notifier id. A new target resets
// the entry, so other notifiers must deliver it again; legacy entries (nil
// Notifiers) are first expanded to every configured notifier.
func (s *Store) RecordDelivery(id string, delivered []model.UpdateAvailable) {
	now := s.clock().UTC()
	for _, u := range delivered {
		key := EntryKey(u)
		mode, value := targetFor(u)
		entry, ok := s.data.Entries[key]
		if !ok || entry.Mode != mode || entry.Value != value {
			entry = EntryState{Mode: mode, Value: value, Notifiers: map[string]time.Time{}}
		} else if entry.Notifiers == nil {
			entry.Notifiers = make(map[string]time.Time, len(s.notifierIDs))
			for _, other := range s.notifierIDs {
				entry.Notifiers[other] = entry.NotifiedAt
			}
		} else {
			entry.Notifiers = copyTimes(entry.Notifiers)
		}
		entry.Notifiers[id] = now
		entry.NotifiedAt = now
		s.pruneNotifiers(&entry)
		s.data.Entries[key] = entry
	}
}

// pruneNotifiers drops delivery marks of notifiers no longer configured.
func (s *Store) pruneNotifiers(entry *EntryState) {
	if len(s.notifierIDs) == 0 {
		return
	}
	known := make(map[string]struct{}, len(s.notifierIDs))
	for _, id := range s.notifierIDs {
		known[id] = struct{}{}
	}
	for id := range entry.Notifiers {
		if _, ok := known[id]; !ok {
			delete(entry.Notifiers, id)
		}
	}
}

func copyTimes(in map[string]time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Finish prunes stale entries (when canPrune) and saves; called once per
// state-updating pass after RecordDelivery for each notifier.
func (s *Store) Finish(activeKeys []string, canPrune bool) error {
	if canPrune {
		s.Prune(activeKeys)
	} else if s.log != nil {
		s.log.Warn("state prune skipped", "active_keys", len(activeKeys))
	}
	return s.Save()
}

// SetRemindAfter re-notifies an unchanged target once its last notification is
// at least d old (0 = off, the default: notify each target once).
func (s *Store) SetRemindAfter(d time.Duration) {
	s.remindAfter = d
}

// Load reads state from path. A missing file yields an empty store.
// Corrupt JSON logs WARN and yields an empty store (monitoring must not block).
// Files with version < 2 (image-scoped keys) are reset: conversion is impossible.
func Load(path string, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}

	s := &Store{
		path: path,
		log:  log,
		data: emptyData(),
		now:  time.Now,
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s
		}
		s.log.Warn("state file unreadable, starting empty", "path", path, "error", err)
		return s
	}

	var loaded fileData
	if err := json.Unmarshal(raw, &loaded); err != nil {
		s.log.Warn("state file corrupt, starting empty", "path", path, "error", err)
		return s
	}
	if loaded.Version < fileVersion {
		s.log.Warn("state format changed (v1→v2), history reset, one-time re-notification of pending updates possible")
		return s
	}
	if loaded.Entries == nil {
		loaded.Entries = make(map[string]EntryState)
	}
	s.data = loaded
	return s
}

func emptyData() fileData {
	return fileData{
		Version: fileVersion,
		Entries: make(map[string]EntryState),
	}
}

// MissingFile reports whether path does not exist yet (first run with no prior state).
func MissingFile(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

// EntryKey returns the state key for an update: {container}|{host}/{repo}.
func EntryKey(u model.UpdateAvailable) string {
	return FormatEntryKey(u.Container.Name, u.Container.ID, u.Host, u.Repo)
}

// FormatEntryKey builds a state key from container identity and image host/repo.
// Empty container name falls back to a short container ID (or "unknown").
func FormatEntryKey(name, id, host, repo string) string {
	namePart := name
	if namePart == "" {
		namePart = shortContainerID(id)
	}
	return namePart + "|" + host + "/" + repo
}

func shortContainerID(id string) string {
	if id == "" {
		return "unknown"
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// EntryCount returns the number of entries tracked in state.
func (s *Store) EntryCount() int {
	return len(s.data.Entries)
}

func (s *Store) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

func targetFor(u model.UpdateAvailable) (mode, value string) {
	if u.LatestTag != "" {
		return "semver", u.LatestTag
	}
	return "digest", normalizeDigest(u.RemoteDigest)
}

func normalizeDigest(d string) string {
	return strings.TrimPrefix(d, "sha256:")
}

// Prune removes state entries for containers no longer in the active fleet.
func (s *Store) Prune(activeKeys []string) {
	active := make(map[string]struct{}, len(activeKeys))
	for _, key := range activeKeys {
		active[key] = struct{}{}
	}
	for key := range s.data.Entries {
		if _, ok := active[key]; !ok {
			delete(s.data.Entries, key)
		}
	}
}

// Save writes state atomically via a temp file and rename.
func (s *Store) Save() error {
	if s.path == "" {
		return fmt.Errorf("state path is empty")
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	s.data.Version = fileVersion
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	raw = append(raw, '\n')

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write state temp file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename state file: %w", err)
	}
	return nil
}
