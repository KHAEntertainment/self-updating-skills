# Style Guide

## Directory structure (Go engine)

```
go.mod                 module github.com/KHAEntertainment/self-updating-skills
cmd/upkeep/            Cobra CLI, a thin shell over engine/
engine/                PUBLIC API: JSON-tagged request/response types + operations
internal/skill/        frontmatter, frozen + generated region parsing
internal/check/        verdict computation
internal/render/       region templates, sidecar I/O, atomic writer
internal/source/       npm, GitHub tags, doc fetch + llms.txt index
internal/redirect/     drafting + verifiers Q/S/D/J
internal/update/       poll → decide → draft → gate → write → render → check
internal/store/        SQLite state + lease
testdata/conformance/  fixture skills + expected verdicts (shared with upkeep.sh)
```

`bin/upkeep.sh` and the `package.json` `bin` entries stay.

## Conventions
- Every `engine` operation takes a `context.Context` and returns a JSON-tagged struct plus a structured error. CLI text output is rendered from the same struct; `--json` prints it as-is.
- `upkeep check` text output stays line-compatible with `upkeep.sh`: one item per line, then `VERDICT: X — summary`.
- Region and frozen markers inside fenced code blocks are ignored. Find markers on fence-masked text, splice on the original bytes (mask by replacing characters, never deleting lines).
- Rendered output is byte-stable: keep existing line endings and trailing newline, sort map keys, no timestamps except data fields. Skip the write when bytes are unchanged.
- File writes: `os.CreateTemp` in the same directory, copy the target's mode, write, fsync, rename, fsync the directory. Sidecar first, then `SKILL.md`.

## Test-run rules (shared machine)
- Full suites, the conformance matrix and concurrency tests are heavy. Announce each before running it, and get the lead's go-ahead when running as a sub-agent.
- Run them in the foreground under `nice -n 10`, one at a time. No `&`, `xargs -P` or parallel runners.
- No test may launch the test suite (no `go test` subprocess from inside a test).
- After a heavy run, check no runner processes remain (`pgrep -f 'go test'`).
