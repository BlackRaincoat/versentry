package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// PluginConfig names a plugin implementation and its opaque settings.
type PluginConfig struct {
	Type string `yaml:"type"`
	// Name identifies a notifier in logs and per-notifier delivery state.
	// Optional; see NotifierIDs for the default.
	Name   string         `yaml:"name"`
	Config map[string]any `yaml:"config"`
}

// RuleConfig is a per-image tag filter / detection track from the Versentry config.
type RuleConfig struct {
	Image   string `yaml:"image"`
	Include string `yaml:"include"`
	// Exclude drops registry tags matching this regex from the candidates
	// (after include, if both are set).
	Exclude string `yaml:"exclude"`
	Track   string `yaml:"track"` // optional; only "digest" is supported
	Mode    string `yaml:"mode"`  // deprecated alias for track (digest only)
	// MinAge overrides the global min_age for this image; nil = inherit, 0 = off.
	MinAge *Duration `yaml:"min_age"`
}

// Config is the top-level Versentry configuration loaded from YAML.
type Config struct {
	Provider          PluginConfig   `yaml:"provider"`
	Registries        []PluginConfig `yaml:"registries"`
	Notifiers         []PluginConfig `yaml:"notifiers"`
	Timeouts          Timeouts       `yaml:"timeouts"`
	Interval          Duration       `yaml:"interval"`
	Schedule          string         `yaml:"schedule"`
	StateFile         string         `yaml:"state_file"`
	LogLevel          string         `yaml:"log_level"`
	Rules             []RuleConfig   `yaml:"rules"`
	ExcludeContainers []string       `yaml:"exclude_containers"`
	InstanceName      string         `yaml:"instance_name"`
	Timezone          string         `yaml:"timezone"`
	RegistryProxy     string         `yaml:"registry_proxy"`
	HealthMaxAge      Duration       `yaml:"health_max_age"`
	// MinAge delays notifications until the candidate image is this old in the
	// registry (config blob created). 0 = off.
	MinAge Duration `yaml:"min_age"`
	// WatchByDefault: nil/true = check every container not opted out (default);
	// false = opt-in, only containers labeled versentry.watch=true.
	WatchByDefault *bool `yaml:"watch_by_default"`
	// WatchContainers: exact names checked in opt-in mode (in addition to the
	// versentry.watch=true label). Ignored (WARN) while watch_by_default is true.
	WatchContainers []string `yaml:"watch_containers"`
	// Heartbeat pings an external dead-man's-switch (Healthchecks.io, Uptime
	// Kuma push, …) after each scheduled check. Empty URL = off.
	Heartbeat HeartbeatConfig `yaml:"heartbeat"`
	// Jitter delays each scheduled check by a random duration in [0, Jitter)
	// (run only; initial check and signals are not delayed). 0 = off.
	Jitter Duration `yaml:"jitter"`
	// RemindAfter re-sends a still-pending update after this long (run only). 0 = off.
	RemindAfter Duration `yaml:"remind_after"`
}

var scheduleParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// HeartbeatConfig is the optional external liveness ping.
type HeartbeatConfig struct {
	URL     string   `yaml:"url"`      // pinged after a successful scheduled check
	FailURL string   `yaml:"fail_url"` // pinged when a check fails; empty = no ping (the missed ping alerts)
	Proxy   string   `yaml:"proxy"`
	Timeout Duration `yaml:"timeout"`
}

// Timeouts holds operation timeouts applied by the core engine.
type Timeouts struct {
	Provider Duration `yaml:"provider"`
	Registry Duration `yaml:"registry"`
}

// Duration wraps time.Duration for YAML parsing.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return err
	}
	parsed, err := ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", raw, err)
	}
	d.Duration = parsed
	return nil
}

// ParseDuration is time.ParseDuration plus a leading day count ("3d", "1d12h").
// Days are 24h; the rest must be a Go duration.
func ParseDuration(raw string) (time.Duration, error) {
	s := strings.TrimSpace(raw)
	i := strings.IndexByte(s, 'd')
	if i <= 0 {
		return time.ParseDuration(s)
	}
	days, err := strconv.Atoi(s[:i])
	if err != nil {
		return time.ParseDuration(s) // let Go report the error for the whole string
	}
	if days < 0 {
		return 0, fmt.Errorf("negative day count in %q", raw)
	}
	total := time.Duration(days) * 24 * time.Hour
	if rest := s[i+1:]; rest != "" {
		extra, err := time.ParseDuration(rest)
		if err != nil {
			return 0, err
		}
		total += extra
	}
	return total, nil
}

// Load reads and parses a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{
		Timeouts: Timeouts{
			Provider: Duration{10 * time.Second},
			Registry: Duration{30 * time.Second},
		},
		Interval: Duration{time.Hour},
		LogLevel: "info",
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Provider.Type == "" {
		return nil, fmt.Errorf("provider.type is required")
	}
	// registries is optional: public hosts (Docker Hub, GHCR, Quay, GitLab)
	// are registered automatically; config is only for private/self-hosted oci.
	if len(cfg.Notifiers) == 0 {
		return nil, fmt.Errorf("at least one notifier is required")
	}
	if _, err := NotifierIDs(cfg.Notifiers); err != nil {
		return nil, err
	}
	if err := validateRules(cfg.Rules); err != nil {
		return nil, err
	}
	if cfg.MinAge.Duration < 0 {
		return nil, fmt.Errorf("min_age must not be negative")
	}
	if cfg.RemindAfter.Duration < 0 {
		return nil, fmt.Errorf("remind_after must not be negative")
	}
	if cfg.Jitter.Duration < 0 {
		return nil, fmt.Errorf("jitter must not be negative")
	}
	if cfg.Schedule == "" && cfg.Jitter.Duration >= cfg.Interval.Duration {
		return nil, fmt.Errorf("jitter (%s) must be shorter than interval (%s)", cfg.Jitter.Duration, cfg.Interval.Duration)
	}
	exclude, _, err := ExcludeContainerSet(cfg.ExcludeContainers)
	if err != nil {
		return nil, err
	}
	watch, _, err := WatchContainerSet(cfg.WatchContainers)
	if err != nil {
		return nil, err
	}
	for name := range watch {
		if _, ok := exclude[name]; ok {
			return nil, fmt.Errorf("container %q is in both watch_containers and exclude_containers", name)
		}
	}
	if cfg.Schedule != "" {
		if _, err := scheduleParser.Parse(cfg.Schedule); err != nil {
			return nil, fmt.Errorf("invalid schedule %q: %w", cfg.Schedule, err)
		}
		if _, err := cfg.ScheduleLocation(); err != nil {
			return nil, err
		}
	}

	ApplyEnvOverrides(cfg)
	// After env: heartbeat URLs are usually injected as secrets.
	if err := validateHeartbeat(cfg.Heartbeat); err != nil {
		return nil, err
	}

	return cfg, nil
}

// WatchesByDefault reports the effective watch_by_default (true when unset).
func (c *Config) WatchesByDefault() bool {
	return c.WatchByDefault == nil || *c.WatchByDefault
}

// NotifierIDs returns a stable ID per notifier: its name when set, else its
// type for the first notifier of that type and type#2, type#3, … for later
// ones (adding a second telegram keeps the first one's ID). IDs key the
// per-notifier delivery state, so they must be unique.
func NotifierIDs(notifiers []PluginConfig) ([]string, error) {
	ids := make([]string, len(notifiers))
	seenType := make(map[string]int)
	used := make(map[string]int)
	for i, n := range notifiers {
		id := strings.TrimSpace(n.Name)
		if id == "" {
			seenType[n.Type]++
			id = n.Type
			if c := seenType[n.Type]; c > 1 {
				id = fmt.Sprintf("%s#%d", n.Type, c)
			}
		}
		if prev, dup := used[id]; dup {
			return nil, fmt.Errorf("notifiers[%d]: name %q already used by notifiers[%d]; set a unique name", i, id, prev)
		}
		used[id] = i
		ids[i] = id
	}
	return ids, nil
}

func validateHeartbeat(h HeartbeatConfig) error {
	if h.URL == "" && h.FailURL != "" {
		return fmt.Errorf("heartbeat: fail_url requires url")
	}
	for field, raw := range map[string]string{"url": h.URL, "fail_url": h.FailURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("heartbeat: %s must be an http(s) URL", field)
		}
	}
	if h.Timeout.Duration < 0 {
		return fmt.Errorf("heartbeat: timeout must not be negative")
	}
	return nil
}

// ResolveStatePath returns the path to the notification state file.
// When state_file is empty, the default is state.json next to the config file.
func ResolveStatePath(configPath, stateFile string) string {
	if stateFile != "" {
		return stateFile
	}
	dir := filepath.Dir(configPath)
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "state.json")
}

func validateRules(rules []RuleConfig) error {
	seen := make(map[string]int, len(rules))
	for i, rule := range rules {
		if rule.Image == "" {
			return fmt.Errorf("rules[%d]: image is required", i)
		}
		track := strings.TrimSpace(rule.Track)
		mode := strings.TrimSpace(rule.Mode)
		if track != "" && mode != "" {
			return fmt.Errorf("rules[%d]: both mode and track are set; use track only (image=%s)", i, rule.Image)
		}
		effective := EffectiveRuleTrack(rule)
		if effective != "" && effective != "digest" {
			return fmt.Errorf("rules[%d]: unknown track %q, only \"digest\" supported", i, effective)
		}
		if rule.Include == "" && rule.Exclude == "" && effective != "digest" && rule.MinAge == nil {
			return fmt.Errorf("rules[%d]: include, exclude, track: digest, or min_age is required", i)
		}
		if rule.Exclude != "" {
			if _, err := regexp.Compile(rule.Exclude); err != nil {
				return fmt.Errorf("rules[%d]: invalid exclude regex: %w", i, err)
			}
		}
		if rule.MinAge != nil && rule.MinAge.Duration < 0 {
			return fmt.Errorf("rules[%d]: min_age must not be negative", i)
		}
		if rule.Include != "" {
			if _, err := regexp.Compile(rule.Include); err != nil {
				return fmt.Errorf("rules[%d]: invalid include regex: %w", i, err)
			}
		}
		for _, key := range imageref.RuleConfigKeys(rule.Image) {
			if prev, ok := seen[key]; ok {
				return fmt.Errorf("rules[%d]: image %q conflicts with rules[%d] image %q", i, rule.Image, prev, rules[prev].Image)
			}
			seen[key] = i
		}
	}
	return nil
}

// EffectiveRuleTrack returns the configured detection track (track, or deprecated mode alias).
func EffectiveRuleTrack(rule RuleConfig) string {
	if t := strings.TrimSpace(rule.Track); t != "" {
		return t
	}
	return strings.TrimSpace(rule.Mode)
}

// RuleUsesDeprecatedMode reports whether the rule uses the deprecated mode field instead of track.
func RuleUsesDeprecatedMode(rule RuleConfig) bool {
	return strings.TrimSpace(rule.Track) == "" && strings.TrimSpace(rule.Mode) != ""
}

// ExcludeContainerSet builds a set of exact Docker container names to exclude from monitoring.
// Names are trimmed. Empty entries are an error. Duplicate names are kept once; the second
// return value lists unique duplicates (for optional WARN — not a config error).
func ExcludeContainerSet(names []string) (map[string]struct{}, []string, error) {
	return containerNameSet("exclude_containers", names)
}

// WatchContainerSet builds the watch_containers set (opt-in by name); same rules as
// ExcludeContainerSet.
func WatchContainerSet(names []string) (map[string]struct{}, []string, error) {
	return containerNameSet("watch_containers", names)
}

func containerNameSet(field string, names []string) (map[string]struct{}, []string, error) {
	if len(names) == 0 {
		return nil, nil, nil
	}
	out := make(map[string]struct{}, len(names))
	var dups []string
	dupSeen := make(map[string]struct{})
	for i, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, nil, fmt.Errorf("%s[%d]: name is required", field, i)
		}
		if _, ok := out[name]; ok {
			if _, logged := dupSeen[name]; !logged {
				dups = append(dups, name)
				dupSeen[name] = struct{}{}
			}
			continue
		}
		out[name] = struct{}{}
	}
	return out, dups, nil
}
