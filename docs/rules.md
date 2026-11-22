# Rules

Tag filters **narrow** candidates; semver/numeric then pick the **newest** matching stable tag across majors (pre-release tags are kept when a rule is active, because suffixes like `-alpine3.24` are part of the line). Cross-major updates are notified and marked `(major)` in the default change text. To stay silent on newer majors, pin the line with `include` (e.g. `^2\.`) — there is no separate flag. Optional `track: digest` forces digest tracking for floating tags that still parse as semver.

This page is the **image** addressing axis (`rules[]` — detection). For **container** opt-out (`exclude_containers` / `versentry.watch`), see [Configuration — Container scope](configuration.md#container-scope-opt-out). Overview of both axes: [Addressing axes](configuration.md#addressing-axes-image-vs-container).

Back to [README](../README.md) · [Configuration overview](configuration.md)

## Two sources (priority)

1. **Config** `rules:` — the base for an image (repo path as in compose: `postgres`, `chatwoot/chatwoot`, `gethomepage/homepage` for GHCR)
2. **Container labels** `versentry.include` / `versentry.exclude` / `versentry.track` / `versentry.min_age` / `versentry.ignore_rebuilds` — override the config rule for that container only
3. Neither → default detection (semver if the tag parses; otherwise digest)

**Config is the base, labels override per container, field by field.** A config rule applies to every container of that image. A container's own labels override only the fields they set and inherit the rest:

| Field | Config `rules[]` | Label (overrides for that container) |
|-------|------------------|--------------------------------------|
| `include` | `include:` | `versentry.include` |
| `exclude` | `exclude:` | `versentry.exclude` |
| `track` | `track: digest` | `versentry.track=digest`, or `versentry.track=auto` to undo a config `track: digest` (default detection: semver → numeric → digest) |
| `min_age` | `min_age:` | `versentry.min_age` (`3d`, `36h`, `0` = off) |
| `ignore_rebuilds` | `ignore_rebuilds: true` / `false` | `versentry.ignore_rebuilds` (`true` / `false`) — see [Ignore rebuilds](configuration.md#ignore-rebuilds-ignore_rebuilds) |

Example: config `postgres` → `include: "^17\\."`, `min_age: 3d`; one container labeled `versentry.include=^16\.` follows the 16 line and still waits 3 days. Other `postgres` containers keep `^17\.`.

When labels change a config rule, Versentry logs a one-time **INFO** `container labels override config rule` with `fields=include,track,…`, so central policy shows where local exceptions are.

**Only labels you set count.** Versentry reads `versentry.*` labels from the container as created by your Compose file or `docker run`. The same keys baked into an image by its publisher (`LABEL versentry.include=…` in their Dockerfile) are ignored, so a third-party image cannot override your central rules or opt itself in. Docker copies image labels onto the container, so a `versentry.*` value identical to the image's is treated as coming from the image. Rare edge: if an image does bake a `versentry.*` label, setting the **same** value on the container has no effect either — use the config (`rules[]`, `exclude_containers`, `watch_containers`) for that image.

**Switching detection with a label** (`track` digest ↔ auto) changes what Versentry remembers for that container, so it may notify about it once — same as changing the config rule.

Invalid regex / unknown `track` in config → fail at startup. Invalid label values → WARN and ignore that label field; the config rule's value for that field (if any) still applies, and the pass continues.

**Selection is the one place where config is not overridden by a label:** `exclude_containers` beats `versentry.watch=true`. Exclusion wins from either source (`versentry.watch=false` also beats `watch_containers`) — see [Container scope](configuration.md#container-scope-opt-out).

Default detection (no `track`):

1. Masterminds **semver** if the tag parses (including prerelease / suffixes like `17.10-alpine3.24`)
2. Else **numeric** if the tag is strictly dotted digits (`^v?\d+(\.\d+)+$`, typically 4+ segments such as `v0.63.1.3`)
3. Else **digest** (local vs remote digest for the **same** tag: `latest`, `pg17-trixie`, `9-alpine`, …)

`include` and `exclude` apply on the semver and numeric paths only. It filters **tag shape** (flavor suffixes, `v`-prefix, …) **and** can pin a **major line** — see [Include and major lines](#include-and-major-lines). Also [Digest diagnostics](#digest-diagnostics) and [Numeric tags](#numeric-tags).

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

Sources: config `track: digest`, or label `versentry.track=digest`. Config accepts only `digest`; a label may also be `auto` to cancel a config `track: digest` for that one container.

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

## Exclude

`exclude` is the opposite of `include`: it **drops** registry tags that match, and keeps everything else. Use it when the default choice is almost right and only some tags get in the way — release candidates, a flavour you never run, a line you are not ready for:

```yaml
rules:
  - image: "example/app"
    exclude: "-(rc|beta|alpha)"        # never offer pre-release spellings
  - image: "grafana/grafana"
    exclude: "^12\\."                    # never offer 12.x; the newest other version (11.x or 13+) still is
  - image: "postgres"
    include: "-alpine$"                # alpine flavour only …
    exclude: "^1[89]"                  # … but not 18 / 19 yet
```

| | Behavior |
|--|----------|
| Order | `include` first (if set), then `exclude` on what is left, then the usual newest-version choice |
| Tag matching both | removed (exclude wins) |
| `exclude` only | default selection on the remaining tags (stable releases, any major) |
| Running tag matches `exclude` | still checked — `exclude` filters **candidates**, not the container (`1.0.0-rc1` still sees `1.0.0`) |
| Nothing left after `exclude` | container skipped with `no tags left after exclude rule` |
| Digest tracking | ignored, with a one-time WARN `exclude rule ignored` (same as `include`) |
| Label `versentry.exclude` | replaces the config `exclude` for that container (one-time INFO `fields=exclude`); invalid regex → WARN, config value kept |

`include` and `exclude` are regular expressions with the same escaping rules — see [Regex escaping](#regex-escaping-common-footgun).

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

`include` does **not** re-enable bare integers as candidates (it filters after/along the same selection rules). Escape for floating majors is to run a multi-segment current tag, or `track: digest` for rebuild alerts — see [Floating tags on the semver path](#floating-tags-on-the-semver-path).

## Numeric tags

When Masterminds semver rejects a tag but it matches `^v?\d+(\.\d+)+$` (optional `v`, then only digits and dots — no letters or `-rc` / `.4a` suffixes), Versentry compares versions **segment by segment**. Missing trailing segments count as `0` (so `1.2.3` is older than `1.2.3.4`). The first segment is the major for bump classification (`Bump`) and for pinning via `include` — same as semver.

`versentry links` shows MODE `numeric` for this path. Notification URLs follow the semver-style link rules (release list / registry tag page).

## Floating tags on the semver path

Tags like `postgres:16`, `redis:7-alpine`, or `nginx:1.27` parse as semver, so Versentry compares **versions**: `16` is treated as `16.0.0` and the newest suitable tag wins — often a new major (`16 → 18.1 (major)`). That answers *“is there a newer line?”*, never *“did my tag move?”*. When `16` is re-pointed from 16.9 to 16.10 (or rebuilt on a patched base image), that is exactly what `docker pull` would fetch, yet semver has nothing new to report.

Detection is **not** changed for these tags (switching them to digest would silently hide new majors). Versentry points the gap out instead:

| Signal | When |
|--------|------|
| One-time **INFO** on `check` / `run` | The registry has more specific tags under the current tag's numeric core (`16` → `16.10`, `1.27` → `1.27.5`, `7-alpine` → `7.4.11-alpine`) — `floating tag on semver path … add track: digest for rebuild alerts` with `line_tag=` as evidence |
| `versentry links` MODE `semver(floating)` | Current tag is a bare major (`16`, `7-alpine`, `v2`) — decided from the tag alone, since `links` makes no registry calls |

A two-component tag (`1.27`) is flagged only by the INFO, because without the tag list it cannot be told apart from a pinned release: `postgres:16.4` has no `16.4.x` tags and stays silent. Full `x.y.z` tags are never flagged.

Choose per image:

- **Newer versions** (default): keep semver; pin a line with `include` if majors are noise.
- **Rebuild alerts for the tag you run**: `track: digest` (config rule or label `versentry.track=digest`). You then lose the “newer major exists” signal for that image.

## Digest diagnostics

Digest tracking of a floating tag only sees **rebuilds of that exact tag**, not “`pg17-trixie` rebuilt vs a new tag name”. Tags that look like versions but fail both semver and strict numeric still fall through here.

Versentry surfaces the trap:

| Signal | When |
|--------|------|
| One-time **WARN** | Tag is neither semver nor strict numeric → digest fallback — *newer version tags will not be detected* |
| One-time **WARN** | `include` is set but the container is on digest (`digest=rule` or `digest=auto`) — rule is dead weight |
| `versentry links` **MODE** | `digest(rule)` vs `digest(auto)` vs `semver` / `semver(floating)` / `numeric` — see [Commands — links](commands.md#links) |

**Exception:** tag `latest` (Docker’s default floating tag) still uses `digest(auto)` in `links`, but does **not** emit the digest-fallback WARN — that nature is expected, not a surprise. Other floating names (`stable`, `main`, `edge`, …) still WARN.

WARN / INFO keys are remembered for the process lifetime (daemon restart clears them).

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
