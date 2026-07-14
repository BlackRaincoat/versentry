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
type EntryState struct {
	Mode       string    `json:"mode"`
	Value      string    `json:"value"`
	NotifiedAt time.Time `json:"notified_at"`
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

// Filter splits batch into updates that should be notified vs suppressed by state.
func (s *Store) Filter(batch []model.UpdateAvailable) (toNotify, suppressed []model.UpdateAvailable) {
	if len(batch) == 0 {
		return nil, nil
	}
	toNotify = make([]model.UpdateAvailable, 0, len(batch))
	for _, u := range batch {
		if s.isNew(u) {
			toNotify = append(toNotify, u)
		} else {
			suppressed = append(suppressed, u)
		}
	}
	return toNotify, suppressed
}

func (s *Store) isNew(u model.UpdateAvailable) bool {
	stored, ok := s.data.Entries[EntryKey(u)]
	if !ok {
		return true
	}

	mode, value := targetFor(u)
	if stored.Mode != mode {
		// Tracking mode changed (e.g. digest latest → semver rule); do not compare
		// digest hash with semver tag — treat as a fresh target.
		return true
	}

	return stored.Value != value
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

// Record updates in-memory state for successfully notified updates.
func (s *Store) Record(notified []model.UpdateAvailable) {
	now := time.Now().UTC()
	for _, u := range notified {
		mode, value := targetFor(u)
		s.data.Entries[EntryKey(u)] = EntryState{
			Mode:       mode,
			Value:      value,
			NotifiedAt: now,
		}
	}
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

// AfterPass records notified updates, optionally prunes stale entries, and saves.
// canPrune must be false when the running container list could not be trusted
// (ListRunning error — caller skips AfterPass entirely — or an empty fleet list).
func (s *Store) AfterPass(activeKeys []string, notified []model.UpdateAvailable, canPrune bool) error {
	if len(notified) > 0 {
		s.Record(notified)
	}
	if canPrune {
		s.Prune(activeKeys)
	} else if s.log != nil {
		s.log.Warn("state prune skipped", "active_keys", len(activeKeys))
	}
	return s.Save()
}
