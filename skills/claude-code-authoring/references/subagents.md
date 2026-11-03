# Subagents quick reference

Verified against https://code.claude.com/docs/en/sub-agents.md on 2026-11-03.
Live page wins on any conflict.

## Locations (highest priority first)

1. Managed settings
2. `--agents` CLI flag
3. `.claude/agents/`
4. `~/.claude/agents/`
5. Plugin `agents/` (subfolders load as `plugin:folder:agent`)

## Frontmatter

| Field | Purpose |
|---|---|
| `name` | Required. Hooks receive it as `agent_type` |
| `description` | Required. When Claude should delegate |
| `tools` | Allowlist (string or list); inherits all tools if omitted |
| `disallowedTools` | Denylist |
| `model` | `sonnet` / `opus` / `haiku` / `fable` / full ID / `inherit` |
| `permissionMode` | `default`, `acceptEdits`, `auto`, `dontAsk`, `bypassPermissions`, `plan` (`manual` = `default`) |
| `maxTurns` | Turn cap; output is marked partial when hit |
| `autoCompactWindow` | Lower the auto-compact window for this subagent, `100000` to `1000000` tokens (v2.1.296+); never raises the window set by `CLAUDE_CODE_AUTO_COMPACT_WINDOW` |
| `skills` | Skills preloaded (full content) at startup |
| `mcpServers` | Server names or inline configs |
| `hooks` | Scoped hooks; `Stop` becomes `SubagentStop` |
| `memory` | `user` / `project` / `local` persistent memory |
| `background` | `true` = stay in the background even when Claude asks to wait (subagents Claude spawns from the main conversation already start in the background by default) |
| `omitClaudeMd` | `true` = skip user, project and local CLAUDE.md |
| `effort` | `low` … `max` |
| `isolation` | `worktree` = temporary git worktree |
| `color` | `red`, `blue`, `green`, `yellow`, `purple`, `orange`, `pink`, `cyan` |
| `initialPrompt` | First user turn when run as the main agent (`--agent`) |
| `experimental` | e.g. `cacheTtl: 5m` or `1h` |

Plugin subagents ignore `hooks`, `mcpServers` and `permissionMode`.

The description is prose about when Claude should delegate.
