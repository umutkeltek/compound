// Package ledger is the append-only canonical store — THE authority. The rented
// engine is a materialized view rebuildable from here (compound rebuild). If the
// engine dies, the ledger survives. See docs/memory-architecture.md.
package ledger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Kinds are the canonical object families, one append-only file each.
var Kinds = []string{"evidence", "entities", "claims", "positions", "patterns", "edges", "scope_assignments"}

type Ledger struct {
	root string
	mu   sync.Mutex
}

// Open ensures the ledger root exists and returns a handle.
func Open(root string) (*Ledger, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Ledger{root: root}, nil
}

func (l *Ledger) path(kind string) string { return filepath.Join(l.root, kind+".jsonl") }

// Append writes one object as a JSON line. Append-only: never rewrites history.
func (l *Ledger) Append(kind string, obj any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path(kind), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Each streams every record line of a kind (caller unmarshals). Missing file = no records.
func (l *Ledger) Each(kind string, fn func([]byte) error) error {
	f, err := os.Open(l.path(kind))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20) // tolerate large records
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		if err := fn(sc.Bytes()); err != nil {
			return err
		}
	}
	return sc.Err()
}

// Count returns the number of records in a kind's ledger.
func (l *Ledger) Count(kind string) (int, error) {
	n := 0
	err := l.Each(kind, func([]byte) error { n++; return nil })
	return n, err
}

// Root returns the ledger directory.
func (l *Ledger) Root() string { return l.root }
