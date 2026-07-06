package work

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"compound/internal/envelope"
)

const workHoldConditionStale = "condition_stale"

type holdEvidenceRef struct {
	Ref         string
	CreatedAt   string
	SourceKind  string
	Summary     string
	MatchedRefs []string
}

type holdEvidenceIndex struct {
	byToken map[string][]holdEvidenceRef
}

func (s *Store) workHoldEvidenceIndex() (holdEvidenceIndex, []string) {
	idx := holdEvidenceIndex{byToken: map[string][]holdEvidenceRef{}}
	var warnings []string
	warnings = append(warnings, s.addReceiptHoldEvidence(&idx)...)
	warnings = append(warnings, s.addRunObservationHoldEvidence(&idx)...)
	return idx, warnings
}

func (s *Store) addReceiptHoldEvidence(idx *holdEvidenceIndex) []string {
	dir := filepath.Join(s.home, "events")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{"hold evidence receipts unreadable: " + err.Error()}
	}
	var warnings []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var header struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := s.readJSON(path, &header); err != nil {
			warnings = append(warnings, "hold evidence receipt "+entry.Name()+" unreadable: "+err.Error())
			continue
		}
		if header.SchemaVersion != envelope.SVReceipt {
			continue
		}
		var receipt envelope.Receipt
		if err := s.readJSON(path, &receipt); err != nil {
			warnings = append(warnings, "hold evidence receipt "+entry.Name()+" unreadable: "+err.Error())
			continue
		}
		if err := receipt.Validate(); err != nil {
			warnings = append(warnings, "hold evidence receipt "+entry.Name()+" invalid: "+err.Error())
			continue
		}
		tokens := evidenceRefTokens(receipt.EvidenceRefs...)
		tokens = mergeUnique(tokens, evidenceRefTokens(receipt.ProofRefs...))
		tokens = mergeUnique(tokens, evidenceRefTokens(receipt.StateDelta.Artifacts...))
		tokens = mergeUnique(tokens, evidenceRefTokens(receipt.StateDelta.Verification...))
		idx.add(tokens, holdEvidenceRef{
			Ref:        receipt.ID,
			CreatedAt:  receipt.CreatedAt,
			SourceKind: "receipt",
			Summary:    receipt.StateDelta.Summary,
		})
	}
	return warnings
}

func (s *Store) addRunObservationHoldEvidence(idx *holdEvidenceIndex) []string {
	dir := filepath.Join(s.home, "run-observations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{"hold evidence observations unreadable: " + err.Error()}
	}
	var warnings []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		var result RunObservationResult
		if err := s.readJSON(path, &result); err != nil {
			warnings = append(warnings, "hold evidence observation "+entry.Name()+" unreadable: "+err.Error())
			continue
		}
		tokens := evidenceRefTokens(
			result.ID,
			result.TraceID,
			result.ActionIntentID,
			result.PolicyDecisionID,
			result.Note,
			result.Trace.ID,
			result.Trace.Source.RawRef,
			result.Trace.Summary,
		)
		tokens = mergeUnique(tokens, evidenceRefTokens(result.Trace.EvidenceRefs...))
		tokens = mergeUnique(tokens, evidenceRefTokens(result.Trace.Outputs...))
		idx.add(tokens, holdEvidenceRef{
			Ref:        firstNonEmpty(result.ID, result.TraceID),
			CreatedAt:  result.GeneratedAt,
			SourceKind: "run_observation",
			Summary:    firstNonEmpty(result.Note, result.Trace.Summary),
		})
	}
	return warnings
}

func (idx holdEvidenceIndex) add(tokens []string, evidence holdEvidenceRef) {
	if idx.byToken == nil || strings.TrimSpace(evidence.Ref) == "" {
		return
	}
	for _, token := range tokens {
		key := holdConditionKey(token)
		if key == "" {
			continue
		}
		idx.byToken[key] = append(idx.byToken[key], evidence)
	}
}

func markStaleHoldReviewCards(w envelope.WorkEnvelope, item WorkbenchItem, cards []WorkbenchReviewCard, evidence holdEvidenceIndex) []WorkbenchReviewCard {
	if len(cards) == 0 || (w.State != "held" && w.State != "blocked") {
		return cards
	}
	out := append([]WorkbenchReviewCard(nil), cards...)
	for i := range out {
		refs := holdReviewCardConditionRefs(w, item, out[i])
		match, ok := evidence.match(refs)
		if !ok {
			continue
		}
		out[i].ConditionStatus = workHoldConditionStale
		out[i].TriggeringEvidenceRef = match.Ref
		out[i].MatchedConditionRefs = append([]string{}, match.MatchedRefs...)
		out[i].NextAction = staleHoldNextAction(out[i], match)
	}
	return out
}

func (idx holdEvidenceIndex) match(refs []string) (holdEvidenceRef, bool) {
	if idx.byToken == nil || len(refs) == 0 {
		return holdEvidenceRef{}, false
	}
	seen := map[string]bool{}
	var matches []holdEvidenceRef
	for _, ref := range refs {
		key := holdConditionKey(ref)
		if key == "" {
			continue
		}
		for _, match := range idx.byToken[key] {
			dedupe := match.Ref + "\x00" + key
			if seen[dedupe] {
				continue
			}
			seen[dedupe] = true
			match.MatchedRefs = mergeUnique(match.MatchedRefs, []string{ref})
			matches = append(matches, match)
		}
	}
	if len(matches) == 0 {
		return holdEvidenceRef{}, false
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].CreatedAt != matches[j].CreatedAt {
			return matches[i].CreatedAt > matches[j].CreatedAt
		}
		return matches[i].Ref > matches[j].Ref
	})
	best := matches[0]
	for _, match := range matches[1:] {
		if match.Ref == best.Ref {
			best.MatchedRefs = mergeUnique(best.MatchedRefs, match.MatchedRefs)
		}
	}
	return best, true
}

func holdReviewCardConditionRefs(w envelope.WorkEnvelope, item WorkbenchItem, card WorkbenchReviewCard) []string {
	refs := []string{
		card.ID,
		"workbench_review_card:" + card.ID,
	}
	if card.GateID != "" {
		refs = append(refs, card.GateID, "gate:"+card.GateID, "operator_gate:"+card.GateID)
	}
	refs = append(refs, w.EvidenceRefs...)
	refs = append(refs, w.VerificationIDs...)
	refs = append(refs, item.EvidenceRefs...)
	refs = append(refs, item.VerificationIDs...)
	refs = append(refs, explicitRefTokens(card.RequiredInputs...)...)
	refs = append(refs, explicitRefTokens(card.ExpectedValidation...)...)
	refs = append(refs, explicitRefTokens(card.StopConditions...)...)
	refs = append(refs, explicitRefTokens(card.Boundary, card.Proof)...)
	return mergeUnique(refs)
}

func staleHoldNextAction(card WorkbenchReviewCard, evidence holdEvidenceRef) string {
	if card.Kind == "source_private_config_gate" && card.GateID == "asc-private-config" {
		return "condition stale: credential/config evidence exists; inspect normalized output without apply, apply one bounded signal, or retire this hold"
	}
	if evidence.Summary != "" {
		return "condition stale: inspect " + evidence.Ref + ", then operator chooses whether to advance, replace, or retire this hold"
	}
	return "condition stale: inspect triggering evidence, then operator chooses whether to advance, replace, or retire this hold"
}

func evidenceRefTokens(values ...string) []string {
	var out []string
	for _, value := range values {
		out = append(out, strings.TrimSpace(value))
		out = append(out, explicitRefTokens(value)...)
	}
	return mergeUnique(out)
}

func explicitRefTokens(values ...string) []string {
	var out []string
	for _, value := range values {
		fields := strings.FieldsFunc(value, func(r rune) bool {
			switch r {
			case ' ', '\t', '\n', '\r', ',', ';', '"', '\'', '`', '[', ']', '(', ')', '{', '}':
				return true
			default:
				return false
			}
		})
		for _, field := range fields {
			token := strings.Trim(field, ".")
			if token == "" {
				continue
			}
			if strings.Contains(token, ":") || strings.Contains(token, "_") || strings.Contains(token, "=") {
				out = append(out, token)
			}
			if before, _, ok := strings.Cut(token, "="); ok {
				out = append(out, before)
			}
		}
	}
	return mergeUnique(out)
}

func holdConditionKey(ref string) string {
	return strings.ToLower(strings.TrimSpace(ref))
}

func heldReleaseOperatorError(from, to, actor string) error {
	if from != "held" || to == "held" {
		return nil
	}
	if hasOperatorProvenance(actor) {
		return nil
	}
	return fmt.Errorf("held work release %s -> %s requires operator provenance", from, to)
}

func hasOperatorProvenance(actor string) bool {
	actor = strings.ToLower(strings.TrimSpace(actor))
	return actor == "operator" ||
		strings.HasPrefix(actor, "operator-") ||
		strings.HasPrefix(actor, "operator:") ||
		strings.HasPrefix(actor, "human-") ||
		strings.HasPrefix(actor, "human:")
}
