"""pve: a virtualization cluster read over ssh -> one `host` per node, one `guest` per VM or container."""

import collections

from ._base import PCT, SourceError, parse_json, pct, reader, short_hash, ssh_cmd, ssh_error


@reader("pve")
def read_pve(ctx):
    """PVE cluster: `pvesh get /cluster/resources` over ssh -> one `host` per node, one `guest` per VM
    or container."""
    result = ctx.run(ssh_cmd(ctx, "pvesh get /cluster/resources --output-format json"), what="the virtualization host")
    if result.code != 0:
        raise ssh_error(result)
    rows = parse_json(result.out, "cluster resources")
    if not isinstance(rows, list):
        raise SourceError("failed", "The cluster listing could not be read: it is not in the expected format.")
    key = ctx.row.get("id_key") or ctx.row["ssh_alias"]
    node_labels = ctx.row.get("node_labels") or {}
    nodes = [r for r in rows if r.get("type") == "node"]
    host_ids = {r.get("node"): f"pve:node:{short_hash(key, r.get('node'))}" for r in nodes}
    running = collections.Counter()
    guests, templates = [], 0
    for r in rows:
        if r.get("type") not in ("qemu", "lxc"):
            continue
        if r.get("template"):
            templates += 1
            continue
        status = str(r.get("status") or "unknown")
        if status == "running":
            running[r.get("node")] += 1
        facts = {"status": ctx.fact(status), "guest_type": ctx.fact(r.get("type"))}
        if status == "running":
            facts["cpu_pct"] = ctx.fact(round(100.0 * float(r.get("cpu") or 0), 1), PCT)
            memory = pct(r.get("mem"), r.get("maxmem"))
            seen_by = "as the hypervisor sees it; can include the guest's cache" if r.get("type") == "qemu" else ""
            facts["memory_pct"] = (ctx.fact(memory, PCT, seen_by) if memory is not None
                                   else ctx.unknown("not_supported", "no memory size reported", PCT))
            if r.get("type") == "lxc":
                disk = pct(r.get("disk"), r.get("maxdisk"))
                facts["disk_pct"] = (ctx.fact(disk, PCT) if disk is not None
                                     else ctx.unknown("not_supported", "no disk size reported", PCT))
            else:
                facts["disk_pct"] = ctx.unknown(
                    "not_supported", "the hypervisor does not see disk use inside a virtual machine; read it on the guest", PCT)
        name = str(r.get("name") or f"{r.get('type')} {r.get('vmid')}")
        parent = host_ids.get(r.get("node"), "")
        guests.append(ctx.resource(f"pve:guest:{short_hash(key, r.get('type'), r.get('vmid'))}", "guest",
                                   name, parent=parent, facts=facts, places=[name]))
    storages = collections.defaultdict(list)
    for r in rows:
        if r.get("type") == "storage" and r.get("status") == "available":
            used = pct(r.get("disk"), r.get("maxdisk"))
            if used is not None:
                storages[r.get("node")].append(used)
    hosts = []
    for r in nodes:
        node = r.get("node")
        status = str(r.get("status") or "unknown")
        facts = {"status": ctx.fact(status), "guests_running": ctx.fact(running[node], "count")}
        if status == "online":
            facts["cpu_pct"] = ctx.fact(round(100.0 * float(r.get("cpu") or 0), 1), PCT)
            facts["memory_pct"] = ctx.fact(pct(r.get("mem"), r.get("maxmem")), PCT)
            facts["disk_pct"] = ctx.fact(pct(r.get("disk"), r.get("maxdisk")), PCT, "root disk")
            facts["cpus"] = ctx.fact(r.get("maxcpu"), "count")
            facts["memory_gib"] = ctx.fact(round(float(r.get("maxmem") or 0) / 2 ** 30, 1), "GiB")
            if storages[node]:
                facts["storage_max_pct"] = ctx.fact(max(storages[node]), PCT,
                                                    f"highest of {len(storages[node])} storages")
        else:
            for name in ("cpu_pct", "memory_pct", "disk_pct"):
                facts[name] = ctx.unknown("failed", f"the node is {status}", PCT)
        label = node_labels.get(node) or (ctx.row.get("label") if len(nodes) == 1 else "") or str(node)
        hosts.append(ctx.resource(host_ids[node], "host", label, facts=facts,
                                  places=ctx.row.get("places") if len(nodes) == 1 else None))
    if templates:
        ctx.detail = f"{templates} templates are not listed."
    return hosts + guests
