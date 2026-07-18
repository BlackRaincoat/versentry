# Changelog

All notable changes to Versentry are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/). Versioning follows [SemVer](https://semver.org/).

## [1.2.2] - 2026-07-18

### Security

- Bump transitive `github.com/docker/cli` `v27.5.0` → `v29.6.2` (via `go-containerregistry` → `cli/config` for registry credentials). Clears Docker Scout’s CVE-2025-15558 finding. That advisory is **Windows-only** (unprivileged write to `C:\ProgramData\Docker\cli-plugins`); Versentry never invokes Docker CLI plugins and runs as a Linux image — false positive by reachability; bump is scanner hygiene only.
- Final image stage is now `scratch` (no Alpine/BusyBox). Removes OS-layer scanner surface (e.g. CVE-2025-60876 BusyBox wget and similar). Runtime needs: static binary, copied CA bundle, embedded `time/tzdata`.

### Changed

- Embed IANA timezone database via `import _ "time/tzdata"` instead of the Alpine `tzdata` package. Cron `timezone:` / `TZ` (e.g. `Europe/Paris`) keep working without zoneinfo files. **Trade-off:** zoneinfo freezes at the Go toolchain version used to build; updates only on rebuild (unlike `apk upgrade tzdata`).
- Docker HEALTHCHECK calls `/usr/local/bin/versentry health` directly (no `/usr/local/bin/health` shell wrapper). Compose overrides should use the same exec form.

## [1.2.1] - 2026-07-17

### Security

- Replace deprecated `github.com/docker/docker` with `github.com/moby/moby/client` (Docker Engine API client for 29.x). Removes the old SDK from the image Go SBOM so Docker Scout stops flagging **daemon-only** Moby findings (e.g. CVE-2026-34040 AuthZ bypass, CVE-2026-41567/41568/42306 `docker cp` / related, CVE-2025-54410 firewalld/iptables). Those affect the Engine process, not the client Versentry uses against the socket — this is scanner hygiene, not a Versentry exploit fix. Alpine/BusyBox base CVEs are unchanged (planned separately).

## [1.2.0] - 2026-07-17

### Changed

- Rules / labels: `mode: digest` renamed to **`track: digest`** (`rules[].track`, label `versentry.track`) to avoid collision with notifier `config.mode: digest|simple`. The old names still work with a **WARN** and will be removed in a future major release.

### Added

- Gotify notifier (`type: gotify`) — self-hosted push via `POST /message` + `X-Gotify-Key`; `priority` (default 5), markdown body, `mode: digest|simple`, optional proxy, `item_template` / `digest_template` (Telegram-shaped); env `VERSENTRY_GOTIFY_URL` / `_TOKEN` / `_PROXY`
- ntfy notifier (`type: ntfy`) — public or self-hosted push via JSON `POST` to server base (`topic` in body); `priority` 1–5 (default 3), `tags` (default `package`), markdown body, optional Bearer `token`, `click` in `simple` only, `item_template` / `digest_template` (Gotify-shaped); env `VERSENTRY_NTFY_URL` / `_TOPIC` / `_TOKEN` / `_PROXY`
- `versentry run` / `check` log binary identity at start: `versentry starting version=… commit=…` (ldflags; local builds show `version=dev`, `commit=unknown`)

### Fixed

- Deterministic semver tag choice when several tags coerce to the same version (e.g. `8.3` vs `8.3.0`): prefer the form of the running tag (v-prefix, then numeric component count, then suffix), not registry `ListTags` order. Existing installs may get a **one-time** re-notification if state stored a different spelling of the same version.

## [1.1.0] - 2026-07-15

### Added

- Rules / labels: `mode: digest` forces digest detection for floating tags that still parse as semver (e.g. `valkey/valkey:9-alpine`)
- `versentry links` — print notification URLs for monitored containers without registry calls

### Changed

- Docs: document notification URL limits (OCI `source` label, wrapper repos, semver `/releases` vs digest registry pages)
- Notification image links prefer reliable pages: GitHub `{source}/releases` (semver), registry tag views for digest; no more `/releases/tag/{docker-tag}` guesses
- `versentry run` / `check` load the config file once (no second parse during app init)
- Notification state is keyed per **container** (`{name}|{host}/{repo}`), not per image — independent suppression for containers sharing an image; state file `version` 2 uses JSON `entries` (was `images`)
- Upgrading from state `version` 1 resets history (cannot convert old keys); a one-time re-notification of pending updates is possible

### Fixed

- Registry timeout/network error for one image no longer aborts the check pass or crashes `versentry run` (image skipped; pass continues; default `timeouts.registry` unchanged at 30s)
- `versentry run` exits cleanly on SIGINT/SIGTERM (no `Error: context canceled` or cobra usage dump)

## [1.0.2] - 2026-07-07

### Fixed

- GitHub release links in Versentry self-update notifications align with registry tags (`1.0.2`, not `v1.0.2`)
- `SIGUSR2` no longer resets the `interval` ticker in `versentry run` (ad-hoc full checks do not delay the next scheduled tick)
- `rules.image` accepts Docker Hub short names (`postgres`, `caddy`, …) as in compose, not only `library/<name>`

### Changed

- Git release tags use `X.Y.Z` without a `v` prefix (aligned with Docker Hub / GHCR); applies from this version onward (`v1.0.0` / `v1.0.1` remain on GitHub)
- Document `interval` vs cron behavior for scheduled ticks, **SIGUSR1**, and **SIGUSR2**

## [1.0.1] - 2026-07-06

### Fixed

- `versentry run` now performs an initial check when the state file is missing, then follows `interval` or cron `schedule` (previously waited until the first tick/slot)

### Changed

- `config.example.yaml`: comment out optional empty `provider.config` block

## [1.0.0] - 2026-07-06

First public release.

### Added

- Notify-only Docker image update monitor (semver tags and digest drift on floating tags)
- Docker provider; OCI registries (Docker Hub, GHCR, Quay, GitLab, private/self-hosted)
- Notifiers: Telegram, Discord, webhook, stdout; optional HTTP/SOCKS proxy
- Delivery modes: `simple` and `digest`; Go `text/template` overrides
- Scheduling: fixed interval or cron with timezone
- Per-container rules via `versentry.include` labels; global `rules:` in config
- Instance/host name in notifications
- State file to suppress repeat alerts
- `versentry health` and Docker HEALTHCHECK support
- Multi-arch Docker image (amd64, arm64)
- `VERSENTRY_*` environment variable overrides for secrets and paths

[1.2.2]: https://github.com/BlackRaincoat/versentry/releases/tag/1.2.2
[1.2.1]: https://github.com/BlackRaincoat/versentry/releases/tag/1.2.1
[1.2.0]: https://github.com/BlackRaincoat/versentry/releases/tag/1.2.0
[1.1.0]: https://github.com/BlackRaincoat/versentry/releases/tag/1.1.0
[1.0.2]: https://github.com/BlackRaincoat/versentry/releases/tag/1.0.2
[1.0.1]: https://github.com/BlackRaincoat/versentry/releases/tag/v1.0.1
[1.0.0]: https://github.com/BlackRaincoat/versentry/releases/tag/v1.0.0
