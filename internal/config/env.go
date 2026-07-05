package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	envTelegramToken        = "VERSENTRY_TELEGRAM_TOKEN"
	envTelegramChatID       = "VERSENTRY_TELEGRAM_CHAT_ID"
	envTelegramProxy        = "VERSENTRY_TELEGRAM_PROXY"
	envDiscordWebhookURL    = "VERSENTRY_DISCORD_WEBHOOK_URL"
	envWebhookURL           = "VERSENTRY_WEBHOOK_URL"
	envWebhookAuthorization = "VERSENTRY_WEBHOOK_AUTHORIZATION"
	envWebhookProxy         = "VERSENTRY_WEBHOOK_PROXY"
	envInstanceName         = "VERSENTRY_INSTANCE_NAME"
	envRegistryProxy        = "VERSENTRY_REGISTRY_PROXY"
	envRegistryUsername     = "VERSENTRY_REGISTRY_USERNAME"
	envRegistryToken        = "VERSENTRY_REGISTRY_TOKEN"
	envStateFile            = "VERSENTRY_STATE_FILE"
)

// ApplyEnvOverrides merges environment variables into cfg.
// Env overrides YAML for the fields listed below (Docker-friendly secrets).
func ApplyEnvOverrides(cfg *Config) {
	applyInstanceNameEnv(cfg)
	applyStateFileEnv(cfg)

	if proxy := strings.TrimSpace(os.Getenv(envRegistryProxy)); proxy != "" {
		cfg.RegistryProxy = proxy
	}

	applyRegistryCredEnv(cfg)
	applyTelegramEnv(cfg)
	applyDiscordEnv(cfg)
	applyWebhookEnv(cfg)
}

func applyInstanceNameEnv(cfg *Config) {
	if name := strings.TrimSpace(os.Getenv(envInstanceName)); name != "" {
		cfg.InstanceName = name
	}
}

func applyStateFileEnv(cfg *Config) {
	if path := strings.TrimSpace(os.Getenv(envStateFile)); path != "" {
		cfg.StateFile = path
	}
}

func applyRegistryCredEnv(cfg *Config) {
	username := strings.TrimSpace(os.Getenv(envRegistryUsername))
	token := strings.TrimSpace(os.Getenv(envRegistryToken))
	if username == "" && token == "" {
		return
	}

	for i := range cfg.Registries {
		if cfg.Registries[i].Type != "oci" {
			continue
		}
		if cfg.Registries[i].Config == nil {
			cfg.Registries[i].Config = make(map[string]any)
		}
		if username != "" {
			cfg.Registries[i].Config["username"] = username
		}
		if token != "" {
			cfg.Registries[i].Config["token"] = token
		}
	}
}

func applyTelegramEnv(cfg *Config) {
	token := strings.TrimSpace(os.Getenv(envTelegramToken))
	chatID := strings.TrimSpace(os.Getenv(envTelegramChatID))
	proxy := strings.TrimSpace(os.Getenv(envTelegramProxy))
	if token == "" && chatID == "" && proxy == "" {
		return
	}

	for i := range cfg.Notifiers {
		if cfg.Notifiers[i].Type != "telegram" {
			continue
		}
		if cfg.Notifiers[i].Config == nil {
			cfg.Notifiers[i].Config = make(map[string]any)
		}
		if token != "" {
			cfg.Notifiers[i].Config["token"] = token
		}
		if chatID != "" {
			cfg.Notifiers[i].Config["chat_id"] = chatID
		}
		if proxy != "" {
			cfg.Notifiers[i].Config["proxy"] = proxy
		}
	}
}

func applyDiscordEnv(cfg *Config) {
	url := strings.TrimSpace(os.Getenv(envDiscordWebhookURL))
	if url == "" {
		return
	}

	for i := range cfg.Notifiers {
		if cfg.Notifiers[i].Type != "discord" {
			continue
		}
		if cfg.Notifiers[i].Config == nil {
			cfg.Notifiers[i].Config = make(map[string]any)
		}
		cfg.Notifiers[i].Config["url"] = url
	}
}

func applyWebhookEnv(cfg *Config) {
	url := strings.TrimSpace(os.Getenv(envWebhookURL))
	auth := strings.TrimSpace(os.Getenv(envWebhookAuthorization))
	proxy := strings.TrimSpace(os.Getenv(envWebhookProxy))
	if url == "" && auth == "" && proxy == "" {
		return
	}

	for i := range cfg.Notifiers {
		if cfg.Notifiers[i].Type != "webhook" {
			continue
		}
		if cfg.Notifiers[i].Config == nil {
			cfg.Notifiers[i].Config = make(map[string]any)
		}
		if url != "" {
			cfg.Notifiers[i].Config["url"] = url
		}
		if proxy != "" {
			cfg.Notifiers[i].Config["proxy"] = proxy
		}
		if auth != "" {
			headers, _ := cfg.Notifiers[i].Config["headers"].(map[string]any)
			if headers == nil {
				headers = make(map[string]any)
			}
			headers["Authorization"] = auth
			cfg.Notifiers[i].Config["headers"] = headers
		}
	}
}

// ScheduleLocation returns the timezone for cron scheduling.
// Uses config timezone first, then the TZ environment variable.
func (cfg *Config) ScheduleLocation() (*time.Location, error) {
	name := strings.TrimSpace(cfg.Timezone)
	if name == "" {
		name = strings.TrimSpace(os.Getenv("TZ"))
	}
	if name == "" {
		if cfg.Schedule != "" {
			return nil, fmt.Errorf("schedule requires timezone in config or TZ environment variable")
		}
		return time.Local, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", name, err)
	}
	return loc, nil
}
