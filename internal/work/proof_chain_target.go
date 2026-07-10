package work

// ProofChainForWork applies the canonical terminal proof-chain checks to one
// archive-resolvable WorkEnvelope. It deliberately excludes the global orphan
// scan so consumers validating a named work are not coupled to unrelated
// historical findings. Callers that need a multi-read atomic snapshot must hold
// the COMPOUND_HOME autonomy-admission lock around this method and adjacent reads.
func (s *Store) ProofChainForWork(workID string) (ProofChainDoctorItem, error) {
	w, err := s.Get(workID)
	if err != nil {
		return ProofChainDoctorItem{}, err
	}
	report := ProofChainDoctorReport{}
	item := s.proofChainItem(w, &report)
	if len(item.Problems) == 0 {
		item.ProofComplete = true
	}
	return item, nil
}
