// Package interest is the read-only, rebuildable interestingness projection over
// failed proof results (Schmidhuber, "Driven by Compression Progress"). Each failed
// work.proof_resource.result is reduced to one public-safe signature from its
// proof report; signatures cluster and each cluster is assimilated (a finding
// names its signature exactly, for every repository it was seen in), learnable
// (it recurs unexplained across work ids or days) or singleton. A note that only
// mentions the failure is related evidence, not an explanation, and an
// explanation is not a verified repair. Attention goes to learnable regularity,
// not to error volume or severity, and progress is the derivative: the
// observations an explanation newly absorbed since the previous persisted
// projection.
//
// Discipline mirrors internal/negatives: rebuilt from scratch, written only under
// projections/, and telemetry never reds anything (an unreadable input is a
// warning). It is a Leaf below internal/work, so it duplicates the two work
// constants it needs instead of importing upward (see proofResultKind).
package interest

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"compound/internal/eventstore"
	"compound/internal/finding"
	"compound/internal/home"
	"compound/internal/negatives"
)

const (
	SchemaVersion = "compound.interest.v0.2"
	ProjectionRef = "compound://projections/interest"
	RelPath       = "projections/interest.json"
	DefaultWindow = 30 * 24 * time.Hour

	StateAssimilated = "assimilated"
	StateLearnable   = "learnable"
	StateSingleton   = "singleton"
	MatchExact       = "exact"
	MatchFuzzy       = "fuzzy"
	ScopeUnscoped    = "unscoped"  // names no repository; the signature was seen in several
	ScopeUncovered   = "uncovered" // names repositories that miss one the signature was seen in

	// SignatureEvidencePrefix ties a finding to one signature exactly:
	// evidence_refs contains "signature:<psig id>". RepositoryEvidencePrefix
	// scopes it: "repository:<fingerprint>", one ref per repository it covers.
	SignatureEvidencePrefix  = "signature:"
	RepositoryEvidencePrefix = "repository:"

	// proofResultKind and proofResultFailed duplicate internal/work's
	// ProofResourceResult.Kind value and ProofResourceFailed (no upward import).
	proofResultKind   = "work.proof_resource.result"
	proofResultFailed = "failed"

	packageTimeoutSeconds  = 1800
	maxSignatureRunes      = 160
	minFuzzySignatureRunes = 16
	maxWarnings            = 20
)

type Projection struct {
	SchemaVersion string      `json:"schema_version"`
	ProjectionRef string      `json:"projection_ref"`
	ComputedAt    string      `json:"computed_at"`
	Window        Window      `json:"window"`
	Totals        Totals      `json:"totals"`
	Signatures    []Signature `json:"signatures"`
	Progress      Progress    `json:"progress"`
	Warnings      []Warning   `json:"warnings"`
}

type Window struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// Totals: assimilated + learnable + singleton results = WithReport;
// AssimilatedShare = AssimilatedResults / FailedResults.
type Totals struct {
	FailedResults      int     `json:"failed_results"`
	WithReport         int     `json:"with_report"`
	WithoutReport      int     `json:"without_report"`
	AssimilatedResults int     `json:"assimilated_results"`
	LearnableResults   int     `json:"learnable_results"`
	SingletonResults   int     `json:"singleton_results"`
	AssimilatedShare   float64 `json:"assimilated_share"`
}

// Signature is one cluster. Repositories are fingerprints, never checkout paths.
type Signature struct {
	ID            string        `json:"id"`
	Step          string        `json:"step"`
	Signature     string        `json:"signature"`
	Tests         []string      `json:"tests"`
	Occurrences   int           `json:"occurrences"`
	FirstSeen     string        `json:"first_seen"`
	LastSeen      string        `json:"last_seen"`
	WorkIDs       []string      `json:"work_ids"`
	Repositories  []string      `json:"repositories"`
	ResourceShape bool          `json:"resource_shape"`
	State         string        `json:"state"`
	ExplainedBy   []Explanation `json:"explained_by"`
	Related       []Explanation `json:"related"`
	AttentionRank int           `json:"attention_rank"`
}

type Explanation struct {
	Ref   string `json:"ref"`
	Match string `json:"match"`
	Scope string `json:"scope,omitempty"`
}

type Progress struct {
	PreviousComputedAt     string     `json:"previous_computed_at"`
	NewlyAssimilated       []Absorbed `json:"newly_assimilated"`
	NewlyLearnable         []string   `json:"newly_learnable"`
	AssimilatedShareBefore float64    `json:"assimilated_share_before"`
}

// Absorbed: a newly assimilated signature and the observations it absorbed.
type Absorbed struct {
	ID          string `json:"id"`
	Occurrences int    `json:"occurrences"`
}

type Warning struct {
	Source string `json:"source"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// BuildOptions: Now stamps the build and ends the window; Since is the window
// length (DefaultWindow when zero).
type BuildOptions struct {
	Now   time.Time
	Since time.Duration
}

type observation struct {
	step, signature, workID, repo string
	tests                         []string
	resource                      bool
	at                            time.Time
}

type cluster struct {
	sig                       Signature
	works, repos, tests, days map[string]bool
}

type explainer struct {
	ref          string
	exact, repos map[string]bool
	text         string
}

type warnings []Warning

func (w *warnings) add(source, code, detail string) {
	switch {
	case len(*w) < maxWarnings:
		*w = append(*w, Warning{Source: source, Code: code, Detail: scrubPaths(detail)})
	case len(*w) == maxWarnings:
		*w = append(*w, Warning{Source: "build", Code: "warnings_truncated", Detail: "further warnings omitted"})
	}
}

// Build derives the projection fresh from homePath without writing anything;
// persist it with WriteProjection. Only an unreadable event store is an error.
func Build(homePath string, opts BuildOptions) (Projection, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	if opts.Since <= 0 {
		opts.Since = DefaultWindow
	}
	since := now.Add(-opts.Since)
	proj := Projection{
		SchemaVersion: SchemaVersion,
		ProjectionRef: ProjectionRef,
		ComputedAt:    now.Format(time.RFC3339),
		Window:        Window{Since: since.Format(time.RFC3339), Until: now.Format(time.RFC3339)},
		Progress:      Progress{NewlyAssimilated: []Absorbed{}, NewlyLearnable: []string{}},
	}
	w := warnings{}
	obs, err := readFailures(homePath, since, now, &proj.Totals, &w)
	if err != nil {
		return Projection{}, err
	}
	proj.Signatures = classify(obs, readExplanations(homePath, now, &w))
	t := &proj.Totals
	results := map[string]*int{StateAssimilated: &t.AssimilatedResults, StateLearnable: &t.LearnableResults, StateSingleton: &t.SingletonResults}
	for _, s := range proj.Signatures {
		*results[s.State] += s.Occurrences
	}
	t.AssimilatedShare = math.Round(float64(t.AssimilatedResults)/float64(max(t.FailedResults, 1))*10000) / 10000
	applyProgress(homePath, &proj, &w)
	proj.Warnings = append([]Warning{}, w...)
	return proj, nil
}

type snapshot struct {
	RepositoryFingerprint string `json:"repository_fingerprint"`
}

type resultRecord struct {
	Kind              string    `json:"kind"`
	Status            string    `json:"status"`
	CreatedAt         string    `json:"created_at"`
	WorkID            string    `json:"work_id"`
	ReportStatus      string    `json:"report_status"`
	ReportArtifactRef string    `json:"report_artifact_ref"`
	SourceSnapshot    *snapshot `json:"source_snapshot"`
}

// proofReport deliberately omits the report's repo path: identity is the fingerprint.
type proofReport struct {
	SourceSnapshot *snapshot `json:"source_snapshot"`
	Steps          []struct {
		Name         string   `json:"name"`
		Status       string   `json:"status"`
		OutputTail   string   `json:"output_tail"`
		FailureLines []string `json:"failure_lines"`
	} `json:"steps"`
}

// readFailures enumerates pfrs_ ids through the store's index-screened
// EnumerateIDs and batch-reads them. A prefix Scan is not used: pfrs_ has no
// family routing, so Scan would decode every record of every family.
func readFailures(homePath string, since, until time.Time, totals *Totals, w *warnings) ([]observation, error) {
	store, err := eventstore.Open(homePath)
	if err != nil {
		return nil, err
	}
	ids, err := store.EnumerateIDs("pfrs_", false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	results := store.ReadMany(ids)
	var out []observation
	for _, id := range ids {
		var rec resultRecord
		if res := results[id]; res.Err != nil {
			w.add("event", "unreadable", id)
			continue
		} else if json.Unmarshal(res.Record.Bytes, &rec) != nil {
			w.add("event", "invalid_json", id)
			continue
		}
		if rec.Kind != proofResultKind || (rec.Status != proofResultFailed && rec.ReportStatus != proofResultFailed) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, rec.CreatedAt)
		if err != nil {
			w.add("event", "invalid_created_at", id)
			continue
		}
		if at.Before(since) || at.After(until) {
			continue
		}
		totals.FailedResults++
		report, ok := readReport(homePath, id, rec.ReportArtifactRef, w)
		if !ok {
			totals.WithoutReport++
			continue
		}
		totals.WithReport++
		o := deriveObservation(report)
		o.at, o.workID = at.UTC(), rec.WorkID
		for _, snap := range []*snapshot{rec.SourceSnapshot, report.SourceSnapshot} {
			if o.repo == "" && snap != nil {
				o.repo = snap.RepositoryFingerprint
			}
		}
		out = append(out, o)
	}
	return out, nil
}

// readReport reads a content-addressed report without creating or repairing any
// custody path and verifies its digest. No ref is normal: no warning.
func readReport(homePath, id, ref string, w *warnings) (proofReport, bool) {
	var report proofReport
	if strings.TrimSpace(ref) == "" {
		return report, false
	}
	code := ""
	digest, err := home.ArtifactHex(ref)
	var body []byte
	if err != nil {
		code = "invalid_ref"
	} else if body, err = os.ReadFile(filepath.Join(homePath, "artifacts", "sha256", digest)); err != nil {
		code = "missing"
	} else if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != digest {
		code = "hash_mismatch"
	} else if json.Unmarshal(body, &report) != nil {
		code = "invalid_json"
	}
	if code != "" {
		w.add("artifact", code, id)
	}
	return report, code == ""
}

// deriveObservation reduces the first step that neither passed, was skipped, nor
// was only planned (dry run).
func deriveObservation(report proofReport) observation {
	for _, step := range report.Steps {
		if step.Status != "ok" && step.Status != "skipped" && step.Status != "planned" {
			// The battery's own failure_lines extraction survives even when the
			// --- FAIL line scrolled out of the bounded output_tail (30 of the real
			// "exit status 1" go-test tails carried their test names only there).
			sig, tests, resource := signatureFromTail(strings.Join(step.FailureLines, "\n") + "\n" + step.OutputTail)
			return observation{step: scrubPaths(step.Name), signature: sig, tests: tests, resource: resource}
		}
	}
	return observation{step: "-", signature: "report:no-failing-step"}
}

var (
	failLineRE   = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
	pkgFailRE    = regexp.MustCompile(`^FAIL\t(\S+)\t(\d+(?:\.\d+)?)s$`)
	pathRE       = regexp.MustCompile(`(^|[^A-Za-z0-9_.~-])/[^\s'"]+`)
	homeRE       = regexp.MustCompile(`(^|[^A-Za-z0-9_.-])~/[^\s'"]*`)
	hexRE        = regexp.MustCompile(`\b[0-9a-fA-F]{7,}\b`)
	numberRE     = regexp.MustCompile(`\d+(?:\.\d+)?`)
	maskRE       = regexp.MustCompile(`\*{4,}`)
	testTokenRE  = regexp.MustCompile(`^Test[A-Za-z0-9_]{5,}$`)
	resourceHits = []string{"signal: killed", "signal: terminated", "context canceled"}
	// cancelledTailRE: a passing `ok <pkg> <secs>s` package line or a testbattery
	// phase header as the LAST line means the run was cut off, not failed.
	cancelledTailRE = regexp.MustCompile(`^(ok\s+\S+\s+\d+(?:\.\d+)?s|testbattery: phase \d+/\d+ packages=\d+)$`)
	lineMarkers     = []string{"no such file or directory", "FAILED ", "MEDIUM ", "HIGH ", "EOF", "error:"}
)

// signatureFromTail applies the ordered rules: go-test failures, package
// timeouts, resource kills, a marked line, the last non-empty line. The bool
// marks a resource-shaped signature (timeout or kill).
func signatureFromTail(tail string) (string, []string, bool) {
	lines := strings.Split(strings.ReplaceAll(tail, "\r\n", "\n"), "\n")
	var tests []string
	for _, line := range lines {
		if m := failLineRE.FindStringSubmatch(line); m != nil {
			if name, _, _ := strings.Cut(m[1], "/"); !slices.Contains(tests, name) {
				tests = append(tests, name)
			}
		}
	}
	if len(tests) > 0 {
		return "fail:" + tests[0], tests, false
	}
	pkg, panicAt := "", -1
	for i, line := range lines {
		if m := pkgFailRE.FindStringSubmatch(strings.TrimRight(line, " \t")); m != nil && pkg == "" {
			if secs, err := strconv.ParseFloat(m[2], 64); err == nil && secs >= packageTimeoutSeconds {
				pkg = m[1]
			}
		}
		if panicAt < 0 && strings.Contains(line, "panic: test timed out") {
			panicAt = i
		}
	}
	if pkg == "" && panicAt >= 0 {
		pkg = "unknown" // the package is the first FAIL line after the panic
		for _, line := range lines[panicAt:] {
			if m := pkgFailRE.FindStringSubmatch(strings.TrimRight(line, " \t")); m != nil {
				pkg = m[1]
				break
			}
		}
	}
	if pkg != "" {
		return "package-timeout:" + scrubPaths(pkg), nil, true
	}
	if _, phrase := firstHit(lines, resourceHits); phrase != "" {
		return "resource:" + phrase, nil, true
	}
	if last := lastNonEmpty(lines); cancelledTailRE.MatchString(last) {
		// The tail ends on a passing package or a phase header: the run was
		// stopped before any failure reached the output (the "go-test exit -1,
		// later gates context canceled" shape). Noise, not a regularity.
		return "resource:cancelled-run", nil, true
	}
	if line, _ := firstHit(lines, lineMarkers); line != "" {
		return Normalize(line), nil, false
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return Normalize(lines[i]), nil, false
		}
	}
	return "empty-output", nil, false
}

// lastNonEmpty returns the last line with non-blank content, trimmed.
func lastNonEmpty(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// firstHit returns the first line holding any needle, and that needle.
func firstHit(lines, needles []string) (string, string) {
	for _, line := range lines {
		for _, needle := range needles {
			if strings.Contains(line, needle) {
				return line, needle
			}
		}
	}
	return "", ""
}

// scrubPaths replaces absolute and home-relative paths with <path>.
func scrubPaths(s string) string {
	return homeRE.ReplaceAllString(pathRE.ReplaceAllString(s, "${1}<path>"), "${1}<path>")
}

// Normalize makes a failure line public-safe and variation-free: paths become
// <path>, 7+ hex runs holding a digit <hex>, numbers <n>, redaction masks (4+
// asterisks, length varies with the value) <mask>; whitespace collapses; the
// result is capped at 160 runes.
func Normalize(line string) string {
	s := hexRE.ReplaceAllStringFunc(scrubPaths(line), func(run string) string {
		if strings.ContainsAny(run, "0123456789") {
			return "<hex>"
		}
		return run
	})
	s = maskRE.ReplaceAllString(numberRE.ReplaceAllString(s, "<n>"), "<mask>")
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxSignatureRunes {
		s = string(r[:maxSignatureRunes])
	}
	return s
}

// SignatureID is "psig_" + the first 12 hex of sha256(step NUL signature).
func SignatureID(step, signature string) string {
	sum := sha256.Sum256([]byte(step + "\x00" + signature))
	return "psig_" + hex.EncodeToString(sum[:])[:12]
}

// readExplanations loads the Compound-owned explanation stores: findings (exact
// on topic or a signature: evidence ref, fuzzy on text) and negatives (fuzzy).
func readExplanations(homePath string, now time.Time, w *warnings) []explainer {
	var out []explainer
	dir := filepath.Join(homePath, filepath.FromSlash(finding.Subdir))
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		w.add("finding", "unreadable_dir", finding.Subdir)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var f finding.Finding
		if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err != nil || json.Unmarshal(data, &f) != nil || f.ID == "" {
			w.add("finding", "not_a_finding", e.Name()+": unreadable, not JSON, or no finding id")
			continue
		}
		exact, repos := map[string]bool{strings.TrimSpace(f.Topic): true}, map[string]bool{}
		for _, ref := range f.EvidenceRefs {
			ref = strings.TrimSpace(ref)
			if id, ok := strings.CutPrefix(ref, SignatureEvidencePrefix); ok {
				exact[id] = true
			} else if fp, ok := strings.CutPrefix(ref, RepositoryEvidencePrefix); ok {
				add(repos, fp)
			}
		}
		out = append(out, explainer{ref: "finding:" + f.ID, exact: exact, repos: repos, text: f.Topic + "\n" + f.Body})
	}
	neg, _, err := negatives.ReadFresh(homePath, now)
	if err != nil {
		w.add("negatives", "unreadable", "negatives projection could not be read")
	}
	for _, r := range neg.Records {
		out = append(out, explainer{ref: r.Ref, text: strings.Join([]string{r.ApproachKey, r.TaskClass, r.WhatWasTried, r.WhyItFailed}, "\n")})
	}
	return out
}

// classify clusters observations, joins explanations, sets state and rank:
// learnable, singleton, assimilated; then occurrences desc, last_seen desc, id asc.
func classify(obs []observation, exps []explainer) []Signature {
	clusters := map[string]*cluster{}
	for _, o := range obs {
		id := SignatureID(o.step, o.signature)
		c := clusters[id]
		if c == nil {
			c = &cluster{sig: Signature{ID: id, Step: o.step, Signature: o.signature},
				works: map[string]bool{}, repos: map[string]bool{}, tests: map[string]bool{}, days: map[string]bool{}}
			clusters[id] = c
		}
		c.sig.Occurrences++
		c.sig.ResourceShape = c.sig.ResourceShape || o.resource
		at := o.at.Format(time.RFC3339) // fixed-width UTC: lexical order is time order
		if c.sig.FirstSeen == "" || at < c.sig.FirstSeen {
			c.sig.FirstSeen = at
		}
		c.sig.LastSeen = max(c.sig.LastSeen, at)
		c.days[at[:len(time.DateOnly)]] = true
		add(c.works, o.workID)
		add(c.repos, o.repo)
		for _, t := range o.tests {
			add(c.tests, t)
		}
	}
	out := make([]Signature, 0, len(clusters))
	for _, c := range clusters {
		s := c.sig
		s.WorkIDs, s.Repositories, s.Tests = sorted(c.works, 10), sorted(c.repos, 5), sorted(c.tests, 10)
		s.ExplainedBy, s.Related = explain(s, c.repos, exps)
		switch {
		case len(s.ExplainedBy) > 0:
			s.State = StateAssimilated
		case s.Occurrences >= 2 && (len(c.works) >= 2 || len(c.days) >= 2):
			s.State = StateLearnable
		default: // one observation, or one work retried within a day
			s.State = StateSingleton
		}
		out = append(out, s)
	}
	order := map[string]int{StateLearnable: 0, StateSingleton: 1, StateAssimilated: 2}
	slices.SortFunc(out, func(a, b Signature) int {
		// Learnable first: independent recurrence outranks one work's retries,
		// however many. This is attention, not severity, which the projection
		// does not judge. Within a state, regular shapes before resource shapes:
		// a kill or cut-off run says less on its own, though load, a leak or a
		// broken host can sit behind it. Then volume, recency, id.
		return cmp.Or(cmp.Compare(order[a.State], order[b.State]), compareBool(a.ResourceShape, b.ResourceShape),
			cmp.Compare(b.Occurrences, a.Occurrences), strings.Compare(b.LastSeen, a.LastSeen), strings.Compare(a.ID, b.ID))
	})
	for i := range out {
		out[i].AttentionRank = i + 1
	}
	return out
}

func add(set map[string]bool, v string) {
	if v = strings.TrimSpace(v); v != "" {
		set[v] = true
	}
}

func sorted(set map[string]bool, limit int) []string {
	out := append([]string{}, slices.Sorted(maps.Keys(set))...)
	return out[:min(len(out), limit)]
}

func compareBool(a, b bool) int {
	if a == b {
		return 0
	} else if a {
		return 1
	}
	return -1
}

// explain joins a signature to held explanations. Only an exact match whose
// scope covers the signature explains it: a finding topic or signature:
// evidence ref names the id, and its repository: refs name every repository
// the signature was seen in (naming none covers a signature seen in at most
// one). Everything else that matches is related: an exact match with a
// narrower scope, or a fuzzy one, whose text names the signature's test as a
// whole word (TestAlpha never matches TestAlphaBeta) or holds the exact
// normalized signature when it is long enough not to match any prose.
func explain(s Signature, seen map[string]bool, exps []explainer) (explained, related []Explanation) {
	var word *regexp.Regexp
	token, _ := strings.CutPrefix(s.Signature, "fail:")
	if testTokenRE.MatchString(token) { // identifier characters only: regex-safe
		word = regexp.MustCompile(`(^|[^A-Za-z0-9_])` + token + `($|[^A-Za-z0-9_])`)
	}
	bySignature := len([]rune(s.Signature)) >= minFuzzySignatureRunes
	explained, related = []Explanation{}, []Explanation{}
	for _, e := range exps {
		if e.exact[s.ID] {
			if scope := uncoveredScope(e.repos, seen); scope == "" {
				explained = append(explained, Explanation{Ref: e.ref, Match: MatchExact})
			} else {
				related = append(related, Explanation{Ref: e.ref, Match: MatchExact, Scope: scope})
			}
		} else if (word != nil && strings.Contains(e.text, token) && word.MatchString(e.text)) ||
			(bySignature && strings.Contains(e.text, s.Signature)) {
			related = append(related, Explanation{Ref: e.ref, Match: MatchFuzzy})
		}
	}
	return firstExplanations(explained), firstExplanations(related)
}

// uncoveredScope is "" when the named repositories cover every one seen.
func uncoveredScope(named, seen map[string]bool) string {
	if len(named) == 0 {
		if len(seen) > 1 {
			return ScopeUnscoped
		}
		return ""
	}
	for r := range seen {
		if !named[r] {
			return ScopeUncovered
		}
	}
	return ""
}

func firstExplanations(out []Explanation) []Explanation {
	slices.SortFunc(out, func(a, b Explanation) int { // "exact" sorts before "fuzzy"
		return cmp.Or(strings.Compare(a.Match, b.Match), strings.Compare(a.Ref, b.Ref))
	})
	return out[:min(len(out), 10)]
}

// applyProgress diffs against the previously persisted projection. Without one
// there is no derivative yet: progress stays empty and a warning says so.
func applyProgress(homePath string, proj *Projection, w *warnings) {
	prev, ok, err := ReadProjection(homePath)
	if err != nil {
		w.add("projection", "invalid_previous", "previous projection is not decodable; progress starts at this build")
		return
	} else if !ok {
		w.add("projection", "no_previous", "no previous projection; progress starts at this build")
		return
	} else if prev.SchemaVersion != SchemaVersion {
		w.add("projection", "schema_changed", "previous projection uses "+prev.SchemaVersion+"; its states do not compare, progress starts at this build")
		return
	}
	before := map[string]string{}
	for _, s := range prev.Signatures {
		before[s.ID] = s.State
	}
	proj.Progress.PreviousComputedAt = prev.ComputedAt
	proj.Progress.AssimilatedShareBefore = prev.Totals.AssimilatedShare
	for _, s := range proj.Signatures { // attention-rank order
		was, existed := before[s.ID]
		if s.State == StateAssimilated && (was == StateLearnable || was == StateSingleton) {
			proj.Progress.NewlyAssimilated = append(proj.Progress.NewlyAssimilated, Absorbed{ID: s.ID, Occurrences: s.Occurrences})
		}
		if s.State == StateLearnable && (!existed || was == StateSingleton) {
			proj.Progress.NewlyLearnable = append(proj.Progress.NewlyLearnable, s.ID)
		}
	}
	slices.Sort(proj.Progress.NewlyLearnable)
}

// WriteProjection persists atomically (temp + rename) under projections/ and
// returns the absolute path written. Derived state; never the ledger.
func WriteProjection(homePath string, proj Projection) (string, error) {
	path := filepath.Join(homePath, filepath.FromSlash(RelPath))
	b, err := json.MarshalIndent(proj, "", "  ")
	if err = errors.Join(err, os.MkdirAll(filepath.Dir(path), 0o755)); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".interest-*.json.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(append(b, '\n'))
	if err = errors.Join(err, tmp.Chmod(0o644), tmp.Close()); err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	return path, err
}

// ReadProjection loads the persisted projection; false (no error) when none exists.
func ReadProjection(homePath string) (Projection, bool, error) {
	var proj Projection
	data, err := os.ReadFile(filepath.Join(homePath, filepath.FromSlash(RelPath)))
	if os.IsNotExist(err) {
		return proj, false, nil
	} else if err != nil {
		return proj, false, err
	}
	if err := json.Unmarshal(data, &proj); err != nil {
		return Projection{}, false, err
	}
	return proj, true, nil
}
