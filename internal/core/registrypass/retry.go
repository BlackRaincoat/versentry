package registrypass

import (
	"context"
	"errors"
	"time"

	"github.com/BlackRaincoat/versentry/internal/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

const (
	// maxRegistryRetryAfter is the longest Retry-After honored for a single 429 retry;
	// longer or missing values mark the host rate-limited for the rest of the pass.
	maxRegistryRetryAfter = 10 * time.Second

	// registry5xxMaxAttempts is pass-level retries on top of go-containerregistry transport retries.
	registry5xxMaxAttempts = 2

	registry5xxBackoff           = time.Second
	registry5xxBackoffMultiplier = 2
)

type sleepFunc func(ctx context.Context, d time.Duration) error

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRateLimit(err error) bool {
	var rle *registry.RateLimitError
	return errors.As(err, &rle)
}

func isServerError(err error) bool {
	var terr *transport.Error
	if errors.As(err, &terr) {
		return terr.StatusCode >= 500 && terr.StatusCode < 600
	}
	return false
}

func serverBackoff(attempt int) time.Duration {
	delay := registry5xxBackoff
	for i := 1; i < attempt; i++ {
		delay *= registry5xxBackoffMultiplier
	}
	return delay
}
