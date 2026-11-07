#!/usr/bin/env bash
# Daily local runner for the Skill Engine (architecture D9, decision P3).
#
# The launchd agent runs this script once a day. It never holds a secret:
# the Anthropic API key is read from the user's keychain through Enigma
# when the job runs, and reaches the engine as an environment variable for
# that process only. Nothing is written to the plist, to this file or to a
# log line.
#
#   1. ci-owned skills live in this repo, so `git pull --ff-only` brings in
#      what their owner runner (CI) already merged — the local runner only
#      pulls skills it does not own (decision P3).
#   2. local-owned skills live under ~/.agents/skills and are updated in
#      place by `upkeep update --runner local`; the engine skips every
#      skill whose owner is not "local".
#
# Usage: upkeep-local-run.sh [repo] [upkeep-binary]
set -uo pipefail

log() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

REPO="${1:-}"
if [[ -z "$REPO" ]]; then
  REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fi
BIN="${2:-${UPKEEP_BIN:-}}"

if [[ -z "$BIN" || ! -x "$BIN" ]]; then
  for cand in "$(command -v upkeep 2>/dev/null || true)" "$HOME/.agents/upkeep/bin/upkeep"; do
    if [[ -n "$cand" && -x "$cand" ]]; then
      BIN="$cand"
      break
    fi
  done
fi
if [[ -z "$BIN" || ! -x "$BIN" ]]; then
  log "upkeep binary not found: pass it as the second argument or set UPKEEP_BIN"
  exit 1
fi

ENIGMA="${UPKEEP_ENIGMA_BIN:-}"
if [[ -z "$ENIGMA" ]]; then
  for cand in "$(command -v enigma 2>/dev/null || true)" "$HOME/.traycer/cli/bin/enigma"; do
    if [[ -n "$cand" && -x "$cand" ]]; then
      ENIGMA="$cand"
      break
    fi
  done
fi
if [[ -z "$ENIGMA" ]]; then
  log "enigma not found: the API key is read from the keychain through it (set UPKEEP_ENIGMA_BIN)"
  exit 1
fi

rc=0

# 1. The ci-owned skills: pull what their owner runner merged.
if GIT_TERMINAL_PROMPT=0 git -C "$REPO" pull --ff-only; then
  log "pulled $REPO"
else
  log "git pull --ff-only failed in $REPO (the local-owned skills do not depend on it)"
  rc=1
fi

# 2. The local-owned skills: update them in place.
skill_dirs=()
for d in "$HOME"/.agents/skills/*/; do
  [[ -f "$d/SKILL.md" ]] || continue
  skill_dirs+=("${d%/}")
done
if [[ ${#skill_dirs[@]} -eq 0 ]]; then
  log "no skills under $HOME/.agents/skills"
  exit "$rc"
fi

# The key comes from the keychain now, through Enigma: the global scope
# first, then the project scope (the repo is the working directory).
if ! "$ENIGMA" run --only ANTHROPIC_API_KEY --scope global -- "$BIN" update --runner local "${skill_dirs[@]}"; then
  if ! "$ENIGMA" run --only ANTHROPIC_API_KEY --scope project -- "$BIN" update --runner local "${skill_dirs[@]}"; then
    log "could not read ANTHROPIC_API_KEY from the keychain: register it with"
    log "  enigma add ANTHROPIC_API_KEY --scope global --depository keychain --usage unattended"
    exit 1
  fi
fi

exit "$rc"
