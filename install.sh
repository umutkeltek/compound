#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
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
install_dir=$(CDPATH='' cd -- "$install_dir" && pwd -P)
case "$compound_home" in /*) ;; *) compound_home=$repo_root/$compound_home ;; esac
# Recompute the alias after resolving a relative GOBIN.
os_alias=$install_dir/os
umask 077

fail() { echo "install: $*" >&2; exit 1; }
[ -n "${HOME:-}" ] || fail "HOME is required for installed profile refresh"

# Share the immutable release installer's persistent kernel lock. Source installs
# never interpret or recover its journal; only that existing owner may do so.
state_root=$install_dir/.compound-install-state
case "$(uname -s)" in
  Darwin) stat_mode() { stat -f '%Lp' "$1"; }; stat_uid() { stat -f '%u' "$1"; }; stat_links() { stat -f '%l' "$1"; } ;;
  Linux) stat_mode() { stat -c '%a' "$1"; }; stat_uid() { stat -c '%u' "$1"; }; stat_links() { stat -c '%h' "$1"; } ;;
  *) fail "source installation requires Darwin or Linux install locking" ;;
esac

# Every step that can wait on something outside this script (the install
# verifier, codesign, security) runs under a time limit, so a system prompt
# nobody answers fails the install instead of hanging it.
step_cap=${COMPOUND_INSTALL_STEP_TIMEOUT:-120}
case "$step_cap" in ''|*[!0-9]*) fail "COMPOUND_INSTALL_STEP_TIMEOUT must be a whole number of seconds" ;; esac
[ "$step_cap" -gt 0 ] || fail "COMPOUND_INSTALL_STEP_TIMEOUT must be at least 1 second"
cap_bin=$(command -v timeout 2>/dev/null || command -v gtimeout 2>/dev/null || :)
run_capped() {
  run_capped_seconds=$1
  shift
  if [ -n "$cap_bin" ]; then
    "$cap_bin" "$run_capped_seconds" "$@"
  elif command -v perl >/dev/null 2>&1; then
    perl -e 'alarm shift @ARGV; exec { $ARGV[0] } @ARGV; die "cannot start $ARGV[0]: $!\n"' "$run_capped_seconds" "$@"
  else
    "$@"
  fi
}
# Only used before activation: a failure here leaves nothing published.
step_failed() {
  case "$2" in
    124|137|142) fail "$1 did not finish within ${step_cap}s (a system prompt may be waiting on screen; answer it only if you expected it); nothing was published" ;;
  esac
  fail "$1 failed (exit $2); nothing was published"
}
run_step() {
  run_step_name=$1
  shift
  if run_capped "$step_cap" "$@"; then return 0; else step_failed "$run_step_name" "$?"; fi
}

# Optional fixed code-signing identity (macOS). macOS ties a privacy grant
# (an external volume, the local network, ...) given to an ad-hoc signed
# program to that one build, so after every install the program is new to it
# and asks again. Signing every published Mach-O program with one identity and
# a fixed identifier keeps the grant across rebuilds. The environment wins when
# it names the identity; otherwise a private KEY=value file is read when it
# exists. Unset, or identity "-", keeps the linker's ad-hoc signature.
sign_identity=${COMPOUND_INSTALL_SIGN_IDENTITY:-}
sign_keychain=${COMPOUND_INSTALL_SIGN_KEYCHAIN:-}
sign_password_file=${COMPOUND_INSTALL_SIGN_KEYCHAIN_PASSWORD_FILE:-}
sign_config=${COMPOUND_INSTALL_SIGN_CONFIG:-$compound_home/config/install-signing.conf}
sign_source=environment
private_file() {
  [ -f "$1" ] && [ ! -L "$1" ] && [ "$(stat_uid "$1")" = "$(id -u)" ] &&
    case "$(stat_mode "$1")" in 600|400) true ;; *) false ;; esac
}
if [ -z "$sign_identity" ] && { [ -e "$sign_config" ] || [ -L "$sign_config" ]; }; then
  private_file "$sign_config" || fail "signing settings $sign_config must be a regular file you own with mode 600"
  sign_source=$sign_config
  sign_keychain=
  sign_password_file=
  while IFS= read -r sign_line || [ -n "$sign_line" ]; do
    case "$sign_line" in ''|'#'*) continue ;; *=*) ;; *) fail "signing settings line is not KEY=value in $sign_config" ;; esac
    sign_value=${sign_line#*=}
    case "${sign_line%%=*}" in
      COMPOUND_INSTALL_SIGN_IDENTITY) sign_identity=$sign_value ;;
      COMPOUND_INSTALL_SIGN_KEYCHAIN) sign_keychain=$sign_value ;;
      COMPOUND_INSTALL_SIGN_KEYCHAIN_PASSWORD_FILE) sign_password_file=$sign_value ;;
      *) fail "unknown signing setting ${sign_line%%=*} in $sign_config" ;;
    esac
  done <"$sign_config"
fi
if [ "$sign_identity" = - ]; then sign_identity=; fi
if [ -n "$sign_identity" ]; then
  [ "$(uname -s)" = Darwin ] || fail "code signing (settings: $sign_source) needs macOS codesign; leave COMPOUND_INSTALL_SIGN_IDENTITY unset on this host"
  case "$sign_identity" in *[!0-9A-Fa-f]*) fail "signing identity (settings: $sign_source) must be a certificate SHA-1 (40 hex digits)" ;; esac
  [ "${#sign_identity}" = 40 ] || fail "signing identity (settings: $sign_source) must be a certificate SHA-1 (40 hex digits)"
  command -v codesign >/dev/null 2>&1 || fail "code signing needs codesign on PATH"
  [ -n "$cap_bin" ] || command -v perl >/dev/null 2>&1 || fail "code signing needs timeout or perl to bound codesign"
  if [ -n "$sign_keychain" ]; then
    case "$sign_keychain" in /*) ;; *) fail "signing keychain (settings: $sign_source) must be an absolute path" ;; esac
    [ -f "$sign_keychain" ] || fail "signing keychain $sign_keychain is not a file"
  fi
  if [ -n "$sign_password_file" ]; then
    [ -n "$sign_keychain" ] || fail "a keychain password file (settings: $sign_source) needs COMPOUND_INSTALL_SIGN_KEYCHAIN"
    case "$sign_password_file" in /*) ;; *) fail "keychain password file (settings: $sign_source) must be an absolute path" ;; esac
    private_file "$sign_password_file" || fail "keychain password file $sign_password_file must be a regular file you own with mode 600"
    command -v security >/dev/null 2>&1 || fail "unlocking the signing keychain needs security on PATH"
  fi
fi
sign_kind=
# Sign one staged program, then prove the result: the signature verifies and
# its designated requirement names the identity, not this build's hash.
sign_staged() {
  sign_file=$1
  sign_id=$2
  if [ -n "$sign_keychain" ]; then
    run_step "codesign of $sign_id" codesign --force --sign "$sign_identity" --keychain "$sign_keychain" --identifier "$sign_id" --timestamp=none "$sign_file"
  else
    run_step "codesign of $sign_id" codesign --force --sign "$sign_identity" --identifier "$sign_id" --timestamp=none "$sign_file"
  fi
  run_step "signature check of $sign_id" codesign --verify --strict "$sign_file"
  sign_requirement=$(run_capped "$step_cap" codesign -d -r- "$sign_file" 2>/dev/null) || step_failed "reading the designated requirement of $sign_id" "$?"
  case "$sign_requirement" in
    *cdhash*) fail "$sign_id is still bound to this one build (designated requirement is cdhash-based); nothing was published" ;;
    *"identifier $sign_id "*certificate*|*"identifier \"$sign_id\""*certificate*) sign_kind=identity-based ;;
    *) fail "$sign_id has no identity-based designated requirement ($sign_requirement); nothing was published" ;;
  esac
}
unlock_signing_keychain() {
  [ -n "$sign_password_file" ] || return 0
  if run_capped "$step_cap" security unlock-keychain "$sign_keychain" <"$sign_password_file" >"$stage/unlock.out" 2>&1; then return 0; else unlock_status=$?; fi
  sed 's/^/  /' "$stage/unlock.out" >&2
  step_failed "unlocking the signing keychain" "$unlock_status"
}

if [ ! -e "$state_root" ] && [ ! -L "$state_root" ]; then mkdir -m 0700 "$state_root"; fi
[ -d "$state_root" ] && [ ! -L "$state_root" ] &&
  [ "$(stat_mode "$state_root")" = 700 ] &&
  [ "$(stat_uid "$state_root")" = "$(id -u)" ] || fail "install state is not a private owned directory"
lock_file=$state_root/lock
if [ ! -e "$lock_file" ] && [ ! -L "$lock_file" ]; then
  for entry in "$state_root"/* "$state_root"/.[!.]* "$state_root"/..?*; do
    if [ -e "$entry" ] || [ -L "$entry" ]; then fail "release recovery state has no lock; use the release installer"; fi
  done
  (set -C; : > "$lock_file") 2>/dev/null || :
fi
[ -f "$lock_file" ] && [ ! -L "$lock_file" ] &&
  [ "$(stat_mode "$lock_file")" = 600 ] &&
  [ "$(stat_uid "$lock_file")" = "$(id -u)" ] &&
  [ "$(stat_links "$lock_file")" = 1 ] || fail "install lock is not a private owned regular file"
exec 9>"$lock_file"
case "$(uname -s)" in
  Darwin) lockf -s -t 0 9 || fail "another installer holds the install lock" ;;
  Linux) flock -n 9 || fail "another installer holds the install lock" ;;
esac
for entry in "$state_root"/* "$state_root"/.[!.]* "$state_root"/..?*; do
  [ "$entry" = "$lock_file" ] && continue
  if [ -e "$entry" ] || [ -L "$entry" ]; then fail "pending release recovery state; run the immutable release installer first"; fi
done
# A killed process cannot run a trap. Preserve its retained bytes and refuse a
# new install until the operator resolves that interrupted source installation.
# A stage that never left the build phase holds only freshly built files (no
# preimage, nothing activated), so it is removed instead of blocking every install.
build_only_stage() {
  [ -d "$1" ] && [ ! -L "$1" ] && [ -f "$1/phase" ] && [ ! -L "$1/phase" ] &&
    [ "$(cat "$1/phase")" = build ] &&
    { [ ! -e "$1/previous" ] || [ -z "$(ls -A "$1/previous")" ]; }
}
for entry in "$install_dir"/.compound-source-stage.*; do
  if [ -e "$entry" ] || [ -L "$entry" ]; then
    if build_only_stage "$entry"; then
      rm -rf "$entry"
      echo "install: removed an interrupted build-only stage at $entry (nothing was published or activated)" >&2
      continue
    fi
    fail "interrupted source installation retained at $entry; inspect before retrying"
  fi
done

artifacts="compound breadth memory_service"
if [ "$install_autonomy_lab" = 1 ]; then artifacts="$artifacts heartbeat_lib heartbeat attention"; fi
if [ "$install_os_alias" = 1 ]; then artifacts="$artifacts alias"; fi
artifact_target() {
  case "$1" in
    compound) target=$install_dir/compound ;;
    breadth) target=$install_dir/compound-breadth-engine ;;
    memory_service) target=$install_dir/compound-memory-service ;;
    heartbeat_lib) target=$install_dir/_lib/heartbeat-promote-stage.sh ;;
    heartbeat) target=$install_dir/compound-autonomy-heartbeat.sh ;;
    attention) target=$compound_home/scripts/attention-letter.py ;;
    alias) target=$os_alias ;;
    *) return 1 ;;
  esac
  incoming=$target.incoming.$$
}

stage=$(mktemp -d "$install_dir/.compound-source-stage.XXXXXX")
published=
prepared=
activation_started=0
completed=0
cleanup() {
  status=$?
  trap - 0 HUP INT TERM
  set +e
  rollback_failed=0
  if [ "$completed" != 1 ]; then
    for key in $published; do
      artifact_target "$key"
      if ! rm -f "$incoming"; then rollback_failed=1; continue; fi
      if [ -e "$stage/previous/$key" ] || [ -L "$stage/previous/$key" ]; then
        if ! cp -pP "$stage/previous/$key" "$incoming" || ! mv -f "$incoming" "$target"; then rollback_failed=1; fi
      elif ! rm -f "$target"; then
        rollback_failed=1
      fi
    done
    if [ -n "$published" ]; then
      if [ "$rollback_failed" = 0 ]; then
        echo "install: publication rolled back; previous artifact bytes, modes and absence restored" >&2
      else
        echo "install: rollback INCOMPLETE; retained previous/new artifacts at $stage" >&2
      fi
    fi
    if [ "$activation_started" = 1 ]; then
      echo "install: activation incomplete; home/profile changes may remain. Inspect binding state before retrying." >&2
    fi
  fi
  for key in $prepared; do artifact_target "$key"; rm -f "$incoming"; done
  if [ "$rollback_failed" = 0 ]; then rm -rf "$stage"; fi
  if [ "$completed" != 1 ] && [ "$status" = 0 ]; then status=1; fi
  exit "$status"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM
mkdir "$stage/new" "$stage/previous" "$stage/tools"
printf 'build\n' >"$stage/phase"

# Build and validate the entire enabled file set before replacing any target.
# Signing happens here, in the stage, so what is verified is what is installed.
go build -ldflags "-X main.buildSHA=$build_sha" -o "$stage/new/compound" ./cmd/compound
chmod 0755 "$stage/new/compound"
if [ -n "$sign_identity" ]; then
  unlock_signing_keychain
  sign_staged "$stage/new/compound" compound
fi
# Build the verifier into the stage and run it from there: `go run` starts it
# from the Go build cache, which may sit on a volume that is not answering.
go build -o "$stage/tools/compound-install-verify" ./cmd/compound-install-verify
run_step "install verification" "$stage/tools/compound-install-verify" --binary "$stage/new/compound" --build-sha "$build_sha"
go build -o "$stage/new/breadth" ./cmd/compound-breadth-engine
chmod 0755 "$stage/new/breadth"
if [ -n "$sign_identity" ]; then sign_staged "$stage/new/breadth" compound-breadth-engine; fi
breadth_nonce=source-install-$$
breadth_probe=$(printf '{"schema_version":1,"request_id":"%s","op":"capabilities"}\n' "$breadth_nonce" |
  COMPOUND_HOME='' COMPOUND_BREADTH_DISABLE_VECTOR=1 "$stage/new/breadth") || fail "breadth engine capabilities probe failed"
case "$breadth_probe" in
  '{"ok":true,"schema_version":1,"request_id":"'"$breadth_nonce"'","name":"compound-breadth","breadth":true,'*) ;;
  *) fail "breadth engine returned invalid capabilities" ;;
esac
if [ "$install_os_alias" = 1 ]; then ln -s compound "$stage/new/alias"; fi
if [ "$install_autonomy_lab" = 1 ]; then
  cp "$repo_root/scripts/_lib/heartbeat-promote-stage.sh" "$stage/new/heartbeat_lib"
  chmod 0644 "$stage/new/heartbeat_lib"
  sh -n "$stage/new/heartbeat_lib"
  sh -c '. "$1"; command -v promote_driver_stage_decide >/dev/null' _ "$stage/new/heartbeat_lib"
  cp "$repo_root/scripts/compound-autonomy-heartbeat.sh" "$stage/new/heartbeat"
  chmod 0755 "$stage/new/heartbeat"
  sh -n "$stage/new/heartbeat"
  cp "$repo_root/scripts/attention-letter.py" "$stage/new/attention"
  chmod 0755 "$stage/new/attention"
  python3 -c 'import ast,sys; ast.parse(open(sys.argv[1]).read())' "$stage/new/attention"
fi

cp "$repo_root/scripts/memory-service.py" "$stage/new/memory_service"
chmod 0755 "$stage/new/memory_service"
python3 -c 'import ast,sys; ast.parse(open(sys.argv[1]).read())' "$stage/new/memory_service"

# Existing-home health remains a refusal gate, not a cleanup mechanism.
if [ -d "$compound_home/config" ]; then
  if ! PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$stage/new/compound" home doctor --home "$compound_home" >"$stage/precheck" 2>&1; then
    sed 's/^/  /' "$stage/precheck" >&2
    fail "pre-publication home doctor FAILED; nothing was published"
  fi
fi

# Refuse unsupported required adapter owners before any home/profile activation
# or publication. A successful preflight proves eligibility, not activation.
if ! PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$stage/new/compound" harness refresh-lane --preflight --base "$HOME" --binary "$install_dir/compound" >"$stage/preflight" 2>&1; then
  cat "$stage/preflight" >&2
  fail "pre-publication refresh preflight FAILED; no home/profile activation or artifacts published"
fi

# Initialize through the verified staged CLI before adding anything to a fresh
# home: home init correctly refuses a populated path without its manifest.
# This is an activation effect, reported honestly even if later publication fails.
printf 'activation\n' >"$stage/phase"
activation_started=1
PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$stage/new/compound" home init --home "$compound_home" --global || fail "activation failed at home_init"

# Retain the complete preimage, then prepare a sibling temporary file at EVERY
# target. Cross-filesystem helper copies and invalid target types fail here.
for key in $artifacts; do
  artifact_target "$key"
  if [ -e "$target" ] || [ -L "$target" ]; then
    if [ "$key" != alias ] && [ -L "$target" ]; then fail "refusing symlink artifact: $target"; fi
    [ -f "$target" ] || [ -L "$target" ] || fail "refusing non-file artifact: $target"
    cp -pP "$target" "$stage/previous/$key"
  fi
  mkdir -p "$(dirname "$target")"
  [ ! -e "$incoming" ] && [ ! -L "$incoming" ] || fail "refusing existing staging path: $incoming"
  prepared="$key $prepared"
  cp -pP "$stage/new/$key" "$incoming"
done
# The shared lock excludes cooperating source/release installers. Consumers must
# be quiesced: the file set still uses sequential renames, never an atomic switch.
for key in $artifacts; do
  artifact_target "$key"
  published="$key $published"
  mv -f "$incoming" "$target"
done
echo "install: publication: complete"

PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" home doctor --home "$compound_home" || fail "activation failed at home_doctor"
PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" runtime doctor "$repo_root/fixtures/runtime/synthetic-service.yaml" || fail "activation failed at runtime_doctor"
# Preserve the refresh command's status; a pipeline ending in sed hid failures.
if ! PATH="$install_dir:$PATH" COMPOUND_HOME="$compound_home" "$install_dir/compound" harness refresh-lane --base "$HOME" --binary "$install_dir/compound" >"$stage/refresh" 2>&1; then
  cat "$stage/refresh" >&2
  fail "activation failed at harness_refresh"
fi
cat "$stage/refresh"
completed=1
# Point at the installed binary by its full path when its folder is not on PATH yet;
# `compound init` then prints the PATH line for the user's shell.
case ":$PATH:" in
  *":$install_dir:"*) init_cmd="compound init" ;;
  *) init_cmd="$install_dir/compound init" ;;
esac
cat <<EOF

compound installed
- publication: complete
- activation: complete
- binary: $install_dir/compound
- breadth_engine: $install_dir/compound-breadth-engine
EOF
if [ -n "$sign_identity" ]; then
  echo "- signature: fixed identity; designated requirement: $sign_kind (compound, compound-breadth-engine; settings: $sign_source)"
fi
cat <<EOF
- os_alias: opt-in via COMPOUND_INSTALL_OS_ALIAS=1 (existing path left unchanged otherwise)
- autonomy_lab: opt-in via COMPOUND_INSTALL_AUTONOMY_LAB=1
- autonomy_heartbeat: $install_dir/compound-autonomy-heartbeat.sh (when opted in)
- attention_letter: $compound_home/scripts/attention-letter.py (when opted in; max 3 decisions per cycle)
- home: $compound_home

next:
  $init_cmd    wire Claude Code or Codex, and check PATH
EOF
