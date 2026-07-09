package policy

import "strings"

// Adjudication dispositions for the decisions-down write seam.
// operator_required → enters DECIDE / proposed queue
// decay_park        → parked with expiry; corroboration may raise later
// auto_file         → filed terminal (no_promote) with receipt — never ambient
const (
	DispositionOperatorRequired = "operator_required"
	DispositionDecayPark        = "decay_park"
	DispositionAutoFile         = "auto_file"
)

// DefaultDecayParkDays is the park-with-expiry window for un-evidenced candidates.
const DefaultDecayParkDays = 30

// AdjudicationInput is the minimum candidate shape the disposition resolver needs.
// No ledger types — keep policy free of mem imports.
type AdjudicationInput struct {
	Visibility     string
	ProvenanceLane string
	PromotionBasis string
	ClaimType      string
	CanonClass     string
	Source         string // Qualifiers["source"]
	Tags           []string
}

// AdjudicationDisposition is the write-seam outcome for a candidate.
type AdjudicationDisposition struct {
	Kind           string // operator_required | decay_park | auto_file
	Reason         string
	ReviewRequired bool
	ParkDays       int // set for decay_park; 0 means caller default
}

// ResolveAdjudication applies the decisions-down law at the write seam.
//
// Hard laws:
//   - privacy-sensitive items never auto_file
//   - cheap-lane never raises a tier (always decay_park when not knowledge-indexed)
//   - operator-permanent kinds (decision/principle/behavior claim types) stay operator_required
//   - un-evidenced durable memory decay_parks (does not inflate the DECIDE queue)
//
// Knowledge source tiers (import/extraction) are classified by the mem admission
// surface, not here — pass Source so callers can short-circuit before calling.
func ResolveAdjudication(in AdjudicationInput) AdjudicationDisposition {
	vis := strings.TrimSpace(strings.ToLower(in.Visibility))
	lane := strings.TrimSpace(strings.ToLower(in.ProvenanceLane))
	basis := strings.TrimSpace(in.PromotionBasis)
	claimType := strings.TrimSpace(in.ClaimType)
	canon := strings.TrimSpace(in.CanonClass)

	// Privacy never auto-files (escalations stay human).
	// Cheap-lane never raises: always decay_park unless operator-permanent.
	if isOperatorPermanentKind(claimType, canon) {
		return AdjudicationDisposition{
			Kind:           DispositionOperatorRequired,
			Reason:         "operator-permanent kind; human gate required",
			ReviewRequired: true,
		}
	}
	if basis == "explicit_confirmation" {
		return AdjudicationDisposition{
			Kind:           DispositionOperatorRequired,
			Reason:         "explicit confirmation basis; human gate required",
			ReviewRequired: true,
		}
	}
	// Measurement / deterministic import artifacts can auto-file when not private.
	if isAutoFileBasis(basis) && vis != "private" {
		return AdjudicationDisposition{
			Kind:           DispositionAutoFile,
			Reason:         "deterministic/measurement artifact; auto-filed with receipt, not operator queue",
			ReviewRequired: false,
		}
	}
	if vis == "private" && isAutoFileBasis(basis) {
		// Falsifier surface: private + auto_file-eligible basis → operator_required, never auto_file.
		return AdjudicationDisposition{
			Kind:           DispositionOperatorRequired,
			Reason:         "private visibility blocks auto-file; human gate required",
			ReviewRequired: true,
		}
	}
	if lane == "cheap-lane" || lane == "cheap_lane" {
		return AdjudicationDisposition{
			Kind:           DispositionDecayPark,
			Reason:         "cheap-lane output; park with expiry, never raises tier",
			ReviewRequired: false,
			ParkDays:       DefaultDecayParkDays,
		}
	}
	// Un-evidenced durable memory: park with expiry (decisions-down).
	return AdjudicationDisposition{
		Kind:           DispositionDecayPark,
		Reason:         "un-evidenced durable memory; park with expiry pending corroboration",
		ReviewRequired: false,
		ParkDays:       DefaultDecayParkDays,
	}
}

func isOperatorPermanentKind(claimType, canonClass string) bool {
	switch claimType {
	case "decision", "preference", "constraint", "architecture":
		return true
	}
	switch canonClass {
	case "decision", "principle", "behavior":
		return true
	}
	return false
}

func isAutoFileBasis(basis string) bool {
	switch basis {
	case "trusted_deterministic_import", "runtime_proof", "curated_page", "decision_record":
		return true
	default:
		return false
	}
}
