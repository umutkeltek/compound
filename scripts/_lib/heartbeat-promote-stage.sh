#!/usr/bin/env sh
# scripts/_lib/heartbeat-promote-stage.sh
#
# Stage-truth decision for the Compound autonomy heartbeat's promote-driver
# stage. Pure shell + python3 so the heartbeat (sh) and the stage test (sh)
# both depend on the same surface. Sourced by:
#   scripts/compound-autonomy-heartbeat.sh
#   scripts/_lib/heartbeat-promote-stage.test.sh
#
# Public surface:
#   promote_driver_stage_decide <driver_json_path> <home_path> <pd_rc>
#       stdin:  the driver's own stdout (raw JSON). Used so we never trust a
#               synthesized string.
#       argv1:  path the caller wrote the driver's stdout to (kept for
#               diagnostics; the function re-reads stdin).
#       argv2:  home path; the lock file and receipts.jsonl are resolved here.
#       argv3:  the binary's process exit code (PD_RC); used as a corroborating
#               signal but never as the only signal.
#       Sets globals (consumed by the heartbeat script):
#           PD_RC        0 when GREEN, 1 when RED
#           PD_STAGE     committed | skipped_locked | partial | failed
#                        | recovery_required | unparseable | unknown
#           PD_PASS      pass_id ("" if missing)
#           PD_PROOF     evidence:promote-driver-pass:<pass>:<stage>[:digest|fresh]
#           PD_PROMOTED  csv of promoted claim_ids
#           PD_PARKED    csv of parked candidate_ids
#           PD_REJECTED  csv of rejected candidate_ids
#           PD_PROMOTED_N PD_SWEPT_N PD_PENDING  counts
#           PD_FRESH_LOCK 1 when skipped_locked was verified FRESH, else 0
#
# GREEN only:
#   - stage == "committed" AND a terminal receipt for pass_id is present in
#     the receipt_path file AND a digest read off the receipt line matches a
#     hash computed from the driver's stdout (no synthesized strings).
#   - OR locked=true AND lock file exists AND receipts.jsonl mtime is within
#     HEARTBEAT_PROMOTE_STAGE_FRESH_MIN minutes (a stale lock is RED).
#
# RED for everything else: malformed/unparseable, missing pass id, missing
# terminal receipt, partial, failed, recovery_required, unknown stage,
# stale lock, and any failure to finalize.

# Default freshness window for skipped_locked liveness check. The hourly
# heartbeat cadence plus a 15-minute budget comfortably catches a stuck
# driver before the next tick. Tests override this via env to avoid timing
# flakes.
: "${HEARTBEAT_PROMOTE_STAGE_FRESH_MIN:=15}"

# raw_head prints the first 300 bytes of a stage's raw output with line breaks
# collapsed, so an `ERR: unparseable` verdict in the one-line heartbeat log
# carries enough of the offending bytes to diagnose later.
raw_head() { printf '%.300s' "$1" | tr -d '\n\r'; }

# decide_rc: 0 GREEN, 1 RED. Mutates globals documented above.
promote_driver_stage_decide() {
  _drv_json="$1"
  _home="$2"
  _pd_rc="$3"
  # Driver stdout is read from a path the caller passed via stdin. The
  # heartbeat writes the driver's stdout to a temp file then passes the
  # path on stdin; tests pass the raw JSON on stdin.
  _drv_in=$(cat)

  PD_RC=1
  PD_STAGE="unparseable"
  PD_PASS=""
  PD_PROOF=""
  PD_PROMOTED=""
  PD_PARKED=""
  PD_REJECTED=""
  PD_PROMOTED_N=0
  PD_SWEPT_N=0
  PD_PENDING=None
  PD_FRESH_LOCK=0

  # ------------------------------------------------------------------
  # 1. Parse + validate the driver JSON strictly. Malformed/missing
  #    required fields => RED with stage=unparseable. Pass the driver
  #    JSON as argv4 (not stdin) because the python heredoc consumes
  #    stdin and we cannot ALSO pipe JSON into it.
  # ------------------------------------------------------------------
  _eval=$(HEARTBEAT_PROMOTE_STAGE_FRESH_MIN="$HEARTBEAT_PROMOTE_STAGE_FRESH_MIN" \
    python3 - "$_drv_json" "$_home" "$_pd_rc" "$_drv_in" <<'PY'
import hashlib, json, os, sys, time

drv_path, home, pd_rc, raw = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
fresh_min = int(os.environ.get("HEARTBEAT_PROMOTE_STAGE_FRESH_MIN", "15"))
try:
    d = json.loads(raw)
except Exception as e:
    print("UNPARSEABLE|" + repr(e)[:160])
    sys.exit(0)

pass_id = (d.get("pass_id") or "").strip()
stage = (d.get("stage") or "").strip()
locked = bool(d.get("locked"))
home_in_report = (d.get("home") or "").strip()
receipt_path = (d.get("receipt_path") or "").strip()

# Strip the optional fields the parent script used to consume (promoted/
# parked/rejected csv + counts) so we update them in one place.
promote = d.get("promote") or {}
sweep = d.get("sweep") or {}
dedupe = d.get("dedupe") or {}
imports = d.get("imports") or {}

promoted = []
for x in promote.get("promotions") or []:
    cid = x.get("claim_id")
    if cid:
        promoted.append(cid)
for src in (imports.get("sources") or []):
    for cid in (src.get("claim_ids") or []):
        if cid:
            promoted.append(cid)

parked = []
for x in sweep.get("actions") or []:
    if x.get("verdict") == "park" and x.get("candidate_id") in set(sweep.get("changed_ids") or []):
        parked.append(x.get("candidate_id"))

rejected = []
for x in sweep.get("actions") or []:
    if x.get("verdict") in ("reject", "no_promote") and x.get("candidate_id") in set(sweep.get("changed_ids") or []):
        rejected.append(x.get("candidate_id"))
for grp in dedupe.get("actions") or []:
    for cid in (grp.get("no_promoted") or []):
        if cid:
            rejected.append(cid)

pending_after = d.get("pending_after")
pending_after_str = "None" if pending_after is None else str(pending_after)

print("FIELDS|" + "|".join([
    pass_id,
    stage,
    ",".join(promoted),
    ",".join(parked),
    ",".join(rejected),
    str(promote.get("promoted", 0)),
    str(sweep.get("adjudicated", 0)),
    pending_after_str,
]))

# ------------------------------------------------------------------
# 2. Semantic stage decision. Anything not in the GREEN list is RED.
# ------------------------------------------------------------------
GREEN = False
verdict_stage = "unparseable"
proof_digest = ""
fresh_lock = False

if not pass_id:
    verdict_stage = "unparseable"
elif stage == "committed":
    # Real terminal receipt required at receipt_path, with a line for pass_id
    # AND a digest read off that line so we never trust a synthesized string.
    if not receipt_path or not os.path.exists(receipt_path):
        verdict_stage = "missing_receipt"
    else:
        found = False
        matching_line = None
        try:
            with open(receipt_path, "r", encoding="utf-8", errors="replace") as fh:
                for line in fh:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        rec = json.loads(line)
                    except Exception:
                        continue
                    if rec.get("pass_id") == pass_id and rec.get("stage") == "committed":
                        found = True
                        matching_line = line
                        break
        except OSError:
            matching_line = None
        if not found or matching_line is None:
            verdict_stage = "missing_receipt"
        else:
            digest = hashlib.sha256(matching_line.encode("utf-8")).hexdigest()
            # Digest must match a digest we can also derive from the driver's
            # own stdout — never a synthesized string. The driver stdout IS
            # the report that was appended as the receipt, so we sha256 the
            # exact bytes the binary emitted and compare to the receipt line.
            raw_digest = hashlib.sha256(raw.strip().encode("utf-8")).hexdigest()
            if digest != raw_digest:
                verdict_stage = "missing_receipt"
            else:
                verdict_stage = "committed"
                GREEN = True
                proof_digest = digest
elif stage in ("planned", "operation_recorded", "finalization_recorded"):
    verdict_stage = "unknown"
elif stage == "partial":
    verdict_stage = "partial"
elif stage == "failed":
    verdict_stage = "failed"
elif stage == "recovery_required":
    verdict_stage = "recovery_required"
elif locked:
    # Skipped because another pass held the single-flight lock.
    # Doctrine: keep waiting, never contend — but ONLY when the holder is
    # alive. A stale lock (no recent receipts.jsonl progress) means the
    # holder is wedged and we MUST surface that.
    lock_path = os.path.join(home, "lock", "promote-driver.lock")
    rcpts = os.path.join(home, "state", "promote-driver", "receipts.jsonl")
    if not os.path.exists(lock_path):
        verdict_stage = "stale_lock"
    elif not os.path.exists(rcpts):
        verdict_stage = "stale_lock"
    else:
        age_min = (time.time() - os.path.getmtime(rcpts)) / 60.0
        if age_min > fresh_min:
            verdict_stage = "stale_lock"
        else:
            verdict_stage = "skipped_locked"
            fresh_lock = True
            GREEN = True
else:
    verdict_stage = "unknown"

print("VERDICT|" + verdict_stage + "|" + ("GREEN" if GREEN else "RED") + "|" + proof_digest + "|" + ("1" if fresh_lock else "0"))
PY
  )

  # Parse the two-line evaluation output.
  _fields_line=$(printf '%s' "$_eval" | sed -n 's/^FIELDS|//p')
  _verdict_line=$(printf '%s' "$_eval" | sed -n 's/^VERDICT|//p')

  IFS='|' read -r PD_PASS PD_STAGE_RAW PD_PROMOTED PD_PARKED PD_REJECTED PD_PROMOTED_N PD_SWEPT_N PD_PENDING <<EOF
$_fields_line
EOF

  IFS='|' read -r _stage _verdict _digest _fresh <<EOF
$_verdict_line
EOF

  PD_STAGE="$_stage"
  PD_FRESH_LOCK="$_fresh"
  # If python never produced a VERDICT line (malformed JSON), default to
  # unparseable so the assertion labels are accurate even on parse failure.
  if [ -z "$PD_STAGE" ]; then
    PD_STAGE="unparseable"
  fi
  if [ "$_verdict" = "GREEN" ]; then
    PD_RC=0
  else
    PD_RC=1
  fi

  # Build the evidence proof. For committed, include the receipt digest
  # (read off the receipt line, not synthesized). For skipped_locked,
  # include the freshness flag.
  case "$PD_STAGE" in
    committed)
      PD_PROOF="evidence:promote-driver-pass:$PD_PASS:committed:receipt-sha256=$_digest"
      ;;
    skipped_locked)
      PD_PROOF="evidence:promote-driver-pass:$PD_PASS:skipped_locked:fresh=$PD_FRESH_LOCK"
      ;;
    *)
      PD_PROOF="evidence:promote-driver-pass:$PD_PASS:$PD_STAGE"
      ;;
  esac

  # ------------------------------------------------------------------
  # 3. The process exit code is corroborating, not authoritative. If the
  #    JSON says it's green but the binary errored, we still surface a RED
  #    so the operator sees the disagreement.
  # ------------------------------------------------------------------
  if [ "$PD_RC" -eq 0 ] && [ "$_pd_rc" != "0" ]; then
    PD_RC=1
    PD_STAGE="unknown"
    PD_PROOF="evidence:promote-driver-pass:$PD_PASS:binary-nonzero"
  fi

  return "$PD_RC"
}
