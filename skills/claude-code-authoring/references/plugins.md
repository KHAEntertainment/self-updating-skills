# Plugins quick reference

Verified against https://code.claude.com/docs/en/plugins-reference.md and
plugin-evals.md on 2026-11-03. The "Plugin options" section was verified on
2026-11-11 against plugins-reference, settings-reference, hooks and mcp, plus
Claude Code 2.1.322. Live page wins on any conflict.

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
- **Configuration:** `userConfig` (prompted at enable; see "Plugin options"
  below), `channels`, `dependencies` (semver constraints; see the
  `plugin-dependencies` page).

## Variables

- **`${CLAUDE_PLUGIN_ROOT}`:** absolute path of the installed version. It
  changes on update, so don't write state there. The `plugins/loading` page
  says when the old directory is cleaned up.
- **`${CLAUDE_PLUGIN_DATA}`:** `~/.claude/plugins/data/{id}/`; survives updates.
- **`${CLAUDE_PROJECT_DIR}`:** the project root.

Hooks and LSP servers get all three as environment variables (hooks also get
`CLAUDE_PLUGIN_OPTION_<KEY>`); stdio MCP servers get only `CLAUDE_PLUGIN_ROOT`
and `CLAUDE_PLUGIN_DATA`; monitors and http/sse/ws MCP servers get none. None
are set in commands Claude runs through the Bash tool. In hooks, prefer exec
form (`args`, no shell, no quoting); in shell form, quote them:
`"${CLAUDE_PLUGIN_ROOT}"/scripts/x.sh`.

## Plugin options (userConfig)

Declare what the user must supply under `userConfig`; Claude Code builds the
dialog, stores the answers and masks secrets. Don't ask users to edit
`settings.json`. Read the "User configuration" section of `plugins-reference`
first; it is short.

- **Keys** are letters, digits and underscores, not starting with a digit.
  Each option is a **strict** object, so one unknown key fails the plugin.
  `type` (`string`, `number`, `boolean`, `directory`, `file`), `title` and
  `description` are required. Optional: `required`, `default`, `options`
  (string only, not with `multiple` or `sensitive`, labels 1-64 chars),
  `multiple`, `sensitive`, `min`/`max` (number).
- **Storage:** non-sensitive answers go to user-settings
  `pluginConfigs["name@marketplace"].options`. `sensitive: true` goes to the
  macOS Keychain, falling back to `~/.claude/.credentials.json` (the only
  store on platforms without a keychain). Project and local `pluginConfigs`
  are ignored, so values are user-wide, not per project. Don't read the
  keychain or credentials file to inspect them.
- **Use a value:** `${user_config.KEY}` in MCP and LSP config, in exec-form
  hook `command`/`args`, and in skill and agent text (non-sensitive only; a
  sensitive value there becomes a placeholder). Hooks also get every option
  as `CLAUDE_PLUGIN_OPTION_<KEY>` (uppercased).
- **Never `${user_config.*}` in** shell-form hook commands, monitor commands
  or MCP `headersHelper`: Claude Code refuses to start the component, even
  with no value set. Use exec form, the env var, or the server's `headers`.
- **Validation is per field only.** No regex, no cross-field or "set exactly
  one" rule; enforce those in the server or hook. `claude plugin validate`
  reports `${user_config.X}` references to undeclared options (v2.1.281+).
- **UI and CLI:** prompted when the plugin is enabled; `/plugin` > Installed >
  plugin > **Configure options** (see the `discover-plugins` page); `/config`
  rows for non-sensitive, non-`multiple` options (v2.1.269+). Shell:
  `claude plugin configure <plugin> [--json]` shows options and which are
  unset; `--values-stdin` saves a JSON object of single-line strings, and
  omitted options keep their values (v2.1.285+, in `--help` but not on the
  `cli-reference` page when checked). Closing `/plugin` with pending changes
  runs `/reload-plugins`; the `unifi-mcp` plugin reports that an open session
  keeps its old values until a reload or new session (not verified here).
- **Versions:** `options` makes the plugin fail to load before v2.1.271.
  Shell-form `${user_config.*}` was rejected from v2.1.207; before that it
  was substituted.
- **Untested here:** how an unset optional value reaches the MCP `env` (the
  `unifi-mcp` plugins say an empty string; treat as unverified), and whether
  a saved keychain secret can be cleared on current versions.

Sanitized example (passes `claude plugin validate --strict`, 2.1.322):

```json
{
  "mcpServers": { "svc": { "command": "npx", "args": ["-y", "svc-mcp@1.0.0"],
    "env": { "SVC_HOST": "${user_config.host}", "SVC_TOKEN": "${user_config.api_token}",
             "SVC_MODE": "${user_config.mode}" } } },
  "userConfig": {
    "host": { "type": "string", "title": "Host", "description": "Service hostname", "required": true },
    "api_token": { "type": "string", "title": "API token", "description": "Kept in the keychain", "sensitive": true },
    "mode": { "type": "string", "title": "Mode", "description": "read-only or read-write",
              "options": ["read-only", "read-write"], "default": "read-only" }
  }
}
```

Real-world reference: `sirkirby/unifi-mcp`
`plugins/unifi-network/.claude-plugin/plugin.json`. Its main convention to
copy: anything that changes state defaults to off.

## CLI

```bash
claude plugin init <name> --with skills agents hooks mcp   # scaffold
claude plugin validate ./my-plugin --strict                # manifest/structure
claude --plugin-dir ./my-plugin                            # load for one session
claude plugin eval init                                    # scaffold eval suite
claude plugin eval ./my-plugin                             # run vs no-plugin baseline
claude plugin install|enable|disable|update|uninstall <plugin>[@marketplace]
claude plugin configure <plugin> [--json] [--values-stdin] # userConfig values
```

In a session, run `/reload-plugins` after changing hooks, agents, MCP or
output styles.

Eval cases live in `evals/` as `case.yaml`, or `prompt.md` plus
`graders/*.md`. This format differs from skill-creator's `evals/evals.json`.
