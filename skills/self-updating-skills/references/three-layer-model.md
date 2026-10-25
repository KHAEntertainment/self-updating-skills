# Three-Layer Classification & Maintenance Policy

Verified against https://raw.githubusercontent.com/agentskills/agentskills/main/docs/skill-creation/best-practices.mdx on 2026-10-24. Live page wins on any conflict.

## 1. Why Static Skills Rot (and "Self-Evolving" Skills Poison Context)

Skills fail in two opposite ways over time:
1. **Silent Drift (Static Skills):** The skill embeds CLI flags, package versions, or API constructors from when it was written. Six months later, the library ships v2 or deprecates token auth, and the skill actively instructs the agent to write broken or insecure code.
2. **Policy Erosion (Unbounded "Self-Updating" Skills):** An agent hits a runtime error, decides to "improve" `SKILL.md`, and overwrites the human owner's security policy, architectural constraints, or design aesthetic with a quick workaround.

The Three-Layer Model solves both by separating **who owns what** inside a skill.

## 2. Skill Classification Matrix

Before adding `upkeep.sh` to a skill or repository, classify each skill:

| Bucket | Description | Examples | Maintenance Policy |
|---|---|---|---|
| **A. Deterministic / Documentation-Faithful** | Wraps a specific CLI, SDK, library, or coding harness whose correctness is verifiable against releases and docs | `npm-publish`, `claude-code-authoring`, `codex-authoring`, `opencode-authoring`, `threejs-webgl` | **Own & auto-maintain** Cached layer (`upkeep-pins` + `references/` API facts); **Freeze** owner policy (`<!-- frozen:id -->`); **Fetch** volatile details live |
| **B. Opinionated Methodology** | Encodes architectural philosophy, team workflow, or curated library picks | `modern-web-design`, `dev-skill` SOPs | **Freeze methodology**; only pin external CLI tools if the skill invokes specific commands |
| **C. Pure Aesthetic / Taste** | Anti-slop rules, visual direction, typography, or brand voice (100% opinion, 0% upstream API) | `design-taste-frontend`, `minimalist-ui` | **Freeze 100%** — never attach auto-updating agents |

## 3. File- and Section-Level Edit Boundaries for Maintenance Agents

When a scheduled maintenance agent (or a human running a refresh pass) sees `VERDICT: STALE`:

| Target | Layer | Allowed Maintenance Action |
|---|---|---|
| `<!-- frozen:id --> ... <!-- /frozen:id -->` | **Frozen** | **NEVER TOUCH.** Any edit causes `upkeep.sh check` to report `VERDICT: TAMPERED`. Only a human owner may edit and re-run `upkeep.sh stamp`. |
| `metadata.upkeep-pins` | **Cached** | Update pinned version/tag/SHA only after checking the upstream release notes or live docs for breaking changes. |
| Redirect tables ("Old habit → Current path") | **Cached** | Add newly deprecated flags/APIs and update replacements verified against live docs; bump `Verified YYYY-MM-DD`. |
| `references/*.md` API/schema summaries | **Cached** | Update renamed/removed fields, events, or CLI subcommands verified against `upkeep.sh fetch <page>`; bump `Verified YYYY-MM-DD`. |
| Full flag tables, long JSON schemas, edge-case options | **Volatile** | **Do not copy into `SKILL.md`.** Keep a one-line pointer to `upkeep.sh fetch <page>` and a `grep` pattern so the agent reads the live upstream doc on demand. |

## 4. Writing High-Leverage Redirect Tables

Models already have broad training-data priors for popular tools (`npm`, `claude`, `codex`, `three`). A skill wastes context if it repeats 2,000 lines of basic tutorial prose the model already knows.

Instead, focus the **Cached** layer on **Redirect Tables** — the specific places where model training data or older community plugins actively disagree with current upstream reality:

| Stale Training Habit / Old Guide | Current Upstream Truth | Where to Verify Live |
|---|---|---|
| What the model defaults to without the skill (e.g., `npm token create`, `.claude/commands/*.md`, `CODEX_UNSAFE_ALLOW_NO_SANDBOX`) | The current idiomatic replacement (e.g., OIDC trusted publishing, `.claude/skills/<name>/SKILL.md`, `sandbox_mode = "danger-full-access"`) | Exact page name for `upkeep.sh fetch <page>` |
