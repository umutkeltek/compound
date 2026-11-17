package work

import (
	"errors"
	"os"
)

// ReadLease returns the live lease of one work item, read-only. held is false
// and err is nil when the work has no lease. It reads only that work's lease
// file, so a hot path such as a post-tool hook can check one holder without
// ListLeases, which reads every lease of the home and fails on any bad one.
func (s *Store) ReadLease(workID string) (lease Lease, held bool, err error) {
	lease, err = s.readLease(workID)
	if errors.Is(err, os.ErrNotExist) {
		return Lease{}, false, nil
	}
	if err != nil {
		return Lease{}, false, err
	}
	return lease, true, nil
}
