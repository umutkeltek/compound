// Package decisions imports decision sources into the spine memory family. A decision
// source becomes a mem candidate; promotion remains a separate adjudication step.
package decisions

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"compound/internal/candidates"
	"compound/internal/mem"
)

const (
	KindPolicy      = "policy"
	KindDoctrine    = "doctrine"
	KindOperational = "operational"

	StatusProposed   = "proposed"
	StatusAccepted   = "accepted"
	StatusSuperseded = "superseded"
	StatusDeprecated = "deprecated"
	StatusRejected   = "rejected"
)

var (
	reReviewTrigger = regexp.MustCompile(`(?i)review[ \-]?trigger[:*\s]+\s*([^\n]+)`)
	reFalsifier     = regexp.MustCompile(`(?i)falsifier[:*\s]+\s*([^\n]+)`)
)

// Lifecycle is the DecisionLifecycle contract projected onto spine memory.
// The claim remains the durable ledger object; these fields make the decision
// lifecycle typed and auditable instead of a loose prose qualifier.
type Lifecycle struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Choice        string   `json:"choice"`
	Status        string   `json:"status"`
	Scope         string   `json:"scope,omitempty"`
	Owner         string   `json:"owner,omitempty"`
	Falsifier     string   `json:"falsifier,omitempty"`
	ReviewTrigger string   `json:"review_trigger,omitempty"`
	Supersedes    []string `json:"supersedes,omitempty"`
	ReplacedBy    string   `json:"replaced_by,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
}

func promotionBasisForStatus(s string) string {
	switch CanonicalStatus(s) {
	case StatusAccepted:
		return candidates.BasisDecisionRecord
	default:
		return ""
	}
}

func CanonicalStatus(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "supersed"):
		return StatusSuperseded
	case strings.Contains(s, "propos"), strings.Contains(s, "open"), strings.Contains(s, "draft"):
		return StatusProposed
	case strings.Contains(s, "deprecat"), strings.Contains(s, "retired"):
		return StatusDeprecated
	case strings.Contains(s, "reject"):
		return StatusRejected
	default:
		return StatusAccepted
	}
}

func lifecycleQualifiers(kind, source, id, status string) map[string]string {
	return map[string]string{
		"kind":             kind,
		"source":           source,
		"decision_id":      id,
		"lifecycle_status": CanonicalStatus(status),
	}
}

func splitFrontmatter(s string) (fm, body string) {
	if !strings.HasPrefix(s, "---") {
		return "", s
	}
	rest := s[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", s
	}
	return rest[:end], rest[end+4:]
}

type decFrontmatter struct {
	ID            string   `yaml:"id"`
	Title         string   `yaml:"title"`
	Status        string   `yaml:"status"`
	Domains       []string `yaml:"domains"`
	Supersedes    []string `yaml:"supersedes"`
	Falsifier     string   `yaml:"falsifier"`
	ReviewTrigger string   `yaml:"review_trigger"`
	Owner         string   `yaml:"owner"`
	Scope         string   `yaml:"scope"`
}

// ImportDEC parses decisions/DEC markdown files into policy decision candidates.
func ImportDEC(root string) ([]mem.Candidate, error) {
	base := filepath.Join(root, "decisions")
	var out []mem.Candidate
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fm, body := splitFrontmatter(string(b))
		var f decFrontmatter
		_ = yaml.Unmarshal([]byte(fm), &f)
		if f.ID == "" {
			f.ID = strings.TrimSuffix(filepath.Base(path), ".md")
		}
		if f.Title == "" {
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(line, "#") {
					f.Title = strings.Trim(strings.TrimLeft(line, "#"), " ")
					break
				}
			}
		}
		q := lifecycleQualifiers(KindPolicy, "decisions-repo", f.ID, f.Status)
		if len(f.Domains) > 0 {
			q["domain"] = strings.Join(f.Domains, ",")
		}
		if len(f.Supersedes) > 0 {
			q["supersedes"] = strings.Join(f.Supersedes, ",")
		}
		if f.Falsifier != "" {
			q["falsifier"] = strings.TrimSpace(f.Falsifier)
		}
		if f.ReviewTrigger != "" {
			q["review_trigger"] = strings.TrimSpace(f.ReviewTrigger)
		}
		if f.Owner != "" {
			q["owner"] = strings.TrimSpace(f.Owner)
		}
		if f.Scope != "" {
			q["scope"] = strings.TrimSpace(f.Scope)
		}
		out = append(out, mem.Candidate{
			Entity:         "decisions",
			Predicate:      "decided",
			Object:         f.Title,
			Confidence:     1.0,
			Derivation:     "deterministic",
			ClaimType:      "decision",
			Qualifiers:     q,
			PromotionBasis: promotionBasisForStatus(f.Status),
			SourceTraceRef: path,
			SnippetHash:    f.ID,
			Visibility:     "operational",
		})
		return nil
	})
	return out, err
}

var reDecHeader = regexp.MustCompile(`(?m)^###\s+([DO]\d+)\b[^\n]*?—\s*(.+)$`)

// ImportOperatorLedger parses operator ledger headings into doctrine decision candidates.
func ImportOperatorLedger(path string) ([]mem.Candidate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	idx := reDecHeader.FindAllStringSubmatchIndex(text, -1)
	var out []mem.Candidate
	for i, m := range idx {
		id := text[m[2]:m[3]]
		title := strings.Trim(strings.TrimSpace(text[m[4]:m[5]]), "~ ")
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		block := text[m[1]:end]
		status := "accepted"
		if strings.HasPrefix(id, "O") {
			status = StatusProposed
		}
		if regexp.MustCompile(`(?i)superseded`).MatchString(block) {
			status = "superseded"
		}
		q := lifecycleQualifiers(KindDoctrine, "operator-ledger", id, status)
		q["scope"] = "personal"
		if mm := reReviewTrigger.FindStringSubmatch(block); mm != nil {
			q["review_trigger"] = strings.TrimSpace(mm[1])
		}
		if mm := reFalsifier.FindStringSubmatch(block); mm != nil {
			q["falsifier"] = strings.TrimSpace(mm[1])
		}
		basis := promotionBasisForStatus(status)
		out = append(out, mem.Candidate{
			Entity:         "decisions",
			Predicate:      "decided",
			Object:         title,
			Confidence:     0.9,
			Derivation:     "deterministic",
			ClaimType:      "decision",
			Qualifiers:     q,
			PromotionBasis: basis,
			SourceTraceRef: path,
			SnippetHash:    id,
			Visibility:     "operational",
		})
	}
	return out, nil
}

// Kind returns a decision kind from a spine claim.
func Kind(c mem.Claim) string {
	if c.Qualifiers == nil {
		return KindOperational
	}
	if k := c.Qualifiers["kind"]; k != "" {
		return k
	}
	return KindOperational
}

// LifecycleFromClaim projects the typed DecisionLifecycle contract from a
// promoted spine claim. It does not create a second source of truth.
func LifecycleFromClaim(c mem.Claim) Lifecycle {
	q := c.Qualifiers
	return Lifecycle{
		ID:            q["decision_id"],
		Kind:          kindFromQualifiers(q),
		Choice:        c.Object,
		Status:        statusFromQualifiers(q),
		Scope:         q["scope"],
		Owner:         q["owner"],
		Falsifier:     q["falsifier"],
		ReviewTrigger: q["review_trigger"],
		Supersedes:    splitCSV(q["supersedes"]),
		ReplacedBy:    q["replaced_by"],
		EvidenceRefs:  append([]string(nil), c.EvidenceRefs...),
	}
}

// LifecycleFromCandidate exposes the same lifecycle before promotion.
func LifecycleFromCandidate(c mem.Candidate) Lifecycle {
	q := c.Qualifiers
	return Lifecycle{
		ID:            q["decision_id"],
		Kind:          kindFromQualifiers(q),
		Choice:        c.Object,
		Status:        statusFromQualifiers(q),
		Scope:         q["scope"],
		Owner:         q["owner"],
		Falsifier:     q["falsifier"],
		ReviewTrigger: q["review_trigger"],
		Supersedes:    splitCSV(q["supersedes"]),
		EvidenceRefs:  []string{c.ID},
	}
}

// MissingRequired reports fields required for a durable decision artifact.
func MissingRequired(l Lifecycle) []string {
	var missing []string
	if strings.TrimSpace(l.Falsifier) == "" {
		missing = append(missing, "falsifier")
	}
	if strings.TrimSpace(l.ReviewTrigger) == "" {
		missing = append(missing, "review_trigger")
	}
	return missing
}

func kindFromQualifiers(q map[string]string) string {
	if q == nil || q["kind"] == "" {
		return KindOperational
	}
	return q["kind"]
}

func statusFromQualifiers(q map[string]string) string {
	if q == nil {
		return StatusAccepted
	}
	return CanonicalStatus(q["lifecycle_status"])
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
