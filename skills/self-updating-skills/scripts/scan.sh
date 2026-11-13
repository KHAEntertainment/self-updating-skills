#!/usr/bin/env bash
# skill-scan: v0.1.0
# Read-only inventory of the agent skills on this machine:
#   scan.sh [--json] [--offline] [--only] [--owner <github-login>]... [path]...
#
#   1. Find    walks the known skill folders (and any path given), resolves symlinks
#   2. Triage  yours / installed / unknown, from the install record, plugin caches and git remotes
#   3. Scan    self-updating skills: the `upkeep.sh check` verdict; all others: package versions
#              named in SKILL.md compared with the latest npm / PyPI release
#
# Writes nothing: no cache, no temp files, no edits, and the npm CLI (which keeps a cache and
# logs) is never run. Scanned folders are treated as hostile input: nothing in them is executed
# or imported, and git is run only as `git ls-remote` for GitHub pins, from /, https only, with
# no repository or user configuration. Every file is read through one reader that refuses
# special files and files over 4 MB. Needs bash and stdlib python3. Always exits 0.
# SKILL_SCAN_NPM_REGISTRY and SKILL_SCAN_PYPI_INDEX point the lookups at a mirror;
# SKILL_SCAN_CHECK_SECONDS (default 120) is the time allowed for one skill's check.
set -uo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# -I keeps the current directory and user site-packages off sys.path, so a file in a scanned
# or downloaded folder can never be imported.
exec python3 -I - "$SELF_DIR/upkeep.sh" "$@" <<'PY'
import collections, contextlib, hashlib, io, json, os, pathlib, re, signal, stat, subprocess, sys
import urllib.parse, urllib.request

upkeep, args = sys.argv[1], sys.argv[2:]
HOME = pathlib.Path.home().resolve()
DEFAULT_ROOTS = [".agents/skills", ".claude/skills", ".codex/skills", ".config/opencode/skills",
                 ".gemini/skills", ".gemini/config/skills", ".cursor/skills", ".openclaw/skills"]
SKIP = {".git", "node_modules"}
NPM_REGISTRY = os.environ.get("SKILL_SCAN_NPM_REGISTRY", "https://registry.npmjs.org").rstrip("/")
PYPI_INDEX = os.environ.get("SKILL_SCAN_PYPI_INDEX", "https://pypi.org/pypi").rstrip("/")
try:
    CHECK_SECONDS = max(1, int(os.environ.get("SKILL_SCAN_CHECK_SECONDS", "120")))
except ValueError:
    CHECK_SECONDS = 120
LIMIT = 4 << 20  # bytes; no SKILL.md, reference, lock file or git config is read past this

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

problems = []  # what could not be read; the rest of the report still stands

def read_file(path, limit=LIMIT):
    """Bytes of a regular file of at most `limit` bytes. Anything else raises OSError: the open
    cannot block on a FIFO or a device, and a longer file is refused, never cut short."""
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_NONBLOCK", 0) | getattr(os, "O_NOCTTY", 0))
    with os.fdopen(fd, "rb") as f:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise OSError("not a regular file")
        data = f.read(limit + 1)
    if len(data) > limit:
        raise OSError(f"larger than {limit} bytes")
    return data

def read_text(path, limit=LIMIT):
    return read_file(path, limit).decode("utf-8", "replace")

# === 1. FIND ===
def find(p, depth, top=True):
    """Skill folders under p. A symlink is followed only when it is the root or is itself a skill."""
    try:
        if (p / "SKILL.md").is_file():
            return [p]
        if depth == 0 or not p.is_dir() or (p.is_symlink() and not top):
            return []
        kids = sorted(c for c in p.iterdir() if c.name not in SKIP)
    except OSError as e:
        problems.append(f"{p}: {e.strerror or e}")
        return []
    return [s for c in kids for s in find(c, depth - 1, False)]

roots = [] if only else [(HOME / r, 1) for r in DEFAULT_ROOTS]
roots += [(pathlib.Path(p).expanduser(), 6) for p in paths]
missing, broken, entries, skills = [], [], 0, {}
for root, depth in roots:
    try:
        if not root.exists():
            missing.append(str(root))
            continue
        if root.is_dir():  # only links directly inside a scanned folder are reported
            broken += [str(c) for c in sorted(root.iterdir()) if c.is_symlink() and not c.exists()]
    except OSError as e:
        problems.append(f"{root}: {e.strerror or e}")
    for entry in find(root, depth):
        entries += 1
        try:
            skills.setdefault(entry.resolve(), []).append(str(entry))
        except (OSError, RuntimeError) as e:
            problems.append(f"{entry}: {e}")

# === 2. TRIAGE ===
try:
    lock = json.loads(read_text(HOME / ".agents/.skill-lock.json"))["skills"]
    lock = lock if isinstance(lock, dict) else {}
except (OSError, ValueError, KeyError, TypeError):
    lock = {}
PLUGINS = (HOME / ".claude/plugins").resolve()
INSTALL_DIR = (HOME / ".agents/skills").resolve()
GITHUB = re.compile(r'^(?:https://(?:[^@/\s]+@)?github\.com/|git@github\.com:|ssh://git@github\.com/)'
                    r'([\w.-]+)/([\w.-]+?)(?:\.git)?/?$', re.I)
origin_cache = {}

def origin_url(top):
    """remote.origin.url read from the checkout's config file. git is never run on a scanned folder."""
    try:
        gitdir = top / ".git"
        if gitdir.is_file():  # a worktree or submodule: "gitdir: <path>"
            m = re.match(r'gitdir:\s*(.+)', read_text(gitdir, 4096))
            gitdir = top / m.group(1).strip() if m else gitdir
            if (gitdir / "commondir").is_file():
                gitdir = gitdir / read_text(gitdir / "commondir", 4096).strip()
        text = read_text(gitdir / "config", 1 << 20)
    except OSError:
        return ""
    section = ""
    for line in text.splitlines():
        line = line.strip()
        if line.startswith("["):
            section = line
        elif re.match(r'\[remote\s+"origin"\]', section):
            m = re.match(r'url\s*=\s*(.+)', line)
            if m:
                return m.group(1).strip().strip('"')
    return ""

def git_source(real):
    """owner/repo when the enclosing checkout's origin is on github.com, else ''. Stops below $HOME."""
    try:
        for top in [real, *real.parents]:
            if top == HOME or top == top.parent:
                return ""
            if (top / ".git").exists():
                break
        else:
            return ""
    except OSError:
        return ""
    if top not in origin_cache:
        m = GITHUB.match(origin_url(top))
        origin_cache[top] = f"{m.group(1)}/{m.group(2)}" if m else ""
    return origin_cache[top]

def triage(real):
    """(origin, record, source). Only a source verified as a github.com owner/repo can be 'yours'."""
    if PLUGINS in real.parents:  # always 'installed': a plugin update replaces the folder
        parts = real.relative_to(PLUGINS).parts
        return "installed", "plugin", "/".join(parts[1:3] if parts[0] == "cache" else parts[1:2])
    rec = lock.get(real.name)
    # The record counts only for the folder that physically sits in the install location, never
    # for a folder reached through a link from there.
    if isinstance(rec, dict) and real.parent == INSTALL_DIR:
        url = rec.get("sourceUrl") or (f"https://github.com/{rec.get('source', '')}"
                                       if rec.get("sourceType", "github") == "github" else "")
        m = GITHUB.match(str(url))
        if m and f"{m.group(1)}/{m.group(2)}".split("/")[0].lower() in owners:
            return "yours", "lock", f"{m.group(1)}/{m.group(2)}"
        return "installed", "lock", str(rec.get("source", ""))
    source = git_source(real)
    if source and source.split("/")[0].lower() in owners:
        return "yours", "git", source
    if source and owners:
        return "installed", "git", source
    return "unknown", "git" if source else "none", source

# === 3. SCAN ===
PACKAGE = r'(?:@[a-z0-9][\w.-]*/)?[a-z0-9][\w.-]*'
NPM_AT = re.compile(r'(?<![\w/@.-])(' + PACKAGE + r')@(\d+\.\d+(?:\.\d+)?)\b')
NPM_JSON = re.compile(r'"(' + PACKAGE + r')"\s*:\s*"[\^~]?(\d+\.\d+(?:\.\d+)?)"')
PIP_EQ = re.compile(r'(?<![\w.-])([A-Za-z0-9][\w.-]*)==(\d+\.\d+(?:\.\d+)?)\b')
NOT_PACKAGES = {"version"}  # JSON keys that carry a version but name no package
latest_cache = {}

def ver(v):
    return tuple(int(x) for x in re.findall(r'\d+', v)[:3])

def latest(registry, pkg):
    """Latest release from the registry over HTTPS, or None. One request per package per run."""
    if (registry, pkg) not in latest_cache:
        found = None
        if re.fullmatch(PACKAGE, pkg, re.I):
            if registry == "npm":
                url, key = f"{NPM_REGISTRY}/{urllib.parse.quote(pkg, safe='@')}/latest", None
            else:
                url, key = f"{PYPI_INDEX}/{urllib.parse.quote(pkg)}/json", "info"
            try:
                with urllib.request.urlopen(url, timeout=8) as r:
                    data = json.loads(r.read(8 << 20))
                found = str((data[key] if key else data)["version"])
            except Exception:
                pass
        latest_cache[registry, pkg] = found  # only a finished lookup is remembered
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

MAX_LOOKUPS = 30
real_check_output, real_path_read_text = subprocess.check_output, pathlib.Path.read_text
check_code, lookups = None, [0]

class CheckAbort(BaseException):
    """Stops a check for good. Not an Exception on purpose: the check code and latest() both
    catch Exception around a lookup, and would otherwise carry on past a deadline or a limit."""

def checked_read_text(self, encoding=None, errors=None):
    """Path.read_text while the check runs: every file it opens goes through read_file. Line
    endings are translated as the real read_text does, so a CRLF skill gets the same verdict."""
    try:
        data = read_file(self)
    except OSError as e:
        raise CheckAbort(f"{self.name}: {e.strerror or e}")
    return io.TextIOWrapper(io.BytesIO(data), encoding=encoding or "utf-8", errors=errors or "strict",
                            newline=None).read()

def tool_output(cmd, **kw):
    """Answers the two commands the check runs. `npm view` comes from the registry instead, and
    `git ls-remote` runs where no repository, user or system configuration can steer it."""
    lookups[0] -= 1
    if lookups[0] < 0:
        raise CheckAbort(f"more than {MAX_LOOKUPS} pins")
    if cmd[:2] == ["npm", "view"]:
        found = latest("npm", cmd[2])
        if not found:
            raise OSError("unresolved")
        return found + "\n"
    if cmd[:2] == ["git", "ls-remote"]:
        env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
        env.update(GIT_TERMINAL_PROMPT="0", GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
        return real_check_output(["git", "-c", "protocol.allow=never", "-c", "protocol.https.allow=always",
                                  *cmd[1:]], cwd="/", env=env, **kw)
    raise OSError("not allowed")

def on_alarm(signum, frame):
    raise CheckAbort(f"timed out after {CHECK_SECONDS} s")

def run_check(real):
    """(verdict, detail) from the check code of the upkeep.sh next to this file, run inside this
    isolated interpreter so the verdict logic has one source. No script of the scanned skill runs."""
    global check_code
    try:
        if check_code is None:
            src = read_text(pathlib.Path(upkeep))
            check_code = compile(src.split("<<'PY'\n", 1)[1].rsplit("\nPY", 1)[0], upkeep, "exec")
    except (OSError, IndexError, SyntaxError, ValueError):
        return "UNKNOWN", "upkeep.sh is not next to scan.sh"
    out, argv = io.StringIO(), sys.argv
    lookups[0] = MAX_LOOKUPS  # a hostile pin list cannot run the scan forever
    try:
        try:
            sys.argv = [upkeep, "check", str(real), str(real)]
            subprocess.check_output, pathlib.Path.read_text = tool_output, checked_read_text
            if hasattr(signal, "SIGALRM"):
                signal.signal(signal.SIGALRM, on_alarm)
                signal.alarm(CHECK_SECONDS)
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
                exec(check_code, {"__name__": "__main__"})
        finally:
            if hasattr(signal, "SIGALRM"):
                signal.alarm(0)
            sys.argv = argv
            subprocess.check_output, pathlib.Path.read_text = real_check_output, real_path_read_text
    except SystemExit:
        pass
    except CheckAbort as e:  # whatever was printed so far is not a verdict
        return "UNKNOWN", f"check stopped: {e}"
    except Exception as e:
        return "UNKNOWN", f"check failed: {type(e).__name__}"
    m = re.search(r'^VERDICT: (\w+)', out.getvalue(), re.M)
    # An unresolved pin does not change the verdict, so it is listed: FRESH with one is not proven.
    flagged = re.findall(r'^(?:STALE|TAMPERED|MISSING|UNSTAMPED|UNKNOWN(?=\s+pin:))\s+(.+?)\s*$',
                         out.getvalue(), re.M)
    return (m.group(1) if m else "UNKNOWN"), "; ".join(re.sub(r'\s+', ' ', f) for f in flagged[:3])

def scan(real, text, self_updating):
    """(state, detail, signals)."""
    named = lambda sigs: ", ".join(f"{s['package']} {s['written']}" for s in sigs)
    if self_updating:
        return ("NOT-CHECKED", "", []) if offline else (*run_check(real), [])
    sigs = signals(text)
    if not sigs:
        return "NO-SIGNAL", "", sigs
    if offline:
        return "NOT-CHECKED", named(sigs), sigs
    for s in sigs:
        s["latest"] = latest(s["registry"], s["package"])
    gaps = [s for s in sigs if s["latest"] and ver(s["latest"]) > ver(s["written"])]
    unresolved = [s for s in sigs if not s["latest"]]
    if gaps:
        detail = ", ".join(f"{s['package']} {s['written']} → {s['latest']}" for s in gaps)
        return "GAP", detail + (f"; not found: {named(unresolved)}" if unresolved else ""), sigs
    if unresolved:  # CURRENT is claimed only when every named package resolved
        return "UNRESOLVED", f"not found: {named(unresolved)}", sigs
    return "CURRENT", named(sigs), sigs

rows = []
for real, links in sorted(skills.items()):
    origin, record, source = triage(real)
    row = {"name": real.name, "frontmatter_name": "", "path": str(real), "links": links,
           "origin": origin, "record": record, "source": source, "self_updating": False,
           "state": "UNREADABLE", "detail": "", "signals": [], "sha256": ""}
    rows.append(row)  # the folder name is the skill's identity (the spec requires `name` to match it)
    try:
        data = read_file(real / "SKILL.md")
    except OSError as e:
        row["detail"] = e.strerror or str(e)
        problems.append(f"{real / 'SKILL.md'}: {row['detail']}")
        continue
    text = data.decode("utf-8", "replace")
    fm = re.match(r'---\r?\n(.*?)\r?\n---', text, re.S)
    fm = fm.group(1) if fm else ""
    nm = re.search(r'^name:\s*["\']?([^"\'\n#]+)', fm, re.M)
    # Any upkeep-* key counts: a freeze-only skill has a stamp and no pins.
    row["self_updating"] = bool(re.search(r'^\s{2,}upkeep-[a-z0-9-]+\s*:|^\s{2}pins:\s*$', fm, re.M))
    row["frontmatter_name"] = nm.group(1).strip() if nm else ""
    row["sha256"] = hashlib.sha256(data).hexdigest()[:16]  # of the whole file, never of a prefix
    try:
        row["state"], row["detail"], row["signals"] = scan(real, text, row["self_updating"])
    except CheckAbort as e:  # a deadline that fired outside the check's own handler
        row["state"], row["detail"] = "UNKNOWN", f"check stopped: {e}"

by_name = collections.defaultdict(list)
for r in rows:
    by_name[r["name"]].append(r)
for r in rows:
    r["copies"] = len(by_name[r["name"]])
    hashes = {c["sha256"] for c in by_name[r["name"]]}
    r["copies_identical"] = None if "" in hashes else len(hashes) == 1  # None: a copy was not read

summary = {"roots": len(roots) - len(missing), "missing_roots": missing, "broken_links": broken,
           "problems": problems, "entries": entries, "skill_folders": len(rows), "names": len(by_name),
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
if problems:
    print(f"could not read ({len(problems)}): {'; '.join(problems)}")
if not owners:
    print("no --owner given: nothing is marked 'yours'")
if rows:
    order = {"yours": 0, "unknown": 1, "installed": 2}
    rows.sort(key=lambda r: (order[r["origin"]], r["name"], r["path"]))
    w = min(34, max(len(r["name"]) for r in rows))
    print(f"\n{'NAME':<{w}}  {'ORIGIN':<9}  {'STATE':<11}  COPIES  SOURCE / DETAIL")
    printed = set()
    for r in rows:
        mark = {True: "=", False: "≠", None: "?"}[r["copies_identical"]]
        copies = "1" if r["copies"] == 1 else f"{r['copies']}{mark}"
        differs = r["path"].replace(str(HOME), "~", 1) if r["copies"] > 1 and not r["copies_identical"] else ""
        tail = " — ".join(x for x in (r["source"], r["detail"], differs) if x)
        line = f"{r['name'][:w]:<{w}}  {r['origin']:<9}  {r['state']:<11}  {copies:<6}  {tail}"
        if (line, r["sha256"]) not in printed:  # identical copies print once
            printed.add((line, r["sha256"]))
            print(line)
    print("\nCOPIES: '=' every copy has the same SKILL.md, '≠' they differ, '?' one could not be read. Paths: --json.")
PY
