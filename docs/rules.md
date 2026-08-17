# Rules

Tag filters **narrow** candidates; semver/numeric then pick the **newest** matching stable tag across majors (pre-release tags are kept when a rule is active, because suffixes like `-alpine3.24` are part of the line). Cross-major updates are notified and marked `(major)` in the default change text. To stay silent on newer majors, pin the line with `include` (e.g. `^2\.`) — there is no separate flag. Optional `track: digest` forces digest tracking for floating tags that still parse as semver.

This page is the **image** addressing axis (`rules[]` — detection). For **container** opt-out (`exclude_containers` / `versentry.watch`), see [Configuration — Container scope](configuration.md#container-scope-opt-out). Overview of both axes: [Addressing axes](configuration.md#addressing-axes-image-vs-container).

Back to [README](../README.md) · [Configuration overview](configuration.md)

## Two sources (priority)

1. **Config** `rules:` — image repo path as in compose (`postgres`, `chatwoot/chatwoot`, `gethomepage/homepage` for GHCR)
2. **Container labels** `versentry.include` / `versentry.track` — that container only
3. Neither → default detection (semver if the tag parses; otherwise digest)

**Config wins over labels — whole rule, not field-by-field.** If an image has a rule in config `rules:`, labels (`versentry.include`, `versentry.track`) for that image are **ignored** for **every** container running that image. The config rule overrides entirely, not per field. To set `track: digest` for an image that already has a config rule, add `track` on that config rule itself — not on a label. Labels apply only to images **without** a config rule.

**Practical consequence:** with a shared `rules` entry for `postgres`, you cannot give two Postgres containers different `include`/`track` via labels. Exclude one container from monitoring with `exclude_containers` (or `versentry.watch=false`) instead.

Invalid regex / unknown `track` in config → fail at startup. Invalid label values → WARN and ignore (that field; pass continues).

Default detection (no `track`):

1. Masterminds **semver** if the tag parses (including prerelease / suffixes like `17.10-alpine3.24`)
2. Else **numeric** if the tag is strictly dotted digits (`^v?\d+(\.\d+)+$`, typically 4+ segments such as `v0.63.1.3`)
3. Else **digest** (local vs remote digest for the **same** tag: `latest`, `pg17-trixie`, `9-alpine`, …)

`include` applies on the semver and numeric paths only. It filters **tag shape** (flavor suffixes, `v`-prefix, …) **and** can pin a **major line** — see [Include and major lines](#include-and-major-lines). Also [Digest diagnostics](#digest-diagnostics) and [Numeric tags](#numeric-tags).

```yaml
rules:
  - image: "postgres"
    include: "^17\\.\\d+-alpine3\\.\\d+$"
  - image: "chatwoot/chatwoot"
    include: "^v\\d+\\.\\d+\\.\\d+-ce$"
  - image: "valkey/valkey"
    track: digest
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
      versentry.track: digest
```

`image` is the **repository path** from your compose ref (strip tag and registry host): `postgres` for `postgres:17.10-alpine3.24`, `chatwoot/chatwoot` for `chatwoot/chatwoot:v4…`, `gethomepage/homepage` for `ghcr.io/gethomepage/homepage:v1…`. Do not include the registry host or tag.

On **Docker Hub only**, official single-name images (`postgres`, `caddy`, `nginx`, …) are stored internally as `library/<name>`; Versentry accepts either `postgres` or `library/postgres` in `rules.image` for those images. Other registries use the exact repo path with no `library/` alias.

## `track: digest`

Force **digest** detection for an image/container: compare local vs remote digest of the **current tag**, even when the tag parses as semver (e.g. `9-alpine` → `9.0.0-alpine`).

| When to use | Example |
|-------------|---------|
| Floating / line tags you want rebuild alerts for, not “newer version” | `valkey/valkey:9-alpine`, `redis:7-alpine`, `stable`, `mainline` |
| Usually unnecessary | Pinned `1.2.3` (rebuild tracking of an exact pin is rare) — allowed but uncommon |

Sources: config `track: digest`, or label `versentry.track=digest`. Only value supported: `digest`.

**With `include`:** if both are set on the same effective rule, `include` is ignored and Versentry logs a **one-time WARN** (`include applies only in semver mode`). The same WARN fires when `include` is set but the running tag is not semver (digest fallback). Not a config error.

A config rule may be `track`-only (no `include`):

```yaml
rules:
  - image: "valkey/valkey"
    track: digest
```

## Include and major lines

Versentry is notify-only: by default it selects the **newest** suitable tag (any major) and notifies if that tag is newer than the running one. Cross-major changes appear as `2.8.1 → 3.2.0 (major)` in default templates (`Bump=major` for custom templates).

There is **no** `allow_major` (or similar) flag. To ignore newer majors, narrow `include` to your line:

```yaml
rules:
  - image: "example/app"
    include: "^2\\."   # stay on major 2 — 3.x will not notify
```

**Footgun:** `include` does double duty — tag format **and** major pin. An old rule like `^2\\.` means “do not tell me about 3.0”, not only “prefer `2.*` spelling”. If you want major alerts again, widen or remove that include.

**0.x / patch lines (e.g. Metabase):** to stay on `0.63.1.*` rather than every newer `0.*` tag, use a tight `include` (e.g. `^v?0\\.63\\.1\\.\\d+$`). Same mechanism as pinning major 2.

## Bare integer candidates (CI / build numbers)

Registry tag lists often mix real versions with **bare integers** (`8400`, `2102` — pipeline/build IDs). Masterminds coerces `8400` → `8400.0.0`, so those tags would otherwise win as a huge “major”.

Versentry **excludes** tags matching `^v?\d+$` (optional `v`, digits only, **no dot**) from the semver/numeric **candidate** set.

Logging (one collapsed line per image, not per tag). Only bare tags that coerce to a version **strictly newer than current** are mentioned — older/equal bare majors (e.g. Nextcloud `10`…`19` under `34.x`, Authelia `4` under `4.39`) stay silent.

| Case | Level | When |
|------|-------|------|
| Claiming bare omitted | **DEBUG** | Bare tag looks newer than current but was not selected (ambiguous single-segment candidate), e.g. FreshRSS `8400` with `selected=1.29.1` |
| Degenerate gap | **WARN** (once) | Next major appears only as a bare integer (`16.9` + `17`, no `17.x`) — a version bump may have been missed |

Long claiming-tag lists are truncated (`tags=a,b,… (N total)`, first 10 shown).

| | Behavior |
|--|----------|
| Candidates `8400`, `8392` | Ignored for version choice |
| Current tag `postgres:16` / `redis:7` | Still **semver** mode (unchanged) — only candidates are filtered |
| `16` + [`16.9`, `17`, `17.6`] | Selects `17.6` (major); bare `17` dropped but point release wins |
| Degenerate: only bare `17`, no `17.x` | No major offered — rare; pin a point tag if you need that alert |

`include` does **not** re-enable bare integers as candidates (it filters after/along the same selection rules). Escape for floating majors is to run a multi-segment current tag (or accept digest tracking if you later opt into that model).

## Numeric tags

When Masterminds semver rejects a tag but it matches `^v?\d+(\.\d+)+$` (optional `v`, then only digits and dots — no letters or `-rc` / `.4a` suffixes), Versentry compares versions **segment by segment**. Missing trailing segments count as `0` (so `1.2.3` is older than `1.2.3.4`). The first segment is the major for bump classification (`Bump`) and for pinning via `include` — same as semver.

`versentry links` shows MODE `numeric` for this path. Notification URLs follow the semver-style link rules (release list / registry tag page).

## Digest diagnostics

Digest tracking of a floating tag only sees **rebuilds of that exact tag**, not “`pg17-trixie` rebuilt vs a new tag name”. Tags that look like versions but fail both semver and strict numeric still fall through here.

Versentry surfaces the trap:

| Signal | When |
|--------|------|
| One-time **WARN** | Tag is neither semver nor strict numeric → digest fallback — *newer version tags will not be detected* |
| One-time **WARN** | `include` is set but the container is on digest (`digest=rule` or `digest=auto`) — rule is dead weight |
| `versentry links` **MODE** | `digest(rule)` vs `digest(auto)` vs `semver` / `numeric` — see [Commands — links](commands.md#links) |

**Exception:** tag `latest` (Docker’s default floating tag) still uses `digest(auto)` in `links`, but does **not** emit the digest-fallback WARN — that nature is expected, not a surprise. Other floating names (`stable`, `main`, `edge`, …) still WARN.

WARN keys are remembered for the process lifetime (daemon restart clears them).

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
