[![Self-updating-skills](https://ascii.rest/banner/Self-updating-skills.svg?color=d6692e,486af4&font=bold&shadow=heavy&fill=block&size=l)](https://ascii.rest/banner/)

[![skills.sh](https://skills.sh/b/KHAEntertainment/self-updating-skills)](https://skills.sh/KHAEntertainment/self-updating-skills)

Agent Skills (`SKILL.md`) fail in two opposite ways over time:
1. **Static skills rot silently** as soon as an upstream CLI, SDK, cloud platform, or coding harness ships a breaking change (e.g., `npm` replacing `NPM_TOKEN` with OIDC trusted publishing, or `openai/codex` moving its documentation and deprecating `~/.codex/prompts/`).
2. **Unbounded "self-evolving" skills poison context** when agents freely rewrite `SKILL.md` after hitting runtime errors, overwriting the human owner's security posture, architectural policy, or design taste.

**`self-updating-skills`** solves both with a **Three-Layer Skill Architecture** and a single zero-dependency runtime script (`scripts/upkeep.sh`, ~320 lines, requiring only `bash`, `curl`, `git`, and stdlib `python3`).

---

## The Three-Layer Model

| Layer | What belongs here | Where it lives | How `upkeep.sh` enforces it |
|---|---|---|---|
| **1. Frozen (Owner Policy & Taste)** | Non-negotiable workflow rules, security posture, design aesthetic, and what *never* to do | `SKILL.md` inside `<!-- frozen:<id> --> ... <!-- /frozen:<id> -->` | `upkeep.sh stamp` writes a SHA-256 digest (`upkeep-frozen-<id>`) into frontmatter; `upkeep.sh check` reports `VERDICT: TAMPERED` if modified |
| **2. Cached (Redirect Table & Orientation)** | Stale-habit → current-equivalent redirect table and high-level component map | `SKILL.md` + dated `references/*.md` (`Verified YYYY-MM-DD`) | `upkeep.sh check` compares `upkeep-pins` against live npm/GitHub releases and flags `Verified` dates older than `upkeep-threshold-days` (default 60d) |
| **3. Volatile (Exact Flags, Schemas, Limits)** | CLI flags, config keys, hook event payloads, rate limits, API signatures | Live upstream raw `.md`/`.mdx` pages configured via `upkeep-source-*` | `upkeep.sh fetch <page>` caches raw docs in `$TMPDIR` (12h TTL) with loud `⚠ OFFLINE/FETCH-FAILED` fallback warnings |

---

## Bundled Skills

| Skill | Target Domain | Live Pins (`upkeep-pins`) | Live Upstream Docs Source (`upkeep-source-base`) |
|---|---|---|---|
| [`self-updating-skills`](skills/self-updating-skills/SKILL.md) | Scaffold, retrofit, stamp, and audit self-updating skills across any domain | `skills=1.7.4`, `agentskills=github:agentskills/agentskills@69ef37e` | `https://raw.githubusercontent.com/agentskills/agentskills/main/docs` (`.mdx`) |
| [`claude-code-authoring`](skills/claude-code-authoring/SKILL.md) | Author & review Claude Code skills, hooks, subagents, plugins, marketplaces, and settings | `@anthropic-ai/claude-code=2.1.310` | `https://code.claude.com/docs/en` (`.md` + `llms.txt` index) |
| [`codex-authoring`](skills/codex-authoring/SKILL.md) | Author & review OpenAI Codex CLI/IDE `AGENTS.md`, `.codex/config.toml`, skills, TOML subagents, hooks, `.rules`, and permission profiles | `@openai/codex=0.164.0` | `https://learn.chatgpt.com/docs` (`.md` + `developers.openai.com/codex/llms.txt` index) |
| [`opencode-authoring`](skills/opencode-authoring/SKILL.md) | Author & review OpenCode `opencode.json`, agents, permissions, custom tools, `@opencode-ai/plugin` hooks, commands, and skills | `opencode-ai=1.19.4` | `https://raw.githubusercontent.com/anomalyco/opencode/dev/packages/web/src/content/docs` (`.mdx`) |

---

## Installation

### 1. Claude Code (`kha-marketplace`)

```bash
/plugin marketplace add KHAEntertainment/marketplace
/plugin install self-updating-skills@kha-marketplace
```

### 2. Cross-Harness (`skills.sh` — Claude Code, Codex, OpenCode, Cursor, Antigravity)

```bash
# Install all 4 skills
npx skills add KHAEntertainment/self-updating-skills

# Or install a single skill
npx skills add KHAEntertainment/self-updating-skills --skill self-updating-skills
npx skills add KHAEntertainment/self-updating-skills --skill claude-code-authoring
npx skills add KHAEntertainment/self-updating-skills --skill codex-authoring
npx skills add KHAEntertainment/self-updating-skills --skill opencode-authoring
```

---

## `upkeep.sh` CLI Reference

`bin/upkeep.sh` (vendored into each skill as `scripts/upkeep.sh`) provides four subcommands:

```bash
# 1. Check version pins, Verified dates, and frozen SHA-256 blocks in a single pass
./bin/upkeep.sh check skills/claude-code-authoring
./bin/upkeep.sh check --strict skills/codex-authoring   # exits 1 on STALE or TAMPERED

# 2. Fetch live upstream markdown documentation (with 12h $TMPDIR cache & offline fallback)
./skills/codex-authoring/scripts/upkeep.sh fetch config-file/config-reference
./skills/codex-authoring/scripts/upkeep.sh fetch --index
./skills/opencode-authoring/scripts/upkeep.sh fetch --list

# 3. Stamp <!-- frozen:id --> blocks into SKILL.md frontmatter as flat upkeep-frozen-<id> keys
./bin/upkeep.sh stamp skills/self-updating-skills

# 4. Sync / upgrade scripts/upkeep.sh across any number of skills
./bin/upkeep.sh sync skills/*
```

## License

MIT
