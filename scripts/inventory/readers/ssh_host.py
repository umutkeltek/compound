"""ssh_host: `uptime`, `df -P /`, and /proc memory and CPU over ssh -> one `host`."""

import re

from ._base import PCT, pct, reader, short_hash, ssh_cmd, ssh_error

REMOTE_STATUS = ("LC_ALL=C; export LC_ALL; uptime; echo @@df; df -P /; echo @@mem; "
                 "cat /proc/meminfo 2>/dev/null; echo @@stat; head -n 1 /proc/stat 2>/dev/null; "
                 "sleep 1; head -n 1 /proc/stat 2>/dev/null; echo @@end")


def split_sections(text):
    sections, name = {"uptime": []}, "uptime"
    for line in text.splitlines():
        if line.startswith("@@"):
            name = line[2:].strip()
            sections[name] = []
        else:
            sections.setdefault(name, []).append(line)
    return sections


def cpu_busy_pct(stat_lines):
    samples = []
    for line in stat_lines:
        parts = line.split()
        if parts and parts[0] == "cpu":
            try:
                samples.append([int(x) for x in parts[1:]])
            except ValueError:
                return None
    if len(samples) < 2:
        return None
    total = sum(samples[1]) - sum(samples[0])
    idle = (samples[1][3] + (samples[1][4] if len(samples[1]) > 4 else 0)) - \
           (samples[0][3] + (samples[0][4] if len(samples[0]) > 4 else 0))
    return round(100.0 * (total - idle) / total, 1) if total > 0 else None


@reader("ssh_host")
def read_ssh_host(ctx):
    """`uptime`, `df -P /`, and memory and CPU where /proc offers a simple read: one `host`."""
    result = ctx.run(ssh_cmd(ctx, REMOTE_STATUS), what="the server")
    if result.code != 0 and "@@end" not in (result.out or ""):
        raise ssh_error(result)
    sec = split_sections(result.out or "")
    uptime_text = " ".join(sec.get("uptime", []))
    facts = {"status": ctx.fact("up")}
    days = re.search(r"up\s+(\d+)\s+day", uptime_text)
    if re.search(r"\bup\b", uptime_text):
        facts["uptime_days"] = ctx.fact(int(days.group(1)) if days else 0, "days")
    load = re.search(r"load averages?:\s*([0-9]+(?:\.[0-9]+)?)", uptime_text)
    if load:
        facts["load_1m"] = ctx.fact(float(load.group(1)))
    df_rows = [line.split() for line in sec.get("df", []) if line.strip()]
    if len(df_rows) >= 2 and len(df_rows[-1]) >= 5 and df_rows[-1][4].endswith("%"):
        row = df_rows[-1]
        facts["disk_pct"] = ctx.fact(float(row[4].rstrip("%")), PCT, "root file system")
        try:
            facts["disk_gib"] = ctx.fact(round(int(row[1]) * 1024 / 2 ** 30, 1), "GiB")
        except ValueError:
            pass
    else:
        facts["disk_pct"] = ctx.unknown("failed", "df gave no usable line", PCT)
    mem = {}
    for line in sec.get("mem", []):
        match = re.match(r"(\w+):\s+(\d+)\s*kB", line)
        if match:
            mem[match.group(1)] = int(match.group(2))
    if mem.get("MemTotal") and "MemAvailable" in mem:
        facts["memory_pct"] = ctx.fact(pct(mem["MemTotal"] - mem["MemAvailable"], mem["MemTotal"]), PCT,
                                       "used, not counting reclaimable cache")
        facts["memory_gib"] = ctx.fact(round(mem["MemTotal"] / 2 ** 20, 1), "GiB")
    else:
        facts["memory_pct"] = ctx.unknown("not_supported", "the host has no simple memory read (/proc/meminfo)", PCT)
    busy = cpu_busy_pct(sec.get("stat", []))
    facts["cpu_pct"] = (ctx.fact(busy, PCT, "busy over one second") if busy is not None
                        else ctx.unknown("not_supported", "the host has no simple CPU read (/proc/stat)", PCT))
    key = ctx.row.get("id_key") or ctx.row["ssh_alias"]
    label = ctx.row.get("label") or ctx.row["ssh_alias"]
    return [ctx.resource(f"ssh:host:{short_hash(key)}", "host", label, facts=facts, places=ctx.row.get("places"))]
