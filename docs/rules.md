# Rules

Tag filters **narrow** candidates; semver still picks the newest matching tag in the **same major** (pre-release tags are kept when a rule is active, because suffixes like `-alpine3.24` are part of the line).

Back to [README](../README.md) · [Configuration overview](configuration.md)

## Two sources (priority)

1. **Config** `rules:` — exact `image` repo (e.g. `library/postgres`, `chatwoot/chatwoot`)
2. **Container label** `versentry.include` — regex for that container only
3. Neither → default semver (same major, drop pre-release / non-semver suffixes)

**Config wins over label.** Invalid regex in config → fail at startup. Invalid label regex → WARN and ignore (fallback to default), pass continues.

Digest mode (`latest`, non-semver tags) does **not** use rules — only local vs remote digest for the same tag.

```yaml
rules:
  - image: "library/postgres"
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

`image` must be the **normalized repo** (what Versentry logs / `imageref` produces): `library/nginx` not `nginx`, `chatwoot/chatwoot` not `docker.io/chatwoot/chatwoot`.

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
