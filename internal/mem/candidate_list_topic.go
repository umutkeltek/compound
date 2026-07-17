// candidate_list_topic.go — work_c13acdc090b5 (S3b): exports the candidate-side topic axis on
// the admitted CandidateListItem projection, mirroring Candidate.Topic() (clusterkey.go) and
// Claim.Topic() (mem.go). The S3b stranger dealer (cmd/compound/orient_operator.go) selects its
// pool via Store.ListCandidates — the packet's named "query surface for stranger pool" — which
// returns authority-admitted CandidateListItem values, not raw Candidate structs; this accessor
// lets the dealer compute a pool member's topic without reaching into mem's private
// topicFromQualifiers helper.
package mem

// Topic is the candidate-side topic axis for an admitted candidate-list projection item:
// domain qualifier first, predicate fallback (identical rule to Candidate.Topic()).
func (item CandidateListItem) Topic() string {
	return topicFromQualifiers(item.Predicate, item.Qualifiers)
}
