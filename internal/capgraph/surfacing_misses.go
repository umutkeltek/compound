package capgraph

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

type SkillSurfacingMisses struct {
	ID         string
	SourcePath string
	Revision   string
	Generated  string
	Scanned    int
	Skipped    int
	LaneFails  int
	Remaining  int
	Proposals  []SkillSurfacingMissSummary `json:"proposals"`
	AllMisses  []SkillSurfacingMissSummary `json:"all_misses"`
	PerReceipt []SkillSurfacingMissReceipt `json:"perReceipt"`
}

type SkillSurfacingMissSummary struct {
	Skill    string   `json:"skill"`
	Count    int      `json:"count"`
	Receipts []string `json:"receipts"`
	Reasons  []string `json:"reasons"`
	MaxConf  float64  `json:"maxConf"`
}

type SkillSurfacingMissReceipt struct {
	Dir   string                    `json:"dir"`
	Kind  string                    `json:"kind"`
	Lanes string                    `json:"lanes"`
	Real  []SkillSurfacingMissEntry `json:"real"`
}

type SkillSurfacingMissEntry struct {
	Skill      string  `json:"skill"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

type SkillSurfacingMissesReport struct {
	Status                string   `json:"status"`
	ReportID              string   `json:"report_id"`
	MissesRead            int      `json:"misses_read"`
	AppliedMisses         int      `json:"applied_misses"`
	AppliedEdges          int      `json:"applied_edges"`
	SkippedUnknown        []string `json:"skipped_unknown,omitempty"`
	SkippedMissingContext []string `json:"skipped_missing_context,omitempty"`
	EdgeIDs               []string `json:"edge_ids"`
}

func LoadSkillSurfacingMisses(path string) (SkillSurfacingMisses, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillSurfacingMisses{}, fmt.Errorf("read skill surfacing misses %s: %w", path, err)
	}
	var report SkillSurfacingMisses
	if err := json.Unmarshal(data, &report); err != nil {
		return SkillSurfacingMisses{}, fmt.Errorf("parse skill surfacing misses %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	report.SourcePath = path
	report.Revision = "sha256:" + hex.EncodeToString(sum[:])
	report.ID = "skill_surfacing_misses:" + hex.EncodeToString(sum[:])[:12]
	if err := report.Validate(); err != nil {
		return SkillSurfacingMisses{}, fmt.Errorf("validate skill surfacing misses %s: %w", path, err)
	}
	return report, nil
}

func (m SkillSurfacingMisses) Validate() error {
	if strings.TrimSpace(m.Generated) == "" {
		return fmt.Errorf("generated required")
	}
	if len(m.AllMisses) == 0 && len(m.PerReceipt) == 0 {
		return fmt.Errorf("all_misses or perReceipt required")
	}
	for _, receipt := range m.PerReceipt {
		if strings.TrimSpace(receipt.Dir) == "" && len(receipt.Real) > 0 {
			return fmt.Errorf("receipt dir required for real misses")
		}
		for _, miss := range receipt.Real {
			if strings.TrimSpace(miss.Skill) == "" {
				return fmt.Errorf("miss skill required")
			}
			if miss.Confidence <= 0 || miss.Confidence > 1 {
				return fmt.Errorf("miss confidence must be > 0 and <= 1")
			}
		}
	}
	return nil
}

func ApplySkillSurfacingMisses(homePath string, misses SkillSurfacingMisses, now time.Time) (SkillSurfacingMissesReport, error) {
	report := SkillSurfacingMissesReport{
		Status:   "ok",
		ReportID: misses.ID,
	}
	store, err := Open(homePath)
	if err != nil {
		report.Status = "fail"
		return report, err
	}
	nodes, err := store.loadNodes()
	if err != nil {
		report.Status = "fail"
		return report, err
	}
	edges, err := store.loadEdges()
	if err != nil {
		report.Status = "fail"
		return report, err
	}
	existingEdges := map[string]bool{}
	for _, edge := range edges {
		existingEdges[edgeKey(edge)] = true
	}
	unknown := map[string]bool{}
	missingContext := map[string]bool{}
	for _, receipt := range misses.PerReceipt {
		for _, miss := range receipt.Real {
			report.MissesRead++
			nodeID := resolveCapabilityNode(nodes, miss.Skill)
			if nodeID == "" {
				nodeID = capabilityNodeID(miss.Skill)
			}
			if nodeID == "" || !capabilityNodeKnown(nodes, nodeID) {
				if !unknown[miss.Skill] {
					report.SkippedUnknown = append(report.SkippedUnknown, miss.Skill)
					unknown[miss.Skill] = true
				}
				continue
			}
			context := strings.TrimSpace(miss.Reason)
			if context == "" {
				context = strings.TrimSpace(receipt.Dir)
			}
			tokens := skillSurfacingContextTokens(context)
			if len(tokens) == 0 {
				key := firstEvalNonEmpty(receipt.Dir, miss.Skill)
				if !missingContext[key] {
					report.SkippedMissingContext = append(report.SkippedMissingContext, key)
					missingContext[key] = true
				}
				continue
			}
			appliedForMiss := false
			for _, tok := range tokens {
				edge := Edge{
					Kind:       EdMissedWhen,
					From:       "context:" + tok,
					To:         nodeID,
					Confidence: miss.Confidence,
					CreatedAt:  now.UTC().Format(time.RFC3339),
				}
				added, err := store.addEdgeOnce(edge, existingEdges, now)
				if err != nil {
					report.Status = "fail"
					return report, err
				}
				if added {
					report.AppliedEdges++
					report.EdgeIDs = append(report.EdgeIDs, edgeID(edge))
					appliedForMiss = true
				}
			}
			if appliedForMiss {
				report.AppliedMisses++
			}
		}
	}
	sort.Strings(report.SkippedUnknown)
	sort.Strings(report.SkippedMissingContext)
	sort.Strings(report.EdgeIDs)
	if len(report.SkippedUnknown) > 0 {
		report.Status = "review"
	}
	return report, nil
}

func skillSurfacingEvidenceRefs(misses SkillSurfacingMisses) []string {
	refs := []string{"skill_surfacing_misses:" + strings.TrimPrefix(misses.Revision, "sha256:")}
	seen := map[string]bool{refs[0]: true}
	for _, receipt := range misses.PerReceipt {
		if strings.TrimSpace(receipt.Dir) == "" {
			continue
		}
		ref := "receipt:" + strings.TrimSpace(receipt.Dir)
		if !seen[ref] {
			refs = append(refs, ref)
			seen[ref] = true
		}
	}
	return refs
}

func capabilityNodeKnown(nodes []Node, nodeID string) bool {
	for _, node := range nodes {
		if node.ID == nodeID {
			return true
		}
	}
	return false
}

func skillSurfacingContextTokens(context string) []string {
	var out []string
	for _, tok := range tokenList(context) {
		if len(tok) < 3 || skillSurfacingStopWords[tok] {
			continue
		}
		out = append(out, tok)
	}
	return out
}

var skillSurfacingStopWords = map[string]bool{
	"and":  true,
	"are":  true,
	"but":  true,
	"for":  true,
	"the":  true,
	"this": true,
	"that": true,
	"with": true,
}

func SkillSurfacingMissesEvidenceRefs(misses SkillSurfacingMisses) []string {
	return skillSurfacingEvidenceRefs(misses)
}

func SkillSurfacingMissesSourceName(path string) string {
	if strings.TrimSpace(path) == "" {
		return "skill-surfacing-proposals.json"
	}
	return filepath.Base(path)
}
