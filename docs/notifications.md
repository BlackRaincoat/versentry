# Notifications

Notifiers receive **all updates from one check pass** as a batch. Empty pass → nothing sent.

Back to [README](../README.md) · [Configuration overview](configuration.md)

Implemented types: `stdout`, `telegram`, `webhook`, `discord`.

## Defaults by channel

Each notifier has its own config block and defaults. Templates are **not** shared across channels.

| Channel | Default `mode` | Custom templates |
|---------|----------------|------------------|
| `telegram` | `digest` | `item_template` + `digest_template` (HTML) |
| `discord` | `digest` | single `template` = full webhook JSON body (optional) |
| `webhook` | `digest` | single `template` = full HTTP body (optional) |
| `stdout` | — | none (fixed log lines) |

**Default `digest`:** one delivery per check pass (instance header + all updates). Set `mode: simple` when you want one message/POST per container update.

## stdout

No config; prints each update to stdout.

## telegram

Bot API `sendMessage`. Optional `proxy` applies **only** to Telegram; registry traffic is direct.

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `token` | yes | — | Bot token |
| `chat_id` | yes | — | Chat or channel id (string or number) |
| `parse_mode` | no | `HTML` | Telegram parse mode |
| `proxy` | no | — | `socks5://…` or `http://…` (password redacted in logs) |
| `api_url` | no | `https://api.telegram.org` | Custom Bot API base |
| `timeout` | no | `10s` | HTTP + dial/proxy handshake timeout (per attempt) |
| `retries` | no | `3` | HTTP attempts per message (`0` or `1` = no retries) |
| `retry_delay` | no | `1s` | Initial backoff delay between retries (doubled each time) |
| `mode` | no | `digest` | `simple` or `digest` |
| `item_template` | no | built-in | Go `text/template` for one update |
| `digest_template` | no | built-in | Wrapper for a batch of items |

Delivery retries transient failures automatically: network errors and HTTP `5xx` use exponential backoff (`retry_delay`, doubled each attempt, up to `retries` attempts). HTTP `429` waits for Telegram `retry_after` (capped at 60s; larger values fail fast). HTTP `400` / `401` / `403` / `404` are not retried (configuration errors). Each failed retry logs **WARN**; final failure logs **ERROR**.

Link previews are disabled (`disable_web_page_preview=true`).

Set credentials via env (`VERSENTRY_TELEGRAM_TOKEN`, `VERSENTRY_TELEGRAM_CHAT_ID`, `VERSENTRY_TELEGRAM_PROXY`) or YAML — see [Configuration — environment variables](configuration.md#environment-variables).

## webhook

Generic HTTP POST (ntfy, Gotify, custom hooks). Shared HTTP retry helper with Telegram. Logs only the **host** of the URL, never path or query (tokens often live there).

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `url` | yes | — | Webhook endpoint (`http://` or `https://`) |
| `mode` | no | `digest` | `simple` (one POST per update) or `digest` (one POST per batch) |
| `template` | no | built-in JSON | Go `text/template` body; see below |
| `headers` | no | — | Custom HTTP headers; values support `os.ExpandEnv` (`${TOKEN}`) |
| `proxy` | no | — | Optional `socks5://…` or `http://…` |
| `timeout` | no | `10s` | HTTP timeout per attempt |
| `retries` | no | `3` | HTTP attempts per POST |
| `retry_delay` | no | `1s` | Initial backoff between retries |

Set `url`, `Authorization`, and `proxy` via env (`VERSENTRY_WEBHOOK_URL`, `VERSENTRY_WEBHOOK_AUTHORIZATION`, `VERSENTRY_WEBHOOK_PROXY`) or YAML — see [Configuration — environment variables](configuration.md#environment-variables).

**Default JSON payload** (no `template`): envelope with `instance`, `count`, and `updates[]`. Each update has `container`, `image`, `host`, `current_tag`, `latest_tag`, `change`, `url`, `mode` (`semver` or `digest`). In `simple` mode the same envelope is sent with `count: 1`.

**Custom `template`:** `simple` mode receives one update as `ItemData`. `digest` mode receives the full payload struct (`Instance`, `Count`, `Updates` slice). Default `Content-Type` is `application/json`; override via `headers`.

```yaml
notifiers:
  - type: webhook
    config:
      # or VERSENTRY_WEBHOOK_URL + VERSENTRY_WEBHOOK_AUTHORIZATION env
      url: "https://example.com/hooks/versentry"
      mode: digest
      headers:
        Authorization: "Bearer ${WEBHOOK_TOKEN}"
```

```yaml
  - type: webhook
    config:
      url: "https://ntfy.example.com/my-topic"
      mode: simple
      template: |
        {"topic":"my-topic","title":"{{.Container}}","message":"{{.Change}}"}
```

## discord

First-class Discord webhook notifier (rich embeds by default). Create a webhook in Discord: **Server Settings → Integrations → Webhooks → New Webhook**. The URL contains a secret token — it is never logged (only `discord.com` host in logs).

Set `url` via `VERSENTRY_DISCORD_WEBHOOK_URL` or YAML — see [Configuration — environment variables](configuration.md#environment-variables).

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `url` | yes | — | Discord webhook URL (`https://discord.com/api/webhooks/…`) |
| `mode` | no | `digest` | `simple` (one message per update) or `digest` (one batch) |
| `format` | no | `embed` | `embed` (rich cards) or `content` (plain text) |
| `color` | no | `3447003` | Embed color (decimal; default Discord blurple) |
| `username` | no | — | Override webhook bot display name |
| `template` | no | built-in | Advanced: full JSON body override (Go `text/template`) |
| `timeout` | no | `10s` | HTTP timeout per attempt |
| `retries` | no | `3` | HTTP attempts per message |
| `retry_delay` | no | `1s` | Initial backoff between retries |

**Default embed format (`format: embed`):**

- **digest:** title `📦 {instance} — N updates` (count omitted when N=1); description lists each update (`container`, change, link). The configured `color` is set on every embed (including overflow splits).
- **simple:** one embed per update (title = `📦 {instance}`; description = container, change, link on following lines). Each embed uses the configured `color`.

**Discord limits** (enforced automatically — large digests are split, no updates lost):

| Limit | Value |
|-------|-------|
| embed description | 4096 chars |
| embed title | 256 chars |
| embeds per message | 10 |
| total embed chars per message | 6000 |
| content field (`format: content`) | 2000 chars |

Overflow → multiple embeds per message, then multiple POST messages if needed.

**`format: content`:** plain `{"content": "…"}` instead of embeds; same text builders, split at 2000 chars.

**429:** Discord rate limits are retried with `Retry-After` (header or JSON `retry_after` in seconds), same cap as other HTTP notifiers.

```yaml
notifiers:
  - type: discord
    config:
      url: "https://discord.com/api/webhooks/123456789/abcdef..."
      mode: digest
      format: embed
      color: 5814783
      username: "Versentry"
```

### Default Discord layout (no `template`)

With `format: embed` (default), Versentry builds embeds in code — there are no `item_template` / `digest_template` keys (unlike Telegram).

**digest + embed:** title `📦 instance — N updates` (count omitted when N=1); description lists each update as markdown `**container**: change` plus URL on the next line.

**simple + embed:** one embed per update; title `📦 instance`; description = one item line.

**`format: content`:** same text as embed mode, sent as plain `content` (split at 2000 chars).

User-controlled text is escaped for Discord markdown (`*`, `_`, `` ` ``, etc.).

### Custom `template` (advanced)

When `template` is set, it **replaces** the built-in embed/content builders. You must emit valid [Discord webhook JSON](https://discord.com/developers/docs/resources/webhook#execute-webhook) yourself.

| `mode` | Template context |
|--------|------------------|
| `simple` | `ItemData`: `{{.Instance}}`, `{{.Container}}`, `{{.Change}}`, `{{.URL}}`, … |
| `digest` | `Payload`: `{{.Instance}}`, `{{.Count}}`, `{{.Updates}}` (slice of update objects) |

Values are **not** HTML-escaped; escape markdown yourself if you use `content` or embed descriptions.

```yaml
notifiers:
  - type: discord
    config:
      url: "https://discord.com/api/webhooks/123456789/abcdef..."
      mode: simple
      template: |
        {"content": "📦 {{.Instance}}\n**{{.Container}}**: {{.Change}}"}
```

```yaml
  - type: discord
    config:
      url: "https://discord.com/api/webhooks/123456789/abcdef..."
      mode: digest
      template: |
        {"embeds":[{"title":"📦 {{.Instance}}","description":"{{.Count}} update(s) — see logs for details","color":3447003}]}
```

## Modes (telegram, webhook, discord)

| `mode` | Behavior |
|--------|----------|
| `simple` | One delivery per update |
| `digest` | One summary for the whole pass (default for telegram, discord, webhook) |

## Notification URLs

`{{.URL}}` / per-update `url` is built from the image OCI label **`org.opencontainers.image.source`** (baked in by the image author) plus registry host and tracking mode. Versentry does not invent a project homepage beyond that metadata. Preview what a running fleet would get with [`versentry links`](commands.md#links).

| Situation | Result |
|-----------|--------|
| No `source` label | GitHub / GHCR pkgs links are not built. Docker Hub may still get a registry tag page; GHCR or an unknown registry without the label → empty URL. |
| `source` is a docker wrapper repo (e.g. `caddyserver/caddy-docker`, `linuxserver/docker-bookstack`) | Link follows that label (e.g. `{source}/releases`). Releases may be empty or for the wrapper, not the upstream project. That is an image-metadata limit — Versentry uses the declared label and cannot reliably map to a “canonical” project repo. |
| Semver mode + GitHub `source` | `{source}/releases` (release **list**). Not `/releases/tag/…` — git tag shapes are not guessed from Docker tags. |
| Digest mode | Registry image page (Hub `?tag=`, Quay tags, GHCR pkgs when `source` is set) — never a git release page. |

## Template variables

Shared field builders live in `internal/notifier/format` (`ItemData`, `Payload`). **Which keys you can set depends on the notifier** — see [Defaults by channel](#defaults-by-channel).

| Notifier | Config keys | Escaping |
|----------|-------------|----------|
| `telegram` | `item_template`, `digest_template` | values HTML-escaped; markup in template kept |
| `discord` | `template` (full JSON body) | no auto-escape in custom template; built-in embed mode escapes markdown |
| `webhook` | `template` (full body) | no HTML escape |

### `ItemData` (telegram templates; discord/webhook `simple` template)

| Variable | Meaning |
|----------|---------|
| `{{.Instance}}` | Instance name (config or hostname) |
| `{{.Container}}` | Container name |
| `{{.Image}}` | Image repo (`library/caddy`) |
| `{{.Tag}}` | Current tag (`event.CurrentTag`) |
| `{{.Change}}` | Ready-made change line: `2.11.3 → 2.11.4` or `digest changed: abc123… → def456…` |
| `{{.URL}}` | Web link (may be empty) — see [Notification URLs](#notification-urls) |
| `{{.CurrentTag}}` | Current tag (same value as `Tag`) |
| `{{.LatestTag}}` | Newer tag (empty for digest-only updates) |
| `{{.Host}}` | Registry host |

### Digest wrapper (telegram `digest_template`)

`{{.Instance}}`, `{{.Count}}`, `{{.Items}}` — `Items` is pre-rendered item lines concatenated with **no** separator.

### Webhook / Discord `digest` template

`Payload` struct: `{{.Instance}}`, `{{.Count}}`, `{{.Updates}}` — each update has `container`, `image`, `host`, `current_tag`, `latest_tag`, `change`, `url`, `mode`.

## Default Telegram templates

Simple mode sends one message per update using the digest wrapper with a single item (instance header + item line). Digest mode batches multiple items under one header.

Item lines (instance is in the digest header, not repeated per item):

```
<b>{{.Container}}</b>: {{.Change}}{{if .URL}}
{{.URL}}{{end}}
```

Digest:

```
📦 {{.Instance}}{{if gt .Count 1}} — {{.Count}} updates{{end}}
{{.Items}}
```

With one update, the count suffix is omitted (`📦 hostname` only).

## Spacing between digest items

Items are concatenated with an empty separator. All spacing between entries is owned by `item_template` — there is no `item_separator` option.

**Compact (default):** the built-in item template ends with a single newline, so entries form consecutive lines with no blank line between them.

```yaml
item_template: |
  <b>{{.Container}}</b>: {{.Change}}{{if .URL}}
  {{.URL}}{{end}}
```

**Blank line between items:** use YAML block scalar with the keep-chomp modifier (`|+`). Plain `|` strips a final blank line.

```yaml
item_template: |+
  <b>{{.Container}}</b>: {{.Change}}
```

(The empty line at the end of the block is intentional — that is the blank line between digest entries after concatenation.)

## Proxy (Telegram)

`proxy` on the telegram notifier only (`socks5://user:pass@host:1080` or `http://…`). Registry checks never use this proxy.

## Examples

### (a) Minimal — public images + Telegram simple

```yaml
provider:
  type: docker

notifiers:
  - type: telegram
    config:
      parse_mode: HTML
```

Set `VERSENTRY_TELEGRAM_TOKEN` and `VERSENTRY_TELEGRAM_CHAT_ID` in the environment (or uncomment `token` / `chat_id` in config).

### (b) Rules + private OCI registry + proxy

<a id="b-rules--private-oci-registry--proxy"></a>

```yaml
instance_name: "prod-docker-01"

provider:
  type: docker

registries:
  - type: oci
    config:
      host: "git.example.com"
      # or VERSENTRY_REGISTRY_USERNAME / VERSENTRY_REGISTRY_TOKEN env
      username: "deploy"
      token: "glpat-..."

rules:
  - image: "postgres"
    include: "^17\\.\\d+-alpine3\\.\\d+$"
  - image: "chatwoot/chatwoot"
    include: "^v\\d+\\.\\d+\\.\\d+-ce$"

notifiers:
  - type: stdout
  - type: telegram
    config:
      parse_mode: HTML
      proxy: "socks5://user:pass@127.0.0.1:1080"   # or VERSENTRY_TELEGRAM_PROXY env

timeouts:
  provider: 10s
  registry: 30s

interval: 12h
log_level: info
```

### (c) Digest mode + custom template

Blank line between items (`|+`) and a header that always includes the count:

```yaml
instance_name: "prod-docker-01"

provider:
  type: docker

notifiers:
  - type: telegram
    config:
      mode: digest
      item_template: |+
        <b>{{.Container}}</b>: {{.Change}}
      digest_template: |
        📦 {{.Instance}} — {{.Count}} updates
        {{.Items}}
```

Set `VERSENTRY_TELEGRAM_TOKEN` and `VERSENTRY_TELEGRAM_CHAT_ID` in the environment.
