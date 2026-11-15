---
name: sglang-plugins
description: Extend SGLang without patching its source, using the general plugin hooks (BEFORE, AFTER, AROUND, REPLACE on any function, method or class via the sglang.srt.plugins entry point) and HiCache L3 storage backends (custom or dynamic backends, fast_file, mooncake, hf3fs, nixl, runtime attach and detach). Checks the live docs.sglang.io pages before acting. Use when writing, debugging or reviewing an SGLang general plugin or hook, a HookRegistry or plugin_hook registration, a HiCache storage backend or HiCacheStorage subclass, --hicache-* flags, or the /hicache/storage-backend admin endpoints, including for Engram Engine. Not for out-of-tree hardware platform plugins.
license: MIT
allowed-tools: Bash(${CLAUDE_SKILL_DIR}/scripts/upkeep.sh *)
metadata:
  author: KHAEntertainment
  version: "0.1.0"
  upkeep-pins: "sglang=github:sgl-project/sglang@0.5.23"
  upkeep-threshold-days: "30"
  upkeep-source-base: "https://docs.sglang.io"
  upkeep-source-ext: ".md"
  upkeep-source-cache: "sglang-docs"
  upkeep-source-index: "https://docs.sglang.io/llms.txt"
  upkeep-source-pages: "docs/hardware-platforms/plugin, docs/advanced_features/hicache_design, docs/advanced_features/hicache_best_practices, docs/advanced_features/hicache_storage_runtime_attach_detach, docs/advanced_features/hicache, docs/advanced_features/server_arguments"
  upkeep-frozen-scope: "sha256:958c5a0dc9a763e4"
  upkeep-frozen-update-policy: "sha256:158012c28a938201"
---

# SGLang Plugins and HiCache Storage

<!-- frozen:scope -->
This skill covers two parts of SGLang: the general plugin hooks (BEFORE, AFTER,
AROUND and REPLACE, registered through the `sglang.srt.plugins` entry point) and
HiCache storage backends (the L3 tier, custom and dynamic backends, and runtime
attach and detach). Engram Engine needs both.

Out-of-tree hardware platform plugins (`sglang.srt.platforms`) are out of scope:
the target hardware is NVIDIA L4 and Jetson, where CUDA is built in, not a plugin.
CUDA install and launch belong to the `sglang-nvidia` skill.

docs.sglang.io is the canonical copy of the docs. The lmsysorg.mintlify.app
address mirrors it; do not track the mirror.
<!-- /frozen:scope -->

<!-- frozen:update-policy -->
The tooling that keeps this skill current (self-updating-skills and upkeep.sh) is
an untested MVP. Never rewrite this skill's files on your own. When `upkeep.sh
check` reports STALE or CONTENT-LAG, or a live page disagrees with anything here,
write the change you propose as a unified diff, show it to the owner together with
the live page and section it comes from, and apply it only after the owner
approves that diff. This covers pins, Verified dates, references and the body of
this file alike.
<!-- /frozen:update-policy -->

## Freshness & Integrity Check

!`"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" check "${CLAUDE_SKILL_DIR}"`

- If the line above was not expanded into a `VERDICT:` line (non-Claude harnesses), run `scripts/upkeep.sh check` from this skill's directory first.
- **`VERDICT: TAMPERED`** → Stop. A frozen owner block was changed without approval; tell the owner.
- **`VERDICT: STALE`** or **`CONTENT-LAG`** → SGLang moved on or a Verified date is old. Read the live page for anything you rely on, and propose a diff for this skill as the update policy says. Do not edit the skill.
- **`VERDICT: FRESH`** → Use the orientation below, and still read the live page for exact signatures, flags and config keys.

## Workflow

1. **Pick the area**: a general plugin hook, or HiCache storage. Each has its pages
   in the table below.
2. **Read the live page before writing code.** Fetch the raw markdown and grep it:

   ```bash
   f=$("${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" fetch docs/hardware-platforms/plugin)
   grep -n '^#' "$f"                                  # find the section
   sed -n '/^## Plugin Type 2/,/^## File Reference/p' "$f"   # general plugins only
   ```

   A `⚠` line from `fetch` means the live page was unreachable and you got a cached
   copy or nothing. Say so, and never guess an unavailable page.
3. **Use the orientation below to know where to look**, not as the final word. If
   the live page disagrees, the page wins; propose a diff for this skill.
4. **Check the hook or backend actually took effect** (see the gotchas below).

| Area | Page (pass to `upkeep.sh fetch`) | Section to grep |
|---|---|---|
| General plugin hooks | `docs/hardware-platforms/plugin` | `## Plugin Type 2: General Plugin`, `### Plugin Loading Flow`, `#### Required platform initialization` |
| HiCache tiers and the storage interface | `docs/advanced_features/hicache_design` | `### Tier Sharing Scope`, `### Unified Interfaces and Rich L3 Storage Backends` |
| HiCache flags, backend setup, custom backends | `docs/advanced_features/hicache_best_practices` | `## Core HiCache Parameters`, `## Custom Storage Backend Integration` |
| Swapping a backend without a restart | `docs/advanced_features/hicache_storage_runtime_attach_detach` | `## 3. How to use` |
| Every server flag (about 160 KB, grep only) | `docs/advanced_features/server_arguments` | `grep -n 'hicache'` |
| Anything else | `fetch --index` lists every page | |

HiCache orientation is in [`references/hicache.md`](references/hicache.md). Read it
when the task touches cache tiers or a storage backend.

## General plugins: orientation

Verified 2026-11-14 against https://docs.sglang.io/docs/hardware-platforms/plugin.md.

- **Packaging.** A plugin is an installed Python package that declares an entry
  point in the `sglang.srt.plugins` group (diffusion uses
  `sglang.multimodal_gen.plugins`). The entry point names a zero-argument
  `register()` function, which registers hooks. Nothing in SGLang's source changes.
- **Hook API.** `HookRegistry.register(target, fn, HookType.X)` or the
  `@plugin_hook(target, type=HookType.X)` decorator, both from
  `sglang.srt.plugins.hook_registry`. Diffusion has its own registry in
  `sglang.multimodal_gen.runtime.platforms.plugins`; a hook registered through the
  other runtime's API never runs.
- **Hook types.** BEFORE can rewrite the arguments, AFTER can replace the result,
  AROUND receives the original function and must call it, REPLACE swaps the
  function or a whole class. Only REPLACE accepts a class. Read the exact
  signatures from the live page.
- **Targets** are dotted paths (`sglang.srt.managers.scheduler.Scheduler.__init__`);
  the entry-point form with a colon also works. The live page lists common
  scheduler, `ServerArgs` and `TpModelWorker` targets.
- **When hooks apply.** `load_plugins()` runs in the main process and again in each
  scheduler subprocess before the `Scheduler` is built. Hooks do not cross a spawn
  boundary, so register them in `register()`, not as a side effect of your own
  launch script.
- **Selection.** `SGLANG_PLUGINS` is a comma-separated allowlist of plugin names;
  when it is set, an unlisted plugin does not load.

## Gotchas

Verified 2026-11-14 against the pages named in each item.

- **A broken plugin can fail silently.** A plugin that does not ship in the selected
  platform's own package is best-effort: if it fails to load or apply, SGLang logs
  it and keeps serving without it (`plugin` page, "Required platform initialization
  vs. optional hooks"). After a launch, confirm your hook ran, for example with a
  log line from an AROUND hook on `Scheduler.__init__`.
- **An allowlist drops plugins.** If `SGLANG_PLUGINS` is set and your plugin's
  entry-point name is not in it, the plugin never loads (`plugin` page).
- **Host memory is never shared.** L1 (GPU) and L2 (host) are private to one
  instance; only an L3 storage backend can share KV across instances
  (`hicache_design`, "Tier Sharing Scope").
- **Attach and detach need an idle server and the admin key.** The calls fail with
  HTTP 400 while any request is running or queued, and with 403 when no admin key
  is configured; `--api-key` is not accepted (`hicache_storage_runtime_attach_detach`).
- **The default L3 path does not survive a restart.** The `file` backend (and
  `fast_file`, which shares its root) writes to `/tmp/hicache` by default, and a
  reboot or tmp cleanup can wipe it. For any test whose cache has to survive a
  restart, set a persistent path with `SGLANG_HICACHE_FILE_BACKEND_STORAGE_DIR`
  (current docs also accept a `file_storage_path` extra-config key) and check the
  path in the startup log (`hicache_best_practices`, "Deployment with Local
  Filesystem Backends").
