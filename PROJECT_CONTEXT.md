# PROJECT_CONTEXT.md — Index File

> This is the master index. It only routes; detail lives in `docs/`.
> The `/dev` skill reads this file at every Phase to restore context.

---

## Repository Info

- **Repo URL**: https://github.com/KHAEntertainment/self-updating-skills
- **Main branch**: main
- **Created**: 2026-10-24 (v0.1.0, commit `fa7e499`)

---

## Sub-document Index

| File | Content | Update timing |
|------|---------|---------------|
| `docs/glossary.md` | Canonical terms: verdicts, regions, sidecar, pins | When a term is added or drifts |
| `docs/tech-stack.md` | Shell runtime today; Go engine and its dependencies | When tech choices change |
| `docs/architecture.md` | Skill Engine decisions D1–D10 and P1–P4 | Immediately when an architecture decision is made |
| `docs/style-guide.md` | Go layout, CLI output compatibility, testing rules | When conventions change |
| `docs/feature-log.md` | Completed features (PR, merge date) and known tech debt | Every Phase 5 round |

---

## Current Status

- **Last updated**: 2026-11-13
- **Current iteration goal**: the standalone utility — the `self-updating-skills` meta-skill and `upkeep.sh`. The find, scan and convert workflow for a library of existing skills is built (`scripts/scan.sh`, `references/convert-existing.md`). Next: a pilot conversion of three skills the owner approves. Plan: Traycer artifact `utility-rescope-plan`.
- **Parked (2026-11-13)**: the Skill Engine (the Go code under `cmd/`, `engine/`, `internal/`, `testdata/`). It stays in the repo, unused, and gets no new work. Phase 4b and Phase 5 were not built; Issue #1 and the milestone "Skill Engine MVP" are closed as not planned. No published skill depends on the `upkeep` binary. Audit: Traycer artifact `project-audit-2026-11-13`.
- **Open PRs**: none recorded here
- **Known tech debt**: see the bottom of `docs/feature-log.md`

---

## Verification Gate

Every worker, QA and reviewer lane runs the same gate. Until the Go module exists (Phase 1 creates it), only the shell lines apply.

- **Lint**: `gofmt -l .` (must print nothing) and `go vet ./...`; `shellcheck bin/upkeep.sh`
- **Type check**: covered by `go build ./...`
- **Static analysis**: `go vet ./...` (staticcheck not installed on the dev Mac; CI may add it)
- **Dependency scan**: `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
- **Tests**: `nice -n 10 go test ./...` (heavy — see the test-run rules in `docs/style-guide.md`)

---

## External Review Policy

- **Mode**: auto
- **Trusted reviewers**: coderabbit
- **Required reviewers**: none
- **Ignored reviewers**: none
- **Additional reviewer identities**: none
- **Pending reviewer decisions**: none
- **Default wait minutes**: 10
- **Allow automatic review requests**: false

If CodeRabbit skips a PR because of its rate limit, "no findings" is not a pass: a Codex reviewer runs `/traycer-review` on the PR before merge.
