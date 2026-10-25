# Skills quick reference

Verified against https://code.claude.com/docs/en/skills.md on 2026-10-24.
Live page wins on any conflict.

## Locations and precedence

| Location | Path |
|---|---|
| Enterprise | managed settings dir `.claude/skills/<name>/SKILL.md` |
| Personal | `~/.claude/skills/<name>/SKILL.md` |
| Project | `.claude/skills/<name>/SKILL.md` (also loaded from parent dirs up to repo root) |
| Nested | `<subdir>/.claude/skills/<name>/SKILL.md`, named `/<subdir>:<name>` |
| Plugin | `<plugin>/skills/<name>/SKILL.md`, namespaced `/<plugin>:<name>` |
| claude.ai synced | shows as `/anthropic-skills:<name>` on name conflict |

Name conflicts: Enterprise > Personal > Project > bundled. Don't name a folder
`synced` (reserved). Symlinked skill folders are supported.

## Frontmatter

| Field | Purpose |
|---|---|
| `name` | Display name; defaults to directory name |
| `description` | What it does + when to use. Key use case first |
| `when_to_use` | Extra trigger phrases; appended to description (1,536-char combined cap) |
| `argument-hint` | Autocomplete hint, e.g. `[issue-number]` |
| `arguments` | Named positional args for `$name` substitution (string or list) |
| `disable-model-invocation` | `true` = only the user can invoke; description not loaded into context |
| `user-invocable` | `false` = hidden from `/` menu; Claude-only |
| `allowed-tools` | Tools pre-approved for the invoking turn; grant clears on next message |
| `disallowed-tools` | Tools removed while the skill is active |
| `model` | Model override for the rest of the turn |
| `effort` | `low` / `medium` / `high` / `xhigh` / `max` |
| `context` | `fork` = run in a subagent |
| `agent` | Subagent type when `context: fork` |
| `background` | With `fork`: `false` waits for the result in the same turn |
| `hooks` | Hooks registered on invocation, kept for the rest of the session (`once: true` supported) |
| `paths` | Globs; auto-activate only when working on matching files |
| `shell` | `bash` (default) or `powershell` for `!` blocks |
| `metadata` | Free-form map; Claude Code ignores it |
| `license`, `compatibility` | Agent Skills spec fields |

`version` is not a documented field.

## String substitutions

`$ARGUMENTS`, `$ARGUMENTS[N]`, `$N`, `$name`, `${CLAUDE_SESSION_ID}`,
`${CLAUDE_EFFORT}`, `${CLAUDE_SKILL_DIR}`, `${CLAUDE_PROJECT_DIR}`,
`${CLAUDE_PLUGIN_ROOT}` and `${CLAUDE_PLUGIN_DATA}` (plugin skills only).

If no placeholder consumes the arguments, they're appended as `ARGUMENTS: ...`.

## Dynamic context injection

- Inline `` !`cmd` `` or a fenced block opened with ```` ```! ````. Runs before the
  content reaches Claude, and the output replaces the placeholder.
- A failing command aborts the invocation. Append `|| true` if non-zero is normal.
- Pre-approve the commands with `allowed-tools`. They never run in skills synced
  from claude.ai. `disableSkillShellExecution` turns them off.

## Size and lifecycle

- Keep SKILL.md under 500 lines.
- After compaction, each invoked skill is re-attached with its first 5,000 tokens,
  within a 25,000-token total budget.
- Personal and project skill edits reload live. Creating a new top-level skills
  directory needs a restart.
