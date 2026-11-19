package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/BlackRaincoat/versentry/internal/core/registrypass"
	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/BlackRaincoat/versentry/internal/model"
	"github.com/BlackRaincoat/versentry/internal/registry"
	"github.com/Masterminds/semver/v3"
)

// minAgeProbes caps image-age lookups per container per pass. Each lookup is a
// manifest GET (one Docker Hub pull); newer-but-too-young candidates are
// skipped in favor of the next older one until an old enough one is found.
const minAgeProbes = 5

// minCreatedYear: images built with a zeroed/epoch "created" (reproducible
// builds) carry no age; treat anything before this as unknown.
const minCreatedYear = 1980

// maxCreatedSkew: a "created" up to this far ahead of our clock is registry/build
// clock skew (age 0, brand new). Further ahead is a bogus timestamp: treating it
// as new would keep the update pending until that date (2099 = forever), so it
// counts as unknown, like a zeroed one.
const maxCreatedSkew = 24 * time.Hour

type pendingCandidate struct {
	tag string
	age time.Duration
}

// effectiveMinAge is the config rule's min_age when set (0 = off), else global.
func (e *Engine) effectiveMinAge(rule *Rule) time.Duration {
	if rule != nil && rule.MinAge != nil {
		return *rule.MinAge
	}
	return e.minAge
}

// pickAgedCandidate returns the newest candidate at least minAge old.
// pick(remaining) must return the best tag strictly newer than the running one
// (false when none is left); same(a, b) reports equal versions so every spelling
// of a too-young version (8.3 / 8.3.0) is dropped together.
// Unknown age (no timestamp, registry without ImageCreatedReader) counts as old
// enough: min_age only delays when the registry proves an image is young.
// Returns pending (newest young candidate) when no probed candidate qualifies.
func (e *Engine) pickAgedCandidate(
	ctx context.Context,
	pass *registrypass.Pass,
	reg registry.Registry,
	parsed imageref.Parsed,
	minAge time.Duration,
	tags []string,
	pick func(remaining []string) (string, bool),
	same func(a, b string) bool,
) (string, *pendingCandidate, error) {
	remaining := tags
	var newestYoung *pendingCandidate
	for probe := 0; probe < minAgeProbes; probe++ {
		tag, ok := pick(remaining)
		if !ok {
			break
		}
		age, known, err := e.candidateAge(ctx, pass, reg, parsed, tag)
		if err != nil {
			return "", nil, err
		}
		if !known || age >= minAge {
			if newestYoung != nil {
				e.log.Debug("min_age: newer candidate too young, using older",
					"image", parsed.Repo,
					"skipped", newestYoung.tag,
					"skipped_age", formatAge(newestYoung.age),
					"selected", tag,
				)
			}
			return tag, nil, nil
		}
		if newestYoung == nil {
			newestYoung = &pendingCandidate{tag: tag, age: age}
		}
		remaining = withoutVersion(remaining, tag, same)
	}
	return "", newestYoung, nil
}

// candidateAge reports how old repo:tag is in the registry. known=false when the
// registry gives no usable timestamp (zeroed created, no ImageCreatedReader).
func (e *Engine) candidateAge(
	ctx context.Context,
	pass *registrypass.Pass,
	reg registry.Registry,
	parsed imageref.Parsed,
	tag string,
) (time.Duration, bool, error) {
	ageCtx, cancel := context.WithTimeout(ctx, e.timeouts.Registry.Duration)
	created, err := pass.ImageCreated(ageCtx, reg, parsed.Host, parsed.Repo, tag)
	cancel()
	if err != nil {
		return 0, false, err
	}
	if created.IsZero() || created.Year() < minCreatedYear {
		e.logOnce(slog.LevelDebug,
			"min-age-unknown:"+parsed.Host+"/"+parsed.Repo+":"+tag,
			"min_age: image has no build timestamp, not delayed",
			"image", parsed.Repo,
			"tag", tag,
		)
		return 0, false, nil
	}
	now := e.now()
	if created.After(now.Add(maxCreatedSkew)) {
		e.logOnce(slog.LevelDebug,
			"min-age-future:"+parsed.Host+"/"+parsed.Repo+":"+tag,
			"min_age: image build timestamp is in the future, not delayed",
			"image", parsed.Repo,
			"tag", tag,
			"created", created.UTC().Format(time.RFC3339),
		)
		return 0, false, nil
	}
	age := now.Sub(created)
	if age < 0 {
		age = 0 // small clock skew between the build host and ours: brand new
	}
	return age, true, nil
}

func (e *Engine) pendingResult(c model.Container, currentTag string, p *pendingCandidate, minAge time.Duration) containerResult {
	return containerResult{
		Container:  c,
		Status:     statusPending,
		CurrentTag: currentTag,
		LatestTag:  p.tag,
		ImageRef:   c.ImageRef,
		Reason: fmt.Sprintf("%s is %s old, min_age %s",
			p.tag, formatAge(p.age), formatAge(minAge)),
	}
}

func withoutVersion(tags []string, drop string, same func(a, b string) bool) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if !same(t, drop) {
			out = append(out, t)
		}
	}
	return out
}

func sameSemverVersion(a, b string) bool {
	va, errA := semver.NewVersion(a)
	vb, errB := semver.NewVersion(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return va.Equal(vb)
}

func sameNumericVersion(a, b string) bool {
	va, okA := parseNumericVersion(a)
	vb, okB := parseNumericVersion(b)
	if !okA || !okB {
		return a == b
	}
	return compareNumeric(va, vb) == 0
}

// formatAge renders a duration as days/hours/minutes (3d, 1d4h, 5h20m, 0m).
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Minute)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)

	out := ""
	if days > 0 {
		out += fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		out += fmt.Sprintf("%dh", hours)
	}
	if mins > 0 && days == 0 {
		out += fmt.Sprintf("%dm", mins)
	}
	if out == "" {
		out = "0m"
	}
	return out
}
