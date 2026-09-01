#!/bin/sh
set -u

# Experimental Compound-owned autonomy heartbeat. Its promotion stage is the
# supported frozen candidate-only driver path; registry and lane-belief
# mutation are intentionally absent.
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

# Landfill compaction, BEFORE the promote pass so the pass walks live work only.
# Terminal candidates (no_promote/rejected/promoted) never leave the inbox on
# their own, and the promote cursor re-walks all of them forever: measured
# 2026-09-01, 154,127 inbox entries of which 11 were `proposed`, so a 500-entry
# walk considered 0 candidates and still cost 78s. archive-history is the
# supported eviction door for exactly those three states (`parked` and
# `proposed` are never touched), and it is reversible via
# `compound home restore-history`. Without this stage the walk stays
# proportional to everything ever adjudicated instead of to what is live.
# The timeout is DELIBERATELY generous, and larger than it looks like it needs to be.
# archive-history moves every eligible file and only THEN commits the archive index
# (internal/home/archive.go, commitOperationalArchiveTransaction after the loop), so a
# timeout that fires mid-loop leaves files moved but unindexed — invisible to
# `restore-history` and silently unrecoverable. Interrupting is strictly worse than
# running long. Steady-state passes take seconds (only the terminal candidates minted
# since the last tick); the one-time 154k drain on 2026-09-01 took ~20 minutes, which a
# 900s budget would have killed at the worst possible moment. Concurrent ticks are safe:
# the whole operation is inside the flock-based autonomy admission lock, so a second tick
# blocks and then finds nothing to do.
ARCH_OUT=$("$TIMEOUT_BIN" 3600 "$BIN" home archive-history --kind memory-candidate 2>&1)
ARCH_RC=$?
ARCH_SUM=$(printf '%s' "$ARCH_OUT" | awk -F': ' '
  /^- archived:/ { archived = $2; found = 1 }
  END { if (found) print "archived=" archived; else print "unparseable" }' 2>/dev/null)
[ "$ARCH_RC" -ne 0 ] && ARCH_SUM="ERR: $(printf '%.160s' "$ARCH_OUT")"
[ "$ARCH_SUM" = "unparseable" ] && ARCH_RC=65 && ARCH_SUM="ERR: unparseable output"

PD_WORK_ID="work_promote_$(date -u +%Y%m%dT%H%M%SZ)_$$"
PD_ACTOR="scheduler:autonomy-heartbeat"
PD_RC=0
PD_SUM="not-run"
PD_TMP=$(mktemp -d "${TMPDIR:-/tmp}/compound-promote-heartbeat.XXXXXX") || exit 70
trap 'rm -rf "$PD_TMP"' EXIT HUP INT TERM

terminalize_failed_work() {
  failure_summary=$1
  failure_proof=$2
  if "$BIN" work close "$PD_WORK_ID" --outcome failed --move-type control_plane_repair --by "$PD_ACTOR" --summary "$failure_summary" --proof "$failure_proof" >"$PD_TMP/close.out" 2>"$PD_TMP/close.err"; then
    return 0
  fi
  "$BIN" work advance "$PD_WORK_ID" --to verifying --by "$PD_ACTOR" --verification "$failure_proof" --note "$failure_summary" >"$PD_TMP/recover.out" 2>"$PD_TMP/recover.err" || return 1
  "$BIN" work close "$PD_WORK_ID" --outcome failed --move-type control_plane_repair --by "$PD_ACTOR" --summary "$failure_summary" --proof "$failure_proof" >"$PD_TMP/close.out" 2>"$PD_TMP/close.err"
}

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
  terminalize_failed_work "Scheduled memory maintenance claim failed" "failure:promote-driver:claim" || PD_SUM="$PD_SUM; terminalization failed"
elif ! "$BIN" work advance "$PD_WORK_ID" --to running --by "$PD_ACTOR" --note "scheduled bounded promote-driver pass" >"$PD_TMP/run.out" 2>"$PD_TMP/run.err"; then
  PD_RC=1
  PD_SUM="ERR: work advance $(printf '%.120s' "$(cat "$PD_TMP/run.err")")"
  terminalize_failed_work "Scheduled memory maintenance running advance failed" "failure:promote-driver:advance" || PD_SUM="$PD_SUM; terminalization failed"
else
	# --walk-scan-limit must EXCEED the whole live corpus or the dedupe phase can never
	# run: driver.go computes `walkedAll := cursor == "" && page.AtEnd && !blocked`, so
	# collapse happens only on a single pass that starts at the beginning AND reaches the
	# end. At the old 500 that was arithmetically unreachable — 104 consecutive passes
	# skipped dedupe with "resume walk". Post-compaction the corpus is ~51k (parked, which
	# is never evicted), and a full 19k walk measured 29s, so 60000 buys full coverage with
	# headroom and degrades gracefully (back to a resume walk) if parked ever outgrows it.
	"$TIMEOUT_BIN" 600 "$BIN" memory promote-driver \
		--confirm --work "$PD_WORK_ID" \
		--promote-limit 1 --scan-limit 100 --walk-scan-limit 60000 \
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
    terminalize_failed_work "Scheduled memory maintenance did not commit ($PD_STAGE)" "failure:promote-driver:$PD_PASS:$PD_STAGE" || PD_SUM="ERR: driver failed and work terminalization failed"
  fi
  if [ "$PD_RC" -ne 0 ] && [ "$PD_STAGE" = "committed" ]; then
    terminalize_failed_work "Scheduled memory maintenance receipt close failed" "failure:promote-driver:$PD_PASS:work-close" || PD_SUM="ERR: committed driver pass left work nonterminal"
  fi
  if [ "$PD_RC" -eq 0 ]; then
    PD_SUM="stage=$PD_STAGE pass=$PD_PASS promoted=$PD_PROMOTED_N swept=$PD_SWEPT_N pending=$PD_PENDING work=$PD_WORK_ID"
  else
    PD_SUM="ERR stage=$PD_STAGE pass=$PD_PASS $(printf '%.120s' "$(cat "$PD_TMP/driver.err" "$PD_TMP/close.err" 2>/dev/null)")"
  fi
fi

# Diagnosis only -- deliberately NOT --yes. The apply path terminalizes an
# envelope read before the liveness probe, so a concurrent actor that changes
# owner/metadata/attachments without changing state has those writes silently
# overwritten, and a process that becomes live after the probe can be
# terminalized under it. Unattended hourly mutation is the wrong place to carry
# that risk: the default dry-run still reports safe/reclaimed/skipped, so the
# backlog stays observable while nothing is rewritten. Restore --yes only once
# the reclaim is bound to the exact envelope generation it diagnosed and the
# envelope write + lifecycle event + lease release are one recoverable unit.
STALE_OUT=$("$TIMEOUT_BIN" 300 "$BIN" work sweep-stale --threshold 168h --json 2>&1)
STALE_RC=$?
STALE_SUM=$(printf '%s' "$STALE_OUT" | python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
    print("safe=%s reclaimed=%s skipped=%s" % (d.get("safe"), d.get("reclaimed"), d.get("skipped")))
except Exception:
    print("unparseable")' 2>/dev/null)
[ "$STALE_RC" -ne 0 ] && STALE_SUM="ERR: $(printf '%.160s' "$STALE_OUT")"
[ "$STALE_SUM" = "unparseable" ] && STALE_RC=65 && STALE_SUM="ERR: unparseable JSON"

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
if [ "$DIR_RC" -ne 0 ] || [ "$ARCH_RC" -ne 0 ] || [ "$PD_RC" -ne 0 ] || [ "$STALE_RC" -ne 0 ] || [ "$CONT_RC" -ne 0 ]; then
  FINAL_RC=1
fi
echo "$TS final rc=$FINAL_RC | director rc=$DIR_RC $DIR_SUM | compaction rc=$ARCH_RC $ARCH_SUM | promote-driver rc=$PD_RC $PD_SUM | stale-work rc=$STALE_RC $STALE_SUM | continue rc=$CONT_RC $CONT_SUM" >> "$LOG"
exit "$FINAL_RC"
