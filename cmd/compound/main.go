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
	"sort"
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
	"compound/internal/reviewartifact"
	"compound/internal/routeevidence"
	"compound/internal/runtimebinding"
	"compound/internal/schema"
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
	case "schema":
		cmdSchema(os.Args[2:])
	case "contract":
		cmdContract(os.Args[2:])
	case "orient":
		cmdOrient(os.Args[2:])
	case "work":
		cmdWork(os.Args[2:])
	case "workbench":
		cmdWorkbench(os.Args[2:])
	case "reviews":
		cmdReviews(os.Args[2:])
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

func sortedMapKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func decisionSourceKind(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "note"
	}
	return raw
}

func importDecisionSource(store *mem.Store, path, sourceKind string, now time.Time) (decisions.BrainDecisionReport, error) {
	switch sourceKind {
	case "note":
		return decisions.ImportNoteDecision(store, path, now)
	default:
		return decisions.BrainDecisionReport{}, fmt.Errorf("unsupported decision source kind %q (supported: note)", sourceKind)
	}
}

func importDecisionSourceDir(store *mem.Store, root, sourceKind string, now time.Time) ([]decisions.BrainDecisionReport, error) {
	switch sourceKind {
	case "note":
		return decisions.ImportNoteDecisionDir(store, root, now)
	default:
		return nil, fmt.Errorf("unsupported decision source kind %q (supported: note)", sourceKind)
	}
}

func eligibleDecisionSourceFiles(root, sourceKind string) ([]string, error) {
	switch sourceKind {
	case "note":
		return decisions.EligibleNoteDecisionFiles(root)
	default:
		return nil, fmt.Errorf("unsupported decision source kind %q (supported: note)", sourceKind)
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
  compound home <init|doctor|where|backup|export|verify|restore|compact> [--home P] [--local] COMPOUND_HOME runtime body + archive/restore
  compound envelope <validate <file>...|kinds>                  typed runtime envelopes
  compound schema <list|export|validate>                        JSON Schema export + contract-aware validation
  compound contract <compile|import-hooks|hooks-authority> ...  EffectiveContract and installed hook authority
  compound orient --harness H [--cwd D] [--task T]              ContextPack orient from records excluded
  compound work <create-plan|create|import-packet|show|list|dispatch-plan|dispatch-claim|dispatch-advance|dispatch-close-plan|dispatch-close|dispatch-step|claim-plan|claim|advance-plan|advance|observe-run|close-plan|close|act> one work_id: intent -> context -> action -> receipt -> transition
  compound workbench [--json] [--recent N] read-only open work / receipt / transition board; no dispatch
  compound reviews [--json] [--area A] read-only owner-review artifact queue; no apply
  compound reviews record-finding (--work-id W|--auto-work) --target state/runs/.../finding.json --summary S [--root DIR] [--capability review.finding] [--source-artifact state/runs/.../artifact.json] [--source-trace trace_id] [--evidence-ref REF...]
  compound policy <evaluate|import-placement-rules|placement-authority|import-standing-grants|standing-grants-authority|import-operator-state|operator-authority|import-operator-prompts|operator-prompts-authority|import-operator-approval-frontier|operator-approval-frontier-authority|import-release-goal-holds|release-goal-holds-authority> ActionIntent -> PolicyDecision; import policy authorities
  compound hook <name> [--enforce] <fixture.json>               run lifecycle hook over fixture; --enforce exits nonzero on pre-tool holds
  compound harness <list|NAME|observe [registry.yaml|--os-entrypoints file]> HarnessRegistry capabilities + limits; observe records capability evidence/no-promote
  compound transition show <id>                                 inspect a Transition or envelope
  compound route import <execution-route-evidence.json>          import route metadata -> Transition projection
  compound route import-model-capabilities <model-capabilities.yaml> OS model router YAML -> Compound route authority
  compound route evidence-authority [--json] [--recent N] read-only route evidence authority
  compound route import-provider-ledger <provider-calls.ndjson> [--limit N|--tail N] provider-call NDJSON -> route/cost projections
  compound route cost-doctor <provider-calls.ndjson> [--limit N|--tail N] [--record] provider cost coverage -> route observation/no-promote
  compound route outcome-review (--route-id ID|--route-evidence file) --receipt file [--target file] [--record] join route evidence to work outcome
  compound route plan --candidate C [--target file]              route_observation candidate -> reviewed routing-policy draft
  compound route plan --capability-class C [--privacy-sensitive] planned lane chain from Compound route authority
  compound memory <candidate|import-page|adjudicate|recall|timeline|query|engine-doctor|engine-compare [--allow-expansion]|doctor>
                                                                 spine memory: candidates, claims, positions, patterns, evidence
  compound distiller eval <case.json|cases-dir> [--record]    Offline request/response/adjudication eval corpus, no model call
  compound distiller eval-plan --candidate C [--corpus dir]   eval_case candidate -> reviewed eval fixture draft, no corpus mutation
  compound distiller improvement-plan --candidate C [--target file] method_upgrade/policy_patch -> reviewed distiller improvement draft
  compound distiller improvement-review <review.yaml>           validate reviewed distiller improvement decision, no source mutation
  compound distiller rule-plan --candidate C [--target file]  failure_pattern candidate -> reviewed rule draft, no doctrine mutation
  compound distiller taste-plan --candidate C [--target file] taste candidate -> reviewed taste standard draft, no doctrine mutation
  compound distiller request --trace-ref T [--work-id W] [--raw f] [--context-pack f] [--work f] [--receipt f] [--envelope f]... [--event-trace trace_id]...
                                                                 TraceDistiller LLM request packet, no model call
  compound distiller extract --request f --eval-corpus dir --openai-compatible-url URL --model M [--api-key-env ENV]
                                                                 gated rented model extraction; provider output stays candidate-only
  compound distiller import-conversation-coverage <summary.json>
                                                                 conversation corpus coverage -> SourceObservation/no-promote; no row import, Dream execution, or memory promotion
  compound distiller import-conversation-mining <report.json> [--mined-tags rows.jsonl] [--lossiness lossiness.json]
                                                                 mined conversation source quality -> SourceObservation/no-promote; no Dream execution or memory promotion
  compound distiller run [--llm-response] <trace.json> | doctor [--writeback] | prior-state Trace -> candidate fan-out, adjudication, prior state
  compound capability <add|surface|observe|consume|list|authority|external-intake|tool-boundary-doctor|skill-source-doctor|import-topology|import-skills|import-evaluations|import-surfacing-misses|evaluation-plan|evaluation-review|skill-plan|skill-candidate-review|skill-candidate-decision> capability graph: auto-surface, never auto-execute; tool-boundary-doctor checks MCP/tool/plugin inventory stays observed/non-executing; skill-source-doctor validates raw SKILL.md sources read-only; imports accept normalized files, OS generated JSON, mcporter JSON, capability-evaluation records, or skill surfacing feedback reports
  compound authority import [f] | path <f> | path-registry <f> | git <repo> | dirty-plan <repo> [--work-id id] | import-job-roster <f> | import-job-token-governance <f> | job-roster-authority [--json] | job-health-plan | job-health-review <review.yaml> | resolve <token> | <entity> import/query authority through spine memory
  compound decisions import [--source <md>] [--source-dir <dir>] [--source-kind note] [--brain <md>] [--policy <DEC.md>] [--policy-dir <dir>] | decisions authority [--source <md>] [--source-dir <dir>] [--source-kind note] [--policy <DEC.md>] [--policy-dir <dir>] | decisions candidate-plan --candidate C [--target file] | decisions [--kind=K] | decisions doctor | decisions source-doctor --template <md> [--format-doc <md>] | decisions repair-plan [--policy-dir <dir>] [--source <md>] [--source-dir <dir>] [--source-kind note] [--brain <md>] | decisions repair-apply --plan <json> [--write-reviewed]
                                                                  import/query decisions through spine memory; verify source format; emit/apply lifecycle repair artifacts
compound runtime doctor [--project-contract] <yaml> [--live] [--writeback] | runtime repair-plan [--project-contract] <yaml> [--live] RuntimeBinding health/live probes + gated writeback/review artifacts
compound projection doctor <freshness.yaml> [--writeback] [--source-observe] | projection dashboard-read-model <read-model.json> [--max-age-hours N] [--writeback] [--source-observe] | projection repair-plan <freshness.yaml>
                                                               ProjectionFreshness + gated writeback/source observation/regeneration review
compound product import <product.yaml>|--project-contract <project.yaml> [--release <release.yaml>] | signal <signal.yaml>|--asc-datapoints <json> --product <id> --app-id <asc_id> [--metric <metric>]|--asc-sales-series <jsonl> --product <id> --app-id <asc_id> --before <range> --after <range> --change-event <text> [--metric <metric>] | canonical-metrics <json> --product <id> | hypothesis-plan --candidate C [--target file] | source-issue-plan --product P --issue I [--target file] | review <review.yaml> | source-issue-review <review.yaml> | timeline <product> | doctor <product> [--writeback]
                                                               Product registry + signal/hypothesis loop
compound source observe <source-observation.yaml>              SourceObservation -> inbox candidate + no-promote
compound event import-os <events.ndjson> | event authority [--json] OS-style append-only events -> TraceEnvelope evidence; optional trace-backed review finding producer
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
		fmt.Fprintln(os.Stderr, "usage: compound capability <add|surface|observe|consume|list|authority|external-intake|tool-boundary-doctor|skill-source-doctor|import-topology|import-skills|import-evaluations|import-surfacing-misses|evaluation-plan|evaluation-review|skill-plan|skill-candidate-review|skill-candidate-decision> ...")
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
	case "tool-boundary-doctor":
		report, err := tooltopology.DoctorBoundary(homePath)
		die(err)
		if hasFlag(rest, "--json") {
			printJSON(report)
			return
		}
		fmt.Printf("# capability tool-boundary-doctor %s\n\n", report.Status)
		fmt.Printf("- authority: %s\n", report.AuthorityRef)
		fmt.Printf("- nodes: %d\n", report.NodeCount)
		fmt.Printf("- mcp: %d\n", report.MCPCount)
		fmt.Printf("- tools: %d\n", report.ToolCount)
		fmt.Printf("- plugin_adapters: %d retired=%d surfaced=%d\n", report.PluginAdapterCount, report.RetiredPluginAdapters, report.PluginSurfaceCount)
		for _, check := range report.Checks {
			fmt.Printf("- %s: %s", check.Name, check.Status)
			if check.Detail != "" {
				fmt.Printf(" (%s)", check.Detail)
			}
			fmt.Println()
		}
		if report.Status != "ok" {
			os.Exit(1)
		}
	case "skill-source-doctor":
		jsonOut := hasFlag(rest, "--json")
		p := posn(map[string]bool{})
		if len(p) != 1 {
			fmt.Fprintln(os.Stderr, "usage: compound capability skill-source-doctor <skill-root-or-SKILL.md> [--json]")
			os.Exit(2)
		}
		report, err := capgraph.DoctorSkillSource(p[0])
		die(err)
		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", " ")
			die(enc.Encode(report))
			if report.Status != "ok" {
				os.Exit(1)
			}
			return
		}
		fmt.Printf("# capability skill-source-doctor %s\n\n", report.Root)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- skill_files: %d\n", report.SkillFileCount)
		fmt.Printf("- errors: %d\n", report.ErrorCount)
		for _, check := range report.Checks {
			fmt.Printf("- %s: %s", check.Name, check.Status)
			if check.Detail != "" {
				fmt.Printf(" (%s)", check.Detail)
			}
			fmt.Println()
		}
		for _, file := range report.Files {
			if file.Status == "ok" {
				continue
			}
			fmt.Printf("- file: %s\n", file.Path)
			for _, msg := range file.Errors {
				fmt.Printf("  - %s\n", msg)
			}
		}
		if report.Status != "ok" {
			os.Exit(1)
		}
	case "import-topology":
		now := time.Now()
		osResolverPath := flag("--os-resolver-json")
		mcporterPath := flag("--mcporter-json")
		p := posn(map[string]bool{"--os-resolver-json": true, "--mcporter-json": true})
		var topo tooltopology.Topology
		var err error
		switch {
		case osResolverPath != "" && len(p) == 0:
			topo, err = tooltopology.LoadOSResolver(osResolverPath, now)
		case mcporterPath != "" && len(p) == 0:
			topo, err = tooltopology.LoadMCPorterCatalog(mcporterPath, now)
		case osResolverPath == "" && mcporterPath == "" && len(p) == 1:
			topo, err = tooltopology.Load(p[0])
		default:
			fmt.Fprintln(os.Stderr, "usage: compound capability import-topology <tool-topology.yaml> | --os-resolver-json <tool-resolver.json> | --mcporter-json <mcporter-list.json>")
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
		auth, err := capgraph.RecordSourceAuthority(homePath, capgraph.SourceAuthorityInput{
			SourcePath:          topo.SourceRef.Path,
			SourceKind:          topo.SourceRef.Kind,
			SourceOwner:         topo.SourceRef.Owner,
			SourceClass:         topo.SourceRef.SourceClass,
			SourceRevision:      topo.SourceRef.Revision,
			ImportedVia:         "compound capability import-topology",
			NodeIDs:             report.NodeIDs,
			EdgeIDs:             report.EdgeIDs,
			SourceObservationID: sourceReport.ObservationID,
			NoPromoteIDs:        sourceReport.NoPromoteIDs,
			EvidenceRefs:        topo.EvidenceRefs,
		}, now)
		die(err)
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
		fmt.Printf("- source_authority: %s\n", auth.ID)
	case "import-skills":
		now := time.Now()
		osSkillPath := flag("--os-skill-json")
		osPluginBoardPath := flag("--os-plugin-board-json")
		p := posn(map[string]bool{"--os-skill-json": true, "--os-plugin-board-json": true})
		var manifest capgraph.SkillManifest
		var err error
		switch {
		case osSkillPath != "" && len(p) == 0:
			manifest, err = capgraph.LoadOSSkillManifest(osSkillPath, now)
		case osPluginBoardPath != "" && len(p) == 0:
			manifest, err = capgraph.LoadOSPluginBoard(osPluginBoardPath, now)
		case osSkillPath == "" && osPluginBoardPath == "" && len(p) == 1:
			manifest, err = capgraph.LoadSkillManifest(p[0])
		default:
			fmt.Fprintln(os.Stderr, "usage: compound capability import-skills <skill-manifest.yaml> | --os-skill-json <skill-manifest.json> | --os-plugin-board-json <skills-selection-board.json>")
			os.Exit(2)
		}
		die(err)
		report, err := capgraph.ApplySkillManifest(homePath, manifest, now)
		die(err)
		sourceReport, err := sourceobs.Apply(homePath, sourceobs.FromSkillManifest(manifest, now), now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "source observe:", err)
			os.Exit(1)
		}
		auth, err := capgraph.RecordSourceAuthority(homePath, capgraph.SourceAuthorityInput{
			SourcePath:          manifest.SourceRef.Path,
			SourceKind:          manifest.SourceRef.Kind,
			SourceOwner:         manifest.SourceRef.Owner,
			SourceClass:         manifest.SourceRef.SourceClass,
			SourceRevision:      manifest.SourceRef.Revision,
			ImportedVia:         "compound capability import-skills",
			NodeIDs:             report.NodeIDs,
			EdgeIDs:             report.EdgeIDs,
			SourceObservationID: sourceReport.ObservationID,
			NoPromoteIDs:        sourceReport.NoPromoteIDs,
			EvidenceRefs:        manifest.EvidenceRefs,
		}, now)
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
		fmt.Printf("- source_observation: %s\n", sourceReport.ObservationID)
		for _, id := range sourceReport.CandidateIDs {
			fmt.Printf("- source_candidate: %s\n", id)
		}
		for _, id := range sourceReport.NoPromoteIDs {
			fmt.Printf("- source_no_promote: %s\n", id)
		}
		fmt.Printf("- source_authority: %s\n", auth.ID)
	case "authority":
		records, err := capgraph.LoadSourceAuthority(homePath)
		die(err)
		stats, err := store.Stats()
		die(err)
		sourceNodeCount := 0
		sourceEdgeCount := 0
		for _, rec := range records {
			sourceNodeCount += len(rec.NodeIDs)
			sourceEdgeCount += len(rec.EdgeIDs)
		}
		if hasFlag(rest, "--json") {
			out := struct {
				SchemaVersion     string                           `json:"schema_version"`
				AuthorityRef      string                           `json:"authority_ref"`
				NodeCount         int                              `json:"node_count"`
				EdgeCount         int                              `json:"edge_count"`
				EdgeKindCounts    map[string]int                   `json:"edge_kind_counts,omitempty"`
				SourceRecordCount int                              `json:"source_record_count"`
				SourceNodeCount   int                              `json:"source_node_count"`
				SourceEdgeCount   int                              `json:"source_edge_count"`
				Records           []capgraph.SourceAuthorityRecord `json:"records"`
			}{
				SchemaVersion:     capgraph.SourceAuthoritySchemaVersion,
				AuthorityRef:      "compound://registries/capability-graph",
				NodeCount:         stats.NodeCount,
				EdgeCount:         stats.EdgeCount,
				EdgeKindCounts:    stats.EdgeKindCounts,
				SourceRecordCount: len(records),
				SourceNodeCount:   sourceNodeCount,
				SourceEdgeCount:   sourceEdgeCount,
				Records:           records,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			die(enc.Encode(out))
			return
		}
		if len(records) == 0 {
			fmt.Println("(no capability source authority records)")
			return
		}
		fmt.Printf("compound://registries/capability-graph nodes=%d edges=%d source_records=%d source_nodes=%d source_edges=%d\n", stats.NodeCount, stats.EdgeCount, len(records), sourceNodeCount, sourceEdgeCount)
		if len(stats.EdgeKindCounts) > 0 {
			fmt.Printf("edge_kinds=%s\n", formatEdgeKindCounts(stats.EdgeKindCounts))
		}
		for _, rec := range records {
			fmt.Printf("%s %s source=%s authority=%s nodes=%d edges=%d retires=%s\n",
				rec.ID,
				rec.AuthorityStatus,
				rec.SourcePath,
				rec.AuthorityRef,
				len(rec.NodeIDs),
				len(rec.EdgeIDs),
				rec.Retires,
			)
		}
	case "external-intake":
		repoQuery := flagValue(rest, "--repo")
		report, err := capgraph.ExternalRepoIntakeReportForHome(homePath, repoQuery)
		die(err)
		if hasFlag(rest, "--json") {
			printJSON(report)
			return
		}
		fmt.Printf("# capability external-intake %s\n", report.Status)
		fmt.Printf("- authority: %s\n", report.AuthorityRef)
		fmt.Printf("- batches: %d\n", report.BatchCount)
		if report.LatestBatchID != "" {
			fmt.Printf("- latest_batch: %s\n", report.LatestBatchID)
		}
		fmt.Printf("- candidates: %d\n", report.CandidateCount)
		fmt.Printf("- keep_needs_review: %d\n", report.OpenReviewCount)
		fmt.Printf("- merge_existing: %d\n", report.MergeExistingCount)
		fmt.Printf("- reference_only: %d\n", report.ReferenceOnlyCount)
		if report.SourceObservationID != "" {
			fmt.Printf("- source_observation: %s\n", report.SourceObservationID)
		}
		if report.RepoQuery != "" {
			fmt.Printf("- repo_query: %s matches=%d\n", report.RepoQuery, len(report.Matches))
			for _, row := range report.Matches {
				fmt.Printf("- repo_match: %s decision=%s lane=%s owner=%s next=%s boundary=%s\n", row.Repo, row.Decision, row.Lane, row.Owner, row.NextReviewStep, row.NotToDo)
			}
			return
		}
		for _, row := range report.HighValue {
			fmt.Printf("- review_next: %s decision=%s lane=%s owner=%s\n", row.Repo, row.Decision, row.Lane, row.Owner)
		}
	case "import-evaluations":
		p := posn(nil)
		if len(p) != 1 {
			fmt.Fprintln(os.Stderr, "usage: compound capability import-evaluations <capability-evaluation.json|dir>")
			os.Exit(2)
		}
		now := time.Now()
		set, err := capgraph.LoadCapabilityEvaluations(p[0])
		die(err)
		report, err := capgraph.ApplyCapabilityEvaluations(homePath, set, now)
		die(err)
		sourceReport, err := sourceobs.Apply(homePath, sourceobs.FromCapabilityEvaluations(set, now), now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "source observe:", err)
			os.Exit(1)
		}
		fmt.Printf("# capability import-evaluations %s\n", report.EvaluationSetID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- evaluations_read: %d\n", report.EvaluationsRead)
		fmt.Printf("- applied_evaluations: %d\n", report.AppliedEvaluations)
		fmt.Printf("- skipped_unknown_count: %d\n", len(report.SkippedUnknown))
		for _, id := range report.EvaluationNodeIDs {
			fmt.Printf("- eval_node: %s\n", id)
		}
		for _, id := range report.EdgeIDs {
			fmt.Printf("- edge: %s\n", id)
		}
		for _, id := range report.SkippedUnknown {
			fmt.Printf("- skipped_unknown: %s\n", id)
		}
		fmt.Printf("- source_observation: %s\n", sourceReport.ObservationID)
		for _, id := range sourceReport.CandidateIDs {
			fmt.Printf("- source_candidate: %s\n", id)
		}
		for _, id := range sourceReport.NoPromoteIDs {
			fmt.Printf("- source_no_promote: %s\n", id)
		}
	case "import-surfacing-misses":
		p := posn(nil)
		if len(p) != 1 {
			fmt.Fprintln(os.Stderr, "usage: compound capability import-surfacing-misses <skill-surfacing-feedback.json>")
			os.Exit(2)
		}
		now := time.Now()
		misses, err := capgraph.LoadSkillSurfacingMisses(p[0])
		die(err)
		report, err := capgraph.ApplySkillSurfacingMisses(homePath, misses, now)
		die(err)
		sourceReport, err := sourceobs.Apply(homePath, sourceobs.FromSkillSurfacingMisses(misses, now), now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "source observe:", err)
			os.Exit(1)
		}
		fmt.Printf("# capability import-surfacing-misses %s\n", report.ReportID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- misses_read: %d\n", report.MissesRead)
		fmt.Printf("- false_triggers_read: %d\n", report.FalseTriggersRead)
		fmt.Printf("- applied_misses: %d\n", report.AppliedMisses)
		fmt.Printf("- applied_false_triggers: %d\n", report.AppliedFalseTriggers)
		fmt.Printf("- applied_edges: %d\n", report.AppliedEdges)
		fmt.Printf("- skipped_unknown_count: %d\n", len(report.SkippedUnknown))
		for _, id := range report.EdgeIDs {
			fmt.Printf("- edge: %s\n", id)
		}
		for _, id := range report.SkippedUnknown {
			fmt.Printf("- skipped_unknown: %s\n", id)
		}
		for _, id := range report.SkippedMissingContext {
			fmt.Printf("- skipped_missing_context: %s\n", id)
		}
		fmt.Printf("- source_observation: %s\n", sourceReport.ObservationID)
		for _, id := range sourceReport.CandidateIDs {
			fmt.Printf("- source_candidate: %s\n", id)
		}
		for _, id := range sourceReport.NoPromoteIDs {
			fmt.Printf("- source_no_promote: %s\n", id)
		}
	case "evaluation-plan":
		capabilityName := flag("--capability")
		targetPath := flag("--target")
		p := posn(map[string]bool{"--capability": true, "--target": true})
		if len(p) != 1 || capabilityName == "" {
			fmt.Fprintln(os.Stderr, "usage: compound capability evaluation-plan <capability-evaluation.json|dir> --capability <id-or-name> [--target file]")
			os.Exit(2)
		}
		now := time.Now()
		set, err := capgraph.LoadCapabilityEvaluations(p[0])
		die(err)
		plan, err := capgraph.BuildCapabilityEvaluationPlan(homePath, set, capabilityName, targetPath, now)
		die(err)
		paths, err := capgraph.RecordCapabilityEvaluationPlan(homePath, plan)
		die(err)
		fmt.Printf("# capability evaluation-plan %s\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- evaluation_set: %s\n", plan.EvaluationSet)
		fmt.Printf("- evaluation: %s\n", plan.EvaluationID)
		fmt.Printf("- capability: %s\n", plan.Capability)
		fmt.Printf("- verdict: %s\n", plan.Verdict)
		fmt.Printf("- node_draft: %s\n", plan.NodeDraft.NodeID)
		fmt.Printf("- node_kind: %s\n", plan.NodeDraft.Kind)
		fmt.Printf("- required_evidence: %d\n", len(plan.NodeDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		fmt.Printf("- review_yaml: %s\n", paths.ReviewYAMLPath)
		fmt.Printf("- manifest_draft: %s\n", paths.ManifestDraftPath)
	case "evaluation-review":
		p := posn(nil)
		if len(p) != 1 {
			fmt.Fprintln(os.Stderr, "usage: compound capability evaluation-review <review.yaml>")
			os.Exit(2)
		}
		review, err := capgraph.LoadCapabilityEvaluationReview(p[0])
		die(err)
		report, err := capgraph.RecordCapabilityEvaluationReview(homePath, review, p[0], time.Now())
		die(err)
		fmt.Printf("# capability evaluation-review %s\n", report.ReviewID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- decision: %s\n", report.Decision)
		fmt.Printf("- node: %s\n", report.NodeID)
		fmt.Printf("- node_status: %s\n", report.NodeStatus)
		fmt.Printf("- manifest: %s\n", report.ManifestPath)
		if report.PlanPath != "" {
			fmt.Printf("- plan: %s\n", report.PlanPath)
		}
		if report.ReviewedPath != "" {
			fmt.Printf("- reviewed: %s\n", report.ReviewedPath)
		}
	case "skill-candidate-review":
		targetPath := flag("--target")
		jsonOut := hasFlag(rest, "--json")
		p := posn(map[string]bool{"--target": true})
		if len(p) != 1 {
			fmt.Fprintln(os.Stderr, "usage: compound capability skill-candidate-review <external-skill-candidate.json> [--target <file-or-dir>] [--json]")
			os.Exit(2)
		}
		review, err := capgraph.BuildExternalSkillCandidateReview(homePath, p[0], targetPath, time.Now())
		die(err)
		paths, err := capgraph.RecordExternalSkillCandidateReview(homePath, review)
		die(err)
		storedReview, err := capgraph.LoadSkillCandidateReview(paths.PlanPath)
		die(err)
		review = storedReview
		if jsonOut {
			printJSON(struct {
				Review capgraph.SkillCandidateReview `json:"review"`
				Paths  struct {
					PlanPath       string `json:"plan_path"`
					ReviewPath     string `json:"review_path"`
					ReviewYAMLPath string `json:"review_yaml_path"`
				} `json:"paths"`
			}{
				Review: review,
				Paths: struct {
					PlanPath       string `json:"plan_path"`
					ReviewPath     string `json:"review_path"`
					ReviewYAMLPath string `json:"review_yaml_path"`
				}{
					PlanPath:       paths.PlanPath,
					ReviewPath:     paths.ReviewPath,
					ReviewYAMLPath: paths.ReviewYAMLPath,
				},
			})
			return
		}
		fmt.Printf("# capability skill-candidate-review %s\n", review.ID)
		fmt.Printf("- status: %s\n", review.Status)
		fmt.Printf("- candidate_kind: %s\n", review.CandidateKind)
		fmt.Printf("- candidate: %s\n", review.CandidateID)
		fmt.Printf("- classification: %s\n", review.ProposedClassification)
		fmt.Printf("- lifecycle_stage: %s\n", review.LifecycleStage)
		fmt.Printf("- disposition: %s\n", review.RecommendedDisposition)
		fmt.Printf("- overlaps: %d\n", len(review.OverlapRefs))
		fmt.Printf("- evidence_required: %d\n", len(review.EvidenceRequired))
		fmt.Printf("- eval_suggestions: %d\n", len(review.EvalFixtureSuggestions))
		if review.TargetPath != "" {
			fmt.Printf("- target: %s\n", review.TargetPath)
			fmt.Printf("- target_exists: %t\n", review.TargetExists)
		}
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		fmt.Printf("- review_yaml: %s\n", paths.ReviewYAMLPath)
	case "skill-candidate-decision":
		jsonOut := hasFlag(rest, "--json")
		p := posn(nil)
		if len(p) != 1 {
			fmt.Fprintln(os.Stderr, "usage: compound capability skill-candidate-decision <review.yaml> [--json]")
			os.Exit(2)
		}
		decision, err := capgraph.LoadSkillCandidateDecision(p[0])
		die(err)
		report, err := capgraph.RecordSkillCandidateDecision(homePath, decision, p[0], time.Now())
		die(err)
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Printf("# capability skill-candidate-decision %s\n", report.ID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- review: %s\n", report.ReviewID)
		fmt.Printf("- decision: %s\n", report.Decision)
		if report.CandidateID != "" {
			fmt.Printf("- candidate: %s\n", report.CandidateID)
			fmt.Printf("- candidate_path: %s\n", report.CandidatePath)
		}
	case "skill-plan":
		candidateID := flag("--candidate")
		targetPath := flag("--target")
		if candidateID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound capability skill-plan --candidate <candidate_id> [--target <file>]")
			os.Exit(2)
		}
		plan, err := capgraph.BuildSkillPlan(homePath, candidateID, targetPath, time.Now())
		die(err)
		paths, err := capgraph.RecordSkillPlan(homePath, plan)
		die(err)
		fmt.Printf("# capability skill-plan %s\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- candidate: %s\n", plan.CandidateID)
		fmt.Printf("- skill_key: %s\n", plan.SkillKey)
		fmt.Printf("- node_id: %s\n", plan.NodeDraft.NodeID)
		if plan.TargetPath != "" {
			fmt.Printf("- target: %s\n", plan.TargetPath)
			fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		}
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- triggers: %d\n", len(plan.NodeDraft.Triggers))
		fmt.Printf("- required_evidence: %d\n", len(plan.NodeDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
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
		jsonOut := hasFlag(rest, "--json")
		harness := flag("--harness")
		p := posn(map[string]bool{"--harness": true})
		if len(p) == 0 {
			fmt.Fprintln(os.Stderr, `usage: compound capability surface [--harness codex|claude_code] [--json] "<task>"`)
			os.Exit(2)
		}
		var hits []capgraph.Surfaced
		var err error
		if harness != "" {
			hits, err = store.SurfaceForHarness(p[0], harness)
		} else {
			hits, err = store.Surface(p[0])
		}
		die(err)
		if jsonOut {
			out := struct {
				SchemaVersion string              `json:"schema_version"`
				AuthorityRef  string              `json:"authority_ref"`
				Task          string              `json:"task"`
				Harness       string              `json:"harness,omitempty"`
				Offers        []capgraph.Surfaced `json:"offers"`
			}{
				SchemaVersion: "compound.capability_surface.v0.1",
				AuthorityRef:  "compound://registries/capability-graph",
				Task:          p[0],
				Harness:       harness,
				Offers:        hits,
			}
			die(json.NewEncoder(os.Stdout).Encode(out))
			return
		}
		fmt.Printf("# %d capabilit(ies) surfaced for %q (offered, NOT executed)\n", len(hits), p[0])
		for _, h := range hits {
			fmt.Printf("  %-28s %-12s score %.1f  [%s]\n", h.Node.ID, h.Node.Kind, h.Score, h.Why)
			printCapabilityConstraints(h.Constraints)
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
		fmt.Printf("- skipped_invalid_receipt: %d\n", report.SkippedInvalidReceipt)
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
		fmt.Fprintln(os.Stderr, "usage: compound capability <add|surface|observe|consume|consume-receipts|list|authority|external-intake|skill-source-doctor|import-topology|import-skills|import-evaluations|import-surfacing-misses|evaluation-plan|evaluation-review|skill-plan|skill-candidate-review|skill-candidate-decision> ...")
		os.Exit(2)
	}
}

func printCapabilityConstraints(c capgraph.SurfacingConstraints) {
	printCapabilityConstraintRefs("requires", c.Required)
	printCapabilityConstraintRefs("conflicts", c.Conflicts)
	printCapabilityConstraintRefs("harnesses", c.Harnesses)
	printCapabilityConstraintRefs("missed_when", c.KnownMissedWhen)
	printCapabilityConstraintRefs("false_triggered_by", c.KnownFalseTriggeredBy)
	printCapabilityConstraintRefs("evidence", c.Evidence)
	if c.CostRiskNote != "" {
		fmt.Printf("    note: %s\n", c.CostRiskNote)
	}
}

func printCapabilityConstraintRefs(label string, values []capgraph.SurfacingConstraint) {
	if len(values) == 0 {
		return
	}
	refs := make([]string, 0, len(values))
	for _, value := range values {
		ref := value.Ref
		if value.Detail != "" {
			ref += " (" + value.Detail + ")"
		}
		refs = append(refs, ref)
	}
	fmt.Printf("    %s: %s\n", label, strings.Join(refs, ", "))
}

func formatEdgeKindCounts(counts map[string]int) string {
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, fmt.Sprintf("%s=%d", kind, counts[kind]))
	}
	return strings.Join(parts, " ")
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
	if len(args) >= 1 && args[0] == "authority" {
		jsonOut := false
		for _, arg := range args[1:] {
			switch arg {
			case "--json":
				jsonOut = true
			default:
				fmt.Fprintln(os.Stderr, "event authority: unknown arg", arg)
				os.Exit(2)
			}
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		report, err := eventsource.LoadAuthority(homePath, 5)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Printf("# event trace authority\n\n")
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- authority: %s\n", report.AuthorityRef)
		fmt.Printf("- traces: %d\n", report.TraceCount)
		if len(report.SourceAuthority) > 0 {
			latest := report.SourceAuthority[len(report.SourceAuthority)-1]
			fmt.Printf("- source_authority: %s\n", latest.ID)
			fmt.Printf("- source: %s\n", latest.SourcePath)
		}
		for _, trace := range report.RecentTraces {
			fmt.Printf("- trace: %s %s %s\n", trace.ID, trace.SourceKind, trace.Summary)
		}
		return
	}
	if len(args) < 2 || args[0] != "import-os" {
		fmt.Fprintln(os.Stderr, "usage: compound event import-os <events.ndjson> [--limit N] [--stream NAME] [--review-root DIR] [--review-summary TEXT] | event authority [--json]")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	path := args[1]
	opts := eventsource.ImportOptions{}
	reviewRoot := ""
	reviewSummary := ""
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
		case "--review-root":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "event import-os: --review-root needs value")
				os.Exit(2)
			}
			i++
			reviewRoot = args[i]
		case "--review-summary":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "event import-os: --review-summary needs value")
				os.Exit(2)
			}
			i++
			reviewSummary = args[i]
		default:
			fmt.Fprintln(os.Stderr, "event import-os: unknown arg", args[i])
			os.Exit(2)
		}
	}
	if strings.TrimSpace(reviewSummary) != "" && strings.TrimSpace(reviewRoot) == "" {
		fmt.Fprintln(os.Stderr, "event import-os: --review-summary requires --review-root")
		os.Exit(2)
	}
	report, err := eventsource.ImportOSEvents(homePath, path, opts, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "event import-os:", err)
		os.Exit(1)
	}
	fmt.Println("# event import")
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- stream: %s\n", report.Stream)
	if report.AuthorityRef != "" {
		fmt.Printf("- authority: %s\n", report.AuthorityRef)
	}
	if report.SourceAuthorityID != "" {
		fmt.Printf("- source_authority: %s\n", report.SourceAuthorityID)
	}
	fmt.Printf("- imported: %d\n", report.Imported)
	fmt.Printf("- skipped: %d\n", report.Skipped)
	for _, id := range report.TraceIDs {
		fmt.Printf("- trace: %s\n", id)
	}
	if strings.TrimSpace(reviewRoot) != "" {
		fmt.Printf("- review_findings: %d\n", len(report.TraceIDs))
		for _, id := range report.TraceIDs {
			cmdReviewsRecordFinding([]string{
				"--auto-work",
				"--target", eventReviewFindingTarget(report.Stream, id),
				"--root", reviewRoot,
				"--summary", eventReviewFindingSummary(report.Stream, id, reviewSummary),
				"--source-trace", id,
				"--evidence-ref", eventsource.TraceEventsAuthorityRef,
			})
		}
	}
}

func eventReviewFindingTarget(stream, traceID string) string {
	streamSlug := reviewartifact.Slugify(reviewartifact.FirstNonEmpty(stream, "event-import"), 80)
	if streamSlug == "" {
		streamSlug = "event-import"
	}
	traceSlug := reviewartifact.Slugify(traceID, 96)
	if traceSlug == "" {
		traceSlug = reviewartifact.Digest12(traceID)
	}
	return filepath.ToSlash(filepath.Join("state", "runs", "compound-event-trace-reviews", streamSlug, traceSlug+".json"))
}

func eventReviewFindingSummary(stream, traceID, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	return fmt.Sprintf("Imported OS event trace %s from %s; record trace-backed review finding.", traceID, reviewartifact.FirstNonEmpty(stream, "event-import"))
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
		fmt.Printf("- candidates: skills=%d decisions=%d failures=%d taste=%d product_hypotheses=%d routes=%d capability_observations=%d eval_cases=%d method_upgrades=%d policy_patches=%d\n",
			cov.SkillCandidates, cov.DecisionCandidates, cov.FailurePatterns, cov.TasteObservations, cov.ProductHypotheses, cov.RouteObservations, cov.CapabilityObs, cov.EvalCases, cov.MethodUpgrades, cov.PolicyPatches)
		fmt.Printf("- capability_nodes: %d\n", cov.CapabilityNodes)
		fmt.Printf("- no_promote_decisions: %d\n", cov.NoPromoteDecisions)
		return
	}
	if len(args) >= 1 && args[0] == "import-conversation-coverage" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound distiller import-conversation-coverage <coverage-summary.json>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		now := time.Now()
		coverage, err := distiller.LoadConversationCoverage(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller import-conversation-coverage:", err)
			os.Exit(1)
		}
		sourceReport, err := sourceobs.Apply(homePath, sourceobs.FromConversationCoverage(sourceobs.ConversationCoverageObservation{
			ID:                coverage.ID,
			ReportPath:        coverage.ReportPath,
			Revision:          coverage.Revision,
			SourceCount:       coverage.SourceCount,
			TotalRows:         coverage.TotalRows,
			RawFiles:          coverage.RawFiles,
			MappedClean:       coverage.MappedClean,
			UnmappedRaw:       coverage.UnmappedRaw,
			Stable:            coverage.Stable,
			DreamCandidates:   coverage.DreamCandidates,
			MissingEmbeddings: coverage.MissingEmbeddings,
			AvgRetainedRatio:  coverage.AvgRetainedRatio,
			TopTagCount:       coverage.TopTagCount,
			ReviewDebt:        coverage.ReviewDebt,
			EvidenceRefs:      coverage.EvidenceRefs,
		}, now), now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "source observe:", err)
			os.Exit(1)
		}
		fmt.Printf("# distiller import-conversation-coverage %s\n", coverage.ID)
		fmt.Printf("- sources: %d\n", coverage.SourceCount)
		fmt.Printf("- total_rows: %d\n", coverage.TotalRows)
		fmt.Printf("- raw_files: %d\n", coverage.RawFiles)
		fmt.Printf("- mapped_clean: %d\n", coverage.MappedClean)
		fmt.Printf("- unmapped_raw: %d\n", coverage.UnmappedRaw)
		fmt.Printf("- dream_candidates: %d\n", coverage.DreamCandidates)
		fmt.Printf("- missing_embeddings: %d\n", coverage.MissingEmbeddings)
		fmt.Printf("- avg_retained_ratio: %.3f\n", coverage.AvgRetainedRatio)
		fmt.Printf("- review_debt: %d\n", len(coverage.ReviewDebt))
		fmt.Printf("- source_observation: %s\n", sourceReport.ObservationID)
		if len(sourceReport.CandidateIDs) > 0 {
			fmt.Printf("- source_candidate: %s\n", sourceReport.CandidateIDs[0])
			fmt.Printf("- review_command: compound distiller improvement-plan --candidate %s\n", sourceReport.CandidateIDs[0])
		}
		if len(sourceReport.NoPromoteIDs) > 0 {
			fmt.Printf("- source_no_promote: %s\n", sourceReport.NoPromoteIDs[0])
		}
		fmt.Println("- row_import: no")
		fmt.Println("- dream_execution: no")
		fmt.Println("- memory_promotion: no")
		return
	}
	if len(args) >= 1 && args[0] == "import-conversation-mining" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: compound distiller import-conversation-mining <mining-report.json> [--mined-tags <mined-tags.jsonl>] [--lossiness <lossiness.json>]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		now := time.Now()
		mining, err := distiller.LoadConversationMining(args[1], distiller.ConversationMiningOptions{
			MinedTagsPath: flagValue(args[2:], "--mined-tags"),
			LossinessPath: flagValue(args[2:], "--lossiness"),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller import-conversation-mining:", err)
			os.Exit(1)
		}
		sourceReport, err := sourceobs.Apply(homePath, sourceobs.FromConversationMining(sourceobs.ConversationMiningObservation{
			ID:                mining.ID,
			ReportPath:        mining.ReportPath,
			MinedTagsPath:     mining.MinedTagsPath,
			LossinessPath:     mining.LossinessPath,
			Revision:          mining.Revision,
			Processed:         mining.Report.Processed,
			Accepted:          mining.Report.Accepted,
			Rejected:          mining.Report.Rejected,
			MinedRows:         mining.MinedRows,
			AcceptedForReview: mining.AcceptedForReview,
			DreamPackFiles:    mining.Report.DreamPackFiles,
			DreamExecution:    mining.DreamExecution(),
			LossinessRecorded: mining.LossinessRecorded,
			LossinessDetail:   mining.LossinessDetail,
			Note:              mining.Report.Note,
			EvidenceRefs:      mining.EvidenceRefs,
		}, now), now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "source observe:", err)
			os.Exit(1)
		}
		fmt.Printf("# distiller import-conversation-mining %s\n", mining.ID)
		fmt.Printf("- status: %s\n", mining.Status())
		fmt.Printf("- processed: %d\n", mining.Report.Processed)
		fmt.Printf("- accepted: %d\n", mining.Report.Accepted)
		fmt.Printf("- rejected: %d\n", mining.Report.Rejected)
		fmt.Printf("- mined_rows: %d\n", mining.MinedRows)
		fmt.Printf("- accepted_for_review: %d\n", mining.AcceptedForReview)
		fmt.Printf("- dream_execution: %s\n", mining.DreamExecution())
		fmt.Printf("- dream_pack_files: %d\n", mining.Report.DreamPackFiles)
		fmt.Printf("- lossiness_recorded: %t\n", mining.LossinessRecorded)
		for _, tag := range mining.TopTags {
			fmt.Printf("- top_tag: %s\n", tag)
		}
		fmt.Printf("- source_observation: %s\n", sourceReport.ObservationID)
		for _, id := range sourceReport.CandidateIDs {
			fmt.Printf("- source_candidate: %s\n", id)
			fmt.Printf("- review_command: compound distiller improvement-plan --candidate %s\n", id)
		}
		for _, id := range sourceReport.NoPromoteIDs {
			fmt.Printf("- source_no_promote: %s\n", id)
		}
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
		fmt.Printf("- duplicate_candidates: rejected=%d\n", report.DuplicateCandidateRejections)
		fmt.Printf("- no_promote: total=%d duplicate_memory=%d duplicate_candidates=%d\n",
			report.NoPromoteRows, report.DuplicateMemoryNoPromoteRows, report.DuplicateCandidateNoPromoteRows)
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
	if len(args) >= 1 && args[0] == "eval-plan" {
		candidateID := flagValue(args[1:], "--candidate")
		corpusDir := flagValue(args[1:], "--corpus")
		if candidateID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound distiller eval-plan --candidate <candidate_id> [--corpus <dir>]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		plan, err := distiller.BuildEvalPlan(homePath, candidateID, corpusDir, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller eval-plan:", err)
			os.Exit(1)
		}
		paths, err := distiller.RecordEvalPlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller eval-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# distiller eval-plan %s\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- candidate: %s\n", plan.CandidateID)
		fmt.Printf("- proposed_case: %s\n", plan.FixtureDraft.ProposedCaseID)
		fmt.Printf("- request_trace_ref: %s\n", plan.FixtureDraft.RequestTraceRef)
		if plan.TargetCorpusDir != "" {
			fmt.Printf("- target_corpus: %s\n", plan.TargetCorpusDir)
			fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		}
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- required_artifacts: %d\n", len(plan.FixtureDraft.RequiredArtifacts))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		return
	}
	if len(args) >= 1 && args[0] == "improvement-plan" {
		candidateID := flagValue(args[1:], "--candidate")
		targetPath := flagValue(args[1:], "--target")
		if candidateID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound distiller improvement-plan --candidate <candidate_id> [--target <file>]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		plan, err := distiller.BuildImprovementPlan(homePath, candidateID, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller improvement-plan:", err)
			os.Exit(1)
		}
		paths, err := distiller.RecordImprovementPlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller improvement-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# distiller improvement-plan %s\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- candidate: %s\n", plan.CandidateID)
		fmt.Printf("- candidate_kind: %s\n", plan.CandidateKind)
		fmt.Printf("- improvement_key: %s\n", plan.ImprovementKey)
		fmt.Printf("- improvement_id: %s\n", plan.Draft.ImprovementID)
		fmt.Printf("- area: %s\n", plan.Draft.Area)
		if plan.TargetPath != "" {
			fmt.Printf("- target: %s\n", plan.TargetPath)
			fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		}
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- required_evidence: %d\n", len(plan.Draft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		fmt.Printf("- review_yaml: %s\n", paths.ReviewYAMLPath)
		return
	}
	if len(args) >= 1 && args[0] == "improvement-review" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound distiller improvement-review <review.yaml>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		review, err := distiller.LoadImprovementReview(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller improvement-review:", err)
			os.Exit(1)
		}
		report, err := distiller.RecordImprovementReview(homePath, review, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller improvement-review:", err)
			os.Exit(1)
		}
		fmt.Printf("# distiller improvement-review %s\n", report.ReviewID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- decision: %s\n", report.Decision)
		fmt.Printf("- reviewed: %s\n", report.ReviewedPath)
		return
	}
	if len(args) >= 1 && args[0] == "rule-plan" {
		candidateID := flagValue(args[1:], "--candidate")
		targetPath := flagValue(args[1:], "--target")
		if candidateID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound distiller rule-plan --candidate <candidate_id> [--target <file>]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		plan, err := distiller.BuildFailureRulePlan(homePath, candidateID, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller rule-plan:", err)
			os.Exit(1)
		}
		paths, err := distiller.RecordFailureRulePlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller rule-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# distiller rule-plan %s\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- candidate: %s\n", plan.CandidateID)
		fmt.Printf("- failure_key: %s\n", plan.FailureKey)
		fmt.Printf("- rule_id: %s\n", plan.RuleDraft.RuleID)
		if plan.TargetPath != "" {
			fmt.Printf("- target: %s\n", plan.TargetPath)
			fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		}
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- required_evidence: %d\n", len(plan.RuleDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		return
	}
	if len(args) >= 1 && args[0] == "taste-plan" {
		candidateID := flagValue(args[1:], "--candidate")
		targetPath := flagValue(args[1:], "--target")
		if candidateID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound distiller taste-plan --candidate <candidate_id> [--target <file>]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		plan, err := distiller.BuildTastePlan(homePath, candidateID, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller taste-plan:", err)
			os.Exit(1)
		}
		paths, err := distiller.RecordTastePlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller taste-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# distiller taste-plan %s\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- candidate: %s\n", plan.CandidateID)
		fmt.Printf("- taste_key: %s\n", plan.TasteKey)
		fmt.Printf("- standard_id: %s\n", plan.StandardDraft.StandardID)
		if plan.TargetPath != "" {
			fmt.Printf("- target: %s\n", plan.TargetPath)
			fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		}
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- required_evidence: %d\n", len(plan.StandardDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		return
	}
	if len(args) >= 1 && args[0] == "eval" {
		record := hasFlag(args[1:], "--record")
		var paths []string
		for _, arg := range args[1:] {
			if arg == "--record" {
				continue
			}
			paths = append(paths, arg)
		}
		if len(paths) != 1 {
			fmt.Fprintln(os.Stderr, "usage: compound distiller eval <case.json|cases-dir> [--record]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		targetPath := paths[0]
		info, err := os.Stat(targetPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller eval:", err)
			os.Exit(1)
		}
		if info.IsDir() {
			report, err := distiller.EvaluateCorpusDirInRoot(targetPath, homePath, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "distiller eval:", err)
				os.Exit(1)
			}
			printDistillerEvalCorpus(report)
			if report.Status != "pass" {
				os.Exit(1)
			}
			if record {
				proof, err := distiller.RecordEvalCorpusReport(homePath, report)
				if err != nil {
					fmt.Fprintln(os.Stderr, "distiller eval record:", err)
					os.Exit(1)
				}
				fmt.Printf("- proof_ref: %s\n", proof.ProofRef)
				fmt.Printf("- proof: %s\n", proof.Path)
			}
			return
		}
		report, err := distiller.EvaluateCaseFileInRoot(targetPath, homePath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller eval:", err)
			os.Exit(1)
		}
		printDistillerEval(report)
		if report.Status != "pass" {
			os.Exit(1)
		}
		if record {
			proof, err := distiller.RecordEvalReport(homePath, report)
			if err != nil {
				fmt.Fprintln(os.Stderr, "distiller eval record:", err)
				os.Exit(1)
			}
			fmt.Printf("- proof_ref: %s\n", proof.ProofRef)
			fmt.Printf("- proof: %s\n", proof.Path)
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
		fmt.Fprintln(os.Stderr, "usage: compound distiller eval <case.json|cases-dir> [--record] | eval-plan --candidate C [--corpus dir] | rule-plan --candidate C [--target file] | taste-plan --candidate C [--target file] | request --trace-ref T [--work-id W] [--raw f] [--context-pack f] [--work f] [--receipt f] [--envelope f]... [--event-trace trace_id]... | extract --request f --eval-corpus dir --openai-compatible-url URL --model M [--api-key-env ENV] | import-conversation-mining <report.json> [--mined-tags rows.jsonl] [--lossiness lossiness.json] | run [--llm-response] <trace.json> | doctor [--writeback] | prior-state")
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
	fmt.Printf("- prior: current_claims=%d skill_candidates=%d decision_candidates=%d route_observations=%d capability_observations=%d eval_cases=%d method_upgrades=%d policy_patches=%d\n",
		report.Request.PriorCoverage.CurrentClaims,
		report.Request.PriorCoverage.SkillCandidates,
		report.Request.PriorCoverage.DecisionCandidates,
		report.Request.PriorCoverage.RouteObservations,
		report.Request.PriorCoverage.CapabilityObs,
		report.Request.PriorCoverage.EvalCases,
		report.Request.PriorCoverage.MethodUpgrades,
		report.Request.PriorCoverage.PolicyPatches)
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
		fmt.Fprintln(os.Stderr, "usage: compound memory <candidate <trace.json>|import-page <markdown>|adjudicate <id> --verdict V|recall <entity> [--as-of N]|timeline <entity>|write-plan --candidate C --target brain.md|query <text>|engine-doctor <text>|engine-compare <text> [--allow-expansion]|doctor>")
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
	case "import-page":
		pagePath := pos()
		if pagePath == "" {
			fmt.Fprintln(os.Stderr, "usage: compound memory import-page <markdown>")
			os.Exit(2)
		}
		report, err := store.ImportKnowledgePage(pagePath, time.Now())
		die(err)
		fmt.Printf("# memory import-page %s\n", report.PageID)
		fmt.Printf("- evidence: %s\n", report.EvidenceID)
		fmt.Printf("- candidates: %d\n", len(report.CandidateIDs))
		fmt.Printf("- created: %d\n", report.Created)
		fmt.Printf("- reused: %d\n", report.Reused)
		for _, id := range report.CandidateIDs {
			fmt.Printf(" - candidate %s\n", id)
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
	case "write-plan":
		candidateID := flag("--candidate")
		targetPath := flag("--target")
		if candidateID == "" || targetPath == "" {
			fmt.Fprintln(os.Stderr, "usage: compound memory write-plan --candidate <candidate_id> --target <markdown>")
			os.Exit(2)
		}
		plan, err := store.BuildMeaningWritePlan(candidateID, targetPath, time.Now())
		die(err)
		paths, err := mem.RecordMeaningWritePlan(homePath, plan)
		die(err)
		fmt.Printf("# memory write-plan %s\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- candidate: %s\n", plan.CandidateID)
		fmt.Printf("- target: %s\n", plan.TargetPath)
		fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
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
		var r mem.Report
		if strings.TrimSpace(os.Getenv("COMPOUND_MEM_ENGINE_BIN")) != "" {
			eng, closeEng := memBreadthEngine()
			r = store.DoctorWithEngine(eng)
			closeEng()
		} else {
			r = store.Doctor()
		}
		if has("--json") {
			printJSON(r)
			if !r.OK {
				os.Exit(1)
			}
			return
		}
		for _, h := range r.Hops {
			fmt.Printf("%-15s %-8s %s\n", h.Name, h.Status, h.Detail)
		}
		fmt.Printf("verdict: %s — %s\n", okStr(r.OK), r.Note)
		if !r.OK {
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: compound memory <candidate|import-page|adjudicate|recall|timeline|query|engine-doctor|engine-compare [--allow-expansion]|doctor> ...")
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
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound route <evidence-authority [--json] [--recent N]|import <execution-route-evidence.json>|import-model-capabilities <model-capabilities.yaml>|import-provider-ledger <provider-calls.ndjson> [--limit N|--tail N]|cost-doctor <provider-calls.ndjson> [--limit N|--tail N] [--record]|outcome-review (--route-id ID|--route-evidence file) --receipt file [--target file] [--record]|plan (--candidate C [--target file]|--capability-class C [--privacy-sensitive])>")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	if args[0] == "evidence-authority" {
		recent := 6
		if v := flagValue(args[1:], "--recent"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "route evidence-authority: --recent must be a non-negative integer")
				os.Exit(2)
			}
			recent = n
		}
		report, err := routeevidence.LoadAuthority(homePath, recent)
		if err != nil {
			fmt.Fprintln(os.Stderr, "route evidence-authority:", err)
			os.Exit(1)
		}
		if hasFlag(args[1:], "--json") {
			printJSON(report)
			return
		}
		fmt.Println("# route evidence-authority")
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- authority: %s\n", report.AuthorityRef)
		fmt.Printf("- authority_path: %s\n", report.AuthorityPath)
		fmt.Printf("- route_evidence_count: %d\n", report.RouteEvidenceCount)
		fmt.Printf("- provider_ledger_evidence_count: %d\n", report.ProviderLedgerEvidenceCount)
		fmt.Printf("- token_usage_rows: %d\n", report.TokenUsageRows)
		fmt.Printf("- cost_known_rows: %d\n", report.CostKnownRows)
		if len(report.Recent) > 0 {
			last := report.Recent[len(report.Recent)-1]
			fmt.Printf("- latest_route_evidence: %s\n", last.ID)
			fmt.Printf("- latest_selected_target: %s/%s (%s)\n", last.Provider, last.Model, last.Protocol)
		}
		fmt.Println("- verdict: metadata_only_not_task_success")
		return
	}
	if args[0] == "plan" {
		candidateID := flagValue(args[1:], "--candidate")
		capabilityClass := flagValue(args[1:], "--capability-class")
		targetPath := flagValue(args[1:], "--target")
		if (candidateID == "" && capabilityClass == "") || (candidateID != "" && capabilityClass != "") {
			fmt.Fprintln(os.Stderr, "usage: compound route plan (--candidate <candidate_id> [--target <file>]|--capability-class <class> [--privacy-sensitive])")
			os.Exit(2)
		}
		if capabilityClass != "" {
			plan, err := routeevidence.PlanCapabilityRoute(homePath, capabilityClass, routeevidence.CapabilityRoutePlanOptions{
				PrivacySensitive: hasFlag(args[1:], "--privacy-sensitive"),
			}, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "route plan:", err)
				os.Exit(1)
			}
			fmt.Printf("# route capability-plan %s\n", plan.ID)
			fmt.Printf("- status: %s\n", plan.Status)
			fmt.Printf("- authority: %s\n", plan.AuthorityRef)
			fmt.Printf("- authority_path: %s\n", plan.AuthorityPath)
			if plan.SourceAuthorityID != "" {
				fmt.Printf("- source_authority: %s\n", plan.SourceAuthorityID)
			}
			fmt.Printf("- capability_class: %s\n", plan.CapabilityClass)
			fmt.Printf("- privacy_sensitive: %t\n", plan.PrivacySensitive)
			fmt.Printf("- routing_rule: %s\n", plan.RoutingRule)
			fmt.Printf("- chain_count: %d\n", len(plan.Chain))
			for _, lane := range plan.Chain {
				fmt.Printf("- lane: %d %s tier=%s role=%s claim=%s\n", lane.Position, lane.ModelID, lane.Tier, lane.GateRole, lane.ClaimLevel)
				fmt.Printf("  lane_justification: %s\n", lane.Justification)
			}
			if len(plan.Omitted) > 0 {
				fmt.Printf("- omitted_count: %d\n", len(plan.Omitted))
				for _, lane := range plan.Omitted {
					fmt.Printf("- omitted: %d %s reason=%s\n", lane.Position, lane.ModelID, lane.OmitReason)
				}
			}
			if len(plan.PendingObservations) > 0 {
				fmt.Printf("- pending_route_observations: %d\n", len(plan.PendingObservations))
				for _, observation := range plan.PendingObservations {
					fmt.Printf("- pending_observation: %s match=%s review=\"%s\"\n", observation.CandidateID, strings.Join(observation.MatchReasons, ","), observation.ReviewCommand)
				}
			}
			for _, warning := range plan.Warnings {
				fmt.Printf("- warning: %s\n", warning)
			}
			fmt.Println("- verdict: metadata_only_not_task_success")
			return
		}
		plan, err := routeevidence.BuildRoutePolicyPlan(homePath, candidateID, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "route plan:", err)
			os.Exit(1)
		}
		paths, err := routeevidence.RecordRoutePolicyPlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "route plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# route plan %s\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- candidate: %s\n", plan.CandidateID)
		fmt.Printf("- route_key: %s\n", plan.RouteKey)
		fmt.Printf("- policy_id: %s\n", plan.PolicyDraft.PolicyID)
		if plan.TargetPath != "" {
			fmt.Printf("- target: %s\n", plan.TargetPath)
			fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		}
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- required_evidence: %d\n", len(plan.PolicyDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		return
	}
	if args[0] == "import-model-capabilities" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound route import-model-capabilities <model-capabilities.yaml>")
			os.Exit(2)
		}
		report, err := routeevidence.ImportModelCapabilitiesYAML(homePath, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "route import-model-capabilities:", err)
			os.Exit(1)
		}
		fmt.Println("# route import-model-capabilities")
		fmt.Printf("- authority: %s\n", report.AuthorityRef)
		fmt.Printf("- authority_path: %s\n", report.AuthorityPath)
		fmt.Printf("- source_authority: %s\n", report.SourceAuthorityID)
		fmt.Printf("- models: %d\n", report.ModelCount)
		fmt.Printf("- capability_classes: %d\n", report.CapabilityClassCount)
		fmt.Printf("- routing_defaults: %d\n", report.RoutingDefaultCount)
		fmt.Println("- verdict: compound_primary")
		return
	}
	if args[0] == "outcome-review" {
		routeID := flagValue(args[1:], "--route-id")
		routePath := flagValue(args[1:], "--route-evidence")
		receiptPath := flagValue(args[1:], "--receipt")
		targetPath := flagValue(args[1:], "--target")
		if receiptPath == "" || (routeID == "" && routePath == "") || (routeID != "" && routePath != "") {
			fmt.Fprintln(os.Stderr, "usage: compound route outcome-review (--route-id <id>|--route-evidence <file>) --receipt <file> [--target <file>] [--record]")
			os.Exit(2)
		}
		var ev routeevidence.Evidence
		if routeID != "" {
			ev, err = routeevidence.LoadEvidenceByID(homePath, routeID)
		} else {
			ev, err = routeevidence.LoadEvidenceFile(routePath)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "route outcome-review:", err)
			os.Exit(1)
		}
		rec, err := routeevidence.LoadReceiptFile(receiptPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "route outcome-review:", err)
			os.Exit(1)
		}
		review, err := routeevidence.BuildRouteOutcomeReview(ev, rec, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "route outcome-review:", err)
			os.Exit(1)
		}
		if hasFlag(args[1:], "--record") {
			review, err = routeevidence.RecordRouteOutcomeObservation(homePath, review, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "route outcome-review:", err)
				os.Exit(1)
			}
		}
		paths, err := routeevidence.RecordRouteOutcomeReview(homePath, review)
		if err != nil {
			fmt.Fprintln(os.Stderr, "route outcome-review:", err)
			os.Exit(1)
		}
		target := review.SelectedTarget
		fmt.Printf("# route outcome-review %s\n", review.ID)
		fmt.Printf("- status: %s\n", review.Status)
		fmt.Printf("- route_evidence: %s\n", review.RouteEvidenceID)
		fmt.Printf("- receipt: %s\n", review.ReceiptID)
		fmt.Printf("- selected_target: %s/%s (%s)\n", target.Provider, target.Model, target.Protocol)
		if len(review.CapabilityClasses) > 0 {
			fmt.Printf("- capability_classes: %s\n", strings.Join(review.CapabilityClasses, ","))
		}
		fmt.Printf("- receipt_outcome: %s\n", review.ReceiptOutcome)
		fmt.Printf("- state_changed: %t\n", review.ReceiptStateChanged)
		fmt.Printf("- assessment_class: %s\n", review.Assessment.Class)
		fmt.Printf("- should_review_policy: %t\n", review.Assessment.ShouldReviewPolicy)
		if review.CandidateID != "" {
			fmt.Printf("- candidate: %s\n", review.CandidateID)
			fmt.Printf("- no_promote: %s\n", review.NoPromoteID)
		}
		if review.TargetPath != "" {
			fmt.Printf("- target: %s\n", review.TargetPath)
			fmt.Printf("- target_exists: %t\n", review.TargetExists)
		}
		if review.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", review.TargetHash)
		}
		fmt.Printf("- required_evidence: %d\n", len(review.ReviewDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(review.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		fmt.Println("- verdict: route_outcome_review_not_policy")
		return
	}
	if args[0] == "import-provider-ledger" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: compound route import-provider-ledger <provider-calls.ndjson> [--limit N|--tail N]")
			os.Exit(2)
		}
		limit, err := intFlag(args[2:], "--limit")
		if err != nil {
			fmt.Fprintln(os.Stderr, "route import-provider-ledger:", err)
			os.Exit(2)
		}
		tail, err := intFlag(args[2:], "--tail")
		if err != nil {
			fmt.Fprintln(os.Stderr, "route import-provider-ledger:", err)
			os.Exit(2)
		}
		summary, err := routeevidence.ImportProviderLedgerFile(homePath, args[1], routeevidence.ProviderLedgerImportOptions{Limit: limit, Tail: tail})
		if err != nil {
			fmt.Fprintln(os.Stderr, "route import-provider-ledger:", err)
			os.Exit(1)
		}
		fmt.Println("# route import-provider-ledger")
		fmt.Printf("- rows_read: %d\n", summary.RowsRead)
		fmt.Printf("- imported: %d\n", summary.Imported)
		if summary.FirstID != "" {
			fmt.Printf("- first_route_evidence: %s\n", summary.FirstID)
			fmt.Printf("- last_route_evidence: %s\n", summary.LastID)
		}
		if len(summary.Results) > 0 {
			last := summary.Results[len(summary.Results)-1]
			target := last.Evidence.SelectedTarget
			fmt.Printf("- last_request: %s\n", last.Evidence.RequestID)
			fmt.Printf("- last_selected_target: %s/%s (%s)\n", target.Provider, target.Model, target.Protocol)
			fmt.Printf("- last_transition: %s\n", last.Transition.ID)
		}
		fmt.Println("- verdict: metadata_only_not_task_success")
		return
	}
	if args[0] == "cost-doctor" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: compound route cost-doctor <provider-calls.ndjson> [--limit N|--tail N] [--record]")
			os.Exit(2)
		}
		limit, err := intFlag(args[2:], "--limit")
		if err != nil {
			fmt.Fprintln(os.Stderr, "route cost-doctor:", err)
			os.Exit(2)
		}
		tail, err := intFlag(args[2:], "--tail")
		if err != nil {
			fmt.Fprintln(os.Stderr, "route cost-doctor:", err)
			os.Exit(2)
		}
		report, err := routeevidence.DoctorProviderLedgerCosts(args[1], routeevidence.ProviderLedgerImportOptions{Limit: limit, Tail: tail})
		if err != nil {
			fmt.Fprintln(os.Stderr, "route cost-doctor:", err)
			os.Exit(1)
		}
		if hasFlag(args[2:], "--record") && report.Status != "ok" {
			report, err = routeevidence.RecordCostDoctorObservation(homePath, report, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "route cost-doctor:", err)
				os.Exit(1)
			}
		}
		fmt.Println("# route cost-doctor")
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- rows_read: %d\n", report.RowsRead)
		fmt.Printf("- rows_checked: %d\n", report.RowsChecked)
		fmt.Printf("- token_usage_rows: %d\n", report.TokenUsageRows)
		fmt.Printf("- cost_known_rows: %d\n", report.CostKnownRows)
		fmt.Printf("- token_usage_missing_cost: %d\n", report.TokenUsageMissingCost)
		fmt.Printf("- unknown_project_rows: %d\n", report.UnknownProjectRows)
		fmt.Printf("- partial_metadata_rows: %d\n", report.PartialMetadataRows)
		if report.CandidateID != "" {
			fmt.Printf("- candidate: %s\n", report.CandidateID)
			fmt.Printf("- no_promote: %s\n", report.NoPromoteID)
		}
		fmt.Println("- verdict: route_cost_observation_not_policy")
		return
	}
	if len(args) != 2 || args[0] != "import" {
		fmt.Fprintln(os.Stderr, "usage: compound route <evidence-authority [--json] [--recent N]|import <execution-route-evidence.json>|import-model-capabilities <model-capabilities.yaml>|import-provider-ledger <provider-calls.ndjson> [--limit N|--tail N]|cost-doctor <provider-calls.ndjson> [--limit N|--tail N] [--record]|outcome-review (--route-id ID|--route-evidence file) --receipt file [--target file] [--record]|plan (--candidate C [--target file]|--capability-class C [--privacy-sensitive])>")
		os.Exit(2)
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
	if target == "observe" {
		if len(args) > 3 {
			fmt.Fprintln(os.Stderr, "usage: compound harness observe [harness-registry.yaml|--os-entrypoints file]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		now := time.Now()
		obs := sourceobs.FromHarnessRegistry(reg, now)
		switch {
		case len(args) == 2:
			manifest, err := harness.LoadRegistryManifest(args[1])
			die(err)
			obs, err = sourceobs.FromHarnessRegistryManifest(manifest, now)
			die(err)
		case len(args) == 3 && args[1] == "--os-entrypoints":
			manifest, err := harness.LoadOSEntrypointsManifest(args[2])
			die(err)
			obs, err = sourceobs.FromHarnessRegistryManifest(manifest, now)
			die(err)
		case len(args) == 3:
			fmt.Fprintln(os.Stderr, "usage: compound harness observe [harness-registry.yaml|--os-entrypoints file]")
			os.Exit(2)
		}
		report, err := sourceobs.Apply(homePath, obs, now)
		die(err)
		fmt.Println("# harness observe")
		fmt.Printf("- source_observation: %s\n", report.ObservationID)
		fmt.Printf("- status: %s\n", report.Status)
		for _, id := range report.CandidateIDs {
			fmt.Printf("- source_candidate: %s\n", id)
		}
		for _, id := range report.NoPromoteIDs {
			fmt.Printf("- source_no_promote: %s\n", id)
		}
		fmt.Println("- verdict: capability_evidence_only_not_authority")
		return
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
	if code := runHookCommand(args); code != 0 {
		os.Exit(code)
	}
}

func runHookCommand(args []string) int {
	hookName, fixturePath, enforce, err := parseHookArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	homePath := ""
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	var fx hook.Fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		fmt.Fprintln(os.Stderr, "error: parse hook fixture:", err)
		return 1
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
		return 1
	}
	printJSON(res)
	if hookName == "session-end" {
		autoConsumeCapabilityFeedback(homePath)
	}
	fmt.Fprintf(os.Stderr, "[%s/%s] %s — %s\n", fx.Harness, res.Event.Status, res.Event.HookName, res.Summary)
	if enforce {
		return hookEnforceExit(hookName, res)
	}
	return 0
}

func parseHookArgs(args []string) (hookName, fixturePath string, enforce bool, err error) {
	if len(args) < 2 {
		return "", "", false, fmt.Errorf("usage: compound hook <session-start|user-prompt-submit|pre-tool-use|post-tool-use|session-end|stop> [--enforce] <fixture.json>")
	}
	hookName = args[0]
	for _, arg := range args[1:] {
		if arg == "--enforce" {
			enforce = true
			continue
		}
		if fixturePath != "" {
			return "", "", false, fmt.Errorf("usage: compound hook <session-start|user-prompt-submit|pre-tool-use|post-tool-use|session-end|stop> [--enforce] <fixture.json>")
		}
		fixturePath = arg
	}
	if fixturePath == "" {
		return "", "", false, fmt.Errorf("usage: compound hook <session-start|user-prompt-submit|pre-tool-use|post-tool-use|session-end|stop> [--enforce] <fixture.json>")
	}
	return hookName, fixturePath, enforce, nil
}

func hookEnforceExit(hookName string, res hook.Result) int {
	if hookName != "pre-tool-use" {
		return 0
	}
	for _, produced := range res.Produced {
		if pd, ok := produced.(*envelope.PolicyDecision); ok && !policy.Allowed(*pd) {
			return gateExit(*pd)
		}
	}
	if res.Event.Status == "blocked" || res.Event.Status == "fail_open" {
		return 1
	}
	return 0
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
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound policy <evaluate|import-placement-rules|placement-authority|import-standing-grants|standing-grants-authority|import-operator-state|operator-authority|import-operator-prompts|operator-prompts-authority|import-operator-approval-frontier|operator-approval-frontier-authority|import-release-goal-holds|release-goal-holds-authority> ...")
		os.Exit(2)
	}
	switch args[0] {
	case "evaluate":
		if len(args) != 2 {
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
	case "import-placement-rules":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound policy import-placement-rules <placement-rules.json>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report, err := policy.ImportPlacementRules(homePath, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printJSON(report)
	case "placement-authority":
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound policy placement-authority [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		type placementAuthorityReport struct {
			PlacementMode    string                                  `json:"placement_guard_mode,omitempty"`
			PolicySource     string                                  `json:"policy_source,omitempty"`
			AuthorityRef     string                                  `json:"authority_ref"`
			AuthorityPath    string                                  `json:"authority_path"`
			Installed        bool                                    `json:"installed"`
			OwnerCount       int                                     `json:"owner_count,omitempty"`
			BlockOwnerCount  int                                     `json:"block_owner_count,omitempty"`
			BlockOwnerIDs    []string                                `json:"block_owner_ids,omitempty"`
			OpenWriteCount   int                                     `json:"open_write_count,omitempty"`
			SourceAuthority  []policy.PlacementSourceAuthorityRecord `json:"source_authority,omitempty"`
			SourceRecordPath string                                  `json:"source_record_path"`
		}
		cfg, policySource, err := policy.LoadConfig(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report := placementAuthorityReport{
			PlacementMode:    cfg.PlacementGuardMode,
			PolicySource:     policySource,
			AuthorityRef:     policy.PlacementRulesAuthorityRef,
			AuthorityPath:    policy.PlacementRulesPath(homePath),
			SourceRecordPath: policy.PlacementSourceAuthorityPath(homePath),
		}
		rules, err := policy.LoadPlacementRulesFromHome(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		} else {
			report.Installed = true
			report.OwnerCount = len(rules.Owners)
			report.BlockOwnerIDs = rules.BlockOwnerIDs()
			report.BlockOwnerCount = len(report.BlockOwnerIDs)
			report.OpenWriteCount = len(rules.OpenWriteGlobs)
		}
		records, err := policy.LoadPlacementSourceAuthority(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report.SourceAuthority = records
		printJSON(report)
	case "import-standing-grants":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound policy import-standing-grants <standing-grants.yaml|json>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report, err := policy.ImportStandingGrants(homePath, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printJSON(report)
	case "standing-grants-authority":
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound policy standing-grants-authority [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		type standingGrantsAuthorityReport struct {
			AuthorityRef     string                                       `json:"authority_ref"`
			AuthorityPath    string                                       `json:"authority_path"`
			Installed        bool                                         `json:"installed"`
			GeneratedAt      string                                       `json:"generated_at,omitempty"`
			GrantCount       int                                          `json:"grant_count,omitempty"`
			CapabilityCount  int                                          `json:"capability_count,omitempty"`
			Grants           []policy.StandingGrant                       `json:"grants,omitempty"`
			SourceAuthority  []policy.StandingGrantsSourceAuthorityRecord `json:"source_authority,omitempty"`
			SourceRecordPath string                                       `json:"source_record_path"`
		}
		report := standingGrantsAuthorityReport{
			AuthorityRef:     policy.StandingGrantsAuthorityRef,
			AuthorityPath:    policy.StandingGrantsPath(homePath),
			SourceRecordPath: policy.StandingGrantsSourceAuthorityPath(homePath),
		}
		registry, err := policy.LoadStandingGrantsFromHome(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		} else {
			report.Installed = true
			report.GeneratedAt = registry.GeneratedAt
			report.GrantCount = len(registry.Grants)
			report.CapabilityCount = len(registry.Grants)
			report.Grants = registry.Grants
		}
		records, err := policy.LoadStandingGrantsSourceAuthority(homePath)
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report.SourceAuthority = records
		if len(args) == 2 && args[1] == "--json" {
			printJSON(report)
			return
		}
		fmt.Printf("standing grants authority: %s\n", report.AuthorityRef)
		fmt.Printf("installed: %t\n", report.Installed)
		fmt.Printf("grants: %d capabilities=%d\n", report.GrantCount, report.CapabilityCount)
	case "import-operator-state":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound policy import-operator-state <operator-state.json>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report, err := policy.ImportOperatorState(homePath, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printJSON(report)
	case "import-operator-prompts":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound policy import-operator-prompts <operator-decision-prompts.json>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report, err := policy.ImportOperatorPrompts(homePath, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printJSON(report)
	case "operator-prompts-authority":
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound policy operator-prompts-authority [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		type operatorPromptsAuthorityReport struct {
			AuthorityRef      string                                        `json:"authority_ref"`
			AuthorityPath     string                                        `json:"authority_path"`
			Installed         bool                                          `json:"installed"`
			GeneratedAt       string                                        `json:"generated_at,omitempty"`
			PromptCount       int                                           `json:"prompt_count,omitempty"`
			HighSeverityCount int                                           `json:"high_severity_count,omitempty"`
			Prompts           []policy.OperatorPrompt                       `json:"prompts,omitempty"`
			SourceAuthority   []policy.OperatorPromptsSourceAuthorityRecord `json:"source_authority,omitempty"`
			SourceRecordPath  string                                        `json:"source_record_path"`
		}
		report := operatorPromptsAuthorityReport{
			AuthorityRef:     policy.OperatorPromptsAuthorityRef,
			AuthorityPath:    policy.OperatorPromptsPath(homePath),
			SourceRecordPath: policy.OperatorPromptsSourceAuthorityPath(homePath),
		}
		registry, err := policy.LoadOperatorPromptsFromHome(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		} else {
			report.Installed = true
			report.GeneratedAt = registry.GeneratedAt
			report.PromptCount = len(registry.Prompts)
			report.HighSeverityCount = 0
			for _, prompt := range registry.Prompts {
				if strings.EqualFold(strings.TrimSpace(prompt.Severity), "high") {
					report.HighSeverityCount++
				}
			}
			report.Prompts = registry.Prompts
		}
		records, err := policy.LoadOperatorPromptsSourceAuthority(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		} else {
			report.SourceAuthority = records
		}
		printJSON(report)
	case "import-operator-approval-frontier":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound policy import-operator-approval-frontier <operator-approval-frontier-summary.json>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report, err := policy.ImportOperatorApprovalFrontier(homePath, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printJSON(report)
	case "operator-approval-frontier-authority":
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound policy operator-approval-frontier-authority [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		type operatorApprovalFrontierAuthorityReport struct {
			AuthorityRef              string                                                 `json:"authority_ref"`
			AuthorityPath             string                                                 `json:"authority_path"`
			Installed                 bool                                                   `json:"installed"`
			GeneratedAt               string                                                 `json:"generated_at,omitempty"`
			RunID                     string                                                 `json:"run_id,omitempty"`
			Status                    string                                                 `json:"status,omitempty"`
			FamilyCount               int                                                    `json:"family_count,omitempty"`
			DeferredCount             int                                                    `json:"deferred_count,omitempty"`
			ApprovalRequiredOpenCount int                                                    `json:"approval_required_open_count,omitempty"`
			Families                  []policy.OperatorApprovalFamily                        `json:"families,omitempty"`
			CurrentState              map[string]interface{}                                 `json:"current_state,omitempty"`
			SourceAuthority           []policy.OperatorApprovalFrontierSourceAuthorityRecord `json:"source_authority,omitempty"`
			SourceRecordPath          string                                                 `json:"source_record_path"`
		}
		report := operatorApprovalFrontierAuthorityReport{
			AuthorityRef:     policy.OperatorApprovalFrontierAuthorityRef,
			AuthorityPath:    policy.OperatorApprovalFrontierPath(homePath),
			SourceRecordPath: policy.OperatorApprovalFrontierSourceAuthorityPath(homePath),
		}
		frontier, err := policy.LoadOperatorApprovalFrontierFromHome(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		} else {
			report.Installed = true
			report.GeneratedAt = frontier.GeneratedAt
			report.RunID = frontier.RunID
			report.Status = frontier.Status
			report.FamilyCount = len(frontier.Families)
			report.DeferredCount = 0
			for _, family := range frontier.Families {
				if strings.EqualFold(strings.TrimSpace(family.CurrentDecision), "defer") {
					report.DeferredCount++
				}
			}
			report.ApprovalRequiredOpenCount = frontier.ApprovalRequiredOpenCount
			report.Families = frontier.Families
			report.CurrentState = frontier.CurrentState
		}
		records, err := policy.LoadOperatorApprovalFrontierSourceAuthority(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		} else {
			report.SourceAuthority = records
		}
		printJSON(report)
	case "import-release-goal-holds":
		sourcePath := ""
		gateLive := policy.DefaultReleaseGoalHoldsGateLive
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--gate-live":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "usage: compound policy import-release-goal-holds <release-orchestrator-state.sqlite> [--gate-live TS]")
					os.Exit(2)
				}
				gateLive = args[i+1]
				i++
			default:
				if sourcePath != "" {
					fmt.Fprintln(os.Stderr, "usage: compound policy import-release-goal-holds <release-orchestrator-state.sqlite> [--gate-live TS]")
					os.Exit(2)
				}
				sourcePath = args[i]
			}
		}
		if sourcePath == "" {
			fmt.Fprintln(os.Stderr, "usage: compound policy import-release-goal-holds <release-orchestrator-state.sqlite> [--gate-live TS]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report, err := policy.ImportReleaseGoalHolds(homePath, sourcePath, gateLive, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printJSON(report)
	case "release-goal-holds-authority":
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound policy release-goal-holds-authority [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		type releaseGoalHoldsAuthorityReport struct {
			AuthorityRef     string                                         `json:"authority_ref"`
			AuthorityPath    string                                         `json:"authority_path"`
			Installed        bool                                           `json:"installed"`
			GeneratedAt      string                                         `json:"generated_at,omitempty"`
			SourceDB         string                                         `json:"source_db,omitempty"`
			GateLive         string                                         `json:"gate_live,omitempty"`
			HoldCount        int                                            `json:"hold_count,omitempty"`
			Holds            []policy.ReleaseGoalHold                       `json:"holds,omitempty"`
			SourceAuthority  []policy.ReleaseGoalHoldsSourceAuthorityRecord `json:"source_authority,omitempty"`
			SourceRecordPath string                                         `json:"source_record_path"`
		}
		report := releaseGoalHoldsAuthorityReport{
			AuthorityRef:     policy.ReleaseGoalHoldsAuthorityRef,
			AuthorityPath:    policy.ReleaseGoalHoldsPath(homePath),
			SourceRecordPath: policy.ReleaseGoalHoldsSourceAuthorityPath(homePath),
		}
		authority, err := policy.LoadReleaseGoalHoldsFromHome(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		} else {
			report.Installed = true
			report.GeneratedAt = authority.GeneratedAt
			report.SourceDB = authority.SourceDB
			report.GateLive = authority.GateLive
			report.HoldCount = authority.HoldCount
			report.Holds = authority.Holds
		}
		records, err := policy.LoadReleaseGoalHoldsSourceAuthority(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		} else {
			report.SourceAuthority = records
		}
		printJSON(report)
	case "operator-authority":
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound policy operator-authority [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		type operatorAuthorityReport struct {
			AuthorityRef     string                                      `json:"authority_ref"`
			AuthorityPath    string                                      `json:"authority_path"`
			Installed        bool                                        `json:"installed"`
			AutonomyLevel    string                                      `json:"autonomy_level,omitempty"`
			Mode             string                                      `json:"mode,omitempty"`
			NeedsUmutCount   int                                         `json:"needs_umut_count,omitempty"`
			BlockedCount     int                                         `json:"blocked_count,omitempty"`
			SourceAuthority  []policy.OperatorStateSourceAuthorityRecord `json:"source_authority,omitempty"`
			SourceRecordPath string                                      `json:"source_record_path"`
		}
		report := operatorAuthorityReport{
			AuthorityRef:     policy.OperatorStateAuthorityRef,
			AuthorityPath:    policy.OperatorStatePath(homePath),
			SourceRecordPath: policy.OperatorStateSourceAuthorityPath(homePath),
		}
		state, err := policy.LoadOperatorStateFromHome(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		} else {
			report.Installed = true
			report.AutonomyLevel = policy.OperatorStateLevel(state)
			report.Mode = state.Mode
			report.NeedsUmutCount = len(state.NeedsUmut)
			report.BlockedCount = len(state.Blocked)
		}
		records, err := policy.LoadOperatorStateSourceAuthority(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		} else {
			report.SourceAuthority = records
		}
		printJSON(report)
	default:
		fmt.Fprintln(os.Stderr, "usage: compound policy <evaluate|import-placement-rules|placement-authority|import-operator-state|operator-authority|import-operator-prompts|operator-prompts-authority|import-operator-approval-frontier|operator-approval-frontier-authority|import-release-goal-holds|release-goal-holds-authority> ...")
		os.Exit(2)
	}
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

func flagValue(args []string, name string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func intFlag(args []string, name string) (int, error) {
	raw := flagValue(args, name)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return n, nil
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

func flagCSVValues(args []string, names ...string) []string {
	nameSet := make(map[string]bool, len(names))
	for _, name := range names {
		nameSet[name] = true
	}
	var out []string
	for i := 0; i < len(args); i++ {
		if !nameSet[args[i]] || i+1 >= len(args) {
			continue
		}
		out = append(out, splitCSV(args[i+1])...)
		i++
	}
	return out
}

func cmdWorkbench(args []string) {
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
	jsonOut := false
	recent := 5
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--recent":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: compound workbench [--json] [--recent N]")
				os.Exit(2)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "error: --recent must be a non-negative integer")
				os.Exit(2)
			}
			recent = n
		default:
			fmt.Fprintln(os.Stderr, "usage: compound workbench [--json] [--recent N]")
			os.Exit(2)
		}
	}
	report, err := store.Workbench(time.Now(), work.WorkbenchOptions{RecentLimit: recent})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
		return
	}
	fmt.Printf("workbench open=%d needs_claim=%d needs_review=%d in_progress=%d terminal=%d\n",
		report.Counts.Open, report.Counts.NeedsClaim, report.Counts.NeedsReview, report.Counts.InProgress, report.Counts.Terminal)
	if report.Counts.RunObservations > 0 {
		fmt.Printf("run_observations=%d\n", report.Counts.RunObservations)
	}
	if len(report.Integrity) > 0 {
		fmt.Println("integrity:")
		for _, msg := range report.Integrity {
			fmt.Println("- " + msg)
		}
	}
	if len(report.Open) == 0 {
		fmt.Println("open: none")
	} else {
		fmt.Println("open:")
		for _, item := range report.Open {
			fmt.Printf("- %s %-10s %-14s %s\n", item.WorkID, item.State, item.OpenReason, item.SuccessPredicate)
			fmt.Printf("  next: %s\n", item.NextAction)
			if item.ClaimedBy != "" {
				fmt.Printf("  claimed_by: %s\n", item.ClaimedBy)
			}
			if len(item.RunObservations) > 0 {
				latest := item.RunObservations[0]
				fmt.Printf("  observed_run: %s trace=%s policy=%s\n", latest.ID, latest.TraceID, latest.PolicyDecisionID)
			}
		}
	}
	if len(report.Recent) > 0 {
		fmt.Println("recent:")
		for _, item := range report.Recent {
			fmt.Printf("- %s %-10s receipt=%s transition=%s move=%s changed=%v\n",
				item.WorkID, item.State, emptyDash(item.ReceiptID), emptyDash(item.TransitionID), emptyDash(item.MoveType), item.Changed)
		}
	}
}

func cmdReviews(args []string) {
	if len(args) > 0 && args[0] == "record-finding" {
		cmdReviewsRecordFinding(args[1:])
		return
	}
	jsonOut := hasFlag(args, "--json")
	area := valueFlag(args, "--area")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
		case "--area":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: compound reviews [--json] [--area A]")
				os.Exit(2)
			}
			i++
		default:
			fmt.Fprintln(os.Stderr, "usage: compound reviews [--json] [--area A]")
			os.Exit(2)
		}
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	report, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{Area: area}, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	decisions.AnnotateLifecycleRepairQueue(&report)
	product.AnnotateReviewQueue(&report)
	authority.AnnotateJobHealthQueue(&report)
	capgraph.AnnotateSkillCandidateQueue(&report)
	reviewartifact.Recount(&report)
	if jsonOut {
		printJSON(report)
		return
	}
	fmt.Printf("reviews total=%d review_required=%d ready_to_apply=%d ok=%d\n", report.Counts.Total, report.Counts.ReviewRequired, report.Counts.ReadyToApply, report.Counts.OK)
	if len(report.Counts.ByArea) > 0 {
		fmt.Println("areas:")
		for _, area := range sortedMapKeys(report.Counts.ByArea) {
			fmt.Printf("- %s: %d\n", area, report.Counts.ByArea[area])
		}
	}
	if len(report.Counts.ByStatus) > 0 {
		fmt.Println("statuses:")
		for _, status := range sortedMapKeys(report.Counts.ByStatus) {
			fmt.Printf("- %s: %d\n", status, report.Counts.ByStatus[status])
		}
	}
	if len(report.Items) == 0 {
		fmt.Println("review_required: none")
		return
	}
	fmt.Println("items:")
	for _, item := range report.Items {
		fmt.Printf("- %s/%s status=%s repairs=%d", item.Area, item.ID, item.Status, item.RepairCount)
		if item.Readiness != "" {
			fmt.Printf(" readiness=%s", item.Readiness)
		}
		fmt.Println()
		fmt.Printf(" json: %s\n", item.JSONPath)
		if item.MarkdownPath != "" {
			fmt.Printf(" review: %s\n", item.MarkdownPath)
		}
		for _, action := range item.Actions {
			fmt.Printf(" action:%s %s", action.ID, action.Command)
			if action.RequiresReview {
				fmt.Print(" (requires review)")
			}
			fmt.Println()
		}
	}
}

type reviewFindingRecord struct {
	SchemaVersion    string                        `json:"schema_version"`
	ID               string                        `json:"id"`
	CreatedAt        string                        `json:"created_at"`
	Status           string                        `json:"status"`
	WorkID           string                        `json:"work_id"`
	Capability       string                        `json:"capability"`
	Summary          string                        `json:"summary"`
	TargetPath       string                        `json:"target_path"`
	ArtifactPath     string                        `json:"artifact_path"`
	MarkdownPath     string                        `json:"markdown_path,omitempty"`
	ActionIntentID   string                        `json:"action_intent_id"`
	PolicyDecisionID string                        `json:"policy_decision_id"`
	PolicyOutcome    string                        `json:"policy_outcome"`
	StandingGrantID  string                        `json:"standing_grant_id,omitempty"`
	SourceArtifacts  []reviewFindingSourceArtifact `json:"source_artifacts,omitempty"`
	SourceTraces     []reviewFindingSourceTrace    `json:"source_traces,omitempty"`
	EvidenceRefs     []string                      `json:"evidence_refs,omitempty"`
	Warnings         []string                      `json:"warnings,omitempty"`
}

type reviewFindingSourceArtifact struct {
	Path         string `json:"path"`
	ArtifactPath string `json:"artifact_path"`
	Hash         string `json:"hash"`
}

type reviewFindingSourceTrace struct {
	ID            string   `json:"id"`
	SourceSystem  string   `json:"source_system,omitempty"`
	SourceKind    string   `json:"source_kind,omitempty"`
	MutationClass string   `json:"mutation_class,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
}

func cmdReviewsRecordFinding(args []string) {
	workID := valueFlag(args, "--work-id")
	targetPath := valueFlag(args, "--target")
	summary := valueFlag(args, "--summary")
	root := valueFlag(args, "--root")
	capability := reviewartifact.FirstNonEmpty(valueFlag(args, "--capability"), "review.finding")
	evidenceRefs := valuesFlag(args, "--evidence-ref")
	sourceArtifactPaths := valuesFlag(args, "--source-artifact")
	sourceTraceIDs := valuesFlag(args, "--source-trace")
	autoWork := hasFlag(args, "--auto-work")
	usage := "usage: compound reviews record-finding (--work-id W|--auto-work) --target state/runs/.../finding.json --summary S [--root DIR] [--capability review.finding] [--source-artifact state/runs/.../artifact.json] [--source-trace trace_id] [--evidence-ref REF...]"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--auto-work":
		case "--work-id", "--target", "--summary", "--root", "--capability", "--source-artifact", "--source-trace", "--evidence-ref":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
			i++
		default:
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
	}
	if (workID == "" && !autoWork) || targetPath == "" || summary == "" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if workID != "" && autoWork {
		fmt.Fprintln(os.Stderr, "error: --work-id and --auto-work are mutually exclusive")
		os.Exit(2)
	}
	if autoWork && len(sourceArtifactPaths) == 0 && len(sourceTraceIDs) == 0 {
		fmt.Fprintln(os.Stderr, "error: --auto-work requires --source-artifact or --source-trace")
		os.Exit(2)
	}
	if capability != "review.finding" && capability != "review.summary" {
		fmt.Fprintln(os.Stderr, "error: --capability must be review.finding or review.summary")
		os.Exit(2)
	}
	target, err := normalizeRunReviewTarget(targetPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	artifactPath := filepath.Join(root, filepath.FromSlash(target))
	markdownPath := strings.TrimSuffix(artifactPath, filepath.Ext(artifactPath)) + ".md"
	sourceArtifacts, sourceEvidenceRefs, err := reviewFindingSourceArtifacts(root, sourceArtifactPaths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	if _, err := os.Stat(artifactPath); err == nil {
		fmt.Fprintf(os.Stderr, "error: review finding target already exists: %s\n", artifactPath)
		os.Exit(1)
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if _, err := os.Stat(markdownPath); err == nil {
		fmt.Fprintf(os.Stderr, "error: review finding markdown target already exists: %s\n", markdownPath)
		os.Exit(1)
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	sourceTraces, traceEvidenceRefs, err := reviewFindingSourceTraces(homePath, sourceTraceIDs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	store, err := work.Open(homePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	now := time.Now()
	createdAt := reviewartifact.Timestamp(now)
	evidenceRefs = append(evidenceRefs, sourceEvidenceRefs...)
	evidenceRefs = append(evidenceRefs, traceEvidenceRefs...)
	if autoWork {
		created, err := store.Create(work.CreateInput{
			Owner:            "compound reviews",
			SuccessPredicate: "governed review finding recorded at " + target,
			DesiredOutcome:   strings.TrimSpace(summary),
			EvidenceRefs:     reviewartifact.Unique(append(append([]string{}, evidenceRefs...), target)),
			Risk:             "tier1",
			Privacy:          "private",
		}, now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		workID = created.ID
	} else {
		w, err := store.Get(workID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if w.State == "completed" || w.State == "failed" || w.State == "cancelled" {
			fmt.Fprintf(os.Stderr, "error: work %s is %s (terminal); cannot record review finding\n", workID, w.State)
			os.Exit(1)
		}
	}
	evidenceRefs = reviewartifact.Unique(append(evidenceRefs, "work://"+workID, target))
	actionID := reviewartifact.ID("act_", workID, capability, target, summary, createdAt)
	ai := envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            actionID,
			CreatedAt:     createdAt,
			Producer:      envelope.Producer{Kind: "cli", Name: "compound reviews"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Owner: "compound", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  evidenceRefs,
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "compound-cli",
		Capability:    capability,
		Action:        "record_review_finding",
		Target:        envelope.ActionTarget{Kind: "path", Path: target},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "repo",
		Preview:       summary,
		RollbackPlan:  "remove the newly written review finding artifact and markdown sibling",
	}
	pd := policy.Evaluate(ai, policyConfig(), now)
	if !policy.Allowed(pd) {
		printJSON(pd)
		os.Exit(gateExit(pd))
	}
	report := reviewFindingRecord{
		SchemaVersion:    "compound.review_finding_record.v0.1",
		ID:               reviewartifact.ID("rvf_", workID, capability, target, summary),
		CreatedAt:        createdAt,
		Status:           "recorded",
		WorkID:           workID,
		Capability:       capability,
		Summary:          strings.TrimSpace(summary),
		TargetPath:       target,
		ArtifactPath:     artifactPath,
		MarkdownPath:     markdownPath,
		ActionIntentID:   ai.ID,
		PolicyDecisionID: pd.ID,
		PolicyOutcome:    pd.Outcome,
		StandingGrantID:  pd.StandingGrantID,
		SourceArtifacts:  sourceArtifacts,
		SourceTraces:     sourceTraces,
		EvidenceRefs:     evidenceRefs,
		Warnings: []string{
			"writes only under state/runs/**",
			"requires allowing PolicyDecision before artifact write",
			"records ActionIntent on WorkEnvelope after artifact write",
		},
	}
	data, err := json.MarshalIndent(report, "", " ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(artifactPath, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(markdownPath, []byte(renderReviewFindingMarkdown(report)), 0o644); err != nil {
		_ = os.Remove(artifactPath)
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if _, err := store.RecordAction(workID, ai, pd, now); err != nil {
		_ = os.Remove(artifactPath)
		_ = os.Remove(markdownPath)
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if autoWork {
		if _, err := store.Claim(workID, "compound reviews", now); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if _, err := store.Advance(workID, work.AdvanceInput{To: "running", Note: "review finding recorded"}, now); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if _, err := store.Advance(workID, work.AdvanceInput{To: "verifying", VerificationID: target, Note: "review finding artifact written"}, now); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if _, err := store.Close(workID, work.CloseInput{
			Outcome:   "completed",
			MoveType:  "control_plane_repair",
			Summary:   "Review finding recorded through StandingGrant",
			Changed:   true,
			ProofRefs: []string{target},
		}, now); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	printJSON(report)
}

func reviewFindingSourceTraces(homePath string, values []string) ([]reviewFindingSourceTrace, []string, error) {
	envs, err := distiller.LoadTraceEnvelopes(homePath, values)
	if err != nil {
		return nil, nil, err
	}
	var traces []reviewFindingSourceTrace
	var evidenceRefs []string
	for _, env := range envs {
		trace, ok := env.(*envelope.TraceEnvelope)
		if !ok {
			return nil, nil, fmt.Errorf("source trace is %T, want TraceEnvelope", env)
		}
		traceRef := "trace_envelope:" + trace.ID
		refs := reviewartifact.Unique(append([]string{traceRef}, trace.EvidenceRefs...))
		traces = append(traces, reviewFindingSourceTrace{
			ID:            trace.ID,
			SourceSystem:  trace.Source.System,
			SourceKind:    trace.Source.SourceKind,
			MutationClass: trace.MutationClass,
			Summary:       trace.Summary,
			EvidenceRefs:  refs,
		})
		evidenceRefs = append(evidenceRefs, refs...)
	}
	return traces, reviewartifact.Unique(evidenceRefs), nil
}

func reviewFindingSourceArtifacts(root string, values []string) ([]reviewFindingSourceArtifact, []string, error) {
	var artifacts []reviewFindingSourceArtifact
	var evidenceRefs []string
	seen := map[string]bool{}
	for _, value := range values {
		source, err := normalizeRunReviewSourceArtifact(value)
		if err != nil {
			return nil, nil, err
		}
		if seen[source] {
			continue
		}
		seen[source] = true
		artifactPath := filepath.Join(root, filepath.FromSlash(source))
		exists, hash, err := reviewartifact.HashFileTarget(artifactPath)
		if err != nil {
			return nil, nil, fmt.Errorf("source artifact %q: %w", source, err)
		}
		if !exists {
			return nil, nil, fmt.Errorf("source artifact does not exist: %s", source)
		}
		hashRef := "sha256:" + hash
		artifacts = append(artifacts, reviewFindingSourceArtifact{
			Path:         source,
			ArtifactPath: artifactPath,
			Hash:         hashRef,
		})
		evidenceRefs = append(evidenceRefs, source, hashRef)
	}
	return artifacts, reviewartifact.Unique(evidenceRefs), nil
}

func normalizeRunReviewSourceArtifact(source string) (string, error) {
	source = filepath.ToSlash(strings.TrimSpace(source))
	if source == "" {
		return "", fmt.Errorf("source artifact path required")
	}
	if filepath.IsAbs(source) || strings.HasPrefix(source, "~") {
		return "", fmt.Errorf("source artifact must be relative state/runs path, got %q", source)
	}
	clean := filepath.ToSlash(filepath.Clean(source))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("source artifact must not escape root, got %q", source)
	}
	if !strings.HasPrefix(clean, "state/runs/") {
		return "", fmt.Errorf("source artifact must be under state/runs/**, got %q", clean)
	}
	return clean, nil
}

func normalizeRunReviewTarget(target string) (string, error) {
	target = filepath.ToSlash(strings.TrimSpace(target))
	if target == "" {
		return "", fmt.Errorf("target required")
	}
	if filepath.IsAbs(target) || strings.HasPrefix(target, "~") {
		return "", fmt.Errorf("review finding target must be relative state/runs path, got %q", target)
	}
	clean := filepath.ToSlash(filepath.Clean(target))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("review finding target must not escape root, got %q", target)
	}
	if !strings.HasPrefix(clean, "state/runs/") {
		return "", fmt.Errorf("review finding target must be under state/runs/**, got %q", clean)
	}
	if !strings.HasSuffix(clean, ".json") {
		return "", fmt.Errorf("review finding target must be a .json artifact, got %q", clean)
	}
	return clean, nil
}

func renderReviewFindingMarkdown(report reviewFindingRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Review Finding: %s\n\n", report.ID)
	fmt.Fprintf(&b, "- status: %s\n", report.Status)
	fmt.Fprintf(&b, "- work_id: %s\n", report.WorkID)
	fmt.Fprintf(&b, "- capability: %s\n", report.Capability)
	fmt.Fprintf(&b, "- target: %s\n", report.TargetPath)
	fmt.Fprintf(&b, "- policy_decision: %s (%s)\n", report.PolicyDecisionID, report.PolicyOutcome)
	if report.StandingGrantID != "" {
		fmt.Fprintf(&b, "- standing_grant: %s\n", report.StandingGrantID)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Summary")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, report.Summary)
	if len(report.SourceArtifacts) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Source Artifacts")
		fmt.Fprintln(&b)
		for _, artifact := range report.SourceArtifacts {
			fmt.Fprintf(&b, "- %s (%s)\n", artifact.Path, artifact.Hash)
		}
	}
	if len(report.SourceTraces) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Source Traces")
		fmt.Fprintln(&b)
		for _, trace := range report.SourceTraces {
			fmt.Fprintf(&b, "- %s", trace.ID)
			if trace.SourceKind != "" {
				fmt.Fprintf(&b, " [%s]", trace.SourceKind)
			}
			if trace.Summary != "" {
				fmt.Fprintf(&b, ": %s", trace.Summary)
			}
			fmt.Fprintln(&b)
		}
	}
	if len(report.EvidenceRefs) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Evidence")
		fmt.Fprintln(&b)
		for _, ref := range report.EvidenceRefs {
			fmt.Fprintf(&b, "- %s\n", ref)
		}
	}
	return b.String()
}

// cmdWork is the lifecycle store: one work_id threads intent -> context -> action ->
// receipt -> transition. State lives in COMPOUND_HOME (docs/architecture/transition.md).
func cmdWork(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound work <create-plan|create|import-packet|show|list|dispatch-plan|dispatch-claim|dispatch-advance|dispatch-close-plan|dispatch-close|dispatch-step|claim-plan|claim|advance-plan|advance|observe-run|close-plan|close|act> ...")
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
	case "create-plan":
		plan, err := store.CreatePlan(work.CreateInput{
			Owner:            flag("--owner"),
			Actor:            firstNonEmpty(flag("--by"), flag("--actor")),
			SuccessPredicate: flag("--predicate"),
			DesiredOutcome:   flag("--outcome"),
			NonGoals:         flagCSVValues(rest, "--non-goal", "--non-goals"),
			EvidenceRefs:     flagCSVValues(rest, "--evidence-ref", "--evidence"),
			ContextPackID:    flag("--context-pack"),
			State:            flag("--state"),
			Risk:             flag("--risk"),
			Privacy:          flag("--privacy"),
		}, time.Now(), work.CreatePlanOptions{
			Actor:         firstNonEmpty(flag("--by"), flag("--actor")),
			Capability:    flag("--capability"),
			Action:        flag("--action"),
			MutationClass: flag("--mutation-class"),
			Reversibility: flag("--reversibility"),
			BlastRadius:   flag("--blast-radius"),
			TargetKind:    flag("--target-kind"),
			TargetID:      flag("--target-id"),
			TargetPath:    flag("--target-path"),
			Record:        !has("--dry-run"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		fmt.Printf("create-plan %s status=%s work=%s policy=%s allowed=%v\n", plan.ID, plan.Status, plan.WorkID, plan.Policy.Outcome, plan.Policy.Allowed)
		if plan.Policy.StandingGrantID != "" {
			fmt.Printf("standing_grant: %s\n", plan.Policy.StandingGrantID)
		}
		if len(plan.CreateCommand) > 0 {
			fmt.Printf("command: %s\n", strings.Join(plan.CreateCommand, " "))
		}
		if plan.NoAction != "" {
			fmt.Printf("no_action: %s\n", plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "create":
		w, err := store.Create(work.CreateInput{
			Owner:            flag("--owner"),
			Actor:            firstNonEmpty(flag("--by"), flag("--actor")),
			SuccessPredicate: flag("--predicate"),
			DesiredOutcome:   flag("--outcome"),
			NonGoals:         flagCSVValues(rest, "--non-goal", "--non-goals"),
			EvidenceRefs:     flagCSVValues(rest, "--evidence-ref", "--evidence"),
			ContextPackID:    flag("--context-pack"),
			State:            flag("--state"),
			Risk:             flag("--risk"),
			Privacy:          flag("--privacy"),
		}, time.Now())
		die(err)
		fmt.Printf("created %s (%s)\n", w.ID, w.State)
		printJSON(w)
	case "import-packet":
		packetPath := pos()
		if packetPath == "" {
			fmt.Fprintln(os.Stderr, "usage: compound work import-packet <packet.md> [--owner O] [--by A] [--state proposed|ready] [--context-pack ctx] [--risk tier1] [--privacy private]")
			os.Exit(2)
		}
		report, err := store.ImportPacket(packetPath, work.PacketImportOptions{
			Owner:         flag("--owner"),
			Actor:         firstNonEmpty(flag("--by"), flag("--actor")),
			State:         flag("--state"),
			Risk:          flag("--risk"),
			Privacy:       flag("--privacy"),
			ContextPackID: flag("--context-pack"),
		}, time.Now())
		die(err)
		fmt.Printf("imported packet %q -> %s created=%v state=%s\n", report.PacketTitle, report.Work.ID, report.Created, report.Work.State)
		printJSON(report)
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
	case "dispatch-plan":
		usage := "usage: compound work dispatch-plan [--actor A] [--source-packet packet.md] [--source-state proposed|ready] [--source-owner O] [--source-context-pack ctx] [--source-risk tier1] [--source-privacy private] [--json] [--dry-run]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run":
			case "--actor", "--source-packet", "--source-state", "--source-owner", "--source-context-pack", "--source-risk", "--source-privacy":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		if has("--dry-run") && flag("--source-packet") != "" {
			fmt.Fprintln(os.Stderr, "usage: --dry-run cannot be combined with --source-packet because packet import writes source-backed work; use a temp COMPOUND_HOME for preview")
			os.Exit(2)
		}
		plan, err := store.DispatchPlan(time.Now(), work.DispatchPlanOptions{
			Actor:                 flag("--actor"),
			Record:                !has("--dry-run"),
			SourcePacketPath:      flag("--source-packet"),
			SourcePacketState:     flag("--source-state"),
			SourcePacketOwner:     flag("--source-owner"),
			SourcePacketRisk:      flag("--source-risk"),
			SourcePacketPrivacy:   flag("--source-privacy"),
			SourcePacketContextID: flag("--source-context-pack"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		if plan.Selected != nil {
			fmt.Printf("dispatch-plan %s status=%s selected=%s action=%s\n", plan.ID, plan.Status, plan.Selected.WorkID, plan.Selected.Action)
			if len(plan.Selected.Command) > 0 {
				fmt.Printf("command: %s\n", strings.Join(plan.Selected.Command, " "))
			}
		} else {
			fmt.Printf("dispatch-plan %s status=%s no_action=%s\n", plan.ID, plan.Status, plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "dispatch-claim":
		usage := "usage: compound work dispatch-claim [--actor A] [--source-packet packet.md] [--source-state proposed|ready] [--source-owner O] [--source-context-pack ctx] [--source-risk tier1] [--source-privacy private] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json":
			case "--actor", "--source-packet", "--source-state", "--source-owner", "--source-context-pack", "--source-risk", "--source-privacy":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		result, err := store.DispatchClaim(time.Now(), work.DispatchPlanOptions{
			Actor:                 flag("--actor"),
			SourcePacketPath:      flag("--source-packet"),
			SourcePacketState:     flag("--source-state"),
			SourcePacketOwner:     flag("--source-owner"),
			SourcePacketRisk:      flag("--source-risk"),
			SourcePacketPrivacy:   flag("--source-privacy"),
			SourcePacketContextID: flag("--source-context-pack"),
		})
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.ClaimedWorkID != "" {
			fmt.Printf("dispatch-claim %s status=%s claimed=%s policy=%s\n", result.ID, result.Status, result.ClaimedWorkID, result.PolicyDecisionID)
		} else {
			fmt.Printf("dispatch-claim %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.DispatchPlanPath != "" {
			fmt.Printf("plan: %s\n", result.DispatchPlanPath)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "dispatch-advance":
		usage := "usage: compound work dispatch-advance [--actor A] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json":
			case "--actor":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		result, err := store.DispatchAdvance(time.Now(), work.DispatchPlanOptions{Actor: flag("--actor")})
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.AdvancedWorkID != "" {
			fmt.Printf("dispatch-advance %s status=%s advanced=%s policy=%s\n", result.ID, result.Status, result.AdvancedWorkID, result.PolicyDecisionID)
		} else {
			fmt.Printf("dispatch-advance %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "dispatch-close-plan":
		usage := "usage: compound work dispatch-close-plan [--actor A] [--outcome completed|failed|cancelled] [--move-type type] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json":
			case "--actor", "--outcome", "--move-type", "--capability", "--action", "--mutation-class", "--reversibility", "--blast-radius", "--target-kind", "--target-id", "--target-path":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		result, err := store.DispatchClosePlan(time.Now(), work.DispatchClosePlanOptions{
			Actor:         flag("--actor"),
			Outcome:       flag("--outcome"),
			MoveType:      flag("--move-type"),
			Capability:    flag("--capability"),
			Action:        flag("--action"),
			MutationClass: flag("--mutation-class"),
			Reversibility: flag("--reversibility"),
			BlastRadius:   flag("--blast-radius"),
			TargetKind:    flag("--target-kind"),
			TargetID:      flag("--target-id"),
			TargetPath:    flag("--target-path"),
		})
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.ClosePlanID != "" && result.Selected != nil {
			fmt.Printf("dispatch-close-plan %s status=%s selected=%s close_plan=%s\n", result.ID, result.Status, result.Selected.WorkID, result.ClosePlanID)
		} else {
			fmt.Printf("dispatch-close-plan %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.ClosePlanPath != "" {
			fmt.Printf("close_plan: %s\n", result.ClosePlanPath)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "dispatch-close":
		usage := "usage: compound work dispatch-close [--actor A] [--outcome completed|failed|cancelled] [--move-type type] [--summary S] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json":
			case "--actor", "--outcome", "--move-type", "--summary":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		result, err := store.DispatchClose(time.Now(), work.DispatchCloseOptions{
			Actor:    flag("--actor"),
			Outcome:  flag("--outcome"),
			MoveType: flag("--move-type"),
			Summary:  flag("--summary"),
		})
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.ClosedWorkID != "" {
			fmt.Printf("dispatch-close %s status=%s closed=%s receipt=%s transition=%s\n", result.ID, result.Status, result.ClosedWorkID, result.ReceiptID, result.TransitionID)
		} else {
			fmt.Printf("dispatch-close %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.DispatchClosePlanPath != "" {
			fmt.Printf("dispatch_close_plan: %s\n", result.DispatchClosePlanPath)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "dispatch-step":
		usage := "usage: compound work dispatch-step [--actor A] [--source-packet packet.md] [--source-state proposed|ready] [--source-owner O] [--source-context-pack ctx] [--source-risk tier1] [--source-privacy private] [--outcome completed|failed|cancelled] [--move-type type] [--summary S] [--json]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json":
			case "--actor", "--source-packet", "--source-state", "--source-owner", "--source-context-pack", "--source-risk", "--source-privacy", "--outcome", "--move-type", "--summary":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
		}
		result, err := store.DispatchStep(time.Now(), work.DispatchStepOptions{
			Actor:                 flag("--actor"),
			Outcome:               flag("--outcome"),
			MoveType:              flag("--move-type"),
			Summary:               flag("--summary"),
			SourcePacketPath:      flag("--source-packet"),
			SourcePacketState:     flag("--source-state"),
			SourcePacketOwner:     flag("--source-owner"),
			SourcePacketRisk:      flag("--source-risk"),
			SourcePacketPrivacy:   flag("--source-privacy"),
			SourcePacketContextID: flag("--source-context-pack"),
		})
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		if result.WorkID != "" {
			fmt.Printf("dispatch-step %s status=%s action=%s work=%s\n", result.ID, result.Status, result.Action, result.WorkID)
		} else {
			fmt.Printf("dispatch-step %s status=%s no_action=%s\n", result.ID, result.Status, result.NoAction)
		}
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
	case "claim-plan":
		usage := "usage: compound work claim-plan <work_id> [--actor A] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json] [--dry-run]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run":
			case "--actor", "--capability", "--action", "--mutation-class", "--reversibility", "--blast-radius", "--target-kind", "--target-id", "--target-path":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				if strings.HasPrefix(rest[i], "-") {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
			}
		}
		if pos() == "" {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		plan, err := store.ClaimPlan(pos(), time.Now(), work.ClaimPlanOptions{
			Actor:         flag("--actor"),
			Capability:    flag("--capability"),
			Action:        flag("--action"),
			MutationClass: flag("--mutation-class"),
			Reversibility: flag("--reversibility"),
			BlastRadius:   flag("--blast-radius"),
			TargetKind:    flag("--target-kind"),
			TargetID:      flag("--target-id"),
			TargetPath:    flag("--target-path"),
			Record:        !has("--dry-run"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		fmt.Printf("claim-plan %s status=%s work=%s policy=%s allowed=%v\n", plan.ID, plan.Status, plan.WorkID, plan.Policy.Outcome, plan.Policy.Allowed)
		if plan.Policy.StandingGrantID != "" {
			fmt.Printf("standing_grant: %s\n", plan.Policy.StandingGrantID)
		}
		if len(plan.ClaimCommand) > 0 {
			fmt.Printf("command: %s\n", strings.Join(plan.ClaimCommand, " "))
		}
		if plan.NoAction != "" {
			fmt.Printf("no_action: %s\n", plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "claim":
		w, err := store.Claim(pos(), flag("--by"), time.Now())
		die(err)
		fmt.Printf("%s claimed by %s (%s) policy=%s\n", w.ID, w.Authority.Owner, w.State, w.PolicyDecisionID)
	case "advance-plan":
		usage := "usage: compound work advance-plan <work_id> --to <state> [--actor A] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json] [--dry-run]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run":
			case "--to", "--actor", "--capability", "--action", "--mutation-class", "--reversibility", "--blast-radius", "--target-kind", "--target-id", "--target-path":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				if strings.HasPrefix(rest[i], "-") {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
			}
		}
		if pos() == "" || flag("--to") == "" {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		plan, err := store.AdvancePlan(pos(), time.Now(), work.AdvancePlanOptions{
			Actor:         flag("--actor"),
			To:            flag("--to"),
			Capability:    flag("--capability"),
			Action:        flag("--action"),
			MutationClass: flag("--mutation-class"),
			Reversibility: flag("--reversibility"),
			BlastRadius:   flag("--blast-radius"),
			TargetKind:    flag("--target-kind"),
			TargetID:      flag("--target-id"),
			TargetPath:    flag("--target-path"),
			Record:        !has("--dry-run"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		fmt.Printf("advance-plan %s status=%s work=%s to=%s policy=%s allowed=%v\n", plan.ID, plan.Status, plan.WorkID, plan.ToState, plan.Policy.Outcome, plan.Policy.Allowed)
		if plan.Policy.StandingGrantID != "" {
			fmt.Printf("standing_grant: %s\n", plan.Policy.StandingGrantID)
		}
		if len(plan.AdvanceCommand) > 0 {
			fmt.Printf("command: %s\n", strings.Join(plan.AdvanceCommand, " "))
		}
		if plan.NoAction != "" {
			fmt.Printf("no_action: %s\n", plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "advance":
		w, err := store.Advance(pos(), work.AdvanceInput{
			To:             flag("--to"),
			Actor:          firstNonEmpty(flag("--by"), flag("--actor")),
			RunID:          flag("--run"),
			ArtifactID:     flag("--artifact"),
			VerificationID: flag("--verification"),
			ContextPackID:  flag("--context-pack"),
			Note:           flag("--note"),
		}, time.Now())
		die(err)
		fmt.Printf("%s -> %s policy=%s\n", w.ID, w.State, w.PolicyDecisionID)
	case "observe-run":
		workID := pos()
		if workID == "" || flag("--trace") == "" {
			fmt.Fprintln(os.Stderr, "usage: compound work observe-run <work_id> --trace <trace_id> [--actor A] [--note N] [--json]")
			os.Exit(2)
		}
		result, err := store.ObserveRun(workID, work.ObserveRunInput{
			Actor:   firstNonEmpty(flag("--by"), flag("--actor")),
			TraceID: flag("--trace"),
			Note:    flag("--note"),
		}, time.Now())
		die(err)
		if has("--json") {
			printJSON(result)
			return
		}
		fmt.Printf("%s observed trace=%s state=%s policy=%s\n", result.WorkID, result.TraceID, result.State, result.PolicyDecisionID)
		if result.ResultPath != "" {
			fmt.Printf("result: %s\n", result.ResultPath)
		}
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
	case "close-plan":
		usage := "usage: compound work close-plan <work_id> --outcome completed|failed|cancelled --move-type <type> [--actor A] [--capability C] [--action A] [--mutation-class C] [--reversibility R] [--blast-radius B] [--target-kind K] [--target-id ID] [--target-path P] [--json] [--dry-run]"
		for i := 0; i < len(rest); i++ {
			switch rest[i] {
			case "--json", "--dry-run":
			case "--outcome", "--move-type", "--actor", "--capability", "--action", "--mutation-class", "--reversibility", "--blast-radius", "--target-kind", "--target-id", "--target-path":
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
				i++
			default:
				if strings.HasPrefix(rest[i], "-") {
					fmt.Fprintln(os.Stderr, usage)
					os.Exit(2)
				}
			}
		}
		if pos() == "" || flag("--outcome") == "" || flag("--move-type") == "" {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		plan, err := store.ClosePlan(pos(), time.Now(), work.ClosePlanOptions{
			Actor:         flag("--actor"),
			Outcome:       flag("--outcome"),
			MoveType:      flag("--move-type"),
			Capability:    flag("--capability"),
			Action:        flag("--action"),
			MutationClass: flag("--mutation-class"),
			Reversibility: flag("--reversibility"),
			BlastRadius:   flag("--blast-radius"),
			TargetKind:    flag("--target-kind"),
			TargetID:      flag("--target-id"),
			TargetPath:    flag("--target-path"),
			Record:        !has("--dry-run"),
		})
		die(err)
		if has("--json") {
			printJSON(plan)
			return
		}
		fmt.Printf("close-plan %s status=%s work=%s outcome=%s move_type=%s policy=%s allowed=%v\n", plan.ID, plan.Status, plan.WorkID, plan.Outcome, plan.MoveType, plan.Policy.Outcome, plan.Policy.Allowed)
		if plan.Policy.StandingGrantID != "" {
			fmt.Printf("standing_grant: %s\n", plan.Policy.StandingGrantID)
		}
		if len(plan.CloseCommand) > 0 {
			fmt.Printf("command: %s\n", strings.Join(plan.CloseCommand, " "))
		}
		if len(plan.ProofRefs) > 0 {
			fmt.Printf("proof_refs: %s\n", strings.Join(plan.ProofRefs, ","))
		}
		if len(plan.RunObservations) > 0 {
			fmt.Printf("run_observations: %d\n", len(plan.RunObservations))
		}
		if plan.NoAction != "" {
			fmt.Printf("no_action: %s\n", plan.NoAction)
		}
		if plan.PlanPath != "" {
			fmt.Printf("plan: %s\n", plan.PlanPath)
		}
	case "close":
		res, err := store.Close(pos(), work.CloseInput{
			Outcome:                    flag("--outcome"),
			MoveType:                   flag("--move-type"),
			Actor:                      firstNonEmpty(flag("--by"), flag("--actor")),
			Summary:                    flag("--summary"),
			Changed:                    has("--changed"),
			MissedCapabilities:         splitCSV(flag("--missed")),
			FalseTriggeredCapabilities: splitCSV(flag("--false-triggered")),
			ProofRefs:                  splitCSV(flag("--proof")),
			MemoryCandidates:           splitCSV(flag("--memory-candidate")),
			MemoryPromoted:             splitCSV(flag("--memory-promoted")),
			MemoryParked:               splitCSV(flag("--memory-parked")),
			MemoryRejected:             splitCSV(flag("--memory-rejected")),
		}, time.Now())
		die(err)
		fmt.Printf("%s closed: %s  move_type=%s  policy=%s  receipt=%s  transition=%s\n",
			res.Work.ID, res.Work.State, res.Receipt.MoveType, res.Work.PolicyDecisionID, res.Receipt.ID, res.Transition.ID)
		printJSON(res)
		autoConsumeCapabilityFeedback(homePath)
	default:
		fmt.Fprintln(os.Stderr, "usage: compound work <create-plan|create|import-packet|show|list|dispatch-plan|dispatch-claim|dispatch-advance|dispatch-close-plan|dispatch-close|dispatch-step|claim-plan|claim|advance-plan|advance|observe-run|close-plan|close|act> ...")
		os.Exit(2)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound contract <compile|import-hooks|hooks-authority> ...")
		os.Exit(2)
	}
	switch args[0] {
	case "compile":
		homePath, err := home.Resolve("")
		die(err)
		in, err := parseOrientArgsWithHome(args[1:], homePath)
		die(err)
		if in.Harness == "" {
			fmt.Fprintln(os.Stderr, "usage: compound contract compile --harness <h> --cwd <dir> --task <t> [--repo-kind <k>] [--mode <m>]")
			os.Exit(2)
		}
		ec, err := contract.CompileWithRuntime(in, orientPolicy(), homePath, time.Now())
		die(err)
		printJSON(ec)
	case "import-hooks":
		sourcePath := valueFlag(args[1:], "--codex-hooks-json")
		registrationPath := valueFlag(args[1:], "--hook-registration-ts")
		if sourcePath == "" && registrationPath == "" && len(args) == 2 {
			sourcePath = args[1]
		}
		if (sourcePath == "" && registrationPath == "") || (sourcePath != "" && registrationPath != "") {
			fmt.Fprintln(os.Stderr, "usage: compound contract import-hooks (--codex-hooks-json <codex-hooks-json.json>|--hook-registration-ts <hook-registration.ts>)")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		die(err)
		var report contract.InstalledHooksImportReport
		if registrationPath != "" {
			report, err = contract.ImportHookRegistrationTS(homePath, registrationPath, time.Now())
		} else {
			report, err = contract.ImportCodexHooksJSON(homePath, sourcePath, time.Now())
		}
		die(err)
		printJSON(report)
	case "hooks-authority":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "usage: compound contract hooks-authority")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		die(err)
		type hooksAuthorityReport struct {
			AuthorityRef     string                                   `json:"authority_ref"`
			AuthorityPath    string                                   `json:"authority_path"`
			Installed        bool                                     `json:"installed"`
			HookCount        int                                      `json:"hook_count,omitempty"`
			Harnesses        []string                                 `json:"harnesses,omitempty"`
			SourceAuthority  []contract.InstalledHooksSourceAuthority `json:"source_authority,omitempty"`
			SourceRecordPath string                                   `json:"source_record_path"`
		}
		report := hooksAuthorityReport{
			AuthorityRef:     contract.InstalledHooksAuthorityRef,
			AuthorityPath:    contract.InstalledHooksPath(homePath),
			SourceRecordPath: contract.InstalledHooksSourceAuthorityPath(homePath),
		}
		manifest, err := contract.LoadInstalledHooksManifest(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				die(err)
			}
		} else {
			report.Installed = true
			report.HookCount = len(manifest.Hooks)
			report.Harnesses = hookManifestHarnesses(manifest)
		}
		records, err := contract.LoadInstalledHooksSourceAuthority(homePath)
		die(err)
		report.SourceAuthority = records
		printJSON(report)
	default:
		fmt.Fprintln(os.Stderr, "usage: compound contract <compile|import-hooks|hooks-authority> ...")
		os.Exit(2)
	}
}

func hookManifestHarnesses(manifest contract.InstalledHooksManifest) []string {
	seen := map[string]bool{}
	for _, hook := range manifest.Hooks {
		h := strings.TrimSpace(hook.Harness)
		if h != "" {
			seen[h] = true
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
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
	positionals := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--home" && i+1 < len(args):
			flagHome = args[i+1]
			i++
		case (a == "--target" || a == "--archive") && i+1 < len(args):
			i++
		case a == "--local":
			kind = "local"
		case a == "--global":
			kind = "global"
		case strings.HasPrefix(a, "-"):
			// command-specific boolean flags are read below.
		default:
			positionals = append(positionals, a)
		}
	}
	if len(positionals) > 0 {
		sub = positionals[0]
	}
	restPos := []string{}
	if len(positionals) > 1 {
		restPos = positionals[1:]
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
	case "backup":
		report, err := home.Backup(resolve(), flagValue(args, "--target"), time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "home backup:", err)
			os.Exit(1)
		}
		printHomeArchiveReport("backup", report)
	case "export":
		report, err := home.ExportPublicFixture(resolve(), flagValue(args, "--target"), time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "home export:", err)
			os.Exit(1)
		}
		printHomeArchiveReport("export", report)
	case "verify":
		archivePath := flagValue(args, "--archive")
		if archivePath == "" && len(restPos) > 0 {
			archivePath = restPos[0]
		}
		if archivePath == "" {
			printHomeDoctor(home.Doctor(resolve()))
			return
		}
		report, err := home.VerifyArchive(archivePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "home verify:", err)
			os.Exit(1)
		}
		printHomeVerifyReport(report)
	case "restore":
		archivePath := flagValue(args, "--archive")
		if archivePath == "" && len(restPos) > 0 {
			archivePath = restPos[0]
		}
		targetPath := flagValue(args, "--target")
		if archivePath == "" || targetPath == "" {
			fmt.Fprintln(os.Stderr, "usage: compound home restore <archive.tar.gz> --target <empty-home-path>")
			os.Exit(2)
		}
		report, err := home.RestoreArchive(archivePath, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "home restore:", err)
			os.Exit(1)
		}
		printHomeRestoreReport(report)
	case "compact":
		report, err := home.Compact(resolve(), home.CompactOptions{
			DryRun:      hasFlag(args, "--dry-run"),
			Rebuildable: hasFlag(args, "--rebuildable"),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "home compact:", err)
			os.Exit(1)
		}
		printHomeCompactReport(report)
	default:
		fmt.Fprintln(os.Stderr, "usage: compound home <init|doctor|where|backup|export|verify|restore|compact> [--home <path>] [--local|--global]")
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

func printHomeArchiveReport(action string, r home.ArchiveReport) {
	fmt.Printf("# home %s\n", action)
	fmt.Printf("- kind: %s\n", r.Kind)
	fmt.Printf("- archive: %s\n", r.ArchivePath)
	fmt.Printf("- files: %d\n", r.Files)
	fmt.Printf("- bytes: %d\n", r.Bytes)
	for _, skipped := range r.Skipped {
		fmt.Printf("- skipped_root: %s\n", skipped)
	}
}

func printHomeVerifyReport(r home.VerifyReport) {
	fmt.Println("# home verify")
	fmt.Printf("- archive: %s\n", r.ArchivePath)
	fmt.Printf("- kind: %s\n", r.Kind)
	fmt.Printf("- files: %d\n", r.Files)
	fmt.Printf("- bytes: %d\n", r.Bytes)
	fmt.Printf("- verdict: %s\n", homeOKStr(r.OK))
}

func printHomeRestoreReport(r home.RestoreReport) {
	fmt.Println("# home restore")
	fmt.Printf("- archive: %s\n", r.ArchivePath)
	fmt.Printf("- target_home: %s\n", r.TargetHome)
	fmt.Printf("- kind: %s\n", r.Kind)
	fmt.Printf("- files: %d\n", r.Files)
	fmt.Printf("- bytes: %d\n", r.Bytes)
	fmt.Println("- verdict: ok")
}

func printHomeCompactReport(r home.CompactReport) {
	fmt.Println("# home compact")
	fmt.Printf("- home: %s\n", r.Home)
	fmt.Printf("- dry_run: %v\n", r.DryRun)
	fmt.Printf("- removed_files: %d\n", r.RemovedFiles)
	fmt.Printf("- removed_bytes: %d\n", r.RemovedBytes)
	for _, dir := range r.ClearedDirs {
		fmt.Printf("- cleared_dir: %s\n", dir)
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

func cmdSchema(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound schema <list|export|validate> ...")
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		for _, c := range schema.Contracts() {
			fmt.Printf("%-20s %s\n", c.Name, c.Description)
		}
	case "export":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: compound schema export <name>|--all [--dir <dir>]")
			os.Exit(2)
		}
		if args[1] == "--all" {
			dir := flagValue(args[2:], "--dir")
			if dir == "" {
				all := map[string]any{}
				for _, c := range schema.Contracts() {
					doc, err := schema.Export(c.Name)
					die(err)
					all[c.Name] = doc
				}
				printJSON(all)
				return
			}
			die(os.MkdirAll(dir, 0o755))
			for _, c := range schema.Contracts() {
				doc, err := schema.Export(c.Name)
				die(err)
				b, err := json.MarshalIndent(doc, "", "  ")
				die(err)
				path := filepath.Join(dir, c.Name+".schema.json")
				die(os.WriteFile(path, append(b, '\n'), 0o644))
				fmt.Printf("- %s\n", path)
			}
			return
		}
		doc, err := schema.Export(args[1])
		die(err)
		printJSON(doc)
	case "validate":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: compound schema validate <name> <file>")
			os.Exit(2)
		}
		die(schema.Validate(args[1], args[2]))
		fmt.Printf("%s: ok (%s)\n", args[2], args[1])
	default:
		fmt.Fprintln(os.Stderr, "usage: compound schema <list|export|validate> ...")
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
		fmt.Fprintln(os.Stderr, "usage: compound authority import [registry.yaml] | path <path-authority.yaml> | path-registry <os-path-authority.yaml> | git <repo-path> | dirty-plan <repo-path> [--work-id id] | import-job-roster <cron-jobs.json> | import-job-token-governance <cron-token-usage.json> | job-roster-authority [--json] | job-health-plan | job-health-review <review.yaml> | resolve <token> | <entity>")
		os.Exit(2)
	}
	store, homePath := openMemStore()
	if args[0] == "import-job-roster" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority import-job-roster <cron-jobs.json>")
			os.Exit(2)
		}
		report, err := authority.ImportJobRoster(homePath, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority import-job-roster:", err)
			os.Exit(1)
		}
		printJSON(report)
		return
	}
	if args[0] == "import-job-token-governance" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority import-job-token-governance <cron-token-usage.json>")
			os.Exit(2)
		}
		report, err := authority.ImportJobTokenGovernance(homePath, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority import-job-token-governance:", err)
			os.Exit(1)
		}
		printJSON(report)
		return
	}
	if args[0] == "job-roster-authority" {
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound authority job-roster-authority [--json]")
			os.Exit(2)
		}
		type jobRosterAuthorityReport struct {
			AuthorityRef             string                                     `json:"authority_ref"`
			AuthorityPath            string                                     `json:"authority_path"`
			Installed                bool                                       `json:"installed"`
			GeneratedAt              string                                     `json:"generated_at,omitempty"`
			SourcePath               string                                     `json:"source_path,omitempty"`
			SourceRevision           string                                     `json:"source_revision,omitempty"`
			JobCount                 int                                        `json:"job_count,omitempty"`
			EnabledCount             int                                        `json:"enabled_count,omitempty"`
			RecentRunCount           int                                        `json:"recent_run_count,omitempty"`
			ErrorCount               int                                        `json:"error_count,omitempty"`
			RecentErrors             []authority.JobSummary                     `json:"recent_errors,omitempty"`
			TokenUsageSourcePath     string                                     `json:"token_usage_source_path,omitempty"`
			TokenUsageSourceRevision string                                     `json:"token_usage_source_revision,omitempty"`
			TokenMetric              string                                     `json:"token_metric,omitempty"`
			TokenSessionsCount       int                                        `json:"token_sessions_count"`
			EnabledAgentCount        int                                        `json:"enabled_agent_count"`
			HighTokenEnabledCount    int                                        `json:"high_token_enabled_count"`
			TokenRiskJobs            []authority.JobSummary                     `json:"token_risk_jobs,omitempty"`
			SourceAuthority          []authority.JobRosterSourceAuthorityRecord `json:"source_authority,omitempty"`
			SourceRecordPath         string                                     `json:"source_record_path"`
		}
		report := jobRosterAuthorityReport{
			AuthorityRef:     authority.JobRosterAuthorityRef,
			AuthorityPath:    authority.JobRosterAuthorityPath(homePath),
			SourceRecordPath: authority.JobRosterSourceAuthorityPath(homePath),
		}
		roster, err := authority.LoadJobRosterAuthority(homePath)
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "authority job-roster-authority:", err)
				os.Exit(1)
			}
		} else {
			report.Installed = true
			report.GeneratedAt = roster.GeneratedAt
			report.SourcePath = roster.SourcePath
			report.SourceRevision = roster.SourceRevision
			report.JobCount = roster.JobCount
			report.EnabledCount = roster.EnabledCount
			report.RecentRunCount = roster.RecentRunCount
			report.ErrorCount = roster.ErrorCount
			report.RecentErrors = roster.RecentErrors
			report.TokenUsageSourcePath = roster.TokenUsageSourcePath
			report.TokenUsageSourceRevision = roster.TokenUsageSourceRevision
			report.TokenMetric = roster.TokenMetric
			report.TokenSessionsCount = roster.TokenSessionsCount
			report.EnabledAgentCount = roster.EnabledAgentCount
			report.HighTokenEnabledCount = roster.HighTokenEnabledCount
			report.TokenRiskJobs = roster.TokenRiskJobs
		}
		records, err := authority.LoadJobRosterSourceAuthority(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority job-roster-authority:", err)
			os.Exit(1)
		}
		report.SourceAuthority = records
		if len(args) == 2 && args[1] == "--json" {
			printJSON(report)
			return
		}
		fmt.Printf("job roster authority: %s\n", report.AuthorityRef)
		fmt.Printf("installed: %t\n", report.Installed)
		fmt.Printf("jobs: %d enabled=%d recent=%d errors=%d\n", report.JobCount, report.EnabledCount, report.RecentRunCount, report.ErrorCount)
		return
	}
	if args[0] == "job-health-plan" {
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "usage: compound authority job-health-plan")
			os.Exit(2)
		}
		roster, err := authority.LoadJobRosterAuthority(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority job-health-plan:", err)
			os.Exit(1)
		}
		report, err := authority.RecordJobHealthPlans(homePath, roster, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority job-health-plan:", err)
			os.Exit(1)
		}
		printJSON(report)
		return
	}
	if args[0] == "job-health-review" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority job-health-review <review.yaml>")
			os.Exit(2)
		}
		review, err := authority.LoadJobHealthReview(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority job-health-review:", err)
			os.Exit(1)
		}
		report, err := authority.RecordJobHealthReview(homePath, review, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority job-health-review:", err)
			os.Exit(1)
		}
		printJSON(report)
		return
	}
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
	if args[0] == "dirty-plan" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority dirty-plan <repo-path> [--work-id id]")
			os.Exit(2)
		}
		workID := flagValue(args[2:], "--work-id")
		snap, _, _, _, err := authority.ImportGitSnapshot(args[1], nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority dirty-plan:", err)
			os.Exit(1)
		}
		plan, err := authority.BuildSourceControlReviewPlan(snap, workID, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority dirty-plan:", err)
			os.Exit(1)
		}
		paths, err := authority.RecordSourceControlReviewPlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority dirty-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# source-control dirty-plan %s\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- repo: %s\n", plan.RepoID)
		if plan.WorkID != "" {
			fmt.Printf("- work: %s\n", plan.WorkID)
		}
		fmt.Printf("- source_status: %s\n", plan.SourceStatus)
		fmt.Printf("- changed_paths: %d\n", plan.ChangedPaths)
		fmt.Printf("- staged: %d\n", plan.Staged)
		fmt.Printf("- unstaged: %d\n", plan.Unstaged)
		fmt.Printf("- untracked: %d\n", plan.Untracked)
		fmt.Printf("- required_evidence: %d\n", len(plan.ReviewDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
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
	return "usage: compound product authority [--json] | import <product.yaml>|--project-contract <project.yaml> [--release <release.yaml>] | signal <signal.yaml>|--asc-datapoints <json> --product <id> --app-id <asc_id> [--metric <metric>]|--asc-sales-series <jsonl> --product <id> --app-id <asc_id> --before <range> --after <range> --change-event <text> [--metric <metric>] | canonical-metrics <json> --product <id> | change-stream <candidates.md> | hypothesis-plan --candidate <candidate_id> [--target <file>] | review-plan --hypothesis <hypothesis_id> --signal <signal_id> [--target <file>] | source-issue-plan --product <product_id> --issue <issue_id> [--target <file>] | review <review.yaml> | source-issue-review <review.yaml> | timeline <product_id> | doctor <product_id> [--writeback]"
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
	case "authority":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound product authority [--json]")
			os.Exit(2)
		}
		report, err := product.Authority(homePath, store)
		if err != nil {
			fmt.Fprintln(os.Stderr, "product authority:", err)
			os.Exit(1)
		}
		if len(args) == 2 && args[1] == "--json" {
			b, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				fmt.Fprintln(os.Stderr, "product authority:", err)
				os.Exit(1)
			}
			fmt.Println(string(b))
			return
		}
		fmt.Printf("# product authority\n\n")
		fmt.Printf("- authority: %s\n", report.AuthorityRef)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- products: %d\n", report.ProductCount)
		fmt.Printf("- signals: %d\n", report.SignalCount)
		fmt.Printf("- hypotheses: %d\n", report.HypothesisCount)
		fmt.Printf("- review_ready: %d\n", report.ReviewReadyCount)
		fmt.Printf("- source_issues: %d\n", report.SourceIssueCount)
		for _, item := range report.Products {
			fmt.Printf("- product %s: %s (%d signal, %d hypothesis, %d transition)\n", item.ProductID, item.Status, item.SignalCount, item.HypothesisCount, item.TransitionCount)
			for _, command := range item.ReviewPlanCommands {
				fmt.Printf("  review_plan: %s\n", command)
			}
		}
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
	case "canonical-metrics":
		productID := flagValue(args[1:], "--product")
		var sourcePath string
		for i := 1; i < len(args); i++ {
			switch {
			case args[i] == "--product":
				i++
			case strings.HasPrefix(args[i], "-"):
				fmt.Fprintln(os.Stderr, "usage: compound product canonical-metrics <json> --product <id>")
				os.Exit(2)
			case sourcePath == "":
				sourcePath = args[i]
			default:
				fmt.Fprintln(os.Stderr, "usage: compound product canonical-metrics <json> --product <id>")
				os.Exit(2)
			}
		}
		if sourcePath == "" || productID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound product canonical-metrics <json> --product <id>")
			os.Exit(2)
		}
		report, err := product.ApplyCanonicalMetrics(homePath, store, sourcePath, product.CanonicalMetricsOptions{ProductID: productID}, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product canonical-metrics:", err)
			os.Exit(1)
		}
		fmt.Printf("# product canonical-metrics %s\n\n", report.SourceRef)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- product: %s\n", report.ProductID)
		fmt.Printf("- source_hash: %s\n", report.SourceHash)
		fmt.Printf("- ideas_read: %d\n", report.IdeasRead)
		fmt.Printf("- signals_created: %d\n", report.SignalsCreated)
		fmt.Printf("- signals_existing: %d\n", report.SignalsExisting)
		fmt.Printf("- hypotheses_created: %d\n", report.HypothesesCreated)
		fmt.Printf("- hypotheses_existing: %d\n", report.HypothesesExisting)
		for _, id := range report.SignalIDs {
			fmt.Printf("- signal: %s\n", id)
		}
		for _, id := range report.HypothesisIDs {
			fmt.Printf("- hypothesis: %s\n", id)
		}
		for _, id := range report.TransitionIDs {
			fmt.Printf("- transition: %s\n", id)
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
	case "change-stream":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound product change-stream <candidates.md>")
			os.Exit(2)
		}
		doc, err := product.LoadChangeStreamCandidates(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "product change-stream:", err)
			os.Exit(1)
		}
		report, err := product.ApplyChangeStreamCandidates(homePath, doc, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product change-stream:", err)
			os.Exit(1)
		}
		fmt.Printf("# product change-stream %s\n\n", report.SourceRef)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- candidates_read: %d\n", report.CandidatesRead)
		fmt.Printf("- issues_created: %d\n", report.IssuesCreated)
		fmt.Printf("- issues_existing: %d\n", report.IssuesExisting)
		if report.SourceHash != "" {
			fmt.Printf("- source_hash: %s\n", report.SourceHash)
		}
		for _, productID := range report.ProductIDs {
			fmt.Printf("- product: %s\n", productID)
		}
		for _, issueID := range report.IssueIDs {
			fmt.Printf("- issue: %s\n", issueID)
		}
	case "hypothesis-plan":
		candidateID := flagValue(args[1:], "--candidate")
		targetPath := flagValue(args[1:], "--target")
		if candidateID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound product hypothesis-plan --candidate <candidate_id> [--target <file>]")
			os.Exit(2)
		}
		plan, err := product.BuildHypothesisPlan(homePath, candidateID, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product hypothesis-plan:", err)
			os.Exit(1)
		}
		paths, err := product.RecordHypothesisPlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "product hypothesis-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# product hypothesis-plan %s\n\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- candidate: %s\n", plan.CandidateID)
		fmt.Printf("- product: %s\n", plan.ProductID)
		fmt.Printf("- hypothesis_key: %s\n", plan.HypothesisKey)
		fmt.Printf("- review_id: %s\n", plan.ReviewDraft.ReviewID)
		if plan.TargetPath != "" {
			fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		}
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- required_evidence: %d\n", len(plan.ReviewDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		fmt.Printf("- signal_draft: %s\n", paths.SignalDraftPath)
	case "review-plan":
		hypothesisID := flagValue(args[1:], "--hypothesis")
		signalID := flagValue(args[1:], "--signal")
		targetPath := flagValue(args[1:], "--target")
		if hypothesisID == "" || signalID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound product review-plan --hypothesis <hypothesis_id> --signal <signal_id> [--target <file>]")
			os.Exit(2)
		}
		plan, err := product.BuildReviewPlan(homePath, hypothesisID, signalID, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product review-plan:", err)
			os.Exit(1)
		}
		paths, err := product.RecordReviewPlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "product review-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# product review-plan %s\n\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- product: %s\n", plan.ProductID)
		fmt.Printf("- hypothesis: %s\n", plan.HypothesisID)
		fmt.Printf("- signal: %s\n", plan.SignalID)
		fmt.Printf("- review_id: %s\n", plan.ReviewDraft.ReviewID)
		if plan.TargetPath != "" {
			fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		}
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- required_evidence: %d\n", len(plan.ReviewDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		fmt.Printf("- review_yaml: %s\n", paths.ReviewYAMLPath)
	case "source-issue-plan":
		productID := flagValue(args[1:], "--product")
		issueID := flagValue(args[1:], "--issue")
		targetPath := flagValue(args[1:], "--target")
		if productID == "" || issueID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound product source-issue-plan --product <product_id> --issue <issue_id> [--target <file>]")
			os.Exit(2)
		}
		plan, err := product.BuildSourceIssuePlan(homePath, productID, issueID, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product source-issue-plan:", err)
			os.Exit(1)
		}
		paths, err := product.RecordSourceIssuePlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "product source-issue-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# product source-issue-plan %s\n\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- product: %s\n", plan.ProductID)
		fmt.Printf("- issue: %s\n", plan.IssueID)
		fmt.Printf("- source_ref: %s\n", plan.SourceRef)
		fmt.Printf("- review_id: %s\n", plan.ReviewDraft.ReviewID)
		if plan.TargetPath != "" {
			fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		}
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- required_evidence: %d\n", len(plan.ReviewDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
		fmt.Printf("- review_yaml: %s\n", paths.ReviewYAMLPath)
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
		if report.HypothesisID != "" {
			fmt.Printf("- hypothesis: %s\n", report.HypothesisID)
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
		fmt.Printf("- review_ready: %d\n", report.ReviewReadyCount)
		for _, ready := range report.ReviewReady {
			fmt.Printf("- review_plan: %s\n", ready.ReviewPlanCommand)
			fmt.Printf("  match: %s %s %s\n", ready.MatchReason, ready.MetricName, ready.Window)
		}
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
	store, homePath := openMemStore()
	switch sub {
	case "candidate-plan":
		candidateID := flagValue(args[1:], "--candidate")
		targetPath := flagValue(args[1:], "--target")
		if candidateID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound decisions candidate-plan --candidate <candidate_id> [--target <file>]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions candidate-plan:", err)
			os.Exit(1)
		}
		plan, err := decisions.BuildDecisionCandidatePlan(homePath, candidateID, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions candidate-plan:", err)
			os.Exit(1)
		}
		paths, err := decisions.RecordDecisionCandidatePlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions candidate-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# decisions candidate-plan %s\n\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- candidate: %s\n", plan.CandidateID)
		fmt.Printf("- decision_key: %s\n", plan.DecisionKey)
		fmt.Printf("- decision_id: %s\n", plan.LifecycleDraft.DecisionID)
		fmt.Printf("- entity: %s\n", plan.LifecycleDraft.EntityID)
		if plan.TargetPath != "" {
			fmt.Printf("- target_exists: %t\n", plan.TargetExists)
		}
		if plan.TargetHash != "" {
			fmt.Printf("- target_hash: %s\n", plan.TargetHash)
		}
		fmt.Printf("- required_evidence: %d\n", len(plan.LifecycleDraft.RequiredEvidence))
		fmt.Printf("- evidence_refs: %d\n", len(plan.EvidenceRefs))
		fmt.Printf("- plan: %s\n", paths.PlanPath)
		fmt.Printf("- review: %s\n", paths.ReviewPath)
	case "import":
		sourceKind := decisionSourceKind(value("--source-kind"))
		var sourceReports []decisions.BrainDecisionReport
		var sourceAuthority []decisions.SourceAuthorityRecord
		for _, path := range values("--source") {
			report, err := importDecisionSource(store, path, sourceKind, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "decision source import:", err)
				os.Exit(1)
			}
			rec, err := decisions.RecordSourceAuthority(homePath, decisions.DecisionImportReport(report), "decisions import --source", time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "decision source authority:", err)
				os.Exit(1)
			}
			sourceAuthority = append(sourceAuthority, rec)
			sourceReports = append(sourceReports, report)
		}
		for _, path := range values("--brain") {
			report, err := decisions.ImportBrainDecision(store, path, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "brain decision import:", err)
				os.Exit(1)
			}
			rec, err := decisions.RecordSourceAuthority(homePath, decisions.DecisionImportReport(report), "decisions import --brain", time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "brain decision source authority:", err)
				os.Exit(1)
			}
			sourceAuthority = append(sourceAuthority, rec)
			sourceReports = append(sourceReports, report)
		}
		for _, root := range values("--source-dir") {
			reports, err := importDecisionSourceDir(store, root, sourceKind, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "decision sources import:", err)
				os.Exit(1)
			}
			for _, report := range reports {
				rec, err := decisions.RecordSourceAuthority(homePath, decisions.DecisionImportReport(report), "decisions import --source-dir", time.Now())
				if err != nil {
					fmt.Fprintln(os.Stderr, "decision sources authority:", err)
					os.Exit(1)
				}
				sourceAuthority = append(sourceAuthority, rec)
			}
			sourceReports = append(sourceReports, reports...)
		}
		var policyReports []decisions.DecisionImportReport
		for _, path := range values("--policy") {
			report, err := decisions.ImportPolicyDecision(store, path, time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "policy decision import:", err)
				os.Exit(1)
			}
			rec, err := decisions.RecordSourceAuthority(homePath, report, "decisions import --policy", time.Now())
			if err != nil {
				fmt.Fprintln(os.Stderr, "policy decision source authority:", err)
				os.Exit(1)
			}
			sourceAuthority = append(sourceAuthority, rec)
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
			for _, report := range reports {
				rec, err := decisions.RecordSourceAuthority(homePath, report, "decisions import --policy-dir", time.Now())
				if err != nil {
					fmt.Fprintln(os.Stderr, "policy decisions source authority:", err)
					os.Exit(1)
				}
				sourceAuthority = append(sourceAuthority, rec)
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
		for _, report := range sourceReports {
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
		fmt.Printf("processed %d decision sources through spine memory (%d primary, %d parked)\n", len(captured)+len(sourceReports)+len(policyReports), promoted, parked)
		for _, report := range sourceReports {
			fmt.Printf("- source %s: %s kind=%s candidate=%s evidence=%s claim=%s\n", report.Lifecycle.ID, report.Verdict, report.SourceKind, report.CandidateID, report.EvidenceID, report.ClaimID)
		}
		for _, report := range policyReports {
			fmt.Printf("- policy %s: %s status=%s candidate=%s evidence=%s claim=%s\n", report.Lifecycle.ID, report.Verdict, report.Lifecycle.Status, report.CandidateID, report.EvidenceID, report.ClaimID)
		}
		for _, rec := range sourceAuthority {
			fmt.Printf("- source_authority: %s %s -> %s (%s)\n", rec.ID, rec.SourceRole, rec.AuthorityRef, rec.AuthorityStatus)
		}
	case "authority":
		sourceKind := decisionSourceKind(value("--source-kind"))
		policyDirs := values("--policy-dir")
		if root := strings.TrimSpace(os.Getenv("COMPOUND_POLICY_DECISIONS_DIR")); root != "" {
			policyDirs = append(policyDirs, root)
		}
		var eligible []string
		eligible = append(eligible, values("--source")...)
		eligible = append(eligible, values("--policy")...)
		eligible = append(eligible, values("--brain")...)
		for _, root := range policyDirs {
			files, err := decisions.EligiblePolicyDecisionFiles(root)
			if err != nil {
				fmt.Fprintln(os.Stderr, "decisions authority:", err)
				os.Exit(1)
			}
			eligible = append(eligible, files...)
		}
		for _, root := range values("--source-dir") {
			files, err := eligibleDecisionSourceFiles(root, sourceKind)
			if err != nil {
				fmt.Fprintln(os.Stderr, "decisions authority:", err)
				os.Exit(1)
			}
			eligible = append(eligible, files...)
		}
		report, err := decisions.SourceAuthorityCoverage(homePath, eligible)
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions authority:", err)
			os.Exit(1)
		}
		fmt.Printf("# decisions authority\n\n")
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- records: %d\n", len(report.Records))
		fmt.Printf("- eligible: %d\n", len(report.Eligible))
		fmt.Printf("- covered: %d\n", len(report.Eligible)-len(report.Missing))
		fmt.Printf("- missing: %d\n", len(report.Missing))
		for _, kind := range sortedMapKeys(report.CountsByKind) {
			fmt.Printf("- source_kind.%s: %d\n", kind, report.CountsByKind[kind])
		}
		for _, status := range sortedMapKeys(report.CountsByStatus) {
			fmt.Printf("- authority_status.%s: %d\n", status, report.CountsByStatus[status])
		}
		for _, path := range report.Missing {
			fmt.Printf("- missing_source: %s\n", path)
		}
		if len(report.Eligible) > 0 && len(report.Missing) > 0 {
			os.Exit(1)
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
		sourceKind := decisionSourceKind(value("--source-kind"))
		policyDirs := values("--policy-dir")
		sourcePaths := append([]string{}, values("--source")...)
		brainPaths := values("--brain")
		for _, root := range values("--source-dir") {
			files, err := eligibleDecisionSourceFiles(root, sourceKind)
			if err != nil {
				fmt.Fprintln(os.Stderr, "decisions repair-plan:", err)
				os.Exit(1)
			}
			sourcePaths = append(sourcePaths, files...)
		}
		if root := strings.TrimSpace(os.Getenv("COMPOUND_POLICY_DECISIONS_DIR")); root != "" {
			policyDirs = append(policyDirs, root)
		}
		if len(policyDirs) == 0 && len(sourcePaths) == 0 && len(brainPaths) == 0 {
			fmt.Fprintln(os.Stderr, "usage: compound decisions repair-plan [--policy-dir <dir>] [--source <markdown>] [--source-dir <dir>] [--source-kind note] [--brain <markdown>]")
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
		if len(sourcePaths) > 0 {
			plan, err := decisions.BuildSourceLifecycleRepairPlan(sourcePaths, sourceKind, time.Now())
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
			if report.ID != "" {
				reportPath, recordErr := decisions.RecordLifecycleRepairApplyReport(homePath, report)
				if recordErr == nil {
					fmt.Fprintf(os.Stderr, "decisions repair-apply report: %s\n", reportPath)
				} else {
					fmt.Fprintf(os.Stderr, "decisions repair-apply report: %v\n", recordErr)
				}
			}
			fmt.Fprintln(os.Stderr, "decisions repair-apply:", err)
			os.Exit(1)
		}
		reportPath, err := decisions.RecordLifecycleRepairApplyReport(homePath, report)
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions repair-apply:", err)
			os.Exit(1)
		}
		if !dryRun && decisions.LifecycleRepairApplyTerminal(report) {
			if _, err := decisions.RecordAppliedLifecycleRepairPlan(homePath, plan); err != nil {
				fmt.Fprintln(os.Stderr, "decisions repair-apply:", err)
				os.Exit(1)
			}
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

// writebackSpec is the per-doctor parameterization of the one writeback
// choreography (runDoctorWriteback). Each doctor differs only in these values
// plus how it builds its OwnerWritebackGate and summaries; the spine
// (work create→claim→advance→ActionIntent→policy→gate→receipt→transition) is shared.
type writebackSpec struct {
	Owner            string
	DesiredOutcome   string
	SuccessPredicate string
	NonGoals         []string
	Risk             string
	Privacy          string
	Actor            string
	IDParts          []string // cliID middle parts; cliTS(now) is appended
	Capability       string
	Action           string
	TargetKind       string
	TargetID         string
	TargetPath       string
	Preview          string
	RollbackPlan     string
	RunningNote      string
	CheckedNote      string
	EvidenceRefs     []string
	Verification     string // post-check VerificationID; "" => the checked gate's CandidateRef
	MoveType         string // success-close move_type
	BlockedSummary   func(status, gateID string) string
	AllowedSummary   func(status, gateID string) string
	GateFor          func(ai envelope.ActionIntent, pd envelope.PolicyDecision) writeback.OwnerWritebackGate
}

// runDoctorWriteback runs the one governed owner-writeback choreography shared by
// every doctor: open work + writeback stores, create/claim a work envelope,
// propose the ActionIntent, evaluate policy and the writeback gate, then close
// the work (control_plane_repair/outward_delta, or audit_only when the gate
// blocks) while recording the gate and the receipt/transition refs.
// runDoctorWriteback is the thin CLI adapter: it resolves the home and hands the
// per-domain spec to writeback.RunGatedWriteback, which owns the spine. The CLI
// keeps only what it owns — the id/header scheme (ActionIntentFor) and policy
// config (Evaluate) — while the work→ActionIntent→policy→gate→receipt→transition
// choreography lives behind one internal seam.
func runDoctorWriteback(spec writebackSpec, now time.Time) (writebackWorkResult, error) {
	homePath, err := home.Resolve("")
	if err != nil {
		return writebackWorkResult{}, err
	}
	res, err := writeback.RunGatedWriteback(writeback.GatedWriteback{
		Home: homePath,
		Now:  now,
		Work: work.CreateInput{
			Owner:            spec.Owner,
			DesiredOutcome:   spec.DesiredOutcome,
			SuccessPredicate: spec.SuccessPredicate,
			NonGoals:         spec.NonGoals,
			State:            "ready",
			Risk:             spec.Risk,
			Privacy:          spec.Privacy,
		},
		Actor:        spec.Actor,
		RunningNote:  spec.RunningNote,
		CheckedNote:  spec.CheckedNote,
		Verification: spec.Verification,
		MoveType:     spec.MoveType,
		ActionIntentFor: func(workID string) envelope.ActionIntent {
			idParts := append(append([]string{}, spec.IDParts...), cliTS(now))
			return envelope.ActionIntent{
				Header:        cliHeader(envelope.SVActionIntent, cliID("act_", idParts...), workID, now, spec.EvidenceRefs),
				Actor:         spec.Actor,
				Capability:    spec.Capability,
				Action:        spec.Action,
				Target:        envelope.ActionTarget{Kind: spec.TargetKind, ID: spec.TargetID, Path: spec.TargetPath},
				MutationClass: "local_reversible",
				Reversibility: "easy",
				BlastRadius:   "local",
				Preview:       spec.Preview,
				RollbackPlan:  spec.RollbackPlan,
			}
		},
		Evaluate: func(ai envelope.ActionIntent) envelope.PolicyDecision {
			return policy.Evaluate(ai, policyConfig(), now)
		},
		GateFor:        spec.GateFor,
		BlockedSummary: spec.BlockedSummary,
		AllowedSummary: spec.AllowedSummary,
	})
	return writebackWorkResult{
		WorkID:           res.WorkID,
		ActionIntentID:   res.ActionIntentID,
		PolicyDecisionID: res.PolicyDecisionID,
		GateID:           res.GateID,
		GateStatus:       res.GateStatus,
		ReceiptID:        res.ReceiptID,
		TransitionID:     res.TransitionID,
	}, err
}

func runDecisionWriteback(report decisions.DoctorReport, now time.Time) (writebackWorkResult, error) {
	if !decisions.NeedsWriteback(report) {
		return writebackWorkResult{}, nil
	}
	gapCount := len(report.LifecycleGaps)
	return runDoctorWriteback(writebackSpec{
		Owner:            "decisions:lifecycle",
		DesiredOutcome:   "Route decision lifecycle gaps through governed owner writeback",
		SuccessPredicate: "decision writeback gate recorded with receipt/transition refs",
		NonGoals:         []string{"mutate decision owner files", "promote decision truth"},
		Risk:             "tier1",
		Privacy:          "private",
		Actor:            "compound decisions doctor",
		IDParts:          []string{"decision-writeback", strconv.Itoa(gapCount)},
		Capability:       "decisions.writeback",
		Action:           "record_decision_lifecycle_writeback_gate",
		TargetKind:       "decision_lifecycle",
		TargetID:         "spine-memory-decisions",
		Preview:          fmt.Sprintf("decisions doctor found %d lifecycle gap(s); record owner writeback gate", gapCount),
		RollbackPlan:     "remove COMPOUND_HOME writeback gate/work records before any owner file is changed",
		RunningNote:      "decisions doctor running writeback",
		CheckedNote:      "decision writeback gate checked",
		EvidenceRefs:     decisions.EvidenceRefs(report),
		MoveType:         "control_plane_repair",
		BlockedSummary: func(status, _ string) string {
			return fmt.Sprintf("decision lifecycle writeback blocked for %d gap(s): gate %s", gapCount, status)
		},
		AllowedSummary: func(_, gateID string) string {
			return fmt.Sprintf("%d decision lifecycle gap(s) routed through owner writeback gate %s", gapCount, gateID)
		},
		GateFor: func(ai envelope.ActionIntent, pd envelope.PolicyDecision) writeback.OwnerWritebackGate {
			return decisions.WritebackGate(report, ai.ID, pd.ID, now)
		},
	}, now)
}

func runRuntimeWriteback(report runtimebinding.DoctorReport, now time.Time) (writebackWorkResult, error) {
	if !runtimebinding.NeedsWriteback(report) {
		return writebackWorkResult{}, nil
	}
	return runDoctorWriteback(writebackSpec{
		Owner:            "runtime:" + report.ServiceID,
		DesiredOutcome:   "Route failing runtime doctor report through governed owner writeback",
		SuccessPredicate: "runtime writeback gate recorded with receipt/transition refs",
		NonGoals:         []string{"mutate runtime owner files", "promote runtime truth"},
		Risk:             "tier1",
		Privacy:          "private",
		Actor:            "compound runtime doctor",
		IDParts:          []string{"runtime-writeback", report.ServiceID, report.Status},
		Capability:       "runtime.writeback",
		Action:           "record_runtime_writeback_gate",
		TargetKind:       "runtime_binding",
		TargetID:         report.ServiceID,
		Preview:          fmt.Sprintf("runtime doctor for %s is %s; record owner writeback gate", report.ServiceID, report.Status),
		RollbackPlan:     "remove COMPOUND_HOME writeback gate/work records before any owner file is changed",
		RunningNote:      "runtime doctor running writeback",
		CheckedNote:      "runtime writeback gate checked",
		EvidenceRefs:     runtimebinding.EvidenceRefs(report),
		Verification:     "runtime:" + report.ServiceID + ":" + report.Status,
		MoveType:         "control_plane_repair",
		BlockedSummary: func(status, _ string) string {
			return fmt.Sprintf("runtime writeback blocked for %s: gate %s", report.ServiceID, status)
		},
		AllowedSummary: func(_, gateID string) string {
			return fmt.Sprintf("runtime %s %s routed through owner writeback gate %s", report.ServiceID, report.Status, gateID)
		},
		GateFor: func(ai envelope.ActionIntent, pd envelope.PolicyDecision) writeback.OwnerWritebackGate {
			return runtimebinding.WritebackGate(report, ai.ID, pd.ID, now)
		},
	}, now)
}

func runDistillerWriteback(report distiller.DoctorReport, now time.Time) (writebackWorkResult, error) {
	if !distiller.NeedsWriteback(report) {
		return writebackWorkResult{}, nil
	}
	debtCount := report.SelfImprovementDebt
	return runDoctorWriteback(writebackSpec{
		Owner:            "distiller:policy",
		DesiredOutcome:   "Route distiller method/policy debt through governed owner writeback",
		SuccessPredicate: "distiller writeback gate recorded with receipt/transition refs",
		NonGoals:         []string{"mutate distillation policy", "call model extractor", "promote memory truth"},
		Risk:             "tier1",
		Privacy:          "private",
		Actor:            "compound distiller doctor",
		IDParts:          []string{"distiller-writeback", strconv.Itoa(report.MethodUpgradeDebt), strconv.Itoa(report.PolicyPatchDebt)},
		Capability:       "distiller.writeback",
		Action:           "record_distillation_policy_writeback_gate",
		TargetKind:       "distillation_policy",
		TargetID:         distiller.DefaultPolicy().Stamp(),
		Preview:          fmt.Sprintf("distiller doctor found %d method/policy debt item(s); record owner writeback gate", debtCount),
		RollbackPlan:     "remove COMPOUND_HOME writeback gate/work records before any distillation policy is changed",
		RunningNote:      "distiller doctor running writeback",
		CheckedNote:      "distiller writeback gate checked",
		EvidenceRefs:     distiller.EvidenceRefs(report),
		MoveType:         "control_plane_repair",
		BlockedSummary: func(status, _ string) string {
			return fmt.Sprintf("distiller method/policy writeback blocked for %d debt item(s): gate %s", debtCount, status)
		},
		AllowedSummary: func(_, gateID string) string {
			return fmt.Sprintf("%d distiller method/policy debt item(s) routed through owner writeback gate %s", debtCount, gateID)
		},
		GateFor: func(ai envelope.ActionIntent, pd envelope.PolicyDecision) writeback.OwnerWritebackGate {
			return distiller.WritebackGate(report, ai.ID, pd.ID, now)
		},
	}, now)
}

func cmdRuntime(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, runtimeUsage())
		os.Exit(2)
	}
	switch args[0] {
	case "doctor":
		cmdRuntimeDoctor(args[1:])
	case "repair-plan":
		cmdRuntimeRepairPlan(args[1:])
	default:
		fmt.Fprintln(os.Stderr, runtimeUsage())
		os.Exit(2)
	}
}

func runtimeUsage() string {
	return "usage: compound runtime doctor [--project-contract] <yaml> [--live] [--writeback] | runtime repair-plan [--project-contract] <yaml> [--live]"
}

func cmdRuntimeDoctor(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: compound runtime doctor [--project-contract] <yaml> [--live] [--writeback]")
		os.Exit(2)
	}
	report := loadRuntimeReportOrExit(args, "runtime doctor", hasFlag(args, "--live"))
	printRuntimeDoctorReport(report)
	if report.Status != "ok" {
		if hasFlag(args, "--writeback") {
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

func cmdRuntimeRepairPlan(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: compound runtime repair-plan [--project-contract] <yaml> [--live]")
		os.Exit(2)
	}
	now := time.Now()
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime repair-plan:", err)
		os.Exit(1)
	}
	report := loadRuntimeReportOrExit(args, "runtime repair-plan", hasFlag(args, "--live"))
	if !runtimebinding.NeedsWriteback(report) {
		fmt.Fprintf(os.Stderr, "runtime repair-plan: runtime %s ok; no repair plan needed\n", report.ServiceID)
		os.Exit(1)
	}
	writebackResult, err := runRuntimeWriteback(report, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime repair-plan:", err)
		os.Exit(1)
	}
	plan, err := runtimebinding.BuildRepairPlan(report, runtimebinding.RepairRefs{
		WritebackGateID: writebackResult.GateID,
		WorkID:          writebackResult.WorkID,
		ReceiptID:       writebackResult.ReceiptID,
		TransitionID:    writebackResult.TransitionID,
	}, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime repair-plan:", err)
		os.Exit(1)
	}
	paths, err := runtimebinding.RecordRepairPlan(homePath, plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime repair-plan:", err)
		os.Exit(1)
	}
	fmt.Printf("# runtime repair-plan %s\n", plan.ID)
	fmt.Printf("- service: %s\n", plan.ServiceID)
	fmt.Printf("- status: %s\n", plan.Status)
	fmt.Printf("- failed_checks: %d\n", plan.FailedCount)
	fmt.Printf("- writeback_gate: %s\n", plan.WritebackGateID)
	fmt.Printf("- work: %s\n", plan.WorkID)
	fmt.Printf("- receipt: %s\n", plan.ReceiptID)
	fmt.Printf("- transition: %s\n", plan.TransitionID)
	fmt.Printf("- plan: %s\n", paths.PlanPath)
	fmt.Printf("- review: %s\n", paths.ReviewPath)
}

func loadRuntimeReportOrExit(args []string, label string, live bool) runtimebinding.DoctorReport {
	path := ""
	load := runtimebinding.Load
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project-contract":
			load = runtimebinding.LoadProjectContract
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "usage: compound %s --project-contract <project.yaml>\n", label)
				os.Exit(2)
			}
			path = args[i+1]
			i++
		case "--live", "--writeback":
			continue
		default:
			if strings.HasPrefix(args[i], "-") {
				continue
			}
			if path == "" {
				path = args[i]
			}
		}
	}
	if path == "" {
		fmt.Fprintf(os.Stderr, "usage: compound %s [--project-contract] <yaml>\n", label)
		os.Exit(2)
	}
	bundle, err := load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, label+":", err)
		os.Exit(1)
	}
	if live {
		return runtimebinding.DoctorWithLiveProbes(context.Background(), bundle, time.Now())
	}
	return runtimebinding.Doctor(bundle)
}

func printRuntimeDoctorReport(report runtimebinding.DoctorReport) {
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
	if !product.NeedsWriteback(report) {
		return writebackWorkResult{}, nil
	}
	return runDoctorWriteback(writebackSpec{
		Owner:            "product_strategy:" + report.ProductID,
		DesiredOutcome:   "Route product feedback review through governed owner writeback",
		SuccessPredicate: "product feedback writeback gate recorded with receipt/transition refs",
		NonGoals:         []string{"mutate product pages", "declare product causality", "execute next product action"},
		Risk:             "tier1",
		Privacy:          "private",
		Actor:            "compound product doctor",
		IDParts:          []string{"product-writeback", report.ProductID, strconv.Itoa(report.PendingReviewCount), strconv.Itoa(report.SourceIssueCount)},
		Capability:       "product.writeback",
		Action:           "record_product_feedback_writeback_gate",
		TargetKind:       "product_strategy",
		TargetID:         report.ProductID,
		Preview:          fmt.Sprintf("product doctor found %d pending hypothesis review(s) and %d source issue(s); record owner writeback gate", report.PendingReviewCount, report.SourceIssueCount),
		RollbackPlan:     "remove COMPOUND_HOME writeback gate/work records before any product owner file or external surface is changed",
		RunningNote:      "product doctor running writeback",
		CheckedNote:      "product hypothesis writeback gate checked",
		EvidenceRefs:     product.EvidenceRefs(report),
		MoveType:         "outward_delta",
		BlockedSummary: func(_, gateID string) string {
			return fmt.Sprintf("product feedback writeback blocked for %s: gate %s", report.ProductID, gateID)
		},
		AllowedSummary: func(_, gateID string) string {
			return fmt.Sprintf("product %s feedback review routed through owner writeback gate %s", report.ProductID, gateID)
		},
		GateFor: func(ai envelope.ActionIntent, pd envelope.PolicyDecision) writeback.OwnerWritebackGate {
			return product.WritebackGate(report, ai.ID, pd.ID, now)
		},
	}, now)
}

func runProjectionWriteback(report projection.Freshness, now time.Time) (writebackWorkResult, error) {
	if !projection.NeedsWriteback(report) {
		return writebackWorkResult{}, nil
	}
	return runDoctorWriteback(writebackSpec{
		Owner:            "projection:" + report.ProjectionID,
		DesiredOutcome:   "Route stale/drifted projection through governed owner writeback",
		SuccessPredicate: "writeback gate recorded with receipt/transition refs",
		NonGoals:         []string{"regenerate the projection output", "promote projection truth"},
		Risk:             "tier1",
		Privacy:          "private",
		Actor:            "compound projection doctor",
		IDParts:          []string{"projection-writeback", report.ProjectionID, report.Status},
		Capability:       "projection.writeback",
		Action:           "record_projection_writeback_gate",
		TargetKind:       "generated_view",
		TargetID:         report.ProjectionID,
		TargetPath:       report.ViewRef,
		Preview:          fmt.Sprintf("projection %s is %s; record owner writeback gate for %s", report.ProjectionID, report.Status, report.ViewRef),
		RollbackPlan:     "remove the COMPOUND_HOME writeback gate/work records before any owner file is changed",
		RunningNote:      "projection doctor running writeback",
		CheckedNote:      "projection writeback gate checked",
		EvidenceRefs:     projection.EvidenceRefs(report),
		Verification:     "projection:" + report.ProjectionID + ":" + report.Status,
		MoveType:         "control_plane_repair",
		BlockedSummary: func(status, _ string) string {
			return fmt.Sprintf("projection writeback blocked for %s: gate %s", report.ProjectionID, status)
		},
		AllowedSummary: func(_, gateID string) string {
			return fmt.Sprintf("projection %s %s routed through owner writeback gate %s", report.ProjectionID, report.Status, gateID)
		},
		GateFor: func(ai envelope.ActionIntent, pd envelope.PolicyDecision) writeback.OwnerWritebackGate {
			return projection.WritebackGate(report, ai.ID, pd.ID, now)
		},
	}, now)
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

func runProjectionContextRecord(report projection.Freshness, repoKindArg string, now time.Time) (projection.ContextRecord, string, error) {
	homePath, err := home.Resolve("")
	if err != nil {
		return projection.ContextRecord{}, "", err
	}
	return projection.RecordContext(homePath, report, splitCSV(repoKindArg), now)
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
	return "usage: compound projection doctor <freshness.yaml> [--writeback] [--source-observe] [--record-context --repo-kind K] | projection dashboard-read-model <read-model.json> [--max-age-hours N] [--writeback] [--source-observe] [--record-context --repo-kind K] | projection repair-plan <freshness.yaml>"
}

func cmdProjectionDashboardReadModel(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: compound projection dashboard-read-model <read-model.json> [--max-age-hours N] [--writeback] [--source-observe] [--record-context --repo-kind K]")
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
	finishProjectionDoctor(report, hasFlag(args[1:], "--writeback"), hasFlag(args[1:], "--source-observe"), hasFlag(args[1:], "--record-context"), valueFlag(args[1:], "--repo-kind"), now)
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

func finishProjectionDoctor(report projection.Freshness, writebackEnabled, sourceObserveEnabled, recordContextEnabled bool, contextRepoKind string, now time.Time) {
	if recordContextEnabled {
		record, path, err := runProjectionContextRecord(report, contextRepoKind, now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "projection context record:", err)
			os.Exit(1)
		}
		fmt.Printf("- context_record: %s\n", record.ID)
		fmt.Printf("- context_repo_kinds: %s\n", strings.Join(record.RepoKinds, ","))
		fmt.Printf("- context_path: %s\n", path)
	}
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
		if writebackEnabled || sourceObserveEnabled || recordContextEnabled {
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
		fmt.Fprintln(os.Stderr, "usage: compound projection doctor <freshness.yaml> [--writeback] [--source-observe] [--record-context --repo-kind K]")
		os.Exit(2)
	}
	now := time.Now()
	writebackEnabled := hasFlag(args[2:], "--writeback")
	sourceObserveEnabled := hasFlag(args[2:], "--source-observe")
	recordContextEnabled := hasFlag(args[2:], "--record-context")
	contextRepoKind := valueFlag(args[2:], "--repo-kind")
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
	if recordContextEnabled {
		record, path, err := runProjectionContextRecord(report, contextRepoKind, now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "projection context record:", err)
			os.Exit(1)
		}
		fmt.Printf("- context_record: %s\n", record.ID)
		fmt.Printf("- context_repo_kinds: %s\n", strings.Join(record.RepoKinds, ","))
		fmt.Printf("- context_path: %s\n", path)
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
		if writebackEnabled || sourceObserveEnabled || recordContextEnabled {
			return
		}
		os.Exit(1)
	}
}
