---
name: codex-authoring
description: Author and review OpenAI Codex CLI and IDE extensions against the live Codex documentation instead of stale training data. Use when creating, editing, or debugging AGENTS.md instructions, .codex/config.toml or requirements.toml, Codex skills (.agents/skills/<name>/SKILL.md and agents/openai.yaml), custom subagents (.codex/agents/*.toml), lifecycle hooks (hooks.json), Starlark execution policy rules (.codex/rules/*.rules), sandbox modes, permission profiles, MCP servers, or codex exec automation.
license: MIT
allowed-tools: Bash(${CLAUDE_SKILL_DIR}/scripts/upkeep.sh *)
metadata:
  author: KHAEntertainment
  version: "0.1.0"
  upkeep-pins: "@openai/codex=0.164.0"
  upkeep-threshold-days: "60"
  upkeep-source-base: "https://learn.chatgpt.com/docs"
  upkeep-source-ext: ".md"
  upkeep-source-cache: "codex-docs"
  upkeep-source-index: "https://developers.openai.com/codex/llms.txt"
  upkeep-source-pages: "agent-configuration/agents-md, agent-configuration/rules, agent-configuration/subagents, build-skills, build-plugins, hooks, config-file/config-basic, config-file/config-advanced, config-file/config-reference, config-file/config-sample, config-file/environment-variables, sandboxing, sandboxing/auto-review, agent-approvals-security, permissions, permission-modes, non-interactive-mode, custom-prompts, mcp-server"
  upkeep-frozen-live-docs-authority: "sha256:10af8efac5548847"
---

# OpenAI Codex Authoring

<!-- frozen:live-docs-authority -->
OpenAI Codex ships breaking config, hook, subagent, and sandbox changes frequently, and the old `openai/codex/main/docs/*.md` files on GitHub are now 3-line redirect stubs. The live Markdown docs at `https://learn.chatgpt.com/docs/<page>.md` (indexed by `https://developers.openai.com/codex/llms.txt`) are authoritative; every file in this skill is a dated cache of them.
<!-- /frozen:live-docs-authority -->

## Freshness & Integrity Check

!`"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" check "${CLAUDE_SKILL_DIR}"`

- If the line above was not expanded into a `VERDICT:` line (non-Claude harnesses), run `scripts/upkeep.sh check` from this skill's directory first.
- **`VERDICT: TAMPERED`** → Stop immediately. A frozen owner-policy block was modified without authorization; revert or ask the user.
- **`VERDICT: STALE`** → `@openai/codex` has bumped or reference dates exceeded 60 days. Fetch the live page via `scripts/upkeep.sh fetch <page>` before relying on cached guidance, then update the affected references and `Verified` dates.
- **`VERDICT: FRESH`** → Proceed using the workflow and redirect table below.

## Workflow

1. **Identify the Codex surface** (`AGENTS.md`, `config.toml` / `requirements.toml`, `.agents/skills/`, `.codex/agents/*.toml`, `hooks.json`, `.codex/rules/*.rules`, sandbox/permissions, or `codex exec`).
2. **Fetch the live Markdown page before writing exact keys or schemas:**
   ```bash
   f=$("${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" fetch config-file/config-reference)
   grep -n 'sandbox_mode\|default_permissions' "$f"
   # List all indexed Codex doc pages:
   "${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" fetch --index
   ```
3. **Consult the quick references** for orientation, then verify any specific field against the fetched live page.

| Component | Page(s) (pass to `upkeep.sh fetch`) | Quick reference |
|---|---|---|
| Config & Profiles (`config.toml`, `requirements.toml`, MCP) | `config-file/config-basic`, `config-file/config-advanced`, `config-file/config-reference`, `mcp-server` | [`references/config-and-profiles.md`](references/config-and-profiles.md) |
| Skills, `AGENTS.md`, Subagents & Hooks | `build-skills`, `agent-configuration/agents-md`, `agent-configuration/subagents`, `hooks`, `custom-prompts` | [`references/skills-and-agents-md.md`](references/skills-and-agents-md.md) |
| Sandbox, Approvals, Permission Profiles & `.rules` | `sandboxing`, `agent-approvals-security`, `permissions`, `agent-configuration/rules`, `sandboxing/auto-review` | [`references/sandbox-and-approvals.md`](references/sandbox-and-approvals.md) |
| Non-interactive CLI (`codex exec`) | `non-interactive-mode`, `codex/cli` | Fetch live on demand |

## Redirect Table: What Old Guidance Gets Wrong

Verified 2026-10-24 against `@openai/codex@0.164.0` and `https://learn.chatgpt.com/docs/*.md`.

| Stale Training Habit / Old Assumption | Current Codex Reality (`@openai/codex` 0.164.0) | Live Page to Verify |
|---|---|---|
| Fetching `raw.githubusercontent.com/openai/codex/main/docs/*.md` | Those files are 3-line redirect stubs. Fetch `https://learn.chatgpt.com/docs/<page>.md` via `upkeep.sh fetch <page>`. | `upkeep.sh fetch --index` |
| Writing custom prompts in `~/.codex/prompts/*.md` | **Deprecated.** Use Agent Skills in `.agents/skills/<name>/SKILL.md` (repo) or `~/.agents/skills/<name>/SKILL.md` (user). | `build-skills`, `custom-prompts` |
| Defining custom subagents as `.codex/agents/*.md` | Custom subagents in Codex are **TOML** files (`.codex/agents/<name>.toml` or `~/.codex/agents/<name>.toml`) with required `name`, `description`, and `developer_instructions`. | `agent-configuration/subagents` |
| Putting `sandbox_mode`, `approval_policy`, or `mcp_servers` inside a custom agent `.toml` | Ignored in custom agent files — subagents inherit sandbox, approvals, and MCP servers from the parent session. | `agent-configuration/subagents` |
| Running `codex mcp-server` or `codex-mcp-server` | **Removed.** Migrate integrations to `codex app-server` (JSON-RPC). To connect Codex *to* external MCP servers, use `codex mcp` or `[mcp_servers.<name>]` in `config.toml`. | `mcp-server` |
| Mixing `default_permissions` / `[permissions.<name>]` with `sandbox_mode` | They **do not compose**. If `sandbox_mode` or `--sandbox` is set anywhere in active config layers, Codex ignores `default_permissions` (unless managed `allowed_permission_profiles` is enforced). | `permissions` |
| Assuming `network.enabled = true` in a permission profile enforces domain allowlists | Domain rules in `[permissions.<name>.network]` only take effect when `features.network_proxy = true` (or managed `[experimental_network]`) is also enabled. | `permissions` |
| Setting `allow_managed_hooks_only = true` in `config.toml` | Ignored in `config.toml` — only supported in admin-enforced `requirements.toml`. | `hooks`, `config-file/config-reference` |
