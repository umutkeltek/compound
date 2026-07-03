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
	Leaf            Class = "leaf"             // shared leaf util (e.g. the candidate map)
	MemTarget       Class = "mem-target"       // the convergence TARGET the Gen-1 object model migrates into
	ConvergePending Class = "converge-pending" // Gen-1 capability to migrate onto the spine, THEN retire (test-gated)
	Importer        Class = "importer"         // an importer to upgrade in place (authority/decisions)
)

// Classification — every internal/ package, with its job. This is enforced: see CheckClassificationTotal.
var Classification = map[string]Class{
	// spine (canonical runtime + spine-bound Gen-2)
	"arch":           Spine,
	"capgraph":       Spine,
	"contract":       Spine,
	"distiller":      Spine,
	"envelope":       Spine,
	"harness":        Spine,
	"home":           Spine,
	"hook":           Spine,
	"localmodel":     Spine,
	"panel":          Spine,
	"policy":         Spine,
	"product":        Spine,
	"projection":     Spine,
	"routeevidence":  Spine,
	"runtimebinding": Spine,
	"schema":         Spine,
	"work":           Spine,
	"writeback":      Spine,
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
		f = append(f, checkMCPorterTopologyContract(repoRoot)...)
		f = append(f, checkPluginServingContract(repoRoot)...)
		f = append(f, checkMemoryEngineContract(repoRoot)...)
		f = append(f, checkHarnessEntrypointContract(repoRoot)...)
		f = append(f, checkHookSourceControlContract(repoRoot)...)
		f = append(f, checkStandingGrantContract(repoRoot)...)
		f = append(f, checkPlacementGuardContract(repoRoot)...)
		f = append(f, checkProductContractContract(repoRoot)...)
		f = append(f, checkCapabilityEvaluationContract(repoRoot)...)
		f = append(f, checkConversationMiningContract(repoRoot)...)
		f = append(f, checkConversationCoverageContract(repoRoot)...)
		f = append(f, checkDecisionSourceAuthorityContract(repoRoot)...)
		f = append(f, checkDistillerImprovementReviewContract(repoRoot)...)
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
			f = append(f, Finding{"classification", Fail, "internal/" + e.Name() + " is UNCLASSIFIED — classify it in arch.Classification (spine/leaf/mem-target/converge-pending/importer) so it isn't a silent new front door"})
		}
	}
	for pkg := range Classification {
		if !found[pkg] {
			f = append(f, Finding{"classification", Warn, "arch.Classification names internal/" + pkg + " but the dir is gone (retired?) — drop it from the map"})
		}
	}
	if len(f) == 0 {
		f = append(f, Finding{"classification", Info, "all " + itoa(len(found)) + " internal/ packages classified"})
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
	return []Finding{{"status-hot-path", Info, "docs/architecture/status.md hot path is " + itoa(lines) + "/" + itoa(MaxStatusHotPathLines) + " lines"}}
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
	}
}

func leakScanFile(path string) bool {
	ext := filepath.Ext(path)
	switch ext {
	case ".go", ".md", ".json", ".jsonl", ".yaml", ".yml", ".toml", ".txt":
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
