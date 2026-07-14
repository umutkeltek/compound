// Package finding is the direct-write home for an agent-authored operational finding — a durable,
// immediately-recallable note (a runbook fact, an operational observation) that is NOT a promotable
// truth claim and does NOT need adjudication latency. It is the POSITIVE half of the placement
// grammar: the governed place an agent writes a finding instead of inventing a path under
// COMPOUND_HOME (the itch the closed-world guard denies).
//
// Findings live under COMPOUND_HOME/observed/findings — a rebuildable, provenance-tagged,
// content-addressed directory OUTSIDE COMPOUND_HOME/ledger, mirroring observed/tool-output. Recording
// a finding therefore never adds a ledger byte (LedgerDigest stays byte-identical by construction),
// and a finding is source_class=evidence: recallable, but it can never enter ambient recall as a
// promoted Claim. A finding that later proves durable is promoted through the SEPARATE governed
// memory door — two doors, by design (capture is cheap; promotion is adjudicated).
package finding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Subdir is the direct-write, content-addressed home for operational findings, relative to
// COMPOUND_HOME. Exported so the placement resolver can compose it as the operational-finding target
// without re-authoring the path (a read facade over the real owner).
const Subdir = "observed/findings"

// Finding is one agent-authored operational note. It is append-only with supersession (a revised
// finding carries Supersedes = the prior id), mirroring the ledger's discipline without being in
// the ledger.
type Finding struct {
	ID           string   `json:"id"`                    // "find_" + digest12(topic|body) — idempotent on content
	CID          string   `json:"cid"`                   // "sha256:"+hex(body) — content address of the body
	Topic        string   `json:"topic"`                 // recall key (subject / entity)
	Body         string   `json:"body"`                  // the note (markdown)
	ProducedBy   string   `json:"produced_by,omitempty"` // harness / agent that authored it
	WorkID       string   `json:"work_id,omitempty"`     // provenance: the work_id it was learned under
	SourceClass  string   `json:"source_class"`          // always "evidence" — never ambient truth
	Supersedes   string   `json:"supersedes,omitempty"`  // prior finding id this revises
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	CreatedAt    string   `json:"created_at"`
}

// CID content-addresses a finding body: "sha256:"+hex(sha256(body)).
func CID(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digest12(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

// Record writes a finding to observed/findings. It is idempotent on (topic, body): the same content
// yields the same id and rewrites the identical record (never resetting anything). It writes ONLY
// under observed/findings — NEVER a ledger byte.
func Record(homePath string, f Finding, now time.Time) (Finding, error) {
	if strings.TrimSpace(homePath) == "" {
		return Finding{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(f.Topic) == "" {
		return Finding{}, fmt.Errorf("finding topic required")
	}
	if strings.TrimSpace(f.Body) == "" {
		return Finding{}, fmt.Errorf("finding body required")
	}
	f.ID = "find_" + digest12(f.Topic+"|"+f.Body)
	f.CID = CID(f.Body)
	f.SourceClass = "evidence" // findings are evidence, never promoted truth
	if strings.TrimSpace(f.CreatedAt) == "" {
		f.CreatedAt = now.UTC().Format(time.RFC3339)
	}
	f.EvidenceRefs = dedupe(f.EvidenceRefs)
	dir := filepath.Join(homePath, Subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Finding{}, err
	}
	data, err := json.MarshalIndent(&f, "", "  ")
	if err != nil {
		return Finding{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, f.ID+".json"), append(data, '\n'), 0o644); err != nil {
		return Finding{}, err
	}
	return f, nil
}

// Show returns a finding by id. A missing id is an explicit error (never a silent zero value).
func Show(homePath, id string) (Finding, error) {
	if err := validID(id); err != nil {
		return Finding{}, err
	}
	b, err := os.ReadFile(filepath.Join(homePath, Subdir, id+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return Finding{}, fmt.Errorf("finding %s not found", id)
		}
		return Finding{}, err
	}
	var f Finding
	if err := json.Unmarshal(b, &f); err != nil {
		return Finding{}, err
	}
	return f, nil
}

// List returns findings, optionally filtered by a topic/body substring (case-insensitive), newest
// first. A missing findings dir is the empty no-op, not an error.
func List(homePath, filter string) ([]Finding, error) {
	dir := filepath.Join(homePath, Subdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(filter))
	var out []Finding
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var f Finding
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, err
		}
		if needle != "" && !strings.Contains(strings.ToLower(f.Topic), needle) && !strings.Contains(strings.ToLower(f.Body), needle) {
			continue
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt // newest first
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// FindingDigest is one finding in a lifecycle report — enough to identify and act on it without the
// full body.
type FindingDigest struct {
	ID           string `json:"id"`
	Topic        string `json:"topic"`
	CreatedAt    string `json:"created_at"`
	SupersededBy string `json:"superseded_by,omitempty"`
	AgeDays      int    `json:"age_days,omitempty"`
}

// FindingReport is Doctor's read-only lifecycle view: how many findings exist, how many are live,
// which are superseded (their id is named by a later finding's Supersedes), and which live findings
// are stale (older than the age threshold). It NEVER promotes, archives, or deletes — visibility only,
// so the direct-write home stays accountable without a second authority.
type FindingReport struct {
	Total      int             `json:"total"`
	Live       int             `json:"live"`
	StaleDays  int             `json:"stale_days"`
	Superseded []FindingDigest `json:"superseded"`
	Stale      []FindingDigest `json:"stale"`
}

// Doctor reports finding-home lifecycle hygiene: superseded records (an id named by a later finding's
// Supersedes) and stale LIVE records (CreatedAt older than staleDays; staleDays<=0 disables the age
// axis). It reads only — it never promotes, archives, or deletes. It deliberately does NOT report
// "unreferenced": findings are referenced BY nothing inward by design (their EvidenceRefs point
// outward), so an unreferenced axis would be fabricated noise rather than a real signal.
func Doctor(homePath string, now time.Time, staleDays int) (FindingReport, error) {
	all, err := List(homePath, "")
	if err != nil {
		return FindingReport{}, err
	}
	supersededBy := map[string]string{} // superseded id -> the finding id that revises it
	for _, f := range all {
		if s := strings.TrimSpace(f.Supersedes); s != "" {
			supersededBy[s] = f.ID
		}
	}
	rep := FindingReport{Total: len(all), StaleDays: staleDays}
	for _, f := range all {
		if by, ok := supersededBy[f.ID]; ok {
			rep.Superseded = append(rep.Superseded, FindingDigest{ID: f.ID, Topic: f.Topic, CreatedAt: f.CreatedAt, SupersededBy: by})
			continue
		}
		rep.Live++
		if staleDays > 0 {
			if age, ok := ageDays(f.CreatedAt, now); ok && age >= staleDays {
				rep.Stale = append(rep.Stale, FindingDigest{ID: f.ID, Topic: f.Topic, CreatedAt: f.CreatedAt, AgeDays: age})
			}
		}
	}
	return rep, nil
}

func ageDays(createdAt string, now time.Time) (int, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(createdAt))
	if err != nil {
		return 0, false
	}
	d := now.Sub(t)
	if d < 0 {
		return 0, true
	}
	return int(d.Hours() / 24), true
}

// validID keeps a crafted id from escaping the findings dir (the id keys a filename).
func validID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("finding id required")
	}
	if id == "." || id == ".." || id != filepath.Base(id) || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("finding id must be a bare identifier, got %q", id)
	}
	return nil
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
