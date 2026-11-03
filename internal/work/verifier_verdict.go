package work

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
)

// The post-land verifier ("verifier") verdict is the canonical proof that closes
// source-controlled Compound work (operator decision 2026-11-03, D7; skills/compound-land §1).
//
// Rule. Let T be the landed candidate tip of a work and B the bound target branch.
//
//   - Accept: a GREEN full-suite verdict at commit S, with T an ancestor of or equal to S and S an
//     ancestor of or equal to B. The verifier ran the whole suite on the target branch INCLUDING the
//     change, on an otherwise idle host. That is the property a completed close claims ("the change
//     is on main and main is green with it"). A laptop battery at T proves less: T alone, before the
//     land, on a host that six lanes load at once.
//   - Refuse (culprit): the open red record names a culprit range (lo, hi] that contains T, and no
//     green verdict covering T finished after that red was named. A later covering green means main
//     with T is green again (the flake retried green, or a fix landed), so the culprit no longer
//     describes main.
//   - Refuse (not verified): no green verdict covers T yet. The message is "verifier has not verified
//     this tip yet". There is no fallback to the laptop battery: that fallback is the 45-minute
//     queue this rule retires. Governance-core diffs keep the full pre-land battery through the
//     existing proof-check path; this rule does not weaken it.
//
// Evidence lives in $COMPOUND_HOME/state/verify (verdicts.tsv + red.json). The private verify loop
// mirrors its files there; tracked code never names the loop's own directory.
const (
	VerifierVerdictVerificationPrefix = "verifier-verdict:"
	VerifierVerdictsFileName          = "verdicts.tsv"
	VerifierRedFileName               = "red.json"

	VerifierVerdictCovered     = "covered"
	VerifierVerdictRedCulprit  = "red_culprit"
	VerifierVerdictNotVerified = "not_verified"

	verifierVerdictKindMain = "main"
)

var (
	verifierRunIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$`)
	verifierSHAPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// VerifierVerdictRow is one line of verdicts.tsv:
// finished_at sha run_id verdict kind mode duration_s [failed_packages].
type VerifierVerdictRow struct {
	FinishedAt     time.Time `json:"finished_at"`
	SHA            string    `json:"sha"`
	RunID          string    `json:"run_id"`
	Verdict        string    `json:"verdict"`
	Kind           string    `json:"kind"`
	Mode           string    `json:"mode"`
	FailedPackages []string  `json:"failed_packages,omitempty"`
}

// green reports a full-suite green of the target branch: a main run green, or a
// main run whose only failures passed on the packages retry (green_after_retry).
func (r VerifierVerdictRow) green() bool {
	return r.Kind == verifierVerdictKindMain && (r.Verdict == "green" || r.Verdict == "green_after_retry") && verifierSHAPattern.MatchString(r.SHA)
}

// VerifierCulpritRange is one still-failing package and the narrowest first-parent
// range (lo, hi] the recorded full runs prove. An empty Lo reaches the root.
type VerifierCulpritRange struct {
	Package     string `json:"package"`
	LoExclusive string `json:"lo_exclusive,omitempty"`
	HiInclusive string `json:"hi_inclusive"`
}

// VerifierRed is the verifier's open red record (red.json).
type VerifierRed struct {
	SHA    string            `json:"sha"`
	Since  time.Time         `json:"since"`
	Ranges []VerifierCulpritRange `json:"ranges"`
}

// VerifierVerdictEvidence is everything the rule reads.
type VerifierVerdictEvidence struct {
	Dir  string
	Rows []VerifierVerdictRow
	Red  *VerifierRed
}

// VerifierVerdictDecision is the rule's answer for one tip.
type VerifierVerdictDecision struct {
	Status      string           `json:"status"`
	Tip         string           `json:"tip"`
	TargetSHA   string           `json:"target_sha"`
	Ref         string           `json:"ref,omitempty"`
	RunID       string           `json:"run_id,omitempty"`
	VerifiedSHA string           `json:"verified_sha,omitempty"`
	FinishedAt  string           `json:"finished_at,omitempty"`
	Culprit     *VerifierCulpritRange `json:"culprit,omitempty"`
	RedSince    string           `json:"red_since,omitempty"`
	Reason      string           `json:"reason"`
	VerdictsDir string           `json:"verdicts_dir"`
}

// VerifierAncestry answers "a is an ancestor of, or equal to, b" in the bound repository.
type VerifierAncestry func(a, b string) (bool, error)

// VerifierVerdictRef is the verification ref a covering green verdict attaches to a work.
func VerifierVerdictRef(runID, sha string) string {
	return VerifierVerdictVerificationPrefix + runID + ":" + sha
}

// ParseVerifierVerdictRef splits verifier-verdict:<run_id>:<sha>.
func ParseVerifierVerdictRef(ref string) (runID, sha string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(ref), VerifierVerdictVerificationPrefix)
	if !found {
		return "", "", false
	}
	runID, sha, found = strings.Cut(rest, ":")
	if !found || !verifierRunIDPattern.MatchString(runID) || !verifierSHAPattern.MatchString(sha) {
		return "", "", false
	}
	return runID, sha, true
}

// VerifierVerdictsDir is where Compound reads verifier evidence for a home.
func VerifierVerdictsDir(homePath string) string {
	return filepath.Join(homePath, "state", "verify")
}

// LoadVerifierVerdictEvidence reads verdicts.tsv and red.json from dir. A missing
// directory or file is empty evidence (the rule then refuses as not verified);
// a malformed file is an error, never a silently shorter history.
func LoadVerifierVerdictEvidence(dir string) (VerifierVerdictEvidence, error) {
	ev := VerifierVerdictEvidence{Dir: dir}
	f, err := os.Open(filepath.Join(dir, VerifierVerdictsFileName))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return ev, err
	default:
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for line := 1; scanner.Scan(); line++ {
			text := scanner.Text()
			if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "#") {
				continue
			}
			row, err := parseVerifierVerdictRow(text)
			if err != nil {
				return ev, fmt.Errorf("%s line %d: %w", VerifierVerdictsFileName, line, err)
			}
			ev.Rows = append(ev.Rows, row)
		}
		if err := scanner.Err(); err != nil {
			return ev, err
		}
	}
	body, err := os.ReadFile(filepath.Join(dir, VerifierRedFileName))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return ev, err
	default:
		red, err := parseVerifierRed(body)
		if err != nil {
			return ev, fmt.Errorf("%s: %w", VerifierRedFileName, err)
		}
		ev.Red = red
	}
	return ev, nil
}

func parseVerifierVerdictRow(text string) (VerifierVerdictRow, error) {
	fields := strings.Split(text, "\t")
	if len(fields) < 7 {
		return VerifierVerdictRow{}, fmt.Errorf("want at least 7 tab-separated fields, got %d", len(fields))
	}
	finished, err := time.Parse(time.RFC3339, fields[0])
	if err != nil {
		return VerifierVerdictRow{}, fmt.Errorf("finished_at: %w", err)
	}
	row := VerifierVerdictRow{FinishedAt: finished.UTC(), SHA: fields[1], RunID: fields[2], Verdict: fields[3], Kind: fields[4], Mode: fields[5]}
	if len(fields) > 7 {
		for _, pkg := range strings.Split(fields[7], ",") {
			if pkg = strings.TrimSpace(pkg); pkg != "" && pkg != "-" {
				row.FailedPackages = append(row.FailedPackages, pkg)
			}
		}
	}
	return row, nil
}

func parseVerifierRed(body []byte) (*VerifierRed, error) {
	var raw struct {
		SHA      string `json:"sha"`
		Since    string `json:"since"`
		Packages map[string]struct {
			Lo *string `json:"lo_exclusive"`
			Hi string  `json:"hi_inclusive"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	since, err := time.Parse(time.RFC3339, raw.Since)
	if err != nil {
		return nil, fmt.Errorf("since: %w", err)
	}
	red := &VerifierRed{SHA: raw.SHA, Since: since.UTC()}
	for pkg, r := range raw.Packages {
		lo := ""
		if r.Lo != nil {
			lo = *r.Lo
		}
		if !verifierSHAPattern.MatchString(r.Hi) || (lo != "" && !verifierSHAPattern.MatchString(lo)) {
			return nil, fmt.Errorf("package %s has a malformed culprit range (%q..%q]", pkg, lo, r.Hi)
		}
		red.Ranges = append(red.Ranges, VerifierCulpritRange{Package: pkg, LoExclusive: lo, HiInclusive: r.Hi})
	}
	sort.Slice(red.Ranges, func(i, j int) bool { return red.Ranges[i].Package < red.Ranges[j].Package })
	return red, nil
}

// DecideVerifierVerdict applies the rule (see the package comment above) to tip T
// and the current target-branch commit.
func DecideVerifierVerdict(ev VerifierVerdictEvidence, tip, targetSHA string, anc VerifierAncestry) (VerifierVerdictDecision, error) {
	d := VerifierVerdictDecision{Tip: tip, TargetSHA: targetSHA, VerdictsDir: ev.Dir}
	if !verifierSHAPattern.MatchString(tip) || !verifierSHAPattern.MatchString(targetSHA) {
		return d, fmt.Errorf("verifier verdict needs full 40-hex tip and target commits, got tip %q target %q", tip, targetSHA)
	}
	var newest *VerifierVerdictRow
	for i := range ev.Rows {
		row := ev.Rows[i]
		if !row.green() {
			continue
		}
		covers, err := verifierCovers(anc, tip, row.SHA, targetSHA)
		if err != nil {
			return d, err
		}
		if covers && (newest == nil || !row.FinishedAt.Before(newest.FinishedAt)) {
			newest = &ev.Rows[i]
		}
	}
	if ev.Red != nil {
		for _, r := range ev.Red.Ranges {
			inRange, err := verifierCulpritContains(anc, r, tip)
			if err != nil {
				return d, err
			}
			if !inRange || (newest != nil && newest.FinishedAt.After(ev.Red.Since)) {
				continue
			}
			culprit := r
			d.Status, d.Culprit, d.RedSince = VerifierVerdictRedCulprit, &culprit, ev.Red.Since.Format(time.RFC3339)
			d.Reason = fmt.Sprintf("verifier names tip %s in the red culprit range of %s (%s..%s], open since %s, and no later green verdict covers it; fix or revert, then wait for a green verifier verdict",
				verifierShort(tip), r.Package, verifierRangeEnd(r.LoExclusive), verifierShort(r.HiInclusive), d.RedSince)
			return d, nil
		}
	}
	if newest == nil {
		d.Status = VerifierVerdictNotVerified
		d.Reason = fmt.Sprintf("verifier has not verified this tip yet: no green full-suite verdict in %s covers %s on the target branch; wait for the post-land verifier (the laptop battery is not a fallback)", filepath.Join(ev.Dir, VerifierVerdictsFileName), verifierShort(tip))
		return d, nil
	}
	d.Status = VerifierVerdictCovered
	d.RunID, d.VerifiedSHA = newest.RunID, newest.SHA
	d.FinishedAt = newest.FinishedAt.Format(time.RFC3339)
	d.Ref = VerifierVerdictRef(newest.RunID, newest.SHA)
	d.Reason = fmt.Sprintf("verifier run %s verified %s green at %s, which contains tip %s", newest.RunID, verifierShort(newest.SHA), d.FinishedAt, verifierShort(tip))
	return d, nil
}

// ValidateVerifierVerdictRef proves a cited ref is acceptable for tip T now: the rule
// must answer covered, and the cited run must itself be a green verdict covering T.
func ValidateVerifierVerdictRef(ev VerifierVerdictEvidence, ref, tip, targetSHA string, anc VerifierAncestry) (VerifierVerdictDecision, error) {
	runID, sha, ok := ParseVerifierVerdictRef(ref)
	if !ok {
		return VerifierVerdictDecision{}, fmt.Errorf("verifier verdict ref %q is not verifier-verdict:<run_id>:<40-hex sha>", ref)
	}
	d, err := DecideVerifierVerdict(ev, tip, targetSHA, anc)
	if err != nil {
		return d, err
	}
	if d.Status != VerifierVerdictCovered {
		return d, errors.New(d.Reason)
	}
	for _, row := range ev.Rows {
		if row.RunID != runID || row.SHA != sha || !row.green() {
			continue
		}
		covers, err := verifierCovers(anc, tip, sha, targetSHA)
		if err != nil {
			return d, err
		}
		if covers {
			return d, nil
		}
	}
	return d, fmt.Errorf("verifier verdict ref %s names no green full-suite run in %s that covers tip %s on the target branch", ref, filepath.Join(ev.Dir, VerifierVerdictsFileName), verifierShort(tip))
}

func verifierCovers(anc VerifierAncestry, tip, verified, target string) (bool, error) {
	ok, err := anc(tip, verified)
	if err != nil || !ok {
		return false, err
	}
	return anc(verified, target)
}

// verifierCulpritContains is lo < T <= hi on history: T reaches hi and T does not reach lo.
func verifierCulpritContains(anc VerifierAncestry, r VerifierCulpritRange, tip string) (bool, error) {
	inHi, err := anc(tip, r.HiInclusive)
	if err != nil || !inHi {
		return false, err
	}
	if r.LoExclusive == "" {
		return true, nil
	}
	inLo, err := anc(tip, r.LoExclusive)
	return !inLo, err
}

func verifierRangeEnd(sha string) string {
	if sha == "" {
		return "root"
	}
	return verifierShort(sha)
}

func verifierShort(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// latestLandingVerificationRef is the newest verification ref that can certify
// a landing, of any kind. Newest wins, as in latestLandingProofResolution.
func latestLandingVerificationRef(w envelope.WorkEnvelope) string {
	for i := len(w.VerificationIDs) - 1; i >= 0; i-- {
		ref := strings.TrimSpace(w.VerificationIDs[i])
		for _, prefix := range []string{VerifierVerdictVerificationPrefix, ProofAdoptionVerificationPrefix, "proof-check:", DifferentialProofVerificationPrefix} {
			if strings.HasPrefix(ref, prefix) {
				return ref
			}
		}
	}
	return ""
}

// latestVerifierVerdictLandingRef returns the verifier ref when it is the newest landing proof.
func latestVerifierVerdictLandingRef(w envelope.WorkEnvelope) (string, bool) {
	ref := latestLandingVerificationRef(w)
	return ref, strings.HasPrefix(ref, VerifierVerdictVerificationPrefix)
}

// ResolveVerifierVerdict reads this home's verifier evidence and decides tip T
// against the work's bound target branch.
func (s *Store) ResolveVerifierVerdict(workID, tip, verdictsDir string) (VerifierVerdictDecision, error) {
	w, err := s.Get(strings.TrimSpace(workID))
	if err != nil {
		return VerifierVerdictDecision{}, err
	}
	repo, target, err := s.verifierVerdictTarget(w)
	if err != nil {
		return VerifierVerdictDecision{}, err
	}
	if strings.TrimSpace(verdictsDir) == "" {
		verdictsDir = VerifierVerdictsDir(s.home)
	}
	ev, err := LoadVerifierVerdictEvidence(verdictsDir)
	if err != nil {
		return VerifierVerdictDecision{}, err
	}
	return DecideVerifierVerdict(ev, strings.TrimSpace(tip), target, verifierGitAncestry(repo))
}

func (s *Store) verifierVerdictTarget(w envelope.WorkEnvelope) (repo, target string, err error) {
	if w.GitDelivery == nil {
		return "", "", fmt.Errorf("work %s has no git_delivery contract; an verifier verdict certifies a landed Git tip only", w.ID)
	}
	repo = w.GitDelivery.TargetWorktreeRoot
	target, err = runLandingGitOutput(repo, "rev-parse", "--verify", "--quiet", w.GitDelivery.TargetLocalRef+"^{commit}")
	if err != nil {
		return "", "", fmt.Errorf("resolve target %s: %w", w.GitDelivery.TargetLocalRef, err)
	}
	return repo, strings.TrimSpace(target), nil
}

// validateVerifierVerdictAttachment is the attach-time check: the ref is well formed
// and names a green full-suite run present in this home's verifier evidence.
// Containment and the red rule are decided at land-check and again at close.
func (s *Store) validateVerifierVerdictAttachment(ref string) error {
	runID, sha, ok := ParseVerifierVerdictRef(ref)
	if !ok {
		return fmt.Errorf("verifier verdict ref %q is not verifier-verdict:<run_id>:<40-hex sha>", ref)
	}
	ev, err := LoadVerifierVerdictEvidence(VerifierVerdictsDir(s.home))
	if err != nil {
		return err
	}
	for _, row := range ev.Rows {
		if row.RunID == runID && row.SHA == sha && row.green() {
			return nil
		}
	}
	return fmt.Errorf("verifier verdict ref %s names no green full-suite run in %s", ref, filepath.Join(ev.Dir, VerifierVerdictsFileName))
}

func verifierGitAncestry(repo string) VerifierAncestry {
	return func(a, b string) (bool, error) { return landingIsAncestor(repo, a, b) }
}
