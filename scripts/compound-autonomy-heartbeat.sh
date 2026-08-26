#!/bin/sh
set -u

# Compound-owned autonomy heartbeat. Every mutating promotion pass gets its own
# WorkEnvelope with explicit scheduler/harness/model provenance, then closes with
# the driver's exact terminal receipt. No static fake work id and no silent pass.
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"
LOG="$HOME/.compound/autonomy-heartbeat.log"
BIN="$HOME/.local/bin/compound"
TIMEOUT_BIN=$(command -v gtimeout || command -v timeout || true)
TS=$(date -u +%FT%TZ)

if [ ! -x "$BIN" ]; then
  echo "$TS preflight rc=69 ERR: installed compound unavailable at $BIN" >> "$LOG"
  exit 69
fi
if [ -z "$TIMEOUT_BIN" ]; then
  echo "$TS preflight rc=69 ERR: timeout utility unavailable; no stages executed" >> "$LOG"
  exit 69
fi
if ! "$BIN" memory promote-driver --help 2>&1 | grep -q "promote-driver"; then
  echo "$TS preflight rc=69 ERR: promote-driver unavailable in installed compound; no stages executed" >> "$LOG"
  exit 69
fi

DIR_OUT=$("$TIMEOUT_BIN" 300 "$BIN" director propose --apply --json 2>&1)
DIR_RC=$?
DIR_SUM=$(printf '%s' "$DIR_OUT" | python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
    print("proposals=%s suppressed=%s skipped=%s" % (d.get("proposal_count"), d.get("suppressed_duplicate_count"), d.get("skipped_count")))
except Exception:
    print("unparseable")' 2>/dev/null)
[ "$DIR_RC" -ne 0 ] && DIR_SUM="ERR: $(printf '%.160s' "$DIR_OUT")"
[ "$DIR_SUM" = "unparseable" ] && DIR_RC=65 && DIR_SUM="ERR: unparseable JSON"

PD_WORK_ID="work_promote_$(date -u +%Y%m%dT%H%M%SZ)_$$"
PD_ACTOR="scheduler:autonomy-heartbeat"
PD_RC=0
PD_SUM="not-run"
PD_TMP=$(mktemp -d "${TMPDIR:-/tmp}/compound-promote-heartbeat.XXXXXX") || exit 70
trap 'rm -rf "$PD_TMP"' EXIT HUP INT TERM

if ! "$BIN" work create \
  --work-id "$PD_WORK_ID" \
  --owner compound:memory-maintenance \
  --predicate "scheduled memory promote-driver pass terminalizes with an exact driver receipt" \
  --outcome "Run one bounded autonomous memory-maintenance pass" \
  --by "$PD_ACTOR" \
  --creator-harness script \
  --creator-model none \
  --creator-lane autonomy-heartbeat \
  --state ready \
  --canonical-surface compound://memory/promote-driver \
  --risk tier1 --privacy private >"$PD_TMP/create.out" 2>"$PD_TMP/create.err"; then
  PD_RC=1
  PD_SUM="ERR: work create $(printf '%.120s' "$(cat "$PD_TMP/create.err")")"
elif ! "$BIN" work claim "$PD_WORK_ID" --by "$PD_ACTOR" >"$PD_TMP/claim.out" 2>"$PD_TMP/claim.err"; then
  PD_RC=1
  PD_SUM="ERR: work claim $(printf '%.120s' "$(cat "$PD_TMP/claim.err")")"
elif ! "$BIN" work advance "$PD_WORK_ID" --to running --by "$PD_ACTOR" --note "scheduled bounded promote-driver pass" >"$PD_TMP/run.out" 2>"$PD_TMP/run.err"; then
  PD_RC=1
  PD_SUM="ERR: work advance $(printf '%.120s' "$(cat "$PD_TMP/run.err")")"
else
  "$TIMEOUT_BIN" 600 "$BIN" memory promote-driver \
    --confirm --work "$PD_WORK_ID" \
    --registry "$HOME/Projects/products/*/project.yaml" \
    --promote-limit 1 --scan-limit 100 --walk-scan-limit 500 \
    --json >"$PD_TMP/driver.json" 2>"$PD_TMP/driver.err"
  PD_RC=$?
  PD_FIELDS=$(python3 - "$PD_TMP/driver.json" <<'PY'
import json,sys
try:
    d=json.load(open(sys.argv[1]))
    promote=d.get("promote",{})
    sweep=d.get("sweep",{})
    promoted=[x.get("claim_id","") for x in promote.get("promotions",[]) if x.get("claim_id")]
    for src in d.get("imports",{}).get("sources",[]): promoted += [x for x in src.get("claim_ids",[]) if x]
    parked=[x.get("candidate_id","") for x in sweep.get("actions",[]) if x.get("verdict")=="park" and x.get("candidate_id") in set(sweep.get("changed_ids",[]))]
    rejected=[x.get("candidate_id","") for x in sweep.get("actions",[]) if x.get("verdict") in ("reject","no_promote") and x.get("candidate_id") in set(sweep.get("changed_ids",[]))]
    for group in d.get("dedupe",{}).get("actions",[]): rejected += group.get("no_promoted",[])
    print("|".join([d.get("pass_id", "unknown"), d.get("stage", "unknown"), ",".join(promoted), ",".join(parked), ",".join(rejected), str(promote.get("promoted",0)), str(sweep.get("adjudicated",0)), str(d.get("pending_after"))]))
except Exception:
    print("unknown|unparseable||||0|0|None")
PY
)
  OLD_IFS=$IFS
  IFS='|'
  read -r PD_PASS PD_STAGE PD_PROMOTED PD_PARKED PD_REJECTED PD_PROMOTED_N PD_SWEPT_N PD_PENDING <<EOF
$PD_FIELDS
EOF
  IFS=$OLD_IFS
  PD_PROOF="evidence:promote-driver-pass:$PD_PASS:$PD_STAGE"
  if [ "$PD_RC" -eq 0 ] && [ "$PD_STAGE" = "committed" ]; then
    "$BIN" work advance "$PD_WORK_ID" --to verifying --by "$PD_ACTOR" --verification "$PD_PROOF" --note "driver terminal receipt committed" >/dev/null 2>"$PD_TMP/verify.err" || PD_RC=1
    if [ "$PD_RC" -eq 0 ]; then
      if [ -n "$PD_PROMOTED$PD_PARKED$PD_REJECTED" ]; then
        set -- "$BIN" work close "$PD_WORK_ID" --outcome completed --move-type memory_delta --by "$PD_ACTOR" --summary "Scheduled memory maintenance committed" --changed --proof "$PD_PROOF"
        [ -n "$PD_PROMOTED" ] && set -- "$@" --memory-promoted "$PD_PROMOTED"
        [ -n "$PD_PARKED" ] && set -- "$@" --memory-parked "$PD_PARKED"
        [ -n "$PD_REJECTED" ] && set -- "$@" --memory-rejected "$PD_REJECTED"
      else
        set -- "$BIN" work close "$PD_WORK_ID" --outcome completed --move-type audit_only --by "$PD_ACTOR" --summary "Scheduled memory maintenance found no state delta" --proof "$PD_PROOF"
      fi
      "$@" >"$PD_TMP/close.out" 2>"$PD_TMP/close.err" || PD_RC=1
    fi
  else
    "$BIN" work close "$PD_WORK_ID" --outcome failed --move-type control_plane_repair --by "$PD_ACTOR" --summary "Scheduled memory maintenance did not commit ($PD_STAGE)" --proof "failure:promote-driver:$PD_PASS:$PD_STAGE" >"$PD_TMP/close.out" 2>"$PD_TMP/close.err" || true
  fi
  if [ "$PD_RC" -eq 0 ]; then
    PD_SUM="stage=$PD_STAGE pass=$PD_PASS promoted=$PD_PROMOTED_N swept=$PD_SWEPT_N pending=$PD_PENDING work=$PD_WORK_ID"
  else
    PD_SUM="ERR stage=$PD_STAGE pass=$PD_PASS $(printf '%.120s' "$(cat "$PD_TMP/driver.err" "$PD_TMP/close.err" 2>/dev/null)")"
  fi
fi

CONT_OUT=$("$TIMEOUT_BIN" 600 "$BIN" work continue --actor claude-minimax --until-wait --max-steps 3 --json 2>&1)
CONT_RC=$?
CONT_SUM=$(printf '%s' "$CONT_OUT" | python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
    print("status=%s steps=%s" % (d.get("status"), d.get("step_count")))
except Exception:
    print("unparseable")' 2>/dev/null)
[ "$CONT_RC" -ne 0 ] && CONT_SUM="ERR: $(printf '%.160s' "$CONT_OUT")"
[ "$CONT_SUM" = "unparseable" ] && CONT_RC=65 && CONT_SUM="ERR: unparseable JSON"

FINAL_RC=0
if [ "$DIR_RC" -ne 0 ] || [ "$PD_RC" -ne 0 ] || [ "$CONT_RC" -ne 0 ]; then
  FINAL_RC=1
fi
echo "$TS final rc=$FINAL_RC | director rc=$DIR_RC $DIR_SUM | promote-driver rc=$PD_RC $PD_SUM | continue rc=$CONT_RC $CONT_SUM" >> "$LOG"
exit "$FINAL_RC"
