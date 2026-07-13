package arch

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The MINT RATCHET — the build-layer half of the Two-Class Law (RATIFIED 2026-07-10,
// instruction_rule:two-class-law). Everything the system MINTS is one of two things at creation:
//
//	(1) ENFORCEMENT (in charge): blocks or passes silently at an adapter/doctor/gate; every block
//	    carries a receipt and an undo; exists ONLY as the compilation of a receipted operator ruling.
//	(2) EVIDENCE (perspective): informs but never blocks; must name its consumer (an enforcement
//	    point, an adjudication lane, or orient) or it decays.
//
// The law's own words: "A new term needs one plain sentence and a named consumer, or it dies in the
// session that minted it." This ratchet makes that mechanical for the three surfaces the build layer
// mints new identifiers on:
//
//	organ        — a new internal/ package (a new front-door organ)
//	registry     — a new compound://registries/<name> surface (a new minted term/registry; this is
//	               the two-class law's own "registry" vocabulary and its route destination)
//	status-note  — a new "<Label> [status] note:" marker in the hot status map (docs/architecture/status.md)
//
// A detected identifier that has NEITHER a grandfather grant (pre-law debt, dated below) NOR a
// two-class MintHome FAILS the architecture doctor — so new terminology cannot accrete unclassified.
//
// Detection is DETERMINISTIC (directory listing / regex over tracked files), never model-inferred.
// The grandfather sets are the exact identifiers present when the ratchet landed (2026-07-12); they
// are pre-law debt listed explicitly (per the operator ruling: whitelist genuine pre-existing
// violations with a dated grandfather list rather than weakening the check). A NEW identifier must
// earn a MintHome (enforcement, or evidence with one plain sentence + a named consumer) or be added
// to a grandfather set with a dated reason; it cannot pass silently.

// MintClass is the two-class home every minted artifact must declare.
type MintClass string

const (
	// MintEnforcement — the identifier is an enforcement point: it blocks or passes silently at an
	// adapter/doctor/gate and is the compilation of a receipted ruling (receipt + undo). Its teeth
	// ARE the gate, so a bare registration suffices.
	MintEnforcement MintClass = "enforcement"
	// MintEvidence — the identifier is evidence: it informs but never blocks. It must carry one
	// plain sentence of purpose AND name a consumer (an enforcement point, an adjudication lane, or
	// orient), or it decays.
	MintEvidence MintClass = "evidence"
)

// MintHome is the two-class registration for one minted identifier.
type MintHome struct {
	Class    MintClass
	Purpose  string // one plain sentence — REQUIRED for MintEvidence
	Consumer string // named consumer (enforcement point / adjudication lane / orient) — REQUIRED for MintEvidence
}

// Valid reports whether a home is well-formed for its class. An enforcement point's teeth are the
// gate itself, so it needs no prose; evidence must name a purpose and a consumer or it decays.
func (h MintHome) Valid() bool {
	switch h.Class {
	case MintEnforcement:
		return true
	case MintEvidence:
		return strings.TrimSpace(h.Purpose) != "" && strings.TrimSpace(h.Consumer) != ""
	default:
		return false
	}
}

// mintHomes is the two-class home for identifiers minted AFTER the ratchet landed (2026-07-12).
// Every new organ / registry surface / status note goes here (or is grandfathered with a dated
// reason). Keys are "<surface>:<id>" (e.g. "organ:scratchpad").
var mintHomes = map[string]MintHome{
	// laneselect landed the same day as the ratchet (6112375) — its registration is the ratchet's
	// first live catch, classified rather than grandfathered.
	"organ:laneselect": {
		Class:    MintEvidence,
		Purpose:  "pure preparation-lane ranking (capability fit, token speed, cost) with a veto seam",
		Consumer: "work dispatch-plan lane selection via the routeevidence adapter",
	},
	// data-sources and services land together (infra-os registry import door,
	// work_0ccf9361f136): both are evidence, not enforcement — they inform the
	// os-absorption doctor's infra_registry_authority proof mode and future
	// runtime/status projections, never block a policy decision themselves.
	"registry:data-sources": {
		Class:    MintEvidence,
		Purpose:  "infra-os database/data-authority registry (database.yaml) installed verbatim under COMPOUND_HOME with source hash + revision recorded",
		Consumer: "authority os-absorption-doctor's infra_registry_authority proof mode (compound/internal/authority)",
	},
	"registry:services": {
		Class:    MintEvidence,
		Purpose:  "infra-os services/topology registry (services.yaml) installed verbatim under COMPOUND_HOME with source hash + revision recorded",
		Consumer: "authority os-absorption-doctor's infra_registry_authority proof mode (compound/internal/authority)",
	},
	// narrate is the correspondent composer: a pure read-only projection that
	// informs the operator; it blocks nothing, so it is evidence, not enforcement.
	"organ:narrate": {
		Class:    MintEvidence,
		Purpose:  "deterministic correspondent script composed over the attention projection, orient changed-set, and recent receipt summaries",
		Consumer: "compound narrate CLI and the embedded narrate field of orient --operator",
	},
	// sycophancy is the stance-probe measurement organ: it makes operator-agreement
	// bias observable instead of trusted away. It blocks nothing (it sets no routing
	// threshold — that is a later, evidence-gated decision), so it is evidence.
	"organ:sycophancy": {
		Class:    MintEvidence,
		Purpose:  "paired-stance probe harness measuring how far a judging lane's verdict shifts when only the operator's stance is inverted (the artifact held constant), plus a read-only agreement-asymmetry projection over derived judgment records",
		Consumer: "routeevidence.SycophancyScore route evidence per lane tuple and the `compound sycophancy` CLI (orchestrator real-lane probe door)",
	},
	// jobprobe is the job-roster probe-refresh collector: it re-observes current
	// unit liveness for declared probe targets and refreshes the roster through
	// authority; it informs job_health surfacing but blocks nothing, so it is
	// evidence, not enforcement.
	"organ:jobprobe": {
		Class:    MintEvidence,
		Purpose:  "probe-refresh collector that re-observes current unit liveness for declared probe targets (files under COMPOUND_HOME) and refreshes the job-roster authority so job_health derives from observed state, not stale imported claims",
		Consumer: "authority job-roster refresh surface consumed by the director job_health scanner (compound/internal/director)",
	},
}

// grandfatheredOrgans — internal/ packages present when the ratchet landed (2026-07-12). Pre-law
// debt: they pass without a fresh two-class home. arch.Classification already records each one's
// architectural class; a NEW organ must additionally declare its two-class MintHome.
var grandfatheredOrgans = map[string]bool{
	"arch": true, "attention": true, "authority": true, "bench": true, "breadth": true,
	"candidates": true, "capgraph": true, "contract": true, "corpus": true, "decisions": true,
	"director": true, "distiller": true, "envelope": true, "eventsource": true, "harness": true,
	"harvest": true, "home": true, "hook": true, "hookplan": true, "knowledge": true,
	"lanebeliefs": true, "localmodel": true, "mem": true, "negatives": true, "observation": true,
	"panel": true, "policy": true, "product": true, "profiles": true, "projection": true,
	"release": true, "reviewartifact": true, "routeevidence": true, "runtimebinding": true,
	"schema": true, "sourceobs": true, "tooltopology": true, "work": true, "writeback": true,
}

// grandfatheredRegistries — compound://registries/<name> surfaces referenced in non-test Go when the
// ratchet landed (2026-07-12). Pre-law debt. A NEW registry surface must declare a two-class MintHome.
var grandfatheredRegistries = map[string]bool{
	"autonomy-halt": true, "capability-graph": true, "decision-freeze": true,
	"decision-source-authority": true, "editable-surfaces": true, "job-roster": true,
	"model-router": true, "operator-approval-frontier": true, "operator-decision-prompts": true,
	"operator-state": true, "os-absorption-source-matrix": true, "placement-rules": true,
	"promotion-criteria": true, "release-goal-holds": true, "source-manifests": true,
	"standing-grants": true, "work-leases": true,
}

// grandfatheredStatusNotes — "<Label> [status] note:" markers present in docs/architecture/status.md
// when the ratchet landed (2026-07-12). Pre-law debt. A NEW status note must declare a two-class
// MintHome. Ids are the marker label with the trailing colon stripped.
var grandfatheredStatusNotes = map[string]bool{
	"ASC daily signals public-boundary note":             true,
	"ASC daily source note":                              true,
	"Active packet proof note":                           true,
	"Admission preview retirement status note":           true,
	"CLI safety note":                                    true,
	"Capability command status note":                     true,
	"Capability evaluation status note":                  true,
	"Capability evaluation surfacing note":               true,
	"Capability semantic surfacing note":                 true,
	"Capability status note":                             true,
	"Capability surfacing status note":                   true,
	"CapabilityGraph meta-token note":                    true,
	"ChatGPT capture edge note":                          true,
	"ContextPack adaptation-offer note":                  true,
	"ContextPack hook status note":                       true,
	"Contract command status note":                       true,
	"Conversation preflight import-safety doctor note":   true,
	"Conversation recall gate note":                      true,
	"Dispatch read-model status note":                    true,
	"Distiller request record note":                      true,
	"Distiller runtime-source note":                      true,
	"Executor liveness and terminal-residue note":        true,
	"External repo context-evaluation note":              true,
	"External repo intake surface note":                  true,
	"Hook context mode status note":                      true,
	"Hook context side-effect note":                      true,
	"Hook event identity note":                           true,
	"Hook input hash note":                               true,
	"Hook readiness authority note":                      true,
	"Install/setup status note":                          true,
	"ObservationBatch status note":                       true,
	"Operator adaptation-intake note":                    true,
	"Operator needs-me candidate authority note":         true,
	"Operator observation-review projection note":        true,
	"Operator orientation adjacent-progress doctor note": true,
	"Operator orientation adjacent-progress note":        true,
	"Operator product-loop projection note":              true,
	"Operator source-control projection note":            true,
	"Outbound distribution dry-run receipt note":         true,
	"Outbound distribution review note":                  true,
	"Panel capability-feedback note":                     true,
	"Panel execution-failure note":                       true,
	"Panel memory-review note":                           true,
	"Panel public-knowledge privacy note":                true,
	"Panel stale-review note":                            true,
	"Product action-card status note":                    true,
	"Product action-context execution note":              true,
	"Product action-context lifecycle note":              true,
	"Product authority status note":                      true,
	"Product command status note":                        true,
	"Product observation-verdict bridge note":            true,
	"Product outbound dry-run bridge note":               true,
	"Product-loop closure proof note":                    true,
	"Product-loop projection note":                       true,
	"Product-loop signal-debt consumption note":          true,
	"Proof-check console note":                           true,
	"Proof-check status note":                            true,
	"Receipt consumer status note":                       true,
	"Reconcile digest and contested-card status note":    true,
	"Review queue surface-coverage note":                 true,
	"Session lens source note":                           true,
	"Source observation-verdict bridge note":             true,
	"Source-intake pending-extraction note":              true,
	"Spine orientation note":                             true,
	"Synthesis status note":                              true,
	"Work ContextPack status note":                       true,
	"Work close proof note":                              true,
	"Work command status note":                           true,
	"Work dispatch status note":                          true,
	"Work lease note":                                    true,
	"Work smoothness note":                               true,
	"Workbench action-card doctor note":                  true,
	"Workbench admission-preview note":                   true,
	"Workbench capability surface note":                  true,
	"Workbench held-valve note":                          true,
	"Workbench run-artifact note":                        true,
}

var registrySurfaceRE = regexp.MustCompile(`compound://registries/([a-z0-9_-]+)`)

// statusNoteRE captures a "<Label> [status] note:" marker: an uppercase-initial run of clean label
// characters (letters, digits, spaces, '/', '-') ending in "note:". The character class stops at the
// first sentence punctuation (., ;, :, backtick, comma), so it never spans descriptive prose — the
// label is bounded to the immediate marker phrase. Lazy so the leftmost clean run wins.
var statusNoteRE = regexp.MustCompile(`([A-Z][A-Za-z0-9 /-]*?note:)`)

// detectOrgans lists internal/ package directories (the same scope checkClassificationTotal walks).
func detectOrgans(repoRoot string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(repoRoot, "internal"))
	if err != nil {
		return nil, err
	}
	var organs []string
	for _, e := range entries {
		if e.IsDir() {
			organs = append(organs, e.Name())
		}
	}
	sort.Strings(organs)
	return organs, nil
}

// detectRegistrySurfaces returns the unique compound://registries/<name> surfaces referenced in
// tracked non-test Go source (the authoritative public scope: git ls-files, with a filesystem-walk
// fallback for non-git checkouts / temp-dir tests). _test.go is excluded so test fixtures naming
// throwaway registries are not policed as real minted surfaces.
func detectRegistrySurfaces(repoRoot string) ([]string, error) {
	seen := map[string]bool{}
	scan := func(rel string, b []byte) {
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return
		}
		for _, m := range registrySurfaceRE.FindAllStringSubmatch(string(b), -1) {
			seen[m[1]] = true
		}
	}
	if tracked, err := gitTrackedFiles(repoRoot); err == nil {
		for _, rel := range tracked {
			if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
				continue
			}
			b, readErr := os.ReadFile(filepath.Join(repoRoot, rel))
			if readErr != nil {
				continue
			}
			scan(rel, b)
		}
	} else {
		if walkErr := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, we error) error {
			if we != nil {
				return nil
			}
			if d.IsDir() {
				if path != repoRoot && privateSourceBodySkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			b, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				rel = path
			}
			scan(rel, b)
			return nil
		}); walkErr != nil {
			return nil, walkErr
		}
	}
	return sortedKeys(seen), nil
}

// detectStatusNotes returns the unique "<Label> [status] note:" marker labels (trailing colon
// stripped) in docs/architecture/status.md.
func detectStatusNotes(repoRoot string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(repoRoot, "docs", "architecture", "status.md"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, m := range statusNoteRE.FindAllStringSubmatch(string(b), -1) {
		label := strings.TrimSpace(strings.TrimSuffix(m[1], ":"))
		if label != "" {
			seen[label] = true
		}
	}
	return sortedKeys(seen), nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mintFindings is the PURE ratchet core: given the detected identifiers for one surface, the
// two-class home registry, and the surface's grandfather set, it returns a Fail for every identifier
// that has NEITHER a grandfather grant NOR a valid two-class home. Pure (no filesystem/process
// access) so the falsifiers are testable without a repo.
func mintFindings(surface string, detected []string, homes map[string]MintHome, grandfather map[string]bool) []Finding {
	var f []Finding
	for _, id := range detected {
		if grandfather[id] {
			continue
		}
		home, ok := homes[surface+":"+id]
		if !ok {
			f = append(f, Finding{"mint-ratchet", Fail, "new " + surface + " " + strconv(id) +
				" has no two-class home — the two-class law (RATIFIED 2026-07-10) requires every minted " + surface +
				" to be either an enforcement point (register arch.mintHomes[" + strconv(surface+":"+id) +
				"] = MintHome{Class: MintEnforcement}) or evidence (MintHome{Class: MintEvidence, Purpose: one plain sentence, Consumer: named consumer}); an unclassified mint dies in the session that minted it"})
			continue
		}
		if !home.Valid() {
			f = append(f, Finding{"mint-ratchet", Fail, "new " + surface + " " + strconv(id) +
				" is minted as " + string(home.Class) + " but its home is incomplete — evidence must carry one plain sentence of Purpose AND a named Consumer, or it decays; an unknown MintClass is not a home"})
		}
	}
	return f
}

// checkMintTwoClassHome runs the mint ratchet across all three build-layer surfaces. It also warns
// (never fails) on stale grandfather entries whose identifier no longer exists, so the dated debt
// list is prunable rather than becoming wallpaper.
func checkMintTwoClassHome(repoRoot string) []Finding {
	var f []Finding
	var detectFailed bool

	organs, err := detectOrgans(repoRoot)
	if err != nil {
		f = append(f, Finding{"mint-ratchet", Warn, "could not detect organs (internal/): " + err.Error()})
		detectFailed = true
	} else {
		f = append(f, mintFindings("organ", organs, mintHomes, grandfatheredOrgans)...)
		f = append(f, staleGrandfather("organ", "arch.grandfatheredOrgans", organs, grandfatheredOrgans)...)
	}

	registries, err := detectRegistrySurfaces(repoRoot)
	if err != nil {
		f = append(f, Finding{"mint-ratchet", Warn, "could not detect registry surfaces: " + err.Error()})
		detectFailed = true
	} else {
		f = append(f, mintFindings("registry", registries, mintHomes, grandfatheredRegistries)...)
		// registry grandfather is scanned from Go source; a name can legitimately disappear as code
		// changes, so no stale warn here (avoids churn noise).
	}

	notes, err := detectStatusNotes(repoRoot)
	if err != nil {
		f = append(f, Finding{"mint-ratchet", Warn, "could not detect status notes (docs/architecture/status.md): " + err.Error()})
		detectFailed = true
	} else {
		f = append(f, mintFindings("status-note", notes, mintHomes, grandfatheredStatusNotes)...)
	}

	hasFail := false
	for _, x := range f {
		if x.Severity == Fail {
			hasFail = true
		}
	}
	if !hasFail && !detectFailed {
		f = append(f, Finding{"mint-ratchet", Info, "every minted organ/registry/status-note carries a two-class home or a dated grandfather grant (two-class law mint gate)"})
	}
	return f
}

// staleGrandfather warns when a grandfather entry names an identifier that no longer exists in the
// tree, so the dated pre-law list can be pruned as debt is paid down. varName names the source map
// so the fix is copy-pasteable.
func staleGrandfather(surface, varName string, detected []string, grandfather map[string]bool) []Finding {
	present := map[string]bool{}
	for _, id := range detected {
		present[id] = true
	}
	var stale []string
	for id := range grandfather {
		if !present[id] {
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)
	var f []Finding
	for _, id := range stale {
		f = append(f, Finding{"mint-ratchet", Warn, "grandfathered " + surface + " " + strconv(id) +
			" no longer exists — drop it from " + varName})
	}
	return f
}
