# Converting a Library of Existing Skills

How to take the skills already on a machine and make the right ones self-updating. Six steps: a script does the first three, an agent does the last three, and the owner approves the list in between.

| Step | Who | What it produces |
|---|---|---|
| 1. Find | `scripts/scan.sh` | One row per real skill folder; names with more than one copy flagged |
| 2. Triage | `scripts/scan.sh` | `yours`, `installed` or `unknown` per skill |
| 3. Scan | `scripts/scan.sh` | A state per skill: stale, behind the latest release, or no version named |
| 4. Classify | agent, then **owner approval** | A proposed list with one reason per skill |
| 5. Convert | agent | One skill at a time, through the 4-step flow in `SKILL.md` |
| 6. Report | agent | What was converted, what was skipped and why, what reads `STALE` |

## Three Rules

1. **Steps 1 to 3 never write.** `scan.sh` creates no file and edits none. It treats every scanned folder as hostile input: it runs no script from one, imports nothing from one, and never runs git inside one. It does not run the npm CLI either, which would write a cache and logs. It is safe on any machine.
2. **Converting does not make a skill current.** The pin records the version the skill was *written for*. A newly converted skill usually reads `VERDICT: STALE`, and that is the correct answer. Bringing the content up to date is a separate pass under the maintenance policy in [`three-layer-model.md`](three-layer-model.md).
3. **Installed skills are report-only.** A reinstall or a plugin update overwrites local edits, so a conversion made in place does not last. Convert an installed skill only on the owner's explicit yes for that skill.

## Steps 1 to 3: Run the Scan

```bash
"${CLAUDE_SKILL_DIR}/scripts/scan.sh" --owner <github-login>            # table
"${CLAUDE_SKILL_DIR}/scripts/scan.sh" --owner <github-login> --json     # paths, links and signals per skill
"${CLAUDE_SKILL_DIR}/scripts/scan.sh" --only path/to/skills             # just one folder or repo
"${CLAUDE_SKILL_DIR}/scripts/scan.sh" --offline                         # find and triage only, no network
```

It walks `~/.agents/skills`, `~/.claude/skills`, `~/.codex/skills`, `~/.config/opencode/skills`, `~/.gemini/skills`, `~/.gemini/config/skills`, `~/.cursor/skills` and `~/.openclaw/skills`, plus any path given. Symlinks are resolved, so a skill linked into five harnesses is one row.

**Triage** (the `ORIGIN` column), first match wins:

| Evidence | Origin |
|---|---|
| The folder is inside `~/.claude/plugins/` | `installed` (a plugin update replaces it) |
| The folder sits in `~/.agents/skills` and `~/.agents/.skill-lock.json` has a record for it | `yours` when the record's GitHub owner matches an `--owner`; otherwise `installed` |
| The folder is in a git checkout whose `origin` is on github.com | `yours` when that owner matches an `--owner`; otherwise `installed` |
| No record | `unknown` |

Without `--owner` nothing is marked `yours`, and a git checkout reads `unknown` because the scan cannot tell whose it is. A record never applies to a folder reached through a link from `~/.agents/skills`, and an `origin` on any other host proves nothing: both read `unknown`. Ask the owner about every `unknown` skill before treating it as theirs.

**Scan** (the `STATE` column):

| State | Meaning |
|---|---|
| `FRESH`, `STALE`, `TAMPERED`, `UNKNOWN` | Already self-updating (it has `upkeep-*` keys): the verdict `upkeep.sh check` gives, with the stale pins and dates listed. A pin that could not be resolved is listed too: `FRESH` with one is not proven. `UNKNOWN` with "check stopped" means the scan gave up on that skill (time limit, more than 30 pins, or a file it refuses to read) |
| `GAP` | Not self-updating, and a package version named in `SKILL.md` is behind the latest npm or PyPI release |
| `CURRENT` | Not self-updating; every package it names was found, and each version is the latest |
| `UNRESOLVED` | No gap found, but at least one named package was not found in a registry |
| `NO-SIGNAL` | Names no `package@version`. Either a taste or methodology skill, or a tool skill that never states the version it was written for |
| `NOT-CHECKED` | `--offline` was passed |
| `UNREADABLE` | Its `SKILL.md` could not be read, is not a regular file, or is over 4 MB (a file is never read in part). The rest of the report still stands |

The version test is deliberately narrow (`name@1.2.3`, `"name": "^1.2.3"`, `name==1.2.3`, in `SKILL.md` only), so `GAP` is strong evidence and `NO-SIGNAL` is none. Most tool skills read `NO-SIGNAL`. Step 4 decides.

`COPIES` shows how many different folders share a name: `3=` means three folders with an identical `SKILL.md`, `2≠` means two that differ, `2?` means one of them could not be read. Settle which copy is the real one before converting any of them. Above the table the scan lists broken links (those directly inside a scanned folder) and anything it could not read.

## Step 4: Classify and Get Approval

Read each candidate's `SKILL.md` and sort it with the matrix in [`three-layer-model.md`](three-layer-model.md):

| Bucket | Action | What the conversion adds |
|---|---|---|
| A. Wraps a CLI, library, platform or harness | **Convert** | Pins, a verified date, the check block, a frozen block for owner policy |
| B. Methodology | **Freeze the method** | The check block and a frozen block; a pin only for a tool it runs by name |
| C. Pure taste | **Freeze only** | The check block and a frozen block. No pins, no dates |
| Any bucket, origin `installed` | **Report only** | Nothing, unless the owner says yes for that skill |

Present the proposal as a table (skill, origin, state, bucket, action, one-line reason) and wait. **The owner approves or edits the list before any skill is modified.** Do not convert on the scan's output alone: a version number in the text does not make a skill a tool wrapper.

## Step 5: Convert One Skill at a Time

Run "Creating or Retrofitting a Skill in 4 Steps" from `SKILL.md` on each approved skill. Rules specific to converting:

- **Edit the real folder**, the `path` in the `--json` output, never a link to it. For a `yours` skill with an install record, convert it in its source repository and reinstall.
- **Pin the version the skill was written for**: the one it names, or the one the owner gives. Never pin the latest release to get a `FRESH` verdict.
- **No version known**: leave `upkeep-pins` out and set `upkeep-verified` to the date the content was last really edited (git history, then the install record, then the file date). The pin is added at the first content refresh.
- **Freeze what the owner wrote as policy**: rules, prohibitions, taste. Wrap existing text; do not reword it.
- **The diff is limited to four things**: `upkeep-*` frontmatter keys (plus `allowed-tools` for the script), the check block at the top, `<!-- frozen:id -->` markers around existing text, and `scripts/upkeep.sh`. Any other change to the skill's content belongs to a later refresh.
- **Verify**: `scripts/upkeep.sh check` prints `INTACT` for every new frozen block, and a diff against the original shows only the four additions. `STALE` is an acceptable verdict here; `TAMPERED` and `UNSTAMPED` are not.

## Step 6: Report

```markdown
## Conversion report — <date>

Scanned: <N> skill folders, <N> names, <N> duplicated, <N> already self-updating.

### Converted
| Skill | Bucket | Pin or verified date | Verdict after converting |

### Skipped
| Skill | Reason (installed / duplicate unresolved / owner declined / nothing to pin or freeze) |

### Needs a content refresh
| Skill | Written for | Latest | First thing to check upstream |

### Cleanup the owner may want
Broken links, and names whose copies differ.
```

"Needs a content refresh" lists every skill that reads `STALE` or `GAP`, converted or not. It is the to-do list for the refresh pass, which this workflow does not do.
