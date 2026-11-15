---
name: self-updating-skills
description: Design, scaffold, retrofit, stamp, and audit self-updating Agent Skills (SKILL.md) that stay accurate when upstream CLIs, libraries, APIs, or coding harnesses ship breaking changes. Use when creating a new skill for a moving target, retrofitting an existing skill with version pins or freshness checks, protecting owner taste/policy with SHA-256 frozen blocks, taking inventory of the skills installed on a machine and converting the ones that should be self-updating, or running upkeep.sh check/fetch/stamp/sync across a skill library.
license: MIT
allowed-tools: Bash(${CLAUDE_SKILL_DIR}/scripts/upkeep.sh *) Bash(${CLAUDE_SKILL_DIR}/scripts/scan.sh *)
metadata:
  author: KHAEntertainment
  version: "0.1.0"
  upkeep-pins: "skills=1.7.4, agentskills=github:agentskills/agentskills@69ef37e"
  upkeep-threshold-days: "60"
  upkeep-source-base: "https://raw.githubusercontent.com/agentskills/agentskills/main/docs"
  upkeep-source-ext: ".mdx"
  upkeep-source-cache: "agentskills-docs"
  upkeep-source-pages: "specification, clients, skill-creation/quickstart, skill-creation/best-practices, skill-creation/using-scripts, skill-creation/optimizing-descriptions, skill-creation/evaluating-skills"
  upkeep-frozen-governing-rule: "sha256:4143151c52aa78fc"
---

# Self-Updating Skills

<!-- frozen:governing-rule -->
Only content whose correctness is verifiable against an upstream source of truth (current package release, CLI schema, or official documentation) may be auto-updated. Everything that encodes owner policy, architectural taste, or methodology is frozen inside `<!-- frozen:id -->` markers, SHA-256 stamped in frontmatter, and never modified by an agent without explicit human approval.
<!-- /frozen:governing-rule -->

## Freshness & Integrity Check

!`"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" check "${CLAUDE_SKILL_DIR}"`

- If the line above was not expanded into a `VERDICT:` line (non-Claude harnesses), run `scripts/upkeep.sh check` from this skill's directory first.
- **`VERDICT: TAMPERED`** → Stop immediately. A frozen owner-policy block was modified without authorization; revert or ask the user.
- **`VERDICT: STALE`** → Cached references or version pins have drifted. Run `scripts/upkeep.sh fetch <page>` and rely on live docs before acting, then refresh the stale `references/` and `Verified` dates.
- **`VERDICT: CONTENT-LAG`** → the upstream version moved and the update is pending review. Fetch the live page for anything version-sensitive before acting.
- **`VERDICT: FRESH`** → Proceed using the cached references below; fetch live docs for any exact spec detail not covered in the summary.

## The Three-Layer Skill Architecture

Verified 2026-10-24 against https://raw.githubusercontent.com/agentskills/agentskills/main/docs/specification.mdx.

Every durable skill separates knowledge into three layers (see [`references/three-layer-model.md`](references/three-layer-model.md) for classification rules):

| Layer | What belongs here | Where it lives | How `upkeep.sh` enforces it |
|---|---|---|---|
| **1. Frozen (Owner Policy & Taste)** | Non-negotiable workflow rules, security posture, design aesthetic, and what *never* to do | `SKILL.md` wrapped in `<!-- frozen:<id> --> ... <!-- /frozen:<id> -->` | `upkeep.sh stamp` hashes the block into `metadata.upkeep-frozen-<id>`; `upkeep.sh check` flags `TAMPERED` if altered |
| **2. Cached (Redirect Table & Orientation)** | Stale-habit → current-equivalent redirect table and high-level workflow map | `SKILL.md` body + dated `references/*.md` (`Verified YYYY-MM-DD`) | `upkeep.sh check` compares `upkeep-pins` against live npm/GitHub releases and flags `Verified` dates older than `upkeep-threshold-days` (default 60d) |
| **3. Volatile (Exact Flags, Schemas, Limits)** | CLI flags, config keys, hook event payloads, rate limits, API signatures | Live upstream raw `.md`/`.mdx` pages configured via `upkeep-source-*` | `upkeep.sh fetch <page>` caches raw docs in `$TMPDIR` (12h TTL) with loud offline fallback warnings |

## Single-File `upkeep.sh` Runtime Workflow

Every self-updating skill vendors a single zero-dependency script at `scripts/upkeep.sh` (requiring only `bash`, `curl`, `git`, and stdlib `python3`):

```bash
# 1. Check pins, Verified dates, and frozen block SHA-256 hashes in one pass (~40ms)
"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" check
"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" check --strict   # exits 1 on STALE or TAMPERED (for CI/cron)

# 2. Fetch live upstream markdown docs (prints local cached path; grep the returned file)
doc=$("${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" fetch specification)
grep -n '^##' "$doc"
"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" fetch --list     # list configured pages (or --index for llms.txt)

# 3. Stamp <!-- frozen:id --> blocks into SKILL.md frontmatter after owner edits
"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" stamp

# 4. Vendor or upgrade scripts/upkeep.sh across one or more target skills
"${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" sync path/to/skill-a path/to/skill-b
```

## Creating or Retrofitting a Skill in 4 Steps

1. **Classify the target skill** using [`references/three-layer-model.md`](references/three-layer-model.md):
   - Pure taste/aesthetic skills → freeze entirely (do not attach version pins).
   - Library, CLI, cloud platform, or harness skills → split into Frozen policy, Cached redirect tables (`Verified YYYY-MM-DD`), and Volatile live docs.
2. **Add `agentskills.io`-compliant `upkeep-*` metadata** to `SKILL.md` frontmatter using [`references/frontmatter-spec.md`](references/frontmatter-spec.md). Keep all `metadata` values as flat strings so `skills-ref validate` passes cleanly.
3. **Wrap owner policy in `<!-- frozen:id -->` markers** and add the cross-harness `!` dynamic injection gate at the top of `SKILL.md`.
4. **Vendor `scripts/upkeep.sh`, stamp, and verify**:
   ```bash
   "${CLAUDE_SKILL_DIR}/scripts/upkeep.sh" sync <target-skill-dir>
   <target-skill-dir>/scripts/upkeep.sh stamp
   <target-skill-dir>/scripts/upkeep.sh check --strict
   ```

## Converting a Library of Skills

For more than one existing skill, start with the read-only scan. It finds every skill on the machine, says where each came from, and says which are stale:

```bash
"${CLAUDE_SKILL_DIR}/scripts/scan.sh" --owner <github-login>     # add --json for paths and details
```

Then follow [`references/convert-existing.md`](references/convert-existing.md): classify each skill, **get the owner's approval of the list**, convert one skill at a time with the 4 steps above, and report. Three rules hold throughout:

- The scan never writes. Nothing is modified before the owner approves the list.
- Converting does not make a skill current. Pin the version the skill was written for; a newly converted skill usually reads `STALE`, and refreshing its content is a separate pass.
- Skills installed from someone else are report-only. A reinstall overwrites local edits.

## Reference Files

- [`references/three-layer-model.md`](references/three-layer-model.md) — Classification matrix (Documentation-faithful vs. Opinionated vs. Hybrid), maintenance agent edit boundaries, and redirect-table design.
- [`references/frontmatter-spec.md`](references/frontmatter-spec.md) — Complete `upkeep-*` frontmatter schema, pin syntax (`npm`, `github` tag, `github` SHA, `manual`), and cross-harness injection templates.
- [`references/convert-existing.md`](references/convert-existing.md) — The find, triage, scan, classify, convert and report workflow for a library of existing skills; `scan.sh` states and the report format.
