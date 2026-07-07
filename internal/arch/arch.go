// Package arch is the architecture doctor: it makes "every package has a logical job, every
// candidate kind one home, no private leak, no silent new front door" a MECHANICAL check rather
// than a doc promise. The package Classification below is the ENFORCED source of truth; docs/
// architecture/status.md narrates it. A new internal/ package that nobody classifies FAILS the
// doctor — so the two-stack convergence debt stays visible and fragmentation cannot grow silently.
package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"compound/internal/candidates"
	"compound/internal/home"
)

// Class is what job a package has in the architecture (LAYERS.md: one spine + object families).
type Class string

const (
	Spine           Class = "spine"            // canonical runtime / spine-bound Gen-2 — no duplication
	ObjectFamily    Class = "object-family"    // an object family the spine's transitions create/own/adjudicate (LAYERS.md); authority for its objects, never for the runtime grammar
	Leaf            Class = "leaf"             // shared leaf util (e.g. the candidate map)
	MemTarget       Class = "mem-target"       // the convergence TARGET the Gen-1 object model migrates into
	ConvergePending Class = "converge-pending" // Gen-1 capability to migrate onto the spine, THEN retire (test-gated)
	Importer        Class = "importer"         // an importer to upgrade in place (authority/decisions)
)

// Classification — every internal/ package, with its job. This is enforced: see CheckClassificationTotal.
var Classification = map[string]Class{
	// spine (the envelope runtime grammar itself — allowlist-ratcheted, see spineCore)
	"arch":      Spine,
	"contract":  Spine,
	"envelope":  Spine,
	"harness":   Spine,
	"home":      Spine,
	"hook":      Spine,
	"policy":    Spine,
	"schema":    Spine,
	"work":      Spine,
	"writeback": Spine,
	// object families the spine's transitions create/own/adjudicate (LAYERS.md) — authority for
	// their objects, never for the runtime grammar
	"capgraph":       ObjectFamily,
	"director":       ObjectFamily,
	"distiller":      ObjectFamily,
	"localmodel":     ObjectFamily,
	"observation":    ObjectFamily,
	"panel":          ObjectFamily,
	"product":        ObjectFamily,
	"projection":     ObjectFamily,
	"routeevidence":  ObjectFamily,
	"runtimebinding": ObjectFamily,
	// shared leaf
	"breadth":        Leaf,
	"candidates":     Leaf,
	"release":        Leaf,
	"reviewartifact": Leaf,
	// memory object family — canonical after Gen-1 package retirement
	"mem": MemTarget,
	// importers — keep, upgrade in place
	"authority":    Importer,
	"decisions":    Importer,
	"eventsource":  Importer,
	"knowledge":    Importer,
	"sourceobs":    Importer,
	"tooltopology": Importer,
}

// spineCore is the ALLOWLIST of packages that ARE the runtime spine (the envelope grammar every
// harness/hook/run flows through). Membership is a conscious edit here, never a default: if
// everything is spine, agents treat everything as equally authoritative. checkClassificationTotal
// fails if a package is classified Spine without a spineCore entry (or a spineCore entry is not
// classified Spine), so a new package must be classified as object-family/leaf/importer instead of
// quietly joining the authoritative spine. Keep this in lockstep with the Spine rows above.
var spineCore = map[string]bool{
	"arch":      true,
	"contract":  true,
	"envelope":  true,
	"harness":   true,
	"home":      true,
	"hook":      true,
	"policy":    true,
	"schema":    true,
	"work":      true,
	"writeback": true,
}

// Severity of a finding. fail = the doctor is not OK; warn/info are reported, not fatal.
type Severity string

const (
	Fail Severity = "fail"
	Warn Severity = "warn"
	Info Severity = "info"
)

// Finding is one doctor result line.
type Finding struct {
	Check    string
	Severity Severity
	Message  string
}

// Report is the doctor outcome.
type Report struct {
	Findings []Finding
	OK       bool // false if any Fail
}

// genericLeak matches GENERIC operator-private shapes (absolute user/home paths, emails) that must
// never appear in a public tracked doc. Operator-SPECIFIC forbidden tokens (private tool/product
// names) are deliberately NOT hardcoded here — putting them in public core would itself leak them.
// They load from an optional private list (docs_private/leak-tokens.txt, one token per line) so the
// public core stays clean while a private overlay can enforce its own vocabulary.
var genericLeak = regexp.MustCompile(`(?i)(/Users/[a-z]|/home/[a-z]|[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,})`)
var statusProofJournalLine = regexp.MustCompile(`(?i)^\s*(?:[-*]\s*)?(?:20[0-9]{2}-[0-9]{2}-[0-9]{2}\b|proof log:|verification run:|test run:|commit [0-9a-f]{7,40}\b)`)

// loadPrivateLeakTokens reads operator-specific forbidden tokens from docs_private/leak-tokens.txt
// (gitignored). It also reports whether the file was PRESENT, so the caller can fail CLOSED: an
// absent list leaves the gate blind to operator tool/product names (it can still catch generic
// path/email shapes), and a public release must not pass with the gate silently disarmed.
func loadPrivateLeakTokens(repoRoot string) (toks []string, present bool) {
	return loadLeakTokens(filepath.Join(repoRoot, "docs_private", "leak-tokens.txt"))
}

func loadLeakTokens(path string) (toks []string, present bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			toks = append(toks, strings.ToLower(t))
		}
	}
	return toks, true
}

// ConvergePendingBudget is the RATCHET: the count of converge→retire packages must never exceed
// this. `--strict` fails if it grows (new fragmentation). Lower it as packages converge onto the
// spine — the budget can only go down, so the debt can never become wallpaper or quietly grow.
const ConvergePendingBudget = 0
const MaxStatusHotPathLines = 250

// Doctor runs every check. repoRoot is the compound repo root for filesystem checks (classification
// totality, doc leak); pass "" to run only the in-memory checks (candidate map). strict turns the
// convergence-debt ratchet into a hard failure if the debt exceeds its budget. It never mutates.
func Doctor(repoRoot string, strict bool) Report {
	var f []Finding
	f = append(f, checkCandidateMap()...)
	if repoRoot != "" {
		f = append(f, checkClassificationTotal(repoRoot)...)
		f = append(f, checkDocLeak(repoRoot, strict)...)
		f = append(f, checkStatusHotPath(repoRoot)...)
		f = append(f, checkRetiredSurfaceReferences(repoRoot)...)
		f = append(f, checkDistillerDuplicateContract(repoRoot)...)
		f = append(f, checkLLMExtractionGroundingContract(repoRoot)...)
		f = append(f, checkDistillerCandidateShapeContract(repoRoot)...)
		f = append(f, checkRouteOutcomeContract(repoRoot)...)
		f = append(f, checkKnowledgePageContract(repoRoot)...)
		f = append(f, checkWorkPacketContract(repoRoot)...)
		f = append(f, checkWorkProofChainContract(repoRoot)...)
		f = append(f, checkMCPorterTopologyContract(repoRoot)...)
		f = append(f, checkPluginServingContract(repoRoot)...)
		f = append(f, checkMemoryEngineContract(repoRoot)...)
		f = append(f, checkHarnessEntrypointContract(repoRoot)...)
		f = append(f, checkHookSourceControlContract(repoRoot)...)
		f = append(f, checkHookLiveParityContextSpineContract(repoRoot)...)
		f = append(f, checkHookReadinessActionContract(repoRoot)...)
		f = append(f, checkStandingGrantContract(repoRoot)...)
		f = append(f, checkPlacementGuardContract(repoRoot)...)
		f = append(f, checkProductContractContract(repoRoot)...)
		f = append(f, checkProductLoopOpenHypothesisContract(repoRoot)...)
		f = append(f, checkOperatorProductLoopProjectionContract(repoRoot)...)
		f = append(f, checkObservationBatchContract(repoRoot)...)
		f = append(f, checkOperatorObservationReviewProjectionContract(repoRoot)...)
		f = append(f, checkProductObservationVerdictContract(repoRoot)...)
		f = append(f, checkSourceObservationVerdictContract(repoRoot)...)
		f = append(f, checkPanelCapabilityFeedbackContract(repoRoot)...)
		f = append(f, checkCapabilityEvaluationContract(repoRoot)...)
		f = append(f, checkExternalRepoContextEvaluationContract(repoRoot)...)
		f = append(f, checkExternalRepoIntakeStateProjectionContract(repoRoot)...)
		f = append(f, checkConversationMiningContract(repoRoot)...)
		f = append(f, checkConversationCoverageContract(repoRoot)...)
		f = append(f, checkDecisionSourceAuthorityContract(repoRoot)...)
		f = append(f, checkDistillerImprovementReviewContract(repoRoot)...)
		f = append(f, checkEpistemicsDoctrine(repoRoot)...)
		f = append(f, checkPrivateAdaptationSteering(repoRoot)...)
		f = append(f, checkReviewSurfaceCoverageContract(repoRoot)...)
		f = append(f, checkWorkbenchActionCardContract(repoRoot)...)
		f = append(f, checkHomeDoctorOperationalHygieneContract(repoRoot)...)
		f = append(f, checkConversationPreflightImportSafetyContract(repoRoot)...)
		f = append(f, checkOperatorNeedsMeCandidateAuthorityContract(repoRoot)...)
		f = append(f, checkOperatorOrientAdjacentProgressContract(repoRoot)...)
		f = append(f, checkOperatorSourceControlProjectionContract(repoRoot)...)
		f = append(f, checkASCDailySignalsPublicBoundaryContract(repoRoot)...)
	}
	f = append(f, convergenceDebt(strict)...)
	ok := true
	for _, x := range f {
		if x.Severity == Fail {
			ok = false
		}
	}
	return Report{Findings: f, OK: ok}
}

// checkCandidateMap (pure): every kind → one non-empty inbox dir, home.Dirs creates it, and every
// kind declares a consumer (no orphan inbox). This is the B1 invariant, enforced.
func checkCandidateMap() []Finding {
	var f []Finding
	homeDirs := map[string]bool{}
	for _, d := range home.Dirs {
		homeDirs[d] = true
	}
	for _, k := range candidates.AllKinds() {
		spec, _ := candidates.SpecFor(k)
		if spec.InboxDir == "" {
			f = append(f, Finding{"candidate-map", Fail, "kind " + string(k) + " has no inbox dir"})
			continue
		}
		if !homeDirs[spec.InboxDir] {
			f = append(f, Finding{"candidate-map", Fail, "kind " + string(k) + " dir " + spec.InboxDir + " is not created by home.Dirs"})
		}
		if strings.TrimSpace(spec.Consumer) == "" {
			f = append(f, Finding{"candidate-map", Fail, "kind " + string(k) + " has no consumer (orphan inbox)"})
		}
	}
	f = append(f, Finding{"candidate-map", Info, "checked " + itoa(len(candidates.AllKinds())) + " candidate kinds → one home each"})
	return f
}

// checkClassificationTotal: every dir under internal/ must be classified, and every classification
// must name a real dir. An unclassified package fails — a new front door cannot appear silently.
func checkClassificationTotal(repoRoot string) []Finding {
	var f []Finding
	entries, err := os.ReadDir(filepath.Join(repoRoot, "internal"))
	if err != nil {
		return []Finding{{"classification", Warn, "could not read internal/: " + err.Error()}}
	}
	found := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		found[e.Name()] = true
		if _, ok := Classification[e.Name()]; !ok {
			f = append(f, Finding{"classification", Fail, "internal/" + e.Name() + " is UNCLASSIFIED — classify it in arch.Classification (spine/object-family/leaf/mem-target/converge-pending/importer) so it isn't a silent new front door"})
		}
	}
	for pkg := range Classification {
		if !found[pkg] {
			f = append(f, Finding{"classification", Warn, "arch.Classification names internal/" + pkg + " but the dir is gone (retired?) — drop it from the map"})
		}
	}
	f = append(f, checkSpineCoreDrift(Classification, spineCore)...)
	if len(f) == 0 {
		f = append(f, Finding{"classification", Info, "all " + itoa(len(found)) + " internal/ packages classified"})
	}
	return f
}

// checkSpineCoreDrift enforces that Spine membership stays a conscious allowlist: the spineCore set
// and the packages classified Spine must name exactly the same packages. Two Fail directions: a
// package classified Spine with no spineCore entry (someone widened the authoritative spine without
// an allowlist edit), and a spineCore entry not classified Spine (map drift). Pure (map-only) so the
// ratchet is testable without the filesystem.
func checkSpineCoreDrift(classification map[string]Class, core map[string]bool) []Finding {
	var f []Finding
	var unlisted []string
	for pkg, c := range classification {
		if c == Spine && !core[pkg] {
			unlisted = append(unlisted, pkg)
		}
	}
	sort.Strings(unlisted)
	for _, pkg := range unlisted {
		f = append(f, Finding{"classification", Fail, "internal/" + pkg + " is classified Spine but is not in the spineCore allowlist — spine membership is a conscious allowlist edit; classify a new package as object-family/leaf/importer instead of joining the runtime spine"})
	}
	var drift []string
	for pkg := range core {
		if classification[pkg] != Spine {
			drift = append(drift, pkg)
		}
	}
	sort.Strings(drift)
	for _, pkg := range drift {
		f = append(f, Finding{"classification", Fail, "spineCore names internal/" + pkg + " but it is not classified Spine (map drift) — keep spineCore and the Spine classification in lockstep"})
	}
	return f
}

// checkStatusHotPath keeps status.md bounded as a hot map, not a runtime proof journal.
func checkStatusHotPath(repoRoot string) []Finding {
	path := filepath.Join(repoRoot, "docs", "architecture", "status.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return []Finding{{"status-hot-path", Warn, "could not read docs/architecture/status.md: " + err.Error()}}
	}
	lines := lineCount(b)
	if lines > MaxStatusHotPathLines {
		return []Finding{{"status-hot-path", Fail, "docs/architecture/status.md is " + itoa(lines) + " lines; keep the hot status surface at or below " + itoa(MaxStatusHotPathLines) + " lines and move proof journal entries to docs/architecture/archive/"}}
	}
	var journalLines []string
	for i, line := range strings.Split(string(b), "\n") {
		if statusProofJournalLine.MatchString(line) {
			journalLines = append(journalLines, "line "+itoa(i+1))
		}
	}
	if len(journalLines) > 0 {
		return []Finding{{"status-hot-path", Fail, "docs/architecture/status.md contains proof-journal shaped entries (" + strings.Join(journalLines, ", ") + "); keep status as current owner/surface map and move dated proof logs to docs/architecture/archive/"}}
	}
	return []Finding{{"status-hot-path", Info, "docs/architecture/status.md hot path is " + itoa(lines) + "/" + itoa(MaxStatusHotPathLines) + " lines with no proof-journal entries"}}
}

func lineCount(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := strings.Count(string(b), "\n")
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

// checkDocLeak: no operator-private token in tracked docs, and no configured
// product/example leak token in public source fixtures/tests. docs_private is
// gitignored; public core knows scan mechanics, not private names.
func checkDocLeak(repoRoot string, strict bool) []Finding {
	var f []Finding
	privateToks, present := loadPrivateLeakTokens(repoRoot)
	if !present {
		sev := Warn
		if strict {
			sev = Fail
		}
		f = append(f, Finding{"doc-leak", sev, "private leak-token list docs_private/leak-tokens.txt MISSING — operator-token gate DISARMED (only generic path/email shapes are caught); create it (gitignored, one token per line) before any public release"})
	}
	f = append(f, scanLeakRoots(repoRoot, []string{filepath.Join(repoRoot, "docs")}, privateToks, true)...)
	sourceToks, sourcePresent := loadSourceLeakTokens(repoRoot)
	if sourcePresent {
		f = append(f, scanLeakRoots(repoRoot, sourceLeakScanRoots(repoRoot), sourceToks, false)...)
	}
	if len(f) == 0 {
		msg := "public docs clean (generic shapes + private list)"
		if sourcePresent {
			msg = "public docs and configured source surfaces clean (generic shapes + private lists)"
		}
		f = append(f, Finding{"doc-leak", Info, msg})
	}
	return f
}

func loadSourceLeakTokens(repoRoot string) (toks []string, present bool) {
	return loadLeakTokens(filepath.Join(repoRoot, "docs_private", "source-leak-tokens.txt"))
}

func scanLeakRoots(repoRoot string, roots []string, privateToks []string, generic bool) []Finding {
	var f []Finding
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.Contains(path, "docs_private") {
				return nil
			}
			if !leakScanFile(path) {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(repoRoot, path)
			for i, line := range strings.Split(string(b), "\n") {
				if generic {
					if m := genericLeak.FindString(line); m != "" {
						f = append(f, Finding{"doc-leak", Fail, rel + ":" + itoa(i+1) + " leaks " + strconv(m)})
					}
				}
				low := strings.ToLower(line)
				for _, t := range privateToks {
					if strings.Contains(low, t) {
						f = append(f, Finding{"doc-leak", Fail, rel + ":" + itoa(i+1) + " leaks private token " + strconv(t)})
					}
				}
			}
			return nil
		})
	}
	return f
}

func sourceLeakScanRoots(repoRoot string) []string {
	return []string{
		filepath.Join(repoRoot, "fixtures"),
		filepath.Join(repoRoot, "internal"),
		filepath.Join(repoRoot, "cmd"),
		filepath.Join(repoRoot, "tools"),
		filepath.Join(repoRoot, "console"),
	}
}

func leakScanFile(path string) bool {
	ext := filepath.Ext(path)
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".md", ".json", ".jsonl", ".yaml", ".yml", ".toml", ".txt":
		return true
	default:
		return false
	}
}

// checkRetiredSurfaceReferences keeps proven-retired public surfaces from
// reappearing as pending roadmap debt after their replacement loop is proven.
func checkRetiredSurfaceReferences(repoRoot string) []Finding {
	phrases := []string{
		"old flat skill matrix",
		"old skill matrix",
	}
	var hits []string
	hasToolResolverRetirement := false
	hasToolResolverEvidenceOnly := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := strings.ToLower(string(b))
		if strings.Contains(text, "os-tool-resolver-json") || strings.Contains(text, "tool-resolver") {
			hasToolResolverRetirement = hasToolResolverRetirement ||
				(strings.Contains(text, "compound://registries/capability-graph") &&
					(strings.Contains(text, "source_view_authority") || strings.Contains(text, "retired_as_authority")))
			hasToolResolverEvidenceOnly = hasToolResolverEvidenceOnly ||
				(strings.Contains(text, "evidence_only_input") || strings.Contains(text, "fallback/evidence"))
		}
		rel, _ := filepath.Rel(repoRoot, path)
		for i, line := range strings.Split(string(b), "\n") {
			low := strings.ToLower(line)
			for _, phrase := range phrases {
				if strings.Contains(low, phrase) {
					hits = append(hits, rel+":"+itoa(i+1)+" references "+strconv(phrase))
				}
			}
		}
		return nil
	})
	if len(hits) > 0 {
		return []Finding{{"retired-surface", Fail, "retired capability-matrix surface still listed in public docs: " + strings.Join(hits, "; ")}}
	}
	if !hasToolResolverRetirement || !hasToolResolverEvidenceOnly {
		return []Finding{{"retired-surface", Fail, "public docs must state generated tool-resolver authority is retired to compound://registries/capability-graph and remains evidence-only rollback input"}}
	}
	return []Finding{{"retired-surface", Info, "capability graph replaced public old-skill-matrix debt; generated tool-resolver authority retired as evidence-only input"}}
}

// checkPrivateAdaptationSteering keeps the operator-only goal prompt from
// drifting back into holistic OS migration or duplicate runtime prompts. Public
// clones normally do not carry docs_private/plan; absence is not a failure.
func checkPrivateAdaptationSteering(repoRoot string) []Finding {
	planDir := filepath.Join(repoRoot, "docs_private", "plan")
	if st, err := os.Stat(planDir); err != nil || !st.IsDir() {
		return []Finding{{"adaptation-steering", Info, "docs_private/plan absent; public clone uses roadmap fallback"}}
	}

	var failures []string
	canonicalPath := filepath.Join(planDir, "codex-goal-prompt.md")
	rulePath := filepath.Join(planDir, "OPERATING-SYSTEM-ADAPTATION-RULE.md")
	_, canonicalStatErr := os.Stat(canonicalPath)
	_, ruleStatErr := os.Stat(rulePath)
	if os.IsNotExist(canonicalStatErr) && os.IsNotExist(ruleStatErr) {
		return []Finding{{"adaptation-steering", Info, "private adaptation steering files absent; public/packet clone uses roadmap fallback for context-aware OS adaptation"}}
	}
	canonical, err := os.ReadFile(canonicalPath)
	if err != nil {
		failures = append(failures, "missing canonical codex-goal-prompt.md: "+err.Error())
	} else {
		text := strings.ToLower(string(canonical))
		required := []string{
			"## context-aware adaptation rules",
			"context-aware adaptation",
			"not copy/paste",
			"silence condition",
			"proof command",
			"old-owner",
			"public/private boundary",
			"trigger, action, boundary, and proof",
			"consequential bounded move",
			"public compound stays generic",
			"private dogfood adapters feed generic doors",
			"held operator valves",
			"oracle is evidence, not authority",
			"do not report compound as globally blocked",
		}
		failures = append(failures, missingPhrases("codex-goal-prompt.md", text, required)...)
		forbidden := []string{
			"current likely next move",
			"do rebuild asc",
		}
		failures = append(failures, presentPhrases("codex-goal-prompt.md", text, forbidden)...)
		lineCount := strings.Count(string(canonical), "\n") + 1
		if lineCount > 220 {
			failures = append(failures, "codex-goal-prompt.md too long for active steering prompt; keep session archive in CODEX-WORKLOG.md")
		}
		if latestCount := strings.Count(text, "## latest verified state"); latestCount > 1 {
			failures = append(failures, "codex-goal-prompt.md has too many Latest Verified State blocks; keep only current state and move history to CODEX-WORKLOG.md")
		}
	}

	rule, err := os.ReadFile(rulePath)
	if err != nil {
		failures = append(failures, "missing OPERATING-SYSTEM-ADAPTATION-RULE.md: "+err.Error())
	} else {
		text := strings.ToLower(string(rule))
		required := []string{
			"## instruction tuning standard",
			"trigger:",
			"action:",
			"boundary:",
			"silence condition:",
			"proof:",
			"retirement:",
			"only merge and proof count",
		}
		failures = append(failures, missingPhrases("OPERATING-SYSTEM-ADAPTATION-RULE.md", text, required)...)
	}

	pointerPath := filepath.Join(planDir, "codex-goal-prompt-compounding-runtime.md")
	if b, err := os.ReadFile(pointerPath); err == nil {
		text := strings.ToLower(string(b))
		required := []string{
			"this is not a second goal prompt",
			"codex-goal-prompt.md",
			"superseded",
		}
		failures = append(failures, missingPhrases("codex-goal-prompt-compounding-runtime.md", text, required)...)
		if strings.Contains(text, "## current-style candidate moves") || strings.Contains(text, "## goal lanes") {
			failures = append(failures, "codex-goal-prompt-compounding-runtime.md still contains active prompt sections; keep it pointer-only")
		}
	}

	failures = append(failures, duplicateGoalPromptBodies(planDir)...)
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"adaptation-steering", Fail, "private adaptation steering is shallow or duplicated: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"adaptation-steering", Info, "private goal prompt enforces context-aware OS adaptation, consequential bounded moves, generic public/private collector boundary, and duplicate prompt is pointer-only"}}
}

// checkWorkbenchActionCardContract keeps held-valve adjacent actions from
// drifting back into vague "operator required" prose. The Workbench is a
// consumed runtime surface, so operator-gated cards must expose machine-readable
// inputs, validation, and stop gates across JSON, text, tests, and public status.
func checkWorkbenchActionCardContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"internal/work/workbench.go": {
			"RequiredInputs",
			"ExpectedValidation",
			"StopConditions",
			"staleness,omitempty",
			"admit_command,omitempty",
			"retire_command,omitempty",
			"requires_operator",
			"admission_blocked_reason",
			"retire_dry_run_command,omitempty",
			"requires_operator,omitempty",
			"admission_blocked_reason,omitempty",
			"admitCommand = nil",
			"operator-only admission preview",
			"\"--kind\", \"create-plan\"",
			"workbenchAdmissionPreviewStaleAfter",
			"withAdjacentActionGates",
			"RequiresOperator: true",
			"conversation-memory-review-gate",
			"compound\", \"panel\", \"plan\"",
			"\"--target-kind\", \"memory_candidate\"",
			"\"--privacy-sensitive\"",
			"\"--max-items\", \"1\"",
			"no compound memory adjudicate --verdict promote is run",
			"review plan would run adjudication or promotion",
			"RunArtifacts       []WorkbenchRunArtifact",
			"ReviewCards        []WorkbenchReviewCard",
			"GateID",
			"SourceKind",
			"workbenchReviewCards",
			"workbenchOperatorGateCards",
			"RequiresOperator",
			"AdmissionBlockedReason",
			"source_private_config_gate",
			"PromotionSummaryRef",
			"workbenchRunArtifacts",
			"runs\", workID",
			"promotion_summary",
			"TargetRefs   []string",
			"hasSourcePrivateConfigCard",
			"hasMemoryCandidateReviewCard",
			"!hasSourcePrivateConfigCard",
			"!hasMemoryCandidateReviewCard",
		},
		"internal/work/review_card_plan.go": {
			"ReviewCardWorkPlanReportSchemaVersion",
			"BuildReviewCardWorkPlan",
			"BuildReviewCardWorkDraft",
			"workbench_review_card",
			"plan_work_from_workbench_review_card",
			"card.RequiresOperator",
			"do not execute review-card command while creating or admitting work-plan",
			"do not clear the source held WorkEnvelope from this proposal",
		},
		"internal/work/review_card_plan_test.go": {
			"TestReviewCardWorkPlanRecordsASCOperatorGateProposalOnly",
			"TestReviewCardWorkPlanRecordsMemoryCandidateCardWithoutPromotion",
			"admitting review-card proposal must not clear source held work",
			"review-card work-plan should not admit work",
		},
		"internal/work/workbench_test.go": {
			"len(action.RequiredInputs) == 0",
			"len(action.ExpectedValidation) == 0",
			"len(action.StopConditions) == 0",
			"TestWorkbenchMarksOldAdmissionPreviewsStale",
			"stale admission preview",
			"RetireCommand",
			"RetireDryRun",
			"source observation would promote truth",
			"conversation-memory-review-gate",
			"--target-kind memory_candidate",
			"--privacy-sensitive",
			"--max-items 1",
			"compound memory adjudicate --verdict promote is not run",
			"TestWorkbenchSurfacesRunArtifactsForHeldWork",
			"TestWorkbenchSurfacesASCOperatorGateCardForHeldWork",
			"run artifact leaked absolute/private path",
			"promotion summary should preserve zero promoted claims",
			"review card should expose exact parked candidate/panel/promotion boundary",
			"ASC operator gate card should expose exact read-only private-config valve",
		},
		"cmd/compound/workbench_cli.go": {
			"action_required_inputs:",
			"action_expected_validation:",
			"action_stop_conditions:",
			"run_artifacts=%d",
			"run_artifact: %s kind=%s status=%s target=%s count=%s",
			"staleness=%s",
			"admit: %s",
			"retire_dry_run: %s",
			"retire: %s",
			"operator_required: true",
			"blocked_reason:",
		},
		"cmd/compound/work_cli.go": {
			"review-card-plan",
			"BuildReviewCardWorkPlan",
		},
		"cmd/compound/main_test.go": {
			"action_required_inputs:",
			"action_expected_validation:",
			"action_stop_conditions:",
			"staleness=fresh",
			"admit: compound work admit-plan",
			"conversation-memory-review-gate",
			"generic conversation review gate should be suppressed by exact review card",
			"run_artifacts=2",
			"kind=review_card",
			"promotion summary remains zero before explicit promotion",
			"generic ASC preflight should be suppressed by exact review card",
			"generic conversation review gate should be suppressed by exact review card",
			"panel-run-single-prun_cli_fixture.json",
			"TestWorkReviewCardPlanCLIAdmitsCardThroughWorkbenchPreview",
			"workbench_review_card",
		},
		"cmd/compound/orient_operator.go": {
			"MemoryReviewCardCount",
			"MemoryReviewCards",
			"OperatorGateCardCount",
			"OperatorGateCards",
			"orientMemoryReviewCards",
			"orientOperatorGateCards",
			"memory_review_cards:",
			"operator_gate_cards:",
		},
		"cmd/compound/orient_operator_test.go": {
			"TestOrientOperatorSurfacesMemoryReviewCardsFromHeldRunArtifacts",
			"TestOrientOperatorSurfacesASCGateCardsForHeldWork",
			"cand_orient_conversation_review",
			"asc-private-config",
			"promoted=0",
			"operator_required=true",
			"blocked_reason: operator required inputs",
		},
		"docs/architecture/status.md": {
			"required_inputs[]",
			"expected_validation[]",
			"stop_conditions[]",
			"Admission previews carry staleness",
			"admit_command",
			"retire_dry_run_command",
			"retire_command",
			"archive-history --kind create-plan",
			"import_safety",
			"conversation-memory-review-gate",
			"memory_candidate",
			"review-card-plan --card",
			"workbench_review_card",
			"consumed_non_packet_actions",
			"Workbench run-artifact note",
			"`run_artifacts[]`",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		text, err := readContractText(repoRoot, rel)
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, text, phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"workbench-action-card-contract", Fail, "Workbench adjacent action cards lost held-valve gates: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"workbench-action-card-contract", Info, "Workbench operator-gated action cards expose required inputs validation stop conditions through consumed surfaces"}}
}

// checkHomeDoctorOperationalHygieneContract keeps home doctor from degrading
// into text-only hygiene warnings. Dirty operational history must surface exact
// reversible archive-history commands and stop gates without clearing held work.
func checkHomeDoctorOperationalHygieneContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"cmd/compound/main.go": {
			"compound.home_doctor_report.v0.1",
			"OperationalHygieneActions",
			"homeDoctorHygieneAction",
			"operational_hygiene_actions",
			"archive-terminal-work",
			"archive-stale-leases",
			"compound\", \"home\", \"archive-history\", \"--kind\", \"work\"",
			"compound\", \"home\", \"archive-history\", \"--kind\", \"lease\"",
			"workbench held/open work would be cleared",
		},
		"cmd/compound/main_test.go": {
			"home doctor --json should parse",
			"archive-terminal-work",
			"archive-stale-leases",
			"dry_run: compound home archive-history --kind work --dry-run",
			"dry_run: compound home archive-history --kind lease --dry-run",
		},
		"docs/architecture/status.md": {
			"home doctor --json",
			"operational_hygiene_actions",
			"archive-history --kind work|lease",
		},
		"docs/architecture/contracts/README.md": {
			"compound.home_doctor_report.v0.1",
			"operational_hygiene_actions",
			"archive-history --kind work|lease",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		text, err := readContractText(repoRoot, rel)
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, text, phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"home-doctor-operational-hygiene-contract", Fail, "home doctor lost JSON hygiene actions: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"home-doctor-operational-hygiene-contract", Info, "home doctor exposes JSON operational hygiene repair actions through archive-history gates"}}
}

// checkConversationPreflightImportSafetyContract keeps the private
// conversation import preflight from degrading into an export-only smoke test.
// The report itself must prove bounded private evidence/candidates and no temp
// promoted claims before any operator can choose a live import.
func checkConversationPreflightImportSafetyContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"tools/chatgpt_conversation_preflight.ts": {
			"type ImportSafetyReport = {",
			"evidence_count: number;",
			"private_evidence_count: number;",
			"max_imported_quote_chars: number;",
			"candidate_count: number;",
			"private_candidate_count: number;",
			"candidate_trace_refs_match_evidence: boolean;",
			"promoted_claim_count: number;",
			"import_safety: ImportSafetyReport;",
			"function buildImportSafetyReport",
			"imported_evidence_private",
			"imported_evidence_quote_bound",
			"imported_candidates_private",
			"candidate_trace_refs_match_evidence",
			"no_temp_promoted_claims",
		},
		"tools/chatgpt_conversation_preflight.test.ts": {
			"report.import_safety.evidence_count",
			"report.import_safety.private_evidence_count",
			"report.import_safety.max_imported_quote_chars",
			"report.import_safety.candidate_count",
			"report.import_safety.private_candidate_count",
			"report.import_safety.candidate_trace_refs_match_evidence",
			"report.import_safety.promoted_claim_count",
			"imported_evidence_private",
			"candidate_trace_refs_match_evidence",
			"no_temp_promoted_claims",
		},
		"internal/work/workbench.go": {
			"conversation-preflight-private-export",
			"import_safety checks pass",
			"import_safety proves private bounded evidence/candidates and zero temp promoted claims",
		},
		"docs/architecture/status.md": {
			"ChatGPT capture edge note",
			"`tools/chatgpt_conversation_preflight.ts` wraps that edge",
			"`import_safety` privacy/quote/candidate/no-claim checks",
			"Conversation preflight import-safety doctor note",
			"No conversation-specific core package, live import, or promotion path exists.",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		text, err := readContractText(repoRoot, rel)
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, text, phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"conversation-preflight-import-safety-contract", Fail, "conversation preflight lost import_safety proof gates: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"conversation-preflight-import-safety-contract", Info, "conversation preflight import_safety proves private bounded evidence/candidates and zero temp promoted claims"}}
}

// checkOperatorNeedsMeCandidateAuthorityContract keeps `orient --operator`
// needs-me counts aligned with consumed authorities. Memory candidate debt must
// come from the memory candidate authority, because raw inbox files can remain
// proposed after admission classifies them as parked source evidence.
func checkOperatorNeedsMeCandidateAuthorityContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"cmd/compound/orient_operator.go": {
			"memoryCandidateInboxSummary",
			"store.ListCandidates(now, mem.CandidateListOptions{})",
			"if kind == candidates.Memory",
			"candidateStateActionable(item.State)",
		},
		"cmd/compound/orient_operator_test.go": {
			"TestOrientOperatorUsesMemoryCandidateAuthorityForNeedsMe",
			"raw candidate state",
			"authority-parked non-actionable source metadata",
			"memory state_counts[parked]",
		},
		"docs/architecture/status.md": {
			"Operator needs-me candidate authority note",
			"`orient --operator` counts memory candidate debt from `compound://memory/candidates`",
			"raw inbox files can remain `proposed`",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, string(b), phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"operator-needs-me-candidate-authority-contract", Fail, "operator needs-me candidate counts lost consumed authority: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"operator-needs-me-candidate-authority-contract", Info, "operator needs-me memory debt consumes memory candidate authority instead of raw inbox state"}}
}

// checkOperatorOrientAdjacentProgressContract keeps the compact operator front
// door from hiding Workbench adjacent progress when direct work is held on
// operator valves. This is the anti-loop contract for `orient --operator`.
func checkOperatorOrientAdjacentProgressContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"cmd/compound/orient_operator.go": {
			"AdjacentProgress",
			"json:\"adjacent_progress,omitempty\"",
			"AdmissionPreviewCount",
			"AdmissionPreviews",
			"json:\"admission_previews,omitempty\"",
			"orientAdmissionPreviews",
			"workbench_review_card",
			"SessionContext",
			"json:\"session_context,omitempty\"",
			"buildOrientSessionContext",
			"buildOrientAdjacentProgress",
			"orientOpenWorkNextAction",
			"continue held-valve-adjacent Compound work without clearing operator-only valves",
			"held on operator valve; use running.adjacent_progress actions:",
			"HeldWorkIDs",
			"PacketCandidateCount",
			"NonPacketActionIDs",
			"ConsumedNonPacketActionCount",
			"ConsumedNonPacketActionIDs",
			"json:\"consumed_non_packet_action_count,omitempty\"",
			"json:\"consumed_non_packet_action_ids,omitempty\"",
			"SESSION-CONTEXT",
			"admission_previews: %d",
			"source_review_card=%s",
			"[]string{\"compound\", \"orient\", \"--harness\", harness, \"--repo-kind\", repoKind, \"--task\", task}",
			"adjacent_progress: available=%t held=%d non_packet_actions=%d consumed_non_packet_actions=%d packet_candidates=%d",
		},
		"cmd/compound/orient_operator_test.go": {
			"TestOrientOperatorSurfacesHeldValveAdjacentProgress",
			"operator-gate-\" + asc.ID + \"-app-store-connect-private-config",
			"conversation-preflight-private-export",
			"conversation-memory-review-gate",
			"second-opinion-evidence-recovery",
			"codex-pretooluse-wrapper-proof",
			"resolve policy/source-control hold",
			"workbench.adjacent_progress",
			"TestOrientOperatorSurfacesAdmissionPreviewReviewCardContext",
			"admission_previews: 1",
			"action_context=workbench_review_card",
			"SESSION-CONTEXT",
			"compound orient --harness codex --repo-kind control-plane --task continue held-valve-adjacent",
			"adjacent_progress: available=true",
		},
		"cmd/compound/proof_check.go": {
			"recordProofCheckRuntimeArtifact",
			"proof-check-installed-runtime",
			"architecture-doctor-strict",
			"workbench-projection-refresh",
			"codex-pretooluse-wrapper-proof",
			"runProofCheckCodexPreToolUseWrapper",
			"workbench\", \"--json\"",
			"adjacent Workbench action",
			"Do not rerun",
			"artifact_ref,omitempty",
		},
		"cmd/compound/main_test.go": {
			"TestProofCheckArtifactConsumesVerificationAdjacentActions",
			"proof-check-installed-runtime",
			"architecture-doctor-strict",
			"workbench-projection-refresh",
			"codex-pretooluse-wrapper-proof",
			"ConsumedNonPacketActions",
			"ConsumedEvidenceRefs",
		},
		"docs/architecture/status.md": {
			"Operator orientation adjacent-progress note",
			"compound orient --operator --json.running.adjacent_progress",
			"--json.session_context",
			"running.admission_previews[]",
			"workbench_review_card",
			"ContextPack command",
			"proof-producing non-packet actions",
			"proof-check-installed-runtime",
			"consumed_non_packet_actions",
			"open_work[].next_action",
			"Operator orientation adjacent-progress doctor note",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, string(b), phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"operator-orient-adjacent-progress-contract", Fail, "operator orient lost held-valve adjacent-progress projection: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"operator-orient-adjacent-progress-contract", Info, "operator orient surfaces Workbench held-valve adjacent progress through JSON text tests and status"}}
}

func checkOperatorSourceControlProjectionContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"cmd/compound/orient_operator.go": {
			"SourceControl",
			"json:\"source_control,omitempty\"",
			"buildOrientSourceControl",
			"SourceControlCleanupReviewArea",
			"authority.ReadGitSnapshot",
			"head_review_recorded",
			"source-control dirty without a cleanup receipt",
			"source_control: live=%s changed=%d",
			"source_control_review:",
		},
		"cmd/compound/orient_operator_test.go": {
			"TestOrientOperatorCarriesSourceControlReviewProjection",
			"head_review_recorded=true",
			"source-control cleanup reviews",
			"source-control snapshot",
			"inspect cleanup receipts",
		},
		"docs/architecture/status.md": {
			"Operator source-control projection note",
			"`compound orient --operator --json.needs_me.source_control`",
			"reviewed dirty git evidence",
			"does not stage, clean, commit, reset, stash, push, or discard",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, string(b), phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"operator-source-control-projection-contract", Fail, "operator orient lost source-control cleanup projection: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"operator-source-control-projection-contract", Info, "operator orient consumes source-control cleanup reviews plus live git snapshot without mutating repo"}}
}

// checkASCDailySignalsPublicBoundaryContract keeps product-signal source docs
// from drifting back into private collector architecture. App Store Connect
// collection is an edge concern; public Compound owns mapping, validation,
// idempotency, staleness, receipts, and projection.
func checkASCDailySignalsPublicBoundaryContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"docs/operations/asc-daily-signals.md": {
			"Compound does not own App Store Connect fetching",
			"any collector can produce ASC-shaped output",
			"Compound only normalizes",
			"Keep private app IDs, vendor IDs, credentials",
			"The bridge does not call ASC",
			"assume any specific upstream collector",
			"Scheduling is deployment-specific",
		},
		"docs/architecture/status.md": {
			"ASC daily signals public-boundary note",
			"source collector",
			"private adapter",
			"product-signal",
			"apply_results",
			"asc-sales-series-bridge-proof",
			"named private collectors",
		},
		"internal/work/workbench.go": {
			"asc-sales-series-bridge-proof",
			"bun\", \"test\", \"tools/asc_sales_series_bridge.test.ts",
			"temp COMPOUND_HOME bridge apply proof passes",
			"apply_results shows signal_count and transition_count",
			"bridge report lacks apply_results",
		},
		"internal/work/workbench_test.go": {
			"asc-sales-series-bridge-proof",
			"bun test tools/asc_sales_series_bridge.test.ts",
			"apply_results shows signal_count and transition_count",
		},
		"cmd/compound/orient_operator.go": {
			"asc-sales-series-bridge-proof",
		},
		"cmd/compound/orient_operator_test.go": {
			"asc-sales-series-bridge-proof",
		},
		"cmd/compound/main_test.go": {
			"asc-sales-series-bridge-proof",
			"action_command: bun test tools/asc_sales_series_bridge.test.ts",
			"apply_results shows signal_count and transition_count",
		},
		"tools/asc_sales_series_bridge.ts": {
			"type ApplyResult = {",
			"signal_count: number;",
			"transition_count: number;",
			"apply_results",
			"countMatches(stdout, /^# product signal /gm)",
			"countMatches(stdout, /^- transition: /gm)",
		},
		"tools/asc_sales_series_bridge.test.ts": {
			"report.apply_results",
			"signal_count: 1",
			"transition_count: 1",
		},
	}
	forbiddenByFile := map[string][]string{
		"docs/operations/asc-daily-signals.md": {
			"must use launchd",
			"requires launchd",
			"/Users/",
		},
	}

	var failures []string
	for rel, phrases := range requiredByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, string(b), phrases)...)
	}
	for rel, phrases := range forbiddenByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, presentPhrases(rel, string(b), phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"asc-daily-signals-public-boundary-contract", Fail, "ASC daily signal docs lost generic public/private source boundary: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"asc-daily-signals-public-boundary-contract", Info, "ASC daily signal docs keep collectors private/generic while Compound owns product-signal normalization"}}
}

// checkReviewSurfaceCoverageContract keeps review queues from drifting back into
// raw artifact lists. Pending review items must expose whether they have
// decision cards and actions, including missing-affordance counters renderers can
// alert on.
func checkReviewSurfaceCoverageContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"internal/reviewartifact/queue.go": {
			"DecisionCardCount                 int            `json:\"decision_card_count\"`",
			"ActionableCount                   int            `json:\"actionable_count\"`",
			"ReviewRequiredMissingDecisionCard int            `json:\"review_required_missing_decision_card\"`",
			"ReviewRequiredMissingAction       int            `json:\"review_required_missing_action\"`",
			"itemNeedsReviewCard",
		},
		"internal/reviewartifact/reviewartifact_test.go": {
			"DecisionCardCount",
			"ActionableCount",
			"ReviewRequiredMissingDecisionCard",
			"ReviewRequiredMissingAction",
			"missing coverage counts",
		},
		"cmd/compound/main.go": {
			"review_surface decision_cards=",
			"missing_cards=",
			"missing_actions=",
		},
		"cmd/compound/main_test.go": {
			"review_surface decision_cards=",
			"ReviewRequiredMissingDecisionCard",
			"ReviewRequiredMissingAction",
		},
		"docs/architecture/status.md": {
			"Review queue surface-coverage note",
			"decision_card_count",
			"actionable_count",
			"review_required_missing_decision_card",
			"review_required_missing_action",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, string(b), phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"review-surface-coverage-contract", Fail, "Review queue surface lost decision/action coverage gates: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"review-surface-coverage-contract", Info, "review queue reports decision/action coverage and missing-affordance counters through consumed surfaces"}}
}

func missingPhrases(source, text string, phrases []string) []string {
	var out []string
	for _, phrase := range phrases {
		if !strings.Contains(text, phrase) {
			out = append(out, source+" missing "+strconv(phrase))
		}
	}
	return out
}

func readPackageText(repoRoot, relDir string) (string, error) {
	dir := filepath.Join(repoRoot, filepath.FromSlash(relDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func readContractText(repoRoot, rel string) (string, error) {
	if rel == "cmd/compound" {
		return readPackageText(repoRoot, rel)
	}
	b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func presentPhrases(source, text string, phrases []string) []string {
	var out []string
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			out = append(out, source+" still contains stale phrase "+strconv(phrase))
		}
	}
	return out
}

func duplicateGoalPromptBodies(planDir string) []string {
	var out []string
	_ = filepath.WalkDir(planDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		name := filepath.Base(path)
		if name == "codex-goal-prompt.md" {
			return nil
		}
		if !strings.Contains(name, "goal") && !strings.Contains(name, "prompt") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := strings.ToLower(string(b))
		if strings.Contains(text, "# compound goal prompt") || strings.Contains(text, "## goal lanes") || strings.Contains(text, "## context-aware adaptation rules") {
			out = append(out, name+" appears to contain a second active goal prompt")
		}
		return nil
	})
	return out
}

// checkDistillerDuplicateContract keeps the public distiller contract aligned
// with the actual anti-churn path. Duplicate suppression is no longer memory-only:
// non-memory candidate meanings also point at prior_candidate_id and no-promote.
func checkDistillerDuplicateContract(repoRoot string) []Finding {
	mentionsCandidateDuplicate := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(b), "\n") {
			low := strings.ToLower(line)
			if strings.Contains(low, "prior_candidate_id") ||
				strings.Contains(low, "duplicate non-memory") ||
				strings.Contains(low, "duplicate candidate") {
				mentionsCandidateDuplicate = true
			}
		}
		return nil
	})
	if !mentionsCandidateDuplicate {
		return []Finding{{"distiller-contract", Fail, "public docs do not describe non-memory duplicate candidate anti-churn (prior_candidate_id/no-promote)"}}
	}
	return []Finding{{"distiller-contract", Info, "distiller duplicate anti-churn covers memory claims and candidate inboxes"}}
}

// checkLLMExtractionGroundingContract keeps the live/eval extractor contract
// from drifting back to generic model-output parsing. Request-aware extraction
// must require candidate evidence refs grounded in LLMExtractionRequest.source_refs.
func checkLLMExtractionGroundingContract(repoRoot string) []Finding {
	hasRequestGrounded := false
	hasEvidenceRefs := false
	hasSourceRefs := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "request-grounded") {
			hasRequestGrounded = true
		}
		if strings.Contains(low, "evidence_refs") {
			hasEvidenceRefs = true
		}
		if strings.Contains(low, "llmextractionrequest.source_refs") || strings.Contains(low, "source_refs[]") {
			hasSourceRefs = true
		}
		return nil
	})
	if !hasRequestGrounded || !hasEvidenceRefs || !hasSourceRefs {
		return []Finding{{"llm-grounding-contract", Fail, "public docs do not describe request-grounded LLM extraction evidence_refs/source_refs gate"}}
	}
	return []Finding{{"llm-grounding-contract", Info, "LLM extraction contract requires request-grounded evidence refs"}}
}

// checkDistillerCandidateShapeContract keeps candidate extraction from drifting
// back to "kind + confidence" only. A candidate must carry enough typed content
// to be reviewed before it can enter an inbox.
func checkDistillerCandidateShapeContract(repoRoot string) []Finding {
	hasShapeContract := false
	hasMemoryShape := false
	hasNonMemoryShape := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "candidate shape") || strings.Contains(low, "thin candidate") {
			hasShapeContract = true
		}
		if strings.Contains(low, "entity") && strings.Contains(low, "predicate") && strings.Contains(low, "object") {
			hasMemoryShape = true
		}
		if strings.Contains(low, "subject") && strings.Contains(low, "statement") {
			hasNonMemoryShape = true
		}
		return nil
	})
	if !hasShapeContract || !hasMemoryShape || !hasNonMemoryShape {
		return []Finding{{"distiller-shape-contract", Fail, "public docs do not describe candidate shape guard (memory entity/predicate/object; non-memory subject/statement)"}}
	}
	return []Finding{{"distiller-shape-contract", Info, "distiller candidate shape guard covers memory and non-memory candidates"}}
}

// checkRouteOutcomeContract keeps route/cost docs from drifting back to
// metadata-only routing. Route evidence is useful only when joined to Receipt
// outcome/state delta, and failed/unproven outcomes must remain review/no-promote
// material rather than direct router/provider mutations.
func checkRouteOutcomeContract(repoRoot string) []Finding {
	hasOutcomeReview := false
	hasReceipt := false
	hasStateDelta := false
	hasNoPromote := false
	hasNoConfigMutation := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "route outcome-review") || strings.Contains(low, "routeoutcomereview") {
			hasOutcomeReview = true
		}
		if strings.Contains(low, "receipt") {
			hasReceipt = true
		}
		if strings.Contains(low, "state delta") || strings.Contains(low, "state_delta") {
			hasStateDelta = true
		}
		if strings.Contains(low, "nopromotedecision") || strings.Contains(low, "no-promote") {
			hasNoPromote = true
		}
		if strings.Contains(low, "does not mutate provider/gateway/model-router config") ||
			strings.Contains(low, "does not mutate gateway/provider/model-router config") ||
			strings.Contains(low, "no gateway or provider config mutation") {
			hasNoConfigMutation = true
		}
		return nil
	})
	if !hasOutcomeReview || !hasReceipt || !hasStateDelta || !hasNoPromote || !hasNoConfigMutation {
		return []Finding{{"route-outcome-contract", Fail, "public docs do not describe receipt-linked route outcome review with state_delta/no-promote and no provider/gateway/model-router mutation"}}
	}
	return []Finding{{"route-outcome-contract", Info, "route outcome review joins route evidence to receipt state delta before policy review"}}
}

// checkKnowledgePageContract keeps curated-source docs from drifting back to a
// vague "knowledge pages become memory" promise. Page import must stay explicit,
// evidence-backed, candidate-only, and non-promoting.
func checkKnowledgePageContract(repoRoot string) []Finding {
	hasImportPage := false
	hasSchema := false
	hasClaims := false
	hasEvidence := false
	hasNoPromotion := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "memory import-page") {
			hasImportPage = true
		}
		if strings.Contains(low, "compound.knowledge_page.v0.1") {
			hasSchema = true
		}
		if strings.Contains(low, "claims[]") {
			hasClaims = true
		}
		if strings.Contains(low, "evidenceitem") {
			hasEvidence = true
		}
		if strings.Contains(low, "no promoted claims") ||
			strings.Contains(low, "creates no promoted claims") ||
			strings.Contains(low, "does not promote") {
			hasNoPromotion = true
		}
		return nil
	})
	if !hasImportPage || !hasSchema || !hasClaims || !hasEvidence || !hasNoPromotion {
		return []Finding{{"knowledge-page-contract", Fail, "public docs do not describe memory import-page with compound.knowledge_page.v0.1 claims[] -> EvidenceItem/proposed candidates and no promoted claims"}}
	}
	return []Finding{{"knowledge-page-contract", Info, "knowledge-page import stays evidence-backed candidate-only no-promotion"}}
}

// checkWorkPacketContract keeps active/execution packet docs from drifting back
// to pointer-only work. Packet markdown must enter the owned spine as proposed
// work identity, while remaining source evidence rather than proof.
func checkWorkPacketContract(repoRoot string) []Finding {
	hasImportPacket := false
	hasPacketHash := false
	hasProposedWork := false
	hasNoMutation := false
	hasNoProof := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "work import-packet") {
			hasImportPacket = true
		}
		if strings.Contains(low, "packet hash") {
			hasPacketHash = true
		}
		if strings.Contains(low, "proposed work") || strings.Contains(low, "proposed workenvelope") {
			hasProposedWork = true
		}
		if strings.Contains(low, "not mutating the packet") ||
			strings.Contains(low, "no source mutation") ||
			strings.Contains(low, "does not mutate the packet") {
			hasNoMutation = true
		}
		if strings.Contains(low, "not proof") ||
			strings.Contains(low, "not as proof") ||
			strings.Contains(low, "not claiming success") ||
			strings.Contains(low, "does not") && strings.Contains(low, "receipt/transition") && strings.Contains(low, "promote") {
			hasNoProof = true
		}
		return nil
	})
	if !hasImportPacket || !hasPacketHash || !hasProposedWork || !hasNoMutation || !hasNoProof {
		return []Finding{{"packet-work-contract", Fail, "public docs do not describe work import-packet as packet-hash proposed WorkEnvelope source evidence with no packet mutation, proof/receipt/transition, or memory promotion"}}
	}
	return []Finding{{"packet-work-contract", Info, "work packet import stays source-backed proposed work, not proof"}}
}

// checkWorkProofChainContract keeps terminal work from drifting into "closed
// because state says so". Completed/failed work must remain mechanically tied to
// readable Receipt and Transition envelopes with the same work_id.
func checkWorkProofChainContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"internal/work/proof_chain_doctor.go": {
			"ProofChainDoctor",
			"MissingReceipt",
			"MissingTransition",
			"MismatchedWorkID",
			"transition_missing_receipt_ref",
			"terminalProofRequired",
			"rec.Validate()",
			"tr.Validate()",
		},
		"cmd/compound/work_cli.go": {
			"proof-chain-doctor",
			"ProofChainDoctorOptions",
			"--include-archived",
		},
		"internal/work/proof_chain_doctor_test.go": {
			"TestProofChainDoctorReportsCleanTerminalWork",
			"TestProofChainDoctorFailsTerminalWorkMissingReceiptTransition",
			"TestProofChainDoctorFailsMismatchedTerminalWorkID",
		},
		"cmd/compound/work_proof_chain_doctor_test.go": {
			"TestWorkProofChainDoctorCLIJSON",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			failures = append(failures, rel+" missing: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, string(b), phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"work-proof-chain-contract", Fail, "terminal work proof-chain doctor drifted: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"work-proof-chain-contract", Info, "terminal work proof-chain doctor verifies receipt/transition existence, validity, and work_id linkage"}}
}

// checkMCPorterTopologyContract keeps real MCP catalog ingestion from drifting
// back to normalized/generated topology only. mcporter catalogs are observations:
// broken/auth/offline servers do not surface, and the source becomes
// SourceObservation/no-promote evidence without executing tools or mutating MCP
// configuration.
func checkMCPorterTopologyContract(repoRoot string) []Finding {
	hasImportTopology := false
	hasMCPorterJSON := false
	hasNonSurfacing := false
	hasNoPromote := false
	hasNoExecution := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "capability import-topology") {
			hasImportTopology = true
		}
		if strings.Contains(low, "mcporter list --json") || strings.Contains(low, "mcporter-json") {
			hasMCPorterJSON = true
		}
		if strings.Contains(low, "non-surfacing") ||
			strings.Contains(low, "auth/error/offline") ||
			strings.Contains(low, "auth_required") {
			hasNonSurfacing = true
		}
		if strings.Contains(low, "sourceobservation") &&
			(strings.Contains(low, "nopromotedecision") || strings.Contains(low, "no-promote")) {
			hasNoPromote = true
		}
		if strings.Contains(low, "never executes") ||
			strings.Contains(low, "none of these commands execute") ||
			(strings.Contains(low, "does not execute") && strings.Contains(low, "mutate")) {
			hasNoExecution = true
		}
		return nil
	})
	if !hasImportTopology || !hasMCPorterJSON || !hasNonSurfacing || !hasNoPromote || !hasNoExecution {
		return []Finding{{"mcporter-topology-contract", Fail, "public docs do not describe mcporter list --json topology import with non-surfacing auth/error/offline servers, SourceObservation/no-promote, and no MCP execution/config mutation"}}
	}
	return []Finding{{"mcporter-topology-contract", Info, "mcporter catalog import stays observational non-executing capability graph evidence"}}
}

// checkPluginServingContract keeps plugin enable-state as inventory evidence,
// not an executable capability offer. Plugin adapters may be observed by the
// graph, but they must stay non-surfacing and never mutate harness configs.
func checkPluginServingContract(repoRoot string) []Finding {
	hasPluginBoard := false
	hasRetiredAdapter := false
	hasNoPromote := false
	hasNoSurface := false
	hasNoExecution := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "os-plugin-board-json") || strings.Contains(low, "skills-selection-board.json") {
			hasPluginBoard = true
		}
		if strings.Contains(low, "retired `adapter`") || strings.Contains(low, "retired adapter") {
			hasRetiredAdapter = true
		}
		if strings.Contains(low, "sourceobservation") && (strings.Contains(low, "nopromotedecision") || strings.Contains(low, "no-promote")) {
			hasNoPromote = true
		}
		if strings.Contains(low, "non-surfacing") || strings.Contains(low, "zero `adapter:plugin:*` offers") {
			hasNoSurface = true
		}
		if strings.Contains(low, "non-executing") ||
			strings.Contains(low, "must not surface plugin adapters") ||
			strings.Contains(low, "execute plugins") ||
			strings.Contains(low, "mutate harness/plugin configs") {
			hasNoExecution = true
		}
		return nil
	})
	if !hasPluginBoard || !hasRetiredAdapter || !hasNoPromote || !hasNoSurface || !hasNoExecution {
		return []Finding{{"plugin-serving-contract", Fail, "public docs do not describe OS plugin board import as retired adapter inventory with SourceObservation/no-promote, non-surfacing offers, and no plugin execution/config mutation"}}
	}
	return []Finding{{"plugin-serving-contract", Info, "plugin serving inventory stays non-surfacing capability graph evidence"}}
}

// checkMemoryEngineContract keeps rented memory engines observable without
// granting them authority. A configured sidecar must be visible through normal
// memory doctor health, leave ledger bytes unchanged, and stay behind the
// MemoryEngine/engine-compare adapter seam.
func checkMemoryEngineContract(repoRoot string) []Finding {
	hasMemoryDoctor := false
	hasConfiguredSidecar := false
	hasSyncEmbedding := false
	hasLedgerUnchanged := false
	hasNoAuthority := false
	hasCompare := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "memory doctor") {
			hasMemoryDoctor = true
		}
		if strings.Contains(low, "compound_mem_engine_bin") || strings.Contains(low, "configured sidecar") {
			hasConfiguredSidecar = true
		}
		if strings.Contains(low, "sync/embedding") {
			hasSyncEmbedding = true
		}
		if strings.Contains(low, "ledger_unchanged") || strings.Contains(low, "ledger bytes unchanged") || strings.Contains(low, "leaves owned ledger bytes unchanged") {
			hasLedgerUnchanged = true
		}
		if strings.Contains(low, "never authority") || strings.Contains(low, "without making it authority") || strings.Contains(low, "engine never owns") {
			hasNoAuthority = true
		}
		if strings.Contains(low, "engine-compare") {
			hasCompare = true
		}
		return nil
	})
	if !hasMemoryDoctor || !hasConfiguredSidecar || !hasSyncEmbedding || !hasLedgerUnchanged || !hasNoAuthority || !hasCompare {
		return []Finding{{"memory-engine-contract", Fail, "public docs do not describe configured MemoryEngine health through memory doctor sync/embedding, ledger_unchanged proof, engine-compare swap parity, and never-authority semantics"}}
	}
	return []Finding{{"memory-engine-contract", Info, "configured MemoryEngine health stays observable rented ledger-safe"}}
}

// checkHarnessEntrypointContract prevents OS-style entrypoint registries from
// drifting into full capability authority. Entrypoint files prove context
// entrypoints, not hook blocking, receipts, skills, or MCP support.
func checkHarnessEntrypointContract(repoRoot string) []Finding {
	hasHarnessObserve := false
	hasOSEntrypoints := false
	hasSessionStartOnly := false
	hasNoPromote := false
	hasNoHookInstall := false
	hasExplicitCapabilityRegistry := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "compound harness observe") {
			hasHarnessObserve = true
		}
		if strings.Contains(low, "--os-entrypoints") {
			hasOSEntrypoints = true
		}
		if strings.Contains(low, "only session-start context") ||
			strings.Contains(low, "only `session_start_context`") ||
			strings.Contains(low, "infer only `session_start_context`") {
			hasSessionStartOnly = true
		}
		if strings.Contains(low, "sourceobservation") &&
			(strings.Contains(low, "nopromotedecision") || strings.Contains(low, "no-promote")) {
			hasNoPromote = true
		}
		if strings.Contains(low, "not hook installation") ||
			strings.Contains(low, "does not install hooks") ||
			strings.Contains(low, "without installing hooks") {
			hasNoHookInstall = true
		}
		if strings.Contains(low, "explicit normalized capability registry") ||
			strings.Contains(low, "require an explicit normalized capability registry") {
			hasExplicitCapabilityRegistry = true
		}
		return nil
	})
	if !hasHarnessObserve || !hasOSEntrypoints || !hasSessionStartOnly || !hasNoPromote || !hasNoHookInstall || !hasExplicitCapabilityRegistry {
		return []Finding{{"harness-entrypoint-contract", Fail, "public docs do not describe --os-entrypoints as session-start-only SourceObservation/no-promote evidence with no hook install and no inferred hook/MCP/skill capability"}}
	}
	return []Finding{{"harness-entrypoint-contract", Info, "OS harness entrypoints stay evidence-only session-start context"}}
}

// checkHookSourceControlContract keeps hook mutation gating from drifting back to
// raw harness path strings. PreToolUse must normalize relative targets before
// policy evaluation, persisted ActionIntent artifacts, and read-only git
// inspection; otherwise one action can have two target identities.
func checkHookSourceControlContract(repoRoot string) []Finding {
	hasPreToolUse := false
	hasNormalizeRelative := false
	hasPolicyEvaluation := false
	hasActionPersistence := false
	hasGitInspection := false
	hasNoMutation := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "hook pre-tool-use") || strings.Contains(low, "pretooluse") {
			hasPreToolUse = true
		}
		if strings.Contains(low, "relative") && strings.Contains(low, "target") &&
			(strings.Contains(low, "normalize") || strings.Contains(low, "cwd")) {
			hasNormalizeRelative = true
		}
		if strings.Contains(low, "policy evaluation") {
			hasPolicyEvaluation = true
		}
		if strings.Contains(low, "actionintent") && (strings.Contains(low, "persistence") || strings.Contains(low, "stored")) {
			hasActionPersistence = true
		}
		if strings.Contains(low, "source-control inspection") || strings.Contains(low, "git snapshot") || strings.Contains(low, "read-only git") {
			hasGitInspection = true
		}
		if strings.Contains(low, "inspected repos are not mutated") || strings.Contains(low, "without mutating the repo") ||
			strings.Contains(low, "no cleanup/staging/push authority") {
			hasNoMutation = true
		}
		return nil
	})
	if !hasPreToolUse || !hasNormalizeRelative || !hasPolicyEvaluation || !hasActionPersistence || !hasGitInspection || !hasNoMutation {
		return []Finding{{"hook-source-control-contract", Fail, "public docs do not describe hook pre-tool-use relative target normalization before policy evaluation, ActionIntent persistence, read-only git inspection, and no repo mutation"}}
	}
	return []Finding{{"hook-source-control-contract", Info, "hook source-control gate normalizes target identity before policy and git inspection"}}
}

// checkHookLiveParityContextSpineContract keeps live hook parity from becoming
// a status-only log. SessionStart parity must cite the generated context spine
// artifacts so harness-native context can be traced back to owned Compound
// state.
func checkHookLiveParityContextSpineContract(repoRoot string) []Finding {
	hasDocs := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "hook-live-parity-context-spine-contract") &&
			strings.Contains(low, "--parity-log") &&
			strings.Contains(low, "context request") &&
			strings.Contains(low, "effective contract") &&
			strings.Contains(low, "context pack") &&
			strings.Contains(low, "injection") {
			hasDocs = true
		}
		return nil
	})

	hookBytes, err := os.ReadFile(filepath.Join(repoRoot, "cmd", "compound", "hook_live.go"))
	if err != nil {
		return []Finding{{"hook-live-parity-context-spine-contract", Warn, "could not read cmd/compound/hook_live.go: " + err.Error()}}
	}
	hookText := string(hookBytes)
	hasRecordFields := strings.Contains(hookText, "type liveHookParityRecord struct") &&
		strings.Contains(hookText, "ContextRequestID") &&
		strings.Contains(hookText, "ContextRequestRef") &&
		strings.Contains(hookText, "ContractID") &&
		strings.Contains(hookText, "ContractRef") &&
		strings.Contains(hookText, "ContextPackID") &&
		strings.Contains(hookText, "ContextPackRef") &&
		strings.Contains(hookText, "InjectionRef")
	hasInjectionWrite := strings.Contains(hookText, "record.ContextRequestID = res.Injection.ContextRequestID") &&
		strings.Contains(hookText, "record.ContractRef = res.Injection.ContractRef") &&
		strings.Contains(hookText, "record.ContextPackRef = res.Injection.ContextPackRef") &&
		strings.Contains(hookText, "record.InjectionRef = res.Injection.Ref")

	testBytes, err := os.ReadFile(filepath.Join(repoRoot, "cmd", "compound", "hook_live_test.go"))
	if err != nil {
		return []Finding{{"hook-live-parity-context-spine-contract", Warn, "could not read cmd/compound/hook_live_test.go: " + err.Error()}}
	}
	testText := string(testBytes)
	hasRegression := strings.Contains(testText, "TestHookLiveSessionStartParityRecordsContextSpineRefs") &&
		strings.Contains(testText, "context_request") &&
		strings.Contains(testText, "effective_contract") &&
		strings.Contains(testText, "context_pack") &&
		strings.Contains(testText, "os.Stat(filepath.Join(homePath, ref))")

	if !hasDocs || !hasRecordFields || !hasInjectionWrite || !hasRegression {
		return []Finding{{"hook-live-parity-context-spine-contract", Fail, "live hook parity must document and test SessionStart context-spine refs: liveHookParityRecord context request/effective contract/context pack/injection refs, injection writeback, and temp-home artifact proof"}}
	}
	return []Finding{{"hook-live-parity-context-spine-contract", Info, "live hook parity records context spine refs with artifact proof"}}
}

// checkHookReadinessActionContract keeps hook readiness rows actionable without
// letting agents flip hook registration. Missing live-adapter rows must carry
// fixture proof and operator-only shim actions with verification/rollback text.
func checkHookReadinessActionContract(repoRoot string) []Finding {
	statusBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs", "architecture", "status.md"))
	if err != nil {
		return []Finding{{"hook-readiness-action-contract", Warn, "could not read docs/architecture/status.md: " + err.Error()}}
	}
	status := strings.ToLower(string(statusBytes))
	hasDocs := strings.Contains(status, "hook-readiness-action-contract") &&
		strings.Contains(status, "codex sessionstart/userpromptsubmit") &&
		strings.Contains(status, "ready") &&
		strings.Contains(status, "contextpack") &&
		strings.Contains(status, "effectivecontract") &&
		strings.Contains(status, "prove_fixture_hook") &&
		strings.Contains(status, "prove_live_wrapper_gate") &&
		strings.Contains(status, "expected_exit_code") &&
		strings.Contains(status, "operator_live_adapter_required") &&
		strings.Contains(status, "compound contract hooks-authority") &&
		strings.Contains(status, "before any registration flip")

	contractBytes, err := os.ReadFile(filepath.Join(repoRoot, "internal", "contract", "contract.go"))
	if err != nil {
		return []Finding{{"hook-readiness-action-contract", Warn, "could not read internal/contract/contract.go: " + err.Error()}}
	}
	contractText := string(contractBytes)
	envelopeBytes, err := os.ReadFile(filepath.Join(repoRoot, "internal", "envelope", "envelope.go"))
	if err != nil {
		return []Finding{{"hook-readiness-action-contract", Warn, "could not read internal/envelope/envelope.go: " + err.Error()}}
	}
	envelopeText := string(envelopeBytes)
	hasSource := strings.Contains(contractText, "hookRuntimeReadinessActions") &&
		strings.Contains(envelopeText, "type HookReadinessAction struct") &&
		strings.Contains(envelopeText, "Actions         []HookReadinessAction") &&
		strings.Contains(contractText, "envelopeHookReadinessActions") &&
		strings.Contains(contractText, "hookReadinessAction") &&
		strings.Contains(contractText, "prove_fixture_hook") &&
		strings.Contains(contractText, "ExpectedExitCode") &&
		strings.Contains(contractText, "prove_live_wrapper_gate") &&
		strings.Contains(contractText, "codexPreToolUseLiveWrapperProofCommand") &&
		strings.Contains(contractText, "operator_live_adapter_required") &&
		strings.Contains(contractText, "compound contract hooks-authority") &&
		strings.Contains(contractText, "must not flip registration")

	contractTestBytes, err := os.ReadFile(filepath.Join(repoRoot, "internal", "contract", "contract_test.go"))
	if err != nil {
		return []Finding{{"hook-readiness-action-contract", Warn, "could not read internal/contract/contract_test.go: " + err.Error()}}
	}
	contractTest := string(contractTestBytes)
	hasContractTest := strings.Contains(contractTest, "TestInstalledHookRuntimeReadinessAddsMissingAdapterActions") &&
		strings.Contains(contractTest, "prove_fixture_hook") &&
		strings.Contains(contractTest, "ExpectedExitCode") &&
		strings.Contains(contractTest, "prove_live_wrapper_gate") &&
		strings.Contains(contractTest, "findEnvelopeHookAction") &&
		strings.Contains(contractTest, "context pack hook readiness missing live wrapper action") &&
		strings.Contains(contractTest, "operator_live_adapter_required") &&
		strings.Contains(contractTest, "compound hook session-start --live --harness codex")
	workbenchBytes, err := os.ReadFile(filepath.Join(repoRoot, "internal", "work", "workbench.go"))
	if err != nil {
		return []Finding{{"hook-readiness-action-contract", Warn, "could not read internal/work/workbench.go: " + err.Error()}}
	}
	workbenchText := string(workbenchBytes)
	hasWorkbenchAction := strings.Contains(workbenchText, "codex-pretooluse-wrapper-proof") &&
		strings.Contains(workbenchText, "compound hook pre-tool-use --live --harness codex") &&
		strings.Contains(workbenchText, "no registration flip")
	proofCheckBytes, err := os.ReadFile(filepath.Join(repoRoot, "cmd", "compound", "proof_check.go"))
	if err != nil {
		return []Finding{{"hook-readiness-action-contract", Warn, "could not read cmd/compound/proof_check.go: " + err.Error()}}
	}
	proofCheckText := string(proofCheckBytes)
	hasProofCheck := strings.Contains(proofCheckText, "codex-pretooluse-wrapper-proof") &&
		strings.Contains(proofCheckText, "runProofCheckCodexPreToolUseWrapper") &&
		strings.Contains(proofCheckText, "COMPOUND_HOME=")

	cliTestBytes, err := os.ReadFile(filepath.Join(repoRoot, "cmd", "compound", "main_test.go"))
	if err != nil {
		return []Finding{{"hook-readiness-action-contract", Warn, "could not read cmd/compound/main_test.go: " + err.Error()}}
	}
	cliTest := string(cliTestBytes)
	hasCLITest := strings.Contains(cliTest, `findHookReadiness(authorityReport.HookReadiness, "codex", "SessionStart")`) &&
		strings.Contains(cliTest, `findHookReadiness(authorityReport.HookReadiness, "codex", "UserPromptSubmit")`) &&
		strings.Contains(cliTest, `got.Status != "ready"`) &&
		strings.Contains(cliTest, `findHookGateReadiness(authorityReport.PreToolUseReadiness, "codex")`) &&
		strings.Contains(cliTest, `"missing_gate"`) &&
		strings.Contains(cliTest, `"prove_live_wrapper_gate"`) &&
		strings.Contains(cliTest, "codex-pretooluse-wrapper-proof") &&
		strings.Contains(cliTest, "ExpectedExitCode")

	if !hasDocs || !hasSource || !hasContractTest || !hasWorkbenchAction || !hasProofCheck || !hasCLITest {
		return []Finding{{"hook-readiness-action-contract", Fail, "hook readiness must prove Codex context hooks ready/quiet and keep missing-live-adapter rows exposing prove_fixture_hook, prove_live_wrapper_gate, codex-pretooluse-wrapper-proof, expected_exit_code, and operator_live_adapter_required action cards with exact commands, proof-check consumption, hooks-authority verification, rollback text, source tests, CLI tests, and public status guard"}}
	}
	return []Finding{{"hook-readiness-action-contract", Info, "hook readiness proves Codex context hooks ready while missing-live-adapter rows expose fixture, live wrapper, and operator-only shim actions"}}
}

// checkStandingGrantContract keeps autonomy grants from drifting back to
// unconditional allow-list entries. Grants must describe a scoped envelope and
// leave final enforcement in PolicyDecision/Gate.
func checkStandingGrantContract(repoRoot string) []Finding {
	hasStandingGrant := false
	hasScopeBudget := false
	hasBlastReversibility := false
	hasTargetCostFields := false
	hasLeavingEnvelope := false
	hasPolicyGate := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "standinggrant") || strings.Contains(low, "standing grant") {
			hasStandingGrant = true
		}
		if strings.Contains(low, "scope") && strings.Contains(low, "budget") {
			hasScopeBudget = true
		}
		if (strings.Contains(low, "blast_radius") || strings.Contains(low, "blast-radius")) && strings.Contains(low, "reversibility") {
			hasBlastReversibility = true
		}
		if strings.Contains(low, "target_path") && (strings.Contains(low, "estimated_cost") || strings.Contains(low, "max_usd") || strings.Contains(low, "max_tokens")) {
			hasTargetCostFields = true
		}
		if strings.Contains(low, "leaving") && (strings.Contains(low, "escalate") || strings.Contains(low, "hold") || strings.Contains(low, "blocked")) {
			hasLeavingEnvelope = true
		}
		if (strings.Contains(low, "policydecision") || strings.Contains(low, "policy decision")) &&
			(strings.Contains(low, "gate") || strings.Contains(low, "adapter rejects") || strings.Contains(low, "enforces")) {
			hasPolicyGate = true
		}
		return nil
	})
	if !hasStandingGrant || !hasScopeBudget || !hasBlastReversibility || !hasTargetCostFields || !hasLeavingEnvelope || !hasPolicyGate {
		return []Finding{{"standing-grant-contract", Fail, "public docs do not describe scoped StandingGrant envelopes with target_path/budget/blast/reversibility and PolicyDecision/Gate enforcement when leaving the envelope"}}
	}
	return []Finding{{"standing-grant-contract", Info, "StandingGrant remains a scoped policy envelope enforced by PolicyDecision/Gate"}}
}

func checkPlacementGuardContract(repoRoot string) []Finding {
	hasPlacementGuard := false
	hasPolicyContext := false
	hasModes := false
	hasConfidenceBlock := false
	hasOpenWriteOverride := false
	hasFailOpen := false
	hasGateEnforce := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "placementguard") || strings.Contains(low, "placement guard") {
			hasPlacementGuard = true
		}
		if (strings.Contains(low, "policydecision") || strings.Contains(low, "policy decision")) && strings.Contains(low, "context") {
			hasPolicyContext = true
		}
		if strings.Contains(low, "observe") && strings.Contains(low, "warn") && strings.Contains(low, "block") {
			hasModes = true
		}
		if strings.Contains(low, "confidence:block") && (strings.Contains(low, "hard-hold") || strings.Contains(low, "hard hold") || strings.Contains(low, "hold")) {
			hasConfidenceBlock = true
		}
		if strings.Contains(low, "open_write") && strings.Contains(low, "placement_override") {
			hasOpenWriteOverride = true
		}
		if strings.Contains(low, "fail open") || strings.Contains(low, "fail-open") {
			hasFailOpen = true
		}
		if strings.Contains(low, "hook pre-tool-use --enforce") && (strings.Contains(low, "gate exit") || strings.Contains(low, "exit code")) {
			hasGateEnforce = true
		}
		return nil
	})
	if !hasPlacementGuard || !hasPolicyContext || !hasModes || !hasConfidenceBlock || !hasOpenWriteOverride || !hasFailOpen || !hasGateEnforce {
		return []Finding{{"placement-guard-contract", Fail, "public docs do not describe PlacementGuard as PolicyDecision context with observe/warn/block, confidence:block threshold, OPEN_WRITE/PLACEMENT_OVERRIDE fail-open behavior, and Gate exit enforcement"}}
	}
	return []Finding{{"placement-guard-contract", Info, "PlacementGuard remains PolicyDecision context with staged fail-open enforcement"}}
}

// checkProductContractContract keeps project/release source contracts from
// becoming product analytics or strategy authority. They may register product
// identity and deterministic surfaces/channels/privacy, but signals,
// hypotheses, causality, and actions must enter through product signal/review.
func checkProductContractContract(repoRoot string) []Finding {
	hasProjectContractImport := false
	hasReleaseImport := false
	hasDeterministicFields := false
	hasNoAnalytics := false
	hasNoCausality := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "product import") && strings.Contains(low, "--project-contract") {
			hasProjectContractImport = true
		}
		if strings.Contains(low, "--release") || strings.Contains(low, "release contract") {
			hasReleaseImport = true
		}
		if strings.Contains(low, "deterministic") &&
			strings.Contains(low, "identity") &&
			strings.Contains(low, "surface") &&
			strings.Contains(low, "channel") &&
			strings.Contains(low, "privacy") {
			hasDeterministicFields = true
		}
		if strings.Contains(low, "never analytics signals") ||
			strings.Contains(low, "no analytics signal") ||
			strings.Contains(low, "does not create") && strings.Contains(low, "signals") {
			hasNoAnalytics = true
		}
		if strings.Contains(low, "never") && strings.Contains(low, "causality") ||
			strings.Contains(low, "without causal attribution") ||
			strings.Contains(low, "does not declare causality") {
			hasNoCausality = true
		}
		return nil
	})
	if !hasProjectContractImport || !hasReleaseImport || !hasDeterministicFields || !hasNoAnalytics || !hasNoCausality {
		return []Finding{{"product-contract-contract", Fail, "public docs do not describe product --project-contract/--release as deterministic identity/surface/channel/privacy intake only, with no analytics signals or causality"}}
	}
	return []Finding{{"product-contract-contract", Info, "product project/release contracts stay deterministic non-causal intake"}}
}

// convergenceDebt reports the two-stack memory debt as a tracked, visible Info finding —
// the coherent treatment when a duplicate cannot yet be retired (Law 9: make it observable).
// checkCapabilityEvaluationContract keeps scored capability-evaluation records
// from drifting into capability offers or promotion authority. Evaluations
// critique already-known graph nodes; they do not mint executable skills.
// checkProductLoopOpenHypothesisContract keeps product loops from becoming a
// status-only list of hypothesis ids. Open hypotheses must carry the measurement
// and next-action contract so a future agent can advance the loop.
func checkProductLoopOpenHypothesisContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"internal/product/loops.go": {
			"OpenHypothesesByMissingSignalSource",
			"SignalsWithoutHypothesis",
			"LoopSignalHypothesisGap",
			"ClosedReviewsByNextAction",
			"ActionCards",
			"[]LoopActionCard",
			"type LoopActionCard struct",
			"validateLoopActionCard",
			"loopProductActionConsumptionIndex",
			"ConsumedByWorkIDs",
			"product_action_card:",
			"ListWithArchived",
			"product_signal_import",
			"product_signal_hypothesis_review",
			"product_review_plan",
			"product_next_bounded_move",
			"signalWithoutHypothesisActionCard",
			"Metrics           []string `json:\"metrics,omitempty\"`",
			"ObservationWindow string   `json:\"observation_window,omitempty\"`",
			"NextAction        string   `json:\"next_action,omitempty\"`",
			"EvidenceRefs      []string `json:\"evidence_refs,omitempty\"`",
			"Metrics:           compact(h.Metrics)",
			"NextAction:        h.NextAction",
		},
		"cmd/compound/product_loops.go": {
			"renderLoopMissingSignalSources",
			"renderLoopSignalHypothesisGaps",
			"renderLoopReviewNextActions",
			"renderLoopActionCards",
			"open_hypotheses_by_missing_signal_source",
			"signals_without_hypothesis",
			"closed_reviews_by_next_action",
			"action_cards",
			"consumed=true",
			"metrics=",
			"window=",
			"next=",
			"renderLoopHypotheses",
		},
		"internal/product/loops_test.go": {
			"open hypothesis should expose measurement/review contract",
			"searchHypothesis.Metrics[0] != \"gsc_clicks\"",
			"searchHypothesis.NextAction",
			"TestDryRunDerivedHypothesisClosesAfterSignalReview",
			"BuildOutboundDryRunEvidencePlan",
			"RecordOutboundDryRunEvidencePlan",
			"ApplyOutboundDryRunEvidenceReview",
			"BuildReviewPlan",
			"findLoopMissingSignalSource",
			"findLoopSignalHypothesisGap",
			"findLoopReviewNextAction",
			"findLoopActionCard",
			"TestBuildLoopModelSuppressesConsumedClosedReviewActionCard",
			"seedCompletedProductActionWork",
			"consumed closed review must not remain in top-level action cards",
			"product_signal_import",
			"product_signal_hypothesis_review",
			"product_review_plan",
			"product_next_bounded_move",
			"signal-without-hypothesis group should expose reviewed synthesis card",
			"reviewed hypothesis should leave open set",
			"loop history should retain applied review and signal ref",
		},
		"cmd/compound/product_loops_test.go": {
			"TestProductLoopsTextPrintsOpenHypothesisContract",
			"metrics=distribution_draft_quality",
			"next=wait for real distribution signal evidence before review",
			"open_hypotheses_by_missing_signal_source",
			"product_signal_import",
			"command=compound product signal",
		},
		"cmd/compound/main_test.go": {
			"cmdProduct([]string{\"loops\", \"--json\", \"--product\", \"product:focus_notes\"})",
			"reviewed hypothesis should leave open_hypotheses",
			"product loops should retain review history with signal ref",
			"product loops should group closed review by next action",
			"TestProductActionPlanAndReviewCLIExposeActionCardsThroughReviewQueue",
			"product.ProductActionReviewArea",
			"action-plan",
			"action-review",
			"action-work-plan",
			"action-work-plan should not admit work",
			"admit-plan",
			"admit-plan should not mutate product timeline",
			"admission_previews=1",
			"runnable action context",
			"prepare ready with reviewed action context",
			"advance-plan text missing action context",
			"claim-plan text missing action context",
			"observe-run text missing action context",
			"action-plan text missing receipt-ready boundary",
			"act --plan did not record planned action",
		},
		"internal/product/action_plan.go": {
			"ProductActionReviewArea",
			"BuildActionPlan",
			"RecordActionPlan",
			"ApplyActionReview",
			"ActionWorkDraft",
			"BuildActionWorkDraft",
			"ExpectedValidation",
			"StopConditions",
			"LoopActionCard",
			"loop.SignalsWithoutHypothesis",
			"product_signal_change_event:",
			"product_signal:",
			"do not run the action command while generating this work-plan preview",
			"does not mutate ProductSignal",
			"does not admit action card into WorkEnvelope execution",
		},
		"internal/product/action_plan_test.go": {
			"TestActionPlanMakesLoopActionCardReviewableWithoutProductMutation",
			"ProductActionReviewArea",
			"product_next_bounded_move",
			"product_signal_hypothesis_review",
			"product_signal:sig_loop_unmatched",
			"BuildActionWorkDraft",
			"action work draft should preserve command/boundary/proof/evidence",
			"action review must not mutate product loop ledgers",
			"recorded action review should make queue terminal",
		},
		"internal/work/workbench.go": {
			"AdmissionPreviews",
			"workbenchAdmissionPreviews",
			"create-plans",
			"workbenchCreatePlanIsAdmissionPreview",
			"prepare ready with reviewed action context",
		},
		"internal/work/workbench_test.go": {
			"TestWorkbenchSurfacesAdmissionPreviewsWithoutAdmittingWork",
			"create-plan should not write work lifecycle events",
			"admission preview must not become dispatchable work",
			"admitted work should retire admission preview",
		},
		"internal/work/create_plan.go": {
			"WorkActionContext",
			"AdmitCreatePlanReport",
			"AdmitCreatePlanPath",
			"AdmitCreatePlan",
			"validateCreatePlanForAdmission",
			"admitted_from_create_plan",
			"create_plan:",
		},
		"internal/work/create_plan_test.go": {
			"TestAdmitCreatePlanCreatesWorkAndRetiresPreview",
			"TestCreatePlanActionContextReachesWorkbenchAndDispatch",
			"admitted work should retire admission preview",
			"dispatch candidate missing action context",
			"ready advance-plan",
			"claim-plan",
			"observe-run",
			"verifying workbench",
			"repeat admission should be idempotent",
		},
		"internal/work/advance_plan.go": {
			"ActionContext",
			"actionContextForWork",
		},
		"internal/work/claim_plan.go": {
			"ActionContext",
			"actionContextForWork",
		},
		"internal/work/run_observation.go": {
			"ActionContext",
			"actionContextForWork",
		},
		"internal/work/action_execution_plan.go": {
			"ActionExecutionPlan",
			"ReceiptDraft",
			"ActionCommand",
			"RequiredInputs",
			"ExpectedValidation",
			"StopConditions",
			"Boundary",
			"Proof",
			"RecordCommand",
			"CloseCommand",
			"close-plan",
			"--action-plan",
			"does not execute action_context command",
		},
		"internal/work/close_plan.go": {
			"ActionPlanPath",
			"ActionExecutionPlanID",
			"ReceiptDraft",
			"ExpectedValidation",
			"not recorded",
			"LoadActionExecutionPlan",
		},
		"internal/work/action_execution_plan_test.go": {
			"TestActionExecutionPlanConsumesActionContextWithoutExecuting",
			"TestActionExecutionPlanRequiresClaimedOrRunningWork",
			"receipt draft missing action proof boundary",
			"action execution planning mutated work",
		},
		"internal/work/close_plan_test.go": {
			"TestClosePlanConsumesRecordedActionExecutionPlanReceiptDraft",
			"not recorded",
			"ReceiptDraft",
			"ExpectedValidation",
		},
		"cmd/compound/main.go": {
			"action-work-plan",
			"product.BuildActionWorkDraft",
			"workStore.CreatePlan",
			"plan_work_from_reviewed_product_action",
		},
		"cmd/compound/work_cli.go": {
			"action-plan",
			"ActionContext",
			"action_context:",
			"ActionExecutionPlan",
			"LoadActionExecutionPlan",
			"record_after_execution",
			"close_after_validation",
			"--action-plan",
			"action_execution_plan:",
			"expected_validation:",
			"plan.ActionContext",
			"result.ActionContext",
			"admit-plan",
			"store.AdmitCreatePlanPath",
		},
		"cmd/compound/workbench_cli.go": {
			"admission_previews=",
			"admission_preview:",
		},
		"internal/product/review_queue.go": {
			"ProductActionReviewArea",
			"annotateProductActionReviewItem",
			"compound\", \"product\", \"action-review\"",
			"record_product_action_review",
		},
		"docs/architecture/status.md": {
			"`compound product loops`",
			"open hypotheses",
			"metrics/window/next-action contract",
			"missing signal source",
			"closed reviews by next action",
			"action cards",
			"product_signal_import",
			"product_review_plan",
			"product_next_bounded_move",
			"dry-run-derived hypothesis",
			"leaves open_hypotheses",
			"ProductSignal",
			"Product action-card status note",
			"`compound product action-plan --product <id> --card <card_id>`",
			"`reviews/product-action-cards`",
			"`compound product action-review <review.yaml>`",
			"`compound product action-work-plan --reviewed <reviewed.json>`",
			"`compound work admit-plan --plan <create-plan.json>`",
			"`action_context`",
			"`action_summary`",
			"review-gate `product action-plan` command is not completion proof",
			"`work advance-plan <id> --to ready`",
			"`claim-plan`",
			"`observe-run`",
			"`work action-plan <id>`",
			"`work act <id> --plan <action-execution-plan.json>`",
			"`work close-plan <id> --action-plan <action-execution-plan.json>`",
			"receipt-close path",
			"does not execute the reviewed action command",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, string(b), phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"product-loop-open-hypothesis-contract", Fail, "product loop open hypotheses lost measurement/next-action contract: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"product-loop-open-hypothesis-contract", Info, "product loops expose open hypothesis metrics/window/next-action contracts"}}
}

// checkOperatorProductLoopProjectionContract keeps the operator front door from
// drifting back to work-only status while product loops already expose evidence
// debt. `orient --operator` must consume the product read model, not recreate it.
func checkOperatorProductLoopProjectionContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"cmd/compound/orient_operator.go": {
			"ProductLoops",
			"*orientProductLoops",
			"json:\"product_loops,omitempty\"",
			"buildOrientProductLoops",
			"product.BuildLoopModel",
			"OpenHypothesesByMissingSignalSource",
			"ClosedReviewsByNextAction",
			"ActionCardCount",
			"orientLoopActionCard",
			"orientLoopActionCards",
			"product_loops: products=%d open_hypothesis_debt=%d closed_review_actions=%d action_cards=%d",
			"compound\", \"product\", \"loops\"",
		},
		"cmd/compound/orient_operator_test.go": {
			"TestOrientOperatorCarriesProductLoopEvidenceDebtProjection",
			"seedOrientProductLoopProjectionHome",
			"missing_signal_source=posthog",
			"closed_review_next=keep screenshot one more window",
			"product_signal_import",
			"product_next_bounded_move",
			"action_cards=",
			"compound product action-plan",
		},
		"docs/architecture/status.md": {
			"Operator product-loop projection note",
			"`compound orient --operator --json.product_loops`",
			"`product.BuildLoopModel` read model",
			"action cards",
			"`compound product action-plan` review gate",
			"`compound product loops` as drill-down",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, string(b), phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"operator-product-loop-projection-contract", Fail, "operator orient lost product-loop evidence debt projection: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"operator-product-loop-projection-contract", Info, "operator orient consumes product loop evidence-debt projection"}}
}

// checkObservationBatchContract keeps external platforms behind one generic
// observation grammar instead of letting provider-specific connector concepts
// become core authority.
func checkObservationBatchContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"internal/observation/observation.go": {
			"compound.observation_batch.v0.1",
			"RecordSourceIntakeReport",
			"IntakeProjectionCommand",
			"RouteProductSignal",
			"RouteKnowledgeExport",
			"RouteTraceEnvelope",
			"RouteRouteEvidence",
			"RouteSourceObservation",
			"RouteCandidateOnly",
			"RouteBlockedPrivate",
			"product.AdaptSignalExport",
			"product.ApplySignal",
			"ApplySourceObservations",
			"ApplyKnowledge",
			"ApplyRouteEvidence",
			"ApplyEvents",
			"SourceObservationDocument",
			"sourceobs.Apply",
			"SourceObservationApplyResult",
			"KnowledgeExportDocument",
			"knowledge.Import",
			"KnowledgeApplyResult",
			"RouteEvidenceDocument",
			"routeevidence.RecordJoined",
			"RouteEvidenceApplyResult",
			"EventSourceRow",
			"eventsource.ImportOSEvents",
			"EventApplyResult",
		},
		"internal/observation/intake.go": {
			"compound.source_intake_projection.v0.1",
			"compound://sources/intake",
			"LoadSourceIntakeProjection",
			"RecordSourceIntakeReport",
			"SourceIntakeActionCard",
			"ActionCardCount",
			"RequiredInputs",
			"ExpectedValidation",
			"StopConditions",
			"AppliedIDs",
			"BlockedRows",
			"SourceCapturedAt",
			"NextCommand",
			"knowledge_extraction_pending",
			"sourceIntakeOpenKnowledgeExtractionIDs",
			"compound\", \"knowledge\", \"extract\", \"--item\"",
			"requested action would run bulk --pending extraction",
			"sourceIntakeRows",
		},
		"internal/observation/intake_work.go": {
			"compound.source_intake_work_plan_report.v0.1",
			"BuildSourceIntakeWorkPlan",
			"BuildSourceIntakeWorkDraft",
			"source_intake_action_card",
			"work.CreatePlan",
			"WorkActionContext",
			"RequiredInputs",
			"ExpectedValidation",
			"StopConditions",
			"do not execute the source-intake action command while creating or admitting this work-plan",
		},
		"internal/observation/review.go": {
			"ObservationReviewArea",
			"observation-candidates",
			"CandidateReviewResult",
			"candidates.CapabilityObs",
			"panel.SelectLensesWithContext",
			"reviewartifact.DecisionCard",
			"compound panel plan --target-kind capability_observation",
			"no truth promotion",
		},
		"cmd/compound": {
			"source observe-batch",
			"source intake",
			"intake-work-plan",
			"--dry-run",
			"--apply-product-signals",
			"--apply-source-observations",
			"--apply-knowledge",
			"--apply-route-evidence",
			"--apply-events",
			"--review-candidates",
			"LoadSourceIntakeProjection",
			"BuildSourceIntakeWorkPlan",
			"observation.AnnotateReviewQueue",
		},
		"cmd/compound/orient_operator.go": {
			"SourceIntake",
			"buildOrientSourceIntake",
			"ActionCardCount",
			"ActionCards",
			`"compound", "source", "intake", "--json"`,
			"LoadSourceIntakeProjection",
		},
		"cmd/compound/main_test.go": {
			"TestSourceIntakeCLIAndOrientConsumeRecordedObservationBatches",
			"TestSourceIntakeWorkPlanCLIAdmitsActionCardThroughWorkbenchPreview",
			"TestSourceObserveBatchCLIAppliesGeneratedSourceObservation",
			"TestSourceObserveBatchCLIAppliesContentToKnowledge",
			"TestSourceObserveBatchCLIAppliesRouteEvidence",
			"TestSourceObserveBatchCLIAppliesEventsToTraceEnvelope",
			"generated-system-source.json",
			"content-knowledge-export.json",
			"route-evidence.json",
			"event-trace-envelope.json",
			"product-signals.jsonl",
			"TestSourceObserveBatchCLIRecordsCandidateReviewMaterial",
			"writeObservationPanelFixtureResponses",
			"cmdPanel([]string{\"run\", \"--plan\"",
			"\"panel-verdicts\"",
		},
		"internal/observation/observation_test.go": {
			"TestSourceIntakeProjectionSummarizesRecordedObservationBatches",
			"TestSourceIntakeProjectionBuildsActionCardsForRowsNeedingAttention",
			"TestSourceIntakeActionCardBuildsWorkCreatePlanContext",
			"knowledge_extraction_pending",
			"done extraction should not keep source-intake card",
			"stale_source",
			"skipped_route",
			"blocked_private_input",
			"unsupported_kind",
			"LoadSourceIntakeProjection",
			"RouteBlockedPrivate",
			"AppliedIDs",
			"BlockedRows",
		},
		"fixtures/observation/generated-system-source.json": {
			"generated_view",
			"compound_projection",
			"obs_fixture_generated_workbench",
		},
		"fixtures/observation/content-knowledge-export.json": {
			"web_capture",
			"research_note",
			"obs_fixture_content_knowledge",
		},
		"fixtures/observation/route-evidence.json": {
			"route_gateway",
			"execution_route",
			"obs_fixture_route_evidence",
		},
		"fixtures/observation/event-trace-envelope.json": {
			"event_stream",
			"workflow:observation-intake",
			"obs_fixture_event_trace",
		},
		"docs/architecture/status.md": {
			"ObservationBatch status note",
			"`compound source observe-batch <batch.json> --dry-run --json`",
			"`compound.observation_batch.v0.1`",
			"generic observation grammar",
			"ProductSignal",
			"KnowledgeExport",
			"TraceEnvelope",
			"ExecutionRouteEvidence",
			"SourceObservation",
			"`--apply-knowledge`",
			"`--apply-events`",
			"`compound source intake --json`",
			"`compound.source_intake_projection.v0.1`",
			"`knowledge_extraction_pending`",
			"`compound knowledge extract --item`",
			"`compound source intake-work-plan --card <card_id> --json`",
			"`work admit-plan`",
			"`action-plan`",
			"`close-plan`",
			"`compound orient --operator --json.needs_me.source_intake`",
			"`eventsource.ImportOSEvents`",
			"`knowledge.Import`",
			"extraction requests",
			"`--apply-source-observations`",
			"`sourceobs.Apply`",
			"`NoPromoteDecision`",
			"candidate/review material",
			"`--review-candidates`",
			"`compound reviews --area observation-candidates --json`",
			"`compound panel plan --target-kind capability_observation`",
			"`compound panel run --plan <run_id> --fixtures <dir> --json`",
			"`panel-verdicts`",
			"no external account",
			"no credentials",
		},
		"docs/architecture/contracts/README.md": {
			"ObservationBatch contract note",
			"`--apply-source-observations`",
			"`--apply-knowledge`",
			"`knowledge.Import`",
			"`--apply-route-evidence`",
			"`routeevidence.RecordJoined`",
			"`--apply-events`",
			"`compound source intake --json`",
			"`compound.source_intake_projection.v0.1`",
			"`compound source intake-work-plan --card <card_id> --json`",
			"`work admit-plan`",
			"`action-plan`",
			"`close-plan`",
			"`eventsource.ImportOSEvents`",
			"`sourceobs.Apply`",
			"`NoPromoteDecision`",
			"does not mutate ProductSignal except the explicit product-signal mode",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		text, err := readContractText(repoRoot, rel)
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, text, phrases)...)
	}
	for _, rel := range []string{
		"fixtures/observation/gsc-product-metric.json",
		"fixtures/observation/asc-product-metric.json",
		"fixtures/observation/social-distribution-feedback.json",
		"fixtures/observation/support-comment.json",
	} {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); err != nil {
			failures = append(failures, rel+" missing")
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"observation-batch-contract", Fail, "ObservationBatch generic seam lost: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"observation-batch-contract", Info, "ObservationBatch routes external payloads through existing meaning owners"}}
}

func checkOperatorObservationReviewProjectionContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"cmd/compound/orient_operator.go": {
			"ObservationReviews",
			"json:\"observation_reviews,omitempty\"",
			"buildOrientObservationReviews",
			"observation.ObservationReviewArea",
			"panel-verdicts",
			"candidates.CapabilityObs",
			"observation_reviews: candidate_reviews=%d panel_verdicts=%d total=%d",
		},
		"cmd/compound/orient_operator_test.go": {
			"TestOrientOperatorCarriesObservationReviewDebtProjection",
			"observe-batch",
			"--review-candidates",
			"observation_reviews: candidate_reviews=1 panel_verdicts=1 total=2",
			"observation candidates: compound reviews --area observation-candidates",
			"observation panel verdicts: compound reviews --area panel-verdicts",
		},
		"docs/architecture/status.md": {
			"Operator observation-review projection note",
			"`compound orient --operator --json.needs_me.observation_reviews`",
			"`observation-candidates`",
			"`panel-verdicts`",
			"`compound reviews --area observation-candidates`",
			"`compound reviews --area panel-verdicts`",
			"lens output evidence/candidate material",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, string(b), phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"operator-observation-review-projection-contract", Fail, "operator orient lost observation review debt projection: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"operator-observation-review-projection-contract", Info, "operator orient consumes observation candidate and panel verdict review debt projection"}}
}

func checkProductObservationVerdictContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"internal/product/observation_verdict.go": {
			"ObservationVerdictReviewArea",
			"product-observation-verdicts",
			"BuildObservationVerdictPlan",
			"panel.LoadVerdictArtifact",
			"ApplyObservationVerdictReview",
			"create_hypothesis_candidate",
			"record_product_observation_verdict_review",
			"writeHypothesisCandidate",
			"does not mutate ProductSignal ledger",
			"does not apply panel verdicts",
		},
		"internal/product/review_queue.go": {
			"ObservationVerdictReviewArea",
			"annotateObservationVerdictItem",
		},
		"internal/product/observation_verdict_test.go": {
			"TestObservationVerdictPlanCreatesReviewGatedProductHypothesisMaterial",
			"product_observation_verdict_review:",
			"ledger\", \"product-signals.jsonl",
		},
		"cmd/compound/main.go": {
			"observation-verdict-plan",
			"BuildObservationVerdictPlan",
			"RecordObservationVerdictPlan",
			"observation-verdict-review",
			"ApplyObservationVerdictReview",
		},
		"cmd/compound/main_test.go": {
			"TestSourceObserveBatchCLIRecordsCandidateReviewMaterial",
			"observation-verdict-plan",
			"observation-verdict-review",
			"record_product_observation_verdict_review",
			"product-signals.jsonl",
		},
		"docs/architecture/status.md": {
			"Product observation-verdict bridge note",
			"`compound product observation-verdict-plan --verdict <panel-verdict.json>`",
			"`compound reviews --area product-observation-verdicts --json`",
			"`compound product observation-verdict-review <review.yaml>`",
			"parked product hypothesis candidate",
			"does not mutate ProductSignal",
		},
		"docs/architecture/contracts/README.md": {
			"ProductObservationVerdict",
			"`product-observation-verdicts`",
			"panel verdict evidence",
			"parked `product_hypothesis` candidate",
			"does not apply panel verdicts",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		b, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, string(b), phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"product-observation-verdict-contract", Fail, "product consumer for observation panel verdicts lost: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"product-observation-verdict-contract", Info, "product consumes observation panel verdict evidence through review-gated hypothesis material"}}
}

func checkSourceObservationVerdictContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"internal/sourceobs/observation_verdict.go": {
			"SourceObservationVerdictReviewArea",
			"source-observation-verdicts",
			"BuildSourceObservationVerdictPlan",
			"panel.LoadVerdictArtifact",
			"ApplySourceObservationVerdictReview",
			"RecordNoPromote",
			"record_source_observation_verdict_review",
			"does not promote memory or source truth",
			"does not mutate ProductSignal ledger",
		},
		"internal/sourceobs/sourceobs_test.go": {
			"TestSourceObservationVerdictPlanRecordsNoPromoteEvidence",
			"TestSourceObservationVerdictPlanParksUnsupportedNoOwnerVerdict",
			"ledger\", \"claims.jsonl",
			"ledger\", \"product-signals.jsonl",
		},
		"cmd/compound": {
			"source observation-verdict-plan",
			"BuildSourceObservationVerdictPlan",
			"RecordSourceObservationVerdictPlan",
			"source observation-verdict-review",
			"ApplySourceObservationVerdictReview",
			"sourceobs.AnnotateObservationVerdictQueue",
		},
		"cmd/compound/main_test.go": {
			"TestSourceObservationVerdictCLIRecordsNoPromoteEvidence",
			"source-observation-verdicts",
			"record_source_observation_verdict_review",
			"product-signals.jsonl",
		},
		"docs/architecture/status.md": {
			"Source observation-verdict bridge note",
			"`compound source observation-verdict-plan --verdict <panel-verdict.json>`",
			"`compound reviews --area source-observation-verdicts --json`",
			"`compound source observation-verdict-review <review.yaml>`",
			"source-owned `NoPromoteDecision`",
			"unsupported no-owner parked evidence",
			"do not apply panel verdicts",
		},
		"docs/architecture/contracts/README.md": {
			"SourceObservationVerdict",
			"`source-observation-verdicts`",
			"source-owned `NoPromoteDecision`",
			"unsupported no-owner parked evidence",
			"does not apply panel verdicts",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		text, err := readContractText(repoRoot, rel)
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, text, phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"source-observation-verdict-contract", Fail, "source consumer for non-product observation panel verdicts lost: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"source-observation-verdict-contract", Info, "sourceobs consumes non-product observation panel verdict evidence through no-promote review material"}}
}

func checkPanelCapabilityFeedbackContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"internal/panel/capability_feedback.go": {
			"PanelCapabilityFeedbackReviewArea",
			"capability-panel-feedback",
			"BuildPanelCapabilityFeedbackPlan",
			"RecordPanelCapabilityFeedbackPlan",
			"RecordPanelCapabilityFeedbackReview",
			"AnnotateCapabilityFeedbackQueue",
			"panelCapabilityFeedbackEdgeKind",
			"capgraph.EdValidatedBy",
			"capgraph.EdMissedWhen",
			"capgraph.EdFalseTriggeredBy",
			"lens:anti-pattern-operational-detector",
			"redirect context",
			"review artifact only; no graph mutation during plan",
			"feedback-review is the only command that may write CapabilityGraph feedback edges",
		},
		"fixtures/panel/lens-manifest.yaml": {
			"anti-pattern-operational-detector",
			"A valid detector has trigger, redirect behavior, consumer path, proof/guard, boundary, silence condition, and retirement/no-promote outcome.",
			"reviewed capability-feedback path can learn validated_by, missed_when, or false_triggered_by",
		},
		"internal/panel/lens_test.go": {
			"anti-pattern-detectors",
			"lens:anti-pattern-operational-detector",
			"want six included and ten non-memory excluded",
			"TestPanelCapabilityFeedbackReviewAppliesGraphEdgesOnlyAfterReview",
			"TestAntiPatternDetectorVerdictFeedsCapabilityFeedbackPlan",
			"TestAntiPatternDetectorFindingPreservesOperationalRedirectEvidence",
			"anti_pattern:trigger",
			"anti_pattern:retirement",
			"want missed_when detector candidate",
			"feedback plan must not mutate capability graph edges",
		},
		"cmd/compound/panel.go": {
			"feedback-plan --verdict",
			"feedback-review <review.yaml>",
			"BuildPanelCapabilityFeedbackPlan",
			"RecordPanelCapabilityFeedbackReview",
		},
		"cmd/compound/workbench_cli.go": {
			"panel.AnnotateCapabilityFeedbackQueue",
		},
		"cmd/compound/panel_test.go": {
			"TestPanelFeedbackPlanReviewCLIFeedsCapabilityGraph",
			"PanelCapabilityFeedbackReviewArea",
			"EdgeKindCounts[capgraph.EdValidatedBy]",
			"lens:anti-pattern-operational-detector",
		},
		"cmd/compound/orient_operator.go": {
			"CapabilityFeedback",
			"BuildPanelCapabilityFeedbackImpact",
			"capability_feedback: pending=%d applied_reviews=%d applied_edges=%d",
			"Label: \"capability authority\"",
		},
		"cmd/compound/orient_operator_test.go": {
			"TestOrientOperatorCarriesReviewedCapabilityFeedbackImpact",
			"capability_feedback: pending=0 applied_reviews=1 applied_edges=1",
			"compound reviews --area capability-panel-feedback",
			"compound capability authority",
		},
		"docs/architecture/status.md": {
			"Panel capability-feedback note",
			"`compound panel feedback-plan --verdict <panel-verdict.json>`",
			"`compound reviews --area capability-panel-feedback --json`",
			"`compound panel feedback-review <review.yaml>`",
			"review-gated CapabilityGraph feedback",
			"`anti-pattern-operational-detector`",
		},
		"docs/architecture/contracts/README.md": {
			"Panel capability feedback note",
			"`compound panel feedback-plan --verdict <panel-verdict.json>`",
			"`capability-panel-feedback`",
			"`compound panel feedback-review <review.yaml>`",
			"does not auto-mutate CapabilityGraph",
			"`anti-pattern-operational-detector`",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		text, err := readContractText(repoRoot, rel)
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, text, phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"panel-capability-feedback-contract", Fail, "panel verdict capability feedback path lost: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"panel-capability-feedback-contract", Info, "panel verdicts feed reviewed CapabilityGraph feedback, anti-pattern detector findings, and operator orient exposes reviewed impact"}}
}

func checkCapabilityEvaluationContract(repoRoot string) []Finding {
	hasImportEvaluations := false
	hasEvidenceOnly := false
	hasEvaluatedBy := false
	hasNoSurface := false
	hasNoPromote := false
	hasReviewYAML := false
	hasReviewCommand := false
	hasEditedDecisionGate := false
	hasReviewedManifest := false
	hasReviewNoMutation := false
	hasReviewMutationClaim := false
	hasImportSkillsBoundary := false

	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "capability import-evaluations") {
			hasImportEvaluations = true
		}
		if strings.Contains(low, "evidence-only") && strings.Contains(low, "eval") {
			hasEvidenceOnly = true
		}
		if strings.Contains(low, "evaluated_by") {
			hasEvaluatedBy = true
		}
		if strings.Contains(low, "do not surface") ||
			strings.Contains(low, "never surface") ||
			strings.Contains(low, "records never retire/execute/surface") ||
			strings.Contains(low, "surface eval records as offers") {
			hasNoSurface = true
		}
		if strings.Contains(low, "sourceobservation") && (strings.Contains(low, "nopromotedecision") || strings.Contains(low, "no-promote")) {
			hasNoPromote = true
		}
		if strings.Contains(low, "review yaml") || strings.Contains(low, "review_yaml") || strings.Contains(low, ".review.yaml") {
			hasReviewYAML = true
		}
		if strings.Contains(low, "capability evaluation-review") {
			hasReviewCommand = true
		}
		if strings.Contains(low, "edited decision") && strings.Contains(low, "rationale") && strings.Contains(low, "confidence") {
			hasEditedDecisionGate = true
		}
		if strings.Contains(low, "reviewed manifest") {
			hasReviewedManifest = true
		}
		if (strings.Contains(low, "evaluation-review") || strings.Contains(low, "review yaml")) &&
			(strings.Contains(low, "without mutating") || strings.Contains(low, "does not mutate") || strings.Contains(low, "no graph mutation")) {
			hasReviewNoMutation = true
		}
		if strings.Contains(low, "evaluation-review mutates") ||
			strings.Contains(low, "evaluation-review updates the graph") ||
			strings.Contains(low, "evaluation-review installs") {
			hasReviewMutationClaim = true
		}
		if strings.Contains(low, "only `import-skills` mutates") ||
			strings.Contains(low, "only import-skills mutates") ||
			(strings.Contains(low, "import-skills") && strings.Contains(low, "mutates the graph")) {
			hasImportSkillsBoundary = true
		}
		return nil
	})
	if !hasImportEvaluations || !hasEvidenceOnly || !hasEvaluatedBy || !hasNoSurface || !hasNoPromote {
		return []Finding{{"capability-evaluation-contract", Fail, "public docs do not describe capability import-evaluations as evidence-only evaluated_by/no-promote records that never surface as agent offers"}}
	}
	if !hasReviewYAML || !hasReviewCommand || !hasEditedDecisionGate || !hasReviewedManifest || !hasReviewNoMutation || !hasImportSkillsBoundary {
		return []Finding{{"capability-evaluation-contract", Fail, "public docs do not describe capability evaluation-review as gated review YAML producing reviewed manifests without graph mutation; import-skills must remain the graph mutation boundary"}}
	}
	if hasReviewMutationClaim {
		return []Finding{{"capability-evaluation-contract", Fail, "public docs claim capability evaluation-review mutates graph state; import-skills must remain the graph mutation boundary"}}
	}
	return []Finding{{"capability-evaluation-contract", Info, "capability evaluations stay evidence-only; review YAML gates activation before import-skills mutates graph offers"}}
}

func checkExternalRepoContextEvaluationContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"internal/capgraph/external_repo_intake.go": {
			"ExternalRepoContextEvaluationStep",
			"EvaluationPlan",
			"externalRepoContextEvaluationPlan",
			"matchExternalRepoDecisionsByContext",
			"ExternalRepoMatchedEvidenceForTask",
			"external_repo_review_evidence",
			"existing-owner-fit",
			"capability-shape-fit",
			"risk-cost-fit",
			"Context-Aware Evaluation Plan",
			"silence:",
			"proof:",
			"retirement:",
		},
		"internal/capgraph/external_repo_review.go": {
			"externalRepoIntakeDecisionCard",
			"evaluation lens: ",
			"evaluation_step_count",
			"no clone/install/serve/auth/graph mutation/memory promotion",
			"externalRepoIntakeDecisionChoices",
		},
		"internal/capgraph/external_repo_evaluation_plan.go": {
			"contextPlan := externalRepoContextEvaluationPlan(report.Repo, match)",
			"externalRepoContextEvaluationSignals(contextPlan)",
			"context_evaluation_lenses",
			"context_evaluation_proofs",
			"context_evaluation_retirement",
			"context_evaluation_step_count",
		},
		"internal/capgraph/evaluation_plan.go": {
			"signalStringSlice(eval.Signals, \"context_evaluation_lenses\")",
			"context lens ",
			"silence condition reviewed",
			"context evaluation proof: ",
		},
		"internal/capgraph/evaluation_review_queue.go": {
			"signalStringSlice(plan.Signals, \"context_evaluation_lenses\")",
			"context_evaluation_silence",
			"context_evaluation_retirement",
			"indexedSignal",
			"evaluation lens: ",
		},
		"internal/capgraph/outbound_distribution_plan.go": {
			"OutboundDistributionPlanAuthorityRef",
			"BuildOutboundDistributionPlan",
			"evidence_only",
			"tier3_public_distribution_policy",
			"product_loop_target",
			"dry_run_receipt",
			"no_social_auth_or_posting",
			"VerdictPath",
			"Boundary",
		},
		"internal/capgraph/outbound_distribution_plan_test.go": {
			"TestOutboundDistributionPlanConsumesMatchedExternalRepoEvidence",
			"gitroomhq/postiz-app",
			"Panniantong/Agent-Reach",
			"cobusgreyling/loop-engineering",
			"no_distribution_evidence",
			"no_social_auth_or_posting",
			"TestOutboundDistributionVerdictPlanRecordsReviewArtifactsWithoutMutation",
			"ReviewRequiredMissingDecisionCard",
			"outbound-distribution-verdict-review",
		},
		"internal/capgraph/outbound_distribution_verdict.go": {
			"OutboundDistributionVerdictPlanSchemaVersion",
			"BuildOutboundDistributionVerdictPlan",
			"RecordOutboundDistributionVerdictPlan",
			"RecordOutboundDistributionVerdictReview",
			"AnnotateOutboundDistributionVerdictQueue",
			"dry_run_receipt_ref",
			"adopt_for_dry_run",
			"does not mutate ProductSignal ledgers",
			"no social auth/post/reply/spend/public side effect",
		},
		"internal/capgraph/outbound_distribution_dry_run.go": {
			"OutboundDistributionDryRunReceiptSchemaVersion",
			"BuildOutboundDistributionDryRunReceipt",
			"RecordOutboundDistributionDryRunReceipt",
			"RecordOutboundDistributionDryRunReview",
			"AnnotateOutboundDistributionDryRunQueue",
			"public side effects/auth/external requests/posting/spend",
			"does not mutate product signals",
			"accept_as_product_loop_evidence",
		},
		"internal/capgraph/outbound_distribution_dry_run_test.go": {
			"TestOutboundDistributionDryRunReceiptConsumesAdoptedVerdictWithoutProductMutation",
			"TestOutboundDistributionDryRunReceiptRejectsPublicSideEffects",
			"public_side_effects",
			"ReviewRequired",
		},
		"internal/product/outbound_dry_run.go": {
			"OutboundDryRunEvidencePlanSchemaVersion",
			"BuildOutboundDryRunEvidencePlan",
			"RecordOutboundDryRunEvidencePlan",
			"ApplyOutboundDryRunEvidenceReview",
			"AnnotateOutboundDryRunEvidenceQueue",
			"does not mutate ProductSignal ledger",
			"does not publish or authenticate social accounts",
			"create_hypothesis_candidate",
		},
		"internal/capgraph/external_repo_intake_test.go": {
			"item.EvaluationPlan",
			"hasExternalRepoEvaluationStep",
			"TestExternalRepoIntakeReportMatchesTaskContextEvidence",
			"harness-fit",
			"DecisionCard.Meta",
			"evaluation lens: harness-fit",
		},
		"cmd/compound/main.go": {
			"ExternalRepoMatchedEvidenceForTask",
			"outbound-dry-run-plan",
			"outbound-dry-run-review",
		},
		"cmd/compound/capability.go": {
			"report.MatchedEvidence",
			"outbound-distribution-plan",
			"outbound-distribution-verdict-plan",
			"outbound-distribution-verdict-review",
			"outbound-distribution-dry-run-receipt",
			"outbound-distribution-dry-run-review",
			"BuildOutboundDistributionPlan",
			"BuildOutboundDistributionVerdictPlan",
			"report.RequiredGates",
		},
		"cmd/compound/workbench_cli.go": {
			"AnnotateOutboundDistributionVerdictQueue",
			"AnnotateOutboundDistributionDryRunQueue",
			"AnnotateOutboundDryRunEvidenceQueue",
		},
		"cmd/compound/main_test.go": {
			"TestCapabilitySurfaceReportCarriesExternalRepoEvidenceWithoutOfferingIt",
			"matched external repo evidence",
			"must not become an offer",
			"TestCapabilityOutboundDistributionPlanJSON",
			"TestCapabilityOutboundDistributionVerdictPlanAndReviewCLI",
			"TestCapabilityOutboundDistributionDryRunReceiptCLI",
			"# product outbound-dry-run-plan",
			"# product outbound-dry-run-review",
			"product.OutboundDryRunEvidenceReviewArea",
			"product-signals.jsonl",
			"tier3_public_distribution_policy",
			"product_loop_target",
			"auto-post",
			"ReviewRequiredMissingDecisionCard",
		},
		"cmd/compound/orient_operator.go": {
			"AdaptationIntake",
			"buildOrientAdaptationIntake",
			"ExternalRepoIntakeReviewArea",
			"capability-evaluation-intake",
			"adaptation_intake",
			"already reviewed/recorded",
		},
		"cmd/compound/orient_operator_test.go": {
			"TestOrientOperatorCarriesAdaptationIntakeProjection",
			"external_repos=1 open=0 reviewed=1 capability_evaluations=1",
			"already reviewed/recorded",
			"external repo intake",
			"capability evaluation intake",
		},
		"docs/architecture/status.md": {
			"External repo context-evaluation note",
			"`compound reviews --area external-repo-intake --json`",
			"`compound orient --operator --json.needs_me.adaptation_intake`",
			"context-aware evaluation plan",
			"trigger/action/boundary/silence/proof/retirement",
			"capability-evaluation plans and decision cards",
			"`context lens ...` review gates",
			"`evaluation lens: ...` reasons",
			"Task-context queries can include matched external repo evidence",
			"capability surface `matched_evidence`",
			"imported `needs_review` graph nodes stay non-offers",
			"`compound capability outbound-distribution-plan --task`",
			"evidence-only outbound distribution review",
			"Tier-3/product-loop gates",
			"no social auth or posting",
			"`compound capability outbound-distribution-verdict-plan`",
			"`compound reviews --area outbound-distribution-verdicts --json`",
			"dry-run receipt requirement",
			"`compound capability outbound-distribution-dry-run-receipt`",
			"`compound reviews --area outbound-distribution-dry-runs --json`",
			"`compound product outbound-dry-run-plan`",
			"`compound reviews --area product-outbound-dry-runs --json`",
			"`compound product outbound-dry-run-review`",
			"no public side effects",
			"does not mutate product signals",
			"does not mutate ProductSignal",
			"no clone/install/serve/auth/graph mutation/memory promotion",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		text, err := readContractText(repoRoot, rel)
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, text, phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"external-repo-context-evaluation-contract", Fail, "external repo intake lost context-aware evaluation plan surface: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"external-repo-context-evaluation-contract", Info, "external repo intake review cards expose context-aware evaluation plans before capability adoption"}}
}

func checkExternalRepoIntakeStateProjectionContract(repoRoot string) []Finding {
	requiredByFile := map[string][]string{
		"internal/capgraph/external_repo_intake.go": {
			"json:\"open_review_count\"",
			"annotateExternalRepoDecisionState",
			"countPendingExternalRepoOwnerReviews",
			"ExternalRepoCapabilityEvaluationStatus",
			"ExternalRepoCapabilityGraphStatus",
		},
		"internal/capgraph/external_repo_intake_test.go": {
			"default report should keep raw decision count but clear owner-review debt",
			"CapabilityEvaluationStatus != \"pending\"",
			"CapabilityGraphStatus != \"blocked_on_capability_evaluation\"",
		},
		"cmd/compound/capability.go": {
			"keep_needs_review_decision",
			"open_owner_reviews",
			"high_value: %s decision=%s lane=%s owner_review=%s capability_evaluation=%s graph=%s owner=%s",
		},
		"cmd/compound/main_test.go": {
			"default external-intake text should distinguish raw decision count from open owner-review debt",
			"owner_review=recorded capability_evaluation=pending graph=blocked_on_capability_evaluation",
		},
		"docs/architecture/status.md": {
			"Default `capability external-intake` separates raw `keep_needs_review` classification from actual `open_owner_reviews`",
			"high-value rows show owner-review, capability-evaluation, and graph status",
			"instead of calling recorded/imported rows review-next work",
		},
	}
	var failures []string
	for rel, phrases := range requiredByFile {
		text, err := readContractText(repoRoot, rel)
		if err != nil {
			failures = append(failures, rel+" unreadable: "+err.Error())
			continue
		}
		failures = append(failures, missingPhrases(rel, text, phrases)...)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return []Finding{{"external-repo-intake-state-projection-contract", Fail, "external repo default projection can confuse classification with open review debt: " + strings.Join(failures, "; ")}}
	}
	return []Finding{{"external-repo-intake-state-projection-contract", Info, "external repo default projection separates raw classification from open owner-review debt and shows evaluation state"}}
}

func checkConversationMiningContract(repoRoot string) []Finding {
	hasImport := false
	hasSourceNoPromote := false
	hasLossiness := false
	hasImprovementPlan := false
	hasNoDream := false
	hasNoMemoryPromotion := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "import-conversation-mining") {
			hasImport = true
		}
		if strings.Contains(low, "sourceobservation") && (strings.Contains(low, "nopromotedecision") || strings.Contains(low, "no-promote")) {
			hasSourceNoPromote = true
		}
		if strings.Contains(low, "lossiness") || strings.Contains(low, "lossy corpus") {
			hasLossiness = true
		}
		if strings.Contains(low, "distiller improvement-plan") {
			hasImprovementPlan = true
		}
		if strings.Contains(low, "no dream execution") || strings.Contains(low, "does not execute dream") || (strings.Contains(low, "dream execution") && strings.Contains(low, "gated")) {
			hasNoDream = true
		}
		if strings.Contains(low, "no memory promotion") || strings.Contains(low, "does not promote memory") || strings.Contains(low, "without memory promotion") {
			hasNoMemoryPromotion = true
		}
		return nil
	})
	if !hasImport || !hasSourceNoPromote || !hasLossiness || !hasImprovementPlan || !hasNoDream || !hasNoMemoryPromotion {
		return []Finding{{"conversation-mining-contract", Fail, "public docs do not describe distiller import-conversation-mining as lossiness-aware SourceObservation/no-promote evidence with reviewed improvement-plan next step, no Dream execution, and no memory promotion"}}
	}
	return []Finding{{"conversation-mining-contract", Info, "conversation mining stays lossiness-aware source evidence, not Dream or memory authority"}}
}

func checkConversationCoverageContract(repoRoot string) []Finding {
	hasImport := false
	hasSourceNoPromote := false
	hasNoRowImport := false
	hasImprovementPlan := false
	hasNoDream := false
	hasNoMemoryPromotion := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "import-conversation-coverage") {
			hasImport = true
		}
		if strings.Contains(low, "sourceobservation") && (strings.Contains(low, "nopromotedecision") || strings.Contains(low, "no-promote")) {
			hasSourceNoPromote = true
		}
		if strings.Contains(low, "no row import") || strings.Contains(low, "does not import row") || strings.Contains(low, "summary-only") {
			hasNoRowImport = true
		}
		if strings.Contains(low, "distiller improvement-plan") {
			hasImprovementPlan = true
		}
		if strings.Contains(low, "no dream execution") || strings.Contains(low, "does not execute dream") || (strings.Contains(low, "dream execution") && strings.Contains(low, "no")) {
			hasNoDream = true
		}
		if strings.Contains(low, "no memory promotion") || strings.Contains(low, "does not promote memory") || strings.Contains(low, "without memory promotion") {
			hasNoMemoryPromotion = true
		}
		return nil
	})
	if !hasImport || !hasSourceNoPromote || !hasNoRowImport || !hasImprovementPlan || !hasNoDream || !hasNoMemoryPromotion {
		return []Finding{{"conversation-coverage-contract", Fail, "public docs do not describe distiller import-conversation-coverage as summary-only SourceObservation/no-promote evidence with reviewed improvement-plan next step, no row import, no Dream execution, and no memory promotion"}}
	}
	return []Finding{{"conversation-coverage-contract", Info, "conversation coverage stays summary-only source-quality evidence, not row import, Dream, or memory authority"}}
}

func checkDecisionSourceAuthorityContract(repoRoot string) []Finding {
	hasGenericSourceImport := false
	hasSourceKindNote := false
	hasPolicyDir := false
	hasAuthorityCommand := false
	hasSourceAuthorityLog := false
	hasLifecycleOwner := false
	hasNoCandidateOnlyParser := false
	hasRuntimeDedupe := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "decisions import --source") || strings.Contains(low, "import --source <") {
			hasGenericSourceImport = true
		}
		if strings.Contains(low, "--source-dir <dir> --source-kind note") || strings.Contains(low, "--source-kind note") {
			hasSourceKindNote = true
		}
		if strings.Contains(low, "--policy-dir <dir>") {
			hasPolicyDir = true
		}
		if strings.Contains(low, "decisions authority") {
			hasAuthorityCommand = true
		}
		if strings.Contains(low, "decision-source-authority.jsonl") || strings.Contains(low, "source-authority coverage") {
			hasSourceAuthorityLog = true
		}
		if strings.Contains(low, "decisionlifecycle") {
			hasLifecycleOwner = true
		}
		if strings.Contains(low, "not a candidate-only parser") || strings.Contains(low, "not candidate-only") {
			hasNoCandidateOnlyParser = true
		}
		if strings.Contains(low, "dedupe") && strings.Contains(low, "decision://") && (strings.Contains(low, "contract compile") || strings.Contains(low, "orient")) {
			hasRuntimeDedupe = true
		}
		return nil
	})
	if !hasGenericSourceImport || !hasSourceKindNote || !hasPolicyDir || !hasAuthorityCommand || !hasSourceAuthorityLog || !hasLifecycleOwner || !hasNoCandidateOnlyParser || !hasRuntimeDedupe {
		return []Finding{{"decision-source-authority-contract", Fail, "public docs do not describe generic DecisionLifecycle source import, policy-dir import, source-authority coverage, non-candidate-only adapter, and runtime decision:// dedupe"}}
	}
	return []Finding{{"decision-source-authority-contract", Info, "decision source authority covers generic note/policy imports, source coverage, and runtime decision ref dedupe"}}
}

func checkDistillerImprovementReviewContract(repoRoot string) []Finding {
	hasPlan := false
	hasReview := false
	hasReviewYAML := false
	hasEvalProof := false
	hasEvalRecord := false
	hasRecordedProof := false
	hasNoMutation := false
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		low := strings.ToLower(string(b))
		if strings.Contains(low, "distiller improvement-plan") {
			hasPlan = true
		}
		if strings.Contains(low, "distiller improvement-review") {
			hasReview = true
		}
		if strings.Contains(low, ".review.yaml") {
			hasReviewYAML = true
		}
		if strings.Contains(low, "eval_proof_refs") {
			hasEvalProof = true
		}
		if strings.Contains(low, "distiller eval") && strings.Contains(low, "--record") {
			hasEvalRecord = true
		}
		if strings.Contains(low, "recorded passing eval proof") || strings.Contains(low, "recorded passing eval proofs") {
			hasRecordedProof = true
		}
		if strings.Contains(low, "does not mutate distiller policy") &&
			strings.Contains(low, "eval corpus") &&
			(strings.Contains(low, "memory truth") || strings.Contains(low, "promote memory")) {
			hasNoMutation = true
		}
		return nil
	})
	if !hasPlan || !hasReview || !hasReviewYAML || !hasEvalProof || !hasEvalRecord || !hasRecordedProof || !hasNoMutation {
		return []Finding{{"distiller-improvement-review-contract", Fail, "public docs do not describe distiller improvement-plan/review as gated review YAML with recorded eval_proof_refs and no policy/eval-corpus/memory mutation"}}
	}
	return []Finding{{"distiller-improvement-review-contract", Info, "distiller improvement review is gated by recorded eval proof, not policy/eval-corpus/memory mutation"}}
}

type epistemicsDoctrineRow struct {
	N                  string
	ImplementingObject string
	ProofCommand       string
}

func checkEpistemicsDoctrine(repoRoot string) []Finding {
	path := filepath.Join(repoRoot, "docs", "architecture", "epistemics.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return []Finding{{"epistemics-doctrine", Fail, "docs/architecture/epistemics.md missing or unreadable: " + err.Error()}}
	}
	rows := parseEpistemicsDoctrineRows(string(b))
	if len(rows) < 7 {
		return []Finding{{"epistemics-doctrine", Fail, "docs/architecture/epistemics.md has " + itoa(len(rows)) + " doctrine row(s); want at least 7 machine-parseable rows"}}
	}
	commandCases, err := compoundCommandCases(repoRoot)
	if err != nil {
		return []Finding{{"epistemics-doctrine", Fail, "could not inspect cmd/compound/main.go command cases: " + err.Error()}}
	}
	var problems []string
	for _, row := range rows {
		for _, object := range splitEpistemicsObjects(row.ImplementingObject) {
			if !strings.HasPrefix(object, "internal/") {
				problems = append(problems, "row "+row.N+" implementing_object "+strconv(object)+" must name internal/<package>")
				continue
			}
			if _, err := os.Stat(filepath.Join(repoRoot, object)); err != nil {
				problems = append(problems, "row "+row.N+" implementing_object "+strconv(object)+" not found")
			}
		}
		verb := epistemicsProofVerb(row.ProofCommand)
		if verb == "" {
			problems = append(problems, "row "+row.N+" proof_command "+strconv(row.ProofCommand)+" has no compound verb")
			continue
		}
		if !commandCases[verb] {
			problems = append(problems, "row "+row.N+" proof_command verb "+strconv(verb)+" has no case in cmd/compound/main.go")
		}
	}
	if len(problems) > 0 {
		return []Finding{{"epistemics-doctrine", Fail, strings.Join(problems, "; ")}}
	}
	return []Finding{{"epistemics-doctrine", Info, "docs/architecture/epistemics.md maps " + itoa(len(rows)) + " doctrine rows to existing packages and compound commands"}}
}

func parseEpistemicsDoctrineRows(text string) []epistemicsDoctrineRow {
	var rows []epistemicsDoctrineRow
	inTable := false
	for _, line := range strings.Split(text, "\n") {
		cells := splitMarkdownTableRow(line)
		if len(cells) == 0 {
			if inTable {
				break
			}
			continue
		}
		if !inTable {
			if len(cells) >= 4 && strings.EqualFold(cells[0], "n") && strings.EqualFold(cells[1], "principle") && strings.EqualFold(cells[2], "implementing_object") && strings.EqualFold(cells[3], "proof_command") {
				inTable = true
			}
			continue
		}
		if isMarkdownSeparatorRow(cells) {
			continue
		}
		if len(cells) < 4 || !isDigits(cells[0]) {
			continue
		}
		rows = append(rows, epistemicsDoctrineRow{
			N:                  cells[0],
			ImplementingObject: cells[2],
			ProofCommand:       cells[3],
		})
	}
	return rows
}

func splitMarkdownTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.Contains(line, "|") {
		return nil
	}
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, cleanEpistemicsCell(part))
	}
	return out
}

func cleanEpistemicsCell(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`")
	return strings.TrimSpace(value)
}

func isMarkdownSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			return false
		}
		for _, r := range cell {
			if r != '-' && r != ':' {
				return false
			}
		}
	}
	return true
}

func splitEpistemicsObjects(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = cleanEpistemicsCell(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func epistemicsProofVerb(command string) string {
	parts := strings.Fields(cleanEpistemicsCell(command))
	if len(parts) < 2 || parts[0] != "compound" {
		return ""
	}
	return parts[1]
}

func compoundCommandCases(repoRoot string) (map[string]bool, error) {
	b, err := os.ReadFile(filepath.Join(repoRoot, "cmd", "compound", "main.go"))
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`(?m)^\s*case\s+"([^"]+)"\s*:`)
	out := map[string]bool{}
	for _, match := range re.FindAllStringSubmatch(string(b), -1) {
		if len(match) == 2 {
			out[match[1]] = true
		}
	}
	return out, nil
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func convergenceDebt(strict bool) []Finding {
	var pending []string
	for pkg, c := range Classification {
		if c == ConvergePending {
			pending = append(pending, pkg)
		}
	}
	sort.Strings(pending)
	if len(pending) == 0 {
		return []Finding{{"convergence", Info, "memory object family converged — no Gen-1 packages pending; budget is 0"}}
	}
	// the ratchet: in strict mode a debt over budget is a FAILURE (fragmentation grew).
	sev := Info
	msg := itoa(len(pending)) + "/" + itoa(ConvergePendingBudget) + " package(s) converge→retire (BLOCKED on parity + tests): " + strings.Join(pending, ", ")
	if len(pending) > ConvergePendingBudget {
		sev = Fail
		msg = "convergence debt GREW past budget " + itoa(ConvergePendingBudget) + " → " + itoa(len(pending)) + " (new fragmentation: " + strings.Join(pending, ", ") + ")"
	} else if strict && len(pending) < ConvergePendingBudget {
		// a chance to ratchet down — surface it loudly so the budget tracks reality.
		sev = Warn
		msg = "debt is " + itoa(len(pending)) + " but budget is " + itoa(ConvergePendingBudget) + " — lower ConvergePendingBudget to lock the gain"
	}
	return []Finding{{"convergence", sev, msg}}
}

// --- tiny local helpers (no fmt dependency for hot strings) ---

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func strconv(s string) string { return "\"" + s + "\"" }
