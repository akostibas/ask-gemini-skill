# ask-gemini

A small Go CLI that asks Google Gemini for a second opinion, plus a Claude Code
skill (`skill/SKILL.md`) that wraps it.

## Layout

- `cmd/ask-gemini/` — the CLI (`main.go`, `main_test.go`).
- `skill/SKILL.md` — the Claude Code skill instructions.
- `bin/` — workflow scripts (`smoke-test.sh`, `release.sh`).

## Testing

- `go test ./...` — unit + HTTP-mocked tests. No network, no key needed.
- `bin/smoke-test.sh [model]` — live, billable API call. Run after changing the
  default model, API endpoints, or request shape.

## Versioning & releases

SemVer, tagged `vMAJOR.MINOR.PATCH`. While pre-1.0, breaking changes bump the
minor.

- **patch** — bug fixes, docs, CI, internal refactors (no behavior change).
- **minor** — new backward-compatible flags or features (e.g. adding `--audio`).
- **major** — breaking changes to flags, output format, or the session-file shape.

Cut releases with `bin/release.sh <version|patch|minor|major>`. It refuses on a
dirty tree, wrong branch, out-of-sync `main`, an existing tag, or failing tests,
then tags, pushes, and creates a GitHub release. Never tag by hand — the script
is the gate that guarantees tests passed against the published commit.

`ask-gemini --version` reports the build version (injected via `-ldflags
-X main.version`, with a fallback to Go's embedded module version for
`go install ...@<tag>`).
