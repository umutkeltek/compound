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
	"home": Spine, "envelope": Spine, "contract": Spine, "work": Spine,
	"policy": Spine, "harness": Spine, "hook": Spine, "arch": Spine,
	"distiller": Spine, "capgraph": Spine, "runtimebinding": Spine, "projection": Spine, "writeback": Spine, "product": Spine, "routeevidence": Spine,
	// shared leaf
	"candidates": Leaf,
	// memory object family — canonical after Gen-1 package retirement
	"mem": MemTarget,
	// importers — keep, upgrade in place
	"authority": Importer, "decisions": Importer, "sourceobs": Importer, "tooltopology": Importer, "eventsource": Importer,
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
	b, err := os.ReadFile(filepath.Join(repoRoot, "docs_private", "leak-tokens.txt"))
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

// Doctor runs every check. repoRoot is the compound repo root for filesystem checks (classification
// totality, doc leak); pass "" to run only the in-memory checks (candidate map). strict turns the
// convergence-debt ratchet into a hard failure if the debt exceeds its budget. It never mutates.
func Doctor(repoRoot string, strict bool) Report {
	var f []Finding
	f = append(f, checkCandidateMap()...)
	if repoRoot != "" {
		f = append(f, checkClassificationTotal(repoRoot)...)
		f = append(f, checkDocLeak(repoRoot, strict)...)
		f = append(f, checkRetiredSurfaceReferences(repoRoot)...)
		f = append(f, checkDistillerDuplicateContract(repoRoot)...)
		f = append(f, checkLLMExtractionGroundingContract(repoRoot)...)
		f = append(f, checkDistillerCandidateShapeContract(repoRoot)...)
		f = append(f, checkRouteOutcomeContract(repoRoot)...)
		f = append(f, checkKnowledgePageContract(repoRoot)...)
		f = append(f, checkWorkPacketContract(repoRoot)...)
		f = append(f, checkMCPorterTopologyContract(repoRoot)...)
		f = append(f, checkMemoryEngineContract(repoRoot)...)
		f = append(f, checkHarnessEntrypointContract(repoRoot)...)
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

// checkDocLeak: no operator-private token in any tracked doc under docs/ (docs_private is gitignored).
// It fails CLOSED on a missing private token list: without it the gate is blind to operator
// tool/product names, so --strict (the public-release gate) makes the absent list fatal.
func checkDocLeak(repoRoot string, strict bool) []Finding {
	var f []Finding
	privateToks, present := loadPrivateLeakTokens(repoRoot)
	if !present {
		sev := Warn
		if strict {
			sev = Fail
		}
		f = append(f, Finding{"doc-leak", sev, "private leak-token list docs_private/leak-tokens.txt is MISSING — the operator-token gate is DISARMED (only generic path/email shapes are caught); create it (gitignored, one token per line) before any public release"})
	}
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		if strings.Contains(path, "docs_private") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(repoRoot, path)
		for i, line := range strings.Split(string(b), "\n") {
			if m := genericLeak.FindString(line); m != "" {
				f = append(f, Finding{"doc-leak", Fail, rel + ":" + itoa(i+1) + " leaks " + strconv(m)})
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
	if len(f) == 0 {
		f = append(f, Finding{"doc-leak", Info, "public docs clean (generic shapes + private list)"})
	}
	return f
}

// checkRetiredSurfaceReferences keeps proven-retired public surfaces from
// reappearing as pending roadmap debt after their replacement loop is proven.
func checkRetiredSurfaceReferences(repoRoot string) []Finding {
	phrases := []string{
		"old flat skill matrix",
		"old skill matrix",
	}
	var hits []string
	docs := filepath.Join(repoRoot, "docs")
	_ = filepath.WalkDir(docs, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
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
	return []Finding{{"retired-surface", Info, "capability graph replaced public old-skill-matrix debt"}}
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

// convergenceDebt reports the two-stack memory debt as a tracked, visible Info finding —
// the coherent treatment when a duplicate cannot yet be retired (Law 9: make it observable).
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
