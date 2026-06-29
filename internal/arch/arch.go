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
	"distiller": Spine, "capgraph": Spine, "runtimebinding": Spine, "projection": Spine, "writeback": Spine,
	// shared leaf
	"candidates": Leaf,
	// memory object family — canonical after Gen-1 package retirement
	"mem": MemTarget,
	// importers — keep, upgrade in place
	"authority": Importer, "decisions": Importer, "sourceobs": Importer, "tooltopology": Importer,
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

// loadPrivateLeakTokens reads optional operator-specific forbidden tokens from
// docs_private/leak-tokens.txt (gitignored). Absent file → no extra tokens (public core default).
func loadPrivateLeakTokens(repoRoot string) []string {
	b, err := os.ReadFile(filepath.Join(repoRoot, "docs_private", "leak-tokens.txt"))
	if err != nil {
		return nil
	}
	var toks []string
	for _, line := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			toks = append(toks, strings.ToLower(t))
		}
	}
	return toks
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
		f = append(f, checkDocLeak(repoRoot)...)
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
func checkDocLeak(repoRoot string) []Finding {
	var f []Finding
	privateToks := loadPrivateLeakTokens(repoRoot)
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

// convergenceDebt reports the two-stack memory debt as a tracked, visible Info finding — the
// coherent treatment when a duplicate cannot yet be retired (Law 9: make it observable).
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
