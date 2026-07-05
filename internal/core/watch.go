package core

import (
	"log/slog"
	"strconv"

	"github.com/BlackRaincoat/versentry/internal/model"
)

// Label key for opt-out monitoring (WUD equivalent: wud.watch).
const labelWatch = "versentry.watch"

// ShouldMonitor reports whether a container should be checked.
// Missing or invalid label values default to monitoring; explicit false opts out.
func ShouldMonitor(labels map[string]string, log *slog.Logger, containerName string) bool {
	if len(labels) == 0 {
		return true
	}

	raw, ok := labels[labelWatch]
	if !ok {
		return true
	}

	v, err := strconv.ParseBool(raw)
	if err != nil {
		if log != nil {
			log.Warn("invalid versentry.watch label value, ignoring",
				"container", containerName,
				"value", raw,
			)
		}
		return true
	}

	return v
}

func filterByWatch(containers []model.Container, log *slog.Logger) ([]model.Container, int) {
	if len(containers) == 0 {
		return nil, 0
	}

	monitored := make([]model.Container, 0, len(containers))
	excluded := 0

	for _, c := range containers {
		if ShouldMonitor(c.Labels, log, c.Name) {
			monitored = append(monitored, c)
			continue
		}

		excluded++
		if log != nil {
			log.Debug("container excluded by versentry.watch=false",
				"container", c.Name,
				"image", c.ImageRef,
			)
		}
	}

	return monitored, excluded
}
