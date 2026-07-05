package stdout

import (
	"context"
	"log/slog"
	"strings"

	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/notifier"
)

func init() {
	notifier.Register("stdout", New)
}

// Notifier logs update events at INFO (same slog handler as the rest of versentry).
type Notifier struct{}

// New constructs a stdout Notifier.
func New(cfg map[string]any) (notifier.Notifier, error) {
	return &Notifier{}, nil
}

// Notify writes a structured log line for each update in the batch.
// Digests are truncated for display only; events carry full digests.
func (n *Notifier) Notify(ctx context.Context, events []model.UpdateAvailable) error {
	for _, event := range events {
		n.logOne(event)
	}
	return nil
}

func (n *Notifier) logOne(event model.UpdateAvailable) {
	image := event.Repo + ":" + event.CurrentTag
	if event.LatestTag != "" {
		slog.Info("container update",
			"container", event.Container.Name,
			"image", image,
			"latest", event.LatestTag,
			"host", event.Host,
		)
		return
	}

	slog.Info("container update",
		"container", event.Container.Name,
		"image", image,
		"host", event.Host,
		"local", shortDigest(event.LocalDigest),
		"remote", shortDigest(event.RemoteDigest),
	)
}

func shortDigest(d string) string {
	hex := strings.TrimPrefix(d, "sha256:")
	if len(hex) > 12 {
		hex = hex[:12]
	}
	return "sha256:" + hex + "…"
}
