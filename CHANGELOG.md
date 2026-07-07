# Changelog

All notable changes to Versentry are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/). Versioning follows [SemVer](https://semver.org/).

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

[1.0.2]: https://github.com/BlackRaincoat/versentry/releases/tag/1.0.2
[1.0.1]: https://github.com/BlackRaincoat/versentry/releases/tag/v1.0.1
[1.0.0]: https://github.com/BlackRaincoat/versentry/releases/tag/v1.0.0
