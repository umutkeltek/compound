package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/decisions"
	"compound/internal/mem"
)

// writeLegacyDecisionLedger writes the real ledger's shape into a temp home:
// each DEC-00xx page as an older legacy claim (claim id = DEC id, no
// decision_id) plus a newer import (decision_id = DEC id), and two
// knowledge-page decisions with no decision_id at all.
func writeLegacyDecisionLedger(t *testing.T, homePath string) {
	t.Helper()
	if _, err := mem.Open(homePath); err != nil {
		t.Fatal(err)
	}
	line := func(id string, seq int, object string, qualifiers string) string {
		return fmt.Sprintf(`{"id":%q,"seq":%d,"entity":"decisions","predicate":"decided","object":%q,"confidence":1,"derivation":"deterministic","claim_type":"decision","qualifiers":%s,"promotion_basis":"decision_record","visibility":"operational","created_at":"2026-06-29T01:04:19Z","valid_from":"2026-06-29T01:04:19Z"}`, id, seq, object, qualifiers)
	}
	lines := []string{
		line("DEC-0001", 1, "Keep decision one", `{"kind":"policy","source":"decisions/DEC-0001.md"}`),
		line("DEC-0003", 2, "Retire decision three", `{"kind":"policy","source":"decisions/DEC-0003.md"}`),
		line("claim_dec1_new", 3, "Keep decision one", `{"kind":"policy","decision_id":"DEC-0001","lifecycle_status":"accepted"}`),
		line("claim_dec3_new", 4, "Retire decision three", `{"kind":"policy","decision_id":"DEC-0003","lifecycle_status":"accepted"}`),
		line("claim_page_retire", 5, "Retire this page", `{"kind":"policy","source":"knowledge_page"}`),
		line("claim_page_keep", 6, "Keep this page", `{"kind":"policy","source":"knowledge_page"}`),
	}
	path := filepath.Join(homePath, "ledger", "claims.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// (e) Job L1: `compound decisions` prints each active decision once and leaves
// retired ones out with a count; `--all` still lists every record; the doctor
// counts the retired records as superseded; `show` no longer finds them.
func TestCmdDecisionsListAndDoctorLeaveRetiredRecordsOut(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	writeLegacyDecisionLedger(t, homePath)

	before := captureStdout(t, func() { cmdDecisions(nil) })
	if got := strings.Count(before, "] DEC-0001 - "); got != 1 {
		t.Fatalf("list printed DEC-0001 %d times, want once:\n%s", got, before)
	}
	if !strings.Contains(before, "- retired: 0 ") {
		t.Fatalf("list must always say how many are retired:\n%s", before)
	}

	captureStdout(t, func() {
		cmdDecisions([]string{"record", "--kind", "ruling", "--id", "retire-batch", "--title", "Retire stale records batch one",
			"--reviewed-by", "operator:test", "--undo-of", "DEC-0003,claim_page_retire"})
	})

	list := captureStdout(t, func() { cmdDecisions(nil) })
	for _, want := range []string{"- [policy/accepted] DEC-0001 - Keep decision one\n", "] claim_page_keep - ", "] retire-batch - ", "- retired: 2 ("} {
		if strings.Count(list, want) != 1 {
			t.Fatalf("list must print %q exactly once:\n%s", want, list)
		}
	}
	for _, gone := range []string{"DEC-0003", "claim_dec3_new", "claim_page_retire", "claim_dec1_new"} {
		if strings.Contains(list, gone) {
			t.Fatalf("list printed %q:\n%s", gone, list)
		}
	}

	all := captureStdout(t, func() { cmdDecisions([]string{"--all"}) })
	for _, want := range []string{"[policy/superseded] DEC-0003 ", "[policy/superseded] claim_dec3_new ", "[policy/superseded] claim_page_retire ", "[policy/accepted] DEC-0001 ", "[policy/accepted] claim_dec1_new "} {
		if !strings.Contains(all, want) {
			t.Fatalf("--all must list every record with its effective status, missing %q:\n%s", want, all)
		}
	}

	doctor := captureStdout(t, func() { cmdDecisions([]string{"doctor"}) })
	for _, want := range []string{"- accepted: 4\n", "- superseded: 3\n", "- retired_by_later_record: 3\n", "- current: 7\n"} {
		if !strings.Contains(doctor, want) {
			t.Fatalf("doctor must report %q:\n%s", strings.TrimSpace(want), doctor)
		}
	}

	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"DEC-0003", "decision://DEC-0003", "claim_dec3_new", "claim_page_retire"} {
		if rec, err := decisions.LoadActive(store, query); err == nil {
			t.Fatalf("decisions show %q still finds %s (%s)", query, rec.Ref, rec.Claim.ID)
		}
	}
	for query, wantClaim := range map[string]string{"DEC-0001": "claim_dec1_new", "claim_page_keep": "claim_page_keep", "retire-batch": ""} {
		rec, err := decisions.LoadActive(store, query)
		if err != nil || (wantClaim != "" && rec.Claim.ID != wantClaim) {
			t.Fatalf("decisions show %q = %+v, %v; want an active record", query, rec.Claim.ID, err)
		}
	}
}
