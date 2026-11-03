# Feature Log

## Completed
- Initial self-updating-skills framework and harness authoring suite, v0.1.0 (commit `fa7e499`, 2026-10-24)
- Skill Engine Phase 1: Go `check` port with verdicts identical to `upkeep.sh` (PR #8, merged 2026-10-26; conformance matrix, cobra CLI `upkeep check`, SQLite migration runner)
- Skill Engine Phase 2: generated regions, `upkeep.data.json` sidecar and lossless render (PR pending, 2026-10-27; `engine.Render`/`upkeep render [--check]`, `upkeep stamp` in Go, `DRIFT` check items (each forcing the overall verdict `STALE`) and the separate `CONTENT-LAG` overall verdict, with the Go `CONTENT-LAG` ↔ script `STALE` mapping, `claude-code-authoring` retrofitted as the proof skill)
- Go `stamp` keeps CRLF line endings by design (the Python stamp normalizes to LF); stdout and frozen digests stay identical between the two
- Skill Engine Phase 4a: redirect drafting and the Q/S/D gate (PR pending, part of #5; `internal/redirect` drafting through `anthropic-sdk-go` behind a `Drafter` interface, schema-validated row JSON, D10 sanitization, Q quote / S config schema (`jsonschema/v6`) / D doc history (`git log -S`) verifiers, per-skill routing auto / PR / fallback / hard stop, `row_verdicts` and the run report, auto-accept through the Phase 3 lease-guarded write, `upkeep status` lists pending items; PR and fallback-issue creation for `ci` skills is Phase 4b)

## Known Tech Debt
- Required status check on `main` not yet configured; needed before Skill Engine Phase 5 auto-merge (recorded: 2026-10-25)
- Fallback parity limits (two documented Go-only classes; the script never reads sidecars): `DRIFT` items make Go report STALE where `upkeep.sh` reports FRESH, and when the sidecar's `latest_seen` is ahead of a rolled-back or unpublished registry Go reports CONTENT-LAG where the script reports FRESH — both deliberate, for never-FRESH safety (recorded: 2026-10-27)
- Skill Engine Phase 3: deterministic updater (PR #13; `engine.Poll`/`Plan`/`Update`/`Status`, `upkeep poll [--seed-docs]`/`update [--dry-run] [--runner]`/`status`, CONTENT-LAG for changed tracked doc pages, SQLite v1 schema (runs, snapshots, row_verdicts, lease), single-writer lease with 30-minute expiry and renewal, rollback of failed advances, `upkeep-owner` gating; `claude-code-authoring` sidecar owner corrected to `ci`)
- Known limit (Phase 3): the frozen `bin/upkeep.sh` never reads sidecars, so a docs-only change with no version move reads FRESH there (Go reports CONTENT-LAG); the two-implementation acceptance is therefore tested with the pin behind AND the page changed, and the docs-only case is covered by Go `check` alone
- Phase 4a limits: the S `flag` check is deferred (no binary is executed); D on a large docs monorepo (opencode) needs a full bare clone in CI; `upkeep status` lists a skill's pending items until a later run drafts it again, so items stay listed after their PR merges (recorded: 2026-11-03)
- `codex-authoring` and `opencode-authoring` have no sidecar yet; the Phase 4 ACs are proved on `testdata` fixtures modelled on them, and retrofitting the real skills is a follow-up (recorded: 2026-11-03)
