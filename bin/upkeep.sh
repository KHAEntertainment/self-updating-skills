#!/usr/bin/env bash
# skill-upkeep-lib: v0.2.0
# Unified self-updating skill runtime:
#   upkeep.sh check [--strict] [--threshold N] [skill-dir]
#   upkeep.sh fetch [--skill-dir <dir>] <page>... | --index | --list
#   upkeep.sh stamp [skill-dir]
#   upkeep.sh sync <skill-dir>...
#
# Supports BOTH:
#   1. agentskills.io-compliant flat `metadata` strings (`upkeep-pins`, `upkeep-source-*`, `upkeep-frozen-*`)
#   2. Legacy nested YAML (`metadata.pins`, `metadata.sources`, `metadata.frozen`)
# Always exits 0 in `check` mode unless `--strict` is passed, so Claude Code `!` dynamic injection never aborts skill loading.
set -uo pipefail

SELF_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
DEFAULT_SKILL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Optional delegation: if a newer canonical ~/.agents/upkeep/bin/upkeep.sh exists, use it
if [[ -z "${UPKEEP_NO_DELEGATE:-}" && -x "$HOME/.agents/upkeep/bin/upkeep.sh" && "$SELF_PATH" != "$HOME/.agents/upkeep/bin/upkeep.sh" ]]; then
  self_ver="$(grep -m1 '^# skill-upkeep-lib:' "$SELF_PATH" 2>/dev/null | awk '{print $3}')"
  global_ver="$(grep -m1 '^# skill-upkeep-lib:' "$HOME/.agents/upkeep/bin/upkeep.sh" 2>/dev/null | awk '{print $3}')"
  if [[ -n "$global_ver" && "$global_ver" != "$self_ver" ]]; then
    UPKEEP_NO_DELEGATE=1 exec "$HOME/.agents/upkeep/bin/upkeep.sh" "$@"
  fi
fi

SUBCMD="${1:-check}"
if [[ "$SUBCMD" == "check" || "$SUBCMD" == "fetch" || "$SUBCMD" == "stamp" || "$SUBCMD" == "sync" ]]; then
  shift
else
  # Default to `check` if a path or flag was passed directly
  SUBCMD="check"
fi

if [[ "$SUBCMD" == "sync" ]]; then
  if [[ $# -eq 0 ]]; then
    echo "usage: $0 sync <skill-dir>..." >&2
    exit 2
  fi
  ver="$(grep -m1 '^# skill-upkeep-lib:' "$SELF_PATH" | awk '{print $3}')"
  for target in "$@"; do
    target="${target%/}"
    [[ -f "$target/SKILL.md" ]] || { echo "SKIP     $target (no SKILL.md)" >&2; continue; }
    mkdir -p "$target/scripts"
    cp "$SELF_PATH" "$target/scripts/upkeep.sh"
    chmod +x "$target/scripts/upkeep.sh"
    echo "SYNCED   $target/scripts/upkeep.sh ($ver)"
  done
  exit 0
fi

exec python3 - "$SUBCMD" "$DEFAULT_SKILL_DIR" "$@" <<'PY'
import datetime, hashlib, os, pathlib, re, subprocess, sys, time

subcmd = sys.argv[1]
default_skill_dir = pathlib.Path(sys.argv[2])
args = sys.argv[3:]

def strip_fenced_blocks(text: str) -> str:
    """Remove fenced code blocks (``` or ~~~) so example snippets in docs are never scanned as live directives."""
    out = []
    in_fence = False
    fence_marker = ""
    for line in text.splitlines():
        s = line.lstrip()
        m = re.match(r'^(`{3,}|~{3,})', s)
        if m:
            marker = m.group(1)
            if not in_fence:
                in_fence = True
                fence_marker = marker[0] * len(marker)
                out.append("")
                continue
            elif s.startswith(fence_marker):
                in_fence = False
                fence_marker = ""
                out.append("")
                continue
        out.append("" if in_fence else line)
    return "\n".join(out)

def parse_skill_frontmatter(sk_path: pathlib.Path):
    txt = sk_path.read_text(encoding="utf-8")
    m = re.match(r'^---\n(.*?)\n---', txt, re.S)
    if not m:
        return {}, "", txt, None
    fm = m.group(1)
    name_m = re.search(r'^name:\s*(.+)$', fm, re.M)
    skill_name = name_m.group(1).strip().strip('"\'') if name_m else sk_path.parent.name

    meta_m = re.search(r'^metadata:\s*$(.*?)(?=^[^\s]|\Z)', fm, re.M | re.S)
    meta_str = meta_m.group(1) if meta_m else ""

    # 1. Flat key-value pairs under metadata: (2-space indent)
    flat = {}
    for line in meta_str.splitlines():
        lm = re.match(r'^\s{2}([A-Za-z0-9_.-]+):\s*(.+)$', line)
        if lm:
            k, v = lm.group(1).strip(), lm.group(2).strip()
            if not (v.startswith('"') or v.startswith("'")):
                v = re.sub(r'\s+#.*$', '', v)
            flat[k] = v.strip('"\'')

    # 2. Extract pins (from flat `upkeep-pins` AND/OR nested `pins:`)
    pins = []  # list of (name, version, kind, repo)
    if "upkeep-pins" in flat and flat["upkeep-pins"]:
        for item in flat["upkeep-pins"].split(","):
            item = item.strip()
            if not item or "=" not in item:
                continue
            k, _, rhs = item.partition("=")
            k, rhs = k.strip(), rhs.strip()
            if rhs.startswith("github:"):
                spec = rhs[len("github:"):]
                repo, _, ver = spec.partition("@")
                pins.append((k, ver.strip(), "github", repo.strip()))
            elif rhs.startswith("manual:"):
                pins.append((k, rhs[len("manual:"):].strip(), "manual", ""))
            elif rhs.startswith("npm:"):
                pins.append((k, rhs[len("npm:"):].strip(), "npm", ""))
            else:
                pins.append((k, rhs, "npm", ""))

    pm = re.search(r'^\s{2}pins:\s*$(.*?)(?=^\s{0,2}[A-Za-z_]|\Z)', meta_str, re.M | re.S)
    if pm:
        for line in pm.group(1).splitlines():
            s = line.strip()
            if not s or s.startswith("#") or ":" not in s:
                continue
            key, _, val = s.partition(":")
            key, val = key.strip().strip('"\' '), val.strip().strip('"\' ')
            if any(p[0] == key for p in pins):
                continue
            if val.startswith("{"):
                kind_m = re.search(r'kind:\s*(\w+)', val)
                kind = kind_m.group(1) if kind_m else "npm"
                repo_m = re.search(r'repo:\s*([A-Za-z0-9_./-]+)', val)
                repo = repo_m.group(1) if repo_m else ""
                ver_m = re.search(r'version:\s*([^\s,}]+)', val)
                ver = ver_m.group(1).strip('"\' ') if ver_m else ""
                pins.append((key, ver, kind, repo))
            else:
                pins.append((key, val, "npm", ""))

    # 3. Extract sources (from flat `upkeep-source-*` AND/OR nested `sources:`)
    src_m = re.search(r'^\s{2}sources:\s*$(.*?)(?=^\s{0,2}[A-Za-z_]|\Z)', meta_str, re.M | re.S)
    src_str = src_m.group(1) if src_m else ""

    def get_src_scalar(flat_key, nested_key, default=""):
        if flat_key in flat and flat[flat_key]:
            return flat[flat_key]
        km = re.search(r'^\s{4}' + re.escape(nested_key) + r':\s*(.+)$', src_str, re.M)
        if not km:
            return default
        val = km.group(1).strip()
        if val.startswith("[") or val.startswith("{"):
            return default
        if not (val.startswith('"') or val.startswith("'")):
            val = re.sub(r'\s+#.*$', '', val)
        return val.strip('"\'')

    base_url = get_src_scalar("upkeep-source-base", "base_url", "").rstrip("/")
    ext = get_src_scalar("upkeep-source-ext", "ext", ".md")
    if ext and not ext.startswith("."):
        ext = "." + ext
    cache_name = get_src_scalar("upkeep-source-cache", "cache_name", f"{skill_name}-docs")
    ttl_minutes = int(get_src_scalar("upkeep-source-ttl", "ttl_minutes", "720") or "720")
    index_url = get_src_scalar("upkeep-source-index", "index_url", "")

    pages = []
    if "upkeep-source-pages" in flat and flat["upkeep-source-pages"]:
        pages = [p.strip().strip('"\'') for p in flat["upkeep-source-pages"].split(",") if p.strip()]
    else:
        pm_inline = re.search(r'^\s{4}pages:\s*\[(.*?)\]', src_str, re.M | re.S)
        if pm_inline:
            pages = [p.strip().strip('"\'') for p in pm_inline.group(1).split(",") if p.strip()]
        else:
            pm_block = re.search(r'^\s{4}pages:\s*$(.*?)(?=^\s{0,4}[A-Za-z_]|\Z)', src_str, re.M | re.S)
            if pm_block:
                for line in pm_block.group(1).splitlines():
                    s = line.strip()
                    if s.startswith("- "):
                        pages.append(s[2:].strip().strip('"\''))

    # 4. Extract frozen hashes (from flat `upkeep-frozen-<id>` AND/OR nested `frozen:`)
    frozen = {}
    for k, v in flat.items():
        if k.startswith("upkeep-frozen-"):
            fid = k[len("upkeep-frozen-"):]
            frozen[fid] = v
    fr_m = re.search(r'^\s{2}frozen:\s*$(.*?)(?=^\s{0,2}[A-Za-z_]|\Z)', meta_str, re.M | re.S)
    if fr_m:
        for line in fr_m.group(1).splitlines():
            s = line.strip()
            if not s or s.startswith("#") or ":" not in s:
                continue
            fk, _, fv = s.partition(":")
            fk = fk.strip().strip('"\'')
            if fk not in frozen:
                frozen[fk] = fv.strip().strip('"\'')

    threshold_days = int(flat.get("upkeep-threshold-days", flat.get("freshness_threshold_days", "60")) or "60")

    cfg = {
        "skill_name": skill_name,
        "pins": pins,
        "threshold_days": threshold_days,
        "verified": flat.get("verified", flat.get("upkeep-verified", "")),
        "sources": {
            "base_url": base_url,
            "ext": ext,
            "cache_name": cache_name,
            "ttl_minutes": ttl_minutes,
            "index_url": index_url,
            "pages": pages,
        },
        "frozen": frozen,
    }
    return cfg, fm, txt, m

def scan_frozen_blocks(skill_dir: pathlib.Path):
    sk = skill_dir / "SKILL.md"
    files = [sk]
    if (skill_dir / "references").is_dir():
        files += sorted((skill_dir / "references").glob("*.md"))
    pat = re.compile(r'^[ \t]*<!--\s*frozen:([A-Za-z0-9_.-]+)\s*-->[ \t]*\n(.*?)\n[ \t]*<!--\s*/frozen:\1\s*-->[ \t]*$', re.M | re.S)
    blocks = {}
    for f in files:
        if not f.exists():
            continue
        rel = "SKILL.md" if f == sk else f"references/{f.name}"
        raw = f.read_text(encoding="utf-8")
        if f == sk:
            raw = re.sub(r'^---\n.*?\n---\n?', '', raw, count=1, flags=re.S)
        cleaned = strip_fenced_blocks(raw)
        for m in pat.finditer(cleaned):
            fid = m.group(1)
            body = "\n".join(line.rstrip() for line in m.group(2).strip().splitlines())
            digest = "sha256:" + hashlib.sha256(body.encode("utf-8")).hexdigest()[:16]
            blocks[fid] = (rel, body, digest)
    return blocks

def ver_parts(v: str):
    v = re.sub(r'^v', '', v.strip().lower())
    out = []
    for p in re.split(r'[._-]+', v):
        if p.isdigit():
            out.append((0, int(p)))
        else:
            out.append((1, p))
    return out

def ver_cmp(a: str, b: str) -> int:
    pa, pb = ver_parts(a), ver_parts(b)
    if pa == pb:
        return 0
    return -1 if pa < pb else 1

# === SUBCOMMAND: CHECK ===
if subcmd == "check":
    strict = False
    threshold_override = None
    skill_dir = default_skill_dir
    i = 0
    while i < len(args):
        if args[i] == "--strict":
            strict = True
            i += 1
        elif args[i] == "--threshold" and i + 1 < len(args):
            threshold_override = int(args[i + 1])
            i += 2
        else:
            skill_dir = pathlib.Path(args[i]).resolve()
            i += 1

    sk = skill_dir / "SKILL.md"
    if not sk.exists():
        print(f"UNKNOWN — no SKILL.md under {skill_dir}")
        sys.exit(1 if strict else 0)

    cfg, _, _, _ = parse_skill_frontmatter(sk)
    threshold = threshold_override if threshold_override is not None else cfg["threshold_days"]
    today = datetime.date.today()

    stale_pins = 0
    total_pins = 0
    manual_pins = 0
    for name, ver, kind, repo in cfg["pins"]:
        total_pins += 1
        if kind == "manual":
            print(f"MANUAL   pin:{name}  {ver} (check vendor releases)")
            manual_pins += 1
            continue
        cur = ""
        is_sha_pin = bool(kind == "github" and re.match(r'^[0-9a-f]{7,40}$', ver or ""))
        try:
            if kind == "github":
                r = repo or name
                if is_sha_pin:
                    out = subprocess.check_output(
                        ["git", "ls-remote", f"https://github.com/{r}.git", "HEAD"],
                        stderr=subprocess.DEVNULL, text=True, timeout=15
                    ).strip()
                    if out:
                        cur = out.split()[0][:len(ver)]
                else:
                    out = subprocess.check_output(
                        ["git", "ls-remote", "--tags", "--refs", f"https://github.com/{r}.git"],
                        stderr=subprocess.DEVNULL, text=True, timeout=15
                    )
                    tags = [line.split("refs/tags/")[-1].strip() for line in out.splitlines() if "refs/tags/" in line]
                    semver_tags = [t for t in tags if re.match(r'^v?\d+\.\d+', t)]
                    use_tags = semver_tags or tags
                    if use_tags:
                        use_tags.sort(key=ver_parts)
                        cur = re.sub(r'^v', '', use_tags[-1])
            else:
                cur = subprocess.check_output(
                    ["npm", "view", name, "version"],
                    stderr=subprocess.DEVNULL, text=True, timeout=15
                ).strip()
        except Exception:
            cur = ""
        if not cur:
            print(f"UNKNOWN  pin:{name}  (pinned {ver or '?'} — current unresolved)")
            continue
        if is_sha_pin:
            if (ver or "").lower() == cur.lower():
                print(f"FRESH    pin:{name}  {cur}")
            else:
                print(f"STALE    pin:{name}  pinned {ver} → current {cur}")
                stale_pins += 1
        else:
            c = ver_cmp(ver or "0", cur)
            if c == 0:
                print(f"FRESH    pin:{name}  {cur}")
            elif c < 0:
                print(f"STALE    pin:{name}  pinned {ver} → current {cur}")
                stale_pins += 1
            else:
                print(f"AHEAD    pin:{name}  pinned {ver} → current {cur}")

    # Freshness dates scan (SKILL.md + references/*.md + metadata.verified fallback)
    files = [sk]
    if (skill_dir / "references").is_dir():
        files += sorted((skill_dir / "references").glob("*.md"))
    date_items = []
    seen_dates = set()
    for f in files:
        if not f.exists():
            continue
        raw = f.read_text(encoding="utf-8")
        if f == sk:
            raw = re.sub(r'^---\n.*?\n---\n?', '', raw, count=1, flags=re.S)
        cleaned = strip_fenced_blocks(raw)
        lines = cleaned.splitlines()
        label = "SKILL.md" if f == sk else f"references/{f.name}"
        for idx, line in enumerate(lines):
            if "erified" not in line:
                continue
            window = " ".join(lines[idx:idx + 2])
            for dm in re.finditer(r'\d{4}-\d{2}-\d{2}', window):
                key = (label, dm.group(0))
                if key not in seen_dates:
                    seen_dates.add(key)
                    date_items.append(key)
    if not date_items and cfg["verified"]:
        date_items.append(("SKILL.md(metadata.verified)", cfg["verified"]))

    stale_dates = 0
    for label, d in date_items:
        try:
            dt = datetime.date.fromisoformat(d)
            age = (today - dt).days
            if age > threshold:
                print(f"STALE    date:{label}  {d} ({age}d > {threshold}d — RE-VERIFY)")
                stale_dates += 1
            else:
                print(f"FRESH    date:{label}  {d} ({age}d)")
        except ValueError:
            print(f"??       date:{label}  {d} (unparseable)")

    # Frozen blocks scan
    blocks = scan_frozen_blocks(skill_dir)
    recorded = cfg["frozen"]
    tampered_frozen = 0
    for fid, (rel, _, digest) in blocks.items():
        rec = recorded.get(fid)
        if not rec:
            print(f"UNSTAMPED frozen:{fid} ({rel})  current {digest} (run: upkeep.sh stamp)")
            tampered_frozen += 1
        elif rec != digest:
            print(f"TAMPERED  frozen:{fid} ({rel})  expected {rec} != current {digest}")
            tampered_frozen += 1
        else:
            print(f"INTACT   frozen:{fid} ({rel})  {digest}")
    for fid, rec in recorded.items():
        if fid not in blocks:
            print(f"MISSING  frozen:{fid}  expected {rec} — frozen block deleted!")
            tampered_frozen += 1

    parts = []
    auto_pins = total_pins - manual_pins
    if total_pins:
        parts.append(f"pins={auto_pins - stale_pins}/{auto_pins} fresh" + (f" (+{manual_pins} manual)" if manual_pins else ""))
    if date_items:
        parts.append(f"dates={len(date_items) - stale_dates}/{len(date_items)} fresh (<={threshold}d)")
    if blocks or recorded:
        tot_f = max(len(blocks), len(recorded))
        parts.append(f"frozen={tot_f - tampered_frozen}/{tot_f} intact")

    summary = ", ".join(parts) if parts else "no pins, dates, or frozen blocks configured"
    if tampered_frozen > 0:
        print(f"VERDICT: TAMPERED — {summary}. Revert owner-policy edits immediately!")
        sys.exit(1 if strict else 0)
    elif stale_pins > 0 or stale_dates > 0:
        print(f"VERDICT: STALE — {summary}. Consult live docs before relying on cached references.")
        sys.exit(1 if strict else 0)
    elif not parts:
        print(f"VERDICT: UNKNOWN — {summary}")
        sys.exit(0)
    else:
        print(f"VERDICT: FRESH — {summary}")
        sys.exit(0)

# === SUBCOMMAND: STAMP ===
elif subcmd == "stamp":
    skill_dir = pathlib.Path(args[0]).resolve() if args else default_skill_dir
    sk = skill_dir / "SKILL.md"
    if not sk.exists():
        print(f"error: no SKILL.md at {sk}", file=sys.stderr)
        sys.exit(2)
    cfg, fm, txt, fm_m = parse_skill_frontmatter(sk)
    blocks = scan_frozen_blocks(skill_dir)
    if not blocks:
        print(f"VERDICT: NONE — no <!-- frozen:id --> blocks found in {skill_dir}")
        sys.exit(0)

    # Remove any old upkeep-frozen-* lines or nested frozen: block in metadata, then write flat upkeep-frozen-<id> keys
    meta_m = re.search(r'^metadata:\s*$(.*?)(?=^[^\s]|\Z)', fm, re.M | re.S)
    stamp_lines = "\n".join(f'  upkeep-frozen-{fid}: "{digest}"' for fid, (_, _, digest) in blocks.items())
    if meta_m:
        mb = meta_m.group(1)
        mb = re.sub(r'^\s{2}upkeep-frozen-[A-Za-z0-9_.-]+:\s*.*$\n?', '', mb, flags=re.M)
        mb = re.sub(r'^\s{2}frozen:\s*$(.*?)(?=^\s{0,2}[A-Za-z_]|\Z)', '', mb, flags=re.M | re.S)
        new_meta = mb.rstrip("\n") + "\n" + stamp_lines + "\n"
        new_fm = fm[:meta_m.start(1)] + new_meta + fm[meta_m.end(1):]
    else:
        new_fm = fm.rstrip("\n") + "\nmetadata:\n" + stamp_lines
    new_txt = "---\n" + new_fm.strip("\n") + "\n---" + txt[fm_m.end():]
    sk.write_text(new_txt, encoding="utf-8")
    for fid, (rel, _, digest) in blocks.items():
        print(f"STAMPED  frozen:{fid} ({rel})  {digest}")
    print(f"VERDICT: INTACT — stamped {len(blocks)} frozen block(s) using agentskills.io-compliant flat keys")
    sys.exit(0)

# === SUBCOMMAND: FETCH ===
elif subcmd == "fetch":
    skill_dir = default_skill_dir
    fargs = []
    i = 0
    while i < len(args):
        if args[i] == "--skill-dir" and i + 1 < len(args):
            skill_dir = pathlib.Path(args[i + 1]).resolve()
            i += 2
        else:
            fargs.append(args[i])
            i += 1
    sk = skill_dir / "SKILL.md"
    if not sk.exists():
        print(f"error: no SKILL.md at {sk}", file=sys.stderr)
        sys.exit(2)
    cfg, _, _, _ = parse_skill_frontmatter(sk)
    src = cfg["sources"]
    base = os.environ.get("DOCS_BASE_OVERRIDE", src["base_url"]).rstrip("/")
    ext = src["ext"]
    cache_dir = pathlib.Path(os.environ.get("DOCS_CACHE_OVERRIDE", f"{os.environ.get('TMPDIR', '/tmp').rstrip('/')}/{src['cache_name']}"))
    ttl = int(os.environ.get("DOCS_TTL_OVERRIDE", str(src["ttl_minutes"])))
    index_url = src["index_url"]
    pages = src["pages"]

    if not base:
        print(f"error: upkeep-source-base is not configured in {sk}", file=sys.stderr)
        sys.exit(2)
    cache_dir.mkdir(parents=True, exist_ok=True)

    def warn(msg: str):
        print(f"⚠ {msg}", file=sys.stderr)

    def curl_fetch(url: str, dest: pathlib.Path) -> bool:
        tmp = dest.with_suffix(dest.suffix + ".tmp")
        r = subprocess.run(["curl", "-fsSL", "--max-time", "30", url, "-o", str(tmp)], stderr=subprocess.DEVNULL)
        if r.returncode == 0 and tmp.exists() and tmp.stat().st_size > 0:
            tmp.replace(dest)
            return True
        tmp.unlink(missing_ok=True)
        return False

    if fargs and fargs[0] == "--list":
        if pages:
            for p in pages:
                print(p)
            sys.exit(0)
        fargs = ["--index"]

    if fargs and fargs[0] == "--index":
        if not index_url:
            if pages:
                for p in pages:
                    print(p)
                sys.exit(0)
            print(f"error: upkeep-source-index is not configured in {sk}", file=sys.stderr)
            sys.exit(2)
        idx = cache_dir / "index.txt"
        if not curl_fetch(index_url, idx):
            warn(f"OFFLINE/FETCH-FAILED: docs index ({index_url}) unreachable.")
            if idx.exists() and idx.stat().st_size > 0:
                mtime = datetime.date.fromtimestamp(idx.stat().st_mtime).isoformat()
                warn(f"  using cached index from {mtime} — list may be stale.")
            else:
                warn("  no cached index available; page names cannot be listed.")
                sys.exit(0)
        txt = idx.read_text(encoding="utf-8")
        pat = re.escape(base) + r'/([^ )\n"\']+)' + re.escape(ext)
        for m in sorted(set(re.findall(pat, txt))):
            print(m)
        sys.exit(0)

    if not fargs:
        print(f"usage: upkeep.sh fetch <page>... | --index | --list", file=sys.stderr)
        if pages:
            print(f"pages: {', '.join(pages)}", file=sys.stderr)
        sys.exit(2)

    now = time.time()
    for page in fargs:
        if page.endswith(ext):
            page = page[:-len(ext)]
        out = cache_dir / f"{page.replace('/', '__')}{ext}"
        if out.exists() and out.stat().st_size > 0 and (now - out.stat().st_mtime) < (ttl * 60):
            print(out)
            continue
        if curl_fetch(f"{base}/{page}{ext}", out):
            print(out)
            continue
        if out.exists() and out.stat().st_size > 0:
            mtime = datetime.date.fromtimestamp(out.stat().st_mtime).isoformat()
            warn(f"OFFLINE/FETCH-FAILED: {page} — falling back to cached copy from {mtime}.")
            warn("  content may be stale; re-verify against the live page when online.")
            print(out)
        else:
            warn(f"OFFLINE/FETCH-FAILED: {page} — no cached copy and the live page is unreachable.")
            warn("  this page is UNAVAILABLE; do not guess its contents.")
PY
