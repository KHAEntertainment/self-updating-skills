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

- **Last updated**: 2026-10-27
- **Current iteration goal**: Skill Engine — replace the scheduled Markdown-editing agent with a deterministic Go updater, agent update as fallback. Tracking issue: #1. Five rollout phases, one Issue each.
- **Open PRs**: #9 skill engine Phase 2 (generated regions, sidecar, lossless render)
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
