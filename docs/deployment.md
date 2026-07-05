# Deployment

Versentry is designed as a **long-running daemon** on the same host as the containers it watches. Mount the Docker socket and persist notification state on a volume.

Back to [README](../README.md) · [Configuration overview](configuration.md)

## Quick deploy (Compose)

```bash
cp docker-compose.example.yml docker-compose.yml
cp config.example.yaml config.yaml
# Edit config.yaml, then set secrets in .env
echo 'VERSENTRY_TELEGRAM_TOKEN=123456:ABC-DEF' >> .env
echo 'VERSENTRY_TELEGRAM_CHAT_ID=123456789' >> .env
echo 'VERSENTRY_TELEGRAM_PROXY=socks5://user:pass@host:1080' >> .env  # optional
docker compose up -d
docker compose logs -f
```

Image: **`blackraincoat/versentry`** on Docker Hub (tags: `latest`, `1.0.0`, …). Alternative: **`ghcr.io/blackraincoat/versentry`**.

To build from source instead of pulling:

```bash
docker compose up -d --build
```

Versentry can live in the **same** `docker-compose.yml` as your other services, or in a separate stack (Portainer). Use `versentry.include` labels on other services for per-container tag rules — see [Rules](rules.md).

## Multi-arch build

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=1.0.0 --build-arg COMMIT=$(git rev-parse --short HEAD) \
  -t versentry:local .
```

## Container layout

| Path | Purpose |
|------|---------|
| `/var/run/docker.sock` | Read running containers (host mount, `:ro` recommended) |
| `/etc/versentry/config.yaml` | Your config (copy from `config.example.yaml`; bind-mount from host, `:ro`) |
| `/etc/versentry/hostname` | Optional: host hostname for notifications (bind mount from `/etc/hostname`) |
| `/data/state.json` | Notification state (path set by image env; internal, not user config) |
| `/data/health` | Daemon liveness stamp (heartbeat + after each successful `run` pass) |

Default image command: `versentry run -c /etc/versentry/config.yaml`.

`restart: unless-stopped` is recommended.

## Docker socket security

Access to `docker.sock` is equivalent to root on the host. Run Versentry only on hosts you trust.

## Environment secrets

Typical `.env` / compose `environment`:

| Variable | Purpose |
|----------|---------|
| `VERSENTRY_TELEGRAM_TOKEN` | Telegram bot token |
| `VERSENTRY_TELEGRAM_CHAT_ID` | Telegram chat id |
| `VERSENTRY_TELEGRAM_PROXY` | SOCKS5/HTTP proxy for Telegram only |
| `VERSENTRY_DISCORD_WEBHOOK_URL` | Discord incoming webhook URL |
| `VERSENTRY_WEBHOOK_URL` | Generic webhook endpoint |
| `VERSENTRY_WEBHOOK_AUTHORIZATION` | `Authorization` header for webhook |
| `VERSENTRY_WEBHOOK_PROXY` | Proxy for webhook HTTP client |
| `VERSENTRY_REGISTRY_USERNAME` | Username for configured `oci` registries |
| `VERSENTRY_REGISTRY_TOKEN` | Token/password for configured `oci` registries |
| `VERSENTRY_REGISTRY_PROXY` | Proxy for all registry API traffic |
| `VERSENTRY_INSTANCE_NAME` | Display name in notifications |
| `VERSENTRY_STATE_FILE` | Notification state path (image default: `/data/state.json`) |
| `TZ` | Container timezone (logs); fallback for cron if `timezone` unset in config — [details](configuration.md#tz-vs-timezone) |

Full list: [Configuration — environment variables](configuration.md#environment-variables).

## Timezone

Versentry has two timezone settings — see [Configuration — TZ vs timezone](configuration.md#tz-vs-timezone) for the full table.

| Setting | Where | Purpose |
|---------|-------|---------|
| `TZ` | compose `environment` | Container-wide: **log timestamps**, process local time |
| `timezone` | config YAML | **Cron only** (`schedule`); ignored when using `interval` |

**Typical Docker setup:** always set `TZ` (e.g. `Europe/Moscow`). Add `timezone` in config only when you use `schedule`; use the same zone as `TZ` unless you have a reason not to.

```yaml
environment:
  TZ: Europe/Moscow
```

## State volume

Mount a volume at `/data` so container restarts do not re-notify every pending update. The image sets `VERSENTRY_STATE_FILE=/data/state.json` — no `state_file` key in config unless you need a custom path.

```yaml
volumes:
  - versentry-state:/data
```

See [Configuration — notification state](configuration.md#notification-state-versentry-run).

## Healthcheck

Included in the image: verifies Docker socket reachability (`docker ping`, not a full container list) and a recent daemon liveness stamp (`/data/health` on the state volume). The stamp is refreshed on **startup**, on a **heartbeat** while `versentry run` is up (every 1–15 minutes), and after each successful scheduled pass.

The image defines `HEALTHCHECK` in the Dockerfile — you do not need a `healthcheck:` block in compose unless you want to override timing. The probe is `/usr/local/bin/health` (a small wrapper around `versentry health`). **Docker runs HEALTHCHECK directly; it does not prepend `ENTRYPOINT`**, so the probe must be an executable path, not the `health` subcommand alone.

```yaml
# Optional override (defaults come from the image):
healthcheck:
  test: ["CMD", "/usr/local/bin/health", "-c", "/etc/versentry/config.yaml"]
  interval: 30s
  timeout: 10s
  retries: 3
  start_period: 60s
```

For a snappier Portainer UI in dev, override `interval` / `start_period` (e.g. `10s` / `10s`).

Stamp behavior: [Configuration — health stamp](configuration.md#health-stamp).

## Signals

<a id="signals"></a>

Do **not** use `docker compose exec` for signals — use `docker kill` on the container.

```bash
docker kill --signal=SIGUSR1 versentry   # scheduled check now (respects state)
docker kill --signal=SIGUSR2 versentry   # full check now (like versentry check)
```

Bare process:

```bash
kill -USR1 <pid>
kill -USR2 <pid>
```

Signal semantics: [Configuration — signals](configuration.md#signals) · [Commands](commands.md).

## Version

```bash
versentry version
```

Prints build version (also available in the Docker image).
