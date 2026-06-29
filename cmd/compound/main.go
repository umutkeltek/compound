// Command compound is the CLI for the compound information kernel / transition substrate.
// Built envelope-first: the proven spine is P0-P6 first slice (home, envelopes, contract/orient, work,
// policy, harness/hook, memory ledger). Memory is one slice on top (P6, re-bound through the spine), not the
// system boundary. Gen-1 memory-era commands are retired tombstones; canonical memory lives
// under `compound memory ...`.
// Architecture: docs/architecture/{substrate,runtime,transition}.md, docs/roadmap/phases.md.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/arch"
	"compound/internal/authority"
	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/contract"
	"compound/internal/decisions"
	"compound/internal/distiller"
	"compound/internal/envelope"
	"compound/internal/eventsource"
	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/mem"
	"compound/internal/policy"
	"compound/internal/product"
	"compound/internal/projection"
	"compound/internal/routeevidence"
	"compound/internal/runtimebinding"
	"compound/internal/sourceobs"
	"compound/internal/tooltopology"
	"compound/internal/work"
	"compound/internal/writeback"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("compound %s\n", version)
	case "distill", "scope", "promote", "build", "reconcile", "timeline", "recall", "pack", "ledger":
		cmdRetiredGen1(os.Args[1])
	case "authority":
		cmdAuthority(os.Args[2:])
	case "decisions":
		cmdDecisions(os.Args[2:])
	case "runtime":
		cmdRuntime(os.Args[2:])
	case "projection":
		cmdProjection(os.Args[2:])
	case "product":
		cmdProduct(os.Args[2:])
	case "source":
		cmdSource(os.Args[2:])
	case "event":
		cmdEvent(os.Args[2:])
	case "writeback":
		cmdWriteback(os.Args[2:])
	case "home":
		cmdHome(os.Args[2:])
	case "envelope":
		cmdEnvelope(os.Args[2:])
	case "contract":
		cmdContract(os.Args[2:])
	case "orient":
		cmdOrient(os.Args[2:])
	case "work":
		cmdWork(os.Args[2:])
	case "policy":
		cmdPolicy(os.Args[2:])
	case "hook":
		cmdHook(os.Args[2:])
	case "harness":
		cmdHarness(os.Args[2:])
	case "transition":
		cmdTransition(os.Args[2:])
	case "route":
		cmdRoute(os.Args[2:])
	case "memory":
		cmdMemory(os.Args[2:])
	case "distiller":
		cmdDistiller(os.Args[2:])
	case "capability":
		cmdCapability(os.Args[2:])
	case "architecture":
		cmdArchitecture(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

// cmdArchitecture runs the architecture doctor: candidate-map integrity, total package
// classification (a new unclassified package fails), public-doc leak gate, and the visible
// memory-stack convergence debt. Exits non-zero if any check FAILs.
func cmdArchitecture(args []string) {
	if len(args) == 0 || args[0] != "doctor" {
		fmt.Fprintln(os.Stderr, "usage: compound architecture doctor [--repo <path>] [--strict]")
		os.Exit(2)
	}
	repo, strict := ".", false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--strict":
			strict = true
		case "--repo":
			if i+1 < len(args) {
				repo = args[i+1]
			}
		}
	}
	r := arch.Doctor(repo, strict)
	for _, f := range r.Findings {
		fmt.Printf("  [%-4s] %-14s %s\n", f.Severity, f.Check, f.Message)
	}
	if r.OK {
		fmt.Println("verdict: ok")
	} else {
		fmt.Println("verdict: FAIL")
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `compound — local meaning + execution substrate

usage:
  compound version
  compound home <init|doctor|where> [--home P] [--local]        COMPOUND_HOME runtime body
  compound envelope <validate <file>...|kinds>                  typed runtime envelopes
  compound contract compile --harness H --cwd D --task T        compile EffectiveContract in force
  compound orient --harness H [--cwd D] [--task T]              ContextPack orient from records excluded
  compound work <create|show|list|claim|advance|close|act>      one work_id: intent -> context -> action -> receipt -> transition
  compound policy evaluate <action-intent.json>                 ActionIntent -> PolicyDecision
  compound hook <name> <fixture.json>                           run lifecycle hook over fixture
  compound harness <list|NAME>                                  HarnessRegistry capabilities + limits
  compound transition show <id>                                 inspect a Transition or envelope
  compound route import <execution-route-evidence.json>          import route metadata -> Transition projection
  compound memory <candidate|adjudicate|recall|timeline|query|engine-doctor|engine-compare [--allow-expansion]|doctor>
                                                                 spine memory: candidates, claims, positions, patterns, evidence
  compound distiller eval <case.json|cases-dir>               Offline request/response/adjudication eval corpus, no model call
  compound distiller request --trace-ref T [--work-id W] [--raw f] [--context-pack f] [--work f] [--receipt f] [--envelope f]... [--event-trace trace_id]...
                                                                 TraceDistiller LLM request packet, no model call
  compound distiller extract --request f --eval-corpus dir --openai-compatible-url URL --model M [--api-key-env ENV]
                                                                 gated rented model extraction; provider output stays candidate-only
  compound distiller run [--llm-response] <trace.json> | doctor [--writeback] | prior-state Trace -> candidate fan-out, adjudication, prior state
  compound capability <add|surface|observe|consume|list|import-topology|import-skills> capability graph: auto-surface, never auto-execute; import-topology/import-skills accept normalized files or OS generated JSON
  compound authority import [f] | path <f> | path-registry <f> | git <repo> | resolve <token> | <entity> import/query authority through spine memory
  compound decisions import [--brain <md>] [--policy <DEC.md>] [--policy-dir <dir>] | decisions [--kind=K] | decisions doctor | decisions source-doctor --template <md> [--format-doc <md>] | decisions repair-plan [--policy-dir <dir>] [--brain <md>] | decisions repair-apply --plan <json> [--write-reviewed]
                                                                  import/query decisions through spine memory; verify source format; emit/apply lifecycle repair artifacts
compound runtime doctor [--project-contract] <yaml> [--writeback] RuntimeBinding health + gated writeback
compound projection doctor <freshness.yaml> [--writeback] [--source-observe] | projection dashboard-read-model <read-model.json> [--max-age-hours N] [--writeback] [--source-observe] | projection repair-plan <freshness.yaml>
                                                               ProjectionFreshness + gated writeback/source observation/regeneration review
compound product import <product.yaml>|--project-contract <project.yaml> [--release <release.yaml>] | signal <signal.yaml>|--asc-datapoints <json> --product <id> --app-id <asc_id> [--metric <metric>]|--asc-sales-series <jsonl> --product <id> --app-id <asc_id> --before <range> --after <range> --change-event <text> [--metric <metric>] | review <review.yaml> | source-issue-review <review.yaml> | timeline <product> | doctor <product> [--writeback]
                                                               Product registry + signal/hypothesis loop
compound source observe <source-observation.yaml>              SourceObservation -> inbox candidate + no-promote
compound event import-os <events.ndjson>                       OS-style append-only events -> TraceEnvelope evidence
compound writeback <gate|no-promote>                           OwnerWritebackGate + NoPromoteDecision

retired Gen-1 tombstones:
  compound distill|scope|promote|build|reconcile|timeline|recall|pack|ledger
  These exit 2 and point to canonical compound memory/distiller surfaces.
`)
}

// cmdCapability is the P8 capability graph: surface relevant capabilities for a task (never
// execute them), and learn which capability fits which context from the distiller's observations.
func cmdCapability(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound capability <add|surface|observe|consume|list|import-topology|import-skills> ...")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := capgraph.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	sub, rest := args[0], args[1:]
	flag := func(name string) string {
		for i := 0; i < len(rest); i++ {
			if rest[i] == name && i+1 < len(rest) {
				return rest[i+1]
			}
		}
		return ""
	}
	posn := func(valueFlags map[string]bool) []string {
		var out []string
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			if strings.HasPrefix(a, "-") {
				if valueFlags[a] {
					i++
				}
				continue
			}
			out = append(out, a)
		}
		return out
	}
	die := func(e error) {
		if e != nil {
			fmt.Fprintln(os.Stderr, "error:", e)
			os.Exit(1)
		}
	}

	switch sub {
	case "import-topology":
		now := time.Now()
		osResolverPath := flag("--os-resolver-json")
		p := posn(map[string]bool{"--os-resolver-json": true})
		var topo tooltopology.Topology
		var err error
		switch {
		case osResolverPath != "" && len(p) == 0:
			topo, err = tooltopology.LoadOSResolver(osResolverPath, now)
		case osResolverPath == "" && len(p) == 1:
			topo, err = tooltopology.Load(p[0])
		default:
			fmt.Fprintln(os.Stderr, "usage: compound capability import-topology <tool-topology.yaml> | --os-resolver-json <tool-resolver.json>")
			os.Exit(2)
		}
		die(err)
		report, err := tooltopology.Apply(homePath, topo, now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			for _, check := range report.Checks {
				fmt.Fprintf(os.Stderr, "- %s: %s", check.Name, check.Status)
				if check.Detail != "" {
					fmt.Fprintf(os.Stderr, " (%s)", check.Detail)
				}
				fmt.Fprintln(os.Stderr)
			}
			os.Exit(1)
		}
		sourceReport, err := sourceobs.Apply(homePath, sourceobs.FromToolTopology(topo, now), now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "source observe:", err)
			os.Exit(1)
		}
		fmt.Printf("# tool topology %s\n\n", report.TopologyID)
		fmt.Printf("- status: %s\n", report.Status)
		for _, check := range report.Checks {
			fmt.Printf("- %s: %s", check.Name, check.Status)
			if check.Detail != "" {
				fmt.Printf(" (%s)", check.Detail)
			}
			fmt.Println()
		}
		for _, id := range report.NodeIDs {
			fmt.Printf("- node: %s\n", id)
		}
		for _, id := range report.EdgeIDs {
			fmt.Printf("- edge: %s\n", id)
		}
		fmt.Printf("- source_observation: %s\n", sourceReport.ObservationID)
		for _, id := range sourceReport.CandidateIDs {
			fmt.Printf("- source_candidate: %s\n", id)
		}
		for _, id := range sourceReport.NoPromoteIDs {
			fmt.Printf("- source_no_promote: %s\n", id)
		}
	case "import-skills":
		osSkillPath := flag("--os-skill-json")
		p := posn(map[string]bool{"--os-skill-json": true})
		var manifest capgraph.SkillManifest
		var err error
		switch {
		case osSkillPath != "" && len(p) == 0:
			manifest, err = capgraph.LoadOSSkillManifest(osSkillPath, time.Now())
		case osSkillPath == "" && len(p) == 1:
			manifest, err = capgraph.LoadSkillManifest(p[0])
		default:
			fmt.Fprintln(os.Stderr, "usage: compound capability import-skills <skill-manifest.yaml> | --os-skill-json <skill-manifest.json>")
			os.Exit(2)
		}
		die(err)
		report, err := capgraph.ApplySkillManifest(homePath, manifest, time.Now())
		die(err)
		fmt.Printf("# capability import-skills %s\n", report.ManifestID)
		fmt.Printf("- status: %s\n", report.Status)
		for _, check := range report.Checks {
			fmt.Printf("- %s: %s", check.Name, check.Status)
			if check.Detail != "" {
				fmt.Printf(" (%s)", check.Detail)
			}
			fmt.Println()
		}
		for _, id := range report.NodeIDs {
			fmt.Printf("- node: %s\n", id)
		}
		for _, id := range report.EdgeIDs {
			fmt.Printf("- edge: %s\n", id)
		}
	case "add":
		p := posn(map[string]bool{"--triggers": true})
		if len(p) < 3 {
			fmt.Fprintln(os.Stderr, "usage: compound capability add <id> <kind> <name> --triggers a,b,c")
			os.Exit(2)
		}
		var trig []string
		if t := flag("--triggers"); t != "" {
			trig = strings.Split(t, ",")
		}
		die(store.Add(capgraph.Node{ID: p[0], Kind: p[1], Name: p[2], Triggers: trig}))
		fmt.Printf("added node %s (%s)\n", p[0], p[1])
	case "surface":
		p := posn(nil)
		if len(p) == 0 {
			fmt.Fprintln(os.Stderr, `usage: compound capability surface "<task>"`)
			os.Exit(2)
		}
		hits, err := store.Surface(p[0])
		die(err)
		fmt.Printf("# %d capabilit(ies) surfaced for %q (offered, NOT executed)\n", len(hits), p[0])
		for _, h := range hits {
			fmt.Printf("  %-28s %-12s score %.1f  [%s]\n", h.Node.ID, h.Node.Kind, h.Score, h.Why)
		}
	case "observe":
		node := posn(map[string]bool{"--signal": true, "--context": true, "--conf": true})
		signal, context := flag("--signal"), flag("--context")
		conf := 0.8
		if v := flag("--conf"); v != "" {
			parsed, perr := strconv.ParseFloat(v, 64)
			if perr != nil {
				die(fmt.Errorf("--conf %q is not a number: %w", v, perr))
			}
			conf = parsed
		}
		if len(node) == 0 {
			fmt.Fprintln(os.Stderr, "usage: compound capability observe <node> --signal <missed_when|false_triggered_by> --context <phrase> [--conf 0.8]")
			os.Exit(2)
		}
		edges, err := store.Observe(node[0], signal, context, conf, time.Now())
		die(err)
		for _, e := range edges {
			fmt.Printf("recorded %s: %s -> %s (conf %.2f)\n", e.Kind, e.From, e.To, e.Confidence)
		}
	case "consume":
		n, err := store.Consume(homePath, time.Now())
		die(err)
		fmt.Printf("applied %d capability observation(s) from the distiller inbox\n", n)
	case "consume-receipts":
		report, err := store.ConsumeReceipts(homePath, time.Now())
		die(err)
		fmt.Println("# capability consume-receipts")
		fmt.Printf("- receipts_scanned: %d\n", report.ReceiptsScanned)
		fmt.Printf("- used_capabilities_seen: %d\n", report.UsedCapabilitiesSeen)
		fmt.Printf("- missed_capabilities_seen: %d\n", report.MissedCapabilitiesSeen)
		fmt.Printf("- false_triggers_seen: %d\n", report.FalseTriggersSeen)
		fmt.Printf("- applied_edges: %d\n", report.AppliedEdges)
		fmt.Printf("- applied_validated_edges: %d\n", report.AppliedValidatedEdges)
		fmt.Printf("- applied_missed_edges: %d\n", report.AppliedMissedEdges)
		fmt.Printf("- applied_false_edges: %d\n", report.AppliedFalseEdges)
		fmt.Printf("- skipped_non_completed: %d\n", report.SkippedNonCompleted)
		fmt.Printf("- skipped_unchanged: %d\n", report.SkippedUnchanged)
		fmt.Printf("- skipped_missing_context: %d\n", report.SkippedMissingContext)
		for _, id := range report.SkippedUnknown {
			fmt.Printf("- skipped_unknown: %s\n", id)
		}
	case "list":
		nodes, err := store.List()
		die(err)
		for _, n := range nodes {
			tag := ""
			if n.Retired {
				tag = " [retired]"
			}
			fmt.Printf("%-28s %-12s %s%s  triggers=%s\n", n.ID, n.Kind, n.Name, tag, strings.Join(n.Triggers, ","))
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: compound capability <add|surface|observe|consume|consume-receipts|list|import-topology|import-skills> ...")
		os.Exit(2)
	}
}

// cmdSource imports existing-system SourceObservation records into Compound-owned
// candidate/no-promote state. It observes; it never promotes source truth.
func cmdSource(args []string) {
	if len(args) != 2 || args[0] != "observe" {
		fmt.Fprintln(os.Stderr, "usage: compound source observe <source-observation.yaml>")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	obs, err := sourceobs.Load(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observe:", err)
		os.Exit(1)
	}
	report, err := sourceobs.Apply(homePath, obs, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "source observe:", err)
		for _, check := range report.Checks {
			fmt.Fprintf(os.Stderr, "- %s: %s", check.Name, check.Status)
			if check.Detail != "" {
				fmt.Fprintf(os.Stderr, " (%s)", check.Detail)
			}
			fmt.Fprintln(os.Stderr)
		}
		os.Exit(1)
	}
	fmt.Printf("# source observation %s\n\n", report.ObservationID)
	fmt.Printf("- status: %s\n", report.Status)
	for _, check := range report.Checks {
		fmt.Printf("- %s: %s", check.Name, check.Status)
		if check.Detail != "" {
			fmt.Printf(" (%s)", check.Detail)
		}
		fmt.Println()
	}
	for _, id := range report.CandidateIDs {
		fmt.Printf("- candidate: %s\n", id)
	}
	for _, id := range report.NoPromoteIDs {
		fmt.Printf("- no_promote: %s\n", id)
	}
}

func cmdEvent(args []string) {
	if len(args) < 2 || args[0] != "import-os" {
		fmt.Fprintln(os.Stderr, "usage: compound event import-os <events.ndjson> [--limit N] [--stream NAME]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	path := args[1]
	opts := eventsource.ImportOptions{}
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "event import-os: --limit needs value")
				os.Exit(2)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "event import-os: --limit must be non-negative integer")
				os.Exit(2)
			}
			opts.Limit = n
		case "--stream":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "event import-os: --stream needs value")
				os.Exit(2)
			}
			i++
			opts.Stream = args[i]
		default:
			fmt.Fprintln(os.Stderr, "event import-os: unknown arg", args[i])
			os.Exit(2)
		}
	}
	report, err := eventsource.ImportOSEvents(homePath, path, opts, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "event import-os:", err)
		os.Exit(1)
	}
	fmt.Println("# event import")
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- stream: %s\n", report.Stream)
	fmt.Printf("- imported: %d\n", report.Imported)
	fmt.Printf("- skipped: %d\n", report.Skipped)
	for _, id := range report.TraceIDs {
		fmt.Printf("- trace: %s\n", id)
	}
}

// cmdDistiller is the P6 distillation front-end: trace → prior state → candidate set → critic →
// adjudicator. Memory candidates flow through the mem ledger; everything else is candidate-first
// and never becomes canon here; self-improvement signals (eval/method/capability) never auto-apply.
func cmdDistiller(args []string) {
	if len(args) == 1 && args[0] == "prior-state" {
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		store, err := mem.Open(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		prior, err := distiller.LoadPriorStateFromHome(homePath, store, distiller.DefaultPolicy())
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller prior-state:", err)
			os.Exit(1)
		}
		cov := prior.Coverage()
		fmt.Println("# distiller prior-state")
		fmt.Printf("- policy: %s\n", prior.PolicyVersion)
		fmt.Printf("- current_claims: %d\n", cov.CurrentClaims)
		fmt.Printf("- claims: decisions=%d preferences=%d architectures=%d statuses=%d constraints=%d\n",
			cov.Decisions, cov.Preferences, cov.Architectures, cov.Statuses, cov.Constraints)
		fmt.Printf("- candidates: failures=%d taste=%d product_hypotheses=%d capability_observations=%d eval_cases=%d method_upgrades=%d policy_patches=%d\n",
			cov.FailurePatterns, cov.TasteObservations, cov.ProductHypotheses, cov.CapabilityObs, cov.EvalCases, cov.MethodUpgrades, cov.PolicyPatches)
		fmt.Printf("- capability_nodes: %d\n", cov.CapabilityNodes)
		fmt.Printf("- no_promote_decisions: %d\n", cov.NoPromoteDecisions)
		return
	}
	if len(args) >= 1 && args[0] == "doctor" {
		writebackEnabled := hasFlag(args[1:], "--writeback")
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		report, err := distiller.Doctor(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("# distiller doctor")
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- adjudications: %d\n", report.TotalDecisions)
		fmt.Printf("- memory: promoted=%d parked=%d rejected=%d duplicate_rejected=%d\n",
			report.MemoryPromoted, report.MemoryParked, report.MemoryRejected, report.DuplicateMemoryRejections)
		fmt.Printf("- no_promote: total=%d duplicate_memory=%d\n",
			report.NoPromoteRows, report.DuplicateMemoryNoPromoteRows)
		fmt.Printf("- eval_cases: %d\n", report.EvalCases)
		fmt.Printf("- method_policy_debt: method_upgrades=%d policy_patches=%d total=%d\n",
			report.MethodUpgradeDebt, report.PolicyPatchDebt, report.SelfImprovementDebt)
		fmt.Printf("- capability_observations: candidates=%d operationalized=%d\n",
			report.CapabilityObservations, report.OperationalizedCapabilityObservations)
		for _, check := range report.Checks {
			fmt.Printf("- %s: %s", check.Name, check.Status)
			if check.Detail != "" {
				fmt.Printf(" (%s)", check.Detail)
			}
			fmt.Println()
		}
		if writebackEnabled && distiller.NeedsWriteback(report) {
			result, err := runDistillerWriteback(report, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "distiller writeback:", err)
				os.Exit(1)
			}
			fmt.Printf("- writeback_gate: %s %s\n", result.GateID, result.GateStatus)
			fmt.Printf("- work: %s\n", result.WorkID)
			fmt.Printf("- receipt: %s\n", result.ReceiptID)
			fmt.Printf("- transition: %s\n", result.TransitionID)
		}
		return
	}
	if len(args) >= 1 && args[0] == "eval" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound distiller eval <case.json|cases-dir>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		info, err := os.Stat(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller eval:", err)
			os.Exit(1)
		}
		if info.IsDir() {
			report, err := distiller.EvaluateCorpusDirInRoot(args[1], homePath, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "distiller eval:", err)
				os.Exit(1)
			}
			printDistillerEvalCorpus(report)
			if report.Status != "pass" {
				os.Exit(1)
			}
			return
		}
		report, err := distiller.EvaluateCaseFileInRoot(args[1], homePath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller eval:", err)
			os.Exit(1)
		}
		printDistillerEval(report)
		if report.Status != "pass" {
			os.Exit(1)
		}
		return
	}
	if len(args) >= 1 && args[0] == "request" {
		cmdDistillerRequest(args[1:])
		return
	}
	if len(args) >= 1 && args[0] == "extract" {
		cmdDistillerExtract(args[1:])
		return
	}
	if len(args) < 2 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: compound distiller eval <case.json|cases-dir> | request --trace-ref T [--work-id W] [--raw f] [--context-pack f] [--work f] [--receipt f] [--envelope f]... [--event-trace trace_id]... | extract --request f --eval-corpus dir --openai-compatible-url URL --model M [--api-key-env ENV] | run [--llm-response] <trace.json> | doctor [--writeback] | prior-state")
		os.Exit(2)
	}

	extractor := distiller.Extractor(distiller.ReferenceExtractor{})
	tracePath := args[1]
	if args[1] == "--llm-response" {
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: compound distiller run --llm-response <response.json>")
			os.Exit(2)
		}
		extractor = distiller.LLMExtractor{}
		tracePath = args[2]
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	res, err := distiller.Run(homePath, store, data, distiller.DefaultPolicy(), extractor, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("# distilled %s under policy %s — %d candidate(s)\n", res.TraceRef, res.Policy, len(res.Decisions))
	for _, d := range res.Decisions {
		extra := ""
		if d.ClaimID != "" {
			extra = " → claim " + d.ClaimID
		} else if d.NoPromoteID != "" {
			extra = " → no_promote " + d.NoPromoteID
		}
		fmt.Printf("%-22s %-14s %s%s\n", d.Kind, d.Verdict, d.Basis, extra)
	}
	autoConsumeDistillerCapabilityFeedback(homePath)
}

func printDistillerEval(report distiller.EvalReport) {
	fmt.Printf("# distiller eval %s\n", report.ID)
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- trace_ref: %s\n", report.TraceRef)
	fmt.Printf("- decisions: %d\n", report.Run.DecisionCount)
	fmt.Printf("- current_claims: %d\n", report.Run.CurrentClaims)
	fmt.Printf("- live_model_calls: %d\n", report.Run.LiveModelCalls)
	if report.Run.Error != "" {
		fmt.Printf("- error: %s\n", report.Run.Error)
	}
	fmt.Printf("- prior: current_claims=%d capability_observations=%d eval_cases=%d method_upgrades=%d\n",
		report.Request.PriorCoverage.CurrentClaims,
		report.Request.PriorCoverage.CapabilityObs,
		report.Request.PriorCoverage.EvalCases,
		report.Request.PriorCoverage.MethodUpgrades)
	for _, check := range report.Checks {
		fmt.Printf("- check:%s %s", check.Name, check.Status)
		if check.Detail != "" {
			fmt.Printf(" (%s)", check.Detail)
		}
		fmt.Println()
	}
}

func printDistillerEvalCorpus(report distiller.EvalCorpusReport) {
	fmt.Printf("# distiller eval corpus %s\n", report.Dir)
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- cases: %d\n", report.CaseCount)
	fmt.Printf("- pass: %d\n", report.PassCount)
	fmt.Printf("- fail: %d\n", report.FailCount)
	fmt.Printf("- decisions: %d\n", report.DecisionCount)
	fmt.Printf("- current_claims: %d\n", report.CurrentClaims)
	fmt.Printf("- live_model_calls: %d\n", report.LiveModelCalls)
	for _, caseReport := range report.Reports {
		fmt.Printf("- case:%s %s decisions=%d current_claims=%d live_model_calls=%d",
			caseReport.ID,
			caseReport.Status,
			caseReport.Run.DecisionCount,
			caseReport.Run.CurrentClaims,
			caseReport.Run.LiveModelCalls)
		if caseReport.Run.Error != "" {
			fmt.Printf(" error=%q", caseReport.Run.Error)
		}
		fmt.Println()
	}
}

func cmdDistillerRequest(args []string) {
	traceRef := valueFlag(args, "--trace-ref")
	if traceRef == "" {
		fmt.Fprintln(os.Stderr, "usage: compound distiller request --trace-ref T [--work-id W] [--raw f] [--context-pack f] [--work f] [--receipt f] [--envelope f]... [--event-trace trace_id]...")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	prior, err := distiller.LoadPriorStateFromHome(homePath, store, distiller.DefaultPolicy())
	if err != nil {
		fmt.Fprintln(os.Stderr, "distiller request prior-state:", err)
		os.Exit(1)
	}
	src := distiller.LLMExtractionSources{}
	if workID := valueFlag(args, "--work-id"); workID != "" {
		if valueFlag(args, "--context-pack") != "" || valueFlag(args, "--work") != "" || valueFlag(args, "--receipt") != "" {
			fmt.Fprintln(os.Stderr, "distiller request: --work-id cannot be combined with --context-pack, --work, or --receipt")
			os.Exit(2)
		}
		src, err = distiller.LoadRuntimeSources(homePath, workID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller request runtime sources:", err)
			os.Exit(1)
		}
	}
	if rawPath := valueFlag(args, "--raw"); rawPath != "" {
		src.RawTrace, err = os.ReadFile(rawPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read raw trace:", err)
			os.Exit(1)
		}
	}
	if path := valueFlag(args, "--context-pack"); path != "" {
		env := readTypedEnvelopeOrDie(path)
		ctx, ok := env.(*envelope.ContextPack)
		if !ok {
			fmt.Fprintf(os.Stderr, "%s is %s, want context_pack\n", path, env.Kind())
			os.Exit(1)
		}
		src.ContextPack = ctx
	}
	if path := valueFlag(args, "--work"); path != "" {
		env := readTypedEnvelopeOrDie(path)
		w, ok := env.(*envelope.WorkEnvelope)
		if !ok {
			fmt.Fprintf(os.Stderr, "%s is %s, want work\n", path, env.Kind())
			os.Exit(1)
		}
		src.Work = w
	}
	if path := valueFlag(args, "--receipt"); path != "" {
		env := readTypedEnvelopeOrDie(path)
		rec, ok := env.(*envelope.Receipt)
		if !ok {
			fmt.Fprintf(os.Stderr, "%s is %s, want receipt\n", path, env.Kind())
			os.Exit(1)
		}
		src.Receipt = rec
	}
	for _, path := range valuesFlag(args, "--envelope") {
		src.Extra = append(src.Extra, readTypedEnvelopeOrDie(path))
	}
	if traceIDs := valuesFlag(args, "--event-trace"); len(traceIDs) > 0 {
		traces, err := distiller.LoadTraceEnvelopes(homePath, traceIDs)
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller request event traces:", err)
			os.Exit(1)
		}
		src.Extra = append(src.Extra, traces...)
	}
	req, err := distiller.BuildLLMExtractionRequestFromSources(traceRef, src, distiller.DefaultPolicy(), prior)
	if err != nil {
		fmt.Fprintln(os.Stderr, "distiller request:", err)
		os.Exit(1)
	}
	out, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "distiller request encode:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

func cmdDistillerExtract(args []string) {
	requestPath := valueFlag(args, "--request")
	evalCorpus := valueFlag(args, "--eval-corpus")
	baseURL := valueFlag(args, "--openai-compatible-url")
	if baseURL == "" {
		baseURL = valueFlag(args, "--base-url")
	}
	model := valueFlag(args, "--model")
	if requestPath == "" || evalCorpus == "" || baseURL == "" || model == "" {
		fmt.Fprintln(os.Stderr, "usage: compound distiller extract --request <request.json> --eval-corpus <cases-dir> --openai-compatible-url <url> --model <model> [--api-key-env ENV]")
		os.Exit(2)
	}
	apiKey := ""
	if env := valueFlag(args, "--api-key-env"); env != "" {
		apiKey = os.Getenv(env)
		if apiKey == "" {
			fmt.Fprintf(os.Stderr, "distiller extract: --api-key-env %s is empty\n", env)
			os.Exit(1)
		}
	}
	data, err := os.ReadFile(requestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "distiller extract request:", err)
		os.Exit(1)
	}
	var req distiller.LLMExtractionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		fmt.Fprintln(os.Stderr, "distiller extract request:", err)
		os.Exit(1)
	}
	if req.SchemaVersion != distiller.LLMExtractionSchemaVersion {
		fmt.Fprintf(os.Stderr, "distiller extract request: schema_version=%q, want %q\n", req.SchemaVersion, distiller.LLMExtractionSchemaVersion)
		os.Exit(1)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	now := time.Now()
	report, err := distiller.ExtractWithOpenAICompatible(context.Background(), req, distiller.OpenAICompatibleOptions{
		BaseURL:       baseURL,
		APIKey:        apiKey,
		Model:         model,
		EvalCorpusDir: evalCorpus,
		EvalRoot:      homePath,
		Now:           now,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "distiller extract:", err)
		os.Exit(1)
	}
	routeEvidence, err := distiller.BuildExecutionRouteEvidence(req, report, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "distiller extract route evidence:", err)
		os.Exit(1)
	}
	routeEvidence, routeTransition, err := distiller.JoinExecutionRouteTransition(routeEvidence)
	if err != nil {
		fmt.Fprintln(os.Stderr, "distiller extract route transition:", err)
		os.Exit(1)
	}
	if err := distiller.RecordExecutionRouteEvidence(homePath, routeEvidence); err != nil {
		fmt.Fprintln(os.Stderr, "distiller extract route evidence:", err)
		os.Exit(1)
	}
	if err := distiller.RecordExecutionRouteTransition(homePath, routeTransition); err != nil {
		fmt.Fprintln(os.Stderr, "distiller extract route transition:", err)
		os.Exit(1)
	}
	fmt.Printf("# distiller extract %s\n", report.Model)
	fmt.Printf("- provider: %s\n", report.Provider)
	fmt.Printf("- eval_status: %s\n", report.Eval.Status)
	fmt.Printf("- eval_cases: %d\n", report.Eval.CaseCount)
	fmt.Printf("- live_model_calls: %d\n", report.LiveModelCalls)
	fmt.Printf("- route_evidence: %s\n", routeEvidence.ID)
	fmt.Printf("- route_transition: %s\n", routeTransition.ID)
	fmt.Printf("- candidates: %d\n", len(report.Response.Candidates))
	out, err := json.MarshalIndent(report.Response, "", " ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "distiller extract encode:", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

func readTypedEnvelopeOrDie(path string) envelope.Envelope {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read envelope %s: %v\n", path, err)
		os.Exit(1)
	}
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		fmt.Fprintf(os.Stderr, "decode envelope %s: %v\n", path, err)
		os.Exit(1)
	}
	env := envelope.New(probe.SchemaVersion)
	if env == nil {
		fmt.Fprintf(os.Stderr, "decode envelope %s: unknown schema_version %q\n", path, probe.SchemaVersion)
		os.Exit(1)
	}
	if err := json.Unmarshal(data, env); err != nil {
		fmt.Fprintf(os.Stderr, "decode envelope %s: %v\n", path, err)
		os.Exit(1)
	}
	if err := env.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid envelope %s: %v\n", path, err)
		os.Exit(1)
	}
	return env
}

// cmdMemory is P6: the owned memory ledger re-bound through the spine. candidate-first
// (reject|park|promote), MVCC-by-fold (as-of recall), provenance + contradiction-over-delete.
func cmdMemory(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound memory <candidate <trace.json>|adjudicate <id> --verdict V|recall <entity> [--as-of N]|timeline <entity>|query <text>|engine-doctor <text>|engine-compare <text> [--allow-expansion]|doctor>")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	sub, rest := args[0], args[1:]
	flag := func(name string) string {
		for i := 0; i < len(rest); i++ {
			if rest[i] == name && i+1 < len(rest) {
				return rest[i+1]
			}
		}
		return ""
	}
	has := func(name string) bool {
		for _, a := range rest {
			if a == name {
				return true
			}
		}
		return false
	}
	pos := func() string {
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") {
				return a
			}
		}
		return ""
	}
	die := func(e error) {
		if e != nil {
			fmt.Fprintln(os.Stderr, "error:", e)
			os.Exit(1)
		}
	}

	positionals := func(valueFlags map[string]bool) []string {
		var out []string
		for i := 0; i < len(rest); i++ {
			a := rest[i]
			if strings.HasPrefix(a, "-") {
				if valueFlags[a] {
					i++
				}
				continue
			}
			out = append(out, a)
		}
		return out
	}
	// firstPos returns the first positional, correctly skipping value flags placed BEFORE it (plain
	// pos() would consume a flag's value as the entity/seed/id).
	firstPos := func(valueFlags map[string]bool) string {
		if p := positionals(valueFlags); len(p) > 0 {
			return p[0]
		}
		return ""
	}

	switch sub {
	case "candidate":
		data, err := os.ReadFile(pos())
		die(err)
		ext, err := mem.StructuredExtractor{}.Extract(data)
		die(err)
		got, err := store.Capture(ext.Candidates, time.Now())
		die(err)
		for _, c := range got {
			fmt.Printf("captured %s  %s.%s = %s  (%s)\n", c.ID, c.Entity, c.Predicate, c.Object, c.State)
		}
		for _, r := range ext.Relations {
			e, err := store.Relate(r.From, r.Kind, r.To, r.Confidence, time.Now())
			die(err)
			fmt.Printf("related  %s -%s-> %s  (seq %d, conf %.2f)\n", e.From, e.Kind, e.To, e.Seq, e.Confidence)
		}
	case "relate":
		args := positionals(map[string]bool{"--conf": true})
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: compound memory relate <from-entity> <relates|contradicts|depends_on|refines|supports|mentions|owns|part_of> <to-entity> [--conf 0.8]")
			os.Exit(2)
		}
		conf := 0.8
		if v := flag("--conf"); v != "" {
			conf, _ = strconv.ParseFloat(v, 64)
		}
		e, err := store.Relate(args[0], args[1], args[2], conf, time.Now())
		die(err)
		fmt.Printf("related %s -%s-> %s  (seq %d, conf %.2f)\n", e.From, e.Kind, e.To, e.Seq, e.Confidence)
	case "graph":
		depth, budget := 2, 20
		minConf := 0.0
		if v := flag("--depth"); v != "" {
			depth, _ = strconv.Atoi(v)
		}
		if v := flag("--budget"); v != "" {
			budget, _ = strconv.Atoi(v)
		}
		if v := flag("--min-conf"); v != "" {
			minConf, _ = strconv.ParseFloat(v, 64)
		}
		sg, err := store.RecallGraph(firstPos(map[string]bool{"--depth": true, "--budget": true, "--min-conf": true}), depth, budget, minConf)
		die(err)
		fmt.Println("# " + sg.Coverage)
		if len(sg.SeedEntities) > 0 {
			fmt.Printf("seeds: %s\n", strings.Join(sg.SeedEntities, ", "))
		}
		for _, c := range sg.Claims {
			fmt.Printf("  %s.%s = %s  (seq %d, conf %.2f)\n", c.Entity, c.Predicate, c.Object, c.Seq, c.Confidence)
		}
		for _, r := range sg.Relations {
			fmt.Printf("  %s -%s-> %s  (conf %.2f)\n", r.From, r.Kind, r.To, r.Confidence)
		}
	case "adjudicate":
		res, err := store.Adjudicate(firstPos(map[string]bool{"--verdict": true, "--work": true}), flag("--verdict"), flag("--work"), time.Now())
		die(err)
		if res.Claim != nil {
			line := fmt.Sprintf("%s: promoted claim %s (seq %d)", res.Candidate.ID, res.Claim.ID, res.Claim.Seq)
			if res.Superseded != "" {
				line += " superseding " + res.Superseded
			}
			fmt.Println(line)
			if res.Transition != nil {
				fmt.Printf("  transition %s — %s\n", res.Transition.ID, res.Transition.StateDelta)
			}
		} else {
			fmt.Printf("%s: %s%s\n", res.Candidate.ID, res.Verdict, dashNote(res.Note))
		}
	case "recall":
		var asOf int64
		if v := flag("--as-of"); v != "" {
			asOf, _ = strconv.ParseInt(v, 10, 64)
		}
		rr, err := store.Recall(firstPos(map[string]bool{"--as-of": true}), mem.RecallOpts{AsOfSeq: asOf, IncludeRetired: has("--include-retired")})
		die(err)
		fmt.Println("# " + rr.Coverage)
		for _, c := range rr.Claims {
			fmt.Printf("%s.%s = %s  (seq %d, since %s, %s, conf %.2f)\n", c.Entity, c.Predicate, c.Object, c.Seq, c.ValidFrom, c.Derivation, c.Confidence)
		}
	case "timeline":
		claims, err := store.Timeline(pos())
		die(err)
		for _, c := range claims {
			arrow := "  +"
			if c.Supersedes != "" {
				arrow = "  ->"
			}
			fmt.Printf("%s seq %-3d %s.%s = %s\n", arrow, c.Seq, c.Entity, c.Predicate, c.Object)
		}
	case "positions":
		// the spine-native Position: current durable stance per (entity, topic) WITH the
		// supersession chain (old→new) — the convergence of Gen-1 internal/position onto the spine.
		ps, err := store.Positions()
		die(err)
		for _, p := range ps {
			fmt.Printf("%-28s = %-12s [%s]  chain:%d\n", p.ID, p.ActiveObject, p.Status, len(p.ClaimChain))
		}
	case "patterns":
		// recurrence hypotheses: (entity, topic) the ledger returns to across ≥2 claims/sessions.
		// Convergence of Gen-1 internal/pattern onto the spine.
		pats, err := store.Patterns()
		die(err)
		for _, p := range pats {
			fmt.Printf("%-22s sessions:%d claims:%d  %s\n", p.ID, p.Sessions, len(p.SupportClaims), p.Status)
		}
	case "entity":
		// the routing backbone: a canonical identity resolvable by slug/identifier/alias/path, so
		// project identity survives a folder move. Convergence of Gen-1 memory.Entity + scope onto the spine.
		split := func(s string) []string {
			if s == "" {
				return nil
			}
			return strings.Split(s, ",")
		}
		switch {
		case len(rest) >= 3 && rest[0] == "register":
			e, err := store.RegisterEntity(mem.Entity{
				Slug: rest[1], Kind: rest[2],
				Aliases: split(flag("--alias")), Identifiers: split(flag("--id")), Parent: flag("--parent"),
			}, time.Now())
			die(err)
			fmt.Printf("registered %s (%s) aliases=%v ids=%v\n", e.Slug, e.Kind, e.Aliases, e.Identifiers)
		case len(rest) >= 2 && rest[0] == "resolve":
			r, err := store.ResolveEntity(rest[1])
			die(err)
			if r.Status == "resolved" {
				fmt.Printf("%s  (via %s)\n", r.Slug, r.Via)
			} else {
				fmt.Printf("%s %s\n", r.Status, r.Slug)
			}
		case len(rest) >= 1 && rest[0] == "list":
			ents, err := store.Entities()
			die(err)
			for _, e := range ents {
				fmt.Printf("%-24s %-10s aliases=%v ids=%v\n", e.Slug, e.Kind, e.Aliases, e.Identifiers)
			}
		default:
			fmt.Fprintln(os.Stderr, "usage: compound memory entity <register <slug> <kind> [--alias a,b] [--id x,y] [--parent p]|resolve <token>|list>")
			os.Exit(2)
		}
	case "evidence":
		// stored, citable, content-addressed raw material — the convergence of Gen-1 memory.EvidenceItem.
		switch {
		case len(rest) >= 1 && rest[0] == "record":
			e, err := store.RecordEvidence(mem.EvidenceItem{
				SourceKind: flag("--source-kind"), SourceID: flag("--source-id"),
				QuoteText: flag("--quote"), Actor: flag("--actor"),
			}, time.Now())
			die(err)
			fmt.Printf("recorded %s (%s) %q\n", e.ID, e.SourceKind, e.QuoteText)
		case len(rest) >= 1 && rest[0] == "list":
			items, err := store.Evidence()
			die(err)
			for _, e := range items {
				fmt.Printf("%-18s %-12s %q\n", e.ID, e.SourceKind, e.QuoteText)
			}
		default:
			fmt.Fprintln(os.Stderr, `usage: compound memory evidence <record --source-kind k --quote "..." [--source-id id] [--actor user]|list>`)
			os.Exit(2)
		}
	case "query":
		// breadth via the RENTED engine, relevance-gated by the ledger. With COMPOUND_MEM_ENGINE_BIN
		// set, drive the sidecar (e.g. turbovec) over the protocol via a warm held coprocess;
		// otherwise the in-process lexical engine. A model swap is a HARD refuse, not a degrade.
		eng, closeEng := memBreadthEngine()
		claims, err := store.QueryBreadth(pos(), 10, eng)
		closeEng() // tear down the held engine BEFORE any exit/return (os.Exit skips defers)
		switch {
		case errors.Is(err, mem.ErrUnsupported), errors.Is(err, mem.ErrEngineUnavailable):
			fmt.Println("# no breadth engine — exact/as-of recall available via `compound memory recall`")
			return
		case errors.Is(err, mem.ErrEmbedderMismatch), errors.Is(err, mem.ErrProtocolVersion):
			fmt.Fprintln(os.Stderr, "error: breadth engine refused (not degrading):", err)
			os.Exit(1)
		}
		die(err)
		fmt.Printf("# %d breadth hit(s) for %q (relevance-gated)\n", len(claims), pos())
		for _, c := range claims {
			fmt.Printf("%s.%s = %s  (seq %d, %s)\n", c.Entity, c.Predicate, c.Object, c.Seq, c.Visibility)
		}
	case "engine-doctor":
		k := 10
		if v := flag("--k"); v != "" {
			n, err := strconv.Atoi(v)
			die(err)
			k = n
		}
		eng, closeEng := memBreadthEngine()
		report, err := store.EngineDoctor(firstPos(map[string]bool{"--k": true}), k, eng)
		closeEng()
		fmt.Println("# memory engine-doctor")
		fmt.Printf("- query: %s\n", report.Query)
		fmt.Printf("- engine: %s\n", report.Engine.Name)
		fmt.Printf("- ledger_gen: %d\n", report.LedgerGen)
		fmt.Printf("- engine_gen: %d\n", report.Engine.CorpusGen)
		fmt.Printf("- ledger_before: %s\n", report.LedgerHashBefore)
		fmt.Printf("- ledger_after: %s\n", report.LedgerHashAfter)
		fmt.Printf("- ledger_unchanged: %t\n", report.LedgerUnchanged)
		if report.Degraded {
			fmt.Printf("- degraded: %s\n", report.Error)
		}
		fmt.Printf("- hits: %d\n", len(report.Hits))
		for _, h := range report.Hits {
			fmt.Printf("  - %s: %s.%s = %s (seq %d)\n", h.ClaimID, h.Entity, h.Predicate, h.Object, h.Seq)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "memory engine-doctor:", err)
			os.Exit(1)
		}
	case "engine-compare":
		if strings.TrimSpace(os.Getenv("COMPOUND_MEM_ENGINE_BIN")) == "" {
			fmt.Fprintln(os.Stderr, "memory engine-compare requires COMPOUND_MEM_ENGINE_BIN (a rented sidecar command)")
			os.Exit(2)
		}
		allowExpansion := has("--allow-expansion")
		k := 10
		if v := flag("--k"); v != "" {
			n, err := strconv.Atoi(v)
			die(err)
			k = n
		}
		eng, closeEng := memBreadthEngine()
		report, err := store.EngineCompareWithOptions(firstPos(map[string]bool{"--k": true}), k, eng, mem.EngineCompareOptions{AllowExpansion: allowExpansion})
		closeEng()
		fmt.Println("# memory engine-compare")
		fmt.Printf("- query: %s\n", report.Query)
		fmt.Printf("- compare_mode: %s\n", report.CompareMode)
		fmt.Printf("- reference_engine: %s\n", report.Reference.Engine.Name)
		fmt.Printf("- candidate_engine: %s\n", report.Candidate.Engine.Name)
		fmt.Printf("- ledger_unchanged: %t\n", report.LedgerUnchanged)
		fmt.Printf("- reference_covered: %t\n", report.ReferenceCovered)
		fmt.Printf("- reference_hits: %d\n", len(report.ReferenceClaimIDs))
		fmt.Printf("- candidate_hits: %d\n", len(report.CandidateClaimIDs))
		fmt.Printf("- missing: %d\n", len(report.MissingClaimIDs))
		for _, id := range report.MissingClaimIDs {
			fmt.Printf(" - missing %s\n", id)
		}
		fmt.Printf("- extra: %d\n", len(report.ExtraClaimIDs))
		for _, id := range report.ExtraClaimIDs {
			fmt.Printf(" - extra %s\n", id)
		}
		fmt.Printf("- expansion: %d\n", len(report.ExpansionClaimIDs))
		for _, id := range report.ExpansionClaimIDs {
			fmt.Printf(" - expansion %s\n", id)
		}
		fmt.Printf("- match: %t\n", report.Match)
		fmt.Printf("- acceptable: %t\n", report.Acceptable)
		if err != nil {
			fmt.Fprintln(os.Stderr, "memory engine-compare:", err)
			os.Exit(1)
		}
		if !report.Acceptable {
			os.Exit(1)
		}
	case "doctor":
		r := store.Doctor()
		for _, h := range r.Hops {
			fmt.Printf("%-15s %-8s %s\n", h.Name, h.Status, h.Detail)
		}
		fmt.Printf("verdict: %s — %s\n", okStr(r.OK), r.Note)
		if !r.OK {
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: compound memory <candidate|adjudicate|recall|timeline|query|engine-doctor|engine-compare [--allow-expansion]|doctor> ...")
		os.Exit(2)
	}
}

// memBreadthEngine selects the rented breadth engine: a protocol sidecar (held warm) when
// COMPOUND_MEM_ENGINE_BIN is set (e.g. the turbovec sidecar, or compound-mock-engine), else the
// in-process lexical engine. Its own env var — distinct from the legacy COMPOUND_ENGINE_BIN, which
// speaks a different protocol and retires with the legacy engine. COMPOUND_MEM_ENGINE_EMBEDDER is
// compound's record of what it indexed with; it drives the identity guard (empty = adopt on first use).
func memBreadthEngine() (mem.Engine, func()) {
	bin := strings.TrimSpace(os.Getenv("COMPOUND_MEM_ENGINE_BIN"))
	if bin == "" {
		return &mem.LexicalEngine{}, func() {}
	}
	tr := mem.NewHeldCoprocess(strings.Fields(bin), nil)
	eng := mem.NewProtocolEngine(tr, os.Getenv("COMPOUND_MEM_ENGINE_EMBEDDER"))
	return eng, func() { _ = eng.Close() }
}

func dashNote(n string) string {
	if n == "" {
		return ""
	}
	return " — " + n
}
func okStr(b bool) string {
	if b {
		return "ok"
	}
	return "FAIL"
}

// cmdTransition gives proof-by-transition a first-class read path: inspect a Transition
// (or any emitted envelope) by id under COMPOUND_HOME/events, validated through the spine.
func cmdTransition(args []string) {
	if len(args) < 2 || args[0] != "show" {
		fmt.Fprintln(os.Stderr, "usage: compound transition show <id>")
		os.Exit(2)
	}
	id := args[1]
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	path := filepath.Join(homePath, "events", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: not found under COMPOUND_HOME/events:", id)
		os.Exit(1)
	}
	kind, verr := envelope.ValidateBytes(data)
	fmt.Println(string(data))
	if verr != nil {
		fmt.Fprintf(os.Stderr, "[%s] INVALID: %v\n", kind, verr)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[%s] ok\n", kind)
}

func cmdRoute(args []string) {
	if len(args) != 2 || args[0] != "import" {
		fmt.Fprintln(os.Stderr, "usage: compound route import <execution-route-evidence.json>")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	result, err := routeevidence.ImportFile(homePath, args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "route import:", err)
		os.Exit(1)
	}
	target := result.Evidence.SelectedTarget
	fmt.Println("# route import")
	fmt.Printf("- route_evidence: %s\n", result.Evidence.ID)
	fmt.Printf("- route_transition: %s\n", result.Transition.ID)
	fmt.Printf("- request: %s\n", result.Evidence.RequestID)
	fmt.Printf("- selected_target: %s/%s (%s)\n", target.Provider, target.Model, target.Protocol)
	fmt.Printf("- status: %s\n", result.Evidence.Status)
	fmt.Println("- verdict: metadata_only_not_task_success")
}

// cmdHarness prints the HarnessRegistry — the spine's record of what each harness
// verifiably supports and where it is weak (docs/architecture/runtime.md §3).
func cmdHarness(args []string) {
	reg := harness.DefaultRegistry()
	target := "list"
	if len(args) > 0 {
		target = args[0]
	}
	print1 := func(h harness.Harness) {
		c := h.Capabilities
		fmt.Printf("%s  (%s)%s\n", h.Name, h.InstructionMechanism, fallbackTag(h.Fallback))
		fmt.Printf("  hooks: session_start=%v user_prompt=%v pre_tool_block=%v post_tool=%v session_end=%v | skills=%v mcp=%v\n",
			c.SessionStartContext, c.UserPromptContext, c.PreToolUseBlock, c.PostToolUseEvent, c.SessionEndReceipt, c.NativeSkills, c.MCP)
		for _, l := range h.Limitations {
			fmt.Printf("  limitation: %s\n", l)
		}
	}
	if target == "list" {
		for _, n := range reg.Names() {
			h, _ := reg.Get(n)
			print1(h)
		}
		return
	}
	h, ok := reg.Get(target)
	if !ok {
		fmt.Printf("(unknown harness %q — falling back to generic)\n", target)
	}
	print1(h)
}

func fallbackTag(b bool) string {
	if b {
		return "  [fallback]"
	}
	return ""
}

// cmdHook runs one lifecycle hook over a fixture, producing typed envelopes. Capability-
// aware per harness; no live install (docs/architecture/runtime.md §3, roadmap P5).
func cmdHook(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: compound hook <session-start|user-prompt-submit|pre-tool-use|post-tool-use|session-end|stop> <fixture.json>")
		os.Exit(2)
	}
	hookName, fixturePath := args[0], args[1]
	homePath := ""
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	var fx hook.Fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		fmt.Fprintln(os.Stderr, "error: parse hook fixture:", err)
		os.Exit(1)
	}
	fx.Hook = hookName // the CLI verb is authoritative

	deps := hook.Deps{Registry: harness.DefaultRegistry(), Orient: orientPolicy(), Policy: policyConfig()}
	if resolvedHome, err := home.Resolve(""); err == nil {
		homePath = resolvedHome
		deps.HomePath = homePath
		if store, err := work.Open(homePath); err == nil {
			deps.Store = store // session-end needs it; others tolerate nil
		}
	}

	res, err := hook.Run(fx, deps, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	printJSON(res)
	if hookName == "session-end" {
		autoConsumeCapabilityFeedback(homePath)
	}
	fmt.Fprintf(os.Stderr, "[%s/%s] %s — %s\n", fx.Harness, res.Event.Status, res.Event.HookName, res.Summary)
}

// gateExit maps a policy outcome to a scriptable exit code (0 allow, 3 hold, 4 deny).
func gateExit(pd envelope.PolicyDecision) int {
	switch pd.Outcome {
	case "allow", "allow_with_obligations":
		return 0
	case "hold":
		return 3
	case "deny":
		return 4
	default:
		return 1
	}
}

func policyConfig() policy.Config {
	homePath, _ := home.Resolve("") // empty if none; LoadConfig falls back to the built-in default
	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return cfg
}

func loadActionIntent(path string) envelope.ActionIntent {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	var ai envelope.ActionIntent
	if err := json.Unmarshal(data, &ai); err != nil {
		fmt.Fprintln(os.Stderr, "error: parse action intent:", err)
		os.Exit(1)
	}
	return ai
}

// cmdPolicy evaluates an ActionIntent into a PolicyDecision. Exit code reflects the gate
// (0 allow, 3 hold, 4 deny) so shell adapters can enforce without parsing JSON.
func cmdPolicy(args []string) {
	if len(args) < 2 || args[0] != "evaluate" {
		fmt.Fprintln(os.Stderr, "usage: compound policy evaluate <action-intent.json>")
		os.Exit(2)
	}
	ai := loadActionIntent(args[1])
	if err := ai.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "error: invalid action intent:", err)
		os.Exit(1)
	}
	pd := policy.Evaluate(ai, policyConfig(), time.Now())
	printJSON(pd)
	os.Exit(gateExit(pd))
}

// cmdWriteback governs candidate updates to owner surfaces.
func cmdWriteback(args []string) {
	if len(args) == 0 {
		writebackUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "gate":
		cmdWritebackGate(args[1:])
	case "no-promote":
		cmdWritebackNoPromote(args[1:])
	default:
		writebackUsage()
		os.Exit(2)
	}
}

func cmdWritebackGate(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: compound writeback gate <gate.json> --policy <policy-decision.json> [--record]")
		os.Exit(2)
	}
	gatePath := args[0]
	rest := args[1:]
	policyPath := valueFlag(rest, "--policy")
	record := hasFlag(rest, "--record")
	gate := loadWritebackGate(gatePath)
	var pd *envelope.PolicyDecision
	if policyPath != "" {
		loaded := loadPolicyDecision(policyPath)
		pd = &loaded
	}
	checked, err := writeback.EvaluateGate(gate, pd, time.Now())
	die(err)
	if record {
		homePath, err := home.Resolve("")
		die(err)
		store, err := writeback.Open(homePath)
		die(err)
		die(store.RecordGate(checked))
	}
	printJSON(checked)
	os.Exit(writebackGateExit(checked.Status))
}

func cmdWritebackNoPromote(args []string) {
	candidate := valueFlag(args, "--candidate")
	kind := valueFlag(args, "--kind")
	basis := valueFlag(args, "--basis")
	evidence := splitCSV(valueFlag(args, "--evidence"))
	ownerKind := valueFlag(args, "--owner-kind")
	ownerRef := valueFlag(args, "--owner-ref")
	expiry := valueFlag(args, "--expiry")
	policyVersion := valueFlag(args, "--policy-version")
	if candidate == "" || basis == "" || len(evidence) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound writeback no-promote --candidate C --basis B --evidence E1[,E2] [--kind K] [--owner-kind K --owner-ref R] [--expiry RFC3339]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	die(err)
	store, err := writeback.Open(homePath)
	die(err)
	decision, created, err := store.RecordNoPromote(writeback.NoPromoteDecision{
		CandidateRef:  candidate,
		CandidateKind: kind,
		OwnerTarget:   writeback.OwnerTarget{Kind: ownerKind, Ref: ownerRef},
		Basis:         basis,
		Expiry:        expiry,
		EvidenceRefs:  evidence,
		PolicyVersion: policyVersion,
	}, time.Now())
	die(err)
	out := struct {
		writeback.NoPromoteDecision
		Created bool `json:"created"`
	}{NoPromoteDecision: decision, Created: created}
	printJSON(out)
}

func writebackUsage() {
	fmt.Fprintln(os.Stderr, `usage:
compound writeback gate <gate.json> --policy <policy-decision.json> [--record]
compound writeback no-promote --candidate C --basis B --evidence E1[,E2] [--kind K] [--owner-kind K --owner-ref R] [--expiry RFC3339]`)
}

func loadWritebackGate(path string) writeback.OwnerWritebackGate {
	data, err := os.ReadFile(path)
	die(err)
	var g writeback.OwnerWritebackGate
	if err := json.Unmarshal(data, &g); err != nil {
		die(fmt.Errorf("parse owner writeback gate: %w", err))
	}
	return g
}

func loadPolicyDecision(path string) envelope.PolicyDecision {
	data, err := os.ReadFile(path)
	die(err)
	var pd envelope.PolicyDecision
	if err := json.Unmarshal(data, &pd); err != nil {
		die(fmt.Errorf("parse policy decision: %w", err))
	}
	return pd
}

func writebackGateExit(status string) int {
	switch status {
	case writeback.GateAllowed:
		return 0
	case writeback.GateDenied:
		return 4
	case writeback.GateHeld, writeback.GateNoPolicy:
		return 3
	default:
		return 1
	}
}

func valueFlag(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func valuesFlag(args []string, name string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			out = append(out, args[i+1])
			i++
		}
	}
	return out
}

func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
	}
	return false
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// cmdWork is the lifecycle store: one work_id threads intent -> context -> action ->
// receipt -> transition. State lives in COMPOUND_HOME (docs/architecture/transition.md).
func cmdWork(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound work <create|show|list|claim|advance|close> ...")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := work.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	sub, rest := args[0], args[1:]
	flag := func(name string) string {
		for i := 0; i < len(rest); i++ {
			if rest[i] == name && i+1 < len(rest) {
				return rest[i+1]
			}
		}
		return ""
	}
	has := func(name string) bool {
		for _, a := range rest {
			if a == name {
				return true
			}
		}
		return false
	}
	pos := func() string {
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") {
				return a
			}
		}
		return ""
	}
	die := func(e error) {
		if e != nil {
			fmt.Fprintln(os.Stderr, "error:", e)
			os.Exit(1)
		}
	}

	switch sub {
	case "create":
		w, err := store.Create(work.CreateInput{
			Owner:            flag("--owner"),
			SuccessPredicate: flag("--predicate"),
			DesiredOutcome:   flag("--outcome"),
			ContextPackID:    flag("--context-pack"),
			State:            flag("--state"),
			Risk:             flag("--risk"),
		}, time.Now())
		die(err)
		fmt.Printf("created %s (%s)\n", w.ID, w.State)
		printJSON(w)
	case "show":
		w, evs, err := store.Show(pos())
		die(err)
		printJSON(w)
		fmt.Println("--- lifecycle ---")
		for _, e := range evs {
			fmt.Printf("%s -> %s  %s %s\n", emptyDash(e.From), e.To, e.By, e.Note)
		}
	case "list":
		ws, err := store.List()
		die(err)
		for _, w := range ws {
			fmt.Printf("%s  %-10s %s\n", w.ID, w.State, w.SuccessPredicate)
		}
	case "claim":
		w, err := store.Claim(pos(), flag("--by"), time.Now())
		die(err)
		fmt.Printf("%s claimed by %s (%s)\n", w.ID, w.Authority.Owner, w.State)
	case "advance":
		w, err := store.Advance(pos(), work.AdvanceInput{
			To:             flag("--to"),
			RunID:          flag("--run"),
			ArtifactID:     flag("--artifact"),
			VerificationID: flag("--verification"),
			ContextPackID:  flag("--context-pack"),
			Note:           flag("--note"),
		}, time.Now())
		die(err)
		fmt.Printf("%s -> %s\n", w.ID, w.State)
	case "act":
		workID := pos()
		intentFile := flag("--intent")
		if workID == "" || intentFile == "" {
			fmt.Fprintln(os.Stderr, "usage: compound work act <work_id> --intent <action-intent.json>")
			os.Exit(2)
		}
		ai := loadActionIntent(intentFile)
		ai.IDs.WorkID = workID // bind the action to this work
		pd := policy.Evaluate(ai, policyConfig(), time.Now())
		printJSON(pd)
		if !policy.Allowed(pd) {
			fmt.Fprintf(os.Stderr, "BLOCKED: policy %s — action NOT recorded\n", pd.Outcome)
			os.Exit(gateExit(pd))
		}
		w, err := store.RecordAction(workID, ai, pd, time.Now())
		die(err)
		fmt.Printf("%s: action %s recorded (policy %s, decision %s)\n", w.ID, ai.ID, pd.Outcome, pd.ID)
	case "close":
		res, err := store.Close(pos(), work.CloseInput{
			Outcome:                    flag("--outcome"),
			MoveType:                   flag("--move-type"),
			Summary:                    flag("--summary"),
			Changed:                    has("--changed"),
			MissedCapabilities:         splitCSV(flag("--missed")),
			FalseTriggeredCapabilities: splitCSV(flag("--false-triggered")),
		}, time.Now())
		die(err)
		fmt.Printf("%s closed: %s  move_type=%s  receipt=%s  transition=%s\n",
			res.Work.ID, res.Work.State, res.Receipt.MoveType, res.Receipt.ID, res.Transition.ID)
		printJSON(res)
		autoConsumeCapabilityFeedback(homePath)
	default:
		fmt.Fprintln(os.Stderr, "usage: compound work <create|show|list|claim|advance|close> ...")
		os.Exit(2)
	}
}

func emptyDash(s string) string {
	if s == "" {
		return "(new)"
	}
	return s
}

// parseOrientArgs reads the shared orientation flags and resolves the repo kind
// (explicit flag > committed marker > product default). Path-independent by design.
func parseOrientArgs(args []string) contract.Input {
	in, _ := parseOrientArgsWithHome(args, "")
	return in
}

func parseOrientArgsWithHome(args []string, homePath string) (contract.Input, error) {
	var harness, cwd, task, repoKind, mode string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--harness":
			if i+1 < len(args) {
				i++
				harness = args[i]
			}
		case "--cwd":
			if i+1 < len(args) {
				i++
				cwd = args[i]
			}
		case "--task":
			if i+1 < len(args) {
				i++
				task = args[i]
			}
		case "--repo-kind":
			if i+1 < len(args) {
				i++
				repoKind = args[i]
			}
		case "--mode":
			if i+1 < len(args) {
				i++
				mode = args[i]
			}
		}
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	kind, via, err := contract.ResolveRepoKindWithHome(homePath, repoKind, cwd)
	if err != nil {
		return contract.Input{}, err
	}
	return contract.Input{Harness: harness, Cwd: cwd, Task: task, RepoKind: kind, RepoKindVia: via, Mode: mode}, nil
}

func orientPolicy() contract.OrientationPolicy {
	homePath, _ := home.Resolve("") // empty if none; LoadPolicy falls back to the built-in default
	pol, _, err := contract.LoadPolicy(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return pol
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Println(string(b))
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func autoConsumeCapabilityFeedback(homePath string) {
	report, ok, err := capgraph.ConsumeReceiptsIfGraphExists(homePath, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "capability feedback:", err)
		os.Exit(1)
	}
	if !ok || report.AppliedEdges == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "[capability-feedback] applied_edges=%d validated=%d missed=%d false=%d\n",
		report.AppliedEdges, report.AppliedValidatedEdges, report.AppliedMissedEdges, report.AppliedFalseEdges)
}

func autoConsumeDistillerCapabilityFeedback(homePath string) {
	if !capgraph.Exists(homePath) {
		return
	}
	store, err := capgraph.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "capability feedback:", err)
		os.Exit(1)
	}
	applied, err := store.Consume(homePath, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "capability feedback:", err)
		os.Exit(1)
	}
	if applied == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "[capability-feedback] applied_distiller_observations=%d\n", applied)
}

func cliHeader(schemaVersion, id, workID string, now time.Time, evidenceRefs []string) envelope.Header {
	return envelope.Header{
		SchemaVersion: schemaVersion,
		ID:            id,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
		IDs:           envelope.IDs{WorkID: workID},
		Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
		Privacy:       "private",
		Risk:          "tier1",
		EvidenceRefs:  evidenceRefs,
		Uncertainty:   envelope.Uncertainty{Level: "known"},
	}
}

func cliID(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(h[:])[:16]
}

func cliTS(now time.Time) string {
	return strconv.FormatInt(now.UTC().UnixNano(), 10)
}

// cmdContract compiles the EffectiveContract — the instructions/hooks/policies in force
// for a harness+repo+task (docs/architecture/contracts/README.md).
func cmdContract(args []string) {
	if len(args) == 0 || args[0] != "compile" {
		fmt.Fprintln(os.Stderr, "usage: compound contract compile --harness <h> --cwd <dir> --task <t> [--repo-kind <k>] [--mode <m>]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	die(err)
	in, err := parseOrientArgsWithHome(args[1:], homePath)
	die(err)
	if in.Harness == "" {
		fmt.Fprintln(os.Stderr, "usage: compound contract compile --harness <h> --cwd <dir> --task <t> [--repo-kind <k>] [--mode <m>]")
		os.Exit(2)
	}
	printJSON(contract.Compile(in, orientPolicy(), time.Now()))
}

// cmdOrient assembles the ContextPack an agent orients from. The excluded list — what
// was NOT loaded and why — is required, so OS-governance context is not ambient noise
// in a product repo.
func cmdOrient(args []string) {
	homePath, err := home.Resolve("")
	die(err)
	in, err := parseOrientArgsWithHome(args, homePath)
	die(err)
	if in.Harness == "" {
		fmt.Fprintln(os.Stderr, "usage: compound orient --harness <h> [--cwd <dir>] [--task <t>] [--repo-kind <k>] [--mode <m>]")
		os.Exit(2)
	}
	pack, err := contract.PackWithCapabilities(in, orientPolicy(), homePath, time.Now())
	die(err)
	printJSON(pack)
}

// cmdHome manages COMPOUND_HOME — the runtime body (docs/architecture/runtime.md).
// The first envelope-first brick: generated state lives here, never in the repo.
func cmdHome(args []string) {
	flagHome, sub, kind := "", "", "global"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--home" && i+1 < len(args):
			flagHome = args[i+1]
			i++
		case a == "--local":
			kind = "local"
		case a == "--global":
			kind = "global"
		case strings.HasPrefix(a, "-"):
			// ignore unknown flags
		case sub == "":
			sub = a
		}
	}
	resolve := func() string {
		p, err := home.Resolve(flagHome)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return p
	}
	switch sub {
	case "init":
		var hp string
		if flagHome == "" && kind == "local" && os.Getenv("COMPOUND_HOME") == "" {
			lp, err := home.LocalPath()
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
			hp = lp
		} else {
			hp = resolve()
		}
		if err := home.Init(hp, kind, time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("compound home initialized: %s (%s)\n", hp, kind)
	case "where":
		fmt.Println(resolve())
	case "doctor", "":
		printHomeDoctor(home.Doctor(resolve()))
	default:
		fmt.Fprintln(os.Stderr, "usage: compound home <init|doctor|where> [--home <path>] [--local|--global]")
		os.Exit(2)
	}
}

func homeOKStr(b bool) string {
	if b {
		return "ok"
	}
	return "MISSING"
}

func printHomeDoctor(r home.Report) {
	// generated state must not be tracked: a global home inside a git repo is the
	// smell; a --local home inside a repo is fine if it is gitignored.
	smell := r.InsideGitRepo && r.Manifest.HomeKind != "local"
	fmt.Printf("home: %s\n", r.Home)
	fmt.Printf("kind: %s\n", r.Manifest.HomeKind)
	fmt.Printf("manifest: %s\n", homeOKStr(r.ManifestOK))
	fmt.Printf("dirs: %d/%d ok\n", r.DirsOK, len(home.Dirs))
	fmt.Printf("inside_git_repo: %v\n", r.InsideGitRepo)
	fmt.Printf("generated_state_in_repo_source: %v\n", smell)
	fmt.Printf("verdict: %s\n", homeOKStr(r.OK && !smell))
	if !r.OK {
		os.Exit(1)
	}
}

// cmdEnvelope validates typed runtime envelopes (docs/architecture/runtime.md).
func cmdEnvelope(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound envelope <validate <file>...|kinds>")
		os.Exit(2)
	}
	switch args[0] {
	case "kinds":
		for _, k := range envelope.Kinds() {
			fmt.Println(k)
		}
	case "validate":
		files := args[1:]
		if len(files) == 0 {
			fmt.Fprintln(os.Stderr, "usage: compound envelope validate <file>...")
			os.Exit(2)
		}
		bad := 0
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				fmt.Printf("%s: read error: %v\n", f, err)
				bad++
				continue
			}
			kind, err := envelope.ValidateBytes(data)
			if err != nil {
				fmt.Printf("%s: INVALID (%s): %v\n", f, kind, err)
				bad++
				continue
			}
			fmt.Printf("%s: ok (%s)\n", f, kind)
		}
		if bad > 0 {
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: compound envelope <validate <file>...|kinds>")
		os.Exit(2)
	}
}

func cmdRetiredGen1(verb string) {
	replacements := map[string]string{
		"distill":   "compound distiller run <trace.json>",
		"scope":     "compound memory entity resolve <token>",
		"promote":   "compound memory adjudicate <candidate-id> --verdict promote",
		"build":     "compound memory positions / compound memory patterns",
		"reconcile": "compound memory adjudicate (supersession is spine-native)",
		"timeline":  "compound memory timeline <entity>",
		"recall":    "compound memory recall <query>",
		"pack":      "compound memory query <query>",
		"ledger":    "compound memory doctor",
	}
	fmt.Fprintf(os.Stderr, "compound %s retired with the Gen-1 memory stack. Use %s.\n", verb, replacements[verb])
	os.Exit(2)
}

func openMemStore() (*mem.Store, string) {
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run compound home init)")
		os.Exit(1)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return store, homePath
}

func cmdAuthority(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound authority import [registry.yaml] | path <path-authority.yaml> | path-registry <os-path-authority.yaml> | git <repo-path> | resolve <token> | <entity>")
		os.Exit(2)
	}
	store, _ := openMemStore()
	if args[0] == "resolve" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority resolve <token>")
			os.Exit(2)
		}
		res, err := store.ResolveEntity(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority resolve:", err)
			os.Exit(1)
		}
		fmt.Printf("status: %s\n", res.Status)
		if res.Slug != "" {
			fmt.Printf("entity: %s\n", res.Slug)
		}
		if res.Via != "" {
			fmt.Printf("via: %s\n", res.Via)
		}
		return
	}
	if args[0] == "path" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority path <path-authority.yaml>")
			os.Exit(2)
		}
		existing, err := store.Entities()
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority path:", err)
			os.Exit(1)
		}
		_, entity, facts, report, err := authority.ImportPathAuthority(args[1], existing)
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority path:", err)
			os.Exit(1)
		}
		if _, err := store.RegisterEntity(entity, time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "authority entity:", err)
			os.Exit(1)
		}
		captured, err := store.Capture(facts, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority capture:", err)
			os.Exit(1)
		}
		promoted := 0
		for _, c := range captured {
			if _, err := store.Adjudicate(c.ID, "promote", "path-authority-import", time.Now()); err != nil {
				fmt.Fprintln(os.Stderr, "authority promote:", err)
				os.Exit(1)
			}
			promoted++
		}
		fmt.Printf("# path authority %s\n\n", report.EntityRef)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- identifiers: %d\n", len(report.Identifiers))
		fmt.Printf("- authority_claims: %d\n", promoted)
		return
	}
	if args[0] == "path-registry" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority path-registry <os-path-authority.yaml>")
			os.Exit(2)
		}
		existing, err := store.Entities()
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority path-registry:", err)
			os.Exit(1)
		}
		_, entities, facts, report, err := authority.ImportOSPathRegistry(args[1], existing)
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority path-registry:", err)
			os.Exit(1)
		}
		for _, entity := range entities {
			if _, err := store.RegisterEntity(entity, time.Now()); err != nil {
				fmt.Fprintln(os.Stderr, "authority entity:", err)
				os.Exit(1)
			}
		}
		captured, err := store.Capture(facts, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority capture:", err)
			os.Exit(1)
		}
		promoted := 0
		for _, c := range captured {
			if _, err := store.Adjudicate(c.ID, "promote", "path-authority-registry-import", time.Now()); err != nil {
				fmt.Fprintln(os.Stderr, "authority promote:", err)
				os.Exit(1)
			}
			promoted++
		}
		fmt.Printf("# path authority registry\n\n")
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- entities: %d\n", report.EntityCount)
		fmt.Printf("- authority_claims: %d\n", promoted)
		return
	}
	if args[0] == "git" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority git <repo-path>")
			os.Exit(2)
		}
		existing, err := store.Entities()
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority git:", err)
			os.Exit(1)
		}
		_, entity, facts, report, err := authority.ImportGitSnapshot(args[1], existing)
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority git:", err)
			os.Exit(1)
		}
		if _, err := store.RegisterEntity(entity, time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "authority entity:", err)
			os.Exit(1)
		}
		captured, err := store.Capture(facts, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority capture:", err)
			os.Exit(1)
		}
		promoted := 0
		for _, c := range captured {
			res, err := store.Adjudicate(c.ID, "promote", "source-control-import", time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "authority promote:", err)
				os.Exit(1)
			}
			if res.Claim != nil {
				promoted++
			}
		}
		fmt.Printf("# source control\n\n")
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- entity: %s\n", report.EntityRef)
		fmt.Printf("- root: %s\n", report.GitRoot)
		fmt.Printf("- branch: %s\n", report.Branch)
		fmt.Printf("- head: %s\n", report.Head)
		fmt.Printf("- ahead: %d\n", report.Ahead)
		fmt.Printf("- behind: %d\n", report.Behind)
		fmt.Printf("- staged: %d\n", report.Staged)
		fmt.Printf("- unstaged: %d\n", report.Unstaged)
		fmt.Printf("- untracked: %d\n", report.Untracked)
		fmt.Printf("- changed_paths: %d\n", report.ChangedPaths)
		fmt.Printf("- authority_claims: %d\n", promoted)
		return
	}
	if args[0] == "import" {
		path := authority.DefaultRegistry()
		if len(args) > 1 {
			path = args[1]
		}
		if strings.TrimSpace(path) == "" {
			fmt.Fprintln(os.Stderr, "authority import needs registry path or COMPOUND_AUTHORITY_REGISTRY")
			os.Exit(2)
		}
		entities, facts, err := authority.Import(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority import:", err)
			os.Exit(1)
		}
		for _, e := range entities {
			if _, err := store.RegisterEntity(e, time.Now()); err != nil {
				fmt.Fprintln(os.Stderr, "authority entity:", err)
				os.Exit(1)
			}
		}
		captured, err := store.Capture(facts, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority capture:", err)
			os.Exit(1)
		}
		promoted := 0
		for _, c := range captured {
			if _, err := store.Adjudicate(c.ID, "promote", "authority-import", time.Now()); err != nil {
				fmt.Fprintln(os.Stderr, "authority promote:", err)
				os.Exit(1)
			}
			promoted++
		}
		fmt.Printf("imported %d entities, %d authority claims through spine memory\n", len(entities), promoted)
		return
	}
	entity := args[0]
	rr, err := store.Recall(entity, mem.RecallOpts{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "authority recall:", err)
		os.Exit(1)
	}
	fmt.Printf("# Authority - %s\n\n", entity)
	found := false
	for _, c := range rr.Claims {
		if c.Entity == entity && c.ClaimType == "status" {
			fmt.Printf("- **%s**: %s\n", c.Predicate, c.Object)
			found = true
		}
	}
	if !found {
		fmt.Printf("(no authority claims for %s; run compound authority import first)\n", entity)
	}
}

func productUsage() string {
	return "usage: compound product import <product.yaml>|--project-contract <project.yaml> [--release <release.yaml>] | signal <signal.yaml>|--asc-datapoints <json> --product <id> --app-id <asc_id> [--metric <metric>]|--asc-sales-series <jsonl> --product <id> --app-id <asc_id> --before <range> --after <range> --change-event <text> [--metric <metric>] | review <review.yaml> | source-issue-review <review.yaml> | timeline <product_id> | doctor <product_id> [--writeback]"
}

func loadProductSignalArg(args []string) (product.SignalDocument, string, error) {
	if len(args) == 1 {
		doc, err := product.LoadSignal(args[0])
		return doc, args[0], err
	}
	var ascDatapoints string
	var ascSalesSeries string
	datapointsOpts := product.ASCDatapointsSignalOptions{Metric: "review_count"}
	salesOpts := product.ASCSalesSeriesSignalOptions{Metric: "units"}
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return product.SignalDocument{}, "", fmt.Errorf("%s", productUsage())
		}
		value := args[i+1]
		switch args[i] {
		case "--asc-datapoints":
			ascDatapoints = value
		case "--asc-sales-series":
			ascSalesSeries = value
		case "--product":
			datapointsOpts.ProductID = value
			salesOpts.ProductID = value
		case "--app-id":
			datapointsOpts.AppID = value
			salesOpts.AppID = value
		case "--metric":
			datapointsOpts.Metric = value
			salesOpts.Metric = value
		case "--before":
			salesOpts.BeforeWindow = value
		case "--after":
			salesOpts.AfterWindow = value
		case "--change-event":
			salesOpts.ChangeEvent = value
		default:
			return product.SignalDocument{}, "", fmt.Errorf("%s", productUsage())
		}
		i++
	}
	if ascDatapoints != "" && ascSalesSeries != "" {
		return product.SignalDocument{}, "", fmt.Errorf("choose one product signal source")
	}
	if ascDatapoints != "" {
		if datapointsOpts.ProductID == "" || datapointsOpts.AppID == "" {
			return product.SignalDocument{}, "", fmt.Errorf("%s", productUsage())
		}
		doc, err := product.LoadASCDatapointsSignal(ascDatapoints, datapointsOpts)
		return doc, ascDatapoints, err
	}
	if ascSalesSeries != "" {
		if salesOpts.ProductID == "" || salesOpts.AppID == "" || salesOpts.BeforeWindow == "" || salesOpts.AfterWindow == "" || salesOpts.ChangeEvent == "" {
			return product.SignalDocument{}, "", fmt.Errorf("%s", productUsage())
		}
		doc, err := product.LoadASCSalesSeriesSignal(ascSalesSeries, salesOpts)
		return doc, ascSalesSeries, err
	}
	return product.SignalDocument{}, "", fmt.Errorf("%s", productUsage())
}

func cmdProduct(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, productUsage())
		os.Exit(2)
	}
	store, homePath := openMemStore()
	switch args[0] {
	case "import":
		var bundle product.Bundle
		sourcePath := ""
		var err error
		if len(args) == 2 {
			sourcePath = args[1]
			bundle, err = product.Load(args[1])
		} else if len(args) == 3 && args[1] == "--project-contract" {
			sourcePath = args[2]
			bundle, err = product.LoadProjectContractBundle(args[2], "")
		} else if len(args) == 5 && args[1] == "--project-contract" && args[3] == "--release" {
			sourcePath = args[2]
			bundle, err = product.LoadProjectContractBundle(args[2], args[4])
		} else {
			fmt.Fprintln(os.Stderr, "usage: compound product import <product.yaml>|--project-contract <project.yaml> [--release <release.yaml>]")
			os.Exit(2)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "product import:", err)
			os.Exit(1)
		}
		report, err := product.Apply(homePath, store, bundle, sourcePath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product import:", err)
			os.Exit(1)
		}
		fmt.Printf("# product import %s\n\n", report.ProductID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- entity: %s\n", report.EntityID)
		fmt.Printf("- evidence: %d\n", len(report.EvidenceIDs))
		fmt.Printf("- claims: %d\n", len(report.ClaimIDs))
		fmt.Printf("- signals: %d\n", len(report.SignalIDs))
		fmt.Printf("- hypotheses: %d\n", len(report.HypothesisIDs))
	case "signal":
		doc, sourcePath, err := loadProductSignalArg(args[1:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "product signal:", err)
			os.Exit(1)
		}
		report, err := product.ApplySignal(homePath, store, doc, sourcePath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product signal:", err)
			os.Exit(1)
		}
		fmt.Printf("# product signal %s\n\n", report.SignalIDs[0])
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- product: %s\n", report.ProductID)
		fmt.Printf("- evidence: %s\n", report.EvidenceIDs[0])
		if len(report.TransitionIDs) > 0 {
			fmt.Printf("- transition: %s\n", report.TransitionIDs[0])
		}
	case "signal-doctor":
		if len(args) < 2 || len(args) > 3 || (len(args) == 3 && args[2] != "--record") {
			fmt.Fprintln(os.Stderr, "usage: compound product signal-doctor <signal.yaml|source-export.yaml> [--record]")
			os.Exit(2)
		}
		report := product.SignalDoctor(args[1])
		fmt.Printf("# product signal doctor %s\n\n", report.SourceRef)
		fmt.Printf("- status: %s\n", report.Status)
		if report.SchemaVersion != "" {
			fmt.Printf("- schema: %s\n", report.SchemaVersion)
		}
		if report.SignalID != "" {
			fmt.Printf("- signal: %s\n", report.SignalID)
		}
		if report.ProductID != "" {
			fmt.Printf("- product: %s\n", report.ProductID)
		}
		if report.MetricName != "" {
			fmt.Printf("- metric: %s\n", report.MetricName)
		}
		for _, check := range report.Checks {
			fmt.Printf("- %s: %s", check.Name, check.Status)
			if check.Detail != "" {
				fmt.Printf(" (%s)", check.Detail)
			}
			fmt.Println()
		}
		if len(args) == 3 && args[2] == "--record" {
			if report.Status == "ok" {
				fmt.Printf("- no_promote: skipped valid source\n")
			} else {
				nop, appended, err := product.RecordSignalDoctorNoPromote(homePath, report, time.Now())
				if err != nil {
					fmt.Fprintln(os.Stderr, "product signal-doctor record:", err)
					os.Exit(1)
				}
				report.NoPromoteID = nop.ID
				fmt.Printf("- no_promote: %s", nop.ID)
				if !appended {
					fmt.Printf(" (existing)")
				}
				fmt.Println()
			}
		}
	case "review":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound product review <review.yaml>")
			os.Exit(2)
		}
		review, err := product.LoadReview(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "product review:", err)
			os.Exit(1)
		}
		report, err := product.ApplyReview(homePath, store, review, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product review:", err)
			os.Exit(1)
		}
		fmt.Printf("# product review %s\n\n", report.ReviewID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- product: %s\n", report.ProductID)
		fmt.Printf("- hypothesis: %s\n", report.HypothesisID)
		fmt.Printf("- result: %s\n", report.Result)
		fmt.Printf("- evidence: %s\n", report.EvidenceID)
		fmt.Printf("- evidence_refs: %d\n", len(report.EvidenceRefs))
		if report.TransitionID != "" {
			fmt.Printf("- transition: %s\n", report.TransitionID)
		}
	case "source-issue-review":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound product source-issue-review <review.yaml>")
			os.Exit(2)
		}
		review, err := product.LoadSourceIssueReview(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "product source issue review:", err)
			os.Exit(1)
		}
		report, err := product.ApplySourceIssueReview(homePath, store, review, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product source issue review:", err)
			os.Exit(1)
		}
		fmt.Printf("# product source issue review %s\n\n", report.ReviewID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- product: %s\n", report.ProductID)
		fmt.Printf("- issue: %s\n", report.IssueID)
		fmt.Printf("- result: %s\n", report.Result)
		fmt.Printf("- evidence: %s\n", report.EvidenceID)
		fmt.Printf("- evidence_refs: %d\n", len(report.EvidenceRefs))
		if report.TransitionID != "" {
			fmt.Printf("- transition: %s\n", report.TransitionID)
		}
	case "timeline":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound product timeline <product_id>")
			os.Exit(2)
		}
		tl, err := product.LoadTimeline(homePath, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "product timeline:", err)
			os.Exit(1)
		}
		fmt.Printf("# Product timeline - %s\n\n", args[1])
		if len(tl.Signals) == 0 && len(tl.Hypotheses) == 0 && len(tl.Transitions) == 0 && len(tl.SourceIssues) == 0 {
			fmt.Println("(no product loop records)")
			return
		}
		for _, s := range tl.Signals {
			fmt.Printf("- signal %s: %s %.4g -> %.4g on %s (%s)\n", s.ID, s.MetricName, s.Before, s.After, s.Surface, s.Window)
			if s.NextAction != "" {
				fmt.Printf("  next: %s\n", s.NextAction)
			}
			if len(s.Confounders) > 0 {
				fmt.Printf("  confounders: %s\n", strings.Join(s.Confounders, ", "))
			}
		}
		for _, h := range tl.Hypotheses {
			fmt.Printf("- hypothesis %s: %s [%s]\n", h.ID, h.ExpectedEffect, h.Result)
			if h.NextAction != "" {
				fmt.Printf("  next: %s\n", h.NextAction)
			}
		}
		for _, tr := range tl.Transitions {
			fmt.Printf("- transition %s", tr.ID)
			if tr.Kind != "" {
				fmt.Printf(" [%s]", tr.Kind)
			}
			if tr.ObjectRef != "" {
				fmt.Printf(" %s", tr.ObjectRef)
			}
			fmt.Printf(": %s\n", tr.StateDelta)
		}
		for _, issue := range tl.SourceIssues {
			status := issue.Status
			if status == "" {
				status = "open"
			}
			fmt.Printf("- source_issue %s [%s]: %s\n", issue.ID, status, issue.SourceRef)
			if issue.Basis != "" {
				fmt.Printf("  basis: %s\n", issue.Basis)
			}
			if issue.ReviewID != "" {
				fmt.Printf("  review: %s (%s)\n", issue.ReviewID, issue.ReviewResult)
			}
		}
	case "doctor":
		if len(args) < 2 || len(args) > 3 || (len(args) == 3 && args[2] != "--writeback") {
			fmt.Fprintln(os.Stderr, "usage: compound product doctor <product_id> [--writeback]")
			os.Exit(2)
		}
		report, err := product.Doctor(homePath, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "product doctor:", err)
			os.Exit(1)
		}
		fmt.Printf("# product doctor - %s\n\n", report.ProductID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- signals: %d\n", report.SignalCount)
		fmt.Printf("- hypotheses: %d\n", report.HypothesisCount)
		fmt.Printf("- pending_review: %d\n", report.PendingReviewCount)
		fmt.Printf("- source_issues: %d\n", report.SourceIssueCount)
		fmt.Printf("- missing_evidence_refs: %d\n", len(report.MissingEvidenceRefs))
		if len(args) == 3 && args[2] == "--writeback" && product.NeedsWriteback(report) {
			result, err := runProductWriteback(report, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "product writeback:", err)
				os.Exit(1)
			}
			fmt.Printf("- writeback_gate: %s %s\n", result.GateID, result.GateStatus)
			fmt.Printf("- work: %s\n", result.WorkID)
			if result.ReceiptID != "" {
				fmt.Printf("- receipt: %s\n", result.ReceiptID)
			}
			if result.TransitionID != "" {
				fmt.Printf("- transition: %s\n", result.TransitionID)
			}
		}
	default:
		fmt.Fprintln(os.Stderr, productUsage())
		os.Exit(2)
	}
}

func cmdDecisions(args []string) {
	values := func(name string) []string {
		var out []string
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == name && i+1 < len(args):
				out = append(out, args[i+1])
				i++
			case strings.HasPrefix(a, name+"="):
				out = append(out, strings.TrimPrefix(a, name+"="))
			}
		}
		return out
	}
	value := func(name string) string {
		got := values(name)
		if len(got) == 0 {
			return ""
		}
		return got[0]
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	if sub == "source-doctor" {
		templatePath := value("--template")
		formatDocPath := value("--format-doc")
		if strings.TrimSpace(templatePath) == "" {
			fmt.Fprintln(os.Stderr, "usage: compound decisions source-doctor --template <decision-template.md> [--format-doc <README.md>]")
			os.Exit(2)
		}
		report, err := decisions.DoctorSourceFormat(templatePath, formatDocPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions source-doctor:", err)
			os.Exit(1)
		}
		fmt.Printf("# decisions source-doctor\n\n")
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- template: %s\n", report.TemplatePath)
		if report.FormatDocPath != "" {
			fmt.Printf("- format_doc: %s\n", report.FormatDocPath)
		}
		fmt.Printf("- required_lifecycle_fields: %s\n", strings.Join(report.RequiredLifecycleFields, ","))
		fmt.Printf("- missing_template_frontmatter: %d\n", len(report.MissingTemplateFrontmatter))
		for _, field := range report.MissingTemplateFrontmatter {
			fmt.Printf("  - %s\n", field)
		}
		fmt.Printf("- missing_template_guidance: %d\n", len(report.MissingTemplateGuidance))
		for _, field := range report.MissingTemplateGuidance {
			fmt.Printf("  - %s\n", field)
		}
		fmt.Printf("- missing_format_documentation: %d\n", len(report.MissingFormatDocumentation))
		for _, field := range report.MissingFormatDocumentation {
			fmt.Printf("  - %s\n", field)
		}
		if report.Status != decisions.SourceFormatDoctorOK {
			os.Exit(1)
		}
		return
	}
	store, _ := openMemStore()
	switch sub {
	case "import":
		var brainReports []decisions.BrainDecisionReport
		for _, path := range values("--brain") {
			report, err := decisions.ImportBrainDecision(store, path, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "brain decision import:", err)
				os.Exit(1)
			}
			brainReports = append(brainReports, report)
		}
		var policyReports []decisions.DecisionImportReport
		for _, path := range values("--policy") {
			report, err := decisions.ImportPolicyDecision(store, path, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "policy decision import:", err)
				os.Exit(1)
			}
			policyReports = append(policyReports, report)
		}
		policyDirs := values("--policy-dir")
		if root := strings.TrimSpace(os.Getenv("COMPOUND_POLICY_DECISIONS_DIR")); root != "" {
			policyDirs = append(policyDirs, root)
		}
		for _, root := range policyDirs {
			reports, err := decisions.ImportPolicyDecisionDir(store, root, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "policy decisions import:", err)
				os.Exit(1)
			}
			policyReports = append(policyReports, reports...)
		}
		var facts []mem.Candidate
		if pattern := strings.TrimSpace(os.Getenv("COMPOUND_OPERATOR_DECISIONS_GLOB")); pattern != "" {
			matches, _ := filepath.Glob(pattern)
			for _, m := range matches {
				got, err := decisions.ImportOperatorLedger(m)
				if err != nil {
					fmt.Fprintln(os.Stderr, "operator decisions import:", err)
					os.Exit(1)
				}
				facts = append(facts, got...)
			}
		}
		captured, err := store.Capture(facts, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions capture:", err)
			os.Exit(1)
		}
		promoted, parked := 0, 0
		for _, c := range captured {
			verdict := decisions.VerdictForCandidate(c)
			if _, err := store.Adjudicate(c.ID, verdict, "decisions-import", time.Now()); err != nil {
				fmt.Fprintln(os.Stderr, "decisions adjudicate:", err)
				os.Exit(1)
			}
			if verdict == "promote" {
				promoted++
			} else {
				parked++
			}
		}
		for _, report := range brainReports {
			if report.Verdict == candidates.Promote {
				promoted++
			} else {
				parked++
			}
		}
		for _, report := range policyReports {
			if report.Verdict == candidates.Promote {
				promoted++
			} else {
				parked++
			}
		}
		fmt.Printf("imported %d decision candidates through spine memory (%d promoted, %d parked)\n", len(captured)+len(brainReports)+len(policyReports), promoted, parked)
		for _, report := range brainReports {
			fmt.Printf("- brain %s: %s candidate=%s evidence=%s claim=%s\n", report.Lifecycle.ID, report.Verdict, report.CandidateID, report.EvidenceID, report.ClaimID)
		}
		for _, report := range policyReports {
			fmt.Printf("- policy %s: %s status=%s candidate=%s evidence=%s claim=%s\n", report.Lifecycle.ID, report.Verdict, report.Lifecycle.Status, report.CandidateID, report.EvidenceID, report.ClaimID)
		}
	case "doctor":
		writebackEnabled := hasFlag(args[1:], "--writeback")
		claims, err := store.AllCurrent()
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions doctor:", err)
			os.Exit(1)
		}
		cands, err := store.AllCandidates()
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions doctor:", err)
			os.Exit(1)
		}
		report := decisions.DoctorReportFromClaimsAndCandidates(claims, cands)
		fmt.Printf("# decisions doctor\n\n")
		fmt.Printf("- policy: %d\n- doctrine: %d\n- operational: %d\n", report.Counts[decisions.KindPolicy], report.Counts[decisions.KindDoctrine], report.Counts[decisions.KindOperational])
		fmt.Printf("- proposed: %d\n- accepted: %d\n- superseded: %d\n- deprecated: %d\n- rejected: %d\n", report.Statuses[decisions.StatusProposed], report.Statuses[decisions.StatusAccepted], report.Statuses[decisions.StatusSuperseded], report.Statuses[decisions.StatusDeprecated], report.Statuses[decisions.StatusRejected])
		fmt.Printf("- current: %d\n- parked: %d\n", report.Current, report.Parked)
		fmt.Printf("- missing_lifecycle_fields: %d\n", report.MissingLifecycleFields)
		if writebackEnabled && decisions.NeedsWriteback(report) {
			result, err := runDecisionWriteback(report, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "decisions writeback:", err)
				os.Exit(1)
			}
			fmt.Printf("- writeback_gate: %s %s\n", result.GateID, result.GateStatus)
			fmt.Printf("- work: %s\n", result.WorkID)
			if result.ReceiptID != "" {
				fmt.Printf("- receipt: %s\n", result.ReceiptID)
			}
			if result.TransitionID != "" {
				fmt.Printf("- transition: %s\n", result.TransitionID)
			}
		}
	case "repair-plan":
		policyDirs := values("--policy-dir")
		brainPaths := values("--brain")
		if root := strings.TrimSpace(os.Getenv("COMPOUND_POLICY_DECISIONS_DIR")); root != "" {
			policyDirs = append(policyDirs, root)
		}
		if len(policyDirs) == 0 && len(brainPaths) == 0 {
			fmt.Fprintln(os.Stderr, "usage: compound decisions repair-plan [--policy-dir <dir>] [--brain <markdown>]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions repair-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# decisions repair-plan\n\n")
		for _, root := range policyDirs {
			plan, err := decisions.BuildLifecycleRepairPlan(root, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "decisions repair-plan:", err)
				os.Exit(1)
			}
			paths, err := decisions.RecordLifecycleRepairPlan(homePath, plan)
			if err != nil {
				fmt.Fprintln(os.Stderr, "decisions repair-plan:", err)
				os.Exit(1)
			}
			fmt.Printf("- source_root: %s\n", root)
			fmt.Printf("  status: %s\n", plan.Status)
			fmt.Printf("  repairs: %d\n", plan.RepairCount)
			fmt.Printf("  missing_falsifier: %d\n", plan.MissingFieldCounts["falsifier"])
			fmt.Printf("  missing_review_trigger: %d\n", plan.MissingFieldCounts["review_trigger"])
			fmt.Printf("  plan: %s\n", paths.PlanPath)
			fmt.Printf("  review: %s\n", paths.ReviewPath)
		}
		if len(brainPaths) > 0 {
			plan, err := decisions.BuildBrainLifecycleRepairPlan(brainPaths, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "decisions repair-plan:", err)
				os.Exit(1)
			}
			paths, err := decisions.RecordLifecycleRepairPlan(homePath, plan)
			if err != nil {
				fmt.Fprintln(os.Stderr, "decisions repair-plan:", err)
				os.Exit(1)
			}
			fmt.Printf("- source_root: %s\n", plan.SourceRoot)
			fmt.Printf(" source_kind: %s\n", plan.SourceKind)
			fmt.Printf(" status: %s\n", plan.Status)
			fmt.Printf(" repairs: %d\n", plan.RepairCount)
			fmt.Printf(" missing_falsifier: %d\n", plan.MissingFieldCounts["falsifier"])
			fmt.Printf(" missing_review_trigger: %d\n", plan.MissingFieldCounts["review_trigger"])
			fmt.Printf(" plan: %s\n", paths.PlanPath)
			fmt.Printf(" review: %s\n", paths.ReviewPath)
		}
	case "repair-apply":
		plans := values("--plan")
		if len(plans) != 1 {
			fmt.Fprintln(os.Stderr, "usage: compound decisions repair-apply --plan <repair-plan.json> [--write-reviewed]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions repair-apply:", err)
			os.Exit(1)
		}
		plan, err := decisions.LoadLifecycleRepairPlan(plans[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions repair-apply:", err)
			os.Exit(1)
		}
		dryRun := !hasFlag(args[1:], "--write-reviewed")
		report, err := decisions.ApplyLifecycleRepairPlan(plan, decisions.RepairApplyOptions{DryRun: dryRun}, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions repair-apply:", err)
			os.Exit(1)
		}
		reportPath, err := decisions.RecordLifecycleRepairApplyReport(homePath, report)
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions repair-apply:", err)
			os.Exit(1)
		}
		fmt.Printf("# decisions repair-apply\n\n")
		fmt.Printf("- plan: %s\n", report.PlanID)
		fmt.Printf("- dry_run: %v\n", report.DryRun)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- applied_fields: %d\n", report.AppliedFields)
		fmt.Printf("- changed_files: %d\n", len(report.ChangedFiles))
		fmt.Printf("- skipped: %d\n", len(report.Skipped))
		fmt.Printf("- report: %s\n", reportPath)
	default:
		kindFilter := ""
		for _, a := range args {
			if strings.HasPrefix(a, "--kind=") {
				kindFilter = strings.TrimPrefix(a, "--kind=")
			}
		}
		claims, err := store.AllCurrent()
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions:", err)
			os.Exit(1)
		}
		any := false
		for _, c := range claims {
			if c.ClaimType != "decision" {
				continue
			}
			k := decisions.Kind(c)
			if kindFilter != "" && k != kindFilter {
				continue
			}
			l := decisions.LifecycleFromClaim(c)
			fmt.Printf("- [%s/%s] %s - %s\n", l.Kind, l.Status, c.ID, c.Object)
			any = true
		}
		if !any {
			fmt.Println("(no decisions - run compound decisions import)")
		}
	}
}

func runDecisionWriteback(report decisions.DoctorReport, now time.Time) (writebackWorkResult, error) {
	var result writebackWorkResult
	if !decisions.NeedsWriteback(report) {
		return result, nil
	}
	homePath, err := home.Resolve("")
	if err != nil {
		return result, err
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		return result, err
	}
	writebackStore, err := writeback.Open(homePath)
	if err != nil {
		return result, err
	}
	gapCount := len(report.LifecycleGaps)
	w, err := workStore.Create(work.CreateInput{
		Owner:            "decisions:lifecycle",
		DesiredOutcome:   "Route decision lifecycle gaps through governed owner writeback",
		SuccessPredicate: "decision writeback gate recorded with receipt/transition refs",
		NonGoals:         []string{"mutate decision owner files", "promote decision truth"},
		State:            "ready",
		Risk:             "tier1",
		Privacy:          "private",
	}, now)
	if err != nil {
		return result, err
	}
	if _, err := workStore.Claim(w.ID, "compound decisions doctor", now); err != nil {
		return result, err
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{
		To:   "running",
		Note: "decisions doctor running writeback",
	}, now); err != nil {
		return result, err
	}
	evidenceRefs := decisions.EvidenceRefs(report)
	ai := envelope.ActionIntent{
		Header:        cliHeader(envelope.SVActionIntent, cliID("act_", "decision-writeback", strconv.Itoa(gapCount), cliTS(now)), w.ID, now, evidenceRefs),
		Actor:         "compound decisions doctor",
		Capability:    "decisions.writeback",
		Action:        "record_decision_lifecycle_writeback_gate",
		Target:        envelope.ActionTarget{Kind: "decision_lifecycle", ID: "spine-memory-decisions"},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       fmt.Sprintf("decisions doctor found %d lifecycle gap(s); record owner writeback gate", gapCount),
		RollbackPlan:  "remove COMPOUND_HOME writeback gate/work records before any owner file is changed",
	}
	pd := policy.Evaluate(ai, policyConfig(), now)
	gate := decisions.WritebackGate(report, ai.ID, pd.ID, now)
	checked, err := writeback.EvaluateGate(gate, &pd, now)
	if err != nil {
		return result, err
	}
	result = writebackWorkResult{WorkID: w.ID, ActionIntentID: ai.ID, PolicyDecisionID: pd.ID, GateID: checked.ID, GateStatus: checked.Status}
	if !policy.Allowed(pd) || checked.Status != writeback.GateAllowed {
		closeRes, closeErr := workStore.Close(w.ID, work.CloseInput{
			Outcome:  "failed",
			MoveType: "audit_only",
			Summary:  fmt.Sprintf("decision lifecycle writeback blocked for %d gap(s): gate %s", gapCount, checked.Status),
			Changed:  true,
		}, now)
		if closeErr == nil {
			checked.ReceiptRef = closeRes.Receipt.ID
			result.ReceiptID = closeRes.Receipt.ID
			result.TransitionID = closeRes.Transition.ID
		}
		if err := writebackStore.RecordGate(checked); err != nil {
			return result, err
		}
		return result, closeErr
	}
	if _, err := workStore.RecordAction(w.ID, ai, pd, now); err != nil {
		return result, err
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{
		To:             "running",
		ArtifactID:     checked.ID,
		VerificationID: checked.CandidateRef,
		Note:           "decision writeback gate checked",
	}, now); err != nil {
		return result, err
	}
	closeRes, err := workStore.Close(w.ID, work.CloseInput{
		Outcome:  "completed",
		MoveType: "control_plane_repair",
		Summary:  fmt.Sprintf("%d decision lifecycle gap(s) routed through owner writeback gate %s", gapCount, checked.ID),
		Changed:  true,
	}, now)
	if err != nil {
		return result, err
	}
	checked.ReceiptRef = closeRes.Receipt.ID
	if err := writebackStore.RecordGate(checked); err != nil {
		return result, err
	}
	result.ReceiptID = closeRes.Receipt.ID
	result.TransitionID = closeRes.Transition.ID
	return result, nil
}

func runRuntimeWriteback(report runtimebinding.DoctorReport, now time.Time) (writebackWorkResult, error) {
	var result writebackWorkResult
	if !runtimebinding.NeedsWriteback(report) {
		return result, nil
	}
	homePath, err := home.Resolve("")
	if err != nil {
		return result, err
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		return result, err
	}
	writebackStore, err := writeback.Open(homePath)
	if err != nil {
		return result, err
	}
	w, err := workStore.Create(work.CreateInput{
		Owner:            "runtime:" + report.ServiceID,
		DesiredOutcome:   "Route failing runtime doctor report through governed owner writeback",
		SuccessPredicate: "runtime writeback gate recorded with receipt/transition refs",
		NonGoals:         []string{"mutate runtime owner files", "promote runtime truth"},
		State:            "ready",
		Risk:             "tier1",
		Privacy:          "private",
	}, now)
	if err != nil {
		return result, err
	}
	if _, err := workStore.Claim(w.ID, "compound runtime doctor", now); err != nil {
		return result, err
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{
		To:   "running",
		Note: "runtime doctor running writeback",
	}, now); err != nil {
		return result, err
	}
	evidenceRefs := runtimebinding.EvidenceRefs(report)
	ai := envelope.ActionIntent{
		Header:        cliHeader(envelope.SVActionIntent, cliID("act_", "runtime-writeback", report.ServiceID, report.Status, cliTS(now)), w.ID, now, evidenceRefs),
		Actor:         "compound runtime doctor",
		Capability:    "runtime.writeback",
		Action:        "record_runtime_writeback_gate",
		Target:        envelope.ActionTarget{Kind: "runtime_binding", ID: report.ServiceID},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       fmt.Sprintf("runtime doctor for %s is %s; record owner writeback gate", report.ServiceID, report.Status),
		RollbackPlan:  "remove COMPOUND_HOME writeback gate/work records before any owner file is changed",
	}
	pd := policy.Evaluate(ai, policyConfig(), now)
	gate := runtimebinding.WritebackGate(report, ai.ID, pd.ID, now)
	checked, err := writeback.EvaluateGate(gate, &pd, now)
	if err != nil {
		return result, err
	}
	result = writebackWorkResult{WorkID: w.ID, ActionIntentID: ai.ID, PolicyDecisionID: pd.ID, GateID: checked.ID, GateStatus: checked.Status}
	if !policy.Allowed(pd) || checked.Status != writeback.GateAllowed {
		closeRes, closeErr := workStore.Close(w.ID, work.CloseInput{
			Outcome:  "failed",
			MoveType: "audit_only",
			Summary:  fmt.Sprintf("runtime writeback blocked for %s: gate %s", report.ServiceID, checked.Status),
			Changed:  true,
		}, now)
		if closeErr == nil {
			checked.ReceiptRef = closeRes.Receipt.ID
			result.ReceiptID = closeRes.Receipt.ID
			result.TransitionID = closeRes.Transition.ID
		}
		if err := writebackStore.RecordGate(checked); err != nil {
			return result, err
		}
		return result, closeErr
	}
	if _, err := workStore.RecordAction(w.ID, ai, pd, now); err != nil {
		return result, err
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{
		To:             "running",
		ArtifactID:     checked.ID,
		VerificationID: "runtime:" + report.ServiceID + ":" + report.Status,
		Note:           "runtime writeback gate checked",
	}, now); err != nil {
		return result, err
	}
	closeRes, err := workStore.Close(w.ID, work.CloseInput{
		Outcome:  "completed",
		MoveType: "control_plane_repair",
		Summary:  fmt.Sprintf("runtime %s %s routed through owner writeback gate %s", report.ServiceID, report.Status, checked.ID),
		Changed:  true,
	}, now)
	if err != nil {
		return result, err
	}
	checked.ReceiptRef = closeRes.Receipt.ID
	if err := writebackStore.RecordGate(checked); err != nil {
		return result, err
	}
	result.ReceiptID = closeRes.Receipt.ID
	result.TransitionID = closeRes.Transition.ID
	return result, nil
}

func runDistillerWriteback(report distiller.DoctorReport, now time.Time) (writebackWorkResult, error) {
	var result writebackWorkResult
	if !distiller.NeedsWriteback(report) {
		return result, nil
	}
	homePath, err := home.Resolve("")
	if err != nil {
		return result, err
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		return result, err
	}
	writebackStore, err := writeback.Open(homePath)
	if err != nil {
		return result, err
	}
	debtCount := report.SelfImprovementDebt
	w, err := workStore.Create(work.CreateInput{
		Owner:            "distiller:policy",
		DesiredOutcome:   "Route distiller method/policy debt through governed owner writeback",
		SuccessPredicate: "distiller writeback gate recorded with receipt/transition refs",
		NonGoals:         []string{"mutate distillation policy", "call model extractor", "promote memory truth"},
		State:            "ready",
		Risk:             "tier1",
		Privacy:          "private",
	}, now)
	if err != nil {
		return result, err
	}
	if _, err := workStore.Claim(w.ID, "compound distiller doctor", now); err != nil {
		return result, err
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{
		To:   "running",
		Note: "distiller doctor running writeback",
	}, now); err != nil {
		return result, err
	}
	evidenceRefs := distiller.EvidenceRefs(report)
	ai := envelope.ActionIntent{
		Header:        cliHeader(envelope.SVActionIntent, cliID("act_", "distiller-writeback", strconv.Itoa(report.MethodUpgradeDebt), strconv.Itoa(report.PolicyPatchDebt), cliTS(now)), w.ID, now, evidenceRefs),
		Actor:         "compound distiller doctor",
		Capability:    "distiller.writeback",
		Action:        "record_distillation_policy_writeback_gate",
		Target:        envelope.ActionTarget{Kind: "distillation_policy", ID: distiller.DefaultPolicy().Stamp()},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       fmt.Sprintf("distiller doctor found %d method/policy debt item(s); record owner writeback gate", debtCount),
		RollbackPlan:  "remove COMPOUND_HOME writeback gate/work records before any distillation policy is changed",
	}
	pd := policy.Evaluate(ai, policyConfig(), now)
	gate := distiller.WritebackGate(report, ai.ID, pd.ID, now)
	checked, err := writeback.EvaluateGate(gate, &pd, now)
	if err != nil {
		return result, err
	}
	result = writebackWorkResult{WorkID: w.ID, ActionIntentID: ai.ID, PolicyDecisionID: pd.ID, GateID: checked.ID, GateStatus: checked.Status}
	if !policy.Allowed(pd) || checked.Status != writeback.GateAllowed {
		closeRes, closeErr := workStore.Close(w.ID, work.CloseInput{
			Outcome:  "failed",
			MoveType: "audit_only",
			Summary:  fmt.Sprintf("distiller method/policy writeback blocked for %d debt item(s): gate %s", debtCount, checked.Status),
			Changed:  true,
		}, now)
		if closeErr == nil {
			checked.ReceiptRef = closeRes.Receipt.ID
			result.ReceiptID = closeRes.Receipt.ID
			result.TransitionID = closeRes.Transition.ID
		}
		if err := writebackStore.RecordGate(checked); err != nil {
			return result, err
		}
		return result, closeErr
	}
	if _, err := workStore.RecordAction(w.ID, ai, pd, now); err != nil {
		return result, err
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{
		To:             "running",
		ArtifactID:     checked.ID,
		VerificationID: checked.CandidateRef,
		Note:           "distiller writeback gate checked",
	}, now); err != nil {
		return result, err
	}
	closeRes, err := workStore.Close(w.ID, work.CloseInput{
		Outcome:  "completed",
		MoveType: "control_plane_repair",
		Summary:  fmt.Sprintf("%d distiller method/policy debt item(s) routed through owner writeback gate %s", debtCount, checked.ID),
		Changed:  true,
	}, now)
	if err != nil {
		return result, err
	}
	checked.ReceiptRef = closeRes.Receipt.ID
	if err := writebackStore.RecordGate(checked); err != nil {
		return result, err
	}
	result.ReceiptID = closeRes.Receipt.ID
	result.TransitionID = closeRes.Transition.ID
	return result, nil
}

func cmdRuntime(args []string) {
	if len(args) < 1 || args[0] != "doctor" {
		fmt.Fprintln(os.Stderr, "usage: compound runtime doctor [--project-contract] <yaml> [--writeback]")
		os.Exit(2)
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: compound runtime doctor [--project-contract] <yaml> [--writeback]")
		os.Exit(2)
	}
	path := args[1]
	load := runtimebinding.Load
	writebackEnabled := hasFlag(args[1:], "--writeback")
	if args[1] == "--project-contract" {
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: compound runtime doctor --project-contract <project.yaml> [--writeback]")
			os.Exit(2)
		}
		path = args[2]
		load = runtimebinding.LoadProjectContract
	}
	bundle, err := load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime doctor:", err)
		os.Exit(1)
	}
	report := runtimebinding.Doctor(bundle)
	fmt.Printf("# runtime doctor\n\n")
	fmt.Printf("- service: %s\n", report.ServiceID)
	fmt.Printf("- owner: %s\n", report.Owner)
	fmt.Printf("- status: %s\n", report.Status)
	for _, check := range report.Checks {
		fmt.Printf("- %s: %s", check.Name, check.Status)
		if check.Detail != "" {
			fmt.Printf(" (%s)", check.Detail)
		}
		fmt.Println()
	}
	if report.Status != "ok" {
		if writebackEnabled {
			result, err := runRuntimeWriteback(report, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "runtime writeback:", err)
				os.Exit(1)
			}
			fmt.Printf("- writeback_gate: %s %s\n", result.GateID, result.GateStatus)
			fmt.Printf("- work: %s\n", result.WorkID)
			if result.ReceiptID != "" {
				fmt.Printf("- receipt: %s\n", result.ReceiptID)
			}
			if result.TransitionID != "" {
				fmt.Printf("- transition: %s\n", result.TransitionID)
			}
			return
		}
		os.Exit(1)
	}
}

type writebackWorkResult struct {
	WorkID           string `json:"work_id"`
	ActionIntentID   string `json:"action_intent_id"`
	PolicyDecisionID string `json:"policy_decision_id"`
	GateID           string `json:"gate_id"`
	GateStatus       string `json:"gate_status"`
	ReceiptID        string `json:"receipt_id,omitempty"`
	TransitionID     string `json:"transition_id,omitempty"`
}

func runProductWriteback(report product.DoctorReport, now time.Time) (writebackWorkResult, error) {
	var result writebackWorkResult
	if !product.NeedsWriteback(report) {
		return result, nil
	}
	homePath, err := home.Resolve("")
	if err != nil {
		return result, err
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		return result, err
	}
	writebackStore, err := writeback.Open(homePath)
	if err != nil {
		return result, err
	}
	w, err := workStore.Create(work.CreateInput{
		Owner:            "product_strategy:" + report.ProductID,
		DesiredOutcome:   "Route product feedback review through governed owner writeback",
		SuccessPredicate: "product feedback writeback gate recorded with receipt/transition refs",
		NonGoals: []string{
			"mutate product pages",
			"declare product causality",
			"execute next product action",
		},
		State:   "ready",
		Risk:    "tier1",
		Privacy: "private",
	}, now)
	if err != nil {
		return result, err
	}
	if _, err := workStore.Claim(w.ID, "compound product doctor", now); err != nil {
		return result, err
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{
		To:   "running",
		Note: "product doctor running writeback",
	}, now); err != nil {
		return result, err
	}
	evidenceRefs := product.EvidenceRefs(report)
	ai := envelope.ActionIntent{
		Header:        cliHeader(envelope.SVActionIntent, cliID("act_", "product-writeback", report.ProductID, strconv.Itoa(report.PendingReviewCount), strconv.Itoa(report.SourceIssueCount), cliTS(now)), w.ID, now, evidenceRefs),
		Actor:         "compound product doctor",
		Capability:    "product.writeback",
		Action:        "record_product_feedback_writeback_gate",
		Target:        envelope.ActionTarget{Kind: "product_strategy", ID: report.ProductID},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       fmt.Sprintf("product doctor found %d pending hypothesis review(s) and %d source issue(s); record owner writeback gate", report.PendingReviewCount, report.SourceIssueCount),
		RollbackPlan:  "remove COMPOUND_HOME writeback gate/work records before any product owner file or external surface is changed",
	}
	pd := policy.Evaluate(ai, policyConfig(), now)
	gate := product.WritebackGate(report, ai.ID, pd.ID, now)
	checked, err := writeback.EvaluateGate(gate, &pd, now)
	if err != nil {
		return result, err
	}
	result = writebackWorkResult{WorkID: w.ID, ActionIntentID: ai.ID, PolicyDecisionID: pd.ID, GateID: checked.ID, GateStatus: checked.Status}
	if !policy.Allowed(pd) || checked.Status != writeback.GateAllowed {
		closeRes, closeErr := workStore.Close(w.ID, work.CloseInput{
			Outcome:  "failed",
			MoveType: "audit_only",
			Summary:  fmt.Sprintf("product feedback writeback blocked for %s: gate %s", report.ProductID, checked.ID),
			Changed:  true,
		}, now)
		if closeErr == nil {
			checked.ReceiptRef = closeRes.Receipt.ID
			result.ReceiptID = closeRes.Receipt.ID
			result.TransitionID = closeRes.Transition.ID
		}
		if err := writebackStore.RecordGate(checked); err != nil {
			return result, err
		}
		return result, closeErr
	}
	if _, err := workStore.RecordAction(w.ID, ai, pd, now); err != nil {
		return result, err
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{
		To:             "running",
		ArtifactID:     checked.ID,
		VerificationID: checked.CandidateRef,
		Note:           "product hypothesis writeback gate checked",
	}, now); err != nil {
		return result, err
	}
	closeRes, err := workStore.Close(w.ID, work.CloseInput{
		Outcome:  "completed",
		MoveType: "outward_delta",
		Summary:  fmt.Sprintf("product %s feedback review routed through owner writeback gate %s", report.ProductID, checked.ID),
		Changed:  true,
	}, now)
	if err != nil {
		return result, err
	}
	checked.ReceiptRef = closeRes.Receipt.ID
	if err := writebackStore.RecordGate(checked); err != nil {
		return result, err
	}
	result.ReceiptID = closeRes.Receipt.ID
	result.TransitionID = closeRes.Transition.ID
	return result, nil
}

func runProjectionWriteback(report projection.Freshness, now time.Time) (writebackWorkResult, error) {
	var result writebackWorkResult
	if !projection.NeedsWriteback(report) {
		return result, nil
	}
	homePath, err := home.Resolve("")
	if err != nil {
		return result, err
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		return result, err
	}
	writebackStore, err := writeback.Open(homePath)
	if err != nil {
		return result, err
	}
	w, err := workStore.Create(work.CreateInput{
		Owner:            "projection:" + report.ProjectionID,
		DesiredOutcome:   "Route stale/drifted projection through governed owner writeback",
		SuccessPredicate: "writeback gate recorded with receipt/transition refs",
		NonGoals:         []string{"regenerate the projection output", "promote projection truth"},
		State:            "ready",
		Risk:             "tier1",
		Privacy:          "private",
	}, now)
	if err != nil {
		return result, err
	}
	if _, err := workStore.Claim(w.ID, "compound projection doctor", now); err != nil {
		return result, err
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{
		To:   "running",
		Note: "projection doctor running writeback",
	}, now); err != nil {
		return result, err
	}
	evidenceRefs := projection.EvidenceRefs(report)
	ai := envelope.ActionIntent{
		Header:        cliHeader(envelope.SVActionIntent, cliID("act_", "projection-writeback", report.ProjectionID, report.Status, cliTS(now)), w.ID, now, evidenceRefs),
		Actor:         "compound projection doctor",
		Capability:    "projection.writeback",
		Action:        "record_projection_writeback_gate",
		Target:        envelope.ActionTarget{Kind: "generated_view", ID: report.ProjectionID, Path: report.ViewRef},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       fmt.Sprintf("projection %s is %s; record owner writeback gate for %s", report.ProjectionID, report.Status, report.ViewRef),
		RollbackPlan:  "remove the COMPOUND_HOME writeback gate/work records before any owner file is changed",
	}
	pd := policy.Evaluate(ai, policyConfig(), now)
	gate := projection.WritebackGate(report, ai.ID, pd.ID, now)
	checked, err := writeback.EvaluateGate(gate, &pd, now)
	if err != nil {
		return result, err
	}
	result = writebackWorkResult{WorkID: w.ID, ActionIntentID: ai.ID, PolicyDecisionID: pd.ID, GateID: checked.ID, GateStatus: checked.Status}
	if !policy.Allowed(pd) || checked.Status != writeback.GateAllowed {
		closeRes, closeErr := workStore.Close(w.ID, work.CloseInput{
			Outcome:  "failed",
			MoveType: "audit_only",
			Summary:  fmt.Sprintf("projection writeback blocked for %s: gate %s", report.ProjectionID, checked.Status),
			Changed:  true,
		}, now)
		if closeErr == nil {
			checked.ReceiptRef = closeRes.Receipt.ID
			result.ReceiptID = closeRes.Receipt.ID
			result.TransitionID = closeRes.Transition.ID
		}
		if err := writebackStore.RecordGate(checked); err != nil {
			return result, err
		}
		return result, closeErr
	}
	if _, err := workStore.RecordAction(w.ID, ai, pd, now); err != nil {
		return result, err
	}
	if _, err := workStore.Advance(w.ID, work.AdvanceInput{
		To:             "running",
		ArtifactID:     checked.ID,
		VerificationID: "projection:" + report.ProjectionID + ":" + report.Status,
		Note:           "projection writeback gate checked",
	}, now); err != nil {
		return result, err
	}
	closeRes, err := workStore.Close(w.ID, work.CloseInput{
		Outcome:  "completed",
		MoveType: "control_plane_repair",
		Summary:  fmt.Sprintf("projection %s %s routed through owner writeback gate %s", report.ProjectionID, report.Status, checked.ID),
		Changed:  true,
	}, now)
	if err != nil {
		return result, err
	}
	checked.ReceiptRef = closeRes.Receipt.ID
	if err := writebackStore.RecordGate(checked); err != nil {
		return result, err
	}
	result.ReceiptID = closeRes.Receipt.ID
	result.TransitionID = closeRes.Transition.ID
	return result, nil
}

func runProjectionSourceObservation(report projection.Freshness, now time.Time) (sourceobs.Report, error) {
	var result sourceobs.Report
	if !projection.NeedsWriteback(report) {
		return result, nil
	}
	homePath, err := home.Resolve("")
	if err != nil {
		return result, err
	}
	obs := sourceobs.FromProjectionFreshness(report, now)
	return sourceobs.Apply(homePath, obs, now)
}

func cmdProjectionRepairPlan(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: compound projection repair-plan <freshness.yaml>")
		os.Exit(2)
	}
	now := time.Now()
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection repair-plan:", err)
		os.Exit(1)
	}
	report, err := projection.DoctorFile(args[0], now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection repair-plan:", err)
		os.Exit(1)
	}
	if !projection.NeedsWriteback(report) {
		fmt.Fprintf(os.Stderr, "projection repair-plan: projection %s is fresh; no repair plan needed\n", report.ProjectionID)
		os.Exit(1)
	}
	writebackResult, err := runProjectionWriteback(report, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection repair-plan:", err)
		os.Exit(1)
	}
	sourceReport, err := runProjectionSourceObservation(report, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection repair-plan:", err)
		os.Exit(1)
	}
	plan, err := projection.BuildRegenerationPlan(report, projection.RegenerationRefs{
		WritebackGateID:     writebackResult.GateID,
		WorkID:              writebackResult.WorkID,
		ReceiptID:           writebackResult.ReceiptID,
		TransitionID:        writebackResult.TransitionID,
		SourceObservationID: sourceReport.ObservationID,
		CandidateIDs:        sourceReport.CandidateIDs,
		NoPromoteIDs:        sourceReport.NoPromoteIDs,
	}, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection repair-plan:", err)
		os.Exit(1)
	}
	paths, err := projection.RecordRegenerationPlan(homePath, plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection repair-plan:", err)
		os.Exit(1)
	}
	fmt.Printf("# projection repair-plan\n\n")
	fmt.Printf("- plan: %s\n", plan.ID)
	fmt.Printf("- projection: %s\n", plan.ProjectionID)
	fmt.Printf("- status: %s\n", plan.Status)
	fmt.Printf("- freshness: %s\n", plan.FreshnessStatus)
	fmt.Printf("- writeback_gate: %s %s\n", writebackResult.GateID, writebackResult.GateStatus)
	fmt.Printf("- source_observation: %s\n", sourceReport.ObservationID)
	for _, id := range sourceReport.CandidateIDs {
		fmt.Printf("- source_candidate: %s\n", id)
	}
	for _, id := range sourceReport.NoPromoteIDs {
		fmt.Printf("- source_no_promote: %s\n", id)
	}
	fmt.Printf("- review: %s\n", paths.ReviewPath)
	fmt.Printf("- artifact: %s\n", paths.PlanPath)
}

func projectionUsage() string {
	return "usage: compound projection doctor <freshness.yaml> [--writeback] [--source-observe] | projection dashboard-read-model <read-model.json> [--max-age-hours N] [--writeback] [--source-observe] | projection repair-plan <freshness.yaml>"
}

func cmdProjectionDashboardReadModel(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: compound projection dashboard-read-model <read-model.json> [--max-age-hours N] [--writeback] [--source-observe]")
		os.Exit(2)
	}
	maxAgeHours := projection.DefaultDashboardMaxAgeHours
	if v := valueFlag(args[1:], "--max-age-hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			fmt.Fprintln(os.Stderr, "projection dashboard-read-model: --max-age-hours must be a positive integer")
			os.Exit(2)
		}
		maxAgeHours = n
	}
	now := time.Now()
	report, err := projection.DoctorDashboardReadModel(args[0], maxAgeHours, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection dashboard-read-model:", err)
		os.Exit(1)
	}
	printProjectionDoctorReport(report)
	finishProjectionDoctor(report, hasFlag(args[1:], "--writeback"), hasFlag(args[1:], "--source-observe"), now)
}

func printProjectionDoctorReport(report projection.Freshness) {
	fmt.Printf("# projection doctor\n\n")
	fmt.Printf("- projection: %s\n", report.ProjectionID)
	fmt.Printf("- view: %s\n", report.ViewRef)
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- source_ledger_seq: %d\n", report.SourceLedgerSeq)
	if report.Staleness != "" {
		fmt.Printf("- staleness: %s\n", report.Staleness)
	}
	fmt.Printf("- output_hash: %s\n", report.OutputHash)
	maxPrintedSources := 20
	if len(report.Sources) > maxPrintedSources {
		fmt.Printf("- sources: %d total (showing first %d)\n", len(report.Sources), maxPrintedSources)
	}
	for i, source := range report.Sources {
		if i >= maxPrintedSources {
			fmt.Printf("- source: ... %d more\n", len(report.Sources)-maxPrintedSources)
			break
		}
		fmt.Printf("- source: %s %s", source.SourceRef, source.Status)
		if source.CurrentHash != "" {
			fmt.Printf(" (%s)", source.CurrentHash)
		}
		fmt.Println()
	}
	for _, check := range report.Checks {
		fmt.Printf("- check:%s %s", check.Name, check.Status)
		if check.Detail != "" {
			fmt.Printf(" (%s)", check.Detail)
		}
		fmt.Println()
	}
}

func finishProjectionDoctor(report projection.Freshness, writebackEnabled, sourceObserveEnabled bool, now time.Time) {
	if report.Status != projection.StatusFresh {
		if writebackEnabled {
			result, err := runProjectionWriteback(report, now)
			if err != nil {
				fmt.Fprintln(os.Stderr, "projection doctor writeback:", err)
				os.Exit(1)
			}
			fmt.Printf("- writeback_work: %s\n", result.WorkID)
			fmt.Printf("- writeback_gate: %s %s\n", result.GateID, result.GateStatus)
			if result.ReceiptID != "" {
				fmt.Printf("- receipt: %s\n", result.ReceiptID)
			}
			if result.TransitionID != "" {
				fmt.Printf("- transition: %s\n", result.TransitionID)
			}
		}
		if sourceObserveEnabled {
			sourceReport, err := runProjectionSourceObservation(report, now)
			if err != nil {
				fmt.Fprintln(os.Stderr, "projection doctor source-observe:", err)
				os.Exit(1)
			}
			fmt.Printf("- source_observation: %s\n", sourceReport.ObservationID)
			for _, id := range sourceReport.CandidateIDs {
				fmt.Printf("- source_candidate: %s\n", id)
			}
			for _, id := range sourceReport.NoPromoteIDs {
				fmt.Printf("- source_no_promote: %s\n", id)
			}
		}
		if writebackEnabled || sourceObserveEnabled {
			return
		}
		os.Exit(1)
	}
}

func cmdProjection(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, projectionUsage())
		os.Exit(2)
	}
	if args[0] == "repair-plan" {
		cmdProjectionRepairPlan(args[1:])
		return
	}
	if args[0] == "dashboard-read-model" {
		cmdProjectionDashboardReadModel(args[1:])
		return
	}
	if args[0] != "doctor" {
		fmt.Fprintln(os.Stderr, projectionUsage())
		os.Exit(2)
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: compound projection doctor <freshness.yaml> [--writeback] [--source-observe]")
		os.Exit(2)
	}
	now := time.Now()
	writebackEnabled := hasFlag(args[2:], "--writeback")
	sourceObserveEnabled := hasFlag(args[2:], "--source-observe")
	report, err := projection.DoctorFile(args[1], now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection doctor:", err)
		os.Exit(1)
	}
	fmt.Printf("# projection doctor\n\n")
	fmt.Printf("- projection: %s\n", report.ProjectionID)
	fmt.Printf("- view: %s\n", report.ViewRef)
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- source_ledger_seq: %d\n", report.SourceLedgerSeq)
	if report.Staleness != "" {
		fmt.Printf("- staleness: %s\n", report.Staleness)
	}
	fmt.Printf("- output_hash: %s\n", report.OutputHash)
	for _, source := range report.Sources {
		fmt.Printf("- source: %s %s", source.SourceRef, source.Status)
		if source.CurrentHash != "" {
			fmt.Printf(" (%s)", source.CurrentHash)
		}
		fmt.Println()
	}
	for _, check := range report.Checks {
		fmt.Printf("- check:%s %s", check.Name, check.Status)
		if check.Detail != "" {
			fmt.Printf(" (%s)", check.Detail)
		}
		fmt.Println()
	}
	if report.Status != projection.StatusFresh {
		if writebackEnabled {
			result, err := runProjectionWriteback(report, now)
			if err != nil {
				fmt.Fprintln(os.Stderr, "projection writeback:", err)
				os.Exit(1)
			}
			fmt.Printf("- writeback_gate: %s %s\n", result.GateID, result.GateStatus)
			fmt.Printf("- work: %s\n", result.WorkID)
			if result.ReceiptID != "" {
				fmt.Printf("- receipt: %s\n", result.ReceiptID)
			}
			if result.TransitionID != "" {
				fmt.Printf("- transition: %s\n", result.TransitionID)
			}
		}
		if sourceObserveEnabled {
			sourceReport, err := runProjectionSourceObservation(report, now)
			if err != nil {
				fmt.Fprintln(os.Stderr, "projection source observe:", err)
				os.Exit(1)
			}
			fmt.Printf("- source_observation: %s\n", sourceReport.ObservationID)
			for _, id := range sourceReport.CandidateIDs {
				fmt.Printf("- source_candidate: %s\n", id)
			}
			for _, id := range sourceReport.NoPromoteIDs {
				fmt.Printf("- source_no_promote: %s\n", id)
			}
		}
		if writebackEnabled || sourceObserveEnabled {
			return
		}
		os.Exit(1)
	}
}
