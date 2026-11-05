#!/usr/bin/env python3
"""Select source and bounded private context for a general second opinion."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess


MIB = 1024 * 1024
TEXT_EXTENSIONS = {".md", ".txt", ".json", ".jsonl", ".ndjson", ".yaml", ".yml", ".csv", ".tsv", ".log"}
RUNTIME_ROOTS = {".git", ".claude", ".codex", ".agents", ".hermes", ".ua", "state", "body", "home", "memory", "graphify-out", "bin", ".pytest_cache"}
CACHES = {"node_modules", ".next", "__pycache__", ".DS_Store", "__MACOSX"}
DOCUMENT_ROOTS = (
    "docs_private/architecture", "docs_private/planning", "docs_private/instructions",
    "docs_private/knowledge", "docs_private/audits", "docs_private/critique",
    "docs_private/operator-grants", "docs_private/profiles", "docs_private/reports",
    "docs_private/portfolio/dossiers", "docs_private/plan/workstreams",
    "docs_private/plan/reviews", ".compound-private/skills", ".compound-private/audit",
)


def allowed_path(relative):
    return (relative.parts and relative.parts[0] not in RUNTIME_ROOTS
            and not any(part in CACHES for part in relative.parts)
            and relative.name != "compound"
            and not relative.name.startswith(".env"))


def text_files(root):
    if root.is_symlink():
        return []
    if root.is_file():
        candidates = [root]
    elif root.is_dir():
        candidates = []
        for directory, dirs, files in os.walk(root, followlinks=False):
            dirs[:] = sorted(d for d in dirs if d not in CACHES and d != ".git"
                             and not (Path(directory) / d).is_symlink())
            candidates.extend(Path(directory) / f for f in sorted(files))
    else:
        return []
    result = []
    for candidate in candidates:
        if candidate.is_symlink() or not candidate.is_file():
            continue
        if candidate.suffix.lower() not in TEXT_EXTENSIONS and candidate.name != "README":
            continue
        with candidate.open("rb") as stream:
            if b"\0" in stream.read(8192):
                continue
        result.append(candidate)
    return sorted(result)


def recent_evidence(repo):
    """One newest run per workflow, then choose twelve workflows by recency."""
    candidates = []
    root = repo / "docs_private/plan/product-loop/artifacts"
    if root.is_dir() and not root.is_symlink():
        for workflow in sorted(root.iterdir()):
            if not workflow.is_dir() or workflow.is_symlink():
                continue
            runs = [p for p in workflow.iterdir() if p.is_dir() and not p.is_symlink()]
            newest = max(runs, key=lambda p: (p.stat().st_mtime_ns, p.name)) if runs else workflow
            candidates.append(newest)
    candidates.sort(key=lambda p: (-p.stat().st_mtime_ns, p.as_posix()))
    groups = [(p, "recent workflow evidence") for p in candidates[:12]]
    packets = repo / "docs_private/plan/packets"
    if packets.is_dir() and not packets.is_symlink():
        files = [p for p in packets.iterdir() if p.is_file() and not p.is_symlink()
                 and p.suffix.lower() in TEXT_EXTENSIONS]
        files.sort(key=lambda p: (-p.stat().st_mtime_ns, p.name))
        groups.extend((p, "recent work packet") for p in files[:6])
    return groups, candidates[12:]


def select(repo, budget_mb, extras):
    repo = repo.resolve()
    raw = subprocess.check_output(["git", "-C", str(repo), "ls-files", "-z", "--cached", "--others", "--exclude-standard"])
    included = {}
    omissions = []
    for name in sorted(set(os.fsdecode(p) for p in raw.split(b"\0") if p)):
        relative = Path(name)
        candidate = repo / relative
        if not allowed_path(relative) or relative.parts[0] in {"docs_private", ".compound-private", "Plans"}:
            continue
        if candidate.is_file() or candidate.is_symlink():
            included[name] = "source inventory (tracked and nonignored local files)"
        else:
            omissions.append({"path": name, "reason": "absent from working tree"})

    groups = []
    for name in extras:
        relative = Path(name)
        if relative.is_absolute() or not relative.parts or ".." in relative.parts or not allowed_path(relative):
            raise ValueError("--review-evidence must name a repository-relative document or directory outside runtime/caches")
        candidate = repo / relative
        if not candidate.exists() or candidate.is_symlink() or not candidate.resolve().is_relative_to(repo):
            raise ValueError("--review-evidence must exist inside the repository and cannot be a symlink")
        files = text_files(candidate)
        if not files:
            raise ValueError("--review-evidence contains no eligible text evidence: " + name)
        groups.append((candidate, "explicit review evidence"))
    # Current handoff first; remaining roots are structural context, not a success filter.
    groups.append((repo / "docs_private/plan/HANDOFF.md", "current handoff"))
    groups.extend((repo / p, "current private context") for p in DOCUMENT_ROOTS)
    for root_name in ("docs_private", "docs_private/plan", "docs_private/plan/product-loop"):
        root = repo / root_name
        if root.is_dir() and not root.is_symlink():
            groups.extend((p, "current context at directory root") for p in sorted(root.iterdir())
                          if p.is_file() and not p.is_symlink())
    evidence, unsampled = recent_evidence(repo)
    groups.extend(evidence)
    omissions.extend({"path": p.relative_to(repo).as_posix(), "reason": "outside twelve recent workflow samples"} for p in unsampled)

    budget = budget_mb * MIB
    used = 0
    selected_groups = []
    for root, reason in groups:
        files = [p for p in text_files(root) if p.relative_to(repo).as_posix() not in included]
        if not files:
            continue
        size = sum(p.stat().st_size for p in files)
        if size > budget - used:
            if reason == "explicit review evidence":
                raise ValueError("explicit review evidence exceeds --review-data-mb; increase the budget or narrow the path")
            omissions.append({"path": root.relative_to(repo).as_posix(), "bytes": size,
                              "reason": "whole text group omitted to stay within review-data budget"})
            continue
        used += size
        selected_groups.append({"path": root.relative_to(repo).as_posix(), "reason": reason, "files": len(files), "bytes": size})
        for candidate in files:
            included[candidate.relative_to(repo).as_posix()] = reason
    return {
        "schema_version": "compound.second_opinion_selection.v0.1",
        "data_budget_bytes": budget, "selected_data_bytes": used,
        "selection_rule": "All source; current private context; newest text run per workflow for twelve workflows and six recent packets. No success/failure filtering. Whole text groups fit or are omitted; no file truncation.",
        "limitations": [
            "Private data selection is structural and recent, not an exhaustive or statistically representative runtime audit.",
            "Real COMPOUND_HOME is not exported; repository evidence may be historical. No fresh runtime checks are performed.",
            "Screenshots, videos, binaries, source corpora, archives, duplicate worktrees, caches and Git object history are omitted.",
            "References to omitted files are retained. Selected evidence consists of text files; supporting media may be absent.",
        ],
        "excluded_categories": [
            "Git objects and runtime/cache roots", "raw memory corpora and runtime homes",
            "private archives, reconciliation/recovery dumps and duplicate worktrees",
            "older runs and packets outside the recency sample", "unselected private directories",
            "private images, video and binary attachments",
        ],
        "selected_groups": selected_groups, "omissions": omissions, "files": included,
    }


def manifest(stage, selection_path):
    selection = json.loads(selection_path.read_text())
    selected = selection.pop("files")
    entries = []
    for directory, dirs, files in os.walk(stage, followlinks=False):
        dirs.sort()
        names = files + [name for name in dirs if (Path(directory) / name).is_symlink()]
        for name in sorted(names):
            candidate = Path(directory) / name
            relative = candidate.relative_to(stage).as_posix()
            if relative == "REVIEW-MANIFEST.json":
                continue
            if candidate.is_symlink():
                body = os.fsencode(os.readlink(candidate))
                kind = "symlink target"
            else:
                body = candidate.read_bytes()
                kind = "file"
            entries.append({"path": relative, "bytes": len(body), "sha256": hashlib.sha256(body).hexdigest(),
                            "kind": kind, "reason": selected.get(relative, "package-time review metadata")})
    actual = {p["path"] for p in entries}
    selection["omissions"].extend({"path": name, "reason": "filtered by packaging rules or --exclude"}
                                   for name in sorted(set(selected) - actual))
    selection["files"] = entries
    selection["selection_time_data_bytes"] = selection["selected_data_bytes"]
    selection["selected_data_bytes"] = sum(entry["bytes"] for entry in entries
        if entry["path"] in selected and not selected[entry["path"]].startswith("source inventory"))
    (stage / "REVIEW-MANIFEST.json").write_text(json.dumps(selection, indent=2) + "\n")
    if selection["selected_data_bytes"] > selection["data_budget_bytes"]:
        raise ValueError("private evidence grew during copying; snapshot exceeds --review-data-mb; rerun or increase the budget")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    prepare = commands.add_parser("select")
    prepare.add_argument("--repo", type=Path, required=True)
    prepare.add_argument("--out", type=Path, required=True)
    prepare.add_argument("--data-mb", type=int, default=32)
    prepare.add_argument("--evidence", action="append", default=[])
    finish = commands.add_parser("manifest")
    finish.add_argument("--stage", type=Path, required=True)
    finish.add_argument("--selection", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "manifest":
        try:
            manifest(args.stage, args.selection)
        except ValueError as error:
            parser.error(str(error))
        return
    if args.data_mb <= 0:
        parser.error("--data-mb must be positive")
    try:
        report = select(args.repo, args.data_mb, args.evidence)
    except ValueError as error:
        parser.error(str(error))
    args.out.mkdir(parents=True, exist_ok=True)
    (args.out / "files.list").write_bytes(b"".join(os.fsencode(name) + b"\0" for name in sorted(report["files"])))
    (args.out / "selection.json").write_text(json.dumps(report, indent=2) + "\n")
    print("review selection: %d source/context files; %d private bytes; budget %d bytes" %
          (len(report["files"]), report["selected_data_bytes"], report["data_budget_bytes"]))


if __name__ == "__main__":
    main()
