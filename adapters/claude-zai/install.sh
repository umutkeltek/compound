#!/usr/bin/env sh
set -eu

adapter_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
profile_dir=""
compound_bin=""
steering_source=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --profile-dir)
      profile_dir=$2
      shift 2
      ;;
    --compound)
      compound_bin=$2
      shift 2
      ;;
    --steering-source)
      steering_source=$2
      shift 2
      ;;
    *)
      echo "usage: adapters/claude-zai/install.sh [--profile-dir DIR] [--compound PATH] [--steering-source FILE]" >&2
      exit 2
      ;;
  esac
done

if [ -z "${HOME:-}" ]; then
  echo "claude-zai install: HOME is required" >&2
  exit 1
fi
if [ -z "$profile_dir" ]; then
  profile_dir=${CLAUDE_CONFIG_DIR:-"$HOME/.config/switchback/claude/zai-direct"}
fi
if [ -z "$compound_bin" ]; then
  compound_bin=$HOME/.local/bin/compound
fi
if [ -z "$steering_source" ]; then
  steering_source=$HOME/.claude/CLAUDE.md
fi
if [ ! -x "$compound_bin" ]; then
  echo "claude-zai install: Compound binary is not executable: $compound_bin" >&2
  exit 1
fi
if [ ! -f "$steering_source" ]; then
  echo "claude-zai install: global steering source is missing: $steering_source" >&2
  exit 1
fi

mkdir -p "$profile_dir"
"$compound_bin" hook install --harness claude-zai --config-dir "$profile_dir" --binary "$compound_bin"

steering_target=$profile_dir/CLAUDE.md
if [ -e "$steering_target" ] && [ ! -L "$steering_target" ]; then
  echo "claude-zai install: refusing to replace non-symlink steering: $steering_target" >&2
  exit 1
fi
if [ -L "$steering_target" ]; then
  current_target=$(readlink "$steering_target")
  if [ "$current_target" != "$steering_source" ]; then
    ln -sfn "$steering_source" "$steering_target"
  fi
else
  ln -s "$steering_source" "$steering_target"
fi

echo "claude-zai profile installed"
echo "- settings: $profile_dir/settings.json"
echo "- steering: $steering_target -> $steering_source"
echo "- authority source: $adapter_dir/hook-registration.ts"
