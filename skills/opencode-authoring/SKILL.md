---
name: opencode-authoring
description: Author and review OpenCode extensions and configuration against the live OpenCode documentation instead of stale training data. Use when creating, editing, or debugging opencode.json / opencode.jsonc, custom agents (.opencode/agents/*.md or agent config), permissions, custom tools (.opencode/tools/*.ts), plugins (.opencode/plugins/*.ts and @opencode-ai/plugin hooks), slash commands (.opencode/commands/*.md), skills (.opencode/skills/*/SKILL.md), AGENTS.md rules, or MCP server configs.
license: MIT
allowed-tools: Bash(${CLAUDE_SKILL_DIR}/scripts/upkeep.sh *)
metadata:
  author: KHAEntertainment
  version: "0.1.0"
  upkeep-pins: "opencode-ai=1.19.4"
  upkeep-threshold-days: "60"
  upkeep-source-base: "https://raw.githubusercontent.com/anomalyco/opencode/dev/packages/web/src/content/docs"
  upkeep-source-ext: ".mdx"
  upkeep-source-cache: "opencode-docs"
  upkeep-source-pages: "config, agents, commands, custom-tools, plugins, skills, rules, mcp-servers, permissions, policies, providers, tools, tui, cli, acp, sdk, server"
  upkeep-frozen-live-docs-authority: "sha256:0d0675fd3443192b"
---

# OpenCode Authoring

<!-- frozen:live-docs-authority -->
OpenCode (`opencode-ai`) evolves rapidly across config schemas, permission precedence, plugin hook signatures, and OpenCode 1 vs. OpenCode 2 boundaries. The live `.mdx` documentation in `anomalyco/opencode` (`packages/web/src/content/docs/*.mdx`) is authoritative; every file in this skill is a dated cache of those docs.
<!-- /frozen:live-docs-authority -->

## Freshness & Integrity Check

!`"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" check "${CLAUDE_SKILL_DIR}"`

- If the line above was not expanded into a `VERDICT:` line (non-Claude harnesses), run `scripts/upkeep.sh check` from this skill's directory first.
- **`VERDICT: TAMPERED`** → Stop immediately. A frozen owner-policy block was modified without authorization; revert or ask the user.
- **`VERDICT: STALE`** → `opencode-ai` has bumped or reference dates exceeded 60 days. Fetch the live page via `scripts/upkeep.sh fetch <page>` before relying on cached guidance, then update the affected references and `Verified` dates.
- **`VERDICT: FRESH`** → Proceed using the workflow and redirect table below.

## Workflow

1. **Identify the OpenCode surface** (`opencode.json`, agents, permissions, custom tools, plugins, commands, skills, `AGENTS.md`, or MCP).
2. **Fetch the live `.mdx` page before writing exact config keys or TypeScript hook signatures:**
   ```bash
   f=$("${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" fetch plugins)
   grep -n 'tool\.execute\|shell\.env' "$f"
   # List all configured OpenCode doc pages:
   "${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" fetch --list
   ```
3. **Consult the quick references** for orientation, then verify any specific field against the fetched live page.

| Component | Page(s) (pass to `upkeep.sh fetch`) | Quick reference |
|---|---|---|
| Config (`opencode.json`), Agents, Permissions & MCP | `config`, `agents`, `permissions`, `policies`, `mcp-servers`, `providers` | [`references/config-and-agents.md`](references/config-and-agents.md) |
| Custom Tools & Plugins (`@opencode-ai/plugin`) | `custom-tools`, `plugins`, `tools`, `sdk` | [`references/tools-and-plugins.md`](references/tools-and-plugins.md) |
| Slash Commands, Skills & `AGENTS.md` Rules | `commands`, `skills`, `rules`, `tui`, `cli`, `acp` | [`references/commands-and-skills.md`](references/commands-and-skills.md) |

## Redirect Table: What Old Guidance Gets Wrong

Verified 2026-10-24 against `opencode-ai@1.19.4` and `anomalyco/opencode` live `.mdx` docs.

| Stale Training Habit / Old Assumption | Current OpenCode Reality (`opencode-ai` 1.19.4) | Live Page to Verify |
|---|---|---|
| Using legacy `"tools": { "bash": false }` boolean map in `opencode.json` | **Deprecated since v1.1.1.** Use `"permission"` (`"allow"`, `"ask"`, `"deny"`, or pattern objects like `"bash": { "*": "ask", "git *": "allow" }`). | `permissions`, `agents` |
| Confusing singular vs. plural names in `.opencode/` vs. `opencode.json` | Subdirectories in `.opencode/` and `~/.config/opencode/` are **plural** (`agents/`, `commands/`, `plugins/`, `skills/`, `tools/`, `themes/`). Keys in `opencode.json` are **singular** (`"agent"`, `"command"`, `"plugin"`, `"permission"`, `"mcp"`). | `config` |
| Setting top-level `"permission": { "edit": "allow" }` or `"*": "allow"` and expecting the `plan` agent to stay read-only | `plan` agent defaults apply *before* top-level `permission`, and **last matching rule wins**. Override `"agent": { "plan": { "permission": { "edit": "deny" } } }` explicitly. | `agents`, `permissions` |
| Using `experimental.policies` (`provider.use`) in OpenCode 1 | `experimental.policies` is **only enforced in OpenCode 2**. In OpenCode 1, use `disabled_providers` or `enabled_providers` in `opencode.json`. | `policies`, `config` |
| Putting URL-pattern objects under `permission.webfetch` | In OpenCode 1, `permission.webfetch` accepts only `"allow"`, `"ask"`, or `"deny"` (no pattern object). | `permissions` |
| Using `console.log` inside `.opencode/plugins/*.ts` | Corrupts TUI rendering; use `client.app.log()` from the plugin context instead. | `plugins` |
