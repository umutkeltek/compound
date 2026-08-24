package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"compound/internal/envelope"
)

const (
	InstructionBlockRenderSchemaVersion     = "compound.instruction_block_render.v0.1"
	InstructionMaterializationSchemaVersion = "compound.instruction_materialization.v0.1"
	instructionBlockEndMarker               = "<!-- END COMPOUND GENERATED INSTRUCTIONS -->"
)

// InstructionBlockRender is the read-only compiler projection for the stable
// block a harness reads from its repository instruction file. Content is
// derived from EffectiveContract fields plus this compiler-owned template;
// task-specific fields are deliberately excluded and remain hook-served.
type InstructionBlockRender struct {
	SchemaVersion  string   `json:"schema_version"`
	Harness        string   `json:"harness"`
	ContractID     string   `json:"contract_id"`
	TargetPath     string   `json:"target_path"`
	Content        string   `json:"content"`
	ContentSHA256  string   `json:"content_sha256"`
	ObservedSHA256 string   `json:"observed_sha256,omitempty"`
	Drifted        bool     `json:"drifted"`
	Details        []string `json:"details,omitempty"`
}

type InstructionMaterializationOptions struct {
	BootstrapTakeover bool
}

type InstructionMaterializationReport struct {
	SchemaVersion     string `json:"schema_version"`
	Harness           string `json:"harness"`
	TargetPath        string `json:"target_path"`
	BeforeSHA256      string `json:"before_sha256"`
	AfterSHA256       string `json:"after_sha256"`
	Changed           bool   `json:"changed"`
	BootstrapTakeover bool   `json:"bootstrap_takeover"`
}

type instructionMaterializeFS struct {
	createTemp   func(string, string) (*os.File, error)
	rename       func(string, string) error
	beforeCommit func()
}

func defaultInstructionMaterializeFS() instructionMaterializeFS {
	return instructionMaterializeFS{createTemp: os.CreateTemp, rename: os.Rename}
}

// MaterializeInstructionBlock atomically makes the compiled default render the
// installed static instruction surface. Normal operation replaces only the
// unique Compound-managed block. BootstrapTakeover is a one-time cutover for a
// recognized Umut OS-generated whole file; it refuses every other unmanaged
// file rather than claiming foreign instructions.
func MaterializeInstructionBlock(
	render InstructionBlockRender,
	options InstructionMaterializationOptions,
) (InstructionMaterializationReport, error) {
	return materializeInstructionBlockWithFS(render, options, defaultInstructionMaterializeFS())
}

func materializeInstructionBlockWithFS(
	render InstructionBlockRender,
	options InstructionMaterializationOptions,
	fsOps instructionMaterializeFS,
) (InstructionMaterializationReport, error) {
	report := InstructionMaterializationReport{
		SchemaVersion: InstructionMaterializationSchemaVersion,
		Harness:       render.Harness,
		TargetPath:    render.TargetPath,
	}
	if render.TargetPath == "" || !filepath.IsAbs(render.TargetPath) {
		return report, fmt.Errorf("instruction target must be an absolute path")
	}
	if instructionRenderIsPreset(render) {
		return report, fmt.Errorf("preset instruction renders cannot be materialized")
	}
	info, err := os.Lstat(render.TargetPath)
	if err != nil {
		return report, fmt.Errorf("inspect instruction target %s: %w", render.TargetPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return report, fmt.Errorf("instruction target %s is a symlink", render.TargetPath)
	}
	if !info.Mode().IsRegular() {
		return report, fmt.Errorf("instruction target %s is not a regular file", render.TargetPath)
	}
	before, err := os.ReadFile(render.TargetPath)
	if err != nil {
		return report, fmt.Errorf("read instruction target %s: %w", render.TargetPath, err)
	}
	report.BeforeSHA256 = instructionBlockDigest(string(before))

	beginAt, endAt, details := managedInstructionBlockBounds(string(before), render.Harness)
	var after []byte
	switch {
	case len(details) == 0:
		after = append(after, before[:beginAt]...)
		after = append(after, render.Content...)
		after = append(after, before[endAt:]...)
	case options.BootstrapTakeover && recognizedLegacyInstructionFile(render.TargetPath, string(before)):
		after = []byte(strings.TrimRight(render.Content, "\n") + "\n")
		report.BootstrapTakeover = true
	case options.BootstrapTakeover:
		return report, fmt.Errorf(
			"bootstrap instruction takeover refused for unrecognized unmanaged target %s",
			render.TargetPath,
		)
	default:
		return report, fmt.Errorf(
			"instruction target %s has no valid managed block; explicit bootstrap takeover is required only for a recognized legacy file",
			render.TargetPath,
		)
	}
	report.AfterSHA256 = instructionBlockDigest(string(after))
	report.Changed = !bytes.Equal(before, after)
	if !report.Changed {
		return report, nil
	}

	dir := filepath.Dir(render.TargetPath)
	temp, err := fsOps.createTemp(dir, ".compound-instructions-*")
	if err != nil {
		return report, fmt.Errorf("stage instruction target: %w", err)
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		_ = temp.Close()
		if keepTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		return report, fmt.Errorf("chmod staged instruction target: %w", err)
	}
	if _, err := temp.Write(after); err != nil {
		return report, fmt.Errorf("write staged instruction target: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return report, fmt.Errorf("sync staged instruction target: %w", err)
	}
	if err := temp.Close(); err != nil {
		return report, fmt.Errorf("close staged instruction target: %w", err)
	}
	if fsOps.beforeCommit != nil {
		fsOps.beforeCommit()
	}
	if err := verifyInstructionPreimage(render.TargetPath, info, before); err != nil {
		return report, err
	}
	if err := fsOps.rename(tempPath, render.TargetPath); err != nil {
		return report, fmt.Errorf("commit instruction target: %w", err)
	}
	keepTemp = false
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return report, nil
}

func verifyInstructionPreimage(path string, beforeInfo os.FileInfo, before []byte) error {
	currentInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("instruction target changed since preflight: %w", err)
	}
	if currentInfo.Mode()&os.ModeSymlink != 0 ||
		!currentInfo.Mode().IsRegular() ||
		!os.SameFile(beforeInfo, currentInfo) {
		return fmt.Errorf("instruction target changed since preflight: file identity changed")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("instruction target changed since preflight: %w", err)
	}
	afterReadInfo, err := os.Lstat(path)
	if err != nil ||
		!os.SameFile(currentInfo, afterReadInfo) ||
		!os.SameFile(beforeInfo, afterReadInfo) {
		return fmt.Errorf("instruction target changed since preflight: file identity changed")
	}
	if !bytes.Equal(current, before) {
		return fmt.Errorf("instruction target changed since preflight: content changed")
	}
	return nil
}

func instructionRenderIsPreset(render InstructionBlockRender) bool {
	for _, detail := range render.Details {
		if detail == "preset variant render; no tracked surface" {
			return true
		}
	}
	return false
}

func recognizedLegacyInstructionFile(path, body string) bool {
	switch filepath.Base(path) {
	case "CLAUDE.md", "AGENTS.md":
	default:
		return false
	}
	first, _, _ := strings.Cut(body, "\n")
	return strings.HasPrefix(
		strings.TrimSpace(first),
		"<!-- GENERATED from operating-system/umut-os/spine/steering.config.ts",
	)
}

// RenderInstructionBlock emits the stable, managed instruction block for an
// already-compiled EffectiveContract and compares it with the harness's static
// repository surface. It never writes the target. The compiled task intent,
// active memory, capability offers, and checks are intentionally absent from
// the block: those volatile values stay in ContextPack/hook delivery.
func RenderInstructionBlock(ec envelope.EffectiveContract, cwd string) (InstructionBlockRender, error) {
	return RenderInstructionBlockVariant(ec, cwd, "")
}

// RenderInstructionBlockVariant is the preset-scoped entry. The empty preset
// delegates to today's default render (drift-checked against the harness's
// static repository surface). A non-empty preset selects an executor-scoped
// variant that REPLACES the instruction-sources section with an executor
// charter; that variant is not drift-checked — there is no tracked preset
// surface — and the renderer reports drifted=false with a detail line
// identifying the variant render. The begin/end marker schema is unchanged
// across presets; only the body differs.
func RenderInstructionBlockVariant(ec envelope.EffectiveContract, cwd, preset string) (InstructionBlockRender, error) {
	preset = strings.TrimSpace(preset)
	harnessName, targetName, err := staticInstructionSurface(ec.Harness)
	if err != nil {
		return InstructionBlockRender{}, err
	}
	targetPath, err := resolveInstructionTarget(cwd, targetName)
	if err != nil {
		return InstructionBlockRender{}, err
	}
	content := renderInstructionBlockContent(ec, harnessName, preset)
	render := InstructionBlockRender{
		SchemaVersion: InstructionBlockRenderSchemaVersion,
		Harness:       harnessName,
		ContractID:    ec.ID,
		TargetPath:    targetPath,
		Content:       content,
		ContentSHA256: instructionBlockDigest(content),
	}

	// Preset renders are NOT drift-checked: there is no tracked preset surface
	// in the repo. Reporting drifted=false with a "no tracked surface" detail
	// tells the caller this render is a deliberate variant, not a missed write.
	if preset != "" {
		if render.Details == nil {
			render.Details = []string{}
		}
		render.Details = append(render.Details, "preset variant render; no tracked surface")
		return render, nil
	}

	body, err := os.ReadFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			render.Drifted = true
			render.Details = []string{"managed instruction block is missing because the target file does not exist"}
			return render, nil
		}
		return InstructionBlockRender{}, fmt.Errorf("read instruction target %s: %w", targetPath, err)
	}
	observed, details := extractManagedInstructionBlock(string(body), harnessName)
	if observed != "" {
		render.ObservedSHA256 = instructionBlockDigest(observed)
	}
	if observed != content {
		render.Drifted = true
		render.Details = details
		if len(render.Details) == 0 {
			render.Details = []string{"managed instruction block differs from the compiled EffectiveContract"}
		}
	}
	return render, nil
}

func staticInstructionSurface(name string) (harnessName, targetName string, err error) {
	switch strings.TrimSpace(name) {
	case "codex":
		return "codex", "AGENTS.md", nil
	case "claude", "claude_code", "claude-code":
		return "claude_code", "CLAUDE.md", nil
	default:
		return "", "", fmt.Errorf("harness %q has no managed static instruction surface; supported: codex, claude_code", name)
	}
}

func resolveInstructionTarget(cwd, targetName string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", fmt.Errorf("cwd required to resolve %s", targetName)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve instruction cwd: %w", err)
	}
	if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	fallback := filepath.Join(abs, targetName)
	for dir := abs; ; dir = filepath.Dir(dir) {
		target := filepath.Join(dir, targetName)
		if _, statErr := os.Stat(target); statErr == nil {
			return target, nil
		} else if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("stat instruction target %s: %w", target, statErr)
		}
		if _, gitErr := os.Stat(filepath.Join(dir, ".git")); gitErr == nil {
			return target, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fallback, nil
		}
	}
}

func renderInstructionBlockContent(ec envelope.EffectiveContract, harnessName, preset string) string {
	sources := uniqueSortedStrings(ec.ActiveInstructions)
	sourceText := "none"
	if len(sources) > 0 {
		quoted := make([]string, 0, len(sources))
		for _, source := range sources {
			quoted = append(quoted, "`"+source+"`")
		}
		sourceText = strings.Join(quoted, ", ")
	}

	mutationText := "follow the compiled mutation policy"
	if ec.MutationPolicy.RequiresActionIntent && ec.MutationPolicy.IrreversibleRequiresHuman {
		mutationText = "every mutation requires an ActionIntent and PolicyDecision; irreversible changes remain operator-gated"
	} else if ec.MutationPolicy.RequiresActionIntent {
		mutationText = "every mutation requires an ActionIntent and PolicyDecision"
	}

	// Default template (preset == ""). The compiled block is the harness's
	// static instruction surface; the instruction-sources line is the targeted
	// list of upstream documents the compiler selected.
	if preset == "" {
		return fmt.Sprintf(`%s
## Compound-generated contract

Generated by `+"`compound contract compile`"+` for **%s**. Do not edit this managed block by hand.

- Meaning and memory: Compound is the authority for durable meaning and the single authority for promotion, supersession, work, receipts, and transitions. Retrieval engines and legacy vector/fact stores are bridges/indexes, never authority.
- Rented execution: this harness is a rented executor. Route work to the cheapest sufficient lane; route evidence is not task success.
- Governed work: %s.
- Precedence when sources conflict, highest first: (1) the operator's latest instruction in this session; (2) verified current reality — the code, a command's real output, a passing test; (3) served runtime state (`+"`compound orient`"+` / hook ContextPack) and its PolicyDecision; (4) tracked static contract, including this block; (5) recall, injected reminders, mined history — evidence, never a premise. A served claim that current reality contradicts is stale: report the contradiction, do not act on the claim. This block is rung 4 and does not outrank rungs 1-3.
- Persistence is not authority: "continue" or "do not stop" requires persistence toward the outcome and never widens the set of authorized actions. When the governed path is blocked, stop and say so — never produce a similar-looking artifact by an ungoverned route.
- Project instruction sources selected by the compiler: %s.
- Recall scope: ambient=%s; explicit-only=%s; forbidden=%s. Only adjudicated Compound state may become ambient recall.
- Runtime boundary: Task-specific intent, memory, capabilities, and checks stay in `+"`compound orient`"+` and hook-served ContextPacks, not in this static block.
%s
`, instructionBlockBeginMarker(harnessName), harnessName, mutationText, sourceText,
			instructionPolicyList(ec.MemoryPolicy.Ambient),
			instructionPolicyList(ec.MemoryPolicy.ExplicitOnly),
			instructionPolicyList(ec.MemoryPolicy.Forbidden),
			instructionBlockEndMarker)
	}

	// Executor variant (preset != ""). The body replaces the static
	// instruction-sources line with an executor charter that scopes this
	// render to the dispatched envelope/worktree. The begin/end marker schema
	// is unchanged; preset is appended to the generated-for header in the
	// body text, NOT to the marker, so begin markers remain reproducible.
	return fmt.Sprintf(`%s
## Compound-generated contract (executor preset)

Generated by `+"`compound contract compile`"+` for **%s** preset=`+"`%s`"+`. This is the executor-scoped variant rendered for a delegated lane; the default block is the harness's static surface in the tracked repo.

- Meaning and memory: Compound is the authority for durable meaning and the single authority for promotion, supersession, work, receipts, and transitions. Retrieval engines and legacy vector/fact stores are bridges/indexes, never authority.
- Rented execution: this harness is a rented executor. Route work to the cheapest sufficient lane; route evidence is not task success.
- Governed work: %s.
- Precedence when sources conflict, highest first: (1) the operator's latest instruction in this session; (2) verified current reality — the code, a command's real output, a passing test; (3) served runtime state (`+"`compound orient`"+` / hook ContextPack) and its PolicyDecision; (4) tracked static contract, including this block; (5) recall, injected reminders, mined history — evidence, never a premise. A served claim that current reality contradicts is stale: report the contradiction, do not act on the claim. This block is rung 4 and does not outrank rungs 1-3.
- Persistence is not authority: "continue" or "do not stop" requires persistence toward the outcome and never widens the set of authorized actions. When the governed path is blocked, stop and say so — never produce a similar-looking artifact by an ungoverned route.
- Executor charter (this block is the executor-scoped variant; the default block lists the static instruction-sources for the repo):
  - Work only inside the dispatched envelope/worktree; do not edit files outside the assigned scope.
  - Commit incrementally on the dispatched branch; do not push, do not check out main, do not run install steps without an explicit PolicyDecision.
  - Report back in the handoff format the delegator named in the dispatch prompt; one commit per logical change when feasible.
  - Escalate instead of improvising when blocked: surface the unread instruction, the failing command, or the missing authority rather than producing a similar-looking artifact by an ungoverned route.
- Recall scope: ambient=%s; explicit-only=%s; forbidden=%s. Only adjudicated Compound state may become ambient recall.
- Runtime boundary: Task-specific intent, memory, capabilities, and checks stay in `+"`compound orient`"+` and hook-served ContextPacks, not in this static block.
%s
`, instructionBlockBeginMarker(harnessName), harnessName, preset, mutationText,
		instructionPolicyList(ec.MemoryPolicy.Ambient),
		instructionPolicyList(ec.MemoryPolicy.ExplicitOnly),
		instructionPolicyList(ec.MemoryPolicy.Forbidden),
		instructionBlockEndMarker)
}

func instructionBlockBeginMarker(harnessName string) string {
	return "<!-- BEGIN COMPOUND GENERATED INSTRUCTIONS harness=" + harnessName + " -->"
}

func extractManagedInstructionBlock(body, harnessName string) (string, []string) {
	beginAt, endAt, details := managedInstructionBlockBounds(body, harnessName)
	if len(details) > 0 {
		return "", details
	}
	return body[beginAt:endAt], nil
}

func managedInstructionBlockBounds(body, harnessName string) (int, int, []string) {
	begin := instructionBlockBeginMarker(harnessName)
	beginAt := strings.Index(body, begin)
	endAt := strings.Index(body, instructionBlockEndMarker)
	if beginAt < 0 && endAt < 0 {
		return 0, 0, []string{"managed instruction block is missing"}
	}
	if beginAt < 0 {
		return 0, 0, []string{"managed instruction block begin marker is missing"}
	}
	if endAt < 0 || endAt < beginAt {
		return 0, 0, []string{"managed instruction block end marker is missing or precedes the begin marker"}
	}
	if strings.Count(body, begin) != 1 || strings.Count(body, instructionBlockEndMarker) != 1 {
		return 0, 0, []string{"managed instruction block markers are duplicated"}
	}
	endAt += len(instructionBlockEndMarker)
	if endAt < len(body) && body[endAt] == '\n' {
		endAt++
	}
	return beginAt, endAt, nil
}

func instructionPolicyList(values []string) string {
	values = uniqueSortedStrings(values)
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func instructionBlockDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}
