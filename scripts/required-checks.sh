#!/usr/bin/env bash
# The required-checks gate for a pull request (Skill Engine Phase 5).
#
#   * "upkeep check --strict" runs on every skill in the repo.
#   * "upkeep render --check" runs on every skill that has a sidecar, so
#     a hand edit inside a generated region fails the job.
#   * A skill the pull request touches must be fresh.
#   * A TAMPERED verdict (a frozen owner-policy block changed) fails the
#     job for any skill, touched or not.
#
# By default the job is not blocked by a skill the pull request does not
# touch. Three of this repo's skills have no sidecar, so no runner owns
# them and their pins drift on their own; gating the job on them would
# leave it permanently red and would block the updater's own pull
# requests. Set UPKEEP_REQUIRE_ALL_SKILLS=1 to gate the job on every
# skill instead — do that once the skills are retrofitted with sidecars
# and an owner.
#
# Usage: required-checks.sh <upkeep-binary> [base-ref]
set -uo pipefail

BIN="${1:-}"
BASE="${2:-main}"
if [[ -z "$BIN" ]]; then
  echo "required-checks: the upkeep binary is required" >&2
  exit 2
fi
if [[ ! -x "$BIN" ]]; then
  echo "required-checks: $BIN is not executable" >&2
  exit 2
fi

require_all="${UPKEEP_REQUIRE_ALL_SKILLS:-0}"

# The skills this pull request touches.
changed=""
if git rev-parse --verify "$BASE" >/dev/null 2>&1; then
  changed="$(git diff --name-only "$BASE"...HEAD 2>/dev/null || true)"
fi

failed=0

for dir in skills/*/; do
  [[ -d "$dir" ]] || continue
  name="$(basename "${dir%/}")"
  echo "== upkeep check --strict $dir"
  out="$("$BIN" check --strict "$dir" 2>&1)"
  rc=$?
  echo "$out"
  verdict="$(printf '%s\n' "$out" | grep -E '^VERDICT:' | tail -1)"
  case "$verdict" in
    *TAMPERED*)
      echo "required-checks: $name has a tampered frozen block" >&2
      failed=1
      continue
      ;;
  esac
  if [[ "$require_all" == "1" ]]; then
    if [[ $rc -ne 0 ]]; then
      echo "required-checks: $name is not fresh" >&2
      failed=1
    fi
    continue
  fi
  if printf '%s\n' "$changed" | grep -q "^skills/$name/"; then
    if [[ $rc -ne 0 ]]; then
      echo "required-checks: $name is touched by this pull request and is not fresh" >&2
      failed=1
    fi
  fi
done

for dir in skills/*/; do
  [[ -d "$dir" ]] || continue
  [[ -f "$dir/upkeep.data.json" ]] || continue
  echo "== upkeep render --check $dir"
  out="$("$BIN" render --check "$dir" 2>&1)"
  rc=$?
  echo "$out"
  if [[ $rc -ne 0 ]]; then
    echo "required-checks: ${dir%/} has render drift (run: upkeep render)" >&2
    failed=1
  fi
done

if [[ $failed -ne 0 ]]; then
  echo "required-checks: FAILED" >&2
  exit 1
fi
echo "required-checks: OK"
