#!/usr/bin/env bash
# skill-scan: v0.1.0
# Read-only inventory of the agent skills on this machine:
#   scan.sh [--json] [--offline] [--only] [--owner <github-login>]... [path]...
#
#   1. Find    walks the known skill folders (and any path given), resolves symlinks
#   2. Triage  yours / installed / unknown, from the install record, plugin caches and git remotes
#   3. Scan    self-updating skills: `upkeep.sh check`; all others: package versions named in
#              SKILL.md compared with the latest npm / PyPI release
#
# Writes nothing: no cache, no temp files, no edits. Never runs a script from a scanned skill
# (the check uses the upkeep.sh next to this file). Needs bash and stdlib python3; npm and git
# only for the pins of self-updating skills. Always exits 0.
set -uo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# -I keeps the current directory and user site-packages off sys.path, so a file in a scanned
# or downloaded folder can never be imported.
exec python3 -I - "$SELF_DIR/upkeep.sh" "$@" <<'PY'
import collections, hashlib, json, os, pathlib, re, subprocess, sys, urllib.parse, urllib.request

upkeep, args = sys.argv[1], sys.argv[2:]
HOME = pathlib.Path.home().resolve()
DEFAULT_ROOTS = [".agents/skills", ".claude/skills", ".codex/skills", ".config/opencode/skills",
                 ".gemini/skills", ".gemini/config/skills", ".cursor/skills", ".openclaw/skills"]
SKIP = {".git", "node_modules"}

as_json = offline = only = False
owners, paths = set(), []
i = 0
while i < len(args):
    a = args[i]
    if a == "--json":
        as_json = True
    elif a == "--offline":
        offline = True
    elif a == "--only":
        only = True
    elif a == "--owner" and i + 1 < len(args):
        i += 1
        owners.add(args[i].lower())
    elif a in ("-h", "--help") or a.startswith("-"):
        print("usage: scan.sh [--json] [--offline] [--only] [--owner <github-login>]... [path]...\n"
              "  --only     scan just the given paths, not the known skill folders\n"
              "  --offline  skip every network lookup (steps 1 and 2 only)\n"
              "  --owner    a GitHub login whose skills count as yours (repeatable)", file=sys.stderr)
        sys.exit(0 if a in ("-h", "--help") else 2)
    else:
        paths.append(a)
    i += 1

# === 1. FIND ===
def find(p, depth, top=True):
    """Skill folders under p. A symlink is followed only when it is the root or is itself a skill."""
    if (p / "SKILL.md").is_file():
        return [p]
    if depth == 0 or not p.is_dir() or (p.is_symlink() and not top):
        return []
    try:
        kids = sorted(c for c in p.iterdir() if c.name not in SKIP)
    except OSError:
        return []
    return [s for c in kids for s in find(c, depth - 1, False)]

roots = [] if only else [(HOME / r, 1) for r in DEFAULT_ROOTS]
roots += [(pathlib.Path(p).expanduser(), 6) for p in paths]
missing, broken, entries, skills = [], [], 0, {}
for root, depth in roots:
    if not root.exists():
        missing.append(str(root))
        continue
    if root.is_dir():
        broken += [str(c) for c in sorted(root.iterdir()) if c.is_symlink() and not c.exists()]
    for entry in find(root, depth):
        entries += 1
        skills.setdefault(entry.resolve(), []).append(str(entry))

# === 2. TRIAGE ===
try:
    lock = json.loads((HOME / ".agents/.skill-lock.json").read_text(encoding="utf-8")).get("skills", {})
except (OSError, ValueError, AttributeError):
    lock = {}
PLUGINS = (HOME / ".claude/plugins").resolve()
git_cache = {}

def git_source(real):
    """owner/repo of the enclosing git checkout's origin, or ''. Never looks at or above $HOME."""
    for top in [real, *real.parents]:
        if top == HOME or top == top.parent:
            return ""
        if (top / ".git").exists():
            break
    else:
        return ""
    if top not in git_cache:
        try:
            url = subprocess.run(["git", "-C", str(top), "config", "--get", "remote.origin.url"],
                                 capture_output=True, text=True, timeout=10).stdout.strip()
        except (OSError, subprocess.SubprocessError):
            url = ""
        m = re.search(r'[:/]([^/:]+)/([^/]+?)(?:\.git)?/?$', url)
        git_cache[top] = f"{m.group(1)}/{m.group(2)}" if m else ""
    return git_cache[top]

def triage(real):
    """(origin, record, source). A plugin cache is always 'installed': an update replaces it."""
    if PLUGINS in real.parents:
        parts = real.relative_to(PLUGINS).parts
        return "installed", "plugin", "/".join(parts[1:3] if parts[0] == "cache" else parts[1:2])
    rec = lock.get(real.name)
    if isinstance(rec, dict) and (HOME / ".agents/skills" / real.name).resolve() == real:
        record, source = "lock", str(rec.get("source", ""))
    else:
        source = git_source(real)
        record = "git" if source else "none"
    if source and source.split("/")[0].lower() in owners:
        return "yours", record, source
    if record == "lock" or (record == "git" and owners):
        return "installed", record, source
    return "unknown", record, source

# === 3. SCAN ===
NPM_AT = re.compile(r'(?<![\w/@.-])((?:@[a-z0-9][\w.-]*/)?[a-z0-9][\w.-]*)@(\d+\.\d+(?:\.\d+)?)\b')
NPM_JSON = re.compile(r'"((?:@[a-z0-9][\w.-]*/)?[a-z0-9][\w.-]*)"\s*:\s*"[\^~]?(\d+\.\d+(?:\.\d+)?)"')
PIP_EQ = re.compile(r'(?<![\w.-])([A-Za-z0-9][\w.-]*)==(\d+\.\d+(?:\.\d+)?)\b')
NOT_PACKAGES = {"version"}  # JSON keys that carry a version but name no package
latest_cache = {}

def ver(v):
    return tuple(int(x) for x in re.findall(r'\d+', v)[:3])

def latest(registry, pkg):
    if (registry, pkg) not in latest_cache:
        if registry == "npm":
            url, key = f"https://registry.npmjs.org/{urllib.parse.quote(pkg, safe='@')}/latest", None
        else:
            url, key = f"https://pypi.org/pypi/{urllib.parse.quote(pkg)}/json", "info"
        try:
            with urllib.request.urlopen(url, timeout=8) as r:
                data = json.load(r)
            latest_cache[registry, pkg] = str((data[key] if key else data)["version"])
        except Exception:
            latest_cache[registry, pkg] = None
    return latest_cache[registry, pkg]

def signals(body):
    """Up to three (registry, package, highest version named), most-mentioned first."""
    seen = collections.defaultdict(list)
    for registry, rx in (("npm", NPM_AT), ("npm", NPM_JSON), ("pypi", PIP_EQ)):
        for pkg, v in rx.findall(body):
            if pkg not in NOT_PACKAGES:
                seen[registry, pkg].append(v)
    top = sorted(seen.items(), key=lambda kv: (-len(kv[1]), kv[0]))[:3]
    return [{"registry": r, "package": p, "written": max(vs, key=ver)} for (r, p), vs in top]

def scan(real, text, self_updating):
    """(state, detail, signals)."""
    if self_updating:
        if offline:
            return "NOT-CHECKED", "", []
        env = {k: v for k, v in os.environ.items() if k != "UPKEEP_SKILL_DIR"}
        env["UPKEEP_NO_DELEGATE"] = "1"  # script path only: the Go binary keeps a state database
        try:
            out = subprocess.run(["bash", upkeep, "check", str(real)], capture_output=True,
                                 text=True, timeout=180, env=env).stdout
        except (OSError, subprocess.SubprocessError):
            out = ""
        m = re.search(r'^VERDICT: (\w+)', out, re.M)
        flagged = re.findall(r'^(?:STALE|TAMPERED|MISSING|UNSTAMPED)\s+(.+?)\s*$', out, re.M)
        return (m.group(1) if m else "UNKNOWN"), "; ".join(re.sub(r'\s+', ' ', f) for f in flagged[:3]), []
    sigs = signals(text)
    if not sigs:
        return "NO-SIGNAL", "", sigs
    if offline:
        return "NOT-CHECKED", ", ".join(f"{s['package']} {s['written']}" for s in sigs), sigs
    for s in sigs:
        s["latest"] = latest(s["registry"], s["package"])
    gaps = [s for s in sigs if s["latest"] and ver(s["latest"]) > ver(s["written"])]
    if gaps:
        return "GAP", ", ".join(f"{s['package']} {s['written']} → {s['latest']}" for s in gaps), sigs
    if any(s["latest"] for s in sigs):
        return "CURRENT", ", ".join(f"{s['package']} {s['written']}" for s in sigs if s["latest"]), sigs
    return "UNRESOLVED", ", ".join(f"{s['package']} {s['written']}" for s in sigs), sigs

rows = []
for real, links in sorted(skills.items()):
    text = (real / "SKILL.md").read_text(encoding="utf-8", errors="replace")
    fm = re.match(r'---\r?\n(.*?)\r?\n---', text, re.S)
    fm = fm.group(1) if fm else ""
    nm = re.search(r'^name:\s*["\']?([^"\'\n#]+)', fm, re.M)
    # Any upkeep-* key counts: a freeze-only skill has a stamp and no pins.
    self_updating = bool(re.search(r'^\s{2,}upkeep-[a-z0-9-]+\s*:|^\s{2}pins:\s*$', fm, re.M))
    origin, record, source = triage(real)
    state, detail, sigs = scan(real, text, self_updating)
    # The folder name is the skill's identity (the spec requires `name` to match it).
    rows.append({"name": real.name, "frontmatter_name": nm.group(1).strip() if nm else "",
                 "path": str(real), "links": links,
                 "origin": origin, "record": record, "source": source, "self_updating": self_updating,
                 "state": state, "detail": detail, "signals": sigs,
                 "sha256": hashlib.sha256(text.encode("utf-8")).hexdigest()[:16]})

by_name = collections.defaultdict(list)
for r in rows:
    by_name[r["name"]].append(r)
for r in rows:
    r["copies"] = len(by_name[r["name"]])
    r["copies_identical"] = len({c["sha256"] for c in by_name[r["name"]]}) == 1

summary = {"roots": len(roots) - len(missing), "missing_roots": missing, "broken_links": broken,
           "entries": entries,
           "skill_folders": len(rows), "names": len(by_name),
           "duplicated_names": sum(1 for v in by_name.values() if len(v) > 1),
           "self_updating": sum(1 for r in rows if r["self_updating"]), "owners": sorted(owners)}

# === OUTPUT ===
if as_json:
    json.dump({"summary": summary, "skills": rows}, sys.stdout, indent=2, ensure_ascii=False)
    print()
    sys.exit(0)

print(f"roots: {summary['roots']}   entries: {entries}   skill folders: {len(rows)}   names: {len(by_name)}"
      f"   duplicated names: {summary['duplicated_names']}   self-updating: {summary['self_updating']}")
if missing:
    print(f"not found: {', '.join(missing)}")
if broken:
    print(f"broken links ({len(broken)}): {', '.join(broken)}")
if not owners:
    print("no --owner given: nothing is marked 'yours'")
if rows:
    order = {"yours": 0, "unknown": 1, "installed": 2}
    rows.sort(key=lambda r: (order[r["origin"]], r["name"], r["path"]))
    w = min(34, max(len(r["name"]) for r in rows))
    print(f"\n{'NAME':<{w}}  {'ORIGIN':<9}  {'STATE':<11}  COPIES  SOURCE / DETAIL")
    printed = set()
    for r in rows:
        copies = "1" if r["copies"] == 1 else f"{r['copies']}{'=' if r['copies_identical'] else '≠'}"
        differs = r["path"].replace(str(HOME), "~", 1) if r["copies"] > 1 and not r["copies_identical"] else ""
        tail = " — ".join(x for x in (r["source"], r["detail"], differs) if x)
        line = f"{r['name'][:w]:<{w}}  {r['origin']:<9}  {r['state']:<11}  {copies:<6}  {tail}"
        if (line, r["sha256"]) not in printed:  # identical copies print once
            printed.add((line, r["sha256"]))
            print(line)
    print("\nCOPIES: '=' every copy has the same SKILL.md, '≠' they differ. Paths: --json.")
PY
