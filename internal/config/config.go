package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BlackRaincoat/versentry/internal/imageref"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// PluginConfig names a plugin implementation and its opaque settings.
type PluginConfig struct {
	Type   string         `yaml:"type"`
	Config map[string]any `yaml:"config"`
}

// RuleConfig is a per-image tag filter / detection track from the Versentry config.
type RuleConfig struct {
	Image   string `yaml:"image"`
	Include string `yaml:"include"`
	Track   string `yaml:"track"` // optional; only "digest" is supported
	Mode    string `yaml:"mode"`  // deprecated alias for track (digest only)
}

// Config is the top-level Versentry configuration loaded from YAML.
type Config struct {
	Provider     PluginConfig   `yaml:"provider"`
	Registries   []PluginConfig `yaml:"registries"`
	Notifiers    []PluginConfig `yaml:"notifiers"`
	Timeouts     Timeouts       `yaml:"timeouts"`
	Interval     Duration       `yaml:"interval"`
	Schedule     string         `yaml:"schedule"`
	StateFile    string         `yaml:"state_file"`
	LogLevel     string         `yaml:"log_level"`
	Rules        []RuleConfig   `yaml:"rules"`
	InstanceName  string         `yaml:"instance_name"`
	Timezone      string         `yaml:"timezone"`
	RegistryProxy string         `yaml:"registry_proxy"`
	HealthMaxAge  Duration       `yaml:"health_max_age"`
}

var scheduleParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

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
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", raw, err)
	}
	d.Duration = parsed
	return nil
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
	if err := validateRules(cfg.Rules); err != nil {
		return nil, err
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

	return cfg, nil
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
		if rule.Include == "" && effective != "digest" {
			return fmt.Errorf("rules[%d]: include is required unless track is digest", i)
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
