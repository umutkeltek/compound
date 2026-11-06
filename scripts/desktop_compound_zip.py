#!/usr/bin/env python3
"""Create a bounded review snapshot without replacing a usable ZIP on failure."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import time
import zipfile

MB = 1_000_000
LOCK_NAME = ".compound-desktop-snapshot.lock"
TEMP_PREFIX = ".compound-snapshot-"
ALWAYS_EXCLUDES = [
    ".DS_Store", "__MACOSX/", "/.claude/", "/.codex/", "/.agents/",
    "/state/", "/.pytest_cache/", "/.ua/", "/.hermes/", "/body/", "/graphify-out/",
]
REVIEW_EXCLUDES = [
    ".git", "__pycache__/", ".venv/", "venv/", ".mypy_cache/", ".ruff_cache/",
    "/compound", "/bin/", "/home/",
    "/docs_private/portfolio/execution/", "/docs_private/plan/product-loop/artifacts/",
    "/docs_private/plan/artifacts/", "/docs_private/plan/packets/",
    "/docs_private/research/state/", "/docs_private/worktrees/",
    "/docs_private/**.presubmit-*/", "/docs_private/**.gates/",
    "/docs_private/**.xcresult/", "/docs_private/**.xcarchive/",
    "/docs_private/**.app/", "/docs_private/**.dSYM/", "/docs_private/**/DerivedData/",
    "/.compound-private/**.xcresult/", "/.compound-private/**.xcarchive/",
    "/.compound-private/**.app/", "/.compound-private/**.dSYM/",
    "/docs_private/worktree-salvage*/", "/docs_private/portfolio/lanes/",
    "/.compound-private/reconciliation/", "/.compound-private/recovery/",
    "/.compound-private/tmp/", "/.compound-private/worktrees/",
]
ARCHIVES = {".zip", ".gz", ".tgz", ".tar", ".bz2", ".xz", ".7z", ".bundle"}
INDEXES = {".npy", ".npz", ".sqlite", ".sqlite3", ".db", ".pyc"}
EXECUTABLE_MAGIC = {
    b"\x7fELF", b"\xfe\xed\xfa\xce", b"\xce\xfa\xed\xfe",
    b"\xfe\xed\xfa\xcf", b"\xcf\xfa\xed\xfe", b"\xca\xfe\xba\xbe",
    b"\xbe\xba\xfe\xca", b"\xca\xfe\xba\xbf", b"\xbf\xba\xfe\xca",
}


class SnapshotError(Exception):
    pass


def git(repo, *args):
    env = os.environ.copy()
    for key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"):
        env.pop(key, None)
    env["GIT_OPTIONAL_LOCKS"] = "0"
    result = subprocess.run(["git", "-C", str(repo), *args], env=env,
                            capture_output=True, timeout=30)
    return result.stdout if result.returncode == 0 else None


def metadata(repo):
    def read(*args):
        value = git(repo, *args)
        return value.decode(errors="replace").strip() if value is not None else None
    upstream = read("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
    return {
        "head": read("rev-parse", "--verify", "HEAD"),
        "branch": read("rev-parse", "--abbrev-ref", "HEAD"),
        "status": read("status", "--porcelain=v1"),
        "upstream": upstream,
        "behind_ahead": read("rev-list", "--left-right", "--count", "@{upstream}...HEAD")
        if upstream else None,
    }


def parser():
    p = argparse.ArgumentParser(
        prog="scripts/desktop-compound-zip.sh",
        description="Create a verified Desktop ZIP for source review (450 decimal MB by default).",
        epilog="""Review mode keeps source, private plans and readable reports, excluding Git history,
generated runtime trees, compiled executables, private archives/indexes and private logs
larger than 1 MB. SNAPSHOT-MANIFEST.json records selection rules and removed files.
This is a review snapshot, not a runtime backup. Full-local retains the older broad
selection. Every mode excludes harness/runtime directories; all modes preserve symbolic
links as links. Existing Desktop outputs are replaced only after integrity and size checks.
Python 3.9+ and rsync are required. No upload is performed.""",
        formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--repo", type=Path, default=Path(__file__).resolve().parent.parent)
    p.add_argument("--desktop", type=Path,
                   default=Path(os.environ.get("COMPOUND_DESKTOP", str(Path.home() / "Desktop"))))
    p.add_argument("--name", default=os.environ.get("COMPOUND_STAGE_NAME", "compound"))
    p.add_argument("--zip", dest="zip_name", default=os.environ.get("COMPOUND_ZIP_NAME", "compound.zip"))
    modes = p.add_mutually_exclusive_group()
    modes.add_argument("--full-local", action="store_true", help="Retain Git history and generated local material.")
    modes.add_argument("--upload-safe", action="store_true", help="Also exclude private overlays and documents.")
    modes.add_argument("--delta", action="store_true", help="Also exclude private overlay and all external memory clones.")
    p.add_argument("--include-private", action="store_true", help="Keep private documents with --upload-safe.")
    p.add_argument("--include-node-modules", action="store_true", help="Keep node_modules and .next.")
    p.add_argument("--keep-memory", action="append", default=[], metavar="NAME")
    p.add_argument("--no-default-memory", action="store_true")
    p.add_argument("--exclude", action="append", default=[], metavar="PATTERN",
                   help="Additional rsync exclude rule; repeatable, / anchors to transfer root.")
    p.add_argument("--max-zip-mb", type=int, default=450, metavar="N",
                   help="Hard limit in decimal MB (1,000,000 bytes); 0 disables. Default: 450.")
    p.add_argument("--dry-run", action="store_true", help="Describe selection without filesystem changes.")
    return p


def simple_name(value):
    return value not in ("", ".", "..") and "/" not in value and "\0" not in value


def validate(args):
    if args.max_zip_mb < 0:
        raise SnapshotError("--max-zip-mb must be non-negative")
    if not simple_name(args.name) or not simple_name(args.zip_name):
        raise SnapshotError("--name and --zip must be simple names")
    if any(name == LOCK_NAME or name.startswith(TEMP_PREFIX) for name in (args.name, args.zip_name)):
        raise SnapshotError("output name is reserved for snapshot coordination")
    if args.name == args.zip_name:
        raise SnapshotError("stage folder and ZIP must have different names")
    source = args.repo.expanduser().resolve()
    desktop = args.desktop.expanduser().resolve()
    if not source.is_dir():
        raise SnapshotError("source repo does not exist: " + str(source))
    stage, output = desktop / args.name, desktop / args.zip_name
    if desktop == source or source in desktop.parents:
        raise SnapshotError("destination must be outside the source repo")
    for target in (stage, output):
        if target == source or target in source.parents or source in target.parents:
            raise SnapshotError("source and output paths overlap")
        if target.is_symlink():
            raise SnapshotError("refusing a symlink output: " + str(target))
    if stage.exists() and not stage.is_dir():
        raise SnapshotError("stage output exists but is not a directory")
    if output.exists() and not output.is_file():
        raise SnapshotError("ZIP output exists but is not a regular file")
    keep = [] if args.no_default_memory else os.environ.get(
        "COMPOUND_KEEP_MEMORY_PROJECTS", "compound").split()
    keep += args.keep_memory
    if any(not simple_name(name) for name in keep):
        raise SnapshotError("invalid --keep-memory name")
    return source, desktop, stage, output, list(dict.fromkeys(keep))


def selection(args, keep):
    excludes = ALWAYS_EXCLUDES + args.exclude
    if not args.full_local:
        excludes += REVIEW_EXCLUDES
    if not args.include_node_modules:
        excludes += ["node_modules/", ".next/"]
    if args.upload_safe:
        if not args.include_private:
            excludes += ["/docs_private/", "/.compound-private/", "/Plans/", "*.private.md"]
    if args.delta:
        excludes += ["/.compound-private/", "/memory/"]
    command = ["rsync", "-a", "--no-devices", "--no-specials"]
    for pattern in excludes:
        command += ["--exclude", pattern]
    if not args.delta:
        command += ["--filter", "+ /memory/"]
        for name in keep:
            command += ["--filter", "+ /memory/" + name + "/***"]
        command += ["--filter", "- /memory/***"]
    return command, excludes


def prune_generated(stage, source, keep_dependencies=False):
    tracked = git(source, "ls-files", "-z")
    # A repository read failure must not turn tracked fixtures into disposable bulk.
    if tracked is None and (source / ".git").exists():
        raise SnapshotError("could not read tracked-file inventory")
    tracked = {os.fsdecode(p) for p in (tracked or b"").split(b"\0") if p}
    omitted = []
    for directory, _, names in os.walk(stage, followlinks=False):
        for name in names:
            path = Path(directory) / name
            relative = path.relative_to(stage)
            if (path.is_symlink() or relative.as_posix() in tracked or not path.is_file()
                    or (keep_dependencies and any(p in ("node_modules", ".next") for p in relative.parts))):
                continue
            size = path.stat().st_size
            private = relative.parts[0] in ("docs_private", ".compound-private")
            reason = None
            if (name == ".env" or name.startswith(".env.")) and name not in (".env.example", ".env.sample", ".env.template"):
                reason = "local environment configuration"
            elif private and path.suffix.lower() in ARCHIVES:
                reason = "private archive or repository bundle"
            elif private and path.suffix.lower() in INDEXES:
                reason = "generated private index or database"
            elif private and path.suffix.lower() == ".log" and size > MB:
                reason = "generated private log over 1 MB"
            else:
                with path.open("rb") as stream:
                    header = stream.read(8)
                if header[:4] in EXECUTABLE_MAGIC or header[:2] == b"MZ" or header == b"!<arch>\n":
                    reason = "untracked compiled executable or object archive"
            if reason:
                omitted.append({"path": relative.as_posix(), "bytes": size, "reason": reason})
                path.unlink()  # Only the isolated staging copy, never the source.
    return sorted(omitted, key=lambda item: item["path"])


def write_generated(path, body):
    # rsync preserves source symlinks; never follow one while generating metadata.
    with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent,
                                     prefix=".snapshot-note-", delete=False) as stream:
        pending = Path(stream.name)
        try:
            stream.write(body)
        except BaseException:
            pending.unlink()
            raise
    try:
        os.replace(pending, path)
    finally:
        if pending.exists():
            pending.unlink()


def audit_notes(source, mode, before, excludes, omitted):
    return """# Compound review snapshot

Generated at {when}. Source: {source}
Mode: {mode}. Source HEAD: {head}. Branch: {branch}.

## Package-time Git state
{status}

Upstream: {upstream}; behind/ahead counts: {counts}.
These describe the source checkout at copy time, not the extracted archive.
Extractors may discard executable permission bits.

## Coverage
This is a source-review snapshot, not a backup or a live runtime-health report.
The external COMPOUND_HOME is not copied. Private plans and readable reports are
included unless excluded by the selected mode or explicit rules. Missing runtime
trees and generated artifacts are packaging choices, not proof that they are absent
from the running system. Source files are not edited by packaging.

SNAPSHOT-MANIFEST.json lists every rsync exclusion rule and each additional file
removed from the staging copy ({removed} files, {bytes} bytes). In review mode,
tracked files are protected from content-based pruning. Symlinks are stored as
links; their external targets are never followed into the ZIP. Sockets, pipes and
special device files are excluded.

Read this note and the manifest before reporting missing files or repository hygiene.
""".format(when=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
           source=source, mode=mode, head=before["head"], branch=before["branch"],
           status=before["status"] or ("Clean." if before["status"] is not None else "Git state unavailable."),
           upstream=before["upstream"], counts=before["behind_ahead"],
           removed=len(omitted), bytes=sum(item["bytes"] for item in omitted))


def write_zip(stage, output, limit):
    count = 0
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED, compresslevel=6, strict_timestamps=False) as archive:
        for directory, dirs, files in os.walk(stage, followlinks=False):
            current = Path(directory)
            archive.write(current, current.relative_to(stage.parent).as_posix())
            for name in sorted(files + [d for d in dirs if (current / d).is_symlink()]):
                path = current / name
                arcname = path.relative_to(stage.parent).as_posix()
                if path.is_symlink():
                    info = zipfile.ZipInfo(arcname)
                    info.create_system = 3
                    info.external_attr = (stat.S_IFLNK | 0o777) << 16
                    archive.writestr(info, os.fsencode(os.readlink(path)))
                elif path.is_file():
                    archive.write(path, arcname)
                else:
                    raise SnapshotError("unsupported staged file type: " + arcname)
                count += 1
                if limit and archive.fp.tell() > limit:
                    raise SnapshotError("candidate exceeds the ZIP budget during compression")
    size = output.stat().st_size
    if limit and size > limit:
        raise SnapshotError("finished ZIP is over the byte budget")
    with zipfile.ZipFile(output) as archive:
        if archive.testzip() is not None:
            raise SnapshotError("ZIP integrity check failed")
    return count, size


def publish(stage, archive, stage_target, zip_target, backup):
    """Publish the ZIP atomically; roll the stage back if publication fails."""
    previous = stage_target.exists()
    stage_installed = False
    blocked = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGINT, signal.SIGTERM, signal.SIGHUP})
    try:
        if previous:
            os.replace(stage_target, backup)
        try:
            os.replace(stage, stage_target)
            stage_installed = True
            os.replace(archive, zip_target)  # Last operation: old ZIP survives until here.
        except BaseException:
            if stage_installed:
                shutil.rmtree(stage_target)
            if previous:
                os.replace(backup, stage_target)
            raise
    finally:
        signal.pthread_sigmask(signal.SIG_SETMASK, blocked)


def largest_files(stage):
    files = []
    for directory, _, names in os.walk(stage, followlinks=False):
        for name in names:
            path = Path(directory) / name
            if path.is_file() and not path.is_symlink():
                files.append((path.stat().st_size, path.relative_to(stage).as_posix()))
    return sorted(files, reverse=True)[:15]


def run(args):
    source, desktop, stage_target, zip_target, keep = validate(args)
    mode = "full-local" if args.full_local else "upload-safe" if args.upload_safe else "delta" if args.delta else "review"
    command, excludes = selection(args, keep)
    limit = args.max_zip_mb * MB
    print(json.dumps({"source": str(source), "stage": str(stage_target), "zip": str(zip_target),
                      "mode": mode, "budget_bytes": limit, "excludes": excludes,
                      "keep_memory": keep, "dry_run": args.dry_run}, indent=2), flush=True)
    if args.dry_run:
        return
    if not shutil.which("rsync"):
        raise SnapshotError("rsync is required")
    desktop.mkdir(parents=True, exist_ok=True)
    lock = desktop / LOCK_NAME
    try:
        lock.mkdir()
    except FileExistsError:
        raise SnapshotError("another snapshot holds " + str(lock)) from None
    try:
        (lock / "owner.json").write_text(json.dumps({"pid": os.getpid(), "source": str(source)}))
        with tempfile.TemporaryDirectory(prefix=TEMP_PREFIX, dir=desktop) as scratch:
            temp = Path(scratch)
            stage = temp / "candidate" / args.name
            stage.mkdir(parents=True)
            archive = temp / "archive" / args.zip_name
            archive.parent.mkdir()
            before = metadata(source)
            subprocess.run(command + [str(source) + "/", str(stage) + "/"], check=True)
            if metadata(source) != before:
                raise SnapshotError("source Git state changed while copying; rerun the snapshot")
            omitted = [] if args.full_local else prune_generated(stage, source, args.include_node_modules)
            manifest = {"schema": "compound.review_snapshot.v1", "mode": mode,
                        "source": str(source), "source_git": before,
                        "budget_bytes": limit, "rsync_excludes": excludes,
                        "packager_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                        "kept_memory_projects": [] if args.delta else keep,
                        "special_files_excluded": True,
                        "omitted_staged_files": omitted}
            write_generated(stage / "SNAPSHOT-MANIFEST.json", json.dumps(manifest, indent=2) + "\n")
            write_generated(stage / "AUDIT-NOTES.md", audit_notes(source, mode, before, excludes, omitted))
            try:
                count, size = write_zip(stage, archive, limit)
            except SnapshotError:
                print("Largest retained files:", file=sys.stderr)
                for size, name in largest_files(stage):
                    print("  %.2f MB %s" % (size / MB, name), file=sys.stderr)
                raise
            digest = hashlib.sha256()
            with archive.open("rb") as stream:
                for block in iter(lambda: stream.read(MB), b""):
                    digest.update(block)
            # Recheck destinations under the lock immediately before publication.
            validate(args)
            publish(stage, archive, stage_target, zip_target, temp / "previous-stage")
            print(json.dumps({"status": "verified", "zip": str(zip_target), "zip_bytes": size,
                              "zip_mb": round(size / MB, 3), "sha256": digest.hexdigest(),
                              "files": count, "omitted_generated_files": len(omitted),
                              "omitted_generated_bytes": sum(x["bytes"] for x in omitted),
                              "integrity": "CRC passed"}, indent=2), flush=True)
    finally:
        shutil.rmtree(lock)


def main():
    args = parser().parse_args()

    def stop(signum, _frame):
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGHUP, stop)
    try:
        run(args)
        return 0
    except (SnapshotError, OSError, subprocess.SubprocessError, zipfile.BadZipFile) as exc:
        print("desktop-compound-zip: %s; existing outputs preserved unless publication completed." % exc,
              file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
