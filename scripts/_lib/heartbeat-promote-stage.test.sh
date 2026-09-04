#!/usr/bin/env sh
# scripts/_lib/heartbeat-promote-stage.test.sh
#
# Shell-level tests for the promote-driver stage validation that the
# Compound autonomy heartbeat uses to decide rc. Exercises one stage case
# per test. Exits 0 on success, non-zero on first failure.
#
# Stages tested:
#   GREEN:
#     - committed (stage=committed, terminal receipt present)
#     - skipped_locked with FRESH heartbeat evidence (lock owner + recent receipts.jsonl)
#   RED:
#     - unparseable (malformed JSON)
#     - partial    (stage=partial)
#     - failed     (stage=failed)
#     - recovery_required (stage=recovery_required)
#     - unknown    (stage=planned|operation_recorded|finalization_recorded|empty)
#     - missing-pass-id (pass_id blank)
#     - missing-receipt (stage=committed but receipt_path absent)
#     - stale-lock (stage=skipped_locked but heartbeat evidence older than threshold)

set -eu

LIB_DIR=$(CDPATH= dirname -- "$0")
case "$LIB_DIR" in
  /*) ;;
  *)  LIB_DIR="$(pwd)/$LIB_DIR"
      [ -d "$LIB_DIR" ] || LIB_DIR="$(pwd)"
      ;;
esac

TMPDIR=$(mktemp -d 2>/dev/null) || TMPDIR=/tmp/heartbeat-promote-stage.test.$$
mkdir -p "$TMPDIR"
trap 'rm -rf "$TMPDIR"' EXIT

# Use a 5-minute freshness window in tests so we can stamp receipts.jsonl to
# 1 minute ago (fresh) or 30 minutes ago (stale) without timing flakes.
export HEARTBEAT_PROMOTE_STAGE_FRESH_MIN="${HEARTBEAT_PROMOTE_STAGE_FRESH_MIN:-5}"

. "$LIB_DIR/heartbeat-promote-stage.sh"

# The heartbeat's stage function is called directly (no command substitution)
# so its globals (PD_RC, PD_STAGE, PD_PASS, ...) land in THIS shell. The
# test then asserts on $PD_RC and the supporting globals.
call_stage() {
  _json_path="$1"
  _home="$2"
  _pd_rc="$3"
  _stdin_payload="$4"
  # Disable set -e around the call so the failure can be asserted rather
  # than killing the script.
  set +e
  promote_driver_stage_decide "$_json_path" "$_home" "$_pd_rc" <<EOF
$_stdin_payload
EOF
  PD_RC=$?
  set -e
}

assert_rc() {
  _expect="$1"
  _msg="$2"
  if [ "$_expect" != "$PD_RC" ]; then
    echo "FAIL: $_msg"
    echo "  expected rc=$_expect"
    echo "  actual   rc=$PD_RC (stage=$PD_STAGE pass=$PD_PASS)"
    exit 1
  fi
  printf "ok: %s -> rc=%s stage=%s\n" "$_msg" "$PD_RC" "$PD_STAGE"
}

setup_home() {
  _home="$TMPDIR/home-$$-$RANDOM"
  mkdir -p "$_home/state/promote-driver" "$_home/lock"
  printf '%s' "$_home"
}

write_receipts() {
  _home="$1"
  _pass="$2"
  _stage="$3"
  printf '{"pass_id":"%s","stage":"%s"}\n' "$_pass" "$_stage" \
    > "$_home/state/promote-driver/receipts.jsonl"
}

# write_receipt_line writes the EXACT driver JSON line into receipts.jsonl
# so the receipt digest matches the driver stdout digest (committed case).
write_receipt_line() {
  _home="$1"
  _json_line="$2"
  printf '%s\n' "$_json_line" \
    > "$_home/state/promote-driver/receipts.jsonl"
}

freshen_receipts() {
  _home="$1"
  # Set mtime to 1 minute ago -> fresh under default 5-min window.
  touch -t "$(date -v-1M +%Y%m%d%H%M.%S 2>/dev/null || date -d '1 minute ago' +%Y%m%d%H%M.%S)" \
    "$_home/state/promote-driver/receipts.jsonl"
}

stale_receipts() {
  _home="$1"
  # Set mtime to 30 minutes ago -> stale under default 5-min window.
  touch -t "$(date -v-30M +%Y%m%d%H%M.%S 2>/dev/null || date -d '30 minutes ago' +%Y%m%d%H%M.%S)" \
    "$_home/state/promote-driver/receipts.jsonl"
}

take_lock() {
  _home="$1"
  # Just create the lock file. The function under test only checks file
  # presence + receipts.jsonl mtime for staleness — it does NOT take a flock
  # itself, so we don't need to hold one here either. The driver's own
  # single-flight is a separate concern.
  : > "$_home/lock/promote-driver.lock"
}

release_lock() {
  : # no-op; lock is just a file in the test fixture
}

# ----------------------------------------------------------------------

echo "==> GREEN: committed with terminal receipt"

HOME_C=$(setup_home)
PASS_C="pass-committed-001"
# Receipt must match the driver stdout byte-for-byte for the digest check.
RECEIPT_C="{\"pass_id\":\"$PASS_C\",\"stage\":\"committed\",\"home\":\"$HOME_C\",\"receipt_path\":\"$HOME_C/state/promote-driver/receipts.jsonl\",\"started_at\":\"2026-09-05T00:00:00Z\",\"finished_at\":\"2026-09-05T00:00:01Z\",\"promote\":{\"promoted\":0},\"sweep\":{\"adjudicated\":0},\"pending_after\":0}"
write_receipt_line "$HOME_C" "$RECEIPT_C"
call_stage "$TMPDIR/driver-committed.json" "$HOME_C" "0" "$RECEIPT_C"
assert_rc 0 "committed -> rc=0"

# ----------------------------------------------------------------------

echo "==> GREEN: skipped_locked with FRESH heartbeat evidence"

HOME_L=$(setup_home)
PASS_L="pass-locked-001"
take_lock "$HOME_L"
write_receipts "$HOME_L" "pass-prior-000" "committed"
freshen_receipts "$HOME_L"
call_stage "$TMPDIR/driver-locked-fresh.json" "$HOME_L" "0" \
  "{\"pass_id\":\"$PASS_L\",\"locked\":true,\"home\":\"$HOME_L\",\"started_at\":\"2026-09-05T00:00:00Z\",\"finished_at\":\"2026-09-05T00:00:01Z\"}"
release_lock
assert_rc 0 "skipped_locked + fresh heartbeat -> rc=0"

# ----------------------------------------------------------------------

echo "==> RED: unparseable JSON"

HOME_U=$(setup_home)
call_stage "$TMPDIR/driver-unparseable.json" "$HOME_U" "0" \
  "{ this is not valid json"
assert_rc 1 "unparseable -> rc=1"

# ----------------------------------------------------------------------

echo "==> RED: stage=partial"

HOME_P=$(setup_home)
PASS_P="pass-partial-001"
call_stage "$TMPDIR/driver-partial.json" "$HOME_P" "0" \
  "{\"pass_id\":\"$PASS_P\",\"stage\":\"partial\",\"home\":\"$HOME_P\",\"receipt_path\":\"$HOME_P/state/promote-driver/receipts.jsonl\",\"started_at\":\"2026-09-05T00:00:00Z\",\"finished_at\":\"2026-09-05T00:00:01Z\"}"
assert_rc 1 "partial -> rc=1"

# ----------------------------------------------------------------------

echo "==> RED: stage=failed"

HOME_F=$(setup_home)
PASS_F="pass-failed-001"
call_stage "$TMPDIR/driver-failed.json" "$HOME_F" "0" \
  "{\"pass_id\":\"$PASS_F\",\"stage\":\"failed\",\"home\":\"$HOME_F\",\"receipt_path\":\"$HOME_F/state/promote-driver/receipts.jsonl\",\"started_at\":\"2026-09-05T00:00:00Z\",\"finished_at\":\"2026-09-05T00:00:01Z\",\"error\":\"phase foo exploded\"}"
assert_rc 1 "failed -> rc=1"

# ----------------------------------------------------------------------

echo "==> RED: stage=recovery_required"

HOME_R=$(setup_home)
PASS_R="pass-recovery-001"
call_stage "$TMPDIR/driver-recovery.json" "$HOME_R" "0" \
  "{\"pass_id\":\"$PASS_R\",\"stage\":\"recovery_required\",\"home\":\"$HOME_R\",\"started_at\":\"2026-09-05T00:00:00Z\",\"finished_at\":\"2026-09-05T00:00:01Z\",\"error\":\"terminal receipt unavailable\"}"
assert_rc 1 "recovery_required -> rc=1"

# ----------------------------------------------------------------------

echo "==> RED: stage=unknown (empty stage)"

HOME_K=$(setup_home)
PASS_K="pass-unknown-001"
call_stage "$TMPDIR/driver-unknown.json" "$HOME_K" "0" \
  "{\"pass_id\":\"$PASS_K\",\"home\":\"$HOME_K\",\"started_at\":\"2026-09-05T00:00:00Z\",\"finished_at\":\"2026-09-05T00:00:01Z\"}"
assert_rc 1 "unknown (no stage) -> rc=1"

# ----------------------------------------------------------------------

echo "==> RED: missing pass_id"

HOME_M=$(setup_home)
call_stage "$TMPDIR/driver-no-pass.json" "$HOME_M" "0" \
  "{\"pass_id\":\"\",\"stage\":\"committed\",\"home\":\"$HOME_M\",\"receipt_path\":\"$HOME_M/state/promote-driver/receipts.jsonl\"}"
assert_rc 1 "missing-pass-id -> rc=1"

# ----------------------------------------------------------------------

echo "==> RED: missing terminal receipt (committed but receipt_path absent)"

HOME_N=$(setup_home)
PASS_N="pass-no-receipt-001"
call_stage "$TMPDIR/driver-no-receipt.json" "$HOME_N" "0" \
  "{\"pass_id\":\"$PASS_N\",\"stage\":\"committed\",\"home\":\"$HOME_N\",\"started_at\":\"2026-09-05T00:00:00Z\",\"finished_at\":\"2026-09-05T00:00:01Z\"}"
assert_rc 1 "missing-receipt -> rc=1"

# ----------------------------------------------------------------------

echo "==> RED: skipped_locked but heartbeat is STALE"

HOME_S=$(setup_home)
PASS_S="pass-locked-stale-001"
take_lock "$HOME_S"
write_receipts "$HOME_S" "pass-prior-001" "committed"
stale_receipts "$HOME_S"
call_stage "$TMPDIR/driver-locked-stale.json" "$HOME_S" "0" \
  "{\"pass_id\":\"$PASS_S\",\"locked\":true,\"home\":\"$HOME_S\",\"started_at\":\"2026-09-05T00:00:00Z\",\"finished_at\":\"2026-09-05T00:00:01Z\"}"
release_lock
assert_rc 1 "skipped_locked + stale heartbeat -> rc=1"

# ----------------------------------------------------------------------

echo ""
echo "==========================================="
echo "  ALL TESTS PASSED"
echo "==========================================="
