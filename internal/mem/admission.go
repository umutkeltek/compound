package mem

import "strings"

const (
	AdmissionClassDurableMemory     = "durable_memory_candidate"
	AdmissionClassSourceMetadata    = "source_metadata"
	AdmissionClassAnnotatedEvidence = "annotated_evidence"

	AdmissionSurfaceOperatorQueue     = "operator_queue"
	AdmissionSurfaceSourceIndex       = "source_index"
	AdmissionSurfaceAnnotatedEvidence = "annotated_evidence"
)

type CandidateAdmission struct {
	Class          string
	State          string
	Reason         string
	ReviewRequired bool
	TargetSurface  string
}

func AdmitCandidate(c Candidate) (Candidate, CandidateAdmission) {
	decision := ClassifyCandidateAdmission(c)
	if strings.TrimSpace(c.State) == "" || strings.TrimSpace(c.State) == "proposed" {
		c.State = decision.State
	}
	if strings.TrimSpace(c.State) == "" {
		c.State = "proposed"
	}
	if strings.TrimSpace(c.Reason) == "" && c.State == decision.State && strings.TrimSpace(decision.Reason) != "" {
		c.Reason = decision.Reason
	}
	return c, decision
}

func ClassifyCandidateAdmission(c Candidate) CandidateAdmission {
	switch strings.TrimSpace(c.Qualifiers["source"]) {
	case "knowledge_import":
		return CandidateAdmission{
			Class:          AdmissionClassSourceMetadata,
			State:          "parked",
			Reason:         "source metadata; source-index tier, not operator approval",
			ReviewRequired: false,
			TargetSurface:  AdmissionSurfaceSourceIndex,
		}
	case "knowledge_extraction":
		return CandidateAdmission{
			Class:          AdmissionClassAnnotatedEvidence,
			State:          "parked",
			Reason:         "knowledge extraction evidence; annotated-evidence tier, not operator approval",
			ReviewRequired: false,
			TargetSurface:  AdmissionSurfaceAnnotatedEvidence,
		}
	default:
		return CandidateAdmission{
			Class:          AdmissionClassDurableMemory,
			State:          "proposed",
			ReviewRequired: true,
			TargetSurface:  AdmissionSurfaceOperatorQueue,
		}
	}
}
