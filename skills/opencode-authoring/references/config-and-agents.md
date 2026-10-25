# OpenCode Config, Agents, Permissions & MCP Quick Reference

Verified against `config.mdx`, `agents.mdx`, `permissions.mdx`, `policies.mdx`, and `mcp-servers.mdx` in `anomalyco/opencode` on 2026-10-24. Live page wins on any conflict.

## 1. Config Precedence & Merging (`opencode.json` / `opencode.jsonc`)

Config files are **merged** (later sources override earlier ones only on conflicting keys):
1. Remote organizational defaults (`.well-known/opencode`)
2. Global config (`~/.config/opencode/opencode.json`) — TUI settings go in `tui.json`
3. Custom config file (`OPENCODE_CONFIG` env var)
4. Project config (`opencode.json` or `opencode.jsonc` at project root)
5. `.opencode/` subdirectories (`agents/`, `commands/`, `plugins/`, `skills/`, `tools/`, `themes/`)
6. Inline runtime config (`OPENCODE_CONFIG_CONTENT` env var)
7. Managed admin config (`/Library/Application Support/opencode/` on macOS, or MDM `.mobileconfig`)

**Variable substitutions inside config strings:**
- `{env:VAR_NAME}` → environment variable value
- `{file:./path/to/file.txt}` → file contents (relative to config file or `~/`)

## 2. Agents (`agent` in `opencode.json` or `.opencode/agents/<name>.md`)

Built-in agents:
- **Primary (`mode: "primary"`, cycled with Tab):** `build` (full tools) and `plan` (denies edits except `.opencode/plans/*.md`).
- **Subagents (`mode: "subagent"`, invoked automatically or via `@name`):** `general` (parallel multi-step work, all tools except `todowrite`) and `explore` (fast read-only codebase search).

Markdown agent example (`.opencode/agents/review.md` or `~/.config/opencode/agents/review.md`):
```markdown
---
description: Reviews code for quality, security, and test gaps
mode: subagent
model: anthropic/claude-sonnet-5-5
temperature: 0.1
steps: 15
permission:
  edit: deny
  bash:
    "*": deny
    "git diff*": allow
    "git log*": allow
---

You are in read-only code review mode. Focus on correctness, security, and missing tests.
```

## 3. Permissions (`permission` in `opencode.json`)

- Actions: `"allow"` | `"ask"` | `"deny"`.
- **Evaluation order:** Pattern rules evaluate in order and the **last matching rule wins** (put `"*": "ask"` or `"*": "deny"` first, specific rules after).
- CLI `--auto` (`opencode --auto` or `opencode run --auto`) auto-approves `"ask"` rules while still enforcing explicit `"deny"` rules.
- **Provider restrictions:** In OpenCode 1, use `"disabled_providers": ["openai"]` or `"enabled_providers": ["anthropic"]` (`experimental.policies` requires OpenCode 2).

## 4. MCP Servers (`mcp` in `opencode.json`)

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "local-srv": {
      "type": "local",
      "command": ["npx", "-y", "@modelcontextprotocol/server-everything"],
      "environment": { "API_KEY": "{env:MY_API_KEY}" },
      "enabled": true,
      "timeout": 5000
    },
    "remote-srv": {
      "type": "remote",
      "url": "https://mcp.example.com/mcp",
      "headers": { "Authorization": "Bearer {env:MCP_TOKEN}" },
      "oauth": {}
    }
  }
}
```
