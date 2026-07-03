#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$repo_root"

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
go build -o "$install_dir/compound" ./cmd/compound

PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" home init --home "$compound_home" --global
PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" home doctor --home "$compound_home"
PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" runtime doctor "$repo_root/fixtures/runtime/synthetic-service.yaml"

cat <<EOF

compound installed
- binary: $install_dir/compound
- home: $compound_home

next:
  compound workbench
  compound proof-check --json
  cd console && bun install && bun run dev
EOF
