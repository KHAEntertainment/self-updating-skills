---
name: claude-code-authoring
description: Author and review Claude Code extensions against the live, current docs instead of stale training data or old bundled guides. Use when creating, editing, reviewing or debugging a Claude Code skill (SKILL.md, frontmatter, $ARGUMENTS, context fork), hook (settings.json hooks, hooks.json, exit codes, decision JSON), subagent (agents/*.md), plugin (plugin.json, marketplace, plugin layout, plugin validate or eval), output style, or Claude Code settings. Use it even when the request only mentions one field or event name, and whenever older guidance (plugin-dev, docs mirrors, "This skill should be used when...") is in play.
license: MIT
allowed-tools: Bash(${CLAUDE_SKILL_DIR}/scripts/upkeep.sh *)
metadata:
  author: KHAEntertainment
  version: "0.2.0"
  upkeep-pins: "@anthropic-ai/claude-code=2.1.310"
  upkeep-threshold-days: "60"
  upkeep-source-base: "https://code.claude.com/docs/en"
  upkeep-source-ext: ".md"
  upkeep-source-cache: "claude-code-docs"
  upkeep-source-index: "https://code.claude.com/docs/llms.txt"
  upkeep-source-pages: "skills, hooks, hooks-guide, sub-agents, plugins, plugins-reference, plugin-marketplaces, plugin-evals, plugin-dependencies, settings, settings-reference, permissions, permission-modes, mcp, output-styles, memory, commands"
  upkeep-frozen-live-docs-authority: "sha256:b28d97505fc4121b"
  upkeep-verified: "2026-10-24"
---

# Claude Code Authoring

<!-- frozen:live-docs-authority -->
Claude Code ships changes weekly. Your training data, the `plugin-dev` plugin, and
local docs mirrors all lag behind it: as of September 2026 `plugin-dev` knew 9 of 33
hook events and 3 skill frontmatter fields. So for anything you write or review
here, the live docs at `code.claude.com` are authoritative, and every file in this
skill is just a cache of them.
<!-- /frozen:live-docs-authority -->

## Freshness & Integrity Check

!`"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" check "${CLAUDE_SKILL_DIR}"`

- If the line above was not expanded into a `VERDICT:` line (non-Claude harnesses), run `scripts/upkeep.sh check` from this skill's directory first.
- **`VERDICT: TAMPERED`** → Stop immediately. A frozen owner-policy block was modified without authorization; revert or ask the user.
- **`VERDICT: STALE`** → Cached references or version pins have drifted. Fetch the live page via `scripts/upkeep.sh fetch <page>` and re-verify before relying on the guidance below, then refresh the affected `Verified` dates and pins.
- **`VERDICT: CONTENT-LAG`** → The pinned package moved on (`latest_seen` in `upkeep.data.json` is ahead of `verified_for`) and the update is pending re-verification. Fetch the live page for anything version-sensitive before acting.
- **`VERDICT: FRESH`** → Proceed.

## Version pins and freshness

Machine-rendered from `upkeep.data.json` by `upkeep render`; hand edits are reported as `DRIFT` by `upkeep check` and overwritten on the next render.

<!-- generated:pins -->
| package | verified for | latest seen | checked |
| --- | --- | --- | --- |
| @anthropic-ai/claude-code | 2.1.310 | 2.1.312 | 2026-10-27 |
<!-- /generated:pins -->

<!-- generated:freshness -->
Verified 2026-10-24 for @anthropic-ai/claude-code@2.1.310
<!-- /generated:freshness -->

## Workflow

1. **Name the component.** Skill, hook, subagent, plugin/marketplace, MCP config,
   output style, settings, or permissions. Each has one primary docs page (table
   below).

2. **Read the live page before writing.** Fetch the raw markdown, then grep it:

   ```bash
   f=$("${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" fetch hooks)   # prints cached path
   grep -n 'permissionDecision' "$f"
   ```

   If the fetch prints a `⚠` line, you're offline or the page failed to fetch —
   it fell back to a stale cache (or returned nothing). Re-verify against the
   live page before trusting the result, and never guess an unavailable page's
   contents.

   Use the raw page rather than a summarizing fetch. Summaries paraphrase enum
   values: one reported PreToolUse `permissionDecision` as `allow|deny|skip`, but
   the real values are `allow|deny|ask|defer`. Grep for the exact field or event
   name you're about to write, and read the surrounding section. The pages are
   long (`hooks.md` is ~4,200 lines), so grep for headings (`grep -n '^#'`) and
   read the section you need rather than the whole file. Run
   `"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" fetch --index` to list every page when
   the table below doesn't cover something.

3. **Use the quick references for orientation, not as the final word.** They were
   verified on the date in their header. If the live page disagrees, the page
   wins; mention the drift to the user so the reference can be refreshed.

4. **Write it using current conventions** (below), then **validate** (below).

| Component | Page(s) (pass to `upkeep.sh fetch`) | Quick reference |
|---|---|---|
| Skills | `skills` | `references/skills.md` |
| Hooks | `hooks` (reference), `hooks-guide` (tutorial) | `references/hooks.md` |
| Subagents | `sub-agents` | `references/subagents.md` |
| Plugins | `plugins`, `plugins-reference`, `plugin-marketplaces`, `plugin-evals`, `plugin-dependencies` | `references/plugins.md` |
| Settings | `settings`, `settings-reference` | none; always read live |
| Permissions | `permissions`, `permission-modes` | none |
| MCP | `mcp` | none |
| Output styles | `output-styles` | none |
| Memory / CLAUDE.md | `memory` | none |
| Built-in commands | `commands` | none |
| Agent SDK | `agent-sdk/overview`, `agent-sdk/skills`, `agent-sdk/hooks`, ... | none |

## Current conventions that old guidance gets wrong

Verified 2026-10-24; distilled from the live pages in the table above (not one
single page). Re-verify each bullet against its component page when this date is
more than ~60 days old or you hit drift — these bullets are exactly the facts
that rot.

- **Skills replace commands.** `.claude/commands/x.md` still works, but new work
  goes in `skills/<name>/SKILL.md`. A plugin's `commands/` dir is legacy.
- **Descriptions say what, then "Use when...".** Put the key use case first. The
  listing truncates `description` + `when_to_use` at 1,536 characters combined.
  The old third-person "This skill should be used when the user asks..." pattern
  is outdated.
- **SKILL.md under 500 lines**, not a word budget. Move detail into sibling
  files and say when to read each one.
- **Reference bundled files with `${CLAUDE_SKILL_DIR}`** (the skill's own dir) or
  `${CLAUDE_PLUGIN_ROOT}` (plugin root). Put state that must survive plugin
  updates in `${CLAUDE_PLUGIN_DATA}`.
- **Control invocation with frontmatter**, not prose:
  `disable-model-invocation: true` for user-triggered side-effect workflows,
  `user-invocable: false` for background knowledge, `context: fork` + `agent` to
  run in a subagent, `allowed-tools` to pre-approve tools for that turn.
- **Plugin subagents ignore `hooks`, `mcpServers` and `permissionMode`.** Put those
  in the plugin's `hooks/hooks.json` / `.mcp.json` instead.
- **Hooks have five handler types** (`command`, `http`, `mcp_tool`, `prompt`,
  `agent`). Narrow tool hooks with the `if` field (permission-rule syntax), not
  only `matcher`. Treat `if` as best-effort, and use permission rules for hard
  allow or deny.
- **Blocking differs per event.** Exit 2 blocks only on events that can block,
  and the JSON decision field differs per event. Look up the event's "decision
  control" section every time.

Stale habits and their current equivalents (machine-rendered from
`upkeep.data.json`; rows are added by the redirect-drafting phase only after
verification):

<!-- generated:redirects -->
| stale habit → current | since | source |
| --- | --- | --- |
<!-- /generated:redirects -->

## Validate

- Plugins: `claude plugin validate <dir> --strict` for manifest and structure.
  Use `claude --plugin-dir <dir>` to load for one session, and `/reload-plugins`
  after edits to hooks, agents, MCP or output styles. Skill text reloads live.
- Behavior: `claude plugin eval init` scaffolds eval cases, and
  `claude plugin eval <dir-or-name>` runs them against a no-plugin baseline.
- Standalone skills: the `skill-creator` skill's eval loop and description
  optimizer.
- Hooks: pipe a sample JSON payload into the script and check its exit code and
  stdout before wiring it into settings.

## Keeping this skill current

The `references/*.md` files and the "Current conventions" block each carry a
"Verified" date. If a date is more than ~60 days old, or you hit drift, re-verify
that item against the live page (references name their page; the conventions
block is distilled from the component pages in the table above) and update both
the fact and its date. Keep only facts that are stable and frequently needed;
everything else stays a pointer to the live page.
