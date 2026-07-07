# Rules

Tag filters **narrow** candidates; semver still picks the newest matching tag in the **same major** (pre-release tags are kept when a rule is active, because suffixes like `-alpine3.24` are part of the line).

Back to [README](../README.md) · [Configuration overview](configuration.md)

## Two sources (priority)

1. **Config** `rules:` — image repo path as in compose (`postgres`, `chatwoot/chatwoot`, `gethomepage/homepage` for GHCR)
2. **Container label** `versentry.include` — regex for that container only
3. Neither → default semver (same major, drop pre-release / non-semver suffixes)

**Config wins over label.** Invalid regex in config → fail at startup. Invalid label regex → WARN and ignore (fallback to default), pass continues.

Digest mode (`latest`, non-semver tags) does **not** use rules — only local vs remote digest for the same tag.

```yaml
rules:
  - image: "postgres"
    include: "^17\\.\\d+-alpine3\\.\\d+$"
  - image: "chatwoot/chatwoot"
    include: "^v\\d+\\.\\d+\\.\\d+-ce$"
```

Label (Compose):

```yaml
services:
  db:
    image: postgres:17.5-alpine3.20
    labels:
      versentry.include: "^17\\.\\d+-alpine3\\.\\d+$"
```

`image` is the **repository path** from your compose ref (strip tag and registry host): `postgres` for `postgres:17.10-alpine3.24`, `chatwoot/chatwoot` for `chatwoot/chatwoot:v4…`, `gethomepage/homepage` for `ghcr.io/gethomepage/homepage:v1…`. Do not include the registry host or tag.

On **Docker Hub only**, official single-name images (`postgres`, `caddy`, `nginx`, …) are stored internally as `library/<name>`; Versentry accepts either `postgres` or `library/postgres` in `rules.image` for those images. Other registries use the exact repo path with no `library/` alias.

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
