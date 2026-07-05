package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/provider"
	"github.com/BlackRaincoat/versentry/internal/registry"
)

type checkStatus string

const (
	statusUpToDate checkStatus = "up-to-date"
	statusUpdate   checkStatus = "update"
	statusSkipped  checkStatus = "skipped"
)

type containerResult struct {
	Container    model.Container
	Status       checkStatus
	Reason       string
	CurrentTag   string
	LatestTag    string
	LocalDigest  string
	RemoteDigest string
	ImageRef     string
	Update       *model.UpdateAvailable
}

// Engine orchestrates container discovery and version comparison.
type Engine struct {
	provider     provider.Provider
	registries   []registry.Registry
	timeouts     config.Timeouts
	log          *slog.Logger
	tagSelector  TagSelector
	ruleSelector TagSelector
	rules        RuleResolver
}

// NewEngine wires the core check loop.
func NewEngine(
	prov provider.Provider,
	registries []registry.Registry,
	timeouts config.Timeouts,
	log *slog.Logger,
	rules RuleResolver,
) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		provider:     prov,
		registries:   registries,
		timeouts:     timeouts,
		log:          log,
		tagSelector:  DefaultTagSelector{},
		ruleSelector: RuleTagSelector{},
		rules:        rules,
	}
}

// RunOnce performs a single pass over all running containers and returns
// discovered updates, active image keys (host/repo) for state pruning, and
// whether pruning is safe (true only after a successful non-empty fleet listing).
func (e *Engine) RunOnce(ctx context.Context) ([]model.UpdateAvailable, []string, bool, error) {
	listCtx, cancel := context.WithTimeout(ctx, e.timeouts.Provider.Duration)
	defer cancel()

	containers, err := e.provider.ListRunning(listCtx)
	if err != nil {
		return nil, nil, false, fmt.Errorf("list containers: %w", err)
	}

	canPrune := len(containers) > 0
	if !canPrune {
		e.log.Warn("empty container list, state prune skipped")
	}

	monitored, excluded := filterByWatch(containers, e.log)
	e.log.Info("listed running containers",
		"count", len(containers),
		"monitored", len(monitored),
		"excluded", excluded,
	)

	pass := registrypass.New(e.log)

	var checked, upToDate, updates, skipped int
	found := make([]model.UpdateAvailable, 0)
	activeKeys := make([]string, 0, len(monitored))
	activeSeen := make(map[string]struct{}, len(monitored))

	for _, c := range monitored {
		if key, ok := imageKey(c.ImageRef); ok {
			if _, dup := activeSeen[key]; !dup {
				activeSeen[key] = struct{}{}
				activeKeys = append(activeKeys, key)
			}
		}

		result, err := e.checkContainer(ctx, c, pass)
		if err != nil {
			return nil, nil, false, fmt.Errorf("check container %q: %w", c.Name, err)
		}

		checked++
		switch result.Status {
		case statusUpToDate:
			upToDate++
		case statusUpdate:
			updates++
			if result.Update != nil {
				found = append(found, *result.Update)
			}
		case statusSkipped:
			skipped++
		}

		e.logContainerResult(result)
	}

	e.log.Info(
		"check complete",
		"checked", checked,
		"up-to-date", upToDate,
		"updates", updates,
		"skipped", skipped,
		"excluded", excluded,
	)

	return found, activeKeys, canPrune, nil
}

func imageKey(raw string) (string, bool) {
	parsed, err := imageref.Parse(raw)
	if err != nil {
		return "", false
	}
	return parsed.Host + "/" + parsed.Repo, true
}

func (e *Engine) checkContainer(ctx context.Context, c model.Container, pass *registrypass.Pass) (containerResult, error) {
	base := containerResult{Container: c, ImageRef: c.ImageRef}

	parsed, err := imageref.Parse(c.ImageRef)
	if err != nil {
		return e.skipped(base, fmt.Sprintf("parse image ref: %v", err)), nil
	}

	if parsed.Tag == "" {
		return e.skipped(base, "digest-only reference"), nil
	}

	reg, err := e.registryForHost(parsed.Host)
	if err != nil {
		return e.skipped(base, err.Error()), nil
	}

	current, err := parseContainerSemver(parsed.Tag)
	if err != nil {
		return e.checkDigest(ctx, c, parsed, reg, pass)
	}

	listCtx, cancel := context.WithTimeout(ctx, e.timeouts.Registry.Duration)
	tags, err := pass.ListTags(listCtx, reg, parsed.Host, parsed.Repo)
	cancel()
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrUnauthorized) {
			return e.skipped(base, "not found in registry / locally built"), nil
		}
		if errors.Is(err, registry.ErrRateLimited) {
			return e.skipped(base, "registry rate limited, will retry next pass"), nil
		}
		if errors.Is(err, registry.ErrUnavailable) {
			return e.skipped(base, "registry temporarily unavailable"), nil
		}
		return containerResult{}, fmt.Errorf("list tags for %s: %w", parsed.Repo, err)
	}

	selector := e.tagSelector
	if e.rules != nil {
		if rule := e.rules.RuleFor(RuleQuery{Image: parsed.Repo, Labels: c.Labels}); rule != nil {
			if !rule.Include.MatchString(parsed.Tag) {
				return e.skipped(base, "current tag does not match include rule"), nil
			}
			tags = filterTags(tags, rule.Include)
			if len(tags) == 0 {
				return e.skipped(base, "no tags match include rule"), nil
			}
			selector = e.ruleSelector
		}
	}

	latestTag, latest, ok := selector.Select(current, tags)
	if !ok {
		return e.skipped(base, "no matching semver tags in registry"), nil
	}

	if latest.GreaterThan(current) {
		update := model.UpdateAvailable{
			Container:  c,
			Host:       parsed.Host,
			Repo:       parsed.Repo,
			CurrentTag: parsed.Tag,
			LatestTag:  latestTag,
			CheckedAt:  time.Now().UTC(),
		}
		return containerResult{
			Container:  c,
			Status:     statusUpdate,
			CurrentTag: parsed.Tag,
			LatestTag:  latestTag,
			ImageRef:   c.ImageRef,
			Update:     &update,
		}, nil
	}

	return containerResult{
		Container:  c,
		Status:     statusUpToDate,
		CurrentTag: parsed.Tag,
		LatestTag:  latestTag,
		ImageRef:   c.ImageRef,
	}, nil
}

// checkDigest compares the running image digest against the registry tag digest.
// Used for non-semver tags (latest, stable, custom). Full digests are stored and
// compared; truncation is display-only in logs and notifiers.
func (e *Engine) checkDigest(
	ctx context.Context,
	c model.Container,
	parsed imageref.Parsed,
	reg registry.Registry,
	pass *registrypass.Pass,
) (containerResult, error) {
	base := containerResult{Container: c, ImageRef: c.ImageRef, CurrentTag: parsed.Tag}

	localCtx, cancel := context.WithTimeout(ctx, e.timeouts.Provider.Duration)
	localDigest, err := e.provider.LocalDigest(localCtx, c, parsed.Repo)
	cancel()
	if err != nil || localDigest == "" {
		return e.skipped(base, "no local digest (locally built or not pulled from registry)"), nil
	}

	remoteCtx, cancel := context.WithTimeout(ctx, e.timeouts.Registry.Duration)
	remoteDigest, err := pass.TagDigest(remoteCtx, reg, parsed.Host, parsed.Repo, parsed.Tag)
	cancel()
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) || errors.Is(err, registry.ErrUnauthorized) {
			return e.skipped(base, "not found in registry / locally built"), nil
		}
		if errors.Is(err, registry.ErrRateLimited) {
			return e.skipped(base, "registry rate limited, will retry next pass"), nil
		}
		if errors.Is(err, registry.ErrUnavailable) {
			return e.skipped(base, "registry temporarily unavailable"), nil
		}
		return containerResult{}, fmt.Errorf("resolve remote digest for %s:%s: %w", parsed.Repo, parsed.Tag, err)
	}
	if remoteDigest == "" {
		return e.skipped(base, "not found in registry / locally built"), nil
	}

	if normalizeDigest(localDigest) == normalizeDigest(remoteDigest) {
		return containerResult{
			Container:    c,
			Status:       statusUpToDate,
			CurrentTag:   parsed.Tag,
			LocalDigest:  localDigest,
			RemoteDigest: remoteDigest,
			ImageRef:     c.ImageRef,
		}, nil
	}

	update := model.UpdateAvailable{
		Container:    c,
		Host:         parsed.Host,
		Repo:         parsed.Repo,
		CurrentTag:   parsed.Tag,
		LocalDigest:  localDigest,
		RemoteDigest: remoteDigest,
		CheckedAt:    time.Now().UTC(),
	}

	return containerResult{
		Container:    c,
		Status:       statusUpdate,
		CurrentTag:   parsed.Tag,
		LocalDigest:  localDigest,
		RemoteDigest: remoteDigest,
		ImageRef:     c.ImageRef,
		Update:       &update,
	}, nil
}

func (e *Engine) skipped(base containerResult, reason string) containerResult {
	base.Status = statusSkipped
	base.Reason = reason
	return base
}

func (e *Engine) logContainerResult(r containerResult) {
	attrs := []any{
		"container", r.Container.Name,
		"image", r.ImageRef,
		"status", string(r.Status),
	}

	switch r.Status {
	case statusUpdate:
		if r.LatestTag != "" {
			attrs = append(attrs, "current", r.CurrentTag, "latest", r.LatestTag)
		} else {
			attrs = append(attrs,
				"tag", r.CurrentTag,
				"local", shortDigest(r.LocalDigest),
				"remote", shortDigest(r.RemoteDigest),
			)
		}
		e.log.Info("container checked", attrs...)
	case statusUpToDate:
		attrs = append(attrs, "tag", r.CurrentTag)
		e.log.Debug("container checked", attrs...)
	case statusSkipped:
		attrs = append(attrs, "reason", r.Reason)
		e.log.Warn("container skipped", attrs...)
	}
}

func (e *Engine) registryForHost(host string) (registry.Registry, error) {
	for _, reg := range e.registries {
		if reg.Host() == host {
			return reg, nil
		}
	}
	return nil, fmt.Errorf("no registry configured for host %q", host)
}

func normalizeDigest(d string) string {
	return strings.TrimPrefix(d, "sha256:")
}

// shortDigest formats a digest for human-readable output only.
// Full digests are kept for comparison and events.
func shortDigest(d string) string {
	hex := strings.TrimPrefix(d, "sha256:")
	if len(hex) > 12 {
		hex = hex[:12]
	}
	return "sha256:" + hex + "…"
}
