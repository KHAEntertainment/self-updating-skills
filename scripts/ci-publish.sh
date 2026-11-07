#!/usr/bin/env bash
# Publish an updater run to GitHub: one branch and one PR per skill.
#
# Reads the JSON that "upkeep update --runner ci --json" printed.
#
#   * A skill whose rows were all auto-accepted: the branch carries the
#     change and the PR is auto-merged with "gh pr merge --auto --squash"
#     once the required checks pass.
#   * A skill that needs a person (a PR-bound or fallback row): the branch
#     carries a pending-rows document and the PR is NOT auto-merged — a
#     human has to look at it.
#
# The PR body lists every row with its checks and its evidence quote.
#
# Nothing here writes to GitHub in the tests: "gh" is a normal program on
# PATH, so a test can put a stub there.
#
# Usage: ci-publish.sh [--base main] [--update-json FILE] [--repo DIR] [--dry-run]
set -uo pipefail

BASE="main"
JSON=""
REPO="$(pwd)"
DRY_RUN=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --base) BASE="${2:-}"; shift 2 ;;
    --update-json) JSON="${2:-}"; shift 2 ;;
    --repo) REPO="${2:-}"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    *) echo "ci-publish: unknown argument: $1" >&2; exit 2 ;;
  esac
done
if [[ -z "$JSON" ]]; then
  echo "ci-publish: --update-json FILE is required" >&2
  exit 2
fi
if [[ ! -f "$JSON" ]]; then
  echo "ci-publish: no such file: $JSON" >&2
  exit 2
fi

PLAN_DIR="$(mktemp -d)"
trap 'rm -rf "$PLAN_DIR"' EXIT

# The plan: one line per skill, tab separated:
#   status <TAB> skill-dir <TAB> body-file <TAB> title <TAB> automerge <TAB> commit-path
python3 - "$JSON" "$PLAN_DIR" <<'PY' > "$PLAN_DIR/plan.tsv"
import json
import os
import sys

json_path, plan_dir = sys.argv[1], sys.argv[2]
with open(json_path) as f:
    data = json.load(f)

run_id = data.get("run_id") or 0
run_outcome = data.get("run_outcome") or "unknown"


def cell(value):
    """One table cell: no line breaks, no stray pipes."""
    text = " ".join(str(value or "").split())
    return text.replace("|", "\\|")


def check_state(check):
    if not check.get("ran"):
        return "not run"
    return "pass" if check.get("pass") else "fail"


def row_line(row):
    checks = ", ".join(
        "%s %s" % (c.get("name", "?"), check_state(c)) for c in (row.get("checks") or [])
    )
    draft = row.get("row") or {}
    return "| `%s` | `%s` | `%s` | %s | %s | %s |" % (
        cell(draft.get("id")),
        cell(draft.get("old")),
        cell(draft.get("new")),
        cell(checks),
        cell(row.get("outcome")),
        cell(draft.get("quote")),
    )


for skill in data.get("skills") or []:
    status = skill.get("status") or ""
    if status not in ("written", "pr", "fallback"):
        continue
    skill_dir = skill.get("skill_dir") or ""
    name = os.path.basename(skill_dir.rstrip("/"))
    rows = skill.get("rows") or []
    header = "| row | old | new | checks | outcome | evidence quote |"
    divider = "| --- | --- | --- | --- | --- | --- |"
    body = [
        "Skill Engine updater — `%s`." % name,
        "",
        "Run %s, outcome %s. Skill status: **%s**." % (run_id, run_outcome, status),
        "",
    ]
    if skill.get("reason"):
        body += [skill["reason"], ""]
    if status == "written":
        body += [
            "Every row below passed the gate, so the change is written and the PR is",
            "auto-merged once the required checks pass.",
            "",
        ]
    else:
        body += [
            "This skill needs a person: the rows below did not pass the gate, so",
            "nothing was written. The PR is **not** auto-merged.",
            "",
        ]
    if rows:
        body += [header, divider]
        body += [row_line(r) for r in rows]
        body += [""]
    else:
        body += ["No drafted rows: the change is a version or freshness advance.", ""]
    body += [
        "---",
        "",
        "Produced by `upkeep update --runner ci`; the gate is check Q (quote),",
        "check S (config schema) and check D (doc history).",
        "",
    ]
    body_path = os.path.join(plan_dir, "%s.body.md" % name)
    with open(body_path, "w") as f:
        f.write("\n".join(body))

    commit_path = ""
    pending_path = ""
    if status != "written":
        # Nothing was written for this skill, so the branch carries a
        # document with the rows a person has to decide on.
        pending_path = os.path.join(plan_dir, "%s.pending.md" % name)
        with open(pending_path, "w") as f:
            f.write("\n".join(body))
        commit_path = os.path.join("docs", "upkeep", "pending", "%s.md" % name)
    title = (
        "upkeep: update %s" % name
        if status == "written"
        else "upkeep: review pending rows for %s" % name
    )
    print("\t".join([status, skill_dir, body_path, title, "1" if status == "written" else "0", commit_path, pending_path]))
PY

if [[ ! -s "$PLAN_DIR/plan.tsv" ]]; then
  echo "ci-publish: nothing to publish"
  exit 0
fi

published=0
while IFS=$'\t' read -r status skill_dir body_file title automerge commit_path pending_path; do
  [[ -n "$status" ]] || continue
  name="$(basename "${skill_dir%/}")"
  branch="upkeep/${name}-$(date -u +%Y%m%d-%H%M%S)"

  if [[ "$status" == "written" ]]; then
    # The change itself is the commit.
    files=("$skill_dir")
  else
    # No change was written: the branch carries the pending rows.
    mkdir -p "$REPO/$(dirname "$commit_path")"
    cp "$pending_path" "$REPO/$commit_path"
    files=("$commit_path")
  fi

  if [[ "$DRY_RUN" == "1" ]]; then
    echo "ci-publish: would publish $name ($status) on $branch"
    continue
  fi

  git -C "$REPO" checkout -b "$branch" "$BASE" >/dev/null 2>&1 || {
    echo "ci-publish: could not create the branch $branch" >&2
    exit 1
  }
  git -C "$REPO" add -A -- "${files[@]}"
  if git -C "$REPO" diff --cached --quiet; then
    echo "ci-publish: nothing changed for $name; no PR"
    git -C "$REPO" checkout "$BASE" >/dev/null 2>&1
    continue
  fi
  git -C "$REPO" commit -q -m "upkeep: $title" || {
    echo "ci-publish: could not commit $name" >&2
    exit 1
  }
  git -C "$REPO" push -q -u origin "$branch" || {
    echo "ci-publish: could not push $branch" >&2
    exit 1
  }
  ( cd "$REPO" && gh pr create --base "$BASE" --head "$branch" \
    --title "$title" --body-file "$body_file" ) || {
    echo "ci-publish: could not open the PR for $name" >&2
    exit 1
  }
  if [[ "$automerge" == "1" ]]; then
    ( cd "$REPO" && gh pr merge --auto --squash "$branch" ) || {
      echo "ci-publish: could not enable auto-merge for $branch" >&2
      exit 1
    }
  fi
  published=$((published + 1))
  git -C "$REPO" checkout "$BASE" >/dev/null 2>&1
done < "$PLAN_DIR/plan.tsv"

if [[ "$published" == "0" ]]; then
  echo "ci-publish: nothing to publish"
fi
