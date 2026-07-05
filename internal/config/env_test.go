package config

import (
	"testing"
)

func TestApplyEnvOverridesTelegram(t *testing.T) {
	t.Setenv(envTelegramToken, "env-token")
	t.Setenv(envTelegramChatID, "env-chat")

	cfg := &Config{
		Notifiers: []PluginConfig{
			{Type: "stdout"},
			{
				Type: "telegram",
				Config: map[string]any{
					"token":   "yaml-token",
					"chat_id": "yaml-chat",
				},
			},
		},
	}
	ApplyEnvOverrides(cfg)

	tg := cfg.Notifiers[1].Config
	if tg["token"] != "env-token" {
		t.Fatalf("token = %v, want env-token", tg["token"])
	}
	if tg["chat_id"] != "env-chat" {
		t.Fatalf("chat_id = %v, want env-chat", tg["chat_id"])
	}
}

func TestApplyEnvOverridesTelegramProxy(t *testing.T) {
	t.Setenv(envTelegramProxy, "socks5://user:pass@127.0.0.1:1080")

	cfg := &Config{
		Notifiers: []PluginConfig{{
			Type:   "telegram",
			Config: map[string]any{"token": "t", "chat_id": "1"},
		}},
	}
	ApplyEnvOverrides(cfg)

	if cfg.Notifiers[0].Config["proxy"] != "socks5://user:pass@127.0.0.1:1080" {
		t.Fatalf("proxy = %v", cfg.Notifiers[0].Config["proxy"])
	}
}

func TestApplyEnvOverridesRegistryProxy(t *testing.T) {
	t.Setenv(envRegistryProxy, "http://proxy.local:3128")

	cfg := &Config{RegistryProxy: "yaml-proxy"}
	ApplyEnvOverrides(cfg)

	if cfg.RegistryProxy != "http://proxy.local:3128" {
		t.Fatalf("registry_proxy = %q", cfg.RegistryProxy)
	}
}

func TestApplyEnvOverridesRegistryCreds(t *testing.T) {
	t.Setenv(envRegistryUsername, "env-user")
	t.Setenv(envRegistryToken, "env-token")

	cfg := &Config{
		Registries: []PluginConfig{
			{Type: "oci", Config: map[string]any{
				"host":     "git.example.com",
				"username": "yaml-user",
				"token":    "yaml-token",
			}},
		},
	}
	ApplyEnvOverrides(cfg)

	reg := cfg.Registries[0].Config
	if reg["username"] != "env-user" || reg["token"] != "env-token" {
		t.Fatalf("creds = %v/%v", reg["username"], reg["token"])
	}
}

func TestApplyEnvOverridesDiscordURL(t *testing.T) {
	t.Setenv(envDiscordWebhookURL, "https://discord.com/api/webhooks/1/token")

	cfg := &Config{
		Notifiers: []PluginConfig{{
			Type:   "discord",
			Config: map[string]any{"url": "https://discord.com/api/webhooks/9/old"},
		}},
	}
	ApplyEnvOverrides(cfg)

	if cfg.Notifiers[0].Config["url"] != "https://discord.com/api/webhooks/1/token" {
		t.Fatalf("url = %v", cfg.Notifiers[0].Config["url"])
	}
}

func TestApplyEnvOverridesWebhook(t *testing.T) {
	t.Setenv(envWebhookURL, "https://hooks.example.com/v1")
	t.Setenv(envWebhookAuthorization, "Bearer secret")
	t.Setenv(envWebhookProxy, "http://proxy:3128")

	cfg := &Config{
		Notifiers: []PluginConfig{{
			Type: "webhook",
			Config: map[string]any{
				"url": "https://hooks.example.com/old",
				"headers": map[string]any{
					"Authorization": "Bearer yaml",
					"X-Custom":      "keep",
				},
			},
		}},
	}
	ApplyEnvOverrides(cfg)

	wh := cfg.Notifiers[0].Config
	if wh["url"] != "https://hooks.example.com/v1" {
		t.Fatalf("url = %v", wh["url"])
	}
	if wh["proxy"] != "http://proxy:3128" {
		t.Fatalf("proxy = %v", wh["proxy"])
	}
	headers := wh["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer secret" {
		t.Fatalf("authorization = %v", headers["Authorization"])
	}
	if headers["X-Custom"] != "keep" {
		t.Fatalf("custom header = %v", headers["X-Custom"])
	}
}

func TestScheduleLocationRequiresTimezone(t *testing.T) {
	t.Setenv("TZ", "")
	cfg := &Config{Schedule: "0 3 * * *"}
	if _, err := cfg.ScheduleLocation(); err == nil {
		t.Fatal("expected error without timezone")
	}
}

func TestScheduleLocationFromConfig(t *testing.T) {
	t.Setenv("TZ", "")
	cfg := &Config{
		Schedule: "0 3 * * *",
		Timezone: "Europe/Paris",
	}
	loc, err := cfg.ScheduleLocation()
	if err != nil {
		t.Fatal(err)
	}
	if loc.String() != "Europe/Paris" {
		t.Fatalf("location = %s", loc)
	}
}

func TestScheduleLocationFromTZEnv(t *testing.T) {
	t.Setenv("TZ", "UTC")
	cfg := &Config{Schedule: "0 3 * * *"}
	loc, err := cfg.ScheduleLocation()
	if err != nil {
		t.Fatal(err)
	}
	if loc.String() != "UTC" {
		t.Fatalf("location = %s", loc)
	}
}

func TestApplyEnvOverridesInstanceName(t *testing.T) {
	t.Setenv(envInstanceName, "prod-docker")
	cfg := &Config{InstanceName: ""}
	ApplyEnvOverrides(cfg)
	if cfg.InstanceName != "prod-docker" {
		t.Fatalf("got %q", cfg.InstanceName)
	}
}

func TestApplyEnvOverridesInstanceNameEnvWins(t *testing.T) {
	t.Setenv(envInstanceName, "from-env")
	cfg := &Config{InstanceName: "from-yaml"}
	ApplyEnvOverrides(cfg)
	if cfg.InstanceName != "from-env" {
		t.Fatalf("env should win, got %q", cfg.InstanceName)
	}
}

func TestApplyEnvOverridesStateFile(t *testing.T) {
	t.Setenv(envStateFile, "/data/state.json")
	cfg := &Config{StateFile: "/etc/versentry/state.json"}
	ApplyEnvOverrides(cfg)
	if cfg.StateFile != "/data/state.json" {
		t.Fatalf("got %q", cfg.StateFile)
	}
}
