# Commands

Back to [README](../README.md) · [Configuration overview](configuration.md)

## Subcommands

| Command | Description |
|---------|-------------|
| `versentry check` | One stateless pass over running containers, then exit (always reports all updates) |
| `versentry run` | Periodic checks until SIGINT/SIGTERM (suppresses repeat notifications via state file) |
| `versentry links` | Print notification URLs for monitored containers (no registry calls, no notify/state) |
| `versentry health` | Liveness probe: Docker provider + fresh run-pass stamp (for Docker HEALTHCHECK) |
| `versentry version` | Print build version |

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-c`, `--config` | `config.yaml` | Path to config file |
| `--log-level` | (from config, else `info`) | `debug`, `info`, `warn`, `error` — overrides `log_level` in config |

```bash
./versentry check --log-level debug
./versentry run -c /etc/versentry/config.yaml
./versentry links -c /etc/versentry/config.yaml
./versentry health -c /etc/versentry/config.yaml
./versentry version
```

## `links`

Prints a table of notification URLs for **monitored** containers (same opt-out as checks: `versentry.watch=false` is omitted). Uses local data only — Docker list + image/container labels + rules — **no registry API**, no notifications, no state writes.

Useful to verify Hub / GitHub / GHCR links after changing `mode: digest` or rules. How URLs are chosen (and when they are empty or point at a wrapper repo): [Notifications — Notification URLs](notifications.md#notification-urls).

```bash
docker exec versentry versentry links -c /etc/versentry/config.yaml
```

Columns: `CONTAINER`, `IMAGE:TAG`, `MODE` (`semver` / `digest` / `error`), `URL` (`(no url)` when empty). Table on stdout; logs on stderr.

## `check` vs `run`

| | `versentry check` | `versentry run` |
|---|-------------------|-----------------|
| Duration | Single pass, exit | Daemon until stopped |
| State read | Never | Yes (scheduled passes) |
| State write | Never | Yes (after all notifiers succeed) |
| Notifications | All updates found | Only updates not already notified (scheduled) |
| Scheduling | N/A | `interval` or cron `schedule`; **initial check when state file is missing**, then tick/slot |

### Pass modes inside `run`

| Trigger | Reads state | Writes state | Notifies |
|---------|-------------|--------------|----------|
| Scheduled tick / SIGUSR1 | yes | yes (after delivery) | new updates only |
| Container start / restart (no state file) | yes | yes (after delivery) | new updates only |
| Container start / restart (state exists) | — | — | **no check** until next tick/slot |
| SIGUSR2 force-check | no | no | all updates (like `check`) |

State key and pruning: [Configuration — notification state](configuration.md#notification-state-versentry-run).

## Signals (`versentry run`)

| Signal | Behavior |
|--------|----------|
| `SIGUSR1` | Run a scheduled check **now** (with state suppress + state write) |
| `SIGUSR2` | Force a full check **now** (like `versentry check` — no state read/write) |
| `SIGINT` / `SIGTERM` | Stop the daemon (clean exit 0; logged as shutdown, not an error) |

If a signal arrives while a check is already running, one follow-up check is queued (`SIGUSR2` overrides a queued `SIGUSR1`). No parallel checks.

### Interval vs cron after a check

With **`interval`**, the ticker resets after a scheduled tick, the initial check (no state file), or **SIGUSR1** — the next automatic run is one full `interval` after that check **finishes**. **SIGUSR2** does not reset the ticker (ad-hoc full check must not push back the regular schedule).

With **`schedule`** (cron), wall-clock slots are unchanged; none of the signals shift the cron expression.

Docker `docker kill` examples: [Deployment — signals](deployment.md#signals).

## What it does and does not do

| | |
|---|---|
| **Does** | Lists running containers → compares image tags/digests against registries → notifies |
| **Does not** | Auto-update, auto-restart, or write to the Docker daemon beyond read-only inspect |

Detection pipeline: [Configuration — how checks work](configuration.md#how-checks-work).
