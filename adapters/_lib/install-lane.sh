#!/usr/bin/env sh
# adapters/_lib/install-lane.sh
#
# Unified installer for any Claude-flavored lane. Calls `compound hook install`
# to write the harness-authority hooks, then merges the lane-themed statusline
# into the profile's settings.json, then symlinks global steering.
#
# Usage:
#   install-lane.sh --lane <id> [--profile-dir DIR] [--compound PATH]
#                          [--steering-source FILE] [--hook-registration-ts FILE]
#
# Defaults:
#   --profile-dir        $CLAUDE_CONFIG_DIR OR
#                        <lanes.json profile_default> for the lane
#   --compound           $HOME/.local/bin/compound
#   --steering-source    $HOME/.claude/CLAUDE.md
#   --hook-registration-ts   sibling ../hook-registration.ts if present
#
# Idempotent: re-running for the same lane is a no-op; re-running with a
# different lane rewires the statusline command.

set -eu

lane=""
profile_dir=""
compound_bin=""
steering_source=""
hook_reg=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --lane)                lane=$2; shift 2 ;;
    --profile-dir)         profile_dir=$2; shift 2 ;;
    --compound)            compound_bin=$2; shift 2 ;;
    --steering-source)     steering_source=$2; shift 2 ;;
    --hook-registration-ts) hook_reg=$2; shift 2 ;;
    -h|--help)
      echo "usage: install-lane.sh --lane ID [--profile-dir DIR] [--compound PATH] [--steering-source FILE] [--hook-registration-ts FILE]" >&2
      exit 0 ;;
    *) echo "install-lane: unknown arg: $1" >&2; exit 2 ;;
  esac
done

if [ -z "$lane" ]; then
  echo "install-lane: --lane is required" >&2
  exit 2
fi
if [ -z "${HOME:-}" ]; then echo "install-lane: HOME is required" >&2; exit 1; fi

_lib_dir=$(CDPATH= dirname -- "${INSTALL_LANE_FILE:-$0}")
LANES_SH_FILE="$_lib_dir/lanes.sh"
. "$LANES_SH_FILE"

# Resolve lane metadata.
eval "$(lane_emit_sh "$lane")"
if [ -z "${LANE_ID:-}" ]; then
  echo "install-lane: unknown lane: $lane" >&2
  exit 2
fi

if [ -z "$profile_dir" ]; then
  if [ -n "${CLAUDE_CONFIG_DIR:-}" ]; then
    profile_dir=$CLAUDE_CONFIG_DIR
  else
    eval "profile_dir=\"$LANE_PROFILE_DEFAULT\""
  fi
fi
if [ -z "$compound_bin" ]; then
  compound_bin=$HOME/.local/bin/compound
fi
if [ -z "$steering_source" ]; then
  steering_source=$HOME/.claude/CLAUDE.md
fi
if [ -z "$hook_reg" ]; then
  for _candidate in "$_lib_dir/../$LANE_ID" "$_lib_dir/../claude-$LANE_ID"; do
    if [ -f "$_candidate/hook-registration.ts" ]; then
      hook_reg="$_candidate/hook-registration.ts"
      break
    fi
  done
fi

if [ ! -x "$compound_bin" ]; then
  echo "install-lane: compound binary not executable: $compound_bin" >&2
  exit 1
fi
if [ ! -f "$steering_source" ]; then
  echo "install-lane: global steering source missing: $steering_source" >&2
  exit 1
fi
if [ -z "$hook_reg" ] || [ ! -f "$hook_reg" ]; then
  echo "install-lane: hook-registration.ts not found for lane '$lane' (looked near $_lib_dir/../)" >&2
  exit 1
fi

mkdir -p "$profile_dir"

# Every lane that reaches install-lane.sh is a claude_code-family isolated
# profile (its own --config-dir). The hook ROSTER for any such profile is
# claude-zai's full context/guard/ownership set -- that is what the
# hand-written adapters/claude-zai/install.sh installs, and what
# `compound hook install` accepts with --config-dir. Passing the raw lane
# id (qwen/minimax/...) here used to fail: cmdHookInstall's allowlist is
# claude-code|claude-zai|codex|all, and --config-dir is gated to
# claude-zai. The lane id still identifies the profile (via --config-dir
# and the statusline); claude-zai identifies the hook authority source.
"$compound_bin" hook install --harness claude-zai --config-dir "$profile_dir" --binary "$compound_bin"

LANES_SH_FILE="$_lib_dir/lanes.sh" \
LANE_STATUSLINE_FILE="$_lib_dir/lane-statusline.sh" \
  sh "$_lib_dir/statusline-install.sh" --profile-dir "$profile_dir" --lane "$lane"

_steering_target="$profile_dir/CLAUDE.md"
if [ -e "$_steering_target" ] && [ ! -L "$_steering_target" ]; then
  echo "install-lane: refusing to replace non-symlink steering: $_steering_target" >&2
  exit 1
fi
if [ -L "$_steering_target" ]; then
  _curr=$(readlink "$_steering_target")
  if [ "$_curr" != "$steering_source" ]; then
    ln -sfn "$steering_source" "$_steering_target"
  fi
else
  ln -s "$steering_source" "$_steering_target"
fi
unset _steering_target _curr _candidate

echo "lane '$lane' profile installed"
echo "- settings: $profile_dir/settings.json"
echo "- steering: symlinked to $steering_source"
echo "- authority: $hook_reg"
echo "- statusline: $_lib_dir/lane-statusline.sh --lane $lane"

