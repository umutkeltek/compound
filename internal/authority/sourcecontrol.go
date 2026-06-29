package authority

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

const SourceControlSchemaVersion = "compound.source_control_snapshot.v0.1"

type SourceControlSnapshot struct {
	SchemaVersion string `json:"schema_version"`
	RepoID        string `json:"repo_id"`
	RequestedPath string `json:"requested_path,omitempty"`
	GitRoot       string `json:"git_root"`
	Remote        string `json:"remote,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Head          string `json:"head,omitempty"`
	Upstream      string `json:"upstream,omitempty"`
	Ahead         int    `json:"ahead"`
	Behind        int    `json:"behind"`
	Status        string `json:"status"` // clean | dirty
	Staged        int    `json:"staged"`
	Unstaged      int    `json:"unstaged"`
	Untracked     int    `json:"untracked"`
	ChangedPaths  int    `json:"changed_paths"`
	EvidenceRef   string `json:"evidence_ref"`
}

type SourceControlReport struct {
	EntityRef      string `json:"entity_ref"`
	Status         string `json:"status"`
	GitRoot        string `json:"git_root"`
	Branch         string `json:"branch,omitempty"`
	Head           string `json:"head,omitempty"`
	Remote         string `json:"remote,omitempty"`
	Upstream       string `json:"upstream,omitempty"`
	Ahead          int    `json:"ahead"`
	Behind         int    `json:"behind"`
	Staged         int    `json:"staged"`
	Unstaged       int    `json:"unstaged"`
	Untracked      int    `json:"untracked"`
	ChangedPaths   int    `json:"changed_paths"`
	CandidateCount int    `json:"candidate_count"`
	EvidenceRef    string `json:"evidence_ref"`
}

// ImportGitSnapshot reads live git state with read-only git commands and turns
// it into deterministic authority candidates. It never stages, cleans, fetches,
// pushes, or edits the inspected repository.
func ImportGitSnapshot(path string, existing []mem.Entity) (SourceControlSnapshot, mem.Entity, []mem.Candidate, SourceControlReport, error) {
	snap, err := ReadGitSnapshot(path)
	if err != nil {
		return SourceControlSnapshot{}, mem.Entity{}, nil, SourceControlReport{Status: "fail"}, err
	}
	entity := mergeSourceControlEntity(snap, existing)
	facts := sourceControlCandidates(snap)
	report := SourceControlReport{
		EntityRef:      entity.Slug,
		Status:         snap.Status,
		GitRoot:        snap.GitRoot,
		Branch:         snap.Branch,
		Head:           snap.Head,
		Remote:         snap.Remote,
		Upstream:       snap.Upstream,
		Ahead:          snap.Ahead,
		Behind:         snap.Behind,
		Staged:         snap.Staged,
		Unstaged:       snap.Unstaged,
		Untracked:      snap.Untracked,
		ChangedPaths:   snap.ChangedPaths,
		CandidateCount: len(facts),
		EvidenceRef:    snap.EvidenceReference(),
	}
	return snap, entity, facts, report, nil
}

func ReadGitSnapshot(path string) (SourceControlSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requestedPath, inspectDir, err := gitInspectionPath(path)
	if err != nil {
		return SourceControlSnapshot{}, err
	}
	root, err := gitOutput(ctx, inspectDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return SourceControlSnapshot{}, fmt.Errorf("git root: %w", err)
	}
	root = strings.TrimSpace(root)
	remote, _ := gitOutput(ctx, root, "config", "--get", "remote.origin.url")
	branch, _ := gitOutput(ctx, root, "branch", "--show-current")
	head, _ := gitOutput(ctx, root, "rev-parse", "--short=12", "HEAD")
	upstream, _ := gitOutput(ctx, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	ahead, behind := 0, 0
	if strings.TrimSpace(upstream) != "" {
		if counts, err := gitOutput(ctx, root, "rev-list", "--left-right", "--count", "HEAD...@{u}"); err == nil {
			ahead, behind = parseAheadBehind(counts)
		}
	}
	porcelain, err := gitOutput(ctx, root, "status", "--porcelain=v1", "-b")
	if err != nil {
		return SourceControlSnapshot{}, fmt.Errorf("git status: %w", err)
	}
	staged, unstaged, untracked := parsePorcelainCounts(porcelain)
	changed := staged + unstaged + untracked
	status := "clean"
	if changed > 0 {
		status = "dirty"
	}
	snap := SourceControlSnapshot{
		SchemaVersion: SourceControlSchemaVersion,
		RepoID:        repoID(strings.TrimSpace(remote), root),
		RequestedPath: requestedPath,
		GitRoot:       root,
		Remote:        strings.TrimSpace(remote),
		Branch:        strings.TrimSpace(branch),
		Head:          strings.TrimSpace(head),
		Upstream:      strings.TrimSpace(upstream),
		Ahead:         ahead,
		Behind:        behind,
		Status:        status,
		Staged:        staged,
		Unstaged:      unstaged,
		Untracked:     untracked,
		ChangedPaths:  changed,
		EvidenceRef:   sourceControlEvidenceRef(repoID(strings.TrimSpace(remote), root), strings.TrimSpace(head)),
	}
	return snap, nil
}

func (s SourceControlSnapshot) EvidenceReference() string {
	if strings.TrimSpace(s.EvidenceRef) != "" {
		return s.EvidenceRef
	}
	return sourceControlEvidenceRef(s.RepoID, s.Head)
}

func sourceControlEvidenceRef(repoID, head string) string {
	head = strings.TrimSpace(head)
	if head == "" {
		head = "unknown-head"
	}
	return "source-control:" + repoID + ":" + head
}

func gitInspectionPath(path string) (requestedPath, inspectDir string, err error) {
	requestedPath, err = filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(requestedPath)
	if err == nil {
		if info.IsDir() {
			return requestedPath, requestedPath, nil
		}
		return requestedPath, filepath.Dir(requestedPath), nil
	}
	if !os.IsNotExist(err) {
		return "", "", err
	}
	parent, err := nearestExistingParent(requestedPath)
	if err != nil {
		return "", "", err
	}
	return requestedPath, parent, nil
}

func nearestExistingParent(path string) (string, error) {
	p := path
	for {
		info, err := os.Stat(p)
		if err == nil {
			if info.IsDir() {
				return p, nil
			}
			return filepath.Dir(p), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(p)
		if next == p {
			return "", fmt.Errorf("no existing parent for %s", path)
		}
		p = next
	}
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}

func parsePorcelainCounts(text string) (staged, unstaged, untracked int) {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "##") {
			continue
		}
		if strings.HasPrefix(line, "??") {
			untracked++
			continue
		}
		if len(line) < 2 {
			continue
		}
		if line[0] != ' ' {
			staged++
		}
		if line[1] != ' ' {
			unstaged++
		}
	}
	return staged, unstaged, untracked
}

func parseAheadBehind(text string) (ahead, behind int) {
	parts := strings.Fields(text)
	if len(parts) >= 2 {
		ahead, _ = strconv.Atoi(parts[0])
		behind, _ = strconv.Atoi(parts[1])
	}
	return ahead, behind
}

func repoID(remote, root string) string {
	base := strings.TrimSpace(remote)
	if base == "" {
		base = filepath.Base(root)
	} else {
		base = strings.TrimSuffix(base, ".git")
		base = filepath.Base(strings.ReplaceAll(base, ":", "/"))
	}
	return "repo:" + strings.ReplaceAll(slugToken(base), "_", "-")
}

func slugToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

func mergeSourceControlEntity(snap SourceControlSnapshot, existing []mem.Entity) mem.Entity {
	e := mem.Entity{Slug: snap.RepoID, Kind: "repo", Visibility: "operational"}
	for _, ex := range existing {
		if ex.Slug == snap.RepoID {
			e = ex
			e.Kind = "repo"
			if e.Visibility == "" {
				e.Visibility = "operational"
			}
			break
		}
	}
	ids := []string{snap.GitRoot}
	if snap.RequestedPath != "" {
		ids = append(ids, snap.RequestedPath)
	}
	if snap.Remote != "" {
		ids = append(ids, snap.Remote, "git_remote:"+snap.Remote)
	}
	e.Aliases = mergeStrings(e.Aliases, strings.TrimPrefix(snap.RepoID, "repo:"))
	e.Identifiers = mergeStrings(e.Identifiers, ids...)
	return e
}

func sourceControlCandidates(snap SourceControlSnapshot) []mem.Candidate {
	src := snap.EvidenceReference()
	add := func(out *[]mem.Candidate, pred, object string) {
		object = strings.TrimSpace(object)
		if object == "" {
			return
		}
		*out = append(*out, mem.Candidate{
			Entity:         snap.RepoID,
			Predicate:      pred,
			Object:         object,
			Confidence:     1.0,
			Derivation:     "deterministic",
			ClaimType:      "status",
			Qualifiers:     map[string]string{"domain": pred},
			PromotionBasis: candidates.BasisTrustedDeterministic,
			SourceTraceRef: src,
			SnippetHash:    src + ":" + pred + ":" + object,
			Visibility:     "operational",
		})
	}
	var facts []mem.Candidate
	add(&facts, "git_root", snap.GitRoot)
	add(&facts, "source_repo", snap.Remote)
	add(&facts, "current_branch", snap.Branch)
	add(&facts, "head", snap.Head)
	add(&facts, "upstream", snap.Upstream)
	add(&facts, "source_control_status", snap.Status)
	add(&facts, "ahead_count", strconv.Itoa(snap.Ahead))
	add(&facts, "behind_count", strconv.Itoa(snap.Behind))
	add(&facts, "staged_count", strconv.Itoa(snap.Staged))
	add(&facts, "unstaged_count", strconv.Itoa(snap.Unstaged))
	add(&facts, "untracked_count", strconv.Itoa(snap.Untracked))
	add(&facts, "changed_path_count", strconv.Itoa(snap.ChangedPaths))
	return facts
}
