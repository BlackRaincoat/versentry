package core

import (
	"log/slog"
	"slices"
	"strconv"

	"github.com/BlackRaincoat/versentry/internal/model"
)

// Label key for opt-out monitoring (WUD equivalent: wud.watch).
const labelWatch = "versentry.watch"

// ShouldMonitor reports whether a container should be checked.
//
// Exclusion is OR of two opt-out sources (both only exclude, never force-include):
//  1. name listed in config exclude_containers
//  2. label versentry.watch=false (missing/invalid label → monitor)
func ShouldMonitor(c model.Container, exclude map[string]struct{}, log *slog.Logger) bool {
	if exclude != nil {
		if _, ok := exclude[c.Name]; ok {
			return false
		}
	}
	return shouldMonitorLabel(c.Labels, log, c.Name)
}

func shouldMonitorLabel(labels map[string]string, log *slog.Logger, containerName string) bool {
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

func filterByWatch(containers []model.Container, exclude map[string]struct{}, log *slog.Logger) ([]model.Container, int) {
	if len(containers) == 0 {
		return nil, 0
	}

	monitored := make([]model.Container, 0, len(containers))
	excluded := 0

	for _, c := range containers {
		if ShouldMonitor(c, exclude, log) {
			monitored = append(monitored, c)
			continue
		}

		excluded++
		if log != nil {
			log.Debug("container excluded by watch policy",
				"container", c.Name,
				"image", c.ImageRef,
			)
		}
	}

	return monitored, excluded
}

// warnMissingExcludeContainers logs WARN for exclude_containers names not in the running fleet.
// Does not abort: the container may be temporarily stopped.
func warnMissingExcludeContainers(fleet []model.Container, exclude map[string]struct{}, log *slog.Logger) {
	if log == nil || len(exclude) == 0 {
		return
	}
	present := make(map[string]struct{}, len(fleet))
	for _, c := range fleet {
		present[c.Name] = struct{}{}
	}
	names := make([]string, 0, len(exclude))
	for name := range exclude {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if _, ok := present[name]; !ok {
			log.Warn("exclude_containers name not found among running containers",
				"name", name,
			)
		}
	}
}
