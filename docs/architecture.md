# Architecture Decisions — Skill Engine

Settled 2026-10-25. Tracking issue: #1. The full plan (data model, updater, engine, rollout) is the Traycer artifact `skill-engine-tech-plan` in epic `f16d590e-d4c2-48d1-be95-aec24a09cd90`; the phase Issues carry the parts each phase needs.

## Shape

A Go engine keeps self-updating skills current without an agent editing Markdown. Pins, freshness facts and verified redirect rows live in a per-skill data sidecar (`upkeep.data.json`). A deterministic renderer writes them into marked `<!-- generated:id -->` regions of an ordinary `SKILL.md`. An agent-driven update runs only when verification fails.

## Settled decisions

| # | Decision |
| --- | --- |
| D1 | The deterministic updater is the main path; the agent-driven update is the fallback. |
| D2 | MVP: an agent scaffolds each skill with the `self-updating-skills` meta-skill and the engine maintains it. Generating skills from a package name or doc URL is the later goal. Importing existing skills is deferred. |
| D3 | The engine is API-based: a public Go package `engine` returns JSON-tagged results; the Cobra CLI is a thin shell over it; a future HTTP server or dashboard wraps the same calls. |
| D4 | Go engine. `bin/upkeep.sh` gets one last release (v0.3.0) that hands off to the Go binary when installed, then stays frozen as the zero-dependency fallback. |
| D5 | One renderer, one region syntax, single-file skills. No template mode in v1. |
| D6 | SQLite (`modernc.org/sqlite`, pure Go, no cgo) holds local operational state only (runs, snapshots, row verdicts, lease). Git is the history of the content. |
| D7 | New verdict `CONTENT-LAG`: the upstream version moved but the content is verified against an older one. A pin bump alone never reports `FRESH`. |
| D8 | Redirect gate: check Q (quote) is required; Q plus S (schema/CLI) or D (doc history) auto-accepts a row; Q-only opens a PR; a Q failure goes to the agent fallback. J (second model) is escalate-only and off in MVP. |
| D9 | No daemon. GitHub Actions (this repo, `ci`-owned skills) and launchd (`~/.agents`, `local`-owned skills) run daily. |
| D10 | Temp files copy the target's permissions. Only bounded row fields are rendered, never raw doc text; region markers are stripped from fetched text. |
| P1 | `upkeep-pins` means "content verified for" (`verified_for`). The newest upstream version (`latest_seen`) is tracked separately in the sidecar, so the frozen script shows `STALE` exactly where Go shows `CONTENT-LAG`. |
| P2 | Auto-advance: a docs-based skill whose tracked doc snapshots are unchanged advances automatically; a semver patch advances automatically (`upkeep-auto-advance` is `patch` or `none`, default `patch`). Everything else goes to redirect drafting. |
| P3 | Each skill has exactly one owner runner (`upkeep-owner: ci` or `local`). The local runner only `git pull --ff-only`s skills it does not own. |
| P4 | MVP generated regions live in `SKILL.md` only, so one atomic rename covers every write. Regions in `references/` (build-dir + symlink swap) are deferred. |

## Out of scope (MVP)

Importing existing or third-party skills; generating skills from a URL; the HTTP server and dashboard; a Bubble Tea UI; generated regions in `references/*.md`; FUSE or other virtual filesystems.

## When this becomes wrong

| Signal | Response |
| --- | --- |
| More than half of drafted rows land in PRs for 4 consecutive weeks | Add verifiers before adding automation |
| Two processes must write state at once (hosted dashboard) | Move the state store to Turso (same file format) |
| A skill needs different `SKILL.md` content per harness | Add a template mode behind the same renderer |
| A harness can't follow the symlink chain | Copy into that harness instead of linking |
| Mixed versions seen during a `git pull` cause a real failure | Implement the build-dir + symlink swap (lift P4) |
