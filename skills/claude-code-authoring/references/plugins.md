# Plugins quick reference

Verified against https://code.claude.com/docs/en/plugins-reference.md and
plugin-evals.md on 2026-10-24. Live page wins on any conflict.

## Layout (auto-discovered)

```
my-plugin/
├── .claude-plugin/plugin.json   # optional manifest
├── skills/<name>/SKILL.md       # preferred for all new commands and skills
├── commands/*.md                # legacy flat skills
├── agents/*.md                  # subfolders allowed
├── workflows/*.js
├── output-styles/*.md
├── hooks/hooks.json
├── .mcp.json
├── .lsp.json
├── bin/                         # added to PATH
├── settings.json                # only `agent` and `subagentStatusLine` keys
├── themes/, monitors/           # experimental
└── evals/                       # claude plugin eval cases
```

A manifest `skills` path adds to `skills/`. Custom `commands`, `agents`,
`workflows` and `outputStyles` paths replace the default scan. Plugins can't
reference files outside their root.

## plugin.json

- **Required:** `name` (kebab-case).
- **Metadata:** `$schema`, `displayName`, `version`, `description`, `author`
  {name, email, url}, `homepage`, `repository`, `license`, `keywords`,
  `metadata`, `defaultEnabled`.
- **Component paths:** `skills`, `commands`, `agents`, `workflows`, `hooks`,
  `mcpServers`, `outputStyles`, `lspServers`, `experimental.themes`,
  `experimental.monitors`, `experimental.evals`.
- **Configuration:** `userConfig` (prompted at enable; exposed as
  `CLAUDE_PLUGIN_OPTION_<KEY>`), `channels`, `dependencies` (semver
  constraints; see the `plugin-dependencies` page).

## Variables

- **`${CLAUDE_PLUGIN_ROOT}`:** install dir. It changes on update, and the old
  version is kept for about 14 days.
- **`${CLAUDE_PLUGIN_DATA}`:** `~/.claude/plugins/data/{id}/`; survives updates.
- **`${CLAUDE_PROJECT_DIR}`:** the project root.

All three are exported to hooks and to MCP/LSP processes. In shell-form hooks,
quote them: `"${CLAUDE_PLUGIN_ROOT}"/scripts/x.sh`.

## CLI

```bash
claude plugin init <name> --with skills agents hooks mcp   # scaffold
claude plugin validate ./my-plugin --strict                # manifest/structure
claude --plugin-dir ./my-plugin                            # load for one session
claude plugin eval init                                    # scaffold eval suite
claude plugin eval ./my-plugin                             # run vs no-plugin baseline
claude plugin install|enable|disable|update|uninstall <plugin>[@marketplace]
```

In a session, run `/reload-plugins` after changing hooks, agents, MCP or
output styles.

Eval cases live in `evals/` as `case.yaml`, or `prompt.md` plus
`graders/*.md`. This format differs from skill-creator's `evals/evals.json`.
