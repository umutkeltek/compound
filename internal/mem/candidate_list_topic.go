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

// CandidateTopicMembership returns every topic axis that can place an admitted candidate in a
// taxonomy subtree: its domain/predicate fallback plus its explicit concept TopicRefs. The
// caller receives a normalized copy and cannot mutate candidate authority.
func (s *Store) CandidateTopicMembership(candidateID string) ([]string, error) {
	candidate, err := s.getCandidate(candidateID)
	if err != nil {
		return nil, err
	}
	return normalizeStringRefs(append([]string{candidate.Topic()}, candidate.TopicRefs...)), nil
}
