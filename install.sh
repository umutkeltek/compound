#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$repo_root"

"$repo_root/scripts/sync-ceremony-overlays.sh" "$repo_root"

if [ -n "${GOBIN:-}" ]; then
  install_dir=$GOBIN
else
  if [ -z "${HOME:-}" ]; then
    echo "install: HOME is required when GOBIN is not set" >&2
    exit 1
  fi
  install_dir=$HOME/.local/bin
fi

if [ -z "${COMPOUND_HOME:-}" ]; then
  if [ -z "${HOME:-}" ]; then
    echo "install: COMPOUND_HOME or HOME is required" >&2
    exit 1
  fi
  compound_home=$HOME/.compound
else
  compound_home=$COMPOUND_HOME
fi

mkdir -p "$install_dir"

os_alias=$install_dir/os
if [ -L "$os_alias" ]; then
  os_alias_target=$(readlink "$os_alias")
  case "$os_alias_target" in
    compound|"$install_dir/compound") ;;
    *)
      echo "install: refusing to replace foreign os symlink: $os_alias -> $os_alias_target" >&2
      exit 1
      ;;
  esac
elif [ -e "$os_alias" ]; then
  if [ ! -f "$os_alias" ]; then
    echo "install: refusing to replace non-file os path: $os_alias" >&2
    exit 1
  fi
  os_alias_body=$(cat "$os_alias")
  case "$os_alias_body" in
    '#!/bin/sh'*'shim only'*'operating-system/umut-os/exec-lane/os.ts'*) ;;
    *)
      echo "install: refusing to replace foreign os file: $os_alias" >&2
      exit 1
      ;;
  esac
fi
build_sha=$(git -C "$repo_root" rev-parse --short HEAD 2>/dev/null || echo "")

# Build to a staging path, prove the artifact, and only then move it into place. Installing
# first and verifying after would leave a half-installed artifact behind on failure — and the
# lifecycle hooks shell out to that path, so a bad artifact there fails every policy gate OPEN
# with no signal. `set -e` plus this trap means a failed verification replaces nothing.
staged=$install_dir/.compound.incoming.$$
alias_staged=$install_dir/.os.incoming.$$
trap 'rm -f "$staged" "$alias_staged"' EXIT HUP INT TERM
go build -ldflags "-X main.buildSHA=$build_sha" -o "$staged" ./cmd/compound
go run ./cmd/compound-install-verify --binary "$staged" --build-sha "$build_sha"
mv -f "$staged" "$install_dir/compound"
ln -s compound "$alias_staged"
mv -f "$alias_staged" "$os_alias"

go build -o "$install_dir/compound-breadth-engine" ./cmd/compound-breadth-engine

PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" home init --home "$compound_home" --global
PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" home doctor --home "$compound_home"
PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" runtime doctor "$repo_root/fixtures/runtime/synthetic-service.yaml"

cat <<EOF

compound installed
- binary: $install_dir/compound
- breadth_engine: $install_dir/compound-breadth-engine
- os_alias: $os_alias -> compound
- home: $compound_home

next:
  compound workbench
  compound proof-check --json
  cd console && bun install
  compound console --operator
EOF
