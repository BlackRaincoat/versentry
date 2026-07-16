# Versentry

Monitor Docker image updates and get notified — without pulling or restarting containers. Versentry compares running containers against OCI registries (semver tags and digest drift on floating tags) and sends alerts through Telegram, Discord, Gotify, ntfy, webhooks, or stdout. Notify-only by design.

Works on self-hosted hosts; Telegram notifier traffic can use a SOCKS5 or HTTP proxy.

## Features

- **Tag filtering** — central `rules:` in config and per-container `versentry.include` labels
- **Any OCI registry** — public hosts (Docker Hub, GHCR, Quay, GitLab) work out of the box; private and self-hosted via `type: oci`
- **Notifications** — Telegram, Discord, Gotify, ntfy, generic webhook, stdout; optional proxy on HTTP notifiers
- **Delivery modes** — `simple` or `digest`; Go `text/template` overrides where supported
- **Detection** — semver (same-major) and digest comparison for non-semver tags (`latest`, …)
- **Notify-only** — never modifies containers or images on the host
- **Scheduling** — fixed `interval` or cron `schedule` with timezone
- **Resilience** — HTTP retries on notifiers; per-pass registry dedup and rate-limit handling

## Quick start

Published image (Docker Hub):

```bash
docker pull blackraincoat/versentry:latest
```

Also on GHCR: `ghcr.io/blackraincoat/versentry`.

Minimal stack with Telegram (credentials via environment):

```bash
cp docker-compose.example.yml docker-compose.yml
cp config.example.yaml config.yaml
# Edit config.yaml (notifiers, schedule, …)
echo 'VERSENTRY_TELEGRAM_TOKEN=123456:ABC-DEF' >> .env
echo 'VERSENTRY_TELEGRAM_CHAT_ID=123456789' >> .env
docker compose up -d
docker compose logs -f
```

One-off check without the daemon:

```bash
export VERSENTRY_TELEGRAM_TOKEN="..."
export VERSENTRY_TELEGRAM_CHAT_ID="..."
cp config.example.yaml config.yaml
go run ./cmd/versentry check
```

Requires read access to the Docker socket and network reachability to registries (and to your notifier endpoints / proxy if configured).

## Documentation

| Guide | Contents |
|-------|----------|
| [Configuration overview](docs/configuration.md) | Config file layout, provider, timeouts, scheduling, state, env vars, container scope |
| [Rules](docs/rules.md) | Config rules, labels, semver vs digest, regex escaping |
| [Registries](docs/registries.md) | Public hosts, private OCI, `registry_proxy`, rate limits |
| [Notifications](docs/notifications.md) | Telegram, Discord, webhook, templates, examples |
| [Deployment](docs/deployment.md) | Docker image, compose, volumes, healthcheck, signals |
| [Commands](docs/commands.md) | `check`, `run`, `links`, `health`, flags, state behavior |

Full annotated example: [`config.example.yaml`](config.example.yaml).

Not sure Versentry is what you need? See [When to use Versentry](docs/comparison.md).

## License

MIT
