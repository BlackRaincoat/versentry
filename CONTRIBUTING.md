# Contributing to Versentry

Thank you for considering a contribution. Versentry is **notify-only**: it detects image updates and sends alerts. It does not pull images or restart containers. See [docs/comparison.md](docs/comparison.md) for scope and alternatives.

## Ways to contribute

| Type | What to do |
|------|------------|
| **Bug** | Open a [bug report](https://github.com/BlackRaincoat/versentry/issues/new?template=bug_report.yml) with steps to reproduce, version, and redacted config/logs |
| **Feature** | Open a [feature request](https://github.com/BlackRaincoat/versentry/issues/new?template=feature_request.yml). Large or API-changing work should be discussed before a PR |
| **Docs** | Fix typos or clarify `README.md` / `docs/` — small PRs welcome without an issue |
| **Code** | Fork, branch, PR (see below) |

Out of scope by design: web UI, auto-update / Watchtower-style actions, in-app container management.

## Development setup

Requirements: **Go 1.25+**, Docker (for integration with the local socket when testing manually).

```bash
git clone https://github.com/BlackRaincoat/versentry.git
cd versentry
go test ./...
go run ./cmd/versentry check -c config.example.yaml
```

Copy `config.example.yaml` to `config.yaml` for local runs. Do not commit secrets; use `VERSENTRY_*` environment variables (see [docs/configuration.md](docs/configuration.md)).

## Pull requests

1. **Branch** from `main` with a short descriptive name (e.g. `fix/health-stamp-path`, `feat/gotify-notifier`).
2. **Keep PRs focused** — one logical change per PR when possible.
3. **Tests** — add or update tests for behavior you change. Run locally before pushing:

   ```bash
   go vet ./...
   go test ./...
   ```

4. **Docs** — update `docs/` or `config.example.yaml` when user-visible behavior, config, or defaults change.
5. **CI** — PRs must pass GitHub Actions (`go vet`, `go test ./...`).

Link the related issue in the PR description when one exists (`Fixes #123`).

### PR checklist

- [ ] `go vet ./...` and `go test ./...` pass
- [ ] Tests cover the change (or explain why not)
- [ ] User-facing docs updated if needed
- [ ] No secrets, tokens, or private hostnames in commits
- [ ] Commit messages describe **why**, not only what

## Code guidelines

- **Match existing style** — naming, package layout, error wrapping, slog logging (`internal/logging` format).
- **Minimal scope** — avoid drive-by refactors unrelated to the PR.
- **Plugins** — new providers, registries, or notifiers use the existing `Register` / `New` pattern and blank import in `cmd/versentry/main.go`.
- **Comments** — only for non-obvious logic; prefer clear code and tests.
- Run `gofmt` (or your editor’s format-on-save) on changed Go files.

## Commit messages

Use the imperative mood and a concise subject line:

```
Fix false-positive update when tag suffix differs

Compare semver tags after applying the configured include rule.
```

## Questions

Use [GitHub Discussions](https://github.com/BlackRaincoat/versentry/discussions) or open an issue if you are unsure about scope before investing in a large PR.
