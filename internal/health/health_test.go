package health_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BlackRaincoat/versentry/internal/config"
	"github.com/BlackRaincoat/versentry/internal/health"
)

func TestHeartbeatIntervalCapsAtFifteenMinutes(t *testing.T) {
	cfg := &config.Config{
		Schedule: "0 3 * * *",
		Timezone: "UTC",
	}
	if health.HeartbeatInterval(cfg) != 15*time.Minute {
		t.Fatalf("got %v", health.HeartbeatInterval(cfg))
	}
}

func TestHeartbeatIntervalFromInterval(t *testing.T) {
	cfg := &config.Config{
		Interval: config.Duration{Duration: time.Hour},
	}
	if health.HeartbeatInterval(cfg) != 15*time.Minute {
		t.Fatalf("got %v", health.HeartbeatInterval(cfg))
	}
}

func TestDefaultMaxAgeInterval(t *testing.T) {
	cfg := &config.Config{
		Interval: config.Duration{Duration: time.Hour},
	}
	got := health.DefaultMaxAge(cfg)
	want := 2*time.Hour + 5*time.Minute
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDefaultMaxAgeDailySchedule(t *testing.T) {
	cfg := &config.Config{
		Schedule: "0 3 * * *",
		Timezone: "UTC",
	}
	got := health.DefaultMaxAge(cfg)
	// Daily cron → ~24h between runs → 2×24h + 5m
	want := 48*time.Hour + 5*time.Minute
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDefaultMaxAgeWeeklySchedule(t *testing.T) {
	cfg := &config.Config{
		Schedule: "0 3 * * 0",
		Timezone: "UTC",
	}
	got := health.DefaultMaxAge(cfg)
	want := 14*24*time.Hour + 5*time.Minute
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDefaultMaxAgeOverride(t *testing.T) {
	cfg := &config.Config{
		Interval:     config.Duration{Duration: time.Hour},
		HealthMaxAge: config.Duration{Duration: 90 * time.Minute},
	}
	if health.DefaultMaxAge(cfg) != 90*time.Minute {
		t.Fatal("expected override")
	}
}

func TestResolveStampPath(t *testing.T) {
	got := health.ResolveStampPath("/etc/versentry/config.yaml", "/data/state.json")
	if got != "/data/health" {
		t.Fatalf("got %q", got)
	}
}

func TestStampAgeFromContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "health")
	stamp := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano)
	if err := os.WriteFile(path, []byte(stamp), 0o644); err != nil {
		t.Fatal(err)
	}

	age, err := health.StampAge(path)
	if err != nil {
		t.Fatal(err)
	}
	if age < 29*time.Minute || age > 31*time.Minute {
		t.Fatalf("age = %v", age)
	}
}
