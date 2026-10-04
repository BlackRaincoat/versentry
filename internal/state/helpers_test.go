package state

import "github.com/BlackRaincoat/versentry/internal/model"

// testNotifier is the single notifier ID used by tests written against the
// one-channel view (notify / suppress), before per-notifier delivery.
const testNotifier = "n"

func (s *Store) ensureTestNotifier() {
	if len(s.notifierIDs) == 0 {
		s.SetNotifiers([]string{testNotifier})
	}
}

// filter is the one-notifier view of Plan: updates to send vs suppressed.
func filter(s *Store, batch []model.UpdateAvailable) (toNotify, suppressed []model.UpdateAvailable) {
	s.ensureTestNotifier()
	plan := s.Plan(batch)
	toNotify = plan.Batches[testNotifier]
	sent := make(map[string]bool, len(toNotify))
	for _, u := range toNotify {
		sent[EntryKey(u)] = true
	}
	for _, u := range batch {
		if !sent[EntryKey(u)] {
			suppressed = append(suppressed, u)
		}
	}
	return toNotify, suppressed
}

// afterPass records delivery to the test notifier, prunes, and saves.
func afterPass(s *Store, activeKeys []string, notified []model.UpdateAvailable, canPrune bool) error {
	s.ensureTestNotifier()
	if len(notified) > 0 {
		s.RecordDelivery(testNotifier, notified)
	}
	return s.Finish(activeKeys, canPrune)
}

func record(s *Store, notified []model.UpdateAvailable) {
	s.ensureTestNotifier()
	s.RecordDelivery(testNotifier, notified)
}
