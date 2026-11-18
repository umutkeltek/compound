package work

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"compound/internal/envelope"
)

// A completed receipt can be read back as a verification on a later advance.
// Publication and incomplete transaction recovery still require an exclusive producer row.
func (s *Store) historicalReceiptedActionEvent(events []Event, action envelope.ActionIntent, decision envelope.PolicyDecision, receipt envelope.Receipt, transition envelope.Transition) (Event, error) {
	return completedReceiptedActionEvent(events, action, decision, receipt, transition, func(i int) bool {
		return events[i].WorkID == action.IDs.WorkID && s.isVerifyingReceiptCitation(events, i, action.ID, receipt.ID)
	})
}

func (s *Store) isVerifyingReceiptCitation(events []Event, index int, producerID, receiptID string) bool {
	event := events[index]
	refs := event.Attached
	if event.From != "running" && event.From != "verifying" || event.To != "verifying" ||
		len(refs) < 3 {
		return false
	}
	w, err := s.Get(event.WorkID)
	if err != nil || !slices.Contains(w.VerificationIDs, receiptID) || slices.Contains(w.ArtifactIDs, receiptID) {
		return false
	}
	if !s.verificationAttachment(w, refs[2:], receiptID) || !verifyingReceiptOrder(w, events, receiptID) {
		return false
	}
	producer := slices.Index(w.ActionIntentIDs, producerID)
	advance := slices.Index(w.ActionIntentIDs, refs[0])
	if producer < 0 || advance <= producer {
		return false
	}
	for i, other := range events {
		if i != index && len(other.Attached) >= 2 && (other.Attached[0] == refs[0] || other.Attached[1] == refs[1]) {
			return false
		}
	}

	// The native timestamp identifies this ordinary advance, independently of its note.
	nanos, err := strconv.ParseInt(event.TS, 10, 64)
	if err != nil {
		return false
	}
	historical := w
	historical.State = event.From
	var issued envelope.ActionIntent
	if err := s.readEventJSON(refs[0], &issued); err != nil {
		return false
	}
	expected := s.advanceActionIntent(historical, event.By, event.To, time.Unix(0, nanos), AdvancePlanOptions{})
	if refs[0] != expected.ID || issued.Preview != expected.Preview || !strings.HasPrefix(event.PreviousWorkSHA256, "sha256:") ||
		!validProofDigest(strings.TrimPrefix(event.PreviousWorkSHA256, "sha256:")) ||
		!strings.HasPrefix(event.WorkSHA256, "sha256:") || !validProofDigest(strings.TrimPrefix(event.WorkSHA256, "sha256:")) ||
		event.PreviousWorkSHA256 == event.WorkSHA256 {
		return false
	}
	historical.State = event.To
	historical.PolicyDecisionID = refs[1]
	if err := s.validatePendingAdvance(event, historical); err != nil {
		return false
	}

	// Check the post-image wherever the adjacent generation carries a seal. Some
	// native producers (including receipted actions) publish an unsealed event.
	if advance == len(w.ActionIntentIDs)-1 {
		digest, err := workEnvelopeSHA256(w)
		return err == nil && digest == event.WorkSHA256
	}
	next := w.ActionIntentIDs[advance+1]
	for _, successor := range events[index+1:] {
		if len(successor.Attached) > 0 && successor.Attached[0] == next {
			return successor.PreviousWorkSHA256 == "" || successor.PreviousWorkSHA256 == event.WorkSHA256
		}
	}
	return false
}

// Ordinary advance writes evidence, new attempts, an optional artifact, one
// verification and an optional context pack, in that order.
func (s *Store) verificationAttachment(w envelope.WorkEnvelope, refs []string, receiptID string) bool {
	slot := slices.Index(refs, receiptID)
	if slot < 0 || slices.Contains(refs[slot+1:], receiptID) {
		return false
	}
	tail := refs[slot+1:]
	if len(tail) > 1 || len(tail) == 1 && (!strings.HasPrefix(tail[0], "ctx_") || filepath.Base(tail[0]) != tail[0] || s.validateContextPackRef(tail[0]) != nil) {
		return false
	}
	prefix := refs[:slot]
	if n := len(prefix); n > 0 && slices.Contains(w.ArtifactIDs, prefix[n-1]) {
		prefix = prefix[:n-1]
	}
	for len(prefix) > 0 && slices.Contains(w.EvidenceRefs, prefix[0]) {
		prefix = prefix[1:]
	}
	for _, id := range prefix {
		if !slices.Contains(w.RunAttemptIDs, id) {
			return false
		}
	}
	return true
}

// Verify receipt citations against the separately persisted, ordered verification
// list. An authentic advance and policy cannot be reused to claim another
// advance's verification. Other publication kinds can interleave their own refs.
func verifyingReceiptOrder(w envelope.WorkEnvelope, events []Event, receiptID string) bool {
	remaining := w.VerificationIDs
	observed := 0
	for _, event := range events {
		if event.WorkID != w.ID || event.To != "verifying" || event.WorkSHA256 == "" || len(event.Attached) < 3 {
			continue
		}
		refs := event.Attached[2:]
		if n := len(refs); n > 1 && strings.HasPrefix(refs[n-1], "ctx_") {
			refs = refs[:n-1]
		}
		last := refs[len(refs)-1]
		if !strings.HasPrefix(last, "rec_") || !slices.Contains(w.VerificationIDs, last) || slices.Contains(w.ArtifactIDs, last) || slices.Contains(w.EvidenceRefs, last) {
			continue
		}
		pos := slices.Index(remaining, last)
		if pos < 0 {
			return false
		}
		remaining = remaining[pos+1:]
		if last == receiptID {
			observed++
		}
	}
	expected := 0
	for _, ref := range w.VerificationIDs {
		if ref == receiptID {
			expected++
		}
	}
	return observed == expected
}
