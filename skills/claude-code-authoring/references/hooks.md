# Hooks quick reference

Verified against https://code.claude.com/docs/en/hooks.md on 2026-10-24.
Live page wins on any conflict. Before writing a hook for an event, read that
event's "decision control" section in the live page.

## Events (33)

- **Session:** `Setup`, `SessionStart`, `SessionEnd`
- **Turn:** `UserPromptSubmit`, `UserPromptExpansion` (slash commands), `Stop`,
  `StopFailure`, `MessageDisplay`
- **Tools:** `PreToolUse`, `PermissionRequest`, `PermissionDenied`,
  `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`
- **Agents and tasks:** `SubagentStart`, `SubagentStop`, `TaskCreated`,
  `TaskCompleted`, `TeammateIdle`
- **Context:** `PreCompact`, `PostCompact`, `InstructionsLoaded`
- **Environment:** `ConfigChange`, `CwdChanged`, `DirectoryAdded`,
  `FileChanged`, `WorktreeCreate`, `WorktreeRemove`, `Notification`
- **Model:** `PreModelSwitch`, `PostModelSwitch`
- **MCP:** `Elicitation`, `ElicitationResult`

## Where hooks live

- Settings: `~/.claude/settings.json`, `.claude/settings.json`,
  `.claude/settings.local.json`, managed policy.
- Plugins: `hooks/hooks.json`.
- Skill frontmatter: active for the rest of the session after invocation.
- Subagent frontmatter: active while the subagent runs. Ignored for plugin
  subagents.

Entries merge across levels. `allowManagedHooksOnly` (managed) blocks
user, project, local and plugin hooks.

## Config shape

```json
{ "hooks": { "<Event>": [ { "matcher": "Bash|Edit",
    "hooks": [ { "type": "command", "if": "Bash(rm *)",
                 "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/x.sh",
                 "timeout": 600, "statusMessage": "..." } ] } ] } }
```

## Handler types and common fields

- **Types:** `command`, `http`, `mcp_tool`, `prompt`, `agent`.
- **`if`:** a single permission rule, e.g. `Bash(git *)`. It's evaluated only on
  `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest` and
  `PermissionDenied`. On any other event, a hook with `if` never runs. It's
  best-effort, so use permission rules for hard enforcement.
- **`timeout` defaults:** 600s for `command`, `http` and `mcp_tool`; 30s for
  `prompt`; 60s for `agent`. Lower on `UserPromptSubmit` and model-switch
  events (30s) and `MessageDisplay` (10s).
- **`once`:** honored only in skill frontmatter.
- **`async: true`** (command): runs in the background and the timeout isn't
  enforced.

## Exit codes

- **0:** success. stdout JSON is parsed. Plain stdout becomes context only on
  some events (e.g. `UserPromptSubmit`, `SessionStart`).
- **2:** blocking error on events that can block; stderr is the reason. JSON
  `allow` can't override it. `PermissionRequest` ignores exit 2 (use the
  `decision` object instead).
- **Other non-zero:** non-blocking for most events. `WorktreeCreate` and
  `WorktreeRemove` fail on any non-zero exit.

## JSON output

- **Universal fields:** `continue` (false stops Claude entirely), `stopReason`,
  `systemMessage`, `terminalSequence`. `suppressOutput` is accepted but has no
  effect.

| Events | Decision fields |
|---|---|
| UserPromptSubmit, UserPromptExpansion, PostToolUse, PostToolUseFailure, PostToolBatch, Stop, SubagentStop, ConfigChange, PreCompact | top-level `decision: "block"` + `reason` |
| PreToolUse | `hookSpecificOutput.permissionDecision`: `allow` / `deny` / `ask` / `defer`, plus `permissionDecisionReason` |
| PreModelSwitch | `permissionDecision` `allow` / `deny` / `ask`, or `decision: "block"` |
| PermissionRequest | `hookSpecificOutput.decision.behavior`: `allow` / `deny` |
| PermissionDenied | `hookSpecificOutput.retry: true` |
| TeammateIdle, TaskCompleted | exit 2, or `continue: false` |
| TaskCreated | exit 2 or `decision: "block"` |
| WorktreeCreate | print the path on stdout (HTTP: `worktreePath`) |
| Elicitation, ElicitationResult | `action` (accept/decline/cancel), `content` |
| MessageDisplay | `displayContent` (display only) |
| SessionStart, SubagentStart, PostModelSwitch | `additionalContext` only |
| Setup, Notification, SessionEnd, PostCompact, InstructionsLoaded, StopFailure, CwdChanged, DirectoryAdded, FileChanged, WorktreeRemove | none |

Some events can also rewrite input (e.g. `updatedInput`). See "Decision control"
in the live page.
