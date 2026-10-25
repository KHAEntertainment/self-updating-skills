# OpenCode Custom Tools & Plugins Quick Reference

Verified against `custom-tools.mdx` and `plugins.mdx` in `anomalyco/opencode` on 2026-10-24. Live page wins on any conflict.

## 1. Custom Tools (`.opencode/tools/*.ts` or `~/.config/opencode/tools/*.ts`)

Custom tools use `tool()` from `@opencode-ai/plugin` (`tool.schema` is Zod):
- **Default export** in `.opencode/tools/database.ts` → registers tool named `database`.
- **Named exports** `export const add = tool(...)` in `math.ts` → registers `math_add`.
- **Precedence:** A custom tool with the same name as a built-in tool (`bash`, `read`, `write`, `edit`) replaces the built-in tool.

```ts
import { tool } from "@opencode-ai/plugin"

export default tool({
  description: "Query the project SQLite database",
  args: {
    query: tool.schema.string().describe("Read-only SQL query"),
  },
  async execute(args, context) {
    const { directory, worktree, agent, sessionID } = context
    return `Executed in ${directory}: ${args.query}`
  },
})
```

To use third-party npm packages in local tools or plugins, add `.opencode/package.json` (OpenCode runs `bun install` automatically at startup).

## 2. Plugins (`.opencode/plugins/*.ts`, `~/.config/opencode/plugins/*.ts`, or `"plugin": [...]`)

Load order (all hooks run in sequence):
1. Global config `"plugin"` array (`~/.config/opencode/opencode.json`)
2. Project config `"plugin"` array (`opencode.json`)
3. Global plugin directory (`~/.config/opencode/plugins/`)
4. Project plugin directory (`.opencode/plugins/`)

```ts
import { type Plugin, tool } from "@opencode-ai/plugin"

export const GuardPlugin: Plugin = async ({ project, client, $, directory, worktree }) => {
  return {
    "tool.execute.before": async (input, output) => {
      if (input.tool === "read" && output.args.filePath?.includes(".env")) {
        throw new Error("Reading .env files is blocked by policy")
      }
    },
    "shell.env": async (input, output) => {
      output.env.PROJECT_ROOT = input.cwd
    },
    event: async ({ event }) => {
      if (event.type === "session.idle") {
        await client.app.log({ service: "guard-plugin", level: "info", message: "Session idle" })
      }
    },
  }
}
```

### Supported Plugin Hook Events
- **Tool:** `tool.execute.before`, `tool.execute.after`
- **Shell:** `shell.env`
- **Session:** `session.created`, `session.compacted`, `session.deleted`, `session.diff`, `session.error`, `session.idle`, `session.status`, `session.updated`
- **Permission:** `permission.asked`, `permission.replied`
- **File & LSP:** `file.edited`, `file.watcher.updated`, `lsp.client.diagnostics`, `lsp.updated`
- **Message & Command:** `message.updated`, `message.removed`, `message.part.updated`, `message.part.removed`, `command.executed`, `todo.updated`
- **TUI & Server:** `tui.prompt.append`, `tui.command.execute`, `tui.toast.show`, `server.connected`, `installation.updated`
