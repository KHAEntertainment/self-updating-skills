# Codex Sandboxing, Permission Profiles & `.rules` Quick Reference

Verified against https://learn.chatgpt.com/docs/sandboxing.md, agent-approvals-security.md, permissions.md, and agent-configuration/rules.md on 2026-10-24. Live page wins on any conflict.

## 1. Legacy Sandbox Modes vs. Permission Profiles (Mutually Exclusive)

Codex supports two local isolation models — **do not mix them in `config.toml`**:

1. **Legacy Sandbox + Approval Policy:**
   - `sandbox_mode`: `"read-only"` | `"workspace-write"` | `"danger-full-access"`
   - `approval_policy`: `"untrusted"` | `"on-failure"` | `"on-request"` | `"never"`
   - CLI preset `--full-auto` sets `--sandbox workspace-write --ask-for-approval on-request`.
2. **Permission Profiles (Beta, `default_permissions` + `[permissions.<name>]`):**
   - Built-ins: `":read-only"`, `":workspace"`, `":danger-full-access"`.
   - **Precedence trap:** If `sandbox_mode` appears in *any* loaded config file or `--sandbox` is passed, Codex ignores `default_permissions` and uses legacy sandbox settings (unless admin `allowed_permission_profiles` is set in `requirements.toml`).
   - **Network proxy trap:** Setting `network.enabled = true` inside `[permissions.<name>.network]` permits network access, but domain allow/deny rules are **only enforced if `features.network_proxy = true` is also set**.

Example custom permission profile:
```toml
default_permissions = "project-edit"

[features]
network_proxy = true

[permissions.project-edit.workspace_roots]
"~/code/shared-lib" = true

[permissions.project-edit.filesystem.":workspace_roots"]
write = true
".git" = "read"
".codex" = "read"

[permissions.project-edit.network]
enabled = true
allowed_domains = ["registry.npmjs.org", "github.com"]
```

## 2. Execution Policy Rules (`.codex/rules/*.rules`)

Starlark `.rules` files in `~/.codex/rules/*.rules` and `<repo>/.codex/rules/*.rules` (trusted projects only) control which commands can run **outside the sandbox**:

```python
prefix_rule(
    pattern = ["gh", "pr", ["view", "list"]],
    decision = "allow",          # "allow" | "prompt" | "forbidden" (most restrictive wins)
    justification = "Read-only GitHub PR inspection",
    match = ["gh pr view 123", "gh pr list"],
    not_match = ["gh pr merge 123"],
)
```

- Codex validates `match` and `not_match` inline tests at startup!
- Compound shell commands (`bash -lc "cmd1 && cmd2"`) with plain words and safe operators (`&&`, `||`, `;`, `|`) are split via tree-sitter and evaluated per subcommand.
