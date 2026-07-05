#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: scripts/desktop-compound-zip.sh [options]

Create a Desktop snapshot zip from the current compound checkout:
  1. remove existing ~/Desktop/compound and ~/Desktop/compound.zip
  2. copy the compound repo to ~/Desktop/compound
  3. remove top-level external memory projects except the keep list
  4. zip ~/Desktop/compound.zip

Default mode is for a ChatGPT Pro source audit:
  - keeps scripts, tools, console source, private docs, .claude, .git,
    root binaries, and other local audit material
  - excludes dependency/build artifacts: node_modules, .next
  - removes Finder/system junk: .DS_Store, __MACOSX
  - removes top-level memory/* external clones except names in the keep list

Use --upload-safe for the smaller public-audit bundle:
  - also excludes .git, node_modules, .next, root binary, bin/, home/,
    docs_private/, .compound-private/, .claude/, Plans/, and *.private.md

Options:
  --repo PATH              Source repo. Default: git root for this script.
  --desktop PATH           Destination desktop directory. Default: ~/Desktop.
  --name NAME              Staged folder name. Default: compound.
  --zip NAME               Zip filename. Default: compound.zip.
  --upload-safe            Produce the smaller public-audit bundle.
  --include-node-modules   Keep dependency/build artifacts in the local snapshot.
  --include-private        With --upload-safe, keep private docs and .claude.
  --keep-memory NAME       Keep top-level memory/NAME. Can be repeated.
                           Default keep list: compound.
  --no-default-memory      Clear the default keep-memory list.
  --dry-run                Print actions without deleting, copying, or zipping.
  -h, --help               Show this help.

Environment:
  COMPOUND_KEEP_MEMORY_PROJECTS="name other-name" overrides the default keep list.

Notes:
  Memory pruning only applies to top-level memory/* external clones. It does not
  remove Compound source surfaces like internal/mem, docs/memory-architecture.md,
  prototype-ts/src/memory, or console memory pages.
USAGE
}

die() {
  printf 'desktop-compound-zip: %s\n' "$*" >&2
  exit 1
}

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel 2>/dev/null || true)"
if [[ -z "$repo_root" ]]; then
  repo_root="$(cd "$script_dir/.." && pwd -P)"
fi

repo_path="$repo_root"
desktop_path="${COMPOUND_DESKTOP:-$HOME/Desktop}"
stage_name="${COMPOUND_STAGE_NAME:-compound}"
zip_name="${COMPOUND_ZIP_NAME:-compound.zip}"
upload_safe=0
include_private=1
include_node_modules=0
dry_run=0

keep_memory=()
if [[ -n "${COMPOUND_KEEP_MEMORY_PROJECTS:-}" ]]; then
  # shellcheck disable=SC2206
  keep_memory=(${COMPOUND_KEEP_MEMORY_PROJECTS})
else
  keep_memory=("compound")
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      [[ $# -ge 2 ]] || die "--repo requires a path"
      repo_path="$2"
      shift 2
      ;;
    --desktop)
      [[ $# -ge 2 ]] || die "--desktop requires a path"
      desktop_path="$2"
      shift 2
      ;;
    --name)
      [[ $# -ge 2 ]] || die "--name requires a folder name"
      stage_name="$2"
      shift 2
      ;;
    --zip)
      [[ $# -ge 2 ]] || die "--zip requires a file name"
      zip_name="$2"
      shift 2
      ;;
    --upload-safe)
      upload_safe=1
      include_private=0
      shift
      ;;
    --include-node-modules)
      include_node_modules=1
      shift
      ;;
    --include-private)
      include_private=1
      shift
      ;;
    --keep-memory)
      [[ $# -ge 2 ]] || die "--keep-memory requires a top-level memory directory name"
      keep_memory+=("$2")
      shift 2
      ;;
    --no-default-memory)
      keep_memory=()
      shift
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

[[ "$stage_name" != "" ]] || die "--name cannot be empty"
[[ "$zip_name" != "" ]] || die "--zip cannot be empty"
[[ "$stage_name" != "." && "$stage_name" != ".." && "$stage_name" != */* ]] || die "--name must be a simple folder name"
[[ "$zip_name" != "." && "$zip_name" != ".." && "$zip_name" != */* ]] || die "--zip must be a simple file name"
[[ -d "$repo_path" ]] || die "repo path does not exist: $repo_path"

command -v rsync >/dev/null 2>&1 || die "rsync is required"
command -v zip >/dev/null 2>&1 || die "zip is required"

repo_real="$(cd "$repo_path" && pwd -P)"
mkdir -p "$desktop_path"
desktop_real="$(cd "$desktop_path" && pwd -P)"
stage_path="$desktop_real/$stage_name"
zip_path="$desktop_real/$zip_name"

[[ "$repo_real" != "$stage_path" ]] || die "source repo and Desktop stage resolve to the same path"
[[ "$stage_path" == "$desktop_real/"* ]] || die "stage path must stay under desktop"
[[ "$zip_path" == "$desktop_real/"* ]] || die "zip path must stay under desktop"

run() {
  printf '+'
  printf ' %q' "$@"
  printf '\n'
  if [[ "$dry_run" -eq 0 ]]; then
    "$@"
  fi
}

rsync_args=(
  -a
  --delete
  --exclude ".DS_Store"
  --exclude "__MACOSX/"
  --filter "+ /memory/"
)

for name in "${keep_memory[@]}"; do
  [[ -n "$name" && "$name" != "." && "$name" != ".." && "$name" != */* ]] || die "invalid keep-memory name: $name"
  rsync_args+=(--filter "+ /memory/$name/***")
done
rsync_args+=(--filter "- /memory/***")

if [[ "$include_node_modules" -eq 0 ]]; then
  rsync_args+=(
    --exclude "node_modules/"
    --exclude ".next/"
  )
fi

if [[ "$upload_safe" -eq 1 ]]; then
  rsync_args+=(
    --exclude ".git/"
    --exclude "/compound"
    --exclude "/bin/"
    --exclude "/home/"
  )
  if [[ "$include_private" -eq 0 ]]; then
    rsync_args+=(
      --exclude "/docs_private/"
      --exclude "/.compound-private/"
      --exclude "/.claude/"
      --exclude "/Plans/"
      --exclude "*.private.md"
    )
  fi
fi

printf '# compound Desktop snapshot zip\n'
printf -- '- source: %s\n' "$repo_real"
printf -- '- stage: %s\n' "$stage_path"
printf -- '- zip: %s\n' "$zip_path"
printf -- '- mode: %s\n' "$([[ "$upload_safe" -eq 1 ]] && printf upload-safe || printf full-local)"
printf -- '- include_private: %s\n' "$include_private"
printf -- '- include_node_modules: %s\n' "$include_node_modules"
printf -- '- keep_memory: %s\n' "${keep_memory[*]:-(none)}"

run rm -rf -- "$stage_path" "$zip_path"
run mkdir -p -- "$stage_path"
run rsync "${rsync_args[@]}" "$repo_real/" "$stage_path/"

if [[ "$dry_run" -eq 0 && -d "$stage_path/memory" ]]; then
  if ! find "$stage_path/memory" -mindepth 1 -print -quit | grep -q .; then
    rmdir "$stage_path/memory"
  fi
fi

if [[ "$dry_run" -eq 0 ]]; then
  find "$stage_path" -name ".DS_Store" -delete
  (
    cd "$desktop_real"
    COPYFILE_DISABLE=1 zip -qr "$zip_path" "$stage_name"
  )
  file_count="$(find "$stage_path" -type f | wc -l | tr -d ' ')"
  zip_bytes="$(wc -c < "$zip_path" | tr -d ' ')"
  printf -- '- files: %s\n' "$file_count"
  printf -- '- zip_bytes: %s\n' "$zip_bytes"
  printf -- '- done: %s\n' "$zip_path"
else
  printf -- '- dry_run: no files changed\n'
fi
