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
install_os_alias=${COMPOUND_INSTALL_OS_ALIAS:-0}
case "$install_os_alias" in
  0|1) ;;
  *)
    echo "install: COMPOUND_INSTALL_OS_ALIAS must be 0 or 1" >&2
    exit 1
    ;;
esac
install_autonomy_lab=${COMPOUND_INSTALL_AUTONOMY_LAB:-0}
case "$install_autonomy_lab" in
  0|1) ;;
  *)
    echo "install: COMPOUND_INSTALL_AUTONOMY_LAB must be 0 or 1" >&2
    exit 1
    ;;
esac
if [ "$install_os_alias" = 1 ]; then
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
fi
build_sha=$(git -C "$repo_root" rev-parse --short HEAD 2>/dev/null || echo "")

# Build to a staging path, prove the artifact, and only then move it into place. Installing
# first and verifying after would leave a half-installed artifact behind on failure — and the
# lifecycle hooks shell out to that path, so a bad artifact there fails every policy gate OPEN
# with no signal. `set -e` plus this trap means a failed verification replaces nothing.
staged=$install_dir/.compound.incoming.$$
alias_staged=$install_dir/.os.incoming.$$
heartbeat_staged=$install_dir/.compound-autonomy-heartbeat.incoming.$$
attention_staged=$compound_home/scripts/.attention-letter.incoming.$$
trap 'rm -f "$staged" "$alias_staged" "$heartbeat_staged" "$attention_staged"' EXIT HUP INT TERM
go build -ldflags "-X main.buildSHA=$build_sha" -o "$staged" ./cmd/compound
go run ./cmd/compound-install-verify --binary "$staged" --build-sha "$build_sha"
mv -f "$staged" "$install_dir/compound"
if [ "$install_os_alias" = 1 ]; then
  ln -s compound "$alias_staged"
  mv -f "$alias_staged" "$os_alias"
fi
if [ "$install_autonomy_lab" = 1 ]; then
  cp "$repo_root/scripts/compound-autonomy-heartbeat.sh" "$heartbeat_staged"
  chmod 0755 "$heartbeat_staged"
  sh -n "$heartbeat_staged"
  mv -f "$heartbeat_staged" "$install_dir/compound-autonomy-heartbeat.sh"
fi

go build -o "$install_dir/compound-breadth-engine" ./cmd/compound-breadth-engine

PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" home init --home "$compound_home" --global
if [ "$install_autonomy_lab" = 1 ]; then
  mkdir -p "$compound_home/scripts"
  cp "$repo_root/scripts/attention-letter.py" "$attention_staged"
  chmod 0755 "$attention_staged"
  python3 -c 'import ast,sys; ast.parse(open(sys.argv[1]).read())' "$attention_staged"
  mv -f "$attention_staged" "$compound_home/scripts/attention-letter.py"
fi
PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" home doctor --home "$compound_home"
PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" runtime doctor "$repo_root/fixtures/runtime/synthetic-service.yaml"

cat <<EOF

compound installed
- binary: $install_dir/compound
- breadth_engine: $install_dir/compound-breadth-engine
- os_alias: opt-in via COMPOUND_INSTALL_OS_ALIAS=1 (existing path left unchanged otherwise)
- autonomy_lab: opt-in via COMPOUND_INSTALL_AUTONOMY_LAB=1
- autonomy_heartbeat: $install_dir/compound-autonomy-heartbeat.sh (when opted in)
- attention_letter: $compound_home/scripts/attention-letter.py (when opted in; max 3 decisions per cycle)
- home: $compound_home

next:
  compound workbench
  compound proof-check --json
  cd console && bun install
  compound console --operator
EOF
