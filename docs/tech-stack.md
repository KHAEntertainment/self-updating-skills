# Tech Stack

## Today (v0.1.x)
- `bin/upkeep.sh`: bash with embedded Python 3 (standard library only). Copied into each skill as `skills/*/scripts/upkeep.sh` by `bin/upkeep.sh sync skills/*`.
- Distribution: npm package `@kha-entertainment/self-updating-skills` (`bin` entries `self-updating-skills`, `skill-upkeep`), plus plain skill folders.

## Skill Engine (from Phase 1)
- **Language**: Go (module `github.com/KHAEntertainment/self-updating-skills`), binary `upkeep`. No cgo; one `goreleaser` build for darwin, linux, windows.
- **CLI**: Cobra.
- **State store**: SQLite via `modernc.org/sqlite`; migrations are embedded SQL gated by `PRAGMA user_version`.
- **Schema validation (check S)**: `github.com/santhosh-tekuri/jsonschema/v6` (v6.0.3 current as of 2026-10-25; Phase 1 pins the exact version in `go.mod`).
- **Redirect drafting**: `anthropic-sdk-go`; model `claude-opus-5-5`, overridable with `UPKEEP_MODEL`.
- **Later**: Bubble Tea, Huh, Glamour for a terminal UI.

## Test framework
- Go `testing` package. Conformance fixtures in `testdata/conformance/` are shared by the Go tests and `upkeep.sh`.
- `shellcheck` for `bin/upkeep.sh`.
