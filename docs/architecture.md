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

## Redirect gate inputs (Phase 4)

How decision D8 is fed at runtime. All three metadata keys are optional; a check with no input is reported as "not run", never as a pass.

| Input | Metadata key | Rule |
| --- | --- | --- |
| Model | env `UPKEEP_MODEL` (default `claude-opus-5-5`) | Key from `ANTHROPIC_API_KEY` only, never logged or stored. A skill that needs drafting without a key fails ("drafting unavailable: ANTHROPIC_API_KEY not set"). The output is read only through a forced tool call and the row JSON Schema. |
| S schema (`config-key` rows) | `upkeep-schema-url` | https only, on the skill's `upkeep-source-base`/`upkeep-source-index` host or the package's repository host. Fetched per draft, no cache; its sha256 is recorded with the check. Compiled with no URL loader, so a non-local `$ref` cannot compile. **Allowlist (owner decision, PR #18 round 3):** the key path is followed through plain `properties` of plain objects only; the final property must be a plain type schema (type, format, description, title, enum, const, default, examples, minLength, maxLength, pattern), optionally reached through local `$ref`/`allOf` members that are plain in turn (depth ≤ 4, no cycle). A false schema, `not`, `anyOf`/`oneOf`, `if`/`then`/`else`, `patternProperties`, `dependentSchemas`, `$dynamicRef`, `deprecated`, or a `$ref`/`allOf` on an intermediate object makes S "not run", which never passes. `new` must resolve to such a non-deprecated leaf; `old` must be refused by a plain closed object (`additionalProperties: false`) or be a plain leaf carrying `"deprecated": true`. A closed object refuses any unknown name, so a refused `old` also needs evidence that it existed: a whole-identifier mention in the previous snapshot of the page, or a hit in the docs git history (the same evidence as D's first condition; CodeRabbit 4497176934). Without it S does not pass ("old key has no history evidence"). A keyword-deprecated `old` needs no such evidence; only the keyword proves `old` retired, never description wording (round 4). The blocking side stays conservative: a `new` key, or an intermediate object, with the keyword or a description beginning "Deprecated" never lets S pass. Only bare key paths are checked: an assignment (`key = value`) is not validated, so S does not run. |
| S `flag` rows | none | Deferred: no binary is executed. Recorded as "not run (flag check deferred)". |
| D git history | `upkeep-docs-repo`, `upkeep-docs-path` | https repository on the package's repository host. Bare clone cached under `<state dir>/repos/`, fetched later; `git log -p -S <old>` scoped to the page file; an added or removed patch line of a hit must mention `old` as a whole identifier (round 4), and the scan is bounded by 2 minutes and 50 MB of output, either of which counts as not found. Git runs with `protocol.allow=never` plus https, no hooks, no external diff or textconv, no inherited `GIT_*` variables. Used when the previous snapshot is not in the state database or does not show `old`. |

**D (owner decision, PR #18 round 3):** deprecation wording is never evidence. D passes only when `old` was in the previous snapshot or the upstream git history, is absent from the newest snapshot of the page, and `new` is present now. Text is compared after Unicode NFC, Markdown backslash-escape removal and whitespace collapse (round 4). Presence as evidence (`old` before, `new` now) is a whole-identifier match: `api.v1` is not evidenced by `api.v10`, `api.v2` not by `api.v20`. Absence of `old` now is conservative: any substring occurrence (`api.v1.0`, `--legacy-mode`) keeps `old` present. This departs from the original spec's "absent or deprecated now".

Routing per skill: the worst row outcome wins (hard stop > fallback > PR > auto). A changed page with no auto-accepted row on it also sends the skill to a PR, so `verified_sha` never advances without evidence. Only a non-empty draft whose every row auto-accepts is written, through the same lease-guarded write, strict check and rollback as an auto-advance. A row that edits an existing row (same id or same `old`) goes to a PR; a row whose `old` appears in a frozen block is a hard stop instead, which takes precedence. Two drafted rows with the same `old`, or a row whose `old` matches an existing row's, compared in the canonical form above, also go to a PR ("duplicate old", round 4). A draft whose page text and diffs exceed 400,000 bytes makes no model call and goes to a PR.

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
