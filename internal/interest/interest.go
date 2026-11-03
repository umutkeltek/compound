// Package interest is the read-only, rebuildable interestingness projection over
// failed proof results (Schmidhuber, "Driven by Compression Progress"). Each failed
// work.proof_resource.result is reduced to one public-safe signature from its
// proof report; signatures cluster and each cluster is assimilated (a finding
// declares an explanation, names its signature exactly and covers every observed
// repository), learnable
// (it recurs unexplained across work ids or days) or singleton. A note that only
// mentions the failure is related evidence, not an explanation, and an
// explanation is not a verified repair. A finding that names a signature
// exactly but fails the acceptance rules is reported as an orphaned
// explanation with its reason, never dropped silently, so a tightened rule
// shows what it un-explains. Attention goes to learnable regularity,
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
	SchemaVersion = "compound.interest.v0.4"
	ProjectionRef = "compound://projections/interest"
	RelPath       = "projections/interest.json"
	DefaultWindow = 30 * 24 * time.Hour

	// ExplanationDeclarationRef is an author declaration that a finding supplies
	// a diagnosis, never independent acceptance or proof of repair.
	ExplanationDeclarationRef = "explanation:declared"
	StatusDeclared            = "declared"
	StatusUnknown             = "unknown"
	StatusSuperseded          = "superseded"

	StateAssimilated = "assimilated"
	StateLearnable   = "learnable"
	StateSingleton   = "singleton"
	MatchExact       = "exact"
	MatchFuzzy       = "fuzzy"
	ScopeUnscoped    = "unscoped"  // explanation or an observation lacks repository scope
	ScopeUncovered   = "uncovered" // names repositories that miss one the signature was seen in

	// Orphan reasons beside ScopeUnscoped and ScopeUncovered.
	OrphanUnknownSignature   = "unknown_signature"   // names a signature id this window never observed
	OrphanMissingDeclaration = "missing_declaration" // no explanation:declared ref
	OrphanLifecycleUnknown   = "lifecycle_unknown"   // declared, but the finding lifecycle is unreadable

	// SignatureEvidencePrefix ties a finding to one signature exactly:
	// evidence_refs contains "signature:<psig id>". RepositoryEvidencePrefix
	// scopes it: "repository:<fingerprint>", one ref per repository it covers.
	SignatureEvidencePrefix  = "signature:"
	RepositoryEvidencePrefix = "repository:"
	signatureIDPrefix        = "psig_" // SignatureID's prefix; a topic shaped so names a signature
	// RepositoryWildcard after RepositoryEvidencePrefix ("repository:*") is an
	// explicit claim that the cause is repository-independent (a machine, tool
	// or environment cause such as a redact false positive or a load timeout),
	// so the explanation covers every repository the signature was seen in,
	// including ones the projection's capped Repositories list does not show.
	// It is a stated scope, not the absence of one. Missing explanation or
	// observation scope still reads as ScopeUnscoped.
	RepositoryWildcard = "*"

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
	// OrphanedExplanations: exact associations the acceptance rules refuse.
	OrphanedExplanations Orphans   `json:"orphaned_explanations"`
	Warnings             []Warning `json:"warnings"`
}

// Orphans lists every current finding that names a signature exactly (topic
// or signature: ref) but does not explain it, one item per finding and
// signature. A tightened acceptance rule must surface what it un-explains,
// never drop it silently: an orphan is learning that stopped counting.
// ByReason counts each item once, by its Reason; Count = len(Items).
// Superseded findings are retractions, not orphans; a fuzzy mention never was
// an explanation.
type Orphans struct {
	Count    int            `json:"count"`
	ByReason map[string]int `json:"by_reason"`
	Items    []Orphan       `json:"items"`
}

// Orphan: Reason is the first acceptance rule the association fails, in
// acceptance order (declaration, scope, lifecycle); AlsoFails names the rest,
// so fixing only the first is not mistaken for a full repair.
type Orphan struct {
	FindingID   string   `json:"finding_id"`
	SignatureID string   `json:"signature_id"`
	Reason      string   `json:"reason"`
	AlsoFails   []string `json:"also_fails,omitempty"`
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
	ID           string   `json:"id"`
	Step         string   `json:"step"`
	Signature    string   `json:"signature"`
	Tests        []string `json:"tests"`
	Occurrences  int      `json:"occurrences"`
	FirstSeen    string   `json:"first_seen"`
	LastSeen     string   `json:"last_seen"`
	WorkIDs      []string `json:"work_ids"`
	Repositories []string `json:"repositories"`
	// RepositoriesTotal is the uncapped count behind the Repositories sample, so a
	// reader can tell when the sample is partial and an explanation must claim
	// repository:* rather than enumerate.
	RepositoriesTotal int           `json:"repositories_total"`
	ResourceShape     bool          `json:"resource_shape"`
	State             string        `json:"state"`
	ExplainedBy       []Explanation `json:"explained_by"`
	Related           []Explanation `json:"related"`
	AttentionRank     int           `json:"attention_rank"`
}

type Explanation struct {
	Ref   string `json:"ref"`
	Match string `json:"match"`
	Scope string `json:"scope,omitempty"`
	// Status describes an author declaration and the existing finding lifecycle.
	Status string `json:"status"`
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
	unknownRepository         bool
}

type explainer struct {
	ref          string
	exact, repos map[string]bool
	text         string
	status       string
	findingID    string   // "" for a negatives record
	declared     bool     // carries ExplanationDeclarationRef, whatever the lifecycle says
	signatures   []string // signature ids it names exactly, sorted
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
	proj.Signatures, proj.OrphanedExplanations = classify(obs, readExplanations(homePath, now, &w))
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
	lifecycle, lifecycleErr := finding.Doctor(homePath, now, 0)
	if lifecycleErr != nil {
		w.add("finding", "lifecycle_unreadable", "current finding lifecycle is unreadable; explanation status stays unknown")
	}
	superseded := map[string]bool{}
	for _, f := range lifecycle.Superseded {
		superseded[f.ID] = true
	}
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
		topic := strings.TrimSpace(f.Topic)
		exact, repos, signatures := map[string]bool{topic: true}, map[string]bool{}, map[string]bool{}
		if strings.HasPrefix(topic, signatureIDPrefix) {
			signatures[topic] = true
		}
		status, declared := StatusUnknown, false
		for _, ref := range f.EvidenceRefs {
			ref = strings.TrimSpace(ref)
			if ref == ExplanationDeclarationRef {
				status, declared = StatusDeclared, true
			} else if id, ok := strings.CutPrefix(ref, SignatureEvidencePrefix); ok {
				exact[id] = true
				add(signatures, id) // a malformed id stays visible as unknown_signature
			} else if fp, ok := strings.CutPrefix(ref, RepositoryEvidencePrefix); ok {
				add(repos, fp)
			}
		}
		if lifecycleErr != nil {
			status = StatusUnknown
		} else if superseded[f.ID] {
			status = StatusSuperseded
		}
		out = append(out, explainer{ref: "finding:" + f.ID, exact: exact, repos: repos, text: f.Topic + "\n" + f.Body, status: status,
			findingID: f.ID, declared: declared, signatures: sorted(signatures, len(signatures))})
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
// It also returns the exact associations the acceptance rules refuse.
func classify(obs []observation, exps []explainer) ([]Signature, Orphans) {
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
		c.unknownRepository = c.unknownRepository || strings.TrimSpace(o.repo) == ""
		for _, t := range o.tests {
			add(c.tests, t)
		}
	}
	out := make([]Signature, 0, len(clusters))
	for _, c := range clusters {
		s := c.sig
		s.WorkIDs, s.Repositories, s.Tests = sorted(c.works, 10), sorted(c.repos, 5), sorted(c.tests, 10)
		s.RepositoriesTotal = len(c.repos)
		s.ExplainedBy, s.Related = explain(s, c.repos, c.unknownRepository, exps)
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
	return out, orphans(clusters, exps)
}

// orphans reports each current finding's exact association that does not
// explain its signature, with the rules it fails (see Orphans).
func orphans(clusters map[string]*cluster, exps []explainer) Orphans {
	out := Orphans{ByReason: map[string]int{}, Items: []Orphan{}}
	for _, e := range exps {
		if e.findingID == "" || e.status == StatusSuperseded {
			continue
		}
		for _, id := range e.signatures {
			failed := []string{OrphanUnknownSignature}
			if c := clusters[id]; c != nil {
				_, failed = acceptance(e, c.repos, c.unknownRepository)
			}
			if len(failed) == 0 {
				continue
			}
			o := Orphan{FindingID: e.findingID, SignatureID: id, Reason: failed[0]}
			if len(failed) > 1 {
				o.AlsoFails = failed[1:]
			}
			out.Items = append(out.Items, o)
			out.ByReason[o.Reason]++
		}
	}
	slices.SortFunc(out.Items, func(a, b Orphan) int {
		return cmp.Or(strings.Compare(a.FindingID, b.FindingID), strings.Compare(a.SignatureID, b.SignatureID))
	})
	out.Count = len(out.Items)
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
// scope covers the signature explains it, with a current explanation:declared
// author declaration: a finding topic or signature:
// evidence ref names the id, and its repository: refs name every repository
// the signature was seen in, with every observation scoped. Missing scope
// stays uncertain even for one repository. Other matches are related: an exact match with a
// narrower scope, or a fuzzy one, whose text names the signature's test as a
// whole word (TestAlpha never matches TestAlphaBeta) or holds the exact
// normalized signature when it is long enough not to match any prose.
func explain(s Signature, seen map[string]bool, unknownRepository bool, exps []explainer) (explained, related []Explanation) {
	var word *regexp.Regexp
	token, _ := strings.CutPrefix(s.Signature, "fail:")
	if testTokenRE.MatchString(token) {
		word = regexp.MustCompile(`(^|[^A-Za-z0-9_])` + token + `($|[^A-Za-z0-9_])`)
	}
	bySignature := len([]rune(s.Signature)) >= minFuzzySignatureRunes
	explained, related = []Explanation{}, []Explanation{}
	for _, e := range exps {
		status := e.status
		if status == "" {
			status = StatusUnknown
		}
		if e.exact[s.ID] {
			scope, failed := acceptance(e, seen, unknownRepository)
			match := Explanation{Ref: e.ref, Match: MatchExact, Scope: scope, Status: status}
			if len(failed) == 0 {
				explained = append(explained, match)
			} else {
				related = append(related, match)
			}
		} else if (word != nil && strings.Contains(e.text, token) && word.MatchString(e.text)) ||
			(bySignature && strings.Contains(e.text, s.Signature)) {
			related = append(related, Explanation{Ref: e.ref, Match: MatchFuzzy, Status: status})
		}
	}
	return firstExplanations(explained), firstExplanations(related)
}

// acceptance is the one rule an exact association must pass to explain a
// signature: a current explanation:declared declaration and a scope covering
// every repository seen. It returns that scope and the rules failed in
// acceptance order (declaration, scope, lifecycle); none failed explains it.
// Superseded comes last and only explain sees it: orphans skips retractions.
func acceptance(e explainer, seen map[string]bool, unknownRepository bool) (string, []string) {
	scope := uncoveredScope(e.repos, seen, unknownRepository)
	var failed []string
	if !e.declared {
		failed = append(failed, OrphanMissingDeclaration)
	}
	if scope != "" {
		failed = append(failed, scope)
	}
	if e.declared && e.status != StatusDeclared && e.status != StatusSuperseded {
		failed = append(failed, OrphanLifecycleUnknown)
	}
	if e.status == StatusSuperseded {
		failed = append(failed, StatusSuperseded)
	}
	return scope, failed
}

// uncoveredScope is "" when the named repositories cover every one seen; a
// repository:* claim covers all of them by declaration.
func uncoveredScope(named, seen map[string]bool, unknownRepository bool) string {
	if unknownRepository || len(seen) == 0 || len(named) == 0 {
		return ScopeUnscoped
	}
	if named[RepositoryWildcard] {
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
		if s.State == StateLearnable && (!existed || was != StateLearnable) {
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
