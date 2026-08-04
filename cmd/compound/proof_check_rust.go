package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"compound/internal/authority/gitrepository"
	"compound/internal/work"
)

const (
	canonicalProofPlanCompoundV1      = "compound_v1"
	canonicalProofPlanRustWorkspaceV1 = "rust_workspace_v1"
)

// canonicalProofPlan is the one immutable, source-bound inventory shared by proof admission and
// execution. Its ID is descriptive; ProofResourceProfile's digest remains the authority because
// it seals the repository, source snapshot, and every ordered gate identity.
type canonicalProofPlan struct {
	ID     string
	Repo   string
	Source gitrepository.Snapshot
	Steps  []proofCheckStep
}

type canonicalProofPlanContextKey struct{}

func resolveCanonicalProofPlan(repo string, source gitrepository.Snapshot) (canonicalProofPlan, error) {
	resolvedRepo, err := resolveProofCheckRepo(repo)
	if err != nil {
		return canonicalProofPlan{}, err
	}
	if err := gitrepository.ValidateSnapshotShape(source); err != nil {
		return canonicalProofPlan{}, fmt.Errorf("canonical proof source: %w", err)
	}
	if !source.Clean {
		return canonicalProofPlan{}, fmt.Errorf("canonical proof source must be clean")
	}

	markers, err := canonicalProofRootMarkers(
		resolvedRepo,
		source.HeadSHA,
		[]string{"Cargo.lock", "Cargo.toml", "go.mod"},
	)
	if err != nil {
		return canonicalProofPlan{}, fmt.Errorf("inspect canonical proof root markers at %s: %w", source.HeadSHA, err)
	}
	hasCargoManifest := markers["Cargo.toml"]
	hasCargoLock := markers["Cargo.lock"]
	hasGoModule := markers["go.mod"]

	plan := canonicalProofPlan{Repo: resolvedRepo, Source: source}
	switch {
	case hasCargoManifest && hasGoModule:
		return canonicalProofPlan{}, fmt.Errorf("mixed root Cargo.toml and go.mod are not a canonical proof profile")
	case hasCargoManifest && !hasCargoLock:
		return canonicalProofPlan{}, fmt.Errorf("Rust canonical proof requires a tracked root Cargo.lock")
	case hasCargoManifest:
		plan.ID = canonicalProofPlanRustWorkspaceV1
		plan.Steps = rustWorkspaceProofCheckSteps(source.BaseSHA, source.HeadSHA)
	case hasCargoLock:
		return canonicalProofPlan{}, fmt.Errorf("tracked root Cargo.lock without Cargo.toml is not a canonical proof profile")
	case hasGoModule:
		moduleBody, blobErr := canonicalProofGitBlob(resolvedRepo, source.HeadSHA, "go.mod")
		if blobErr != nil {
			return canonicalProofPlan{}, blobErr
		}
		if !isCanonicalCompoundModule(moduleBody) {
			return canonicalProofPlan{}, fmt.Errorf("canonical Compound proof requires tracked root go.mod declaring `module compound`")
		}
		plan.ID = canonicalProofPlanCompoundV1
		plan.Steps, err = proofCheckSteps(source.BaseSHA, source.HeadSHA)
		if err != nil {
			return canonicalProofPlan{}, err
		}
	default:
		return canonicalProofPlan{}, fmt.Errorf("unsupported canonical proof repository: tracked root go.mod or Cargo.toml plus Cargo.lock required")
	}
	return cloneCanonicalProofPlan(plan), nil
}

func canonicalProofRootMarkers(repo, headSHA string, paths []string) (map[string]bool, error) {
	args := []string{"ls-tree", "-z", headSHA, "--"}
	args = append(args, paths...)
	records, err := proofCheckGitNULList(repo, args...)
	if err != nil {
		return nil, err
	}
	markers := make(map[string]bool, len(records))
	for _, record := range records {
		metadataAndPath := strings.SplitN(record, "\t", 2)
		if len(metadataAndPath) != 2 {
			return nil, fmt.Errorf("malformed Git tree marker %q", record)
		}
		metadata := strings.Fields(metadataAndPath[0])
		path := strings.TrimSpace(metadataAndPath[1])
		if len(metadata) != 3 || path == "" {
			return nil, fmt.Errorf("malformed Git tree marker %q", record)
		}
		mode, objectType := metadata[0], metadata[1]
		if objectType != "blob" || (mode != "100644" && mode != "100755") {
			return nil, fmt.Errorf("canonical proof root marker %s must be a regular blob, got mode=%s type=%s", path, mode, objectType)
		}
		markers[path] = true
	}
	return markers, nil
}

func refuseRustInCompoundOnlyProofMode(repo, mode string) error {
	refusal := func() error {
		return fmt.Errorf("%s is Compound-specific; a Rust workspace requires governed canonical proof with explicit --base, --work, --actor, and --out", mode)
	}
	for _, marker := range []string{"Cargo.toml", "Cargo.lock"} {
		if _, err := os.Lstat(filepath.Join(repo, marker)); err == nil {
			return refusal()
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect %s root marker %s: %w", mode, marker, err)
		}
	}
	committed, err := proofCheckGitNULList(
		repo,
		"ls-tree", "-z", "--name-only", "HEAD", "--", "Cargo.lock", "Cargo.toml",
	)
	if err == nil && len(committed) > 0 {
		return refusal()
	}
	return nil
}

var rustfmtDiffHeaderPattern = regexp.MustCompile(`^Diff in (.+):([0-9]+):$`)

// Rustfmt failures are block-structured whitespace evidence. Hashing their lines through the
// generic normalizer would erase indentation, ordering, and header/body association, so only the
// execution-root prefix is normalized and each otherwise byte-exact diff block becomes one fact.
func proofCheckFailureFacts(repo, gate string, output []byte, runErr error) []string {
	if gate == "rustfmt" && runErr != nil {
		if blocks, ok := rustfmtFailureBlocks(repo, output); ok {
			facts := make([]string, 0, len(blocks))
			for _, block := range blocks {
				sum := sha256.Sum256([]byte("rustfmt:block:v1\x00" + block))
				facts = append(facts, "sha256:"+hex.EncodeToString(sum[:]))
			}
			sort.Strings(facts)
			return facts
		}
	}
	return work.ProofFailureFacts(gate, output, runErr)
}

func rustfmtFailureBlocks(repo string, output []byte) ([]string, bool) {
	absRepo, err := filepath.Abs(repo)
	if err != nil || strings.ContainsAny(absRepo, "\r\n") {
		return nil, false
	}
	roots := []string{filepath.Clean(absRepo)}
	if physicalRepo, evalErr := filepath.EvalSymlinks(absRepo); evalErr == nil {
		physicalRepo = filepath.Clean(physicalRepo)
		if physicalRepo != roots[0] {
			roots = append(roots, physicalRepo)
		}
	}

	var blocks []*strings.Builder
	var sawChange []bool
	for _, rawLine := range strings.SplitAfter(string(output), "\n") {
		if rawLine == "" {
			continue
		}
		line, lineEnding := rustfmtLineAndEnding(rawLine)
		if !strings.HasPrefix(line, "Diff in ") {
			if len(blocks) == 0 || !validRustfmtDiffBodyLine(line) {
				return nil, false
			}
			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
				sawChange[len(sawChange)-1] = true
			}
			blocks[len(blocks)-1].WriteString(rawLine)
			continue
		}
		if len(blocks) > 0 && !sawChange[len(sawChange)-1] {
			return nil, false
		}
		match := rustfmtDiffHeaderPattern.FindStringSubmatch(line)
		if len(match) != 3 || !filepath.IsAbs(match[1]) {
			return nil, false
		}
		relative, inside := rustfmtPathRelativeToRepository(roots, match[1])
		if !inside {
			return nil, false
		}
		blocks = append(blocks, &strings.Builder{})
		sawChange = append(sawChange, false)
		blocks[len(blocks)-1].WriteString("Diff in $REPO/" + filepath.ToSlash(relative) + ":" + match[2] + ":" + lineEnding)
	}
	if len(blocks) == 0 || !sawChange[len(sawChange)-1] {
		return nil, false
	}
	labels := make([]string, len(blocks))
	for i := range blocks {
		labels[i] = blocks[i].String()
	}
	return labels, true
}

func rustfmtLineAndEnding(raw string) (line, ending string) {
	line = raw
	if strings.HasSuffix(line, "\n") {
		line = strings.TrimSuffix(line, "\n")
		ending = "\n"
	}
	if strings.HasSuffix(line, "\r") {
		line = strings.TrimSuffix(line, "\r")
		ending = "\r" + ending
	}
	return line, ending
}

func validRustfmtDiffBodyLine(line string) bool {
	if line == "" {
		return true
	}
	return line[0] == ' ' || line[0] == '+' || line[0] == '-'
}

func rustfmtPathRelativeToRepository(roots []string, path string) (string, bool) {
	path = filepath.Clean(path)
	for _, root := range roots {
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			continue
		}
		return relative, true
	}
	return "", false
}

func canonicalProofGitBlob(repo, headSHA, path string) ([]byte, error) {
	cmd := proofCheckCommandContext(context.Background(), "git", "show", headSHA+":"+path)
	cmd.Dir = repo
	body, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read canonical proof marker %s: %w", path, err)
	}
	return body, nil
}

func isCanonicalCompoundModule(body []byte) bool {
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "//") {
			continue
		}
		return len(fields) == 2 && fields[0] == "module" && fields[1] == "compound"
	}
	return false
}

func rustWorkspaceProofCheckSteps(baseRef, headRef string) []proofCheckStep {
	diffRange := strings.TrimSpace(baseRef) + "..." + strings.TrimSpace(headRef)
	redactCommand := []string{
		"redact", "--repo-visibility", "public",
		"<committed blobs changed in " + diffRange + ">",
	}
	return []proofCheckStep{
		{Name: "rustfmt", Command: []string{"cargo", "fmt", "--all", "--", "--check"}, ProofRef: "cargo-fmt-all-check", CIPortable: true},
		{Name: "cargo-clippy", Command: []string{"cargo", "clippy", "--locked", "--workspace", "--all-targets", "--", "-D", "warnings"}, ProofRef: "cargo-clippy-locked-workspace-all-targets-deny-warnings", CIPortable: true},
		{Name: "cargo-test", Command: []string{"cargo", "test", "--locked", "--workspace"}, ProofRef: "cargo-test-locked-workspace", CIPortable: true},
		{Name: "cargo-build", Command: []string{"cargo", "build", "--locked", "--workspace"}, ProofRef: "cargo-build-locked-workspace", CIPortable: true},
		{Name: "diff-check", Command: append([]string{"git", "diff", "--check"}, diffRangeArgs(diffRange)...), ProofRef: "diff-check", CIPortable: true},
		{Name: "redact-public", Command: redactCommand, ProofRef: "redact-public-clean"},
	}
}

func proofCheckAdmissionProfileForPlan(plan canonicalProofPlan) (work.ProofResourceProfile, error) {
	if strings.TrimSpace(plan.ID) == "" || strings.TrimSpace(plan.Repo) == "" || len(plan.Steps) == 0 {
		return work.ProofResourceProfile{}, fmt.Errorf("canonical proof plan is incomplete")
	}
	gates := make([]work.ProofResourceGateProfile, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		gates = append(gates, work.ProofResourceGateProfile{
			Name:          step.Name,
			Command:       append([]string(nil), step.Command...),
			Dir:           step.Dir,
			SkipIfMissing: step.SkipIfMissing,
			ProofRef:      step.ProofRef,
		})
	}
	return work.NewProofResourceProfileWithSource(
		work.ProofResourceProfileCanonicalFull,
		plan.Repo,
		plan.Source,
		gates,
	)
}

func canonicalProofProfilePublishesCompoundRuntimeArtifact(
	plan canonicalProofPlan,
	profile work.ProofResourceProfile,
) (bool, error) {
	if plan.ID != canonicalProofPlanCompoundV1 {
		return false, nil
	}
	expectedSteps, err := proofCheckSteps(plan.Source.BaseSHA, plan.Source.HeadSHA)
	if err != nil {
		return false, err
	}
	expectedPlan := cloneCanonicalProofPlan(plan)
	expectedPlan.Steps = expectedSteps
	expectedProfile, err := proofCheckAdmissionProfileForPlan(expectedPlan)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(profile, expectedProfile), nil
}

func proofCheckContextWithCanonicalPlan(ctx context.Context, plan canonicalProofPlan) context.Context {
	return context.WithValue(ctx, canonicalProofPlanContextKey{}, cloneCanonicalProofPlan(plan))
}

func canonicalProofPlanForExecution(
	ctx context.Context,
	repo string,
	source gitrepository.Snapshot,
) (canonicalProofPlan, error) {
	if stored, ok := ctx.Value(canonicalProofPlanContextKey{}).(canonicalProofPlan); ok {
		resolvedRepo, err := resolveProofCheckRepo(repo)
		if err != nil {
			return canonicalProofPlan{}, err
		}
		if !reflect.DeepEqual(stored.Source, source) {
			return canonicalProofPlan{}, fmt.Errorf("canonical proof plan does not match execution source")
		}
		executionSource, err := gitrepository.CaptureCleanSnapshot(resolvedRepo, source.BaseSHA)
		if err != nil {
			return canonicalProofPlan{}, fmt.Errorf("canonical proof execution checkout: %w", err)
		}
		if executionSource.BaseSHA != source.BaseSHA ||
			executionSource.HeadSHA != source.HeadSHA ||
			executionSource.TreeSHA != source.TreeSHA {
			return canonicalProofPlan{}, fmt.Errorf("canonical proof execution checkout does not match sealed source")
		}
		return cloneCanonicalProofPlan(stored), nil
	}
	return resolveCanonicalProofPlan(repo, source)
}

func cloneCanonicalProofPlan(in canonicalProofPlan) canonicalProofPlan {
	out := in
	out.Steps = make([]proofCheckStep, len(in.Steps))
	for i, step := range in.Steps {
		out.Steps[i] = step
		out.Steps[i].Command = append([]string(nil), step.Command...)
		out.Steps[i].FailureFacts = append([]string(nil), step.FailureFacts...)
	}
	return out
}
