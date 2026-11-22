# Is Versentry right for you?

This page helps you decide whether Versentry fits your setup. The goal is a correct choice — even when that choice is another tool.

Back to [README](../README.md).

## What Versentry does

Versentry is a **notify-only** monitor for Docker image updates. It reads running containers from the local Docker socket, compares tags and digests against OCI registries, and sends alerts. It does **not** pull images, restart containers, or change anything on the host.

Implemented today:

- **Detection:** newer **semver** / dotted numeric tags across majors (cross-major marked `(major)`), and **digest** changes of the same tag for floating tags (`latest`, `stable`, or forced with `track: digest`). Digest updates show the image's OCI version (`2.3.1 → 3.0.0 (major)`) when it has one, can skip rebuilds without a version change (`ignore_rebuilds`), and a multi-arch tag that moved only because another architecture was rebuilt is not reported. Floating version tags such as `postgres:16` get a one-time hint that rebuild alerts need `track: digest`.
- **Policy:** central `rules:` per image in config (`include` / `exclude` regex, `track: digest`, `min_age`, `ignore_rebuilds`); container labels (`versentry.include`, `versentry.exclude`, `versentry.track`, `versentry.min_age`, `versentry.ignore_rebuilds`) override a rule for that one container. Labels baked into an image by its publisher are ignored.
- **Scope:** every running container by default, opt out with `exclude_containers` or `versentry.watch=false`; or opt in with `watch_by_default: false` plus `watch_containers` and/or `versentry.watch=true`.
- **Timing:** `min_age` (notify only when the new image is old enough in the registry; falls back to the newest version that is), `remind_after` (re-send an update you have not applied), `interval` or cron `schedule` with optional `jitter`, notification state so each update is sent once per notifier — a failing notifier is retried alone, without duplicates on the others.
- **Registries:** Docker Hub, GHCR, Quay, GitLab without config; any private / self-hosted OCI registry via `type: oci`. Digest checks use `HEAD`, which Docker Hub does not count against pull limits.
- **Notifiers:** stdout, Telegram, Discord, Gotify, ntfy, generic webhook, and **Apprise** (email, Slack, Pushover, Matrix, Teams and 100+ more through your own apprise-api). `versentry notify-test` sends a sample through all of them.
- **Proxy:** SOCKS5/HTTP for registry traffic and for the Telegram, Gotify, ntfy, Apprise and webhook notifiers.
- **Liveness:** Docker `HEALTHCHECK`, plus an optional `heartbeat` ping to Healthchecks.io / Uptime Kuma after each scheduled check.

## Versentry is likely a good fit if you

- **Manage several Docker hosts** and want **central policy** in one config (tag filters, digest tracking, minimum age by image repo), with labels only for per-container exceptions — rather than the same label copied onto every service.
- Want to know about **new versions** (`1.27.3 → 1.28.0`, `16 → 18 (major)`), not only that the tag you run was rebuilt.
- Want **notifications only**, with no automatic container updates.
- Want a buffer against broken or compromised fresh releases (`min_age`) and reminders for updates you postponed (`remind_after`).
- **Work behind network restrictions** and need a **proxy** for registry and notifier traffic.
- Already use **Telegram**, **Discord**, **Gotify**, **ntfy**, **Apprise**, or a **custom HTTP hook**.

## Versentry is probably not the best fit if you need

**A web UI or dashboard.** Versentry is YAML config, CLI, logs, and notifier messages. [What's Up Docker](https://github.com/getwud/wud) (WUD) and [Cup](https://github.com/sergi0g/cup) ship web UIs.

**Automatic container updates.** Versentry is notify-only by design. [Watchtower](https://github.com/nicholas-fedor/watchtower) (the original `containrrr/watchtower` was archived in December 2025; this is the active community fork) pulls and restarts; [dockcheck](https://github.com/mag37/dockcheck) updates Compose stacks interactively or automatically; WUD can trigger updates too.

**Orchestrators beyond Docker** — Kubernetes, Swarm, Nomad, Podman, or remote Docker hosts without a local socket. Versentry has only the local **Docker** provider. [Diun](https://github.com/crazy-max/diun) covers Docker, Swarm, Kubernetes, Nomad, containerd and more; WUD watches remote Docker hosts and, since 9.1, Kubernetes, Swarm and Nomad.

**Many notification channels without running anything else.** Email, Slack, Teams and similar work in Versentry only through the `apprise` notifier, which needs your own [Apprise API](https://github.com/caronc/apprise-api) server (a separate ~120 MB container). WUD, Diun and Watchtower build many channels in directly.

**A single shell script with no daemon.** dockcheck is one bash script you run by hand or from cron, with interactive selection of what to update.

## How it compares

Snapshot as of **November 2026**, versions as in the column headers; other projects move fast — check their docs for the current state.

| | Versentry | [WUD](https://github.com/getwud/wud) 9.8.0 | [Diun](https://github.com/crazy-max/diun) 4.33.1 | [Watchtower](https://github.com/nicholas-fedor/watchtower) (fork) 1.24.1 | [dockcheck](https://github.com/mag37/dockcheck) 0.8.4 | [Cup](https://github.com/sergi0g/cup) 3.6.0 |
|--|--|--|--|--|--|--|
| **Changes containers** | never | optional (update triggers) | never | yes (`--monitor-only` to only notify) | yes, interactive or auto (`-n` = check only) | never |
| **Web UI** | no | yes | no | no | no | yes |
| **Finds newer version tags** | yes (semver / numeric) | yes (semver) | with `watch_repo` (new tags) | no — same tag only | no — same tag only | yes (semver) |
| **Rebuild of the same tag (digest)** | yes (optionally only when the OCI version changes) | yes (default off for Docker Hub) | yes (default) | yes | yes | yes |
| **Per-image tag policy** | central `rules:` + label override per container | labels per container (regex with `${major}` / `${flavor}` variables); digest watching also set centrally by container name | labels per container + global defaults | — | — | ignore update type globally or per image; exclude images |
| **Default scope** | all running (opt-in available, central list or label) | all (opt-in via label; name / label regex per watcher) | opt-in via label | all (opt-in via label, name/image lists) | all Compose containers (name include / exclude, label opt-in) | all local images (exclude list) |
| **Minimum image age** | `min_age`, falls back to an older version that qualifies | cool-down `wud.watch.delay` | no | `--cooldown-delay` | `-d N` days | no |
| **Notifications** | 6 native + Apprise | many built in | many built in | many built in (shoutrrr) | many via templates (incl. Apprise) | none built in (API / JSON, dashboards) |
| **Providers** | local Docker | Docker (local and remote), Kubernetes, Swarm, Nomad | Docker, Swarm, Kubernetes, Nomad, containerd, files | Docker | local Docker | local Docker (+ aggregates other Cup servers) |
| **Runs as** | container daemon or one-shot CLI | container with UI (login required since 9.0), or one-shot headless with JSON output | container daemon | container daemon | bash script (cron / timer) or container | CLI or server with UI |

### Versentry vs dockcheck

dockcheck is a popular bash script for Compose users who want to **see and apply** updates in one go. The two overlap in detection of rebuilt floating tags and differ in almost everything else:

| | Versentry | dockcheck 0.8.4 |
|--|--|--|
| Purpose | notify only | check **and** update (pull + `docker compose up`), with image backups and pruning |
| What counts as an update | newer version tags (`postgres:16.4 → 16.5`, new majors) **and** rebuilt floating tags | only a changed digest of the **same** tag: `postgres:16.4` never learns about `16.5`; works best on floating tags (`latest`, `16`) |
| Notification links | GitHub releases from the image's OCI `source` label, else the registry page | user-maintained `urls.list` (container name → release notes) |
| Multi-arch tag moved by another architecture's rebuild | not reported (layers of this platform's image compared) | not reported (same check, since 0.8.4) |
| Rebuild without a new version | reported, or skipped with `ignore_rebuilds` when the image has an OCI version label | reported |
| Repeat suppression | state per container; `remind_after` for reminders | snooze window for identical notifications |
| Minimum age | `min_age`, global or per image / container; falls back to an older qualifying version | `-d N` days; too-young images are listed and skipped |
| Metrics | — | Prometheus textfile addon |
| Containers started with `docker run` | checked like any other | skipped unless `-r` |
| Stopped containers | not checked | `-s` includes them |
| Hung / slow registries | per-request dial / TLS / header timeouts, stuck-connection eviction, per-image skip until the next pass | one wall-clock timeout per container (`-t`, default 10 s) |

Pick dockcheck when you want one script that also performs the update. Pick Versentry when updates stay manual and you want to hear about **new versions**, not only rebuilds of the tag you pinned.

## Current limitations

Gaps as of this writing, not a roadmap promise:

- **No web UI** — YAML config and CLI only.
- **Docker provider only** — local socket; no Kubernetes, Swarm, Podman, Nomad, or remote Docker hosts.
- **Running containers only** — stopped containers are not checked.
- **Small native notifier set** — stdout, Telegram, Discord, Gotify, ntfy, webhook; everything else goes through `apprise`, which requires running apprise-api.
- **Floating version tags** (`postgres:16`, `redis:7-alpine`) report newer versions, not rebuilds of the running tag; opt into rebuild alerts per image with `track: digest`.
- **Release-notes links** depend on the image's OCI `source` label; images without it (including most Docker Official Images) link to the registry tag page.
- **Young project** — smaller community, fewer battle-tested deployments than Diun, WUD, Watchtower, or dockcheck.

For feature requests and parity ideas, open a [GitHub issue](https://github.com/BlackRaincoat/versentry/issues). For details, see [Configuration](configuration.md), [Rules](rules.md), and [Notifications](notifications.md).
