package work

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"compound/internal/envelope"
	"compound/internal/home"
)

// BenchCompareProofSchema identifies one recorded bench comparison.
const BenchCompareProofSchema = "compound.bench_compare_proof.v0.1"

// errBenchCompareUnreadable marks a record that exists but could not be read. It
// says nothing about the comparison, so it must never settle a close's verdict.
var errBenchCompareUnreadable = errors.New("read recorded comparison")

const (
	benchCompareAcceptRefFamily = "bench_compare_accept:"
	benchCompareAcceptRefPrefix = benchCompareAcceptRefFamily + "sha256:"
	benchCompareProofKind       = "bench compare proof"
	minBenchBuildSHALength      = 7
)

// BenchCompareProof is the immutable evidence a harness-edit close cites: one
// comparison verdict, bound to the evaluation it measured, the work it judged
// and the build it compared. The bench evaluator decides the verdict; this
// record only carries that verdict to the close that consumes it.
type BenchCompareProof struct {
	Schema             string `json:"schema"`
	WorkID             string `json:"work_id"`
	TargetSurface      string `json:"target_surface"`
	Suite              string `json:"suite"`
	InputDigest        string `json:"input_digest"`
	BeforeReportSHA256 string `json:"before_report_sha256"`
	AfterReportSHA256  string `json:"after_report_sha256"`
	BeforeBuildSHA     string `json:"before_build_sha"`
	AfterBuildSHA      string `json:"after_build_sha"`
	BeforeGraphHash    string `json:"before_graph_hash"`
	AfterGraphHash     string `json:"after_graph_hash"`
	Accept             bool   `json:"accept"`
}

// BenchCompareProofPath is the content-addressed path of one comparison record.
// The name is the SHA-256 of the recorded bytes, so different evidence can never
// replace it. It lives with the work store, outside reviews/: the review queue
// asks the operator to decide on what it finds there, and archives what is
// resolved. This record asks nothing of anyone, and a close must still find it.
func BenchCompareProofPath(homePath, digest string) string {
	return filepath.Join(homePath, "work", "bench-compare", "sha256-"+digest+".json")
}

// RecordBenchCompareInHome records one comparison against an existing work. It
// opens the store read-only, so a comparison of an unknown work creates nothing.
func RecordBenchCompareInHome(homePath, workID string, proof BenchCompareProof) (string, error) {
	store, err := OpenReadOnly(homePath)
	if err != nil {
		return "", err
	}
	_, proofRef, err := store.RecordBenchCompare(workID, proof)
	return proofRef, err
}

// RecordBenchCompare binds one finished comparison to a nonterminal harness-edit
// work and records it, whatever its verdict. An accepted comparison that a close
// could never honor is refused here, while the closer can still measure again.
// The returned proof ref is empty unless the comparison accepted, so a rejected
// comparison has nothing to cite.
func (s *Store) RecordBenchCompare(workID string, proof BenchCompareProof) (path, proofRef string, err error) {
	w, err := s.Get(workID)
	if err != nil {
		return "", "", err
	}
	if terminalStates[w.State] {
		return "", "", fmt.Errorf("work %s is already %s; a comparison cannot prove a closed edit", workID, w.State)
	}
	if strings.TrimSpace(w.TargetSurface) == "" {
		return "", "", fmt.Errorf("work %s declares no target surface; a comparison proves only a harness edit", workID)
	}
	proof.Schema = BenchCompareProofSchema
	proof.WorkID = w.ID
	proof.TargetSurface = w.TargetSurface
	if err := proof.validate(); err != nil {
		return "", "", err
	}
	if proof.Accept {
		if err := proof.bindingError(w); err != nil {
			return "", "", err
		}
	}
	body, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal bench compare proof: %w", err)
	}
	body = append(body, '\n')
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	path = BenchCompareProofPath(s.home, digest)
	if err := home.WriteRootedImmutableFileDurable(s.home, path, benchCompareProofKind, body, 0o644); err != nil {
		return "", "", fmt.Errorf("write bench compare proof: %w", err)
	}
	if proof.Accept {
		proofRef = benchCompareAcceptRefPrefix + digest
	}
	return path, proofRef, nil
}

func (p BenchCompareProof) validate() error {
	if p.Schema != BenchCompareProofSchema {
		return fmt.Errorf("schema %q is not %s", p.Schema, BenchCompareProofSchema)
	}
	for _, field := range []struct{ name, value string }{
		{"work_id", p.WorkID},
		{"target_surface", p.TargetSurface},
		{"suite", p.Suite},
		{"input_digest", p.InputDigest},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if !validBenchCompareDigest(p.BeforeReportSHA256) || !validBenchCompareDigest(p.AfterReportSHA256) {
		return errors.New("before and after report digests must be lowercase SHA-256")
	}
	return nil
}

func validBenchCompareDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// resolveBenchCompareAccept returns nil only when ref names a stored comparison
// that accepted and is bound to this work, this surface and its current candidate.
func (s *Store) resolveBenchCompareAccept(ref string, w envelope.WorkEnvelope) error {
	digest, ok := strings.CutPrefix(strings.TrimSpace(ref), benchCompareAcceptRefPrefix)
	if !ok || !validBenchCompareDigest(digest) {
		return fmt.Errorf("not a recorded comparison; expected %s<digest> from bench compare --work", benchCompareAcceptRefPrefix)
	}
	data, err := home.ReadRootedRegularFile(s.home, BenchCompareProofPath(s.home, digest), benchCompareProofKind)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("no recorded comparison has this digest")
		}
		return fmt.Errorf("%w: %v", errBenchCompareUnreadable, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return errors.New("recorded comparison bytes do not match the digest")
	}
	var proof BenchCompareProof
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proof); err != nil {
		return fmt.Errorf("parse recorded comparison: %w", err)
	}
	if err := proof.validate(); err != nil {
		return fmt.Errorf("recorded comparison is incomplete: %w", err)
	}
	if !proof.Accept {
		return errors.New("the recorded comparison rejected this edit")
	}
	return proof.bindingError(w)
}

// bindingError refuses an accepted comparison that does not prove this edit: one
// of another work or surface, one that measured no edit, or, for source-delivered
// work, one whose two sides are not the delivery base and the current candidate.
// Work with no source delivery has no base to bind, so a closer there still
// chooses the baseline it compares against.
func (p BenchCompareProof) bindingError(w envelope.WorkEnvelope) error {
	switch {
	case p.WorkID != w.ID:
		return fmt.Errorf("the recorded comparison judged work %s", p.WorkID)
	case p.TargetSurface != w.TargetSurface:
		return fmt.Errorf("the recorded comparison judged surface %s", p.TargetSurface)
	case p.BeforeReportSHA256 == p.AfterReportSHA256,
		sameBenchBuild(p.BeforeBuildSHA, p.AfterBuildSHA) && p.BeforeGraphHash == p.AfterGraphHash:
		return errors.New("the comparison measured no edit: before and after are the same build and graph")
	}
	if w.GitDelivery != nil && !benchBuildIdentifies(p.BeforeBuildSHA, w.GitDelivery.ExpectedBaseSHA) {
		return fmt.Errorf("the before report must come from delivery base %s, but it measured build %q", w.GitDelivery.ExpectedBaseSHA, p.BeforeBuildSHA)
	}
	ref, sha, found, valid := latestCommitArtifact(w.ArtifactIDs)
	switch {
	case !found && w.GitDelivery != nil:
		return errors.New("this source-delivered work has no candidate commit to compare; attach commit:<sha> first")
	case !found:
		return nil
	case !valid:
		return fmt.Errorf("latest candidate artifact %q is malformed", ref)
	case !benchBuildIdentifies(p.AfterBuildSHA, sha):
		return fmt.Errorf("stale comparison: it measured build %q, but the candidate is %s", p.AfterBuildSHA, sha)
	}
	return nil
}

// benchBuildIdentifies reports whether a build names this commit. Installed builds
// embed a short commit, so a prefix of a usable length identifies it.
func benchBuildIdentifies(build, sha string) bool {
	return len(build) >= minBenchBuildSHALength && strings.HasPrefix(sha, build)
}

// sameBenchBuild reports whether two build labels name one build. Installed builds
// embed a short commit and release builds a full one, so spelling cannot decide it.
func sameBenchBuild(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	return a == b || benchBuildIdentifies(a, b)
}

// hardenOnClose reports whether a close should stamp SecondOrder.ShouldHarden.
// It is true only on an accepted (outcome "completed") close of an explicit
// harness-edit envelope, one that declares a TargetSurface, when every comparison
// on the close resolves to a stored accepted record for this work. A completed close
// cannot certify itself, and a label cannot stand in for the comparison. Any other
// close of the same envelope stamps false but still emits its Receipt and Transition,
// so rejections are logged without changing the active harness.
//
// It judges the receipt's whole set of refs, so every close door reaches the same
// answer, and an accepted comparison cannot outvote a bad one attached to the work.
//
// A close that cites a comparison asks to harden, so a failure refuses it before
// anything is recorded: a terminal Transition cannot be corrected. A close that cites
// none completes unhardened past a bad attached ref, which the receipt still lists.
// That is the closer's way forward when the attached ref cannot be repaired. A record
// that could not be read refuses either way, because that says nothing about the edit.
func (s *Store) hardenOnClose(w envelope.WorkEnvelope, outcome string, cited, receiptRefs []string) (bool, error) {
	if strings.TrimSpace(w.TargetSurface) == "" || outcome != "completed" {
		return false, nil
	}
	askedToHarden := slices.ContainsFunc(cited, isBenchCompareAcceptRef)
	harden, unhardened := false, false
	for _, ref := range receiptRefs {
		if !isBenchCompareAcceptRef(ref) {
			continue
		}
		err := s.resolveBenchCompareAccept(ref, w)
		switch {
		case err == nil:
			harden = true
		case askedToHarden || errors.Is(err, errBenchCompareUnreadable):
			return false, fmt.Errorf("cited comparison %s: %w", ref, err)
		default:
			unhardened = true
		}
	}
	return harden && !unhardened, nil
}

func isBenchCompareAcceptRef(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), benchCompareAcceptRefFamily)
}
