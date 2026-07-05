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

const fileVersion = 1

// ImageState records the last notified update target for an image.
type ImageState struct {
	Mode       string    `json:"mode"`
	Value      string    `json:"value"`
	NotifiedAt time.Time `json:"notified_at"`
}

type fileData struct {
	Version int                   `json:"version"`
	Images  map[string]ImageState `json:"images"`
}

// Store persists last-notified targets per image in a JSON file.
type Store struct {
	path string
	log  *slog.Logger
	data fileData
}

// Load reads state from path. A missing file yields an empty store.
// Corrupt JSON logs WARN and yields an empty store (monitoring must not block).
func Load(path string, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}

	s := &Store{
		path: path,
		log:  log,
		data: fileData{
			Version: fileVersion,
			Images:  make(map[string]ImageState),
		},
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
	if loaded.Images == nil {
		loaded.Images = make(map[string]ImageState)
	}
	if loaded.Version == 0 {
		loaded.Version = fileVersion
	}
	s.data = loaded
	return s
}

// ImageKey returns the normalized state key for an update event.
func ImageKey(u model.UpdateAvailable) string {
	return u.Host + "/" + u.Repo
}

// EntryCount returns the number of images tracked in state.
func (s *Store) EntryCount() int {
	return len(s.data.Images)
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
	stored, ok := s.data.Images[ImageKey(u)]
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
		s.data.Images[ImageKey(u)] = ImageState{
			Mode:       mode,
			Value:      value,
			NotifiedAt: now,
		}
	}
}

// Prune removes state entries for images no longer in the active fleet.
func (s *Store) Prune(activeKeys []string) {
	active := make(map[string]struct{}, len(activeKeys))
	for _, key := range activeKeys {
		active[key] = struct{}{}
	}
	for key := range s.data.Images {
		if _, ok := active[key]; !ok {
			delete(s.data.Images, key)
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

// AfterPass records notified updates, optionally prunes stale images, and saves.
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
