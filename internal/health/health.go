package health

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/provider"
	"github.com/robfig/cron/v3"
)

const (
	minHealthMaxAge    = 15 * time.Minute
	cronFallbackMaxAge = 26 * time.Hour
	healthAgeMargin    = 5 * time.Minute
	maxHeartbeat       = 15 * time.Minute
	healthPingTimeout  = 5 * time.Second
)

var scheduleParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// ResolveStampPath returns the daemon liveness stamp next to the state file.
func ResolveStampPath(configPath, stateFile string) string {
	statePath := config.ResolveStatePath(configPath, stateFile)
	return filepath.Join(filepath.Dir(statePath), "health")
}

// Touch updates the liveness stamp when the daemon starts, on heartbeat, and after a successful run pass.
func Touch(configPath, stateFile string) error {
	path := ResolveStampPath(configPath, stateFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create health stamp dir: %w", err)
	}
	stamp := []byte(time.Now().UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(path, stamp, 0o644); err != nil {
		return fmt.Errorf("write health stamp: %w", err)
	}
	return nil
}

// HeartbeatInterval is how often versentry run refreshes the liveness stamp while the daemon is up.
func HeartbeatInterval(cfg *config.Config) time.Duration {
	maxAge := DefaultMaxAge(cfg)
	hb := maxAge / 4
	if hb < time.Minute {
		return time.Minute
	}
	if hb > maxHeartbeat {
		return maxHeartbeat
	}
	return hb
}

// DefaultMaxAge returns how old the liveness stamp may be before the daemon is unhealthy.
func DefaultMaxAge(cfg *config.Config) time.Duration {
	if cfg.HealthMaxAge.Duration > 0 {
		return cfg.HealthMaxAge.Duration
	}
	if cfg.Schedule != "" {
		return maxAgeFromCron(cfg, time.Now())
	}
	return maxAgeFromInterval(cfg.Interval.Duration)
}

func maxAgeFromInterval(interval time.Duration) time.Duration {
	age := 2*interval + healthAgeMargin
	if age < minHealthMaxAge {
		return minHealthMaxAge
	}
	return age
}

func maxAgeFromCron(cfg *config.Config, now time.Time) time.Duration {
	interval, err := cronRunInterval(cfg, now)
	if err != nil || interval <= 0 {
		slog.Default().Warn("health max age: cron interval unavailable, using fallback",
			"error", err,
			"fallback", cronFallbackMaxAge,
		)
		return cronFallbackMaxAge
	}
	return maxAgeFromInterval(interval)
}

func cronRunInterval(cfg *config.Config, now time.Time) (time.Duration, error) {
	loc, err := cfg.ScheduleLocation()
	if err != nil {
		return 0, err
	}
	schedule, err := scheduleParser.Parse(cfg.Schedule)
	if err != nil {
		return 0, err
	}
	t := now.In(loc)
	next1 := schedule.Next(t)
	next2 := schedule.Next(next1)
	interval := next2.Sub(next1)
	if interval <= 0 {
		return 0, fmt.Errorf("non-positive cron interval %v", interval)
	}
	return interval, nil
}

// Check verifies the provider is reachable and the daemon stamp is fresh.
func Check(ctx context.Context, cfg *config.Config, configPath string) error {
	prov, err := provider.New(cfg.Provider.Type, cfg.Provider.Config)
	if err != nil {
		return fmt.Errorf("provider: %w", err)
	}

	timeout := cfg.Timeouts.Provider.Duration
	if timeout <= 0 || timeout > healthPingTimeout {
		timeout = healthPingTimeout
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := prov.Ping(pingCtx); err != nil {
		return fmt.Errorf("docker provider: %w", err)
	}

	stampPath := ResolveStampPath(configPath, cfg.StateFile)
	age, err := StampAge(stampPath)
	if err != nil {
		return err
	}

	maxAge := DefaultMaxAge(cfg)
	if age > maxAge {
		return fmt.Errorf("health stamp stale at %s: age %s exceeds max %s", stampPath, age.Truncate(time.Second), maxAge)
	}

	return nil
}

// StampAge returns how old the liveness stamp file is.
func StampAge(path string) (time.Duration, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("health stamp missing at %s (daemon not ready)", path)
		}
		return 0, fmt.Errorf("health stamp: %w", err)
	}

	if raw, err := os.ReadFile(path); err == nil {
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw))); err == nil {
			return time.Since(t), nil
		}
	}

	return time.Since(info.ModTime()), nil
}
