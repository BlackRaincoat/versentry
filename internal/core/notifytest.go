package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BlackRaincoat/versentry/internal/model"
)

// Sample container names make a test delivery recognizable in any channel.
const (
	sampleSemverContainer = "versentry-test-semver"
	sampleDigestContainer = "versentry-test-digest"
)

// SampleUpdates returns one semver and one digest update (with OCI versions and
// build time) so both change-text shapes, a GitHub releases URL, a registry URL,
// and the multi-update digest header are exercised. Never touches Docker or a registry.
func SampleUpdates(now time.Time) []model.UpdateAvailable {
	return []model.UpdateAvailable{
		{
			Container: model.Container{
				ID:       "0000000000000000000000000000000000000000000000000000000000000001",
				Name:     sampleSemverContainer,
				ImageRef: "ghcr.io/blackraincoat/versentry:1.4.0",
				Labels: map[string]string{
					"org.opencontainers.image.source": "https://github.com/BlackRaincoat/versentry",
				},
			},
			Host:       "ghcr.io",
			Repo:       "blackraincoat/versentry",
			CurrentTag: "1.4.0",
			LatestTag:  "1.5.0",
			Bump:       model.BumpMinor,
			CheckedAt:  now,
		},
		{
			Container: model.Container{
				ID:       "0000000000000000000000000000000000000000000000000000000000000002",
				Name:     sampleDigestContainer,
				ImageRef: "nginx:latest",
			},
			Host:         "index.docker.io",
			Repo:         "library/nginx",
			CurrentTag:   "latest",
			LocalDigest:  "1111111111111111111111111111111111111111111111111111111111111111",
			RemoteDigest: "2222222222222222222222222222222222222222222222222222222222222222",
			CheckedAt:    now,
			// What DescribeUpdates adds when the image carries a version label.
			CurrentVersion: "1.27.2",
			LatestVersion:  "1.27.3",
			LatestCreated:  now.Add(-3 * 24 * time.Hour),
		},
	}
}

// NotifyTest sends SampleUpdates through every configured notifier.
// No Docker/registry calls and no state writes. Returns an error when no
// notifier is configured or any notifier fails (each failure is logged).
func (a *App) NotifyTest(ctx context.Context) error {
	if len(a.notifiers) == 0 {
		return errors.New("no notifiers configured")
	}
	batch := SampleUpdates(time.Now())
	a.log.Info("sending test notification", "notifiers", len(a.notifiers), "updates", len(batch))
	if !a.notifyAll(ctx, batch) {
		return fmt.Errorf("test notification failed for at least one notifier")
	}
	return nil
}
