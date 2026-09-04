#!/usr/bin/env sh
# scripts/_lib/heartbeat-end-to-end.test.sh
#
# End-to-end test of scripts/compound-autonomy-heartbeat.sh driven by a
# stub `compound` binary that emits a controlled driver JSON. Runs the real
# script with stub `work`, `memory`, `director`, `home archive-history`,
# `work sweep-stale`, and `work continue` subcommands. Asserts the FINAL_RC
# matches the semantic stage (committed + fresh skipped_locked -> 0;
# everything else -> nonzero). All other stages pass through (their stage
# logic is NOT exercised by this test — that's what
# heartbeat-promote-stage.test.sh covers).

set -eu

LIB_DIR=$(CDPATH= dirname -- "$0")
case "$LIB_DIR" in
  /*) ;;
  *)  LIB_DIR="$(pwd)/$LIB_DIR"
      [ -d "$LIB_DIR" ] || LIB_DIR="$(pwd)"
      ;;
esac

ROOT_DIR=$(CDPATH= cd -- "$LIB_DIR/../.." && pwd)
SCRIPT="$ROOT_DIR/scripts/compound-autonomy-heartbeat.sh"

TMPDIR=$(mktemp -d 2>/dev/null) || TMPDIR=/tmp/heartbeat-e2e.test.$$
mkdir -p "$TMPDIR"
trap 'rm -rf "$TMPDIR"' EXIT

# Stub `compound` — outputs the JSON passed in $FAKE_DRIVER_JSON to stdout
# for `memory promote-driver`, and exits 0. Other subcommands exit 0 too.
FAKE_BIN="$TMPDIR/fake-bin"
mkdir -p "$FAKE_BIN"
cat > "$FAKE_BIN/compound" <<'STUB'
#!/usr/bin/env sh
# Stub for the heartbeat tests. Behaviour is driven by env vars set per case:
#   FAKE_DRIVER_JSON  — JSON to emit on `memory promote-driver --json` (else
#                       a default committed payload).
#   FAKE_HOLD_LOCK    — when set to 1, refuse to release the single-flight
#                       lock (we never take it; the test home has no lock).
# For all other subcommands the stub exits 0 and emits a minimal JSON shape
# the script's parsers accept.
case "${1:-}" in
  memory)
    case "${2:-}" in
      promote-driver)
        # --help path: emit usage that contains "promote-driver" so the
        # heartbeat's preflight recognises the subcommand is supported.
        if [ "${3:-}" = "--help" ] || [ "${3:-}" = "help" ]; then
          printf 'Usage: compound memory promote-driver [--json] [--confirm]\n'
          exit 0
        fi
        if [ -n "${FAKE_DRIVER_JSON:-}" ]; then
          printf '%s\n' "$FAKE_DRIVER_JSON"
        else
          printf '{"pass_id":"unknown","stage":"unknown"}\n'
        fi
        exit 0
        ;;
    esac
    ;;
  director)
    printf '{"proposal_count":0,"suppressed_duplicate_count":0,"skipped_count":0}\n'
    exit 0
    ;;
  home)
    printf -- '- archived: 0\n- dry_run: false\n'
    exit 0
    ;;
  work)
    printf '{"status":"ok","safe":0,"reclaimed":0,"skipped":0,"step_count":0}\n'
    exit 0
    ;;
esac
exit 0
STUB
chmod +x "$FAKE_BIN/compound"

run_heartbeat() {
  _case="$1"
  _expected_rc="$2"
  _extra_setup="$3"

  _home="$TMPDIR/home-$$-$_case-$RANDOM"
  mkdir -p "$_home/.compound/state/promote-driver" "$_home/.compound/lock" \
           "$_home/.local/bin" "$_home/.local/state"

  # Provide a recent receipts.jsonl so the freshness check sees a holder.
  printf '{"pass_id":"prior","stage":"committed"}\n' \
    > "$_home/.compound/state/promote-driver/receipts.jsonl"
  touch "$_home/.compound/lock/promote-driver.lock"

  eval "$_extra_setup"

  # Run the heartbeat with our stub on PATH first so PATH resolution finds
  # the stub (the heartbeat reads $HOME/.local/bin/compound which we also
  # symlink below).
  ln -sf "$FAKE_BIN/compound" "$_home/.local/bin/compound"

  set +e
  HOME="$_home" PATH="$FAKE_BIN:$PATH" timeout 30 sh "$SCRIPT" >/dev/null 2>&1
  _rc=$?
  set -e

  if [ "$_rc" = "$_expected_rc" ]; then
    printf "ok: %s -> FINAL_RC=%s\n" "$_case" "$_rc"
  else
    # Show the heartbeat log to diagnose.
    echo "FAIL: $_case"
    echo "  expected FINAL_RC=$_expected_rc"
    echo "  actual   FINAL_RC=$_rc"
    if [ -f "$_home/.compound/autonomy-heartbeat.log" ]; then
      echo "  --- last log line ---"
      tail -1 "$_home/.compound/autonomy-heartbeat.log"
    fi
    exit 1
  fi
}

# write_receipt_for: stamps the home's receipts.jsonl with a line whose
# pass_id+stage match the test's driver JSON. The semantic stage verifier
# requires the line's sha256 to match the driver's stdout sha256, so the
# caller must pass the EXACT JSON the driver will emit.
write_receipt_for() {
  _home="$1"
  _json="$2"
  printf '%s\n' "$_json" > "$_home/.compound/state/promote-driver/receipts.jsonl"
}

# ----------------------------------------------------------------------

echo "==> e2e: committed driver (process exit 0) -> FINAL_RC=0"

# For the committed case the script writes its driver JSON to a TMPDIR path
# inside the home and then calls our stage function which needs the receipt
# file at the driver-supplied receipt_path to exist with a matching line.
# We can't know the exact TMPDIR path the script will use, so instead we set
# receipt_path to the well-known test path AND pre-populate receipts.jsonl
# there with the exact driver JSON.
TEST_HOME="$TMPDIR/committed-home-$RANDOM"
mkdir -p "$TEST_HOME/.compound"
RECEIPT_PATH="$TEST_HOME/.compound/state/promote-driver/receipts.jsonl"
DRIVER_JSON='{"pass_id":"pass-committed-001","stage":"committed","home":"'"$TEST_HOME"'/.compound","receipt_path":"'"$RECEIPT_PATH"'","started_at":"2026-09-05T00:00:00Z","finished_at":"2026-09-05T00:00:01Z","promote":{"promoted":0},"sweep":{"adjudicated":0},"pending_after":0}'
mkdir -p "$(dirname "$RECEIPT_PATH")"
printf '%s\n' "$DRIVER_JSON" > "$RECEIPT_PATH"

run_heartbeat "committed" "0" \
  'FAKE_DRIVER_JSON="$DRIVER_JSON"; export FAKE_DRIVER_JSON; HOME="'"$TEST_HOME"'"; export HOME'

echo "==> e2e: partial driver (process exit 0) -> FINAL_RC=1"

DRIVER_JSON='{"pass_id":"pass-partial-001","stage":"partial","home":"..."}'
run_heartbeat "partial" "1" \
  'FAKE_DRIVER_JSON="$DRIVER_JSON"; export FAKE_DRIVER_JSON'

echo "==> e2e: failed driver (process exit 0) -> FINAL_RC=1"

DRIVER_JSON='{"pass_id":"pass-failed-001","stage":"failed","error":"phase exploded"}'
run_heartbeat "failed" "1" \
  'FAKE_DRIVER_JSON="$DRIVER_JSON"; export FAKE_DRIVER_JSON'

echo "==> e2e: recovery_required driver (process exit 0) -> FINAL_RC=1"

DRIVER_JSON='{"pass_id":"pass-recovery-001","stage":"recovery_required","error":"terminal receipt unavailable"}'
run_heartbeat "recovery_required" "1" \
  'FAKE_DRIVER_JSON="$DRIVER_JSON"; export FAKE_DRIVER_JSON'

echo "==> e2e: unknown stage (process exit 0) -> FINAL_RC=1"

DRIVER_JSON='{"pass_id":"pass-unknown-001","home":"..."}'
run_heartbeat "unknown" "1" \
  'FAKE_DRIVER_JSON="$DRIVER_JSON"; export FAKE_DRIVER_JSON'

echo ""
echo "==========================================="
echo "  ALL E2E TESTS PASSED"
echo "==========================================="
