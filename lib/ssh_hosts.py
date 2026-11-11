#!/usr/bin/env python3
"""Helper for ssh-menu.sh: parse and edit ~/.ssh/config, check hosts, render list and preview.

Host metadata lives in comments directly above a `Host` line:
    # group: Proxmox
    # env: prod
    # url: https://pm:8006
    Host pm
"""
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import time
import zlib
from collections import Counter
from concurrent.futures import ThreadPoolExecutor

HOME = os.path.expanduser("~")
CFG = os.path.join(HOME, ".ssh", "config")
STATE = os.path.join(os.environ.get("XDG_STATE_HOME", HOME + "/.local/state"), "ssh-menu")
HIST = os.path.join(STATE, "history")
CACHE = os.path.join(STATE, "status.tsv")      # alias, user@host:port, up|down|jump
AUTH = os.path.join(STATE, "auth.tsv")         # alias, key|pw|nokey|hostkey|newkey|down, key name, checked at
FILTER = os.path.join(STATE, "filter")         # group=..., prod=all|hide|only
STATS = os.path.join(STATE, "stats")           # <alias>.json: system info for the preview
BACKUPS = os.path.join(STATE, "backups")
for d in (BACKUPS, STATS):
    os.makedirs(d, exist_ok=True)

AUTH_TTL = 12 * 3600     # re-check key login twice a day
STATS_TTL = 10 * 60      # system info in the preview
NOSHELL_TTL = 24 * 3600  # hosts without a shell (git, network gear): don't retry often

G, R, Y, D, B, X = "\x1b[32m", "\x1b[31m", "\x1b[33m", "\x1b[2m", "\x1b[1;31m", "\x1b[0m"


def rgb(r, g, b, bold=False):
    return f"\x1b[{'1;' if bold else ''}38;2;{r};{g};{b}m"


C_LAB = rgb(120, 108, 112)   # labels, secondary text
C_VAL = rgb(235, 225, 230)   # values
C_OK = rgb(120, 235, 90)
C_WARN = rgb(224, 175, 104)
C_BAD = rgb(255, 92, 108)
C_ACC = rgb(233, 30, 99, bold=True)


# ---------- ~/.ssh/config ----------

def read_lines():
    try:
        return open(CFG).read().split("\n")
    except FileNotFoundError:
        return []


def backup():
    if os.path.exists(CFG):
        shutil.copy2(CFG, os.path.join(BACKUPS, "config-" + time.strftime("%Y%m%d-%H%M%S")))
        old = sorted(f for f in os.listdir(BACKUPS) if f.startswith("config-"))[:-20]
        for f in old:
            os.remove(os.path.join(BACKUPS, f))


def write_lines(lines):
    backup()
    text = "\n".join(lines)
    if not text.endswith("\n"):
        text += "\n"
    with open(CFG, "w") as f:
        f.write(text)
    os.chmod(CFG, 0o600)


def parse():
    hosts, meta = [], {}
    for i, l in enumerate(read_lines()):
        m = re.match(r"^\s*#\s*(group|env|url)\s*:\s*(.+?)\s*$", l, re.I)
        if m:
            meta[m.group(1).lower()] = m.group(2)
            continue
        m = re.match(r"^\s*host\s+(.+?)\s*$", l, re.I)
        if m:
            for a in m.group(1).split():
                if not re.search(r"[*?!]", a):
                    hosts.append(dict(alias=a, group=meta.get("group", ""), env=meta.get("env", ""), url=meta.get("url", ""), line=i + 1))
            meta = {}
        elif l.strip() == "" or not l.lstrip().startswith("#"):
            meta = {}
    return hosts


def find(alias):
    return next((h for h in parse() if h["alias"] == alias), None)


def is_prod(h):
    return h["env"].lower() == "prod"


def resolve(alias):
    """ssh -G -> dict(user, hostname, port, proxyjump, identity)"""
    info = dict(user="", hostname=alias, port="22", proxyjump="", identity="")
    try:
        out = subprocess.run(["ssh", "-G", alias], capture_output=True, text=True, timeout=3).stdout
    except Exception:
        return info
    ids = []
    for l in out.splitlines():
        k, _, v = l.partition(" ")
        if k == "user": info["user"] = v
        elif k == "hostname": info["hostname"] = v
        elif k == "port": info["port"] = v
        elif k == "proxyjump": info["proxyjump"] = v
        elif k == "identityfile": ids.append(os.path.expanduser(v))
    info["identity"] = next((p for p in ids if os.path.exists(p)), "")
    return info


def probe(info):
    if info["proxyjump"]:
        return "jump"
    try:
        socket.create_connection((info["hostname"], int(info["port"])), timeout=0.6).close()
        return "up"
    except Exception:
        return "down"


def addr(info):
    return f'{info["user"]}@{info["hostname"]}:{info["port"]}'


def load_cache():
    c = {}
    try:
        for l in open(CACHE):
            a, ad, st = l.rstrip("\n").split("\t")
            c[a] = (ad, st)
    except Exception:
        pass
    return c


def uses():
    try:
        return Counter(open(HIST).read().split())
    except FileNotFoundError:
        return Counter()


# ---------- key login audit ----------

def load_auth():
    a = {}
    try:
        for l in open(AUTH):
            p = l.rstrip("\n").split("\t")
            if len(p) == 4:
                a[p[0]] = (p[1], p[2], float(p[3]))
    except Exception:
        pass
    return a


def save_auth(a):
    with open(AUTH + ".tmp", "w") as f:
        for k, (st, key, ts) in a.items():
            f.write(f"{k}\t{st}\t{key}\t{ts:.0f}\n")
    os.replace(AUTH + ".tmp", AUTH)


def check_auth(alias):
    """Fresh connection (no multiplexing), public key only, no prompts -> (status, key file)."""
    try:
        err = subprocess.run(
            ["ssh", "-v", "-T", "-o", "BatchMode=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none",
             "-o", "ConnectTimeout=5", "-o", "PreferredAuthentications=publickey", alias, "true"],
            stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=15).stderr
    except Exception:
        return "down", ""
    if "Authenticated to" in err:
        m = re.search(r"Server accepts key: (\S+)", err)
        return "key", os.path.basename(m.group(1)) if m else ""
    if "HOST IDENTIFICATION HAS CHANGED" in err:
        return "hostkey", ""
    if "Host key verification failed" in err:
        return "newkey", ""
    m = re.search(r"Permission denied \(([^)]*)\)", err)
    if m:
        return ("pw" if re.search(r"password|keyboard", m.group(1)) else "nokey"), ""
    return "down", ""


def audit(aliases):
    a = load_auth()
    with ThreadPoolExecutor(16) as ex:
        for alias, (st, key) in zip(aliases, ex.map(check_auth, aliases)):
            if st == "down" and alias in a:
                continue  # keep the last known answer for hosts that are just offline
            a[alias] = (st, key, time.time())
    save_auth(a)


# ---------- list filter ----------

def load_filter():
    f = {"group": "", "prod": "all"}
    try:
        for l in open(FILTER):
            k, _, v = l.rstrip("\n").partition("=")
            if k in f:
                f[k] = v
    except FileNotFoundError:
        pass
    return f


def apply_filter(hosts, f):
    if f["group"]:
        hosts = [h for h in hosts if h["group"].lower() == f["group"].lower()]
    if f["prod"] == "hide":
        hosts = [h for h in hosts if not is_prod(h)]
    elif f["prod"] == "only":
        hosts = [h for h in hosts if is_prod(h)]
    return hosts


def cmd_filter(op):
    f = load_filter()
    if op == "reset":
        f = {"group": "", "prod": "all"}
    elif op in ("next", "prev"):
        groups = [""] + sorted({h["group"] for h in parse() if h["group"]}, key=str.lower)
        i = groups.index(f["group"]) if f["group"] in groups else 0
        f["group"] = groups[(i + (1 if op == "next" else -1)) % len(groups)]
    elif op == "prod":
        f["prod"] = {"all": "hide", "hide": "only"}.get(f["prod"], "all")
    with open(FILTER, "w") as fh:
        fh.write(f"group={f['group']}\nprod={f['prod']}\n")


def cmd_label():
    f = load_filter()
    parts = [f["group"]] if f["group"] else []
    if f["prod"] == "hide": parts.append("without PROD")
    if f["prod"] == "only": parts.append("PROD only")
    n = len(apply_filter(parse(), f))
    print(f" {' · '.join(parts) or 'all hosts'} · {n} ")


# ---------- list ----------

PALETTE = [(248, 187, 217), (125, 207, 255), (158, 206, 106), (224, 175, 104), (187, 154, 247), (115, 218, 202)]
FIXED = {"proxmox": (248, 187, 217), "services": (125, 207, 255), "git": (158, 206, 106), "legacy": (130, 120, 125)}


def group_color(name):
    if name.lower() in FIXED:
        return FIXED[name.lower()]
    return PALETTE[zlib.crc32(name.lower().encode()) % len(PALETTE)]


def group_cell(name, width):
    if not name:
        return " " * width
    return rgb(*group_color(name)) + f"{name:<{width}}" + X


def addr_cell(ad, width):
    """host[:port] for the list (user is in the preview), padded to width"""
    m = re.match(r"^(.*)@(.*):(\d+)$", ad or "")
    if not m:
        return D + f"{ad or '':<{width}}" + X
    _, h, p = m.groups()
    port = "" if p == "22" else f":{p}"
    pad = " " * max(0, width - len(h) - len(port))
    return f"{rgb(205, 195, 200)}{h}{X}{C_LAB}{port}{X}{pad}"


# list marker for hosts where key login does not work (nothing is shown when it does)
AUTH_TAG = {"pw": (C_WARN, "password"), "nokey": (C_BAD, "no access"),
            "hostkey": (C_BAD, "host key changed"), "newkey": (C_LAB, "new host key")}


def cmd_list(live, force=False):
    hosts = parse()
    cnt = uses()
    hosts.sort(key=lambda h: (-cnt.get(h["alias"], 0), h["alias"].lower()))
    if live:
        def work(h):
            i = resolve(h["alias"])
            return h["alias"], addr(i), probe(i)
        with ThreadPoolExecutor(32) as ex:
            rows = list(ex.map(work, hosts))
        with open(CACHE, "w") as f:
            for a, ad, st in rows:
                f.write(f"{a}\t{ad}\t{st}\n")
        cache = {a: (ad, st) for a, ad, st in rows}
        # key login check: only reachable hosts, only when the last check is old
        known, now = load_auth(), time.time()
        todo = [a for a, _, st in rows if st != "down" and (force or now - known.get(a, ("", "", 0))[2] > AUTH_TTL)]
        if todo:
            audit(todo)
        if force:
            for f in os.listdir(STATS):
                os.remove(os.path.join(STATS, f))
    else:
        cache = load_cache()
    auth = load_auth()
    dots = {"up": G + "●" + X, "down": "\x1b[1;31m●" + X, "jump": Y + "●" + X}
    hosts = apply_filter(hosts, load_filter())
    aw = max([len(h["alias"]) for h in hosts] + [8])   # column widths follow the content
    gw = max([len(h["group"]) for h in hosts] + [0])
    for h in hosts:
        ad, st = cache.get(h["alias"], ("", "?"))
        dot = dots.get(st, D + "·" + X)
        prod = rgb(255, 92, 108, bold=True) + "PROD" + X if is_prod(h) else "    "
        col, txt = AUTH_TAG.get(auth.get(h["alias"], ("",))[0], ("", ""))
        tag = f"{col}{txt}{X}" if txt else ""
        print(f'{dot}  {h["alias"]:<{aw}}  {group_cell(h["group"], gw)}  {prod}   {addr_cell(ad, 16)}  {tag}')


# ---------- system info for the preview ----------

STATS_SH = r'''
echo __SM__
. /etc/os-release 2>/dev/null && echo "os=${PRETTY_NAME:-$NAME}"
echo "up=$(cut -d. -f1 /proc/uptime)"
echo "load=$(cut -d' ' -f1-3 /proc/loadavg)"
echo "cpu=$(nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo)"
awk '/^MemTotal:/{t=$2}/^MemAvailable:/{a=$2}END{print "mem=" t-a " " t}' /proc/meminfo
timeout 3 df -PkTl 2>/dev/null | awk 'NR>1 && $2!~/^(tmpfs|devtmpfs|overlay|squashfs|efivarfs|ramfs|nsfs|fuse.*)$/ {print "df=" $4 " " $3 " " $7}'
if command -v systemctl >/dev/null 2>&1; then
  systemctl --failed --no-legend --plain 2>/dev/null | awk 'NF{print "fail=" $1}'
fi
[ -f /var/run/reboot-required ] && echo reboot=1
if command -v apt-get >/dev/null 2>&1; then
  u=$(timeout 5 apt-get -s -o Debug::NoLocking=1 upgrade 2>/dev/null | grep '^Inst')
  echo "upd=$(printf '%s' "$u" | grep -c .) $(printf '%s' "$u" | grep -ci securi)"
elif command -v dnf >/dev/null 2>&1; then
  echo "upd=$(timeout 5 dnf -C -q check-update 2>/dev/null | grep -c '^[[:alnum:]]') 0"
fi
c=$(timeout 2 docker ps -q 2>/dev/null) && echo "docker=$(printf '%s' "$c" | grep -c .)"
'''


def stats_file(alias):
    return os.path.join(STATS, re.sub(r"[^\w.-]", "_", alias) + ".json")


def load_stats(alias):
    try:
        return json.load(open(stats_file(alias)))
    except Exception:
        return None


def fetch_stats(alias, known_shell):
    ssh = ["ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=4", alias]
    try:
        if not known_shell:
            # first contact: a harmless one-word probe, so network gear (FortiOS: "sh" = "show")
            # or git servers never get the script fed to their CLI
            p = subprocess.run(ssh + ["echo __SM__"], stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=10)
        if known_shell or "__SM__" in p.stdout:
            p = subprocess.run(ssh + ["sh -s"], input=STATS_SH, capture_output=True, text=True, timeout=15)
    except Exception:
        return None
    if "__SM__" not in p.stdout:
        if p.returncode == 255:
            return None                       # could not log in / reach it: don't cache
        d = {"ts": time.time(), "noshell": True}  # logged in, but no shell (git, network device)
    else:
        d = {"ts": time.time(), "df": [], "fail": []}
        for l in p.stdout.split("__SM__", 1)[1].splitlines():
            k, _, v = l.partition("=")
            if k == "df":
                d["df"].append(v.split(" ", 2))   # used KiB, total KiB, mount
            elif k == "fail":
                d["fail"].append(v)
            elif k:
                d[k] = v
    with open(stats_file(alias), "w") as f:
        json.dump(d, f)
    return d


def human_kib(k):
    g = k / 1048576
    return f"{g:.1f}G" if g < 10 else f"{g:.0f}G" if g < 1000 else f"{g / 1024:.1f}T"


def human_secs(s):
    d, h, m = s // 86400, s % 86400 // 3600, s % 3600 // 60
    return f"{d}d {h}h" if d else f"{h}h {m}m" if h else f"{m}m"


def ago(ts):
    s = int(time.time() - ts)
    return "just now" if s < 60 else f"{s // 60} min ago" if s < 3600 else f"{s // 3600} h ago"


def bar(pct, width=10):
    n = min(width, round(pct / 100 * width))
    col = C_BAD if pct >= 90 else C_WARN if pct >= 80 else rgb(205, 195, 200)
    return col + "━" * n + rgb(70, 58, 62) + "━" * (width - n) + X


def pct_cell(pct):
    col = C_BAD if pct >= 90 else C_WARN if pct >= 80 else C_VAL
    return f"{col}{pct:>3.0f}%{X}"


def render_stats(d):
    lab = lambda t: C_LAB + f"{t:<8}" + X
    out = []
    if d.get("os"):
        out.append(rgb(205, 195, 200, bold=True) + d["os"] + X)
    if d.get("up", "").isdigit():
        out.append(lab("up") + C_VAL + human_secs(int(d["up"])) + X)
    if d.get("load"):
        cpu = int(d["cpu"]) if d.get("cpu", "").isdigit() else 0
        l1 = float(d["load"].split()[0])
        col = C_BAD if cpu and l1 > 2 * cpu else C_WARN if cpu and l1 > cpu else C_VAL
        out.append(lab("load") + col + d["load"] + X + (f"{C_LAB}  · {cpu} cpu{X}" if cpu else ""))
    if d.get("mem"):
        used, total = map(int, d["mem"].split())
        if total:
            pct = used * 100 / total
            out.append(lab("mem") + bar(pct) + " " + pct_cell(pct) + f"{C_LAB}  {human_kib(used)} / {human_kib(total)}{X}")
    disks = []
    for used, total, mount in d.get("df", []):
        used, total = int(used), int(total)
        if not total or re.match(r"^/(run|snap|var/lib/docker|var/snap)(/|$)", mount):
            continue
        pct = used * 100 / total
        if mount.startswith("/boot") and pct < 80:
            continue
        disks.append((mount != "/", -pct, mount, pct, total - used))
    disks.sort()
    for i, (_, _, mount, pct, free) in enumerate(disks[:4]):
        extra = f"{C_WARN}  {human_kib(free)} free{X}" if pct >= 80 else ""
        out.append(lab("disk" if i == 0 else "") + bar(pct) + " " + pct_cell(pct) + f"  {C_VAL}{mount}{X}" + extra)
    if len(disks) > 4:
        out.append(lab("") + C_LAB + f"+{len(disks) - 4} more" + X)
    if "upd" in d:
        n, sec = (d["upd"].split() + ["0"])[:2]
        if n == "0":
            line = C_LAB + "up to date" + X
        else:
            line = C_WARN + f"{n} pending" + X + (f"{C_BAD}  · {sec} security{X}" if sec != "0" else "")
        out.append(lab("updates") + line)
    if d.get("reboot"):
        out.append(lab("") + C_WARN + "reboot required" + X)
    if d.get("fail"):
        out.append(lab("failed") + C_BAD + f"{len(d['fail'])} unit{'s' if len(d['fail']) > 1 else ''}" + X)
        for u in d["fail"][:4]:
            out.append(lab("") + C_LAB + u + X)
    elif "fail" in d and "load" in d:
        out.append(lab("units") + C_LAB + "all running" + X)
    if d.get("docker"):
        out.append(lab("docker") + C_VAL + f"{d['docker']} running" + X)
    return out


# ---------- preview ----------

AUTH_TEXT = {"pw": (C_WARN, "password only", "Alt+K to push key"),
             "nokey": (C_BAD, "key rejected", "Alt+K to push key"),
             "hostkey": (C_BAD, "host key CHANGED", "check, then ssh-keygen -R"),
             "newkey": (C_WARN, "host key not known yet", "connect once to accept")}


def cmd_preview(alias):
    sys.stdout.reconfigure(line_buffering=True)  # show the fast part before the slow one
    lab = lambda t: C_LAB + f"{t:<8}" + X
    val = lambda t: C_VAL + str(t) + X
    h = find(alias)
    if not h:
        print(f"{alias}\n\n{D}not in ~/.ssh/config:\nwill connect as typed{X}")
        return
    i = resolve(alias)
    st = probe(i)
    col = {"up": C_OK, "down": C_BAD, "jump": C_WARN}[st]
    prod = "  " + rgb(255, 92, 108, bold=True) + "PROD" + X if is_prod(h) else ""
    print("\x1b[1m" + col + "● " + alias + X + prod)
    print(f'{C_LAB}{addr(i)}{X}\n')
    if h["group"]:
        print(lab("group") + rgb(*group_color(h["group"])) + h["group"] + X)
    if i["proxyjump"]:
        print(lab("via") + val(i["proxyjump"]))
    a = load_auth().get(alias)
    if a and a[0] == "key":
        print(lab("key") + val(a[1] or "agent"))
    elif a and a[0] in AUTH_TEXT:
        c, t, hint = AUTH_TEXT[a[0]]
        print(lab("key") + c + t + X + f"{C_LAB}  · {hint}{X}")
    else:
        print(lab("key") + val(os.path.basename(i["identity"]) or "default / agent"))
    if h["url"]:
        print(lab("web") + val(h["url"]))
    if st != "jump":
        try:
            out = subprocess.run(["ping", "-c1", "-W1", i["hostname"]], capture_output=True, text=True, timeout=2).stdout
            m = re.search(r"time=([\d.]+)", out)
            print(lab("ping") + (val(m.group(1) + " ms") if m else C_BAD + "no reply" + X))
        except Exception:
            print(lab("ping") + "-")
    print()
    if st == "down":
        print(C_BAD + "unreachable" + X)
        return
    if a and a[0] != "key":
        print(C_LAB + "system info needs key login" + X)
        return
    d = load_stats(alias)
    ttl = NOSHELL_TTL if d and d.get("noshell") else STATS_TTL
    if not d or time.time() - d["ts"] > ttl:
        d = fetch_stats(alias, bool(d and not d.get("noshell"))) or d
    if not d:
        print(C_LAB + "no system info (login failed)" + X)
    elif d.get("noshell"):
        print(C_LAB + "no shell here (git / network device)" + X)
    else:
        print("\n".join(render_stats(d)))
        print(f"\n{C_LAB}{ago(d['ts'])} · Ctrl+R to refresh{X}")


def cmd_help():
    k = lambda key, txt: print(f"  {rgb(248, 187, 217, bold=True)}{key:<10}{X}{C_VAL}{txt}{X}")
    sec = lambda t: print(f"{C_ACC}{t}{X}")
    sec("connect")
    k("Enter", "ssh")
    k("Alt+T", "ssh + tmux session")
    k("Alt+F", "files in Nautilus")
    k("Alt+U", "upload")
    k("Alt+D", "download")
    k("Alt+W", "open web UI")
    k("Alt+Y", "copy user@host")
    print()
    sec("several hosts")
    k("Tab", "mark · Alt+A/Z all/none")
    k("Alt+X", "run a command on all")
    k("Alt+B", "broadcast: type into all")
    k("Alt+K", "push SSH key")
    print()
    sec("list")
    k("Alt+← →", "show one group")
    k("Alt+P", "all / no PROD / PROD only")
    k("Ctrl+R", "recheck status, keys, info")
    print()
    sec("edit")
    k("Alt+N", "new host")
    k("Alt+E", "edit in ~/.ssh/config")
    k("Alt+G", "set group")
    k("Alt+R", "mark / unmark PROD")


# ---------- edit ----------

def host_index(lines, alias):
    for i, l in enumerate(lines):
        m = re.match(r"^\s*host\s+(.+?)\s*$", l, re.I)
        if m and alias in m.group(1).split():
            return i
    return -1


def set_meta(alias, key, value):
    lines = read_lines()
    idx = host_index(lines, alias)
    if idx < 0:
        sys.exit(f"{alias}: not found")
    j = idx
    while j > 0 and lines[j - 1].lstrip().startswith("#"):
        j -= 1
    block = [l for l in lines[j:idx] if not re.match(rf"^\s*#\s*{key}\s*:", l, re.I)]
    if value:
        block.append(f"# {key}: {value}")
    lines[j:idx] = block
    write_lines(lines)


def cmd_add(alias, hostname, user, port, jump, group, env, identity):
    if find(alias) or re.search(r"[\s*?!]", alias):
        sys.exit("bad or duplicate alias")
    block = []
    if group: block.append(f"# group: {group}")
    if env: block.append(f"# env: {env}")
    block.append(f"Host {alias}")
    block.append(f"    HostName {hostname or alias}")
    if user: block.append(f"    User {user}")
    if port and port != "22": block.append(f"    Port {port}")
    if jump: block.append(f"    ProxyJump {jump}")
    if identity:
        block.append(f"    IdentityFile {identity}")
        block.append("    IdentitiesOnly yes")
    lines = read_lines()
    # insert before the "Host *" defaults block (and the comments above it), so defaults stay last
    star = next((i for i, l in enumerate(lines) if re.match(r"^\s*host\s+\*\s*$", l, re.I)), -1)
    if star >= 0:
        j = star
        while j > 0 and lines[j - 1].lstrip().startswith("#"):
            j -= 1
        while j > 0 and lines[j - 1].strip() == "":
            j -= 1
        lines[j:j] = [""] + block
    else:
        while lines and lines[-1].strip() == "":
            lines.pop()
        lines += [""] + block
    write_lines(lines)


def cmd_run(cmd, hosts):
    """Run cmd on all hosts in parallel; hosts with identical output are merged into one block."""
    def one(h):
        try:
            p = subprocess.run(["ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=6", h, cmd],
                               stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=60)
            return h, p.returncode, (p.stdout + p.stderr).rstrip("\n")
        except subprocess.TimeoutExpired:
            return h, -1, "timeout"
        except Exception as e:
            return h, -1, str(e)
    with ThreadPoolExecutor(16) as ex:
        res = list(ex.map(one, hosts))
    groups, order = {}, []
    for h, rc, out in res:
        k = (rc, out)
        if k not in groups:
            groups[k] = []
            order.append(k)
        groups[k].append(h)
    print(f"{D}$ {cmd}{X}\n")
    for rc, out in order:
        hs = groups[(rc, out)]
        note = "" if rc == 0 else f"  {D}{'ssh error' if rc in (-1, 255) else f'exit {rc}'}{X}"
        print(f'{(G if rc == 0 else R)}●{X} {rgb(235, 225, 230, bold=True)}{", ".join(hs)}{X}{note}')
        for l in out.splitlines():
            print("  " + l)
        print()


def url_default(alias):
    i, h = resolve(alias), find(alias)
    port = ""
    if h and h["group"].lower() == "proxmox":
        port = ":8007" if alias == "pbs" else ":8006"
    return f'https://{i["hostname"]}{port}'


def main():
    a = sys.argv[1:]
    c = a[0] if a else "list"
    if c == "list": cmd_list(False)
    elif c == "live": cmd_list(True)
    elif c == "refresh": cmd_list(True, force=True)
    elif c == "preview": cmd_preview(a[1])
    elif c == "help": cmd_help()
    elif c == "filter": cmd_filter(a[1])
    elif c == "label": cmd_label()
    elif c == "audit": audit(a[1:])
    elif c == "addr":
        i = resolve(a[1]); print(addr(i), "jump" if i["proxyjump"] else "")
    elif c == "env":
        h = find(a[1]); print(h["env"] if h else "")
    elif c == "line":
        h = find(a[1]); print(h["line"] if h else 1)
    elif c == "groups":
        print("\n".join(sorted({h["group"] for h in parse() if h["group"]})))
    elif c == "url":
        h = find(a[1]); print(h["url"] if h else "")
    elif c == "url-default": print(url_default(a[1]))
    elif c == "run": cmd_run(a[1], a[2:])
    elif c == "has": sys.exit(0 if find(a[1]) else 1)
    elif c == "set": set_meta(a[1], a[2], a[3] if len(a) > 3 else "")
    elif c == "toggle-prod":
        h = find(a[1])
        if h: set_meta(a[1], "env", "" if is_prod(h) else "prod")
    elif c == "add": cmd_add(*a[1:9])


main()
