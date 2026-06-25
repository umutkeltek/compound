// Package decisions is Layer 3: ONE decisions store with clean separation by kind
// — unifying what the domain-specific decision sources feed. A decision is a Claim (claim_type=decision) with a `kind`:
//
//	policy      — reusable cross-agent rule        (was decisions/ DEC-####)
//	doctrine    — personal working decision        from an operator ledger
//	operational — control/routing decision         from a domain adapter
//
// Lifecycle is a STATUS (open→decided→superseded), and promotion (doctrine→policy)
// is a status/kind transition in one store, not a cross-repo copy.
package decisions

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/umutkeltek/compound/internal/memory"
)

const (
	KindPolicy      = "policy"
	KindDoctrine    = "doctrine"
	KindOperational = "operational"
)

var reReviewTrigger = regexp.MustCompile(`(?i)review[ \-]?trigger[:*\s]+\s*([^\n]+)`)

func phaseFromStatus(s string) memory.ClaimStatus {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "supersed"):
		return memory.StatusSuperseded
	case strings.Contains(s, "propos"), strings.Contains(s, "open"):
		return memory.StatusCandidate
	case strings.Contains(s, "deprecat"), strings.Contains(s, "retired"):
		return memory.StatusRetired
	default:
		return memory.StatusPromoted // accepted / decided
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
	ID         string   `yaml:"id"`
	Title      string   `yaml:"title"`
	Status     string   `yaml:"status"`
	Domains    []string `yaml:"domains"`
	Supersedes []string `yaml:"supersedes"`
}

// ImportDEC parses the decisions/ project (DEC-####.md) into POLICY decisions.
func ImportDEC(root string) ([]memory.Claim, error) {
	var out []memory.Claim
	base := filepath.Join(root, "decisions")
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasPrefix(d.Name(), "DEC-") || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		fmText, body := splitFrontmatter(string(data))
		var f decFrontmatter
		if yaml.Unmarshal([]byte(fmText), &f) != nil || f.ID == "" {
			return nil
		}
		q := map[string]any{"kind": KindPolicy, "scope": "cross-agent", "source": "decisions/" + d.Name()}
		if len(f.Domains) > 0 {
			q["domains"] = strings.Join(f.Domains, ",")
		}
		if len(f.Supersedes) > 0 {
			q["supersedes"] = strings.Join(f.Supersedes, "; ")
		}
		if m := reReviewTrigger.FindStringSubmatch(body); m != nil {
			q["review_trigger"] = strings.TrimSpace(m[1])
		}
		out = append(out, memory.Claim{
			ID: f.ID, SubjectEntity: "decisions", Predicate: "decided", Object: f.Title,
			ClaimType: memory.ClaimDecision, Status: phaseFromStatus(f.Status),
			Confidence: 1.0, Visibility: memory.VisWorld, CreatedBy: "decision-import",
			Qualifiers: q,
		})
		return nil
	})
	return out, nil
}

var reDecHeader = regexp.MustCompile(`(?m)^###\s+([DO]\d+)\b[^\n]*?—\s*(.+)$`)

// ImportOperatorLedger parses the operator ledger (### D<n>/O<n>) into DOCTRINE
// decisions. O<n> = open (candidate), D<n> = decided, SUPERSEDED markers honored.
func ImportOperatorLedger(path string) ([]memory.Claim, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(data)
	idx := reDecHeader.FindAllStringSubmatchIndex(text, -1)
	var out []memory.Claim
	for i, m := range idx {
		id := text[m[2]:m[3]]
		title := strings.Trim(strings.TrimSpace(text[m[4]:m[5]]), "~ ")
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		block := text[m[1]:end]
		status := memory.StatusPromoted
		if strings.HasPrefix(id, "O") {
			status = memory.StatusCandidate
		}
		if regexp.MustCompile(`(?i)superseded`).MatchString(block) {
			status = memory.StatusSuperseded
		}
		q := map[string]any{"kind": KindDoctrine, "scope": "personal", "source": "operator-ledger"}
		if mm := reReviewTrigger.FindStringSubmatch(block); mm != nil {
			q["review_trigger"] = strings.TrimSpace(mm[1])
		}
		out = append(out, memory.Claim{
			ID: "operator-" + id, SubjectEntity: "decisions", Predicate: "decided", Object: title,
			ClaimType: memory.ClaimDecision, Status: status, Confidence: 0.9,
			Visibility: memory.VisWorld, CreatedBy: "decision-import", Qualifiers: q,
		})
	}
	return out, nil
}

// Kind returns a decision claim's kind (policy/doctrine/operational), or "".
func Kind(c memory.Claim) string {
	if c.Qualifiers != nil {
		if k, ok := c.Qualifiers["kind"].(string); ok {
			return k
		}
	}
	return ""
}
