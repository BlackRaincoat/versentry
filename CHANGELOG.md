# Changelog

All notable changes to Versentry are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/). Versioning follows [SemVer](https://semver.org/).

## [Unreleased]

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

[1.0.0]: https://github.com/BlackRaincoat/versentry/releases/tag/v1.0.0
