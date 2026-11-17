package decisions

import (
	"strings"
	"testing"
)

// (e) Job L1: the list prints each decision once and leaves retired ones out;
// the doctor counts retired records as superseded, not accepted.
func TestListAndDoctorCountRetiredRecords(t *testing.T) {
	claims := append(twoImportLedger(), retiringRuling("claim_retire", "retire-batch", 300, "DEC-0003"))

	listing := ListDecisions(claims, "")
	var active []string
	for _, rec := range listing.Active {
		active = append(active, rec.Ref+"="+rec.Claim.ID)
	}
	if strings.Join(active, " ") != "decision://DEC-0001=claim_dec1_new decision://retire-batch=claim_retire" {
		t.Fatalf("listing active = %v, want each active decision once (newest import)", active)
	}
	if len(listing.Retired) != 1 || listing.Retired[0].Ref != "decision://DEC-0003" || listing.NotAccepted != 0 {
		t.Fatalf("listing retired = %+v not_accepted=%d, want DEC-0003 once", listing.Retired, listing.NotAccepted)
	}
	if byRef := ActiveByRef(claims); len(byRef) != len(listing.Active) {
		t.Fatalf("listing active (%d) must match ActiveByRef (%d)", len(listing.Active), len(byRef))
	}
	if policies := ListDecisions(claims, KindPolicy); len(policies.Active) != 1 || len(policies.Retired) != 1 {
		t.Fatalf("kind filter: %+v", policies)
	}

	report := DoctorReportFromClaims(claims)
	if report.Statuses[StatusSuperseded] != 2 || report.RetiredByLaterRecord != 2 {
		t.Fatalf("doctor superseded=%d retired_by_later_record=%d, want both DEC-0003 imports (2)", report.Statuses[StatusSuperseded], report.RetiredByLaterRecord)
	}
	if report.Statuses[StatusAccepted] != 3 || report.Total != 5 {
		t.Fatalf("doctor accepted=%d total=%d, want 3 of 5", report.Statuses[StatusAccepted], report.Total)
	}
}

// Guard: when no accepted record names anything, nothing is retired and the
// doctor counts every accepted record as accepted.
func TestRetiredRefsEmptyWhenNothingNamed(t *testing.T) {
	claims := unnamedRetireLedger()
	if got := RetiredRefs(claims); len(got) != 0 {
		t.Fatalf("RetiredRefs = %v, want none", got)
	}
	if report := DoctorReportFromClaims(claims); report.RetiredByLaterRecord != 0 || report.Statuses[StatusSuperseded] != 0 {
		t.Fatalf("doctor counted a retirement nobody recorded: %+v", report.Statuses)
	}
}
