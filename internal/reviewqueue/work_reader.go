package reviewqueue

import (
	"compound/internal/envelope"
	"compound/internal/work"
)

// reviewWorkReader is the one work-store read the authority stamp needs.
// *work.Store satisfies it directly.
type reviewWorkReader interface {
	Get(workID string) (envelope.WorkEnvelope, error)
}

// memoWorkReader answers repeated reads of one work ID within one stamping
// pass from the first read. Many review items name the same source or area
// fallback work; when that work is archived or missing, every store Get reloads
// the whole operational archive index, so a crowded area paid that reload once
// per item. The pass is read-only and short, so one read per ID is the same
// snapshot the per-item reads would have seen on a home that is not changing.
type memoWorkReader struct {
	store *work.Store
	reads map[string]memoWorkRead
}

type memoWorkRead struct {
	work envelope.WorkEnvelope
	err  error
}

// newReviewWorkReader returns the memoizing reader, or the store itself when
// memo is false (the reference path the same-output tests compare against).
func newReviewWorkReader(store *work.Store, memo bool) reviewWorkReader {
	if !memo {
		return store
	}
	return &memoWorkReader{store: store, reads: map[string]memoWorkRead{}}
}

func (r *memoWorkReader) Get(workID string) (envelope.WorkEnvelope, error) {
	if read, ok := r.reads[workID]; ok {
		return read.work, read.err
	}
	w, err := r.store.Get(workID)
	r.reads[workID] = memoWorkRead{work: w, err: err}
	return w, err
}
