package core

import (
	"log/slog"
	"slices"
	"strconv"

	"github.com/BlackRaincoat/versentry/internal/model"
)

// Label key for watch policy (WUD equivalent: wud.watch).
const labelWatch = "versentry.watch"

// watchPolicy is the container selection policy for a pass.
type watchPolicy struct {
	exclude        map[string]struct{} // exclude_containers (always excludes)
	watch          map[string]struct{} // watch_containers (opt-in by name)
	watchByDefault bool                // false = opt-in
}

// ShouldMonitor reports whether a container should be checked under the default
// opt-out policy (watch_by_default: true).
//
// Exclusion is OR of two opt-out sources (both only exclude, never force-include):
//  1. name listed in config exclude_containers
//  2. label versentry.watch=false (missing/invalid label → monitor)
func ShouldMonitor(c model.Container, exclude map[string]struct{}, log *slog.Logger) bool {
	return watchPolicy{exclude: exclude, watchByDefault: true}.monitors(c, log)
}

// monitors applies the policy. Exclusion always wins: exclude_containers or
// versentry.watch=false. Otherwise versentry.watch=true or a watch_containers
// name selects the container; else watchByDefault decides (false = opt-in skip).
// An invalid label value logs WARN and counts as absent.
func (p watchPolicy) monitors(c model.Container, log *slog.Logger) bool {
	if _, ok := p.exclude[c.Name]; ok {
		return false
	}
	if v, ok := watchLabel(c, log); ok {
		return v
	}
	if _, ok := p.watch[c.Name]; ok {
		return true
	}
	return p.watchByDefault
}

// watchLabel returns the parsed versentry.watch label; ok=false when absent or invalid.
func watchLabel(c model.Container, log *slog.Logger) (value, ok bool) {
	raw, present := c.Labels[labelWatch]
	if !present {
		return false, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		if log != nil {
			log.Warn("invalid versentry.watch label value, ignoring",
				"container", c.Name,
				"value", raw,
			)
		}
		return false, false
	}
	return v, true
}

func filterByWatch(containers []model.Container, exclude map[string]struct{}, log *slog.Logger) ([]model.Container, int) {
	return filterByWatchPolicy(containers, watchPolicy{exclude: exclude, watchByDefault: true}, log)
}

func filterByWatchPolicy(containers []model.Container, policy watchPolicy, log *slog.Logger) ([]model.Container, int) {
	if len(containers) == 0 {
		return nil, 0
	}

	monitored := make([]model.Container, 0, len(containers))
	excluded := 0

	for _, c := range containers {
		if policy.monitors(c, log) {
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

	if !policy.watchByDefault && len(monitored) == 0 && log != nil {
		log.Warn("watch_by_default is false and no running container is in watch_containers or labeled versentry.watch=true; nothing to check")
	}

	return monitored, excluded
}

// warnMissingExcludeContainers logs WARN for exclude_containers names not in the running fleet.
// Does not abort: the container may be temporarily stopped.
func warnMissingExcludeContainers(fleet []model.Container, exclude map[string]struct{}, log *slog.Logger) {
	warnMissingContainers(fleet, exclude, "exclude_containers", log)
}

// warnMissingContainers logs WARN for configured names (field) not in the running fleet.
func warnMissingContainers(fleet []model.Container, names map[string]struct{}, field string, log *slog.Logger) {
	if log == nil || len(names) == 0 {
		return
	}
	present := make(map[string]struct{}, len(fleet))
	for _, c := range fleet {
		present[c.Name] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	slices.Sort(sorted)
	for _, name := range sorted {
		if _, ok := present[name]; !ok {
			log.Warn(field+" name not found among running containers",
				"name", name,
			)
		}
	}
}
