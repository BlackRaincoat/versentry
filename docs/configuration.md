# Configuration overview

Versentry reads a YAML config file (default `config.yaml`, override with `-c`). The repository ships one annotated template: [`config.example.yaml`](../config.example.yaml).

Back to [README](../README.md).

## Config file

There is a single example in the repo. **Copy it before the first run** — compose and the CLI expect your own `config.yaml`, not the example file directly.

```bash
cp config.example.yaml config.yaml
# edit config.yaml (notifiers, interval or schedule, rules, …)
```

| Setup | Config path | Notes |
|-------|-------------|--------|
| **Docker Compose** (example stack) | `./config.yaml` → `/etc/versentry/config.yaml` | See [`docker-compose.example.yml`](../docker-compose.example.yml) |
| **Docker / Portainer** (custom) | e.g. `/opt/versentry/config.yaml` on the host | Same idea: your file, bind-mounted read-only |
| **Bare metal / `go run`** | `config.yaml` in the working directory | Or any path via `-c` |

Secrets (Telegram token, webhook URLs, registry credentials) go in **environment variables** (`VERSENTRY_*`), not in the YAML you commit or share.

## Top-level fields

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `provider` | yes | — | Container source |
| `notifiers` | yes (≥1) | — | Notification channels — see [Notifications](notifications.md) |
| `registries` | no | auto public hosts | Extra / private OCI registries — see [Registries](registries.md) |
| `rules` | no | — | Per-image tag filters — see [Rules](rules.md) |
| `instance_name` | no | see below | Name shown in notification headers |
| `timeouts.provider` | no | `10s` | Docker API timeout |
| `timeouts.registry` | no | `30s` | Registry API timeout |
| `interval` | no | `1h` | Period for `versentry run` when `schedule` is empty |
| `schedule` | no | — | Cron expression (5 fields) for `versentry run`; overrides `interval` |
| `timezone` | when `schedule` | see below | Cron only — see [TZ vs timezone](#tz-vs-timezone) |
| `state_file` | no | `{config-dir}/state.json` | Internal notification state for `versentry run` — override only for custom layouts |
| `registry_proxy` | no | — | HTTP/SOCKS5 proxy for all registry API calls — see [Registries](registries.md) |
| `health_max_age` | no | see below | Max age of health stamp for `versentry health` |
| `log_level` | no | `info` | `debug` / `info` / `warn` / `error` |

### Example skeleton

```yaml
instance_name: "prod-docker-01"   # optional

provider:
  type: docker

notifiers:
  - type: stdout

timeouts:
  provider: 10s
  registry: 30s

interval: 12h
#schedule: "0 12 * * *"
#timezone: Europe/Paris
#state_file: "/data/state.json"   # rarely needed; Docker image sets path via env
log_level: info
```

## Provider

Only `type: docker` is implemented. Reads running containers from the local Docker socket.

```yaml
provider:
  type: docker
  config:
    #socket: "/var/run/docker.sock"   # optional; default from env / Docker defaults
```

## Registries

Public registries work without configuration. Use `registries:` for private or self-hosted hosts and credentials. Details: [Registries](registries.md).

## Rules

Optional per-image tag include filters (config and/or container labels). Details: [Rules](rules.md).

## Notifiers

At least one notifier is required. Types: `stdout`, `telegram`, `webhook`, `discord`. Details: [Notifications](notifications.md).

Use **one entry per channel** (e.g. `stdout` + `telegram`, or `telegram` + `discord`). The list is not meant for multiple Telegram bots with different credentials — use a single `type: telegram` block and set secrets via env.

Default `mode` and template keys differ by notifier (`telegram`: `item_template`/`digest_template`; `discord`/`webhook`: optional full-body `template`). All notifiers default to **`digest`**. See [Notifications — defaults by channel](notifications.md#defaults-by-channel).

`instance_name` from the top-level config (or hostname / env) is injected into every notifier at startup.

## Environment variables

YAML holds structure and non-secret options. **Env overrides YAML** for sensitive fields (typical in Docker). All Versentry-specific variables use the `VERSENTRY_` prefix to avoid clashes with `TZ`, `HTTP_PROXY`, and other services in the same compose stack.

| Variable | Overrides |
|----------|-----------|
| `VERSENTRY_TELEGRAM_TOKEN` | `token` on the `telegram` notifier |
| `VERSENTRY_TELEGRAM_CHAT_ID` | `chat_id` on the `telegram` notifier |
| `VERSENTRY_TELEGRAM_PROXY` | `proxy` on the `telegram` notifier (`socks5://` or `http://`) |
| `VERSENTRY_DISCORD_WEBHOOK_URL` | `url` on the `discord` notifier |
| `VERSENTRY_WEBHOOK_URL` | `url` on the `webhook` notifier |
| `VERSENTRY_WEBHOOK_AUTHORIZATION` | `Authorization` header on the `webhook` notifier |
| `VERSENTRY_WEBHOOK_PROXY` | `proxy` on the `webhook` notifier |
| `VERSENTRY_REGISTRY_USERNAME` | `username` on every configured `oci` registry |
| `VERSENTRY_REGISTRY_TOKEN` | `token` on every configured `oci` registry |
| `VERSENTRY_REGISTRY_PROXY` | `registry_proxy` (HTTP or `socks5://` for all registries) |
| `VERSENTRY_INSTANCE_NAME` | top-level `instance_name` |
| `VERSENTRY_STATE_FILE` | top-level `state_file` (Docker image default: `/data/state.json`) |

**Override rule (`VERSENTRY_*` only):** when a `VERSENTRY_*` variable is set and non-empty, it replaces the YAML value. Unset env leaves YAML as-is.

`TZ` is **not** a `VERSENTRY_*` override — see [TZ vs timezone](#tz-vs-timezone).

**Instance name in Docker:** `os.Hostname()` inside a container is often the container id (e.g. `8145fc52b5fd`). Set `VERSENTRY_INSTANCE_NAME`, `instance_name` in YAML (overridden by env), or bind-mount the host hostname:

```yaml
volumes:
  - /etc/hostname:/etc/versentry/hostname:ro
```

**Instance name priority:** `VERSENTRY_INSTANCE_NAME` → `instance_name` in YAML → bind-mounted `/etc/versentry/hostname` → `os.Hostname()` → `versentry` when the hostname looks like a container id.

**Bind-mount footgun:** the image ships an empty placeholder at `/etc/versentry/hostname` so Docker mounts the host file correctly. Without it, `- /etc/hostname:/etc/versentry/hostname:ro` often creates a **directory** instead of a file, and the name is not read.

**Where it appears:** **simple** mode uses the same layout as digest — instance on its own line (`📦 prod-docker-01`), then the container update on the next line(s). **Digest** with multiple updates keeps one instance header for the whole batch (`📦 prod-docker-01 — N updates`). Registry host (`index.docker.io`, …) is a separate field and is not in default Telegram/Discord templates.

## Container scope (opt-out)

By default **all running containers** are checked. Set label `versentry.watch=false` to exclude a container (`wud.watch` → `versentry.watch` when migrating).

| Label | Behavior |
|-------|----------|
| absent | monitored |
| `versentry.watch=true` (or `1`, `t`, …) | monitored |
| `versentry.watch=false` (or `0`, `f`, …) | excluded |

Invalid values (`yes`, `on`, …) log **WARN** and are treated as absent (monitored).

Excluded containers do not appear in check results (not counted as skipped). Summary logs include `excluded=N`.

**State / re-enable:** excluded containers are not part of the active fleet for notification state. Their image key is pruned from state on the next pass. If you remove the label or set `versentry.watch=true` again, Versentry may **notify again** for updates that were never applied while the container was excluded.

```yaml
services:
  chatwoot-sidekiq:
    image: chatwoot/chatwoot:latest
    labels:
      versentry.watch: "false"
```

## Notification state (`versentry run`)

`versentry run` keeps a JSON state file to avoid sending the same update repeatedly. `versentry check` is always stateless.

| Mode | Reads state | Writes state | Notifies |
|------|-------------|--------------|----------|
| `check` | no | no | all updates found |
| `run` (scheduled / SIGUSR1) | yes | yes (after successful delivery) | only new updates |
| `run` (SIGUSR2 force-check) | no | no | all updates found (like `check`) |

State key: `{registry-host}/{repo}` (e.g. `index.docker.io/library/nginx`). Value: last notified target — semver `LatestTag` or normalized remote digest.

- Missing state file → all current updates are treated as new.
- Corrupt state file at startup → WARN, empty state (monitoring continues; may re-notify once).
- Stale entries (images no longer running) are pruned on each state-updating pass.
- Empty container list after successful list → prune skipped (transient glitch protection).
- `ListRunning` error → state not touched.
- Tracking mode change (digest ↔ semver) → treated as a new target (may notify once).
- State is saved only after **all** notifiers succeed. Atomic write: temp file + rename.

**Docker:** mount a volume at `/data`. The image sets `VERSENTRY_STATE_FILE=/data/state.json` — you do not need `state_file` in YAML unless you use a non-standard layout:

```yaml
volumes:
  - versentry-state:/data
```

See also [Commands](commands.md) and [Deployment](deployment.md).

## Scheduling (`versentry run`)

Use either `interval` (simple ticker, default `1h`) or `schedule` (cron, 5 fields: minute hour day-of-month month day-of-week). When `schedule` is set, `interval` is ignored.

Invalid cron → fail at startup.

| Expression | Meaning |
|------------|---------|
| `0 12 * * *` | Every day at 12:00 |
| `*/30 * * * *` | Every 30 minutes |
| `0 */6 * * *` | Every 6 hours on the hour |

**Startup:** When the state file does not exist yet, `versentry run` performs one immediate check, then follows `interval` or cron `schedule`. Restarts with an existing state file wait for the next tick or slot. Use `versentry check`, `SIGUSR1`, or `SIGUSR2` for ad-hoc checks anytime.

### TZ vs timezone

Versentry uses **two different timezone knobs**. They are not duplicates and do not follow the `VERSENTRY_*` env-overrides-YAML rule.

| | `TZ` (environment) | `timezone` (YAML config) |
|--|-------------------|--------------------------|
| **Scope** | Whole container / process (standard Unix env) | Cron scheduling in `versentry run` only |
| **Affects** | Log timestamps, Go `time.Local`, libc | When `schedule: "0 12 * * *"` fires (which 12:00) |
| **Used with `interval` only?** | Yes (logs only) | No — ignored without `schedule` |
| **Precedence** | Process default | For cron: `timezone` in config, then `TZ` if `timezone` is empty |

**Cron requires a timezone:** set `timezone` in config (e.g. `Europe/Paris`) **or** `TZ` in the environment. Startup fails if `schedule` is set and neither resolves to a valid IANA zone.

**Docker recommendation:** set `TZ` in compose for readable logs. Add `timezone` in config when you use `schedule` and want the cron zone documented in the config file (or when `TZ` is unset). If both are set, cron follows `timezone`; logs still follow `TZ` — keep them aligned (e.g. both `Europe/Moscow`) to avoid confusion.

```yaml
# compose.yml
environment:
  TZ: Europe/Moscow

# config.yaml (only when using schedule)
schedule: "0 12 * * *"
timezone: Europe/Moscow
```

With **`interval` only** (no `schedule`), `timezone` in config is unused; `TZ` still controls log timestamps.

## Registry behavior (engine)

**Per-pass cache:** each check pass deduplicates `ListTags` / `TagDigest` by `host/repo` (digest mode: `host/repo#tag`). Multiple containers on the same image hit the registry once per pass. Cache resets between passes.

**Rate limits (HTTP 429):** one short `Retry-After` retry (≤10s) per request; if still rate-limited or `Retry-After` is long/missing, the host is skipped for the rest of the pass (`registry rate limited, will retry next pass`). Other registry hosts are unaffected. Persistent 5xx after transport retries skips the image only.

Details: [Registries](registries.md).

## Health stamp

`versentry run` writes `{state-dir}/health` on startup, on a periodic heartbeat while the daemon runs, and after each successful scheduled pass. `versentry health` fails if the stamp is missing or older than `2×interval` (ticker mode), `2×cron interval` derived from `schedule` (cron mode), or `health_max_age` when set (always wins). With cron scheduling the heartbeat keeps the stamp fresh between daily checks so Docker HEALTHCHECK stays green while the process is up.

See [Deployment](deployment.md) for Docker HEALTHCHECK.

## Notifier failures

A notifier failure (e.g. bad Telegram token) logs **ERROR** but **does not stop** `versentry run` — the next scheduled check still runs. Notification state is updated only after **all** notifiers succeed for that pass.

## How checks work

1. List running containers (Docker socket); skip containers with `versentry.watch=false`.
2. Parse image ref → registry host + repo + tag.
3. **Semver tag:** list tags → apply rule filter if any → newest same-major tag → notify if newer.
4. **Non-semver tag** (`latest`, …): compare local manifest digest vs registry tag digest.
5. Batch all updates from the pass → notifiers (`run` may filter already-notified targets before step 5).

In `run` mode, detection always logs every update at INFO; only notifier delivery is suppressed by state.

## Signals

While `versentry run` is active:

| Signal | Behavior |
|--------|----------|
| `SIGUSR1` | Scheduled check **now** (state suppress + state write) |
| `SIGUSR2` | Full check **now** (like `versentry check` — no state read/write) |
| `SIGINT` / `SIGTERM` | Stop the daemon |

If a signal arrives while a check is already running, one follow-up check is queued (`SIGUSR2` overrides a queued `SIGUSR1`). No parallel checks.

After a signal-triggered check, the interval ticker is reset (cron keeps wall-clock schedule).

Container commands: [Deployment](deployment.md#signals). CLI details: [Commands](commands.md).
