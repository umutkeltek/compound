package reviewartifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/home"
)

const QueueSchemaVersion = "compound.review_queue.v0.1"

// QueueOptions narrows the read-only review queue.
type QueueOptions struct {
	Area            string
	IncludeArchived bool
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
	ReadyToApply   int            `json:"ready_to_apply"`
	OK             int            `json:"ok"`
	ByArea         map[string]int `json:"by_area"`
	ByStatus       map[string]int `json:"by_status"`
}

// QueueItem describes one review artifact JSON payload and nearby review files.
type QueueItem struct {
	Area                    string   `json:"area"`
	ID                      string   `json:"id"`
	Status                  string   `json:"status"`
	CreatedAt               string   `json:"created_at,omitempty"`
	JSONPath                string   `json:"json_path"`
	MarkdownPath            string   `json:"markdown_path,omitempty"`
	ExtraPaths              []string `json:"extra_paths,omitempty"`
	SourceKind              string   `json:"source_kind,omitempty"`
	SourceRoot              string   `json:"source_root,omitempty"`
	SourcePath              string   `json:"source_path,omitempty"`
	TargetPath              string   `json:"target_path,omitempty"`
	TargetHash              string   `json:"target_hash,omitempty"`
	CandidateID             string   `json:"candidate_id,omitempty"`
	RepairCount             int      `json:"repair_count,omitempty"`
	EvidenceRefs            []string `json:"evidence_refs,omitempty"`
	GraphDecisionHint       string   `json:"graph_decision_hint,omitempty"`
	GraphDecisionConfidence float64  `json:"graph_decision_confidence,omitempty"`
	GraphDuplicateRisk      float64  `json:"graph_duplicate_risk,omitempty"`
	GraphEvidenceStrength   float64  `json:"graph_evidence_strength,omitempty"`
	GraphAuditRisk          string   `json:"graph_audit_risk,omitempty"`
	GraphTopOverlapScore    int      `json:"graph_top_overlap_score,omitempty"`
	Readiness               string   `json:"readiness,omitempty"`
	BlockedReason           string   `json:"blocked_reason,omitempty"`
	NextAction              string   `json:"next_action,omitempty"`
	Actions                 []Action `json:"actions,omitempty"`
}

// Action is a non-mutating operator hint attached by the domain owner that
// knows how a review artifact is safely consumed.
type Action struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	Command        string   `json:"command"`
	Argv           []string `json:"argv,omitempty"`
	RequiresReview bool     `json:"requires_review,omitempty"`
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
		if !coreReviewJSON(path) {
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
	if opt.IncludeArchived {
		archived, err := archivedQueueItems(homePath, opt.Area)
		if err != nil {
			return QueueReport{}, err
		}
		report.Items = append(report.Items, archived...)
	}
	sort.Slice(report.Items, func(i, j int) bool {
		return queueItemLess(report.Items[i], report.Items[j])
	})
	Recount(&report)
	return report, nil
}

// Recount refreshes queue counters after domain annotators adjust item status or
// readiness. The queue is read-only; this mutates only the projection.
func Recount(report *QueueReport) {
	if report == nil {
		return
	}
	report.Counts = QueueCounts{
		ByArea:   map[string]int{},
		ByStatus: map[string]int{},
	}
	for _, item := range report.Items {
		report.Counts.Total++
		report.Counts.ByArea[item.Area]++
		report.Counts.ByStatus[item.Status]++
		if item.Readiness == "ready_to_apply" {
			report.Counts.ReadyToApply++
		}
		switch item.Status {
		case "ok", "applied", "resolved", "accepted", "recorded", "candidate_recorded":
			report.Counts.OK++
		default:
			report.Counts.ReviewRequired++
		}
	}
}

func queueItem(root, path string) (QueueItem, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return QueueItem{}, err
	}
	return queueItemForRel(path, filepath.ToSlash(rel))
}

func queueItemForRel(path, rel string) (QueueItem, error) {
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
		Area:                    area,
		ID:                      firstString(payload, "id", "plan_id", "review_id", "repair_plan_id"),
		Status:                  firstString(payload, "status", "verdict", "result"),
		CreatedAt:               firstString(payload, "created_at", "generated_at"),
		JSONPath:                path,
		SourceKind:              firstString(payload, "source_kind"),
		SourceRoot:              firstString(payload, "source_root"),
		SourcePath:              firstString(payload, "source_path"),
		TargetPath:              firstString(payload, "target_path"),
		TargetHash:              firstString(payload, "target_hash"),
		CandidateID:             firstString(payload, "candidate_id", "candidate_ref"),
		RepairCount:             intField(payload, "repair_count"),
		EvidenceRefs:            stringSlice(payload["evidence_refs"]),
		GraphDecisionHint:       firstString(payload, "graph_decision_hint"),
		GraphDecisionConfidence: floatField(payload, "graph_decision_confidence"),
		GraphDuplicateRisk:      floatField(payload, "graph_duplicate_risk"),
		GraphEvidenceStrength:   floatField(payload, "graph_evidence_strength"),
		GraphAuditRisk:          firstString(payload, "graph_audit_risk"),
		GraphTopOverlapScore:    intField(payload, "graph_top_overlap_score"),
		Readiness:               firstString(payload, "readiness"),
		BlockedReason:           firstString(payload, "blocked_reason"),
		NextAction:              firstString(payload, "next_action"),
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

func archivedQueueItems(homePath, area string) ([]QueueItem, error) {
	entries, err := home.LoadOperationalArchiveIndex(homePath)
	if err != nil {
		return nil, err
	}
	byOriginal := map[string]home.OperationalArchiveEntry{}
	for _, entry := range entries {
		byOriginal[entry.OriginalPath] = entry
	}
	var out []QueueItem
	for _, entry := range entries {
		if entry.Kind != "review" || filepath.Ext(entry.OriginalPath) != ".json" || !coreReviewJSON(entry.OriginalPath) {
			continue
		}
		rel := strings.TrimPrefix(filepath.ToSlash(entry.OriginalPath), "reviews/")
		if area != "" && !strings.HasPrefix(rel, filepath.ToSlash(area)+"/") && rel != filepath.ToSlash(area) {
			continue
		}
		item, err := queueItemForRel(filepath.Join(homePath, filepath.FromSlash(entry.ArchivePath)), rel)
		if err != nil {
			return nil, err
		}
		item.Status = FirstNonEmpty(item.Status, entry.Status, "archived")
		item.ExtraPaths = archivedReviewExtraPaths(entries, byOriginal, homePath, entry.OriginalPath)
		out = append(out, item)
	}
	return out, nil
}

func archivedReviewExtraPaths(entries []home.OperationalArchiveEntry, byOriginal map[string]home.OperationalArchiveEntry, homePath, originalPath string) []string {
	stem := strings.TrimSuffix(filepath.Base(originalPath), ".json")
	dir := filepath.ToSlash(filepath.Dir(originalPath))
	var out []string
	for _, entry := range entries {
		if entry.Kind != "review" || entry.OriginalPath == originalPath {
			continue
		}
		if filepath.ToSlash(filepath.Dir(entry.OriginalPath)) != dir {
			continue
		}
		if !strings.HasPrefix(filepath.Base(entry.OriginalPath), stem+".") {
			continue
		}
		if _, ok := byOriginal[entry.OriginalPath]; !ok {
			continue
		}
		out = append(out, filepath.Join(homePath, filepath.FromSlash(entry.ArchivePath)))
	}
	sort.Strings(out)
	return out
}

func coreReviewJSON(path string) bool {
	name := filepath.Base(path)
	for _, suffix := range []string{".apply.json", ".decision.json", ".reviewed.json"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	return true
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
	case "ok", "applied", "resolved", "accepted", "recorded", "candidate_recorded":
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

func floatField(payload map[string]any, key string) float64 {
	switch value := payload[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
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
