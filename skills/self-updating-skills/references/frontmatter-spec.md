# Frontmatter Schema & Cross-Harness Injection Specification

Verified against https://raw.githubusercontent.com/agentskills/agentskills/main/docs/specification.mdx on 2026-10-24. Live page wins on any conflict.

## 1. `agentskills.io` Frontmatter Compliance

The Agent Skills specification (`agentskills.io`) restricts top-level YAML frontmatter fields to:
- `name` (required, 1–64 chars, `^[a-z0-9]+(-[a-z0-9]+)*$`, must match directory name)
- `description` (required, 1–1024 chars, what the skill does + "Use when...")
- `license` (optional string)
- `compatibility` (optional string, max 500 chars)
- `allowed-tools` (optional space-separated string, e.g., `Bash(${CLAUDE_SKILL_DIR}/scripts/upkeep.sh *)`)
- `metadata` (optional `dict[str, str]` map of string keys to string values)

Because `skills-ref validate` enforces `dict[str, str]` on `metadata`, `upkeep.sh` stores all configuration as **flat `upkeep-*` string keys** under `metadata:` (while remaining backward-compatible with nested YAML in older skills).

## 2. Complete `metadata.upkeep-*` Key Reference

| Metadata Key | Format | Purpose |
|---|---|---|
| `upkeep-pins` | Comma-separated `<name>=<spec>` list | Version pins checked by `upkeep.sh check` |
| `upkeep-threshold-days` | Integer string (default `"60"`) | Maximum age in days for `Verified YYYY-MM-DD` dates in `SKILL.md` and `references/*.md` |
| `upkeep-verified` | `"YYYY-MM-DD"` (optional) | Fallback verification date when `SKILL.md` has no inline `Verified YYYY-MM-DD` line |
| `upkeep-source-base` | URL string (no trailing slash) | Base URL where raw `.md` or `.mdx` documentation pages live |
| `upkeep-source-ext` | `".md"` or `".mdx"` (default `".md"`) | File extension appended when fetching `<base>/<page><ext>` |
| `upkeep-source-cache` | Directory slug (default `<skill-name>-docs`) | Cache subdirectory name under `$TMPDIR` |
| `upkeep-source-ttl` | Integer minutes string (default `"720"`) | Cache TTL in minutes before `upkeep.sh fetch` re-downloads a page |
| `upkeep-source-index` | URL string (optional, e.g. `llms.txt`) | Index URL scraped by `upkeep.sh fetch --index` to discover all available doc pages |
| `upkeep-source-pages` | Comma-separated page slugs | Curated page slugs listed by `upkeep.sh fetch --list` |
| `upkeep-frozen-<id>` | `"sha256:<16-hex>"` | Managed automatically by `upkeep.sh stamp` for each `<!-- frozen:<id> -->` block |

## 3. Pin Syntax (`upkeep-pins`)

`upkeep-pins` accepts a comma-separated list of pins across four resolver types:

1. **npm package (default):**
   - `npm=12.2.1` or `@anthropic-ai/claude-code=2.1.310` or `@openai/codex=0.164.0`
   - Queries `npm view <pkg> version` and compares semver.
2. **GitHub release tag (`github:<owner>/<repo>@<semver>`):**
   - `babylon=github:BabylonJS/Babylon.js@9.30.0`
   - Queries `git ls-remote --tags --refs https://github.com/<owner>/<repo>.git` and compares the highest semver tag.
3. **GitHub HEAD commit SHA (`github:<owner>/<repo>@<7-to-40-hex-sha>`):**
   - `agentskills=github:agentskills/agentskills@69ef37e`
   - Queries `git ls-remote https://github.com/<owner>/<repo>.git HEAD` and checks whether `HEAD` still matches the pinned SHA prefix (ideal for spec/docs repos that don't cut semver tags).
4. **Manual vendor pin (`manual:<version>`):**
   - `substance=manual:15.0.3`
   - Prints `MANUAL` reminder without failing `--strict`.

## 4. Template for New Self-Updating `SKILL.md`

```yaml
---
name: my-skill-name
description: Clear summary of what this skill does. Use when <specific triggers and contexts>.
license: MIT
allowed-tools: Bash(${CLAUDE_SKILL_DIR}/scripts/upkeep.sh *)
metadata:
  author: YourOrg
  version: "0.1.0"
  upkeep-pins: "my-cli=1.2.3"
  upkeep-threshold-days: "60"
  upkeep-source-base: "https://raw.githubusercontent.com/org/repo/main/docs"
  upkeep-source-ext: ".md"
  upkeep-source-cache: "my-skill-docs"
  upkeep-source-pages: "overview, configuration, cli-reference"
---
```
