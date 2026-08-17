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

**Timeouts:** `timeouts.registry` (default 30s) is the overall ceiling for one `ListTags` / `TagDigest` call. Separately, each HTTP attempt uses short dial / TLS / response-header budgets (~5s / ~8s / ~10s) so a stuck TCP/TLS socket cannot consume the full ceiling. go-containerregistry retries Temporary network errors (including header timeout) on the same transport; Versentry does not add a second reconnect loop around that.

Observed Hub hangs were a live HTTP/2 connection with a stuck stream (`http2: timeout awaiting response headers`, `reused=true` on the same remote), not a dead keep-alive with no frames. `ResponseHeaderTimeout` cancels that stream. Versentry then closes that connection's TCP socket so go-containerregistry's Temporary retry dials a new one instead of reusing the stuck mux. DEBUG `evicted hung http2 connection` (`remote`, `repo`) is that close; `registry_http` (`phase`, `reused`, `idle_ms`, `http_trips`) remains the per-request diagnostic. Dial/TLS timeouts, 429, and SIGTERM/cancel do not evict. `RunOnce` talks to registries sequentially (one container at a time), so closing the mux does not abort a sibling ListTags.

Eviction only fixes **reuse of a stuck connection**. Fleet confirmation: hang on `reused=true` → evict → next trip `reused=false` → success, shorter duration. A later skip on a large repo (e.g. `library/redis`) is a different class: Hub can stall on `GET …/tags/list?n=1000` (thousands of tags) while **the same remote** immediately answers `/v2/` ping and small `tags/list` for other images (`reused=true`, hundreds of ms). Fresh dials for that heavy request can hang too — the socket is live; the expensive listing is not. gcr still retries Temporary errors until `timeouts.registry` (default 30s) expires, then that image is skipped for this pass. That skip is expected, not a failure of eviction. Do not add an earlier give-up: the overall ceiling already bounds the wait. At DEBUG the two classes are distinct: helped = `evicted` then a successful `registry_http` with `reused=false`; heavy-list stall = `evicted` (even on `reused=false`) for that `tags/list`, then `list_tags incomplete` with `http_trips` and `last_phase=waiting_headers`, while later trips on the same remote for other paths succeed.

On hang or network failure the image is skipped and the pass continues; it does not stop `versentry run`. SIGTERM/SIGINT (`context.Canceled`) still aborts the pass.

See also [Configuration — registry behavior](configuration.md#registry-behavior-engine).
