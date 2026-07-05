# Registries

Back to [README](../README.md) · [Configuration overview](configuration.md)

## Public registries (automatic)

**Public registries work automatically** — you do not need to list them. Versentry registers anonymous OCI clients for:

| Host | Registry |
|------|----------|
| `index.docker.io` | Docker Hub (`nginx` → `library/nginx`) |
| `ghcr.io` | GitHub Container Registry |
| `quay.io` | Quay |
| `registry.gitlab.com` | GitLab Container Registry |

Use `registries:` **only** for private or self-hosted hosts, or to attach credentials to a public host (that entry **overrides** the automatic anonymous client for the same host).

## Private and self-hosted (`type: oci`)

```yaml
registries:
  - type: oci
    config:
      host: "git.example.com"          # required
      username: "user"                 # optional; both username and token, or neither
      token: "your-registry-token"
      insecure: false                  # optional; true for HTTP (no TLS)
      #proxy: "socks5://..."           # optional; overrides registry_proxy for this host
```

Only `type: oci` is registered. Duplicate hosts (two `oci` entries, or `oci` conflicting with another entry on the same host) fail at startup.

For a single private registry, credentials can live in env instead of YAML:

| Variable | YAML field |
|----------|------------|
| `VERSENTRY_REGISTRY_USERNAME` | `username` on every `oci` entry |
| `VERSENTRY_REGISTRY_TOKEN` | `token` on every `oci` entry |

Env overrides YAML. Multiple `oci` entries with **different** credentials still belong in YAML (read-only mount).

## Global registry proxy

`registry_proxy` / `VERSENTRY_REGISTRY_PROXY` (HTTP or `socks5://`) applies to **all** OCI registry traffic, including bearer-token requests (e.g. `auth.docker.io`) — auth does not bypass the proxy. This is separate from notifier proxies (Telegram `proxy` / `VERSENTRY_TELEGRAM_PROXY`). Per-registry `proxy` in an `oci` config overrides the global setting for that host.

Without `registry_proxy`, **SOCKS5 is not applied** to registries. HTTP proxies may still be picked up from `HTTP_PROXY` / `HTTPS_PROXY` in the environment (default `go-containerregistry` behavior); SOCKS5 requires an explicit `registry_proxy`.

```yaml
registry_proxy: "socks5://user:pass@host:1080"
```

## Per-pass cache and rate limits

**Per-pass cache:** each check pass deduplicates `ListTags` / `TagDigest` by `host/repo` (digest mode: `host/repo#tag`). Multiple containers on the same image hit the registry once per pass. Cache is reset between passes.

**Rate limits (HTTP 429):** one short `Retry-After` retry (≤10s) per request; if still rate-limited or `Retry-After` is long/missing, the host is skipped for the rest of the pass (`registry rate limited, will retry next pass`). Other registry hosts are unaffected. Persistent 5xx after transport retries skips the image only (not the whole host).

See also [Configuration — registry behavior](configuration.md#registry-behavior-engine).
