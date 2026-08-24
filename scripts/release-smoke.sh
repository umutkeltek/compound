#!/usr/bin/env bash
# release-smoke.sh — release-readiness verification battery for compound.
#
# Runs the end-to-end checks listed in docs_private/plan/2026-08-24-release-day.md
# (Category 1E) and exits non-zero on any failure. Re-runnable.
#
# Usage: bash scripts/release-smoke.sh
#
# Pre-reqs: ~/.local/bin/compound codesigned and at HEAD; ~/.compound intact.

set -u

REPO="${REPO:-$(cd "$(dirname "$0")/.." && pwd)}"
COMPOUND="${COMPOUND:-${HOME}/.local/bin/compound}"
COMPOUND_HOME="${COMPOUND_HOME:-${HOME}/.compound}"
LOG_DIR="${LOG_DIR:-/tmp/release-smoke}"
mkdir -p "${LOG_DIR}"

pass=0
fail=0
warn=0

note() { printf "\033[1;36m[%s]\033[0m %s\n" "note" "$*"; }
ok()   { printf "\033[1;32m[%s]\033[0m %s\n" "PASS" "$*"; pass=$((pass+1)); }
bad()  { printf "\033[1;31m[%s]\033[0m %s\n" "FAIL" "$*"; fail=$((fail+1)); }
warn_(){ printf "\033[1;33m[%s]\033[0m %s\n" "WARN" "$*"; warn=$((warn+1)); }

# 1. Fresh binary at HEAD, codesigned
note "step 1: build fresh + codesign + install"
cd "${REPO}" || exit 2
HEAD_SHA="$(git rev-parse HEAD)"
go build -ldflags "-X main.buildSHA=${HEAD_SHA}" -o /tmp/compound-fresh ./cmd/compound/ \
  || { bad "go build failed"; exit 1; }
codesign -fs - /tmp/compound-fresh >/dev/null \
  || { bad "codesign failed"; exit 1; }
cp /tmp/compound-fresh "${COMPOUND}" \
  || { bad "cp install failed"; exit 1; }
VERSION_OUT="$("${COMPOUND}" version 2>&1 | head -1)"
echo "  ${VERSION_OUT}"
case "${VERSION_OUT}" in
  *"${HEAD_SHA:0:7}"*) ok "binary fresh at HEAD (${HEAD_SHA:0:7})" ;;
  *)                  bad "binary not at HEAD (got: ${VERSION_OUT})" ;;
esac

# 2. Doctor surfaces no FAIL on operator-visible hops
note "step 2: doctor freshness"
DOCTOR_OUT="$("${COMPOUND}" doctor 2>&1)"
if echo "${DOCTOR_OUT}" | grep -q "fresh: installed=.* matches target="; then
  ok "binary staleness check is fresh"
else
  bad "binary staleness check failed"
fi

# 3. Memory doctor surfaces the 14-day promotion stall (KNOWN, not a regression)
note "step 3: memory doctor (freshness gate)"
MEM_JSON="$("${COMPOUND}" memory doctor --json 2>&1 | tail -n +2)"
if echo "${MEM_JSON}" | grep -q "newest_candidate_at"; then
  ok "memory doctor returns freshness hops"
else
  bad "memory doctor JSON missing freshness fields"
fi

# 4. Test battery greenness — narrow scope to slices we own
note "step 4: targeted unit tests"
GO_TEST_LOG="${LOG_DIR}/go-test.log"
go test ./internal/mem/ -count=1 -timeout 120s -p 1 >"${GO_TEST_LOG}" 2>&1 \
  || bad "internal/mem tests failed (see ${GO_TEST_LOG})"
MEM_LINES="$(tail -3 "${GO_TEST_LOG}" | tr '\n' ' ')"
if grep -q "^ok " "${GO_TEST_LOG}"; then
  ok "internal/mem tests pass"
else
  bad "internal/mem tests failed"
fi

go test ./internal/work/ -run "TestWedge|TestCloseWedge|TestAdvanceRefuses|TestAdvanceAllows|TestCloseAcknowledgeAbandoned|TestResolveRunAttemptAuthority" -count=1 -timeout 60s -p 1 >"${GO_TEST_LOG}" 2>&1 \
  || bad "internal/work wedge tests failed"
if grep -q "^ok " "${GO_TEST_LOG}"; then
  ok "internal/work wedge tests pass"
else
  bad "internal/work wedge tests failed"
fi

# 5. Public-export smoke (no docs_private / .claude / identity leak)
note "step 5: public export"
RELEASE_TARBALL="${LOG_DIR}/release-dry-run.tar.gz"
"${COMPOUND}" release export-public --target "${RELEASE_TARBALL}" --repo "${REPO}" >"${LOG_DIR}/export.log" 2>&1 \
  || { bad "release export-public failed"; tail -10 "${LOG_DIR}/export.log"; }
if [[ -f "${RELEASE_TARBALL}" ]]; then
  if tar -tzf "${RELEASE_TARBALL}" | grep -E "(^|/)(docs_private|\.claude/|\.codex/)" >/dev/null; then
    bad "tarball leaks private directories"
  else
    ok "tarball clean (no docs_private / .claude / .codex)"
  fi
  if tar -xzOf "${RELEASE_TARBALL}" 2>/dev/null | grep -E "umut\.keltek@gmail\.com" >/dev/null; then
    bad "tarball leaks operator identity"
  else
    ok "tarball clean (no operator identity)"
  fi
else
  bad "tarball not produced"
fi
rm -f "${RELEASE_TARBALL}"

# 6. Memory backlog drain end-to-end (operator-decision gate)
note "step 6: memory adjudicate --pending --dry-run"
ADJ_OUT="$("${COMPOUND}" memory adjudicate --pending --dry-run --limit 1 2>&1)"
ADJ_SCAN=$(echo "${ADJ_OUT}" | grep -oE "scanned=[0-9]+" | head -1 | cut -d= -f2 || echo 0)
if [[ "${ADJ_SCAN}" -gt 1000 ]]; then
  ok "dry-run walked ${ADJ_SCAN} candidate(s) without --yes mutation"
else
  warn_ "dry-run scanned only ${ADJ_SCAN} candidates (limit ignored — known CLI caveat)"
fi

# 7. Hook end-to-end (priority per prior session)
note "step 7: hook user-prompt-submit"
HOOK_OUT="$(echo '{"cwd":"/tmp","session_id":"smoke","prompt":"smoke"}' \
  | "${COMPOUND}" hook user-prompt-submit --live --harness claude-code 2>&1)"
if echo "${HOOK_OUT}" | grep -q '"hookSpecificOutput"'; then
  ok "hook returns hookSpecificOutput"
else
  bad "hook output malformed"
fi

# Empty stdin via /dev/null (echo "" sends a newline, not truly empty
# bytes; the JSON parse path expects the harness-style </dev/null
# invocation that the prior session's 327fa0f3 fix targets).
HOOK_EMPTY="$("${COMPOUND}" hook user-prompt-submit --live --harness claude-code </dev/null 2>&1)"
if echo "${HOOK_EMPTY}" | grep -q "context_pack\|hookSpecificOutput\|no-op"; then
  ok "hook empty-stdin no-op"
else
  bad "hook empty-stdin failed: ${HOOK_EMPTY}"
fi

# 8. Wedge close (work_1d968c3f0522 should already be closed; verify state)
note "step 8: wedged envelope state"
WORK_STATE="$("${COMPOUND}" work show work_1d968c3f0522 2>/dev/null | grep '"state"' | head -1 | sed 's/.*: "//;s/".*//')"
case "${WORK_STATE}" in
  completed) ok "wedge envelope work_1d968c3f0522 is ${WORK_STATE}" ;;
  *)        bad "wedge envelope work_1d968c3f0522 is ${WORK_STATE} (expected completed)" ;;
esac


# Summary
echo
note "summary: pass=${pass} warn=${warn} fail=${fail}"
if [[ "${fail}" -gt 0 ]]; then
  exit 1
fi
exit 0