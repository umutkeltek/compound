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
)

var reReviewTrigger = regexp.MustCompile(`(?i)review[ \-]?trigger[:*\s]+\s*([^\n]+)`)

func promotionBasisForStatus(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "supersed"), strings.Contains(s, "deprecat"), strings.Contains(s, "retired"):
		return ""
	case strings.Contains(s, "propos"), strings.Contains(s, "open"):
		return ""
	default:
		return candidates.BasisDecisionRecord
	}
}

func legacyStatus(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "supersed"):
		return "superseded"
	case strings.Contains(s, "propos"), strings.Contains(s, "open"):
		return "candidate"
	case strings.Contains(s, "deprecat"), strings.Contains(s, "retired"):
		return "retired"
	default:
		return "accepted"
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
		q := map[string]string{"kind": KindPolicy, "source": "decisions-repo", "legacy_status": legacyStatus(f.Status)}
		if len(f.Domains) > 0 {
			q["domain"] = strings.Join(f.Domains, ",")
		}
		if len(f.Supersedes) > 0 {
			q["supersedes"] = strings.Join(f.Supersedes, ",")
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
			status = "candidate"
		}
		if regexp.MustCompile(`(?i)superseded`).MatchString(block) {
			status = "superseded"
		}
		q := map[string]string{"kind": KindDoctrine, "scope": "personal", "source": "operator-ledger", "legacy_status": status}
		if mm := reReviewTrigger.FindStringSubmatch(block); mm != nil {
			q["review_trigger"] = strings.TrimSpace(mm[1])
		}
		basis := candidates.BasisDecisionRecord
		if status != "accepted" {
			basis = ""
		}
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
