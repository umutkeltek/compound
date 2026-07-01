package reviewartifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const QueueSchemaVersion = "compound.review_queue.v0.1"

// QueueOptions narrows the read-only review queue.
type QueueOptions struct {
	Area string
}

// QueueReport is a read-only projection over COMPOUND_HOME/reviews.
type QueueReport struct {
	SchemaVersion string      `json:"schema_version"`
	GeneratedAt   string      `json:"generated_at"`
	ReviewRoot    string      `json:"review_root"`
	Area          string      `json:"area,omitempty"`
	Counts        QueueCounts `json:"counts"`
	Items         []QueueItem `json:"items"`
}

// QueueCounts summarizes review artifact status without interpreting domain payloads.
type QueueCounts struct {
	Total          int            `json:"total"`
	ReviewRequired int            `json:"review_required"`
	OK             int            `json:"ok"`
	ByArea         map[string]int `json:"by_area"`
	ByStatus       map[string]int `json:"by_status"`
}

// QueueItem describes one review artifact JSON payload and nearby review files.
type QueueItem struct {
	Area         string   `json:"area"`
	ID           string   `json:"id"`
	Status       string   `json:"status"`
	CreatedAt    string   `json:"created_at,omitempty"`
	JSONPath     string   `json:"json_path"`
	MarkdownPath string   `json:"markdown_path,omitempty"`
	ExtraPaths   []string `json:"extra_paths,omitempty"`
	SourceKind   string   `json:"source_kind,omitempty"`
	SourceRoot   string   `json:"source_root,omitempty"`
	SourcePath   string   `json:"source_path,omitempty"`
	TargetPath   string   `json:"target_path,omitempty"`
	TargetHash   string   `json:"target_hash,omitempty"`
	CandidateID  string   `json:"candidate_id,omitempty"`
	RepairCount  int      `json:"repair_count,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

// Queue scans COMPOUND_HOME/reviews for review artifacts. It never mutates
// artifacts, owner files, ledgers, or promotion state.
func Queue(homePath string, opt QueueOptions, now time.Time) (QueueReport, error) {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return QueueReport{}, fmt.Errorf("COMPOUND_HOME required")
	}
	opt.Area = strings.TrimSpace(opt.Area)
	if opt.Area != "" {
		if err := validArea(opt.Area); err != nil {
			return QueueReport{}, err
		}
	}
	root := filepath.Join(homePath, "reviews")
	report := QueueReport{
		SchemaVersion: QueueSchemaVersion,
		GeneratedAt:   Timestamp(now),
		ReviewRoot:    root,
		Area:          opt.Area,
		Counts: QueueCounts{
			ByArea:   map[string]int{},
			ByStatus: map[string]int{},
		},
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		item, err := queueItem(root, path)
		if err != nil {
			return err
		}
		if opt.Area != "" && item.Area != opt.Area {
			return nil
		}
		report.Items = append(report.Items, item)
		return nil
	})
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return QueueReport{}, err
	}
	sort.Slice(report.Items, func(i, j int) bool {
		return queueItemLess(report.Items[i], report.Items[j])
	})
	for _, item := range report.Items {
		report.Counts.Total++
		report.Counts.ByArea[item.Area]++
		report.Counts.ByStatus[item.Status]++
		switch item.Status {
		case "ok", "applied", "resolved", "accepted":
			report.Counts.OK++
		default:
			report.Counts.ReviewRequired++
		}
	}
	return report, nil
}

func queueItem(root, path string) (QueueItem, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return QueueItem{}, err
	}
	area := filepath.ToSlash(filepath.Dir(rel))
	if area == "." {
		area = ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return QueueItem{}, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return QueueItem{}, fmt.Errorf("parse review artifact %s: %w", path, err)
	}
	stem := strings.TrimSuffix(filepath.Base(path), ".json")
	item := QueueItem{
		Area:         area,
		ID:           firstString(payload, "id", "plan_id", "review_id", "repair_plan_id"),
		Status:       firstString(payload, "status", "verdict", "result"),
		CreatedAt:    firstString(payload, "created_at", "generated_at"),
		JSONPath:     path,
		SourceKind:   firstString(payload, "source_kind"),
		SourceRoot:   firstString(payload, "source_root"),
		SourcePath:   firstString(payload, "source_path"),
		TargetPath:   firstString(payload, "target_path"),
		TargetHash:   firstString(payload, "target_hash"),
		CandidateID:  firstString(payload, "candidate_id", "candidate_ref"),
		RepairCount:  intField(payload, "repair_count"),
		EvidenceRefs: stringSlice(payload["evidence_refs"]),
	}
	if item.ID == "" {
		item.ID = stem
	}
	if item.Status == "" {
		item.Status = "unknown"
	}
	if item.RepairCount == 0 {
		if repairs, ok := payload["repairs"].([]any); ok {
			item.RepairCount = len(repairs)
		}
	}
	mdPath := filepath.Join(filepath.Dir(path), stem+".md")
	if _, err := os.Stat(mdPath); err == nil {
		item.MarkdownPath = mdPath
	} else if err != nil && !os.IsNotExist(err) {
		return QueueItem{}, err
	}
	extras, err := queueExtraPaths(filepath.Dir(path), stem, path, item.MarkdownPath)
	if err != nil {
		return QueueItem{}, err
	}
	item.ExtraPaths = extras
	return item, nil
}

func queueExtraPaths(dir, stem, jsonPath, mdPath string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, stem+".") {
			continue
		}
		path := filepath.Join(dir, name)
		if path == jsonPath || path == mdPath {
			continue
		}
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}

func queueItemLess(a, b QueueItem) bool {
	ap, bp := statusPriority(a.Status), statusPriority(b.Status)
	if ap != bp {
		return ap < bp
	}
	if a.Area != b.Area {
		return a.Area < b.Area
	}
	return a.ID < b.ID
}

func statusPriority(status string) int {
	switch status {
	case "review_required", "unknown":
		return 0
	case "held", "blocked":
		return 1
	case "ok", "applied", "resolved", "accepted":
		return 3
	default:
		return 2
	}
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func intField(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

func stringSlice(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, value := range values {
		s, ok := value.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return UniqueSorted(out)
}
