# Codex Skills, `AGENTS.md`, Subagents & Hooks Quick Reference

Verified against https://learn.chatgpt.com/docs/build-skills.md, agent-configuration/agents-md.md, agent-configuration/subagents.md, and hooks.md on 2026-10-24. Live page wins on any conflict.

## 1. `AGENTS.md` Discovery & Precedence

Codex builds its instruction chain once at session start (or per-turn when CWD changes) from:
1. **Global user layer:** `~/.codex/AGENTS.override.md` (if non-empty) else `~/.codex/AGENTS.md`
2. **Project layers (Git root → CWD):** In each directory along the path, checks `AGENTS.override.md`, then `AGENTS.md`, then names in `project_doc_fallback_filenames` (at most one file per directory).

- Combined size cap: `project_doc_max_bytes` (default `32768` bytes / 32 KiB).
- Custom prompts in `~/.codex/prompts/*.md` are **deprecated** — migrate reusable workflows to skills.

## 2. Codex Skills (`.agents/skills/<name>/SKILL.md`)

| Scope | Location |
|---|---|
| Repository | `.agents/skills/<name>/SKILL.md` (scanned from CWD up to repo root) |
| User | `~/.agents/skills/<name>/SKILL.md` |
| System / Admin | `/etc/codex/skills/<name>/SKILL.md` |

Layout:
```text
.agents/skills/my-skill/
├── SKILL.md
├── agents/
│   └── openai.yaml     # Optional UI display metadata, icon, default prompt, tool dependencies
├── scripts/
├── references/
└── assets/
```

Invoke explicitly in Codex with `$skill-name` (or `/skills`), or let Codex match `description` implicitly. Disable specific skills in `config.toml` via `[[skills.config]]` (`path = "..."`, `enabled = false`).

## 3. Custom Subagents (`.codex/agents/<name>.toml`)

Defined as **TOML** files in `~/.codex/agents/<name>.toml` (user) or `<repo>/.codex/agents/<name>.toml` (trusted project):

```toml
name = "reviewer"
description = "PR reviewer focused on correctness, security, and missing tests."
model = "gpt-6.1-sol"
model_reasoning_effort = "medium"
developer_instructions = """
Review code like an owner. Do not edit files.
Lead with concrete findings and reproduction steps.
"""
```

- Required fields: `name`, `description`, `developer_instructions`.
- Optional model fields: `model`, `model_reasoning_effort`, `model_reasoning_summary`, `model_verbosity`.
- Capability restriction flags (can set to `false`): `features.shell_tool`, `features.apps`, `features.plugins`, `features.memories`, `features.request_permissions_tool`, `skills.bundled.enabled`, `skills.include_instructions`, and `[[skills.config]]`.
- **Ignored in custom agent files:** `sandbox_mode`, `approval_policy`, `model_provider`, `mcp_servers` (always inherited from parent session).

## 4. Lifecycle Hooks (`hooks.json` or `[hooks]` in `config.toml`)

- **Locations:** `~/.codex/hooks.json`, `~/.codex/config.toml`, `<repo>/.codex/hooks.json`, `<repo>/.codex/config.toml`, and plugin `hooks/hooks.json`.
- **Events:** `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `PreCompact`, `Stop`, `Interrupt` (main thread only), `SessionStart`, `SubagentStart`, `SessionEnd` (main thread only).
- **Trust review:** Non-managed hooks are hash-pinned and skipped until reviewed and trusted via `/hooks` in the CLI (or bypassed in CI with `--dangerously-bypass-hook-trust`).
-Fetch full event payload and decision JSON schemas before authoring hooks: `upkeep.sh fetch hooks`.
