# Codex Config, Profiles, MCP & Non-Interactive CLI Quick Reference

Verified against https://learn.chatgpt.com/docs/config-file/config-basic.md, config-advanced.md, config-reference.md, and mcp-server.md on 2026-10-24. Live page wins on any conflict.

## 1. Configuration Precedence (Highest to Lowest)

1. CLI flags and `-c` / `--config key=value` overrides (TOML values parsed inline)
2. Project config files: `.codex/config.toml` from project root down to CWD (**closest wins; loaded only when the project is trusted**)
3. Selected profile file: `--profile <name>` (`~/.codex/<name>.config.toml` or `[profiles.<name>]` in `config.toml`)
4. User config: `~/.codex/config.toml`
5. Cloud-managed `config.toml` defaults (workspace delivery)
6. System config: `/etc/codex/config.toml` (Unix)
7. Built-in defaults

**Untrusted project rule:** When a project is marked untrusted, Codex skips all project-scoped `.codex/` layers (`config.toml`, `hooks.json`, `rules/*.rules`, `agents/*.toml`).

**Admin constraints (`requirements.toml`):** Enforces upper bounds (`allowed_approval_policies`, `allowed_sandbox_modes`, `allowed_permission_profiles`, `allow_managed_hooks_only = true`) that user/project `config.toml` cannot relax.

## 2. Core `config.toml` Structure

```toml
model = "gpt-6.1-sol"
model_reasoning_effort = "high"   # low | medium | high | xhigh | max | ultra
approval_policy = "on-request"    # untrusted | on-failure | on-request | never
sandbox_mode = "workspace-write"  # read-only | workspace-write | danger-full-access

[agents]
enabled = true
max_concurrent_threads_per_session = 8
default_subagent_model = "gpt-6-luna"
default_subagent_reasoning_effort = "high"

[mcp_servers.docs]
url = "https://developers.openai.com/mcp"
startup_timeout_sec = 20

[mcp_servers.local_tool]
command = "npx"
args = ["-y", "@my-org/mcp-server"]
env = { API_URL = "https://api.example.com" }
```

## 3. MCP Integration vs. App Server

- **Consuming MCP servers in Codex:** Use `codex mcp` CLI subcommands or `[mcp_servers.<name>]` in `config.toml` (`command`/`args`/`env` for stdio, `url` for HTTP/SSE).
- **Hosting Codex:** `codex mcp-server` and `codex-mcp-server` were **removed**. Use `codex app-server` (JSON-RPC protocol; see `upkeep.sh fetch app-server`).
