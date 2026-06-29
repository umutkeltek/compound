// Package decisions imports decision sources into the spine memory family. A decision
// source becomes a mem candidate; promotion remains a separate adjudication step.
package decisions

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"compound/internal/candidates"
	"compound/internal/mem"
	"compound/internal/writeback"
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

// LifecycleGap identifies a promoted decision claim missing lifecycle proof.
type LifecycleGap struct {
	ClaimID      string   `json:"claim_id"`
	DecisionID   string   `json:"decision_id,omitempty"`
	Kind         string   `json:"kind"`
	Status       string   `json:"status"`
	Missing      []string `json:"missing"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

// DoctorReport is the typed report behind `compound decisions doctor`.
type DoctorReport struct {
	Total                  int            `json:"total"`
	Counts                 map[string]int `json:"counts"`
	Statuses               map[string]int `json:"statuses"`
	MissingLifecycleFields int            `json:"missing_lifecycle_fields"`
	LifecycleGaps          []LifecycleGap `json:"lifecycle_gaps,omitempty"`
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
	Kind          string   `yaml:"kind"`
	Status        string   `yaml:"status"`
	Domains       []string `yaml:"domains"`
	Supersedes    []string `yaml:"supersedes"`
	Falsifier     string   `yaml:"falsifier"`
	ReviewTrigger string   `yaml:"review_trigger"`
	Owner         string   `yaml:"owner"`
	Scope         string   `yaml:"scope"`
}

type BrainDecisionReport struct {
	SourcePath  string    `json:"source_path"`
	EvidenceID  string    `json:"evidence_id"`
	CandidateID string    `json:"candidate_id"`
	ClaimID     string    `json:"claim_id,omitempty"`
	Verdict     string    `json:"verdict"`
	Lifecycle   Lifecycle `json:"lifecycle"`
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

// ImportBrainDecision imports one curated knowledge-base decision page into the
// owned decision lifecycle path. The markdown page is evidence; deterministic
// frontmatter/body parsing proposes the decision, and code adjudicates it.
func ImportBrainDecision(store *mem.Store, path string, now time.Time) (BrainDecisionReport, error) {
	var report BrainDecisionReport
	b, err := os.ReadFile(path)
	if err != nil {
		return report, err
	}
	fm, body := splitFrontmatter(string(b))
	var f decFrontmatter
	_ = yaml.Unmarshal([]byte(fm), &f)
	if f.Title == "" {
		f.Title = firstHeading(body)
	}
	if strings.TrimSpace(f.Title) == "" {
		return report, fmt.Errorf("brain decision %s: title required", path)
	}
	if f.ID == "" {
		f.ID = "brain-" + digest12(path+"\n"+f.Title)
	}
	if f.Kind == "" {
		f.Kind = KindDoctrine
	}
	if f.Falsifier == "" {
		if mm := reFalsifier.FindStringSubmatch(body); mm != nil {
			f.Falsifier = strings.TrimSpace(mm[1])
		}
	}
	if f.ReviewTrigger == "" {
		if mm := reReviewTrigger.FindStringSubmatch(body); mm != nil {
			f.ReviewTrigger = strings.TrimSpace(mm[1])
		}
	}
	evidence, err := store.RecordEvidence(mem.EvidenceItem{
		SourceKind: "note",
		SourceID:   "brain:" + filepath.ToSlash(path),
		QuoteText:  decisionQuote(f.Title, body),
		Actor:      "tool",
		Visibility: "private",
	}, now)
	if err != nil {
		return report, err
	}
	q := lifecycleQualifiers(f.Kind, "brain", f.ID, f.Status)
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
	cands, err := store.Capture([]mem.Candidate{{
		Entity:         "decisions",
		Predicate:      "decided",
		Object:         f.Title,
		Confidence:     1.0,
		Derivation:     "deterministic",
		ClaimType:      "decision",
		Qualifiers:     q,
		PromotionBasis: promotionBasisForStatus(f.Status),
		SourceTraceRef: evidence.ID,
		SnippetHash:    digest12(f.ID + "\n" + f.Title),
		Visibility:     "operational",
	}}, now)
	if err != nil {
		return report, err
	}
	verdict := candidates.Park
	if cands[0].PromotionBasis != "" {
		verdict = candidates.Promote
	}
	res, err := store.Adjudicate(cands[0].ID, verdict, "brain-decision-import", now)
	if err != nil {
		return report, err
	}
	report = BrainDecisionReport{
		SourcePath:  path,
		EvidenceID:  evidence.ID,
		CandidateID: cands[0].ID,
		Verdict:     verdict,
		Lifecycle:   LifecycleFromCandidate(cands[0]),
	}
	if res.Claim != nil {
		report.ClaimID = res.Claim.ID
		report.Lifecycle = LifecycleFromClaim(*res.Claim)
	}
	return report, nil
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

// DoctorReportFromClaims projects current decision claims into a lifecycle audit.
func DoctorReportFromClaims(claims []mem.Claim) DoctorReport {
	report := DoctorReport{
		Counts:   map[string]int{},
		Statuses: map[string]int{},
	}
	for _, c := range claims {
		if c.ClaimType != "decision" {
			continue
		}
		l := LifecycleFromClaim(c)
		report.Total++
		report.Counts[l.Kind]++
		report.Statuses[l.Status]++
		missing := MissingRequired(l)
		if len(missing) == 0 {
			continue
		}
		report.MissingLifecycleFields++
		report.LifecycleGaps = append(report.LifecycleGaps, LifecycleGap{
			ClaimID:      c.ID,
			DecisionID:   l.ID,
			Kind:         l.Kind,
			Status:       l.Status,
			Missing:      append([]string(nil), missing...),
			EvidenceRefs: append([]string(nil), l.EvidenceRefs...),
		})
	}
	return report
}

// NeedsWriteback reports whether decision doctor found owner-fixable gaps.
func NeedsWriteback(report DoctorReport) bool {
	return len(report.LifecycleGaps) > 0
}

// WritebackGate converts decision lifecycle gaps into generic owner writeback.
func WritebackGate(report DoctorReport, actionIntentRef, policyDecisionRef string, now time.Time) writeback.OwnerWritebackGate {
	return writeback.OwnerWritebackGate{
		SchemaVersion:     writeback.GateSchemaVersion,
		CreatedAt:         now.UTC().Format(time.RFC3339),
		CandidateRef:      fmt.Sprintf("decisions:lifecycle_gaps:%d:%s", len(report.LifecycleGaps), lifecycleGapDigest(report)),
		CandidateKind:     string(candidates.Decision),
		OwnerTarget:       writeback.OwnerTarget{Kind: "decision_lifecycle", Ref: "spine-memory-decisions"},
		GateBasis:         fmt.Sprintf("decision doctor found %d lifecycle gap(s); owner decision records may need writeback", len(report.LifecycleGaps)),
		ActionIntentRef:   actionIntentRef,
		MutationClass:     "local_reversible",
		PolicyDecisionRef: policyDecisionRef,
		EvidenceRefs:      EvidenceRefs(report),
	}
}

// EvidenceRefs returns bounded metadata refs for decision doctor output.
func EvidenceRefs(report DoctorReport) []string {
	refs := []string{
		fmt.Sprintf("decisions:total:%d", report.Total),
		fmt.Sprintf("decisions:missing_lifecycle_fields:%d", report.MissingLifecycleFields),
	}
	for _, gap := range report.LifecycleGaps {
		id := gap.DecisionID
		if strings.TrimSpace(id) == "" {
			id = gap.ClaimID
		}
		refs = append(refs, "decision_gap:"+id+":"+strings.Join(gap.Missing, ","))
	}
	return refs
}

func lifecycleGapDigest(report DoctorReport) string {
	refs := EvidenceRefs(report)
	sort.Strings(refs)
	sum := sha256.Sum256([]byte(strings.Join(refs, "\n")))
	return hex.EncodeToString(sum[:])[:12]
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

func firstHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	return ""
}

func decisionQuote(title, body string) string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
		if len(strings.Join(lines, " ")) >= 240 {
			break
		}
	}
	quote := strings.TrimSpace(strings.Join(lines, " "))
	if len(quote) > 320 {
		quote = quote[:320]
	}
	if quote == "" {
		return title
	}
	return title + "\n" + quote
}

func digest12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
