package routeevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
)

const RouteComparisonContractSchemaVersion = "compound.route_comparison_contract.v0.1"

// RouteComparisonContract pins the population and acceptance identities. Its
// immutable artifact reference identifies these inputs, not their authenticity.
type RouteComparisonContract struct {
	SchemaVersion  string                        `json:"schema_version"`
	MinRepetitions int                           `json:"min_repetitions"`
	Cases          []RouteComparisonContractCase `json:"cases"`
}
type RouteComparisonContractCase struct {
	CaseID           string `json:"case_id"`
	InputDigest      string `json:"input_digest"`
	AcceptanceDigest string `json:"acceptance_digest"`
	Critical         bool   `json:"critical"`
}

// RouteComparisonCaseRef binds one case's inputs, acceptance and criticality.
// Executors can include this token and the contract artifact ref in their
// existing route evidence and receipt proof refs; no separate store is needed.
func RouteComparisonCaseRef(c RouteComparisonContractCase) string {
	body, _ := json.Marshal(c)
	digest := sha256.Sum256(body)
	return "route-comparison-case:sha256:" + hex.EncodeToString(digest[:])
}

type comparisonVerification struct {
	seal    string
	reasons []string
}

func comparisonInputSeal(input RouteComparisonInput) string {
	body, _ := json.Marshal(input) // The private verification field is excluded.
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func comparisonVerificationReasons(input RouteComparisonInput) []string {
	if input.verification == nil {
		return []string{"comparison evidence is unverified: resolve a pinned contract and owned route/receipt bindings"}
	}
	if input.verification.seal != comparisonInputSeal(input) {
		return []string{"comparison evidence is unverified: resolved inputs changed after validation"}
	}
	return input.verification.reasons
}

// ResolveRouteComparisonInput verifies the evidence that the public input only
// declares. Missing evidence is an explicit assessment blocker, not authority to
// trust inline copies. The result remains advisory and is not billing truth.
func ResolveRouteComparisonInput(homePath string, input RouteComparisonInput) RouteComparisonInput {
	input.verification = nil
	// Do not share mutable caller slices while deriving owned facts.
	input.Cases = append([]RouteComparisonCaseInput(nil), input.Cases...)
	var reasons []string
	block := func(s string) { reasons = append(reasons, "comparison evidence is unverified: "+s) }
	deferSeal := func() RouteComparisonInput {
		input.verification = &comparisonVerification{reasons: uniqueSortedStrings(reasons)}
		input.verification.seal = comparisonInputSeal(input)
		return input
	}
	if _, err := home.ArtifactHex(input.EvaluationContractRef); err != nil {
		block("a pinned evaluation contract artifact is required")
		return deferSeal()
	}
	exists, err := home.RootedDirectoryExists(homePath, filepath.Join(homePath, "artifacts", "sha256"), "route comparison contract")
	if err != nil || !exists {
		block("evaluation contract custody is unavailable")
		return deferSeal()
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		block("open evaluation contract custody: " + err.Error())
		return deferSeal()
	}
	body, err := artifacts.Get(input.EvaluationContractRef)
	if err != nil {
		block("load evaluation contract: " + err.Error())
		return deferSeal()
	}
	var contract RouteComparisonContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&contract); err != nil {
		block("invalid evaluation contract: " + err.Error())
		return deferSeal()
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		block("evaluation contract contains trailing data")
		return deferSeal()
	}
	declared, err := validateRouteComparisonContract(contract)
	if err != nil {
		block(err.Error())
		return deferSeal()
	}
	if input.MinRepetitions != 0 && input.MinRepetitions != contract.MinRepetitions {
		block("minimum repetitions disagree with the pinned evaluation contract")
	}
	input.MinRepetitions = contract.MinRepetitions
	critical := []string{}
	for _, c := range contract.Cases {
		if c.Critical {
			critical = append(critical, c.CaseID)
		}
	}
	if input.CriticalCases != nil && !sameComparisonStrings(input.CriticalCases, critical) {
		block("critical case declaration disagrees with the pinned evaluation contract")
	}
	input.CriticalCases = critical
	seenCases := map[string]bool{}
	seenRequests := map[string]bool{}
	for i, c := range input.Cases {
		if seenCases[c.CaseID] {
			block("duplicate case " + c.CaseID)
		}
		seenCases[c.CaseID] = true
		required, ok := declared[c.CaseID]
		if !ok {
			block("case " + c.CaseID + " is outside the pinned evaluation contract")
			continue
		}
		bind := func(reviews []RouteOutcomeReview) []RouteOutcomeReview {
			out := append([]RouteOutcomeReview(nil), reviews...)
			for j, supplied := range reviews {
				ev, err := LoadEvidenceByID(homePath, supplied.RouteEvidenceID)
				if err != nil {
					block("case " + c.CaseID + " route evidence cannot be resolved: " + err.Error())
					continue
				}
				rec, err := LoadReceiptByID(homePath, supplied.ReceiptID)
				if err != nil {
					block("case " + c.CaseID + " receipt cannot be resolved: " + err.Error())
					continue
				}
				if ev.WorkID == "" || rec.IDs.WorkID == "" || ev.WorkID != rec.IDs.WorkID {
					block("case " + c.CaseID + " requires exact nonempty route/receipt work identity")
					continue
				}
				refs := comparisonReceiptRefs(rec)
				if !containsRef(refs, ev.ID) && !containsRef(ev.EvidenceRefs, rec.ID) {
					block("case " + c.CaseID + " receipt is not explicitly cross-linked to its route evidence")
				}
				for _, ref := range []string{input.EvaluationContractRef, RouteComparisonCaseRef(required)} {
					if !containsRef(ev.EvidenceRefs, ref) || !containsRef(refs, ref) {
						block("case " + c.CaseID + " lacks matching pinned contract/case bindings in both owned records")
					}
				}
				if ev.RecordedVia == RecorderExternalImport {
					block("case " + c.CaseID + " route evidence is an unverified external import")
				}
				if seenRequests[ev.RequestID] {
					block("case " + c.CaseID + " repeats owned execution request " + ev.RequestID)
				}
				seenRequests[ev.RequestID] = true
				at, _ := time.Parse(time.RFC3339, ev.CreatedAt)
				actual, err := BuildRouteOutcomeReview(ev, rec, "", at)
				if err != nil {
					block("case " + c.CaseID + " owned outcome is invalid: " + err.Error())
					continue
				}
				if !sameComparisonReviewFacts(supplied, actual) {
					block("case " + c.CaseID + " supplied review disagrees with owned route/receipt facts")
				}
				out[j] = actual
			}
			return out
		}
		input.Cases[i].Incumbent = bind(c.Incumbent)
		input.Cases[i].Candidate = bind(c.Candidate)
	}
	for _, required := range contract.Cases {
		if !seenCases[required.CaseID] {
			block("required contract case " + required.CaseID + " was not evaluated")
		}
	}
	return deferSeal()
}

func comparisonReceiptRefs(rec envelope.Receipt) []string {
	refs := append([]string{}, rec.EvidenceRefs...)
	refs = append(refs, rec.ProofRefs...)
	refs = append(refs, rec.StateDelta.Verification...)
	return append(refs, rec.StateDelta.Artifacts...)
}

func sameComparisonReviewFacts(a, b RouteOutcomeReview) bool {
	return a.SchemaVersion == b.SchemaVersion && a.ID == b.ID &&
		a.RouteEvidenceID == b.RouteEvidenceID && a.ReceiptID == b.ReceiptID &&
		a.RequestID == b.RequestID && a.WorkID == b.WorkID &&
		a.ReceiptOutcome == b.ReceiptOutcome && a.ReceiptMoveType == b.ReceiptMoveType &&
		a.ReceiptStateChanged == b.ReceiptStateChanged && a.RouteStatus == b.RouteStatus &&
		a.SelectedTarget == b.SelectedTarget && a.Usage == b.Usage &&
		a.CostMicros == b.CostMicros && a.LatencyMS == b.LatencyMS
}

func validateRouteComparisonContract(c RouteComparisonContract) (map[string]RouteComparisonContractCase, error) {
	if c.SchemaVersion != RouteComparisonContractSchemaVersion || c.MinRepetitions < 1 || len(c.Cases) == 0 {
		return nil, fmt.Errorf("evaluation contract requires its supported schema, positive repetitions and nonempty population")
	}
	out := map[string]RouteComparisonContractCase{}
	validDigest := func(s string) bool {
		b, err := hex.DecodeString(s)
		return err == nil && len(b) == sha256.Size && strings.ToLower(s) == s
	}
	for _, item := range c.Cases {
		if item.CaseID == "" || item.CaseID != strings.TrimSpace(item.CaseID) || out[item.CaseID].CaseID != "" || !validDigest(item.InputDigest) || !validDigest(item.AcceptanceDigest) {
			return nil, fmt.Errorf("evaluation contract has duplicate/invalid case identity or input/acceptance digest")
		}
		out[item.CaseID] = item
	}
	return out, nil
}
func sameComparisonStrings(a, b []string) bool {
	a = uniqueSortedStrings(a)
	b = uniqueSortedStrings(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func strictComparisonJSON(body []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("comparison input contains trailing JSON")
	}
	return nil
}
