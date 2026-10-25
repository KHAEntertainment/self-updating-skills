# OpenCode Slash Commands, Skills & `AGENTS.md` Quick Reference

Verified against `commands.mdx`, `skills.mdx`, and `rules.mdx` in `anomalyco/opencode` on 2026-10-24. Live page wins on any conflict.

## 1. Custom Slash Commands (`.opencode/commands/<name>.md` or `"command"` in `opencode.json`)

Locations: `.opencode/commands/<name>.md` (project) and `~/.config/opencode/commands/<name>.md` (global).

```markdown
---
description: Run tests with coverage
agent: build
model: anthropic/claude-sonnet-5-5
subtask: true
---

Run the test suite for $1 (or full suite if omitted: $ARGUMENTS).
Current git status:
!`git status --short`
Reference config: @package.json
```

**Template substitutions in OpenCode commands:**
- `$ARGUMENTS` — all arguments passed after `/command-name`
- `$1`, `$2`, ... — positional arguments
- `` !`shell command` `` — executes command in project root and embeds stdout into the prompt
- `@path/to/file` — attaches file content into the prompt
- `subtask: true` in frontmatter forces the command to run as a subagent invocation so it doesn't pollute primary context.

## 2. Agent Skills in OpenCode (`SKILL.md`)

OpenCode scans from the current working directory up to the Git worktree root for:
- `.opencode/skills/<name>/SKILL.md`
- `.claude/skills/<name>/SKILL.md`
- `.agents/skills/<name>/SKILL.md`

And globally:
- `~/.config/opencode/skills/<name>/SKILL.md`
- `~/.claude/skills/<name>/SKILL.md`
- `~/.agents/skills/<name>/SKILL.md`

Frontmatter rules (`agentskills.io` spec):
- Required: `name` (`^[a-z0-9]+(-[a-z0-9]+)*$`, 1–64 chars, must match directory name) and `description` (1–1024 chars).
- Optional: `license`, `compatibility`, `metadata` (`dict[str, str]`).
- OpenCode exposes skills to agents via the built-in `skill` tool; control skill access using `"permission": { "skill": { "*": "allow", "internal-*": "deny" } }`.

## 3. `AGENTS.md` Rules & Claude Code Fallbacks

- **Project rules:** `AGENTS.md` at project root (falls back to `CLAUDE.md` unless `OPENCODE_DISABLE_CLAUDE_CODE=1` or `OPENCODE_DISABLE_CLAUDE_CODE_PROMPT=1` is set).
- **Global rules:** `~/.config/opencode/AGENTS.md` (falls back to `~/.claude/CLAUDE.md`).
- **Extra instruction files:** Add globs or paths to `"instructions": ["CONTRIBUTING.md", "docs/guidelines/*.md"]` in `opencode.json`.
