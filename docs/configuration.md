# Configuration overview

Versentry reads a YAML config file (default `/etc/versentry/config.yaml`, override with `-c`). The repository ships one annotated template: [`config.example.yaml`](../config.example.yaml).

Back to [README](../README.md).

## Config file

There is a single example in the repo. **Copy it before the first run** — compose and the CLI expect your own `config.yaml`, not the example file directly.

```bash
cp config.example.yaml config.yaml
# edit config.yaml (notifiers, interval or schedule, rules, …)
```

| Setup | Config path | Notes |
|-------|-------------|--------|
| **Docker Compose** (example stack) | `./config.yaml` → `/etc/versentry/config.yaml` | Default CLI path; no `-c` needed inside the container |
| **Docker / Portainer** (custom) | e.g. `/opt/versentry/config.yaml` on the host | Same idea: your file, bind-mounted read-only at `/etc/versentry/config.yaml` |
| **Bare metal / `go run`** | pass `-c ./config.yaml` (or another path) | Default `/etc/versentry/…` usually does not exist on the host |

Credentials and notifier endpoints are often injected via **`VERSENTRY_*` environment variables** so they stay out of committed YAML — see [Environment variables](#environment-variables). That is **injection**, not a general “env wins over config” rule for fleet policy.

## Top-level fields

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `provider` | yes | — | Container source |
| `notifiers` | yes (≥1) | — | Notification channels — see [Notifications](notifications.md) |
| `registries` | no | auto public hosts | Extra / private OCI registries — see [Registries](registries.md) |
| `rules` | no | — | Per-image detection policy (`include` / `track`) — see [Rules](rules.md) |
| `exclude_containers` | no | — | Exact container names to skip — see [Container scope](#container-scope-opt-out) |
| `watch_by_default` | no | `true` | `false` = opt-in: check only `watch_containers` and containers labeled `versentry.watch=true` — see [Opt-in mode](#opt-in-mode-watch_by_default-false) |
| `watch_containers` | no | — | Exact container names to check in opt-in mode — see [Opt-in mode](#opt-in-mode-watch_by_default-false) |
| `instance_name` | no | see below | Name shown in notification headers |
| `timeouts.provider` | no | `10s` | Docker API timeout |
| `timeouts.registry` | no | `30s` | Overall ceiling per `ListTags` / `TagDigest`; dial/header hang budgets are separate — see [Registries](registries.md#per-pass-cache-and-rate-limits) |
| `interval` | no | `1h` | Period for `versentry run` when `schedule` is empty |
| `schedule` | no | — | Cron expression (5 fields) for `versentry run`; overrides `interval` |
| `timezone` | when `schedule` | see below | Cron only — see [TZ vs timezone](#tz-vs-timezone) |
| `state_file` | no | `{config-dir}/state.json` | Internal notification state for `versentry run` — override only for custom layouts |
| `registry_proxy` | no | — | HTTP/SOCKS5 proxy for all registry API calls — see [Registries](registries.md) |
| `health_max_age` | no | see below | Max age of health stamp for `versentry health` |
| `min_age` | no | off | Notify only when the new image is at least this old in the registry — see [Minimum image age](#minimum-image-age-min_age) |
| `ignore_rebuilds` | no | `false` | Digest tracking: do not report a new image with the same OCI version as the running one — see [Ignore rebuilds](#ignore-rebuilds-ignore_rebuilds) |
| `remind_after` | no | off | `versentry run`: re-send an update you have not applied yet — see [Reminders](#reminders-remind_after) |
| `heartbeat` | no | off | Ping an external monitor (Healthchecks.io, Uptime Kuma push, …) after each scheduled check — see [Heartbeat](#heartbeat-external-liveness) |
| `jitter` | no | off | Random delay `[0, jitter)` before each scheduled check — see [Jitter](#jitter) |
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

At least one notifier is required. Types: `stdout`, `telegram`, `webhook`, `discord`, `gotify`, `ntfy`. Details: [Notifications](notifications.md).

Use **one entry per channel** (e.g. `stdout` + `telegram`, or `telegram` + `discord`). The list is not meant for multiple Telegram bots with different credentials — use a single `type: telegram` block and set secrets via env.

Default `mode` and template keys differ by notifier (`telegram` / `gotify` / `ntfy`: `item_template`/`digest_template`; `discord`/`webhook`: optional full-body `template`). All notifiers default to **`digest`**. See [Notifications — defaults by channel](notifications.md#defaults-by-channel).

Links in alerts come from the image `org.opencontainers.image.source` label and tracking mode — limits (missing label, wrapper repos, semver vs digest) are in [Notifications — Notification URLs](notifications.md#notification-urls).

`instance_name` from the top-level config (or hostname / env) is injected into every notifier at startup.

## Precedence: two different mechanisms

Do not read “env overrides YAML” as “environment is the source of truth for Versentry.” Two independent rules:

### 1. Fleet policy (config ↔ labels)

Source of truth for **what to watch and how to detect updates** is the YAML config, with labels as an escape hatch:

| Concern | Precedence |
|---------|------------|
| Tag filters / `track` | config `rules[]` is the base; `versentry.include` / `versentry.track` on a container **override** it for that container, field by field (see [Rules](rules.md#two-sources-priority)) |
| Opt-out | `exclude_containers` **OR** `versentry.watch=false` (both only exclude); with `watch_by_default: false`, only `watch_containers` **OR** `versentry.watch=true` is checked, and exclusion still wins |

`VERSENTRY_*` does **not** participate here. Scheduling (`interval` / `schedule` / `timezone`), `rules`, `exclude_containers`, `timeouts`, notifier `mode` / templates, and similar policy knobs live in the config file only.

### 2. Injection: secrets and deploy wiring (`VERSENTRY_*` → YAML)

Non-empty `VERSENTRY_*` variables override the matching YAML fields in the table below (credentials, notifier endpoints/proxies, and a few deploy paths). They do **not** override scheduling, rules, exclude lists, or timeouts — those stay in the config file.

Unset or empty env leaves the YAML value as-is. All Versentry-specific variables use the `VERSENTRY_` prefix to avoid clashes with `TZ`, `HTTP_PROXY`, and other services in the same compose stack.

#### Credentials and endpoints

| Variable | Overrides |
|----------|-----------|
| `VERSENTRY_TELEGRAM_TOKEN` | `token` on the `telegram` notifier |
| `VERSENTRY_TELEGRAM_CHAT_ID` | `chat_id` on the `telegram` notifier |
| `VERSENTRY_TELEGRAM_PROXY` | `proxy` on the `telegram` notifier (`socks5://` or `http://`) |
| `VERSENTRY_DISCORD_WEBHOOK_URL` | `url` on the `discord` notifier |
| `VERSENTRY_WEBHOOK_URL` | `url` on the `webhook` notifier |
| `VERSENTRY_WEBHOOK_AUTHORIZATION` | `Authorization` header on the `webhook` notifier |
| `VERSENTRY_WEBHOOK_PROXY` | `proxy` on the `webhook` notifier |
| `VERSENTRY_GOTIFY_URL` | `url` on the `gotify` notifier |
| `VERSENTRY_GOTIFY_TOKEN` | `token` on the `gotify` notifier |
| `VERSENTRY_GOTIFY_PROXY` | `proxy` on the `gotify` notifier |
| `VERSENTRY_NTFY_URL` | `url` on the `ntfy` notifier |
| `VERSENTRY_NTFY_TOPIC` | `topic` on the `ntfy` notifier |
| `VERSENTRY_NTFY_TOKEN` | `token` on the `ntfy` notifier (optional Bearer) |
| `VERSENTRY_NTFY_PROXY` | `proxy` on the `ntfy` notifier |
| `VERSENTRY_APPRISE_URL` | `url` on the `apprise` notifier |
| `VERSENTRY_APPRISE_KEY` | `key` on the `apprise` notifier (drops YAML `urls`) |
| `VERSENTRY_APPRISE_URLS` | `urls` on the `apprise` notifier (drops YAML `key` / `tag`) |
| `VERSENTRY_APPRISE_USERNAME` | `username` on the `apprise` notifier |
| `VERSENTRY_APPRISE_PASSWORD` | `password` on the `apprise` notifier |
| `VERSENTRY_APPRISE_PROXY` | `proxy` on the `apprise` notifier |
| `VERSENTRY_HEARTBEAT_URL` | `heartbeat.url` |
| `VERSENTRY_HEARTBEAT_FAIL_URL` | `heartbeat.fail_url` |
| `VERSENTRY_REGISTRY_USERNAME` | `username` on every configured `oci` registry |
| `VERSENTRY_REGISTRY_TOKEN` | `token` on every configured `oci` registry |
| `VERSENTRY_REGISTRY_PROXY` | `registry_proxy` (HTTP or `socks5://` for all registries) |

#### Deploy overrides (not secrets)

| Variable | Overrides |
|----------|-----------|
| `VERSENTRY_INSTANCE_NAME` | top-level `instance_name` |
| `VERSENTRY_STATE_FILE` | top-level `state_file` (Docker image default: `/data/state.json`) |

These exist so Compose/image layout can set hostname display and the state path without editing policy YAML. Prefer env for tokens and webhook URLs you would not commit; `instance_name` / `state_file` may live in YAML or env equally.

`TZ` is **not** a `VERSENTRY_*` override — see [TZ vs timezone](#tz-vs-timezone).

**Instance name in Docker:** `os.Hostname()` inside a container is often the container id (e.g. `8145fc52b5fd`). Set `VERSENTRY_INSTANCE_NAME`, `instance_name` in YAML (overridden by env when set), or bind-mount the host hostname:

```yaml
volumes:
  - /etc/hostname:/etc/versentry/hostname:ro
```

**Instance name priority:** `VERSENTRY_INSTANCE_NAME` → `instance_name` in YAML → bind-mounted `/etc/versentry/hostname` → `os.Hostname()` → `versentry` when the hostname looks like a container id.

**Bind-mount footgun:** the image ships an empty placeholder at `/etc/versentry/hostname` so Docker mounts the host file correctly. Without it, `- /etc/hostname:/etc/versentry/hostname:ro` often creates a **directory** instead of a file, and the name is not read.

**Where it appears:** **simple** mode uses the same layout as digest — instance on its own line (`📦 prod-docker-01`), then the container update on the next line(s). **Digest** with multiple updates keeps one instance header for the whole batch (`📦 prod-docker-01 — N updates`). Registry host (`index.docker.io`, …) is a separate field and is not in default Telegram/Discord templates.

## Minimum image age (`min_age`)

Delay notifications until the new image has been in the registry for a while — a guard against broken releases that get re-pushed or yanked within the first day.

```yaml
min_age: 3d          # global; 0 or unset = off

rules:
  - image: "grafana/grafana"
    min_age: 7d      # stricter for one image
  - image: "myorg/nightly"
    min_age: 0       # off for a tag rebuilt every night (see below)
```

**Durations** everywhere in the config (`interval`, `timeouts.*`, `min_age`, …) accept a leading day count: `3d`, `1d12h`, alongside Go units (`36h`, `90m`).

**Age** is the `created` time of the image in the registry (config blob; for a multi-arch index, the child for this host's platform). Versentry fetches it only for a **found update**, never for up-to-date containers.

| Path | Too young |
|------|-----------|
| Semver / numeric | Try the next older newer-than-current version (up to 5 lookups per container per pass). The newest one that is old enough is notified — `1.4.0 → 1.4.2` while `1.4.3` is 1 day old. If none qualifies: **pending**. |
| Digest | The tag's current image is the only candidate: **pending** until it is old enough. |

**Pending** means: no notification and **no state write** — the container is re-checked every pass and notified once the candidate is old enough. Logged at INFO as `update pending … reason="1.4.3 is 1d4h old, min_age 3d, eligible 2026-11-22 09:15"` — `eligible` is when the image reaches `min_age` (in the process time zone, like log timestamps); the notification goes out at the first check after that; `check complete` counts `pending=N`. Applies to `versentry check` too.

**Unknown age** (no timestamp, `created` zeroed to 1970 by reproducible builds, or a bogus `created` more than a day in the future) is **not** delayed — min_age holds back only images the registry proves young (DEBUG `min_age: image has no build timestamp` / `… is in the future`). A `created` slightly ahead of your clock (up to a day of skew) counts as brand new.

**Registry errors** during the age lookup (timeout, 429, 5xx) skip the container for this pass, like any other registry failure.

**Cost:** each lookup is one manifest `GET` (index + platform manifest), which Docker Hub counts as **one pull** against the same per-IP limit as your `docker pull`, plus a config blob (not counted). Only containers with an update pay it.

**Frequently rebuilt tags:** a digest-tracked tag rebuilt more often than `min_age` (nightly `latest`) never gets old enough, so it would stay pending forever. Set `min_age: 0` on that image's rule.

**Per-image rules:** `rules[].min_age` overrides the global value (`0` turns it off). For one container, the label `versentry.min_age` (`3d`, `36h`, `0` = off) overrides both; `versentry.include` / `versentry.track` override a rule's `include` / `track` and inherit its `min_age` — see [Rules](rules.md#two-sources-priority).

## Ignore rebuilds (`ignore_rebuilds`)

A floating tag (`latest`, `stable`, or any image with `track: digest`) moves on every rebuild of the image — a new base image, a security patch in a dependency — not only on a new release. When both the running image and the new one carry the standard `org.opencontainers.image.version` label, Versentry can tell the two apart:

```yaml
ignore_rebuilds: true        # global; default false

rules:
  - image: "example/app"
    ignore_rebuilds: false   # this image: report rebuilds too
```

- **Same version** (`2.3.1` → `2.3.1`): not reported. DEBUG `container checked … reason="digest changed, same version (ignore_rebuilds)"`.
- **New version** (`2.3.1` → `2.4.0`): reported as usual.
- **No label** on either image: reported as before — without versions there is nothing to compare.

Only digest tracking is affected; tag updates (`1.2.3 → 1.2.4`) always compare versions. `rules[].ignore_rebuilds` overrides the global value for an image, and the label `versentry.ignore_rebuilds` (`true` / `false`) overrides both for one container — see [Rules](rules.md#two-sources-priority).

**Cost:** reading the new image's version is one manifest `GET` (a Docker Hub pull) when the tag moved, at most 5 per check. The result is remembered while the tag points to that image, so it is not repeated every check, and the notification reuses it. With [`min_age`](#minimum-image-age-min_age) the same lookup serves both. If the lookup fails, the change is reported.

## Addressing axes (image vs container)

Versentry has **two independent addressing axes** in config:

| Axis | Config | Addresses | Purpose |
|------|--------|-----------|---------|
| **Image** | `rules[]` | repository path (`postgres`, `chatwoot/chatwoot`, …) | Detection: which tags / digest vs semver |
| **Container** | `exclude_containers`, `watch_containers` | exact Docker container name (as in `docker ps`) | Opt-out: do not monitor that container; opt-in mode: monitor only these (with `watch_by_default: false`) |

Labels (`versentry.*`) remain available as a per-container escape hatch. Detection and opt-out precedence are under [Fleet policy](#1-fleet-policy-config--labels) — env injection does not apply there.

**Per-container exceptions (detection):** a config `rules[]` entry applies to **every** running container of that image; a container that needs a different line sets `versentry.include` / `versentry.track` labels, which override the config rule for that container only (field by field). Per-container rules in config itself are not implemented (YAGNI).

## Container scope (opt-out)

By default **all running containers** are checked. Opt out by name in config and/or with the label `versentry.watch=false`. Both sources only **exclude**; there is no config switch to force-monitor a labeled container. To check only selected containers instead, see [Opt-in mode](#opt-in-mode-watch_by_default-false).

### Config `exclude_containers`

```yaml
exclude_containers:
  - chatwoot-notify
```

- Exact match of the Docker container name (Compose service name / `docker ps` NAME).
- Duplicate names in the list are harmless (deduped); Versentry may log **WARN**.
- Empty entries → **config error**.
- Name not among **running** containers → **WARN** each pass (container may be stopped); not a fatal error.

### Label `versentry.watch`

Works independently of `exclude_containers` (WUD migration: `wud.watch` → `versentry.watch`; opt-out from an app Compose file without editing Versentry config).

| Label | Behavior |
|-------|----------|
| absent | monitored (unless name is in `exclude_containers`) |
| `versentry.watch=true` (or `1`, `t`, …) | monitored (unless name is in `exclude_containers`) |
| `versentry.watch=false` (or `0`, `f`, …) | excluded |

Invalid values (`yes`, `on`, …) log **WARN** and are treated as absent (monitored), unless the name is also in `exclude_containers`.

Only a `versentry.watch` label set on the container (Compose / `docker run`) counts; the same label baked into an image by its publisher is ignored — see [Rules](rules.md#two-sources-priority).

### Combined rule

A container is **excluded** if it is listed in `exclude_containers` **or** has `versentry.watch=false`. Otherwise it is monitored.

Excluded containers do not appear in check results (not counted as skipped). Summary logs include `excluded=N`.

**State / re-enable:** excluded containers are not part of the active fleet for notification state. Their container state key is pruned on the next pass. If you remove the name from `exclude_containers` and/or clear the label, Versentry may **notify again** for updates that were never applied while the container was excluded.

```yaml
# Config (preferred for per-host policy)
exclude_containers:
  - chatwoot-sidekiq

# Or label on the service (when you cannot / prefer not to touch Versentry config)
services:
  chatwoot-sidekiq:
    image: chatwoot/chatwoot:latest
    labels:
      versentry.watch: "false"
```

### Opt-in mode (`watch_by_default: false`)

On a host where only a few of many containers matter, flip the default: only selected containers are checked; everything else is ignored, including containers started later. Select them centrally by name, with a label on the service, or both:

```yaml
watch_by_default: false
watch_containers:        # central list (exact names, like exclude_containers)
  - postgres
  - nextcloud
```

```yaml
# …or from the app's own Compose file
services:
  immich-server:
    labels:
      versentry.watch: "true"
```

| Container | `watch_by_default: true` (default) | `watch_by_default: false` |
|-----------|-------------------------------------|---------------------------|
| no label, not listed | monitored | **ignored** |
| in `watch_containers` | monitored | monitored |
| `versentry.watch=true` | monitored | monitored |
| `versentry.watch=false` | excluded | excluded (even if listed) |
| in `exclude_containers` | excluded | excluded (even if labeled `true`) |
| invalid label (`yes`, …) | WARN, as if absent | WARN, as if absent |

- **Exclusion always wins:** `exclude_containers` or `versentry.watch=false` beats `watch_containers` and `versentry.watch=true`. A name in both `watch_containers` and `exclude_containers` is a config error.
- `watch_containers` names follow the `exclude_containers` rules: exact Docker names, empty entry = config error, duplicates WARN, a name not among running containers WARNs each pass.
- `watch_containers` with the default `watch_by_default: true` has no effect (startup WARN): every container is already checked.
- Nothing selected → **WARN** each pass (`nothing to check`).
- Startup log: `watch_by_default is false: only watch_containers and containers labeled versentry.watch=true are checked`. `versentry links` lists only selected containers.
- **Switching an existing install:** containers that are no longer watched leave the active fleet, so their state entries are pruned on the next pass (same as excluding them). Selecting one later may re-notify an update it already had.

## Notification state (`versentry run`)

`versentry run` keeps a JSON state file to avoid sending the same update repeatedly. `versentry check` is always stateless.

| Mode | Reads state | Writes state | Notifies |
|------|-------------|--------------|----------|
| `check` | no | no | all updates found |
| `run` (scheduled / SIGUSR1) | yes | yes (after successful delivery) | only new updates (+ due [reminders](#reminders-remind_after)) |
| `run` (SIGUSR2 force-check) | no | no | all updates found (like `check`) |

State key: `{container_name}|{registry-host}/{repo}` (e.g. `web|index.docker.io/library/nginx`). Suppression is **per container**, so two containers on the same image (different tag lines or twin instances) keep independent history. Value: last notified target — semver `LatestTag` or normalized remote digest. JSON map field: `entries` (state file `version` 2).

Registry API dedup within a pass remains **per image** (`host/repo`) — that is separate from state suppression.

- Missing state file → all current updates are treated as new.
- Corrupt state file at startup → WARN, empty state (monitoring continues; may re-notify once).
- State file from an older Versentry (`version` < 2, image-scoped keys) → WARN, history reset; a **one-time re-notification** of pending updates is possible after upgrade.
- Stale entries (containers no longer monitored) are pruned on each state-updating pass.
- Empty container list after successful list → prune skipped (transient glitch protection).
- `ListRunning` error → state not touched.
- Tracking mode change (digest ↔ semver) → treated as a new target (may notify once).
- Delivery is tracked **per notifier**: if one notifier fails, only that notifier gets the update again on the next pass; notifiers that already delivered it are not sent a duplicate. A notifier added to the config later receives the currently pending updates once. Atomic write: temp file + rename.

### Reminders (`remind_after`)

By default each target is notified **once**: if you do not update, Versentry stays silent until an even newer version appears. With `remind_after`, an update that is still pending is sent again once its last notification is at least that old:

```yaml
remind_after: 7d    # 0 or unset = off (notify once)
```

- The repeat is marked: default change text ends with ` (reminder)` (`1.27.1 → 1.27.2 (reminder)`); templates get `{{.Reminder}}`, the webhook JSON `"reminder": true`, `stdout` logs `reminder=true`. INFO `reminders due reminders=N` in the pass log.
- After a reminder is delivered the clock restarts: next reminder another `remind_after` later, until you update or a newer target replaces it (that is a normal notification, not a reminder).
- Uses the `notified_at` already stored in the state file — no state format change. Enabling it on an existing install sends one round of reminders on the first scheduled pass for every update older than `remind_after`.
- Only scheduled passes and `SIGUSR1` (they read state). `check` and `SIGUSR2` notify everything anyway and never mark reminders.
- Reminders are timed per notifier; a notifier whose delivery failed gets the update on the next pass (as a catch-up, not a reminder).

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

### Jitter

Several hosts with the same `schedule` (or restarted together with the same `interval`) all query registries at the same second. `jitter` delays every scheduled check by a random time in `[0, jitter)`:

```yaml
schedule: "0 11 * * *"
jitter: 30m          # checks start between 11:00 and 11:30; 0 or unset = off
```

- Only scheduled checks are delayed. The first check of a fresh install (no state file), `SIGUSR1`, `SIGUSR2`, and `versentry check` run immediately.
- `SIGUSR1` during the wait cancels the delayed check (the forced one replaces it).
- With `interval`, `jitter` must be shorter than `interval`; the next interval is counted from the end of the delayed check, so checks drift by up to `jitter` per cycle. With `schedule`, the cron slot stays fixed and only the start within it is randomised.
- Log: INFO `scheduled check delayed by jitter delay=…` at each tick.

### TZ vs timezone

Versentry uses **two different timezone knobs**. They are not duplicates and are separate from both fleet-policy precedence and `VERSENTRY_*` injection (see [Precedence](#precedence-two-different-mechanisms)).

| | `TZ` (environment) | `timezone` (YAML config) |
|--|-------------------|--------------------------|
| **Scope** | Whole container / process (standard Unix env) | Cron scheduling in `versentry run` only |
| **Affects** | Log timestamps, Go `time.Local`, libc | When `schedule: "0 12 * * *"` fires (which 12:00) |
| **Used with `interval` only?** | Yes (logs only) | No — ignored without `schedule` |
| **Precedence** | Process default | For cron: `timezone` in config, then `TZ` if `timezone` is empty |

**Cron requires a timezone:** set `timezone` in config (e.g. `Europe/Paris`) **or** `TZ` in the environment. Startup fails if `schedule` is set and neither resolves to a valid IANA zone.

**Docker recommendation:** set `TZ` in compose for readable logs. Add `timezone` in config when you use `schedule` and want the cron zone documented in the config file (or when `TZ` is unset). If both are set, cron follows `timezone`; logs still follow `TZ` — keep them aligned (e.g. both `Europe/Paris`) to avoid confusion.

```yaml
# compose.yml
environment:
  TZ: Europe/Paris

# config.yaml (only when using schedule)
schedule: "0 12 * * *"
timezone: Europe/Paris
```

With **`interval` only** (no `schedule`), `timezone` in config is unused; `TZ` still controls log timestamps.

## Registry behavior (engine)

**Per-pass cache:** each check pass deduplicates `ListTags` / `TagDigest` by `host/repo` (digest mode: `host/repo#tag`). Multiple containers on the same image hit the registry once per pass. Cache resets between passes.

**Rate limits (HTTP 429):** one short `Retry-After` retry (≤10s) per request; if still rate-limited or `Retry-After` is long/missing, the host is skipped for the rest of the pass (`registry rate limited, will retry next pass`). Other registry hosts are unaffected. Persistent 5xx after transport retries skips the image only. Per-request timeout / network errors also skip that image and continue the pass (daemon keeps running).

Details: [Registries](registries.md).

## Health stamp

`versentry run` writes `{state-dir}/health` on startup, on a periodic heartbeat while the daemon runs, and after each successful scheduled pass. `versentry health` fails if the stamp is missing or older than `2×interval` (ticker mode), `2×cron interval` derived from `schedule` (cron mode), or `health_max_age` when set (always wins). With cron scheduling the heartbeat keeps the stamp fresh between daily checks so Docker HEALTHCHECK stays green while the process is up.

See [Deployment](deployment.md) for Docker HEALTHCHECK.

## Heartbeat (external liveness)

The Docker HEALTHCHECK only marks the container `unhealthy` — nobody is told, and it cannot notice a dead host or a stopped container. A heartbeat closes that gap: after every scheduled check Versentry pings a URL of an external "dead man's switch" monitor, which alerts you through its own channels when the ping stops coming.

```yaml
heartbeat:
  url: "https://hc-ping.com/<uuid>"           # or VERSENTRY_HEARTBEAT_URL
  fail_url: "https://hc-ping.com/<uuid>/fail" # optional, or VERSENTRY_HEARTBEAT_FAIL_URL
  # proxy: "socks5://…"
  # timeout: 10s
```

| Monitor | `url` | `fail_url` |
|---------|-------|-----------|
| [Healthchecks.io](https://healthchecks.io) (or self-hosted) | `https://hc-ping.com/<uuid>` | `https://hc-ping.com/<uuid>/fail` |
| [Uptime Kuma](https://github.com/louislam/uptime-kuma) push monitor | `https://kuma.example.com/api/push/<token>?status=up` | `https://kuma.example.com/api/push/<token>?status=down` |
| Better Stack, Cronitor, … | the heartbeat / ping URL | the service's failure URL, if any |

- **When:** after each scheduled check — cron / interval ticks, the initial check, and `SIGUSR1`. Not after `SIGUSR2` or `versentry check`.
- **Success** → `url`. **Failure** → `fail_url`: the check could not run (e.g. Docker socket unreachable) **or any notifier failed** (you would not hear about updates). Registry skips and rate limits are normal noise and still count as success.
- **No `fail_url`:** a failure sends nothing; the missed success ping alerts after the monitor's grace period.
- **Body:** a one-line summary as `text/plain` POST, e.g. `versentry: checked=24 up-to-date=21 updates=2 pending=0 skipped=1 excluded=3 failed_notifiers=0` (Healthchecks shows it in the ping log; push monitors ignore it).
- **Monitor settings:** period = your schedule (`0 11 * * *` → 1 day), grace ≥ `jitter` + typical check duration.
- Ping problems are logged (`heartbeat ping failed`, two attempts) and never stop Versentry. The URLs are secrets (check UUID, push token): logs show the host only.

## Notifier failures

A notifier failure (e.g. bad Telegram token) logs **ERROR** but **does not stop** `versentry run` — the next scheduled check still runs. State is tracked per notifier: the failed notifier gets the update again on the next scheduled pass (INFO `re-sending to notifiers that missed earlier updates`), while notifiers that delivered it are not sent a duplicate.

**Notifier names.** Each notifier has an ID used in logs and in that per-notifier state: by default its `type` (`telegram`), and `telegram#2`, `telegram#3`, … for further notifiers of the same type. Set `name:` to choose one — recommended when you have several notifiers of one type, so reordering them does not mix up their history:

```yaml
notifiers:
  - type: telegram
    name: family        # optional, unique
    config: { … }
```

## How checks work

1. List running containers (Docker socket); skip `exclude_containers` names and `versentry.watch=false` (OR).
2. Parse image ref → registry host + repo + tag.
3. If rule/label sets `track: digest` → compare local vs remote digest for the current tag (see [Rules](rules.md#track-digest)).
4. Else if **semver or numeric tag:** list tags → apply `include` filter if any → newest matching tag (any major) → notify if newer (cross-major marked `(major)` / `Bump=major`).
5. Else **non-semver tag** (`latest`, …): compare local manifest digest vs registry tag digest.
6. Batch all updates from the pass → notifiers (`run` may filter already-notified targets before step 6).

In `run` mode, detection always logs every update at INFO; only notifier delivery is suppressed by state.

## Signals

While `versentry run` is active:

| Signal | Behavior |
|--------|----------|
| `SIGUSR1` | Scheduled check **now** (state suppress + state write) |
| `SIGUSR2` | Full check **now** (like `versentry check` — no state read/write) |
| `SIGINT` / `SIGTERM` | Stop the daemon (clean exit; not treated as an error) |

If a signal arrives while a check is already running, one follow-up check is queued (`SIGUSR2` overrides a queued `SIGUSR1`). No parallel checks.

With **`interval`**, the ticker resets after a scheduled tick, the initial check (no state file), or **SIGUSR1** — the next automatic run is one full `interval` after that check **finishes**. **SIGUSR2** does not reset the ticker. With **`schedule`** (cron), wall-clock slots are unchanged.

Container commands: [Deployment](deployment.md#signals). CLI details: [Commands](commands.md).
