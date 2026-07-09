package mem

const reconcileContestedArea = "reconcile-contested"

// SynthesisReconcileContestedCard is the deterministic operator-facing review
// surface for a split current position. It asks; it never promotes or supersedes.
type SynthesisReconcileContestedCard struct {
	ID         string                             `json:"id"`
	ClusterKey string                             `json:"cluster_key"`
	Entity     string                             `json:"entity"`
	Topic      string                             `json:"topic"`
	CreatedAt  string                             `json:"created_at"`
	Claims     []SynthesisReconcileContestedClaim `json:"claims"`
	ReviewRef  string                             `json:"review_ref"`
}

type SynthesisReconcileContestedClaim struct {
	ClaimID string `json:"claim_id"`
	Object  string `json:"object"`
	Source  string `json:"source"`
	Date    string `json:"date"`
}
