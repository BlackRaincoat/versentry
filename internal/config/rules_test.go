package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidateRulesDockerHubAliasConflict(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "postgres", Include: "^17"},
		{Image: "library/postgres", Include: "^16"},
	})
	if err == nil {
		t.Fatal("expected conflict between postgres and library/postgres")
	}
	if !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRulesDistinctImagesOK(t *testing.T) {
	if err := validateRules([]RuleConfig{
		{Image: "postgres", Include: "^17"},
		{Image: "chatwoot/chatwoot", Include: "^v"},
		{Image: "gethomepage/homepage", Include: "^v"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRulesTrackDigestOnlyOK(t *testing.T) {
	if err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Track: "digest"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRulesModeDigestAliasOK(t *testing.T) {
	if err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Mode: "digest"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRulesModeAndTrackFails(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Mode: "digest", Track: "digest"},
	})
	if err == nil {
		t.Fatal("expected conflict when mode and track both set")
	}
	if !strings.Contains(err.Error(), "use track only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRulesTrackDigestWithIncludeOK(t *testing.T) {
	if err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Track: "digest", Include: "^9-alpine$"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRulesUnknownTrackFails(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Track: "semver"},
	})
	if err == nil {
		t.Fatal("expected unknown track error")
	}
	if !strings.Contains(err.Error(), `only "digest" supported`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRulesUnknownModeAliasFails(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "valkey/valkey", Mode: "semver"},
	})
	if err == nil {
		t.Fatal("expected unknown track error for deprecated mode alias")
	}
	if !strings.Contains(err.Error(), `only "digest" supported`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRulesEmptyIncludeWithoutTrackFails(t *testing.T) {
	err := validateRules([]RuleConfig{
		{Image: "valkey/valkey"},
	})
	if err == nil {
		t.Fatal("expected include required error")
	}
	if !strings.Contains(err.Error(), "include, exclude, track: digest, or min_age is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEffectiveRuleTrackPrefersTrackOverMode(t *testing.T) {
	got := EffectiveRuleTrack(RuleConfig{Track: "digest", Mode: "digest"})
	if got != "digest" {
		t.Fatalf("got %q", got)
	}
}

func TestRuleUsesDeprecatedMode(t *testing.T) {
	if !RuleUsesDeprecatedMode(RuleConfig{Mode: "digest"}) {
		t.Fatal("expected deprecated mode")
	}
	if RuleUsesDeprecatedMode(RuleConfig{Track: "digest"}) {
		t.Fatal("track field is not deprecated")
	}
}

func TestValidateRulesMinAgeOnlyRuleAllowed(t *testing.T) {
	zero := Duration{}
	if err := validateRules([]RuleConfig{{Image: "nginx", MinAge: &zero}}); err != nil {
		t.Fatalf("min_age-only rule (0 = off for this image) must be valid: %v", err)
	}
	neg := Duration{Duration: -time.Hour}
	if err := validateRules([]RuleConfig{{Image: "nginx", MinAge: &neg}}); err == nil {
		t.Fatal("negative min_age must fail")
	}
}

func TestParseDurationDays(t *testing.T) {
	cases := map[string]time.Duration{
		"3d":    72 * time.Hour,
		"1d12h": 36 * time.Hour,
		"0d":    0,
		"36h":   36 * time.Hour,
		"90m":   90 * time.Minute,
	}
	for raw, want := range cases {
		got, err := ParseDuration(raw)
		if err != nil || got != want {
			t.Fatalf("ParseDuration(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	for _, bad := range []string{"d", "3x", "3dd", "-1d", "1d-"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Fatalf("ParseDuration(%q) should fail", bad)
		}
	}
}

func TestWatchesByDefault(t *testing.T) {
	f, tr := false, true
	for _, tc := range []struct {
		v    *bool
		want bool
	}{{nil, true}, {&tr, true}, {&f, false}} {
		if got := (&Config{WatchByDefault: tc.v}).WatchesByDefault(); got != tc.want {
			t.Fatalf("WatchByDefault=%v: got %v, want %v", tc.v, got, tc.want)
		}
	}
}

func TestLoadWatchByDefaultFalse(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	body := "provider: {type: docker}\nnotifiers: [{type: stdout}]\nwatch_by_default: false\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WatchesByDefault() {
		t.Fatal("watch_by_default: false not applied")
	}

	if err := os.WriteFile(path, []byte("provider: {type: docker}\nnotifiers: [{type: stdout}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = Load(path); err != nil || !cfg.WatchesByDefault() {
		t.Fatalf("unset watch_by_default must stay true (err=%v)", err)
	}
}

func TestLoadWatchContainers(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("provider: {type: docker}\nnotifiers: [{type: stdout}]\n"+body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("watch_by_default: false\nwatch_containers: [postgres, nextcloud]\n")
	cfg, err := Load(path)
	if err != nil || len(cfg.WatchContainers) != 2 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}

	write("watch_containers: [db]\nexclude_containers: [db]\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "both watch_containers and exclude_containers") {
		t.Fatalf("err = %v, want conflict error", err)
	}

	write("watch_containers: [\"\"]\n")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "watch_containers[0]") {
		t.Fatalf("err = %v, want empty-name error", err)
	}
}

func TestNotifierIDs(t *testing.T) {
	ids, err := NotifierIDs([]PluginConfig{
		{Type: "telegram"}, {Type: "discord"}, {Type: "telegram"}, {Type: "telegram", Name: "family"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ids, ","); got != "telegram,discord,telegram#2,family" {
		t.Fatalf("ids = %s", got)
	}
	if _, err := NotifierIDs([]PluginConfig{{Type: "telegram"}, {Type: "ntfy", Name: "telegram"}}); err == nil {
		t.Fatal("name clashing with a default ID must fail")
	}
	if _, err := NotifierIDs([]PluginConfig{{Type: "ntfy", Name: "x"}, {Type: "gotify", Name: " x "}}); err == nil {
		t.Fatal("duplicate names must fail")
	}
}

func TestValidateRulesExclude(t *testing.T) {
	if err := validateRules([]RuleConfig{{Image: "nginx", Exclude: `-(rc|beta)`}}); err != nil {
		t.Fatalf("exclude-only rule must be valid: %v", err)
	}
	if err := validateRules([]RuleConfig{{Image: "nginx", Exclude: `(`}}); err == nil || !strings.Contains(err.Error(), "invalid exclude regex") {
		t.Fatalf("err = %v, want invalid exclude regex", err)
	}
}

func TestJitterValidation(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	load := func(extra string) error {
		if err := os.WriteFile(path, []byte("provider: {type: docker}\nnotifiers: [{type: stdout}]\n"+extra), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		return err
	}
	if err := load("interval: 12h\njitter: 30m\n"); err != nil {
		t.Fatalf("valid jitter: %v", err)
	}
	if err := load("interval: 1h\njitter: 1h\n"); err == nil || !strings.Contains(err.Error(), "shorter than interval") {
		t.Fatalf("err = %v, want jitter < interval", err)
	}
	if err := load("schedule: \"0 11 * * *\"\ntimezone: UTC\njitter: 2h\n"); err != nil {
		t.Fatalf("cron + jitter is allowed: %v", err)
	}
	if err := load("jitter: -1m\n"); err == nil {
		t.Fatal("negative jitter must fail")
	}
}

func TestHeartbeatValidation(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yaml"
	load := func(extra string) error {
		if err := os.WriteFile(path, []byte("provider: {type: docker}\nnotifiers: [{type: stdout}]\n"+extra), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		return err
	}
	if err := load("heartbeat: {url: \"https://hc-ping.com/abc\", fail_url: \"https://hc-ping.com/abc/fail\"}\n"); err != nil {
		t.Fatalf("valid heartbeat: %v", err)
	}
	if err := load("heartbeat: {fail_url: \"https://hc-ping.com/abc/fail\"}\n"); err == nil || !strings.Contains(err.Error(), "fail_url requires url") {
		t.Fatalf("err = %v", err)
	}
	if err := load("heartbeat: {url: \"hc-ping.com/abc\"}\n"); err == nil || !strings.Contains(err.Error(), "http(s) URL") {
		t.Fatalf("err = %v", err)
	}

	// Env-injected URL is validated too (Load validates after overrides).
	t.Setenv(envHeartbeatURL, "ftp://nope")
	if err := load(""); err == nil {
		t.Fatal("invalid env heartbeat URL must fail")
	}
	t.Setenv(envHeartbeatURL, "https://push.example.com/api/push/tok")
	t.Setenv(envHeartbeatFailURL, "https://push.example.com/api/push/tok?status=down")
	if err := os.WriteFile(path, []byte("provider: {type: docker}\nnotifiers: [{type: stdout}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || cfg.Heartbeat.URL != "https://push.example.com/api/push/tok" || !strings.HasSuffix(cfg.Heartbeat.FailURL, "status=down") {
		t.Fatalf("cfg=%+v err=%v", cfg.Heartbeat, err)
	}
}
