# Is Versentry right for you?

This page helps you decide whether Versentry fits your setup. The goal is a correct choice — even when that choice is another tool.

Back to [README](../README.md).

## What Versentry does

Versentry is a **notify-only** monitor for Docker image updates. It reads running containers from the local Docker socket, compares tags and digests against OCI registries, and sends alerts. It does **not** pull images, restart containers, or change anything on the host.

Implemented today: Docker provider; tag filtering via central `rules:` in config and per-container `versentry.include` labels; public and private OCI registries; notifiers **stdout**, **Telegram**, **Discord**, **Gotify**, **ntfy**, and generic **webhook**; optional SOCKS5/HTTP proxy for HTTP notifiers and registry traffic; scheduled or one-shot checks with notification state to avoid repeats.

## Versentry is likely a good fit if you

- **Manage several Docker hosts** and want **central tag-filter policy** in config (regex `rules:` by image repo), not only per-container labels on every service.
- **Work behind network restrictions** and need a **proxy** (SOCKS5 or HTTP) for notifier delivery — Telegram, Gotify, ntfy, and webhook support a dedicated proxy; registries can use `registry_proxy`.
- Want **notifications only**, with no automatic container updates.
- Already use **Telegram**, **Discord** (server webhook), **Gotify**, **ntfy**, or a **custom HTTP hook** — Versentry has first-class notifiers for those; webhook with templates can reach other systems, but you wire the payload yourself.
- Need one mechanism for **any OCI-compatible registry** — public hosts work without extra config; private and self-hosted registries via `type: oci`.

## Versentry is probably not the best fit if you need

**A web UI or dashboard** to browse pending updates visually. Versentry has no web interface — configuration is YAML, output is logs and notifier messages. [What's Up Docker](https://github.com/fmartinou/whats-up-docker) (WUD) is notify-only like Versentry and ships with a web UI for overview and configuration.

**Automatic container updates**, not just alerts. Versentry is intentionally notify-only. For pull-and-restart workflows, [Watchtower](https://github.com/containrrr/watchtower) and community forks are built for that job. Note: the original `containrrr/watchtower` repository was archived in December 2025; an active community fork is [nicholas-fedor/watchtower](https://github.com/nicholas-fedor/watchtower).

**Orchestrators beyond Docker** — Kubernetes, Swarm, Podman, Nomad, remote hosts without a local Docker socket. Versentry currently supports only the **Docker** provider (`docker.sock`). [Diun](https://github.com/crazy-max/diun) supports a wider set of container providers.

**Many first-class notification channels out of the box** — Slack, email, Microsoft Teams, Mail, and others without wiring a webhook yourself. Versentry ships stdout, Telegram, Discord, Gotify, ntfy, and a generic HTTP webhook. That covers common self-hosted setups, but Diun and WUD still offer more built-in notifier types; a Versentry webhook can reach some of the rest if you configure the endpoint and payload.

## Current limitations

These are gaps as of this writing, not a roadmap promise:

- **No web UI** — YAML config and CLI only.
- **Docker provider only** — no Kubernetes, Swarm, Podman, or Nomad provider yet.
- **Narrow built-in notifier set** — stdout, Telegram, Discord, Gotify, ntfy, and generic webhook are implemented; Slack and email as dedicated types are not. Discord, Gotify, and ntfy are real notifiers (`type: discord` / `type: gotify` / `type: ntfy`), not “use generic webhook and hope.”
- **Include-only tag rules** — no `exclude` filter in rules yet.
- **Young project** — smaller community, fewer battle-tested deployments than Diun, WUD, or Watchtower.

For feature requests and parity ideas, open a [GitHub issue](https://github.com/BlackRaincoat/versentry/issues). For what is implemented in detail, see [Configuration](configuration.md) and [Notifications](notifications.md).

If your requirements match the “not the best fit” cases above, one of those projects may serve you better. If you need notify-only monitoring on Docker with central rules and registry flexibility, Versentry is worth trying.
