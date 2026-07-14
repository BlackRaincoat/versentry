# Rules

Tag filters **narrow** candidates; semver still picks the newest matching tag in the **same major** (pre-release tags are kept when a rule is active, because suffixes like `-alpine3.24` are part of the line). Optional `mode: digest` forces digest tracking for floating tags that still parse as semver.

Back to [README](../README.md) · [Configuration overview](configuration.md)

## Two sources (priority)

1. **Config** `rules:` — image repo path as in compose (`postgres`, `chatwoot/chatwoot`, `gethomepage/homepage` for GHCR)
2. **Container labels** `versentry.include` / `versentry.mode` — that container only
3. Neither → default detection (semver if the tag parses; otherwise digest)

**Config wins over labels — whole rule, not field-by-field.** If an image has a rule in config `rules:`, labels (`versentry.include`, `versentry.mode`) for that image are **ignored**. The config rule overrides entirely, not per field. To set `mode: digest` for an image that already has a config rule, add `mode` on that config rule itself — not on a label. Labels apply only to images **without** a config rule.

Invalid regex / unknown `mode` in config → fail at startup. Invalid label values → WARN and ignore (that field; pass continues).

Default detection (no `mode`): non-semver tags (`latest`, `pg17-trixie`, …) use digest (local vs remote digest for the same tag). Semver-parsable tags use the semver path; `include` only applies there.

```yaml
rules:
  - image: "postgres"
    include: "^17\\.\\d+-alpine3\\.\\d+$"
  - image: "chatwoot/chatwoot"
    include: "^v\\d+\\.\\d+\\.\\d+-ce$"
  - image: "valkey/valkey"
    mode: digest
```

Label (Compose):

```yaml
services:
  db:
    image: postgres:17.5-alpine3.20
    labels:
      versentry.include: "^17\\.\\d+-alpine3\\.\\d+$"
  cache:
    image: valkey/valkey:9-alpine
    labels:
      versentry.mode: digest
```

`image` is the **repository path** from your compose ref (strip tag and registry host): `postgres` for `postgres:17.10-alpine3.24`, `chatwoot/chatwoot` for `chatwoot/chatwoot:v4…`, `gethomepage/homepage` for `ghcr.io/gethomepage/homepage:v1…`. Do not include the registry host or tag.

On **Docker Hub only**, official single-name images (`postgres`, `caddy`, `nginx`, …) are stored internally as `library/<name>`; Versentry accepts either `postgres` or `library/postgres` in `rules.image` for those images. Other registries use the exact repo path with no `library/` alias.

## `mode: digest`

Force **digest** detection for an image/container: compare local vs remote digest of the **current tag**, even when the tag parses as semver (e.g. `9-alpine` → `9.0.0-alpine`).

| When to use | Example |
|-------------|---------|
| Floating / line tags you want rebuild alerts for, not “newer version” | `valkey/valkey:9-alpine`, `redis:7-alpine`, `stable`, `mainline` |
| Usually unnecessary | Pinned `1.2.3` (rebuild tracking of an exact pin is rare) — allowed but uncommon |

Sources: config `mode: digest`, or label `versentry.mode=digest`. Only value supported: `digest`.

**With `include`:** if both are set on the same effective rule, `include` is ignored and Versentry logs **WARN** (`include applies only in semver mode`). Not a config error.

A config rule may be `mode`-only (no `include`):

```yaml
rules:
  - image: "valkey/valkey"
    mode: digest
```

## Regex escaping (common footgun)

The include pattern is a **Go regex**. How many backslashes you need depends on the file format.

| Where | How to write “digit” (`\d`) | Why |
|-------|----------------------------|-----|
| `config.yaml` string | `"^17\\.\\d+$"` | YAML parses the string first: `\\` → `\`, then Go regex sees `\d` |
| Compose label **in quotes** | `"^17\\.\\d+$"` | Same as YAML — quoted string, double the backslash |
| Compose label **unquoted** | `^17\.\d+$` | No YAML string escape; a single `\` reaches the regex engine |

**Wrong in `config.yaml`:** `include: "^17\.\d+$"` — YAML may eat or mis-parse `\d`, and the rule will not match what you expect.

**Right in `config.yaml`:**

```yaml
include: "^17\\.\\d+-alpine3\\.\\d+$"
```

**Right in Compose (quoted):**

```yaml
labels:
  versentry.include: "^17\\.\\d+-alpine3\\.\\d+$"
```

**Right in Compose (unquoted):**

```yaml
labels:
  versentry.include: ^17\.\d+-alpine3\.\d+$
```

## Example: rules + private registry

See [Notifications — full stack example](notifications.md#b-rules--private-oci-registry--proxy).
