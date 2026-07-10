package decisions

// Record is the decide->move edge (grants-as-gates, 2026-07-10): an operator
// answer to a card becomes a decision object with --reviewed-by provenance
// and per-record undo, instead of a worklog line or a brain paragraph a
// future session has to remember. `compound decisions record` writes a
// DEC-style markdown file under the HOME's decisions/operator/ dir (never
// the repo, never a caller's real ~/.compound outside a test temp home) and
// imports it through the SAME ImportPolicyDecision path every other policy
// decision uses, so it lands in the store with real lifecycle qualifiers —
// grant/freeze-class records the existing admission seams can consume.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"compound/internal/mem"
)

// defaultRecordFalsifier / defaultRecordReviewTrigger let an operator record
// a grant/freeze/ruling without hand-authoring lifecycle proof every time;
// VerdictForCandidate still requires both fields non-empty to promote, so a
// record without an explicit --falsifier/--review-trigger still promotes
// (falsifier a needs an ACTIVE, i.e. promoted, grant to resolve).
const (
	defaultRecordFalsifier     = "A later decisions record explicitly supersedes this one (--undo-of); prose/worklog edits never change it."
	defaultRecordReviewTrigger = "Re-check when a superseding decisions record names this record's id."
)

// RecordInput carries the operator-facing fields for `compound decisions
// record --kind grant|freeze|ruling --reviewed-by <operator>`.
type RecordInput struct {
	Kind          string   // grant | freeze | ruling (required)
	ID            string   // optional; a stable slug is generated from kind+title when empty
	Title         string   // required; the decision choice/title
	ReviewedBy    string   // required; refused without it (falsifier d, 664e1a7 parity)
	Scope         string   // e.g. "director-apply" for a grant
	OwnerGlobs    []string // freeze scope: literal owner globs (settled design decision 2)
	Keywords      []string // freeze scope: literal predicate/evidence keywords
	Domains       []string
	Falsifier     string
	ReviewTrigger string
	UndoOf        string // optional; this record supersedes the named decision id (falsifier c)
	Now           time.Time
}

// RecordReport is what `compound decisions record` prints/returns: where the
// markdown landed and the resulting decision import (evidence/candidate/
// claim + typed lifecycle).
type RecordReport struct {
	Path   string               `json:"path"`
	Import DecisionImportReport `json:"import"`
}

// Record writes the operator decision markdown and imports it through
// ImportPolicyDecision. It refuses (writing nothing) without --kind in
// grant|freeze|ruling, without --title, or without --reviewed-by.
func Record(store *mem.Store, homePath string, in RecordInput) (RecordReport, error) {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return RecordReport{}, fmt.Errorf("decisions record: COMPOUND_HOME required")
	}
	kind := strings.TrimSpace(in.Kind)
	switch kind {
	case KindGrant, KindFreeze, KindRuling:
	default:
		return RecordReport{}, fmt.Errorf("decisions record: --kind must be one of grant|freeze|ruling, got %q", in.Kind)
	}
	reviewedBy := strings.TrimSpace(in.ReviewedBy)
	if reviewedBy == "" {
		return RecordReport{}, fmt.Errorf("decisions record: --reviewed-by required")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return RecordReport{}, fmt.Errorf("decisions record: --title required")
	}
	if store == nil {
		return RecordReport{}, fmt.Errorf("decisions record: mem store required")
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = "operator-" + kind + "-" + digest12(title+"|"+kind)
	}
	falsifier := strings.TrimSpace(in.Falsifier)
	if falsifier == "" {
		falsifier = defaultRecordFalsifier
	}
	reviewTrigger := strings.TrimSpace(in.ReviewTrigger)
	if reviewTrigger == "" {
		reviewTrigger = defaultRecordReviewTrigger
	}

	fm := decFrontmatter{
		ID:               id,
		Title:            title,
		Kind:             kind,
		Status:           StatusAccepted,
		Owner:            reviewedBy,
		ReviewedBy:       reviewedBy,
		Scope:            strings.TrimSpace(in.Scope),
		Domains:          cleanRecordStrings(in.Domains),
		FreezeOwnerGlobs: cleanRecordStrings(in.OwnerGlobs),
		FreezeKeywords:   cleanRecordStrings(in.Keywords),
		Falsifier:        falsifier,
		ReviewTrigger:    reviewTrigger,
	}
	if undoOf := strings.TrimSpace(in.UndoOf); undoOf != "" {
		fm.Supersedes = []string{undoOf}
	}
	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		return RecordReport{}, err
	}
	var body strings.Builder
	body.WriteString("---\n")
	body.Write(fmBytes)
	body.WriteString("---\n\n")
	fmt.Fprintf(&body, "# %s\n\n", title)
	fmt.Fprintf(&body, "Recorded via `compound decisions record --kind %s --reviewed-by %s`.\n", kind, reviewedBy)

	dir := filepath.Join(homePath, "decisions", "operator")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return RecordReport{}, err
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
		return RecordReport{}, err
	}

	report, err := ImportPolicyDecision(store, path, now)
	if err != nil {
		return RecordReport{}, err
	}
	return RecordReport{Path: path, Import: report}, nil
}

func cleanRecordStrings(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
