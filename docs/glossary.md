# Glossary

## Terms

**Frozen block**: an author-owned `<!-- frozen:id -->` region whose hash is stamped in frontmatter; any edit makes `check` report `TAMPERED`.
_Avoid_: locked block, protected block

**Generated region**: a renderer-owned `<!-- generated:id -->` region rewritten from the sidecar; hand edits are reported as `DRIFT`.
_Avoid_: dynamic block, template block

**Sidecar**: `upkeep.data.json` next to `SKILL.md`, tracked in git; the single source the renderer reads.
_Avoid_: data file, manifest

**`verified_for`**: the upstream version the skill's content was verified against; rendered into the `upkeep-pins` frontmatter key.
_Avoid_: pin version, current version

**`latest_seen`**: the newest upstream release seen by the last poll; machine-owned, never rendered as the pin.

**Redirect row**: one stale habit → current replacement entry (`old`, `new`, `kind`, `since`, `source`), accepted only through the gate.

**Gate**: the per-row verification checks Q (quote), S (schema/CLI), D (doc history), J (second model, escalate-only).

**Owner runner**: the single runner (`ci` or `local`) allowed to update a skill.

## Verdicts
`FRESH`, `STALE`, `CONTENT-LAG`, `TAMPERED`; `DRIFT` is reported per item and makes the overall verdict `STALE`.
