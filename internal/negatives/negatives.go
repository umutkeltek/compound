// Package negatives is the read-only, rebuildable projection over evidence that an
// approach was already tried and failed: inbox/failure-patterns/ (candidates.FailurePattern,
// written by the distiller — internal/distiller) and reviews/route-remorse/
// (routeevidence.RouteOutcomeReview, written by internal/routeevidence). See
// docs_private/plan/packets/2026-07-10-negatives-at-orient.md ("cite, don't
// redesign"): both producers already exist and are untouched by this package.
//
// It closes Weng harness-RSI map item 6 — "negative results first-class recallable"
// — by giving orient (internal/contract's ContextPack) something to join against.
// Before this package the only propose-time reader of route-remorse was
// internal/director's loadRouteRemorseReviews, which turns remorse into harness-edit
// PROPOSALS for the operator (a PROPOSER). This package WARNS an executor about to
// walk a path a prior lane already burned — read-only display, never routing.
//
// Projection discipline mirrors internal/mem/temperature.go: rebuilt from scratch on
// every call (a rejected/resolved source record disappears on rebuild, never a stale
// tombstone), written only under projections/ (never the ledger). This package reads
// inbox/failure-patterns and reviews/route-remorse as plain JSON directories rather
// than importing internal/distiller or internal/routeevidence — negatives sits below
// both object families (it is a Leaf, like internal/attention), so it takes its own
// minimal decode of the record shapes it needs instead of betting on an upward
// import; the route-remorse schema version is duplicated as a local constant on
// purpose (see routeOutcomeReviewSchemaVersion), the same tradeoff temperature.go
// documents for capabilityUsageRelPath.
package negatives

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/reviewartifact"
)

const (
	// SchemaVersion versions the projection body.
	SchemaVersion = "compound.negatives.v0.1"
	// ProjectionRef is the stable ref for this generated view.
	ProjectionRef = "compound://projections/negatives"
	// RelPath is the home-relative path the projection is written to.
	RelPath = "projections/negatives.json"

	// SourceFailurePattern / SourceRouteRemorse tag which producer a Record came
	// from — the falsifier that both producers surface through the same section
	// reads this field to prove it is one reader, two producers.
	SourceFailurePattern = "failure_pattern"
	SourceRouteRemorse   = "route_remorse"

	// routeRemorseReviewArea mirrors routeevidence's own (unexported)
	// routeOutcomeReviewArea constant: reviews/route-remorse/*.json core artifacts
	// written by routeevidence.RecordRouteOutcomeReview.
	routeRemorseReviewArea = "route-remorse"

	// routeOutcomeReviewSchemaVersion duplicates routeevidence.RouteOutcomeReviewSchemaVersion
	// without an upward import (see package doc).
	routeOutcomeReviewSchemaVersion = "compound.route_outcome_review.v0.1"

	// minScopeTokenLen matches the codebase's established ambient-recall token floor
	// (internal/contract's memoryTextTokens): short tokens are dropped as noise.
	minScopeTokenLen = 3

	// maxApproachKeySlugLen bounds a derived approach key so a long Statement never
	// turns into an unwieldy ref.
	maxApproachKeySlugLen = 60
)

// Record is one already-tried-and-failed approach, scoped for a deterministic join at
// orient time. Owner/TaskClass/ApproachKey are the scope key the packet specifies;
// Owner defaults to "" (unscoped — matches any owner) because neither producer
// carries an owner field today, so a record narrows on owner only when its evidence
// explicitly tags one (an "owner:<value>" evidence ref) — see ownerFromEvidenceRefs.
// WhatWasTried/WhyItFailed are reduced to "" at build time when the source free text
// looked operator-personal, so a stale personal string can never leak through a
// rebuild — ApproachKey and Ref remain so the record still cites its source.
type Record struct {
	Source       string  `json:"source"`
	RefID        string  `json:"ref_id"`
	Owner        string  `json:"owner,omitempty"`
	TaskClass    string  `json:"task_class,omitempty"`
	ApproachKey  string  `json:"approach_key"`
	WhatWasTried string  `json:"what_was_tried,omitempty"`
	WhyItFailed  string  `json:"why_it_failed,omitempty"`
	Ref          string  `json:"ref"`
	Confidence   float64 `json:"confidence,omitempty"`
}

// Projection is the full derived surface written under projections/.
type Projection struct {
	SchemaVersion string   `json:"schema_version"`
	ProjectionRef string   `json:"projection_ref"`
	ComputedAt    string   `json:"computed_at"`
	Records       []Record `json:"records"`
}

// BuildOptions parameterizes the build. Now is the ComputedAt stamp; inject it for
// determinism in tests.
type BuildOptions struct {
	Now time.Time
}

// Build reads the two existing producers fresh from homePath and returns the
// negatives projection. It never mutates either producer; the caller persists the
// result via WriteProjection. Rebuilding is lossless for unchanged inputs and drops
// any record whose source has since been dismissed/resolved (never a stale
// tombstone) — see readFailurePatternRecords / readRouteRemorseRecords.
func Build(homePath string, opts BuildOptions) (Projection, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	proj := Projection{
		SchemaVersion: SchemaVersion,
		ProjectionRef: ProjectionRef,
		ComputedAt:    now.UTC().Format(time.RFC3339),
		Records:       []Record{},
	}
	fp, err := readFailurePatternRecords(homePath)
	if err != nil {
		return proj, err
	}
	rr, err := readRouteRemorseRecords(homePath)
	if err != nil {
		return proj, err
	}
	proj.Records = append(proj.Records, fp...)
	proj.Records = append(proj.Records, rr...)
	sort.Slice(proj.Records, func(i, j int) bool {
		if proj.Records[i].Source != proj.Records[j].Source {
			return proj.Records[i].Source < proj.Records[j].Source
		}
		return proj.Records[i].RefID < proj.Records[j].RefID
	})
	return proj, nil
}

// WriteProjection persists the projection under projections/, returning the absolute
// path written. This is derived state; it must never touch the ledger.
func WriteProjection(homePath string, proj Projection) (string, error) {
	path := filepath.Join(homePath, filepath.FromSlash(RelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(proj, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ReadProjection loads the persisted projection. The bool is false (with no error)
// when the projection has not been built yet — an expected, not exceptional, state
// (fail-open, mirroring mem.ReadTemperature).
func ReadProjection(homePath string) (Projection, bool, error) {
	path := filepath.Join(homePath, filepath.FromSlash(RelPath))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Projection{}, false, nil
		}
		return Projection{}, false, err
	}
	var proj Projection
	if err := json.Unmarshal(data, &proj); err != nil {
		return Projection{}, false, err
	}
	return proj, true, nil
}

// ScopeTokens tokenizes free text into the deterministic, no-LLM scope-match
// vocabulary: lowercased content runs of length >= minScopeTokenLen. It mirrors
// internal/contract's own memoryTextTokens tokenizer so callers reuse the same
// "is this in scope" primitive the ambient-memory ranker already established.
func ScopeTokens(values ...string) map[string]bool {
	tokens := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
		}) {
			if len(part) >= minScopeTokenLen {
				tokens[part] = true
			}
		}
	}
	return tokens
}

// inScope is the deterministic "scope match, not dump" gate: a record with a
// non-empty Owner only surfaces when the query's owner tokens name it; every record
// must also share at least one token with the query's task tokens (derived from its
// own ApproachKey + TaskClass) — a disjoint task never matches, no matter how
// severe the recorded failure was.
func (r Record) inScope(ownerTokens, taskTokens map[string]bool) bool {
	if owner := strings.TrimSpace(r.Owner); owner != "" && !ownerTokens[owner] {
		return false
	}
	scope := ScopeTokens(r.ApproachKey, r.TaskClass)
	if len(scope) == 0 {
		return false
	}
	for token := range scope {
		if taskTokens[token] {
			return true
		}
	}
	return false
}

// MatchingRecords returns the records in scope for the given owner/task query
// tokens, deterministically ordered (source, then ref id) and capped to limit (<=0
// means unbounded) — the read-only orient-time join. It never inspects or affects
// route/dispatch/claim state; it is a pure filter over already-built records.
func MatchingRecords(records []Record, ownerTokens, taskTokens map[string]bool, limit int) []Record {
	var out []Record
	for _, r := range records {
		if r.inScope(ownerTokens, taskTokens) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].RefID < out[j].RefID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// personalPatterns is a minimal, deterministic "looks operator-personal" heuristic:
// an email-like token or an absolute home-directory path. It is intentionally
// narrow (no NLP, no model call) — good enough to catch a planted personal string
// and reduce free text to structured form before it is ever persisted or displayed.
var personalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),
	regexp.MustCompile(`/Users/[A-Za-z0-9_.-]+`),
	regexp.MustCompile(`(?i)/home/[A-Za-z0-9_.-]+`),
}

func looksPersonal(values ...string) bool {
	for _, value := range values {
		if strings.Contains(value, "~/") {
			return true
		}
		for _, p := range personalPatterns {
			if p.MatchString(value) {
				return true
			}
		}
	}
	return false
}

// ownerFromEvidenceRefs derives the optional Owner scope dimension from an
// "owner:<value>" evidence ref, a lightweight additive convention on the existing
// free-form evidence_refs[] field — no producer schema change required. Absent the
// convention, Owner stays "" (unscoped: the record narrows on task-class only).
func ownerFromEvidenceRefs(refs []string) string {
	for _, ref := range refs {
		if v, ok := strings.CutPrefix(strings.TrimSpace(ref), "owner:"); ok {
			if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
				return v
			}
		}
	}
	return ""
}

// failurePatternRaw is the minimal decode shape of an inbox/failure-patterns/*.json
// candidate record (compound/internal/distiller.Candidate's JSON projection) — see
// the package doc for why this duplicates rather than imports distiller.
type failurePatternRaw struct {
	ID           string   `json:"id"`
	Subject      string   `json:"subject"`
	Statement    string   `json:"statement"`
	Detail       string   `json:"detail"`
	Confidence   float64  `json:"confidence"`
	EvidenceRefs []string `json:"evidence_refs"`
	Verdict      string   `json:"verdict"`
}

// readFailurePatternRecords reads every inbox/failure-patterns/*.json candidate and
// turns Park/Operationalize verdicts into Records; Reject (dismissed — "not a real
// failure") is dropped, so a rejected candidate never becomes a stale tombstone.
func readFailurePatternRecords(homePath string) ([]Record, error) {
	dir := filepath.Join(homePath, candidates.InboxDir(candidates.FailurePattern))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var raw failurePatternRaw
		if json.Unmarshal(data, &raw) != nil {
			continue // skip torn/foreign json rather than brick the build
		}
		if strings.TrimSpace(raw.ID) == "" {
			continue
		}
		if raw.Verdict == string(candidates.Reject) {
			continue // dismissed: this was not a valid failure — drop, never surface
		}
		personal := looksPersonal(raw.Subject, raw.Statement, raw.Detail)
		var approachKey string
		if personal {
			// A personal Subject/Statement must never leak a fragment into the
			// approach key either (slugify only strips separators, it does not
			// redact) — fall back to an opaque digest of the candidate id.
			approachKey = "fp-" + reviewartifact.Digest12(raw.ID)
		} else {
			approachKey = reviewartifact.FirstNonEmpty(
				reviewartifact.Slugify(raw.Subject, 0),
				reviewartifact.Slugify(raw.Statement, maxApproachKeySlugLen),
				"fp-"+reviewartifact.Digest12(raw.ID),
			)
		}
		whatTried := strings.TrimSpace(raw.Statement)
		whyFailed := reviewartifact.FirstNonEmpty(raw.Detail, "flagged as a recorded failure pattern")
		taskClass := strings.TrimSpace(raw.Detail)
		if personal {
			whatTried, whyFailed, taskClass = "", "", ""
		}
		out = append(out, Record{
			Source:       SourceFailurePattern,
			RefID:        raw.ID,
			Owner:        ownerFromEvidenceRefs(raw.EvidenceRefs),
			TaskClass:    taskClass,
			ApproachKey:  approachKey,
			WhatWasTried: whatTried,
			WhyItFailed:  whyFailed,
			Ref:          "candidate://" + string(candidates.FailurePattern) + "/" + raw.ID,
			Confidence:   raw.Confidence,
		})
	}
	return out, nil
}

// routeOutcomeReviewRaw is the minimal decode shape of a
// reviews/route-remorse/*.json core artifact (routeevidence.RouteOutcomeReview's
// JSON projection) — see the package doc for why this duplicates rather than
// imports routeevidence.
type routeOutcomeReviewRaw struct {
	SchemaVersion     string   `json:"schema_version"`
	ID                string   `json:"id"`
	CapabilityClasses []string `json:"capability_classes"`
	EvidenceRefs      []string `json:"evidence_refs"`
	ReceiptOutcome    string   `json:"receipt_outcome"`
	SelectedTarget    struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Protocol string `json:"protocol"`
	} `json:"selected_target"`
	Assessment struct {
		Class              string `json:"class"`
		ShouldReviewPolicy bool   `json:"should_review_policy"`
		Rationale          string `json:"rationale"`
	} `json:"assessment"`
}

// readRouteRemorseRecords reads every reviews/route-remorse/*.json core artifact
// (skipping the .reviewed./.apply./.decision. siblings, the same convention
// internal/director's loadRouteRemorseReviews uses) and turns should_review_policy
// reviews into Records; a review whose assessment no longer flags a policy concern
// (should_review_policy=false — resolved) is dropped, so it never becomes a stale
// tombstone.
func readRouteRemorseRecords(homePath string) ([]Record, error) {
	dir := filepath.Join(homePath, "reviews", routeRemorseReviewArea)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.Contains(name, ".reviewed.") || strings.Contains(name, ".apply.") || strings.Contains(name, ".decision.") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var raw routeOutcomeReviewRaw
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		if raw.SchemaVersion != routeOutcomeReviewSchemaVersion || strings.TrimSpace(raw.ID) == "" {
			continue
		}
		if !raw.Assessment.ShouldReviewPolicy {
			continue // resolved: no policy concern anymore — drop, never surface
		}
		routeKey := strings.Trim(strings.Join([]string{raw.SelectedTarget.Provider, raw.SelectedTarget.Model, raw.SelectedTarget.Protocol}, "/"), "/")
		approachKey := reviewartifact.Slugify(routeKey, 0)
		if approachKey == "" {
			approachKey = "ror-" + reviewartifact.Digest12(raw.ID)
		}
		taskClass := ""
		if len(raw.CapabilityClasses) > 0 {
			taskClass = strings.TrimSpace(raw.CapabilityClasses[0])
		}
		whatTried := strings.TrimSpace(fmt.Sprintf("routed to %s", reviewartifact.FirstNonEmpty(routeKey, raw.ID)))
		whyFailed := reviewartifact.FirstNonEmpty(raw.Assessment.Rationale, raw.ReceiptOutcome, "route outcome flagged for policy review")
		if looksPersonal(whatTried, whyFailed) {
			whatTried, whyFailed = "", ""
		}
		out = append(out, Record{
			Source:       SourceRouteRemorse,
			RefID:        raw.ID,
			Owner:        ownerFromEvidenceRefs(raw.EvidenceRefs),
			TaskClass:    taskClass,
			ApproachKey:  approachKey,
			WhatWasTried: whatTried,
			WhyItFailed:  whyFailed,
			Ref:          "route-remorse://" + raw.ID,
			Confidence:   1,
		})
	}
	return out, nil
}
