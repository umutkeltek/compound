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
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"compound/internal/authority"
	"compound/internal/breadth"
	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/contract"
	"compound/internal/corpus"
	"compound/internal/decisions"
	"compound/internal/distiller"
	"compound/internal/envelope"
	"compound/internal/eventsource"
	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/knowledge"
	"compound/internal/mem"
	"compound/internal/policy"
	"compound/internal/product"
	"compound/internal/projection"
	"compound/internal/reviewartifact"
	"compound/internal/routeevidence"
	"compound/internal/runtimebinding"
	"compound/internal/schema"
	"compound/internal/sourceobs"
	"compound/internal/work"
	"compound/internal/writeback"
	"gopkg.in/yaml.v3"
)

const version = "0.1.0"

// buildSHA is embedded at build time via
//
//	-ldflags "-X main.buildSHA=$(git rev-parse --short HEAD)"
//
// (see install.sh, the entry point `make install` calls). Empty when built without
// ldflags (e.g. a plain `go build`/`go run` during dev) — `compound version` then
// reports "dev" rather than silently claiming a build provenance it doesn't have.
// This is the freshness signal consumed by internal/arch's build-freshness check:
// a hook/consumer running a stale installed binary is otherwise undetectable.
var buildSHA string

func buildSHAOrDev() string {
	if strings.TrimSpace(buildSHA) == "" {
		return "dev"
	}
	return strings.TrimSpace(buildSHA)
}

// versionString is what `compound version`/`--version`/`-v` prints. Split out from the
// switch so tests can assert build-SHA embedding without shelling out to a built binary.
func versionString() string {
	return fmt.Sprintf("compound %s (build %s)", version, buildSHAOrDev())
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println(versionString())
	case "distill", "scope", "promote", "build", "reconcile", "timeline", "recall", "pack", "ledger":
		cmdRetiredGen1(os.Args[1])
	case "authority":
		cmdAuthority(os.Args[2:])
	case "decisions":
		cmdDecisions(os.Args[2:])
	case "director":
		cmdDirector(os.Args[2:])
	case "runtime":
		cmdRuntime(os.Args[2:])
	case "projection":
		cmdProjection(os.Args[2:])
	case "system":
		cmdSystem(os.Args[2:])
	case "product":
		cmdProduct(os.Args[2:])
	case "source":
		cmdSource(os.Args[2:])
	case "console":
		cmdConsole(os.Args[2:])
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
	case "packets":
		cmdPackets(os.Args[2:])
	case "workbench":
		cmdWorkbench(os.Args[2:])
	case "attention":
		cmdAttention(os.Args[2:])
	case "executors":
		cmdExecutors(os.Args[2:])
	case "proof-check":
		cmdProofCheck(os.Args[2:])
	case "release":
		cmdRelease(os.Args[2:])
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
	case "profiles":
		cmdProfiles(os.Args[2:])
	case "memory":
		cmdMemory(os.Args[2:])
	case "corpus":
		cmdCorpus(os.Args[2:])
	case "synthesis":
		cmdSynthesis(os.Args[2:])
	case "knowledge":
		cmdKnowledge(os.Args[2:])
	case "sessions":
		cmdSessions(os.Args[2:])
	case "distiller":
		cmdDistiller(os.Args[2:])
	case "capability":
		cmdCapability(os.Args[2:])
	case "bench":
		cmdBench(os.Args[2:])
	case "panel":
		cmdPanel(os.Args[2:])
	case "local-models":
		cmdLocalModels(os.Args[2:])
	case "lanebeliefs":
		cmdLaneBeliefs(os.Args[2:])
	case "negatives":
		cmdNegatives(os.Args[2:])
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

func hasUsageFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func memoryUsage(sub string) string {
	switch sub {
	case "candidate":
		return "usage: compound memory candidate <trace.json>"
	case "candidates":
		return "usage: compound memory candidates --json [--state proposed|parked|promoted|rejected|no_promote|needs_review] [--limit N] [--kind K] [--include-evidence]"
	case "adjudicate":
		return "usage: compound memory adjudicate <id> --verdict reject|park|promote|no_promote [--work W]"
	case "graph":
		return "usage: compound memory graph <seed> [--depth N] [--budget N] [--min-conf F] [--direction up|down] [--kind KIND[,KIND]...]"
	case "import-topics":
		return "usage: compound memory import-topics <topics.json> [--json]"
	case "temperature":
		return "usage: compound memory temperature [--json] [--rebuild]"
	case "canon":
		return "usage: compound memory canon [--json]"
	case "volatile-restore":
		return "usage: compound memory volatile-restore <cooled_claim_id>"
	case "aliases":
		return "usage: compound memory aliases <show|rebuild|expand <query>> [--json]"
	case "fold":
		return "usage: compound memory fold [--entity E] [--json]"
	case "position":
		return "usage: compound memory position <entity> [--as-of N] [--topic topic-slug] [--json]"
	case "engine-config":
		return "usage: compound memory engine-config [--profile generic|memory-cli|gbrain|breadth] [--clear] [--json]"
	case "bakeoff":
		return "usage: compound memory bakeoff <init [--queries file] [--force]|run --queries file [--engine bin] [--k N] [--json]>"
	case "search":
		return "usage: compound memory search --lane L <query> [--k N] [--json]"
	default:
		return "usage: compound memory <candidate <trace.json>|candidates --json|import-page <markdown>|import-topics <topics.json>|adjudicate <id> --verdict V|recall [entity] [--as-of N] [--topic topic-slug] [--include-needs-review] [--include-evidence]|timeline <entity>|fold [--entity E] [--json]|position <entity> [--as-of N] [--topic topic-slug] [--json]|graph <seed> [--direction up|down] [--kind K]|write-plan --candidate C --target brain.md|query <text>|search --lane L <query>|canon [--json]|aliases <show|rebuild|expand Q>|engine-doctor <text>|engine-compare <text> [--allow-expansion]|engine-config|bakeoff <init|run>|doctor>"
	}
}

func routeUsage(sub string) string {
	switch sub {
	case "cost-doctor":
		return "usage: compound route cost-doctor <provider-calls.ndjson> [--limit N|--tail N] [--record]"
	case "promotion-record":
		return "usage: compound route promotion-record --review <route_promotion.json> --rationale <text> [--json]"
	default:
		return "usage: compound route <evidence-authority [--json] [--recent N]|import <execution-route-evidence.json>|import-model-capabilities <model-capabilities.yaml>|import-promotion-criteria <promotion-criteria.yaml>|promotion-review [--lane ID] [--json]|promotion-record --review <route_promotion.json> --rationale <text> [--json]|import-provider-ledger <provider-calls.ndjson> [--limit N|--tail N]|cost-doctor <provider-calls.ndjson> [--limit N|--tail N] [--record]|outcome-review [(--route-id ID|--route-evidence file)] (--receipt file|--receipt-id ID|--work-id ID) [--target file] [--record]|plan (--candidate C [--target file]|--capability-class C [--privacy-sensitive])>"
	}
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

func usage() {
	fmt.Fprint(os.Stderr, `compound — local meaning + execution substrate

usage:
  compound version
compound home <init|doctor|where|backup|export|verify|restore|archive-history|restore-history|compact> [--home P] [--local] COMPOUND_HOME runtime body + archive/restore; archive-history accepts --kind work|lease|review|comparison|create-plan|memory-candidate and --path <rel>
  compound envelope <validate <file>...|kinds>                  typed runtime envelopes
  compound schema <list|export|validate>                        JSON Schema export + contract-aware validation
  compound contract <compile|import-hooks|hooks-authority> ...  EffectiveContract and installed hook authority
  compound orient --harness H [--cwd D] [--task T] [--topic S]  ContextPack orient from records excluded; --topic narrows ambient recall
  compound console --operator [--private-fallbacks]             start local operator console with mutation env wired
compound work <create-plan|admit-plan|review-card-plan|create|import-packet|show|list|leases|active-packet-proof|proof-chain-doctor|continue|dispatch-plan|dispatch-claim|dispatch-advance|dispatch-close-plan|dispatch-close|dispatch-step|claim-plan|claim|advance-plan|advance|observe-run|action-plan|act|close-plan|close> one work_id: intent -> context -> action -> receipt -> transition
compound workbench [--json] [--recent N] [--actor A] [--active-packet-surface S] [--source-packet packet.md] [--packet-dir D] [--capability-task T] [--capability-harness H] [--capability-limit N] [--route-class C] [--route-privacy-sensitive] read-only open work / receipt / transition board; optional packet offers, bounded capability offers, and route plan; no dispatch
compound attention [--json] [--home P] ranked operator-attention projection from reviews, workbench, and executors; no mutation
compound executors [--json] read-only executor projection from Workbench, leases, and profile guide; no health overclaim
compound proof-check [--json] [--dry-run] [--repo P] deterministic local gate bundle: tests/vet/build/gofmt/diff/doctors/redact
  compound director propose [--dry-run|--apply] --json          suggested-work proposals backed by evidence; no add/continue side effects
  compound release export-public [--repo P] [--target archive.tar.gz] [--prefix NAME] [--json] public source archive from git archive HEAD with private/runtime path gate
  compound reviews [--json] [--area A] [--include-archived] read-only owner-review artifact queue; no apply
  compound reviews record-finding (--work-id W|--auto-work) --target state/runs/.../finding.json --summary S [--root DIR] [--capability review.finding] [--source-artifact state/runs/.../artifact.json] [--source-trace trace_id] [--evidence-ref REF...]
  compound policy <evaluate|import-placement-rules|placement-authority|import-standing-grants|standing-grants-authority|import-operator-state|operator-authority|import-operator-prompts|operator-prompts-authority|import-operator-approval-frontier|operator-approval-frontier-authority|import-release-goal-holds|release-goal-holds-authority> ActionIntent -> PolicyDecision; import policy authorities
  compound hook <name> [--enforce] <fixture.json>               run lifecycle hook over fixture; --enforce exits nonzero on pre-tool holds
  compound harness <list|NAME|observe [registry.yaml|--os-entrypoints file]> HarnessRegistry capabilities + limits; observe records capability evidence/no-promote
  compound transition show <id>                                 inspect a Transition or envelope
  compound route import <execution-route-evidence.json>          import route metadata -> Transition projection
  compound route import-model-capabilities <model-capabilities.yaml> OS model router YAML -> Compound route authority
  compound route import-promotion-criteria <promotion-criteria.yaml> Promotion criteria YAML -> Compound route review authority
  compound route promotion-review [--lane ID] [--json] Review model-lane claim promotion evidence, no authority mutation
  compound route promotion-record --review route_promotion.json --rationale TEXT [--json] Record reviewed hold decision, no authority mutation
  compound route evidence-authority [--json] [--recent N] read-only route evidence authority
  compound route import-provider-ledger <provider-calls.ndjson> [--limit N|--tail N] provider-call NDJSON -> route/cost projections
  compound route cost-doctor <provider-calls.ndjson> [--limit N|--tail N] [--record] provider cost coverage -> route observation/no-promote
  compound route outcome-review (--route-id ID|--route-evidence file) --receipt file [--target file] [--record] join route evidence to work outcome
  compound route plan --candidate C [--target file]              route_observation candidate -> reviewed routing-policy draft
  compound route plan --capability-class C [--privacy-sensitive] planned lane chain from Compound route authority
	compound memory <candidate|candidates|import-page|import-topics|adjudicate|recall|timeline|fold|position|query|search --lane L|engine-doctor|engine-compare [--allow-expansion]|bakeoff <init|run>|doctor>
	              spine memory: candidates, claims, positions, patterns, evidence
  compound corpus <index --lane L [--rebuild]|doctor --lane L [--json]> rebuildable corpus vector indexes
  compound synthesis sweep [--stage cluster,contradict,corroborate,decay,reconcile] [--json]
                memory synthesis reports; contradictions append needs_review markers only; decay files expired parks
  compound distiller eval <case.json|cases-dir> [--record]    Offline request/response/adjudication eval corpus, no model call
  compound distiller eval-plan --candidate C [--corpus dir]   eval_case candidate -> reviewed eval fixture draft, no corpus mutation
  compound distiller improvement-plan --candidate C [--target file] method_upgrade/policy_patch -> reviewed distiller improvement draft
  compound distiller improvement-review <review.yaml>           validate reviewed distiller improvement decision, no source mutation
  compound distiller rule-plan --candidate C [--target file]  failure_pattern candidate -> reviewed rule draft, no doctrine mutation
  compound distiller taste-plan --candidate C [--target file] taste candidate -> reviewed taste standard draft, no doctrine mutation
  compound distiller request --trace-ref T [--work-id W] [--raw f] [--context-pack f] [--work f] [--receipt f] [--envelope f]... [--event-trace trace_id]... [--record]
  TraceDistiller LLM request packet, no model call
  compound distiller extract --request <request.json|distiller_request:id> --eval-corpus dir --openai-compatible-url URL --model M [--api-key-env ENV]
  gated rented model extraction; provider output stays candidate-only
  compound distiller import-conversation-coverage <summary.json>
                                                                 conversation corpus coverage -> SourceObservation/no-promote; no row import, Dream execution, or memory promotion
  compound distiller import-conversation-mining <report.json> [--mined-tags rows.jsonl] [--lossiness lossiness.json]
                                                                 mined conversation source quality -> SourceObservation/no-promote; no Dream execution or memory promotion
  compound distiller import-session-lenses <multi-lens.json>
                                                                 existing session distill lenses -> SourceObservation/no-promote; no row import or memory promotion
  compound distiller run [--llm-response response.json [--request request.json]|trace.json] | doctor [--writeback] | prior-state Trace -> candidate fan-out, adjudication, prior state
compound capability <add|surface|observe|consume|consume-receipts|list|graph|authority|external-intake|outbound-distribution-plan|outbound-distribution-verdict-plan|outbound-distribution-verdict-review|outbound-distribution-dry-run-receipt|outbound-distribution-dry-run-review|external-intake-review-plan|external-intake-evaluation-plan|external-intake-import-reviewed|external-intake-review|tool-boundary-doctor|skill-source-doctor|import-topology|import-skills|import-evaluations|import-surfacing-misses|evaluation-plan|evaluation-review|skill-plan|skill-candidate-review|skill-candidate-decision> capability graph: auto-surface, never auto-execute; tool-boundary-doctor checks MCP/tool/plugin inventory stays observed/non-executing; skill-source-doctor validates raw SKILL.md sources read-only; imports accept normalized files, OS generated JSON, mcporter JSON, capability-evaluation records, or skill surfacing feedback reports
 compound panel <import-lenses|lenses|plan|run|verdict-record|show> perspective panel planning and dry-run verdicts
  compound local-models advise [--lm-studio-url U] [--required MODEL]... [--min-context MODEL=TOKENS]... [--host-context-limit N] [--idle-ttl DURATION] [--json] local model context/load/unload advice with lifecycle intents; reads configured local provider, no mutation
  compound authority import [f] | path <f> | path-registry <f> | git <repo> [--json] | dirty-plan <repo> [--work-id id] [--json] | dirty-review <review.yaml> [--json] | import-job-roster <f> | import-job-token-governance <f> | job-roster-authority [--json] | job-health-plan | job-health-review <review.yaml> | resolve <token> | <entity> import/query authority through spine memory
  compound decisions import [--source <md>] [--source-dir <dir>] [--source-kind note] [--brain <md>] [--policy <DEC.md>] [--policy-dir <dir>] | decisions authority [--source <md>] [--source-dir <dir>] [--source-kind note] [--policy <DEC.md>] [--policy-dir <dir>] [--json] | decisions candidate-plan --candidate C [--target file] | decisions [--kind=K] | decisions doctor | decisions source-doctor --template <md> [--format-doc <md>] | decisions repair-plan [--policy-dir <dir>] [--source <md>] [--source-dir <dir>] [--source-kind note] [--brain <md>] | decisions repair-apply --plan <json> [--write-reviewed]
                                                                  import/query decisions through spine memory; verify source format; emit/apply lifecycle repair artifacts
compound runtime doctor [--project-contract] <yaml> [--live] [--writeback] | runtime repair-plan [--project-contract] <yaml> [--live] | runtime repair-review <review.yaml> RuntimeBinding health/live probes + gated writeback/review artifacts
compound projection doctor <freshness.yaml> [--writeback] [--source-observe] | projection dashboard-read-model <read-model.json> [--max-age-hours N] [--writeback] [--source-observe] | projection repair-plan <freshness.yaml> | projection repair-review <review.yaml>
                                                               ProjectionFreshness + gated writeback/source observation/regeneration review
compound product import <product.yaml>|--project-contract <project.yaml> [--release <release.yaml>] | signal <signal.yaml>|--asc-datapoints <json> --product <id> --app-id <asc_id> [--metric <metric>]|--asc-sales-series <jsonl> --product <id> --app-id <asc_id> --before <range> --after <range> --change-event <text> [--metric <metric>]|--gsc-report <json> --product <id> --site <site> [--metric <metric>]|--ga4-report <json> --product <id> --property <id> [--metric <metric>]|--adsense-report <json> --product <id> --account <id> [--metric <metric>] | loops [--product <id>] [--json] [--recent N] [--stale-days D] | canonical-metrics <json> --product <id> | hypothesis-plan --candidate C [--target file] | observation-verdict-plan --verdict <panel-verdict.json> [--target file] | observation-verdict-review <review.yaml> | source-issue-plan --product P --issue I [--target file] | review <review.yaml> | source-issue-review <review.yaml> | timeline <product> | doctor <product> [--writeback]
                                                               Product registry + signal/hypothesis loop
compound source observe <source-observation.yaml> | observe-batch <batch.json> (--dry-run|--apply-product-signals|--apply-source-observations|--apply-knowledge|--apply-route-evidence|--apply-events|--review-candidates) [--json] | intake [--limit N] [--json] | intake-work-plan --card <card_id> [--actor A] [--json] SourceObservation or generic ObservationBatch intake
compound event import-os <events.ndjson> | event authority [--json] OS-style append-only events -> TraceEnvelope evidence; optional trace-backed review finding producer
compound writeback <gate|no-promote>                           OwnerWritebackGate + NoPromoteDecision

retired Gen-1 tombstones:
  compound distill|scope|promote|build|reconcile|timeline|recall|pack|ledger
  These exit 2 and point to canonical compound memory/distiller surfaces.
`)
}

// buildCapabilitySurfaceReport adapts CapabilityGraph offers into the stable
// machine surface consumed by CLI/read-model callers. It only reads graph state.
func buildCapabilitySurfaceReport(store *capgraph.Store, homePath, task, harness string) (capgraph.SurfaceReport, error) {
	task = strings.TrimSpace(task)
	if task == "" {
		return capgraph.SurfaceReport{}, fmt.Errorf("capability task must be non-empty")
	}
	harness = strings.TrimSpace(harness)
	var offers []capgraph.Surfaced
	var err error
	if harness != "" {
		offers, err = store.SurfaceForHarness(task, harness)
	} else {
		offers, err = store.Surface(task)
	}
	if err != nil {
		return capgraph.SurfaceReport{}, err
	}
	stats, err := store.Stats()
	if err != nil {
		return capgraph.SurfaceReport{}, err
	}
	records, err := capgraph.LoadSourceAuthority(homePath)
	if err != nil {
		return capgraph.SurfaceReport{}, err
	}
	report := capgraph.NewSurfaceReport(task, harness, offers, stats, len(records))
	matchedEvidence, err := capgraph.ExternalRepoMatchedEvidenceForTask(homePath, task, 5)
	if err != nil {
		return capgraph.SurfaceReport{}, err
	}
	report.MatchedEvidence = matchedEvidence
	return report, nil
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || arg == "help" {
			return true
		}
	}
	return false
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
	if len(args) >= 1 && args[0] == "import-session-lenses" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: compound distiller import-session-lenses <multi-lens.json>")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
		now := time.Now()
		lenses, err := distiller.LoadSessionLenses(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller import-session-lenses:", err)
			os.Exit(1)
		}
		sourceReport, err := sourceobs.Apply(homePath, sourceobs.FromSessionLenses(sourceobs.SessionLensObservation{
			ID:             lenses.ID,
			SourcePath:     lenses.SourcePath,
			SourceRevision: lenses.Revision,
			Decisions:      lenses.Decisions,
			Outcomes:       lenses.Outcomes,
			Patterns:       lenses.Patterns,
			Skills:         lenses.Skills,
			Taste:          lenses.Taste,
			Contradictions: lenses.Contradictions,
			TotalItems:     lenses.TotalItems,
			EvidenceRefs:   lenses.EvidenceRefs,
		}, now), now)
		if err != nil {
			fmt.Fprintln(os.Stderr, "source observe:", err)
			os.Exit(1)
		}
		fmt.Printf("# distiller import-session-lenses %s\n", lenses.ID)
		fmt.Printf("- decisions: %d\n", lenses.Decisions)
		fmt.Printf("- outcomes: %d\n", lenses.Outcomes)
		fmt.Printf("- patterns: %d\n", lenses.Patterns)
		fmt.Printf("- skills: %d\n", lenses.Skills)
		fmt.Printf("- taste: %d\n", lenses.Taste)
		fmt.Printf("- contradictions: %d\n", lenses.Contradictions)
		fmt.Printf("- total_items: %d\n", lenses.TotalItems)
		fmt.Printf("- source_observation: %s\n", sourceReport.ObservationID)
		for _, id := range sourceReport.CandidateIDs {
			fmt.Printf("- source_candidate: %s\n", id)
			fmt.Printf("- review_command: compound distiller improvement-plan --candidate %s\n", id)
		}
		for _, id := range sourceReport.NoPromoteIDs {
			fmt.Printf("- source_no_promote: %s\n", id)
		}
		fmt.Println("- row_import: no")
		fmt.Println("- memory_promotion: no")
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
		fmt.Fprintln(os.Stderr, "usage: compound distiller eval <case.json|cases-dir> [--record] | eval-plan --candidate C [--corpus dir] | rule-plan --candidate C [--target file] | taste-plan --candidate C [--target file] | request --trace-ref T [--work-id W] [--raw f] [--context-pack f] [--work f] [--receipt f] [--envelope f]... [--event-trace trace_id]... [--record] | extract --request <request.json|distiller_request:id> --eval-corpus dir --openai-compatible-url URL --model M [--api-key-env ENV] | import-conversation-mining <report.json> [--mined-tags rows.jsonl] [--lossiness lossiness.json] | import-session-lenses <multi-lens.json> | run [--llm-response response.json [--request <request.json|distiller_request:id>]|trace.json] | doctor [--writeback] | prior-state")
		os.Exit(2)
	}

	extractor := distiller.Extractor(distiller.ReferenceExtractor{})
	tracePath := args[1]
	homePath := ""
	if args[1] == "--llm-response" {
		if len(args) != 3 && len(args) != 5 {
			fmt.Fprintln(os.Stderr, "usage: compound distiller run --llm-response <response.json> [--request <request.json|distiller_request:id>]")
			os.Exit(2)
		}
		tracePath = args[2]
		extractor = distiller.LLMExtractor{}
		if len(args) == 5 {
			if args[3] != "--request" || strings.TrimSpace(args[4]) == "" {
				fmt.Fprintln(os.Stderr, "usage: compound distiller run --llm-response <response.json> [--request <request.json|distiller_request:id>]")
				os.Exit(2)
			}
			var err error
			homePath, err = home.Resolve("")
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
				os.Exit(1)
			}
			req, err := distiller.LoadLLMExtractionRequest(homePath, args[4])
			if err != nil {
				fmt.Fprintln(os.Stderr, "distiller run request:", err)
				os.Exit(1)
			}
			extractor = distiller.NewGroundedLLMExtractor(req)
		}
	}
	if homePath == "" {
		var err error
		homePath, err = home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
			os.Exit(1)
		}
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
		fmt.Fprintln(os.Stderr, "usage: compound distiller request --trace-ref T [--work-id W] [--raw f] [--context-pack f] [--work f] [--receipt f] [--envelope f]... [--event-trace trace_id]... [--record]")
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
	if hasFlag(args, "--record") {
		record, err := distiller.RecordLLMExtractionRequest(homePath, req)
		if err != nil {
			fmt.Fprintln(os.Stderr, "distiller request record:", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "- request_ref: %s\n", record.Ref)
		fmt.Fprintf(os.Stderr, "- request: %s\n", record.Path)
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
		fmt.Fprintln(os.Stderr, "usage: compound distiller extract --request <request.json|distiller_request:id> --eval-corpus <cases-dir> --openai-compatible-url <url> --model <model> [--api-key-env ENV]")
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
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	req, err := distiller.LoadLLMExtractionRequest(homePath, requestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "distiller extract request:", err)
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
func cmdKnowledge(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound knowledge <import|extract> ...")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	store, err := mem.Open(homePath)
	die(err)
	switch args[0] {
	case "import":
		rest := args[1:]
		path := ""
		for _, arg := range rest {
			if !strings.HasPrefix(arg, "-") {
				path = arg
				break
			}
		}
		if path == "" {
			fmt.Fprintln(os.Stderr, "usage: compound knowledge import <export.json> [--json]")
			os.Exit(2)
		}
		report, err := knowledge.ImportFile(homePath, store, path, time.Now())
		die(err)
		if hasFlag(rest, "--json") {
			printJSON(report)
			return
		}
		fmt.Printf("# knowledge import %s\n", path)
		fmt.Printf("- items: %d\n", report.Items)
		fmt.Printf("- imported: %d\n", report.Imported)
		fmt.Printf("- echoes: %d\n", report.Echoes)
		fmt.Printf("- reused: %d\n", report.Reused)
		for _, id := range report.EvidenceIDs {
			fmt.Printf(" - evidence %s\n", id)
		}
		for _, id := range report.CandidateIDs {
			fmt.Printf(" - candidate %s\n", id)
		}
		for _, id := range report.ExtractionIDs {
			fmt.Printf(" - extraction %s\n", id)
		}
	case "extract":
		rest := args[1:]
		opts := knowledge.ExtractOptions{
			Pending:             hasFlag(rest, "--pending"),
			ItemID:              flagValue(rest, "--item"),
			FixturesDir:         flagValue(rest, "--fixtures"),
			OpenAICompatibleURL: flagValue(rest, "--openai-compatible-url"),
			Model:               flagValue(rest, "--model"),
			APIKeyEnv:           flagValue(rest, "--api-key-env"),
			EvalCorpusDir:       flagValue(rest, "--eval-corpus"),
			EvalRoot:            flagValue(rest, "--eval-root"),
		}
		if !opts.Pending && strings.TrimSpace(opts.ItemID) == "" {
			fmt.Fprintln(os.Stderr, "usage: compound knowledge extract (--pending|--item <id>) (--fixtures dir | --openai-compatible-url U --model M --eval-corpus dir [--eval-root dir] [--api-key-env E]) [--json]")
			os.Exit(2)
		}
		report, err := knowledge.Extract(homePath, store, opts, time.Now())
		die(err)
		if hasFlag(rest, "--json") {
			printJSON(report)
			return
		}
		fmt.Println("# knowledge extract")
		fmt.Printf("- processed: %d\n", report.Processed)
		fmt.Printf("- created: %d\n", report.Created)
		fmt.Printf("- skipped: %d\n", report.Skipped)
		fmt.Printf("- failed: %d\n", report.Failed)
		for _, id := range report.CandidateIDs {
			fmt.Printf(" - candidate %s\n", id)
		}
		for _, id := range report.ExtractionIDs {
			fmt.Printf(" - extraction %s\n", id)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: compound knowledge <import|extract> ...")
		os.Exit(2)
	}
}

func cmdMemory(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, memoryUsage(""))
		os.Exit(2)
	}
	if hasUsageFlag(args) {
		fmt.Println(memoryUsage(args[0]))
		return
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

	var restoreMemoryEngineProfile func()
	if sub != "engine-config" {
		var err error
		restoreMemoryEngineProfile, err = configureMemoryEngineFromHome(homePath, flag("--engine-profile"))
		die(err)
		defer restoreMemoryEngineProfile()
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
	case "import-topics":
		cmdMemoryImportTopics(store, rest)
	case "temperature":
		cmdMemoryTemperature(homePath, rest)
	case "canon":
		cmdMemoryCanon(store, rest)
	case "volatile-restore":
		if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
			fmt.Fprintln(os.Stderr, "usage: compound memory volatile-restore <cooled_claim_id>")
			os.Exit(2)
		}
		claim, err := store.VolatileRestore(rest[0], time.Now())
		die(err)
		fmt.Printf("restored %s -> %s visibility=volatile expires_at=%s (undo of %s)\n", rest[0], claim.ID, claim.ExpiresAt, mem.CooledReasonVolatileExpired)
	case "aliases":
		cmdMemoryAliases(homePath, store, rest)
	case "candidates":
		limit := 0
		if v := flag("--limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "error: --limit must be a non-negative integer")
				os.Exit(2)
			}
			limit = n
		}
		report, err := store.ListCandidates(time.Now(), mem.CandidateListOptions{
			State:           flag("--state"),
			Kind:            flag("--kind"),
			Limit:           limit,
			IncludeEvidence: has("--include-evidence"),
		})
		die(err)
		if has("--json") {
			printJSON(report)
			return
		}
		fmt.Printf("# memory candidates %d/%d\n", report.Counts.Matched, report.Counts.Total)
		for _, item := range report.Items {
			line := fmt.Sprintf("%s %s %s.%s = %s", item.ID, item.State, item.Entity, item.Predicate, item.Object)
			if item.ReviewState != "" {
				line += " review=" + item.ReviewState
			}
			fmt.Println(line)
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
		direction := flag("--direction")
		kinds := flagCSVValues(rest, "--kind", "--kinds")
		if v := flag("--depth"); v != "" {
			depth, _ = strconv.Atoi(v)
		}
		if v := flag("--budget"); v != "" {
			budget, _ = strconv.Atoi(v)
		}
		if v := flag("--min-conf"); v != "" {
			minConf, _ = strconv.ParseFloat(v, 64)
		}
		sg, err := store.RecallGraphOpts(firstPos(map[string]bool{"--depth": true, "--budget": true, "--min-conf": true, "--direction": true, "--kind": true, "--kinds": true}), mem.GraphOpts{Depth: depth, Budget: budget, MinConf: minConf, Direction: direction, Kinds: kinds})
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
		cmdMemoryRecall(store, rest)
	case "bakeoff":
		cmdMemoryBakeoff(store, rest, memBreadthEngine)
	case "search":
		if flag("--lane") == "" {
			fmt.Fprintln(os.Stderr, "usage: compound memory search --lane L <query> [--k N] [--json]")
			os.Exit(2)
		}
		cmdMemoryCorpusSearch(homePath, rest)
	case "timeline":
		claims, err := store.Timeline(pos())
		die(err)
		for _, c := range claims {
			arrow := "  +"
			if c.Supersedes != "" {
				arrow = "  ->"
			}
			line := fmt.Sprintf("%s seq %-3d %s %s.%s = %s", arrow, c.Seq, c.ID, c.Entity, c.Predicate, c.Object)
			if c.Supersedes != "" {
				line += " supersedes=" + c.Supersedes
			}
			if len(c.EvidenceRefs) > 0 {
				line += " evidence=" + strings.Join(c.EvidenceRefs, ",")
			}
			fmt.Println(line)
		}
	case "fold":
		cmdMemoryFold(store, rest)
	case "position":
		cmdMemoryPosition(store, rest)
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
	case "engine-config":
		if has("--clear") && flag("--profile") != "" {
			fmt.Fprintln(os.Stderr, memoryUsage("engine-config"))
			os.Exit(2)
		}
		var report memoryEngineConfigReport
		var err error
		switch {
		case has("--clear"):
			report, err = clearMemoryEngineConfig(homePath)
		case flag("--profile") != "":
			report, err = writeMemoryEngineConfig(homePath, flag("--profile"))
		default:
			cfg, path, loadErr := loadMemoryEngineConfig(homePath)
			err = loadErr
			report = memoryEngineConfigReport{
				SchemaVersion: memoryEngineConfigSchemaVersion,
				ConfigPath:    path,
				Configured:    cfg.Profile != "",
				Profile:       cfg.Profile,
				CorpusLanes:   len(cfg.CorpusLanes),
			}
		}
		die(err)
		if has("--json") {
			printJSON(report)
			return
		}
		fmt.Println("# memory engine-config")
		fmt.Printf("- config: %s\n", report.ConfigPath)
		fmt.Printf("- configured: %t\n", report.Configured)
		if report.Profile != "" {
			fmt.Printf("- profile: %s\n", report.Profile)
		}
		if report.CorpusLanes > 0 {
			fmt.Printf("- corpus_lanes: %d\n", report.CorpusLanes)
		}
		if report.Cleared {
			fmt.Println("- cleared: true")
		}
	case "doctor":
		var r mem.Report
		var eng mem.Engine
		if strings.TrimSpace(os.Getenv("COMPOUND_MEM_ENGINE_BIN")) != "" {
			var closeEng func()
			eng, closeEng = memBreadthEngine()
			r = store.DoctorWithEngine(eng)
			closeEng()
		} else {
			r = store.Doctor()
		}
		r.SemanticReadiness = memorySemanticReadiness(homePath, eng)
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
		if r.SemanticReadiness != nil {
			fmt.Printf("%-15s %-8s %s\n", "semantic", r.SemanticReadiness.Status, r.SemanticReadiness.Detail)
		}
		fmt.Printf("verdict: %s — %s\n", okStr(r.OK), r.Note)
		if !r.OK {
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: compound memory <candidate|import-page|adjudicate|recall|timeline|query|volatile-restore|engine-doctor|engine-compare [--allow-expansion]|bakeoff <init|run>|doctor> ...")
		os.Exit(2)
	}
}

// memBreadthEngine selects the rented breadth engine: a protocol sidecar (held warm) when
// COMPOUND_MEM_ENGINE_BIN is set (e.g. the turbovec sidecar, or compound-mock-engine), else the
// in-process lexical engine. Its own env var — distinct from the legacy COMPOUND_ENGINE_BIN, which
// speaks a different protocol and retires with the legacy engine. COMPOUND_MEM_ENGINE_EMBEDDER is
// compound's record of what it indexed with; it drives the identity guard (empty = adopt on first use).
func memBreadthEngine() (mem.Engine, func()) {
	// One shared factory (mem.DefaultBreadthEngine) so the CLI and the orient/ContextPack
	// path build the identical engine. Resolve the home here (CLI layer owns home resolution)
	// and hand it to the factory, which forwards it to the sidecar's vector cache.
	resolved := ""
	if hp, err := home.Resolve(""); err == nil {
		resolved = strings.TrimSpace(hp)
	}
	return mem.DefaultBreadthEngine(resolved)
}

const memoryEngineConfigSchemaVersion = "compound.memory_engine_config.v0.1"

type memoryEngineConfig struct {
	SchemaVersion string `json:"schema_version" yaml:"schema_version"`
	Profile       string `json:"profile,omitempty" yaml:"profile,omitempty"`
	// Embed is the OPT-IN embedder for the breadth profile's semantic lane. Absent/empty =
	// lexical only (OFF by default, no silent default — the EP-A6 ruling). When Model is set,
	// the breadth profile forwards these to the sidecar so semantic recall engages WITHOUT the
	// operator exporting COMPOUND_BREADTH_EMBED_* in every shell.
	Embed *memoryEmbedConfig `json:"embed,omitempty" yaml:"embed,omitempty"`
	// CorpusLanes are class-separated imported-corpus indexes. They are never
	// ambient memory profiles and never inherit COMPOUND_BREADTH_EMBED_*.
	CorpusLanes []corpus.LaneConfig `json:"corpus_lanes,omitempty" yaml:"corpus_lanes,omitempty"`
}

type memoryEmbedConfig struct {
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"` // e.g. ollama | openai
	Model    string `json:"model,omitempty" yaml:"model,omitempty"`       // e.g. nomic-embed-text | text-embedding-3-large
	BaseURL  string `json:"base_url,omitempty" yaml:"base_url,omitempty"` // e.g. localhost:11434/v1 (ollama)
	Embedder string `json:"embedder,omitempty" yaml:"embedder,omitempty"` // optional identity guard (provider:model:dim:bits)
}

type memoryEngineConfigReport struct {
	SchemaVersion string `json:"schema_version"`
	ConfigPath    string `json:"config_path"`
	Configured    bool   `json:"configured"`
	Profile       string `json:"profile,omitempty"`
	CorpusLanes   int    `json:"corpus_lanes,omitempty"`
	Cleared       bool   `json:"cleared,omitempty"`
}

func normalizeMemoryEngineProfile(profile string) (string, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	switch profile {
	case "", "generic", "memory-cli", "gbrain", "breadth":
		return profile, nil
	default:
		return "", fmt.Errorf("unknown memory engine profile %q", profile)
	}
}

func memoryEngineConfigPath(homePath string) string {
	return filepath.Join(homePath, "config", "memory-engine.yaml")
}

func loadMemoryEngineConfig(homePath string) (memoryEngineConfig, string, error) {
	path := memoryEngineConfigPath(homePath)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return memoryEngineConfig{}, path, nil
	}
	if err != nil {
		return memoryEngineConfig{}, path, err
	}
	var cfg memoryEngineConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return memoryEngineConfig{}, path, fmt.Errorf("parse memory engine config: %w", err)
	}
	if cfg.SchemaVersion != "" && cfg.SchemaVersion != memoryEngineConfigSchemaVersion {
		return memoryEngineConfig{}, path, fmt.Errorf("unsupported memory engine config schema %q", cfg.SchemaVersion)
	}
	profile, err := normalizeMemoryEngineProfile(cfg.Profile)
	if err != nil {
		return memoryEngineConfig{}, path, err
	}
	cfg.SchemaVersion = memoryEngineConfigSchemaVersion
	cfg.Profile = profile
	if err := validateCorpusLaneConfigs(cfg.CorpusLanes); err != nil {
		return memoryEngineConfig{}, path, err
	}
	for i := range cfg.CorpusLanes {
		lane, err := corpus.ValidateLaneConfig(cfg.CorpusLanes[i])
		if err != nil {
			return memoryEngineConfig{}, path, err
		}
		cfg.CorpusLanes[i] = lane
	}
	return cfg, path, nil
}

func validateCorpusLaneConfigs(lanes []corpus.LaneConfig) error {
	seen := map[string]bool{}
	for _, lane := range lanes {
		name := strings.TrimSpace(lane.Lane)
		if name == "" {
			return fmt.Errorf("corpus lane name required")
		}
		if seen[name] {
			return fmt.Errorf("duplicate corpus lane %q", name)
		}
		seen[name] = true
	}
	return nil
}

func writeMemoryEngineConfig(homePath, profile string) (memoryEngineConfigReport, error) {
	profile, err := normalizeMemoryEngineProfile(profile)
	if err != nil {
		return memoryEngineConfigReport{}, err
	}
	if profile == "" {
		return memoryEngineConfigReport{}, fmt.Errorf("memory engine profile is required")
	}
	path := memoryEngineConfigPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return memoryEngineConfigReport{}, err
	}
	cfg := memoryEngineConfig{SchemaVersion: memoryEngineConfigSchemaVersion, Profile: profile}
	// Preserve an existing opt-in embedder block so switching profile doesn't silently disable
	// the semantic lane the operator configured.
	if existing, _, err := loadMemoryEngineConfig(homePath); err == nil && existing.Embed != nil {
		cfg.Embed = existing.Embed
	}
	if existing, _, err := loadMemoryEngineConfig(homePath); err == nil && len(existing.CorpusLanes) > 0 {
		cfg.CorpusLanes = existing.CorpusLanes
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return memoryEngineConfigReport{}, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return memoryEngineConfigReport{}, err
	}
	return memoryEngineConfigReport{
		SchemaVersion: memoryEngineConfigSchemaVersion,
		ConfigPath:    path,
		Configured:    true,
		Profile:       profile,
	}, nil
}

func clearMemoryEngineConfig(homePath string) (memoryEngineConfigReport, error) {
	path := memoryEngineConfigPath(homePath)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return memoryEngineConfigReport{}, err
	}
	return memoryEngineConfigReport{
		SchemaVersion: memoryEngineConfigSchemaVersion,
		ConfigPath:    path,
		Configured:    false,
		Cleared:       true,
	}, nil
}

func configureMemoryEngineFromHome(homePath, explicitProfile string) (func(), error) {
	profile, err := normalizeMemoryEngineProfile(explicitProfile)
	if err != nil {
		return func() {}, err
	}
	var cfg memoryEngineConfig
	cfg, _, err = loadMemoryEngineConfig(homePath)
	if err != nil {
		return func() {}, err
	}
	if profile == "" {
		profile = cfg.Profile
	}
	restore, err := configureMemoryEngineProfile(profile)
	if err != nil {
		return restore, err
	}
	// Opt-in embedder: makes the breadth semantic lane engage from home config, not shell exports.
	// Fill-in only (a shell export still wins) and gated on Model being set, so an empty/absent
	// block leaves the lane lexical — OFF by default, no silent default (EP-A6 ruling).
	if profile == "breadth" && cfg.Embed != nil && strings.TrimSpace(cfg.Embed.Model) != "" {
		embedRestore := applyEmbedConfigEnv(*cfg.Embed)
		return func() { embedRestore(); restore() }, nil
	}
	return restore, nil
}

// applyEmbedConfigEnv fills the breadth sidecar's embedder env from config WITHOUT overriding an
// explicit shell export, capturing prior values so the caller can restore them.
func applyEmbedConfigEnv(embed memoryEmbedConfig) func() {
	restore := captureEnv(
		"COMPOUND_BREADTH_EMBED_MODEL",
		"COMPOUND_BREADTH_EMBED_PROVIDER",
		"COMPOUND_BREADTH_EMBED_BASE_URL",
		"COMPOUND_MEM_ENGINE_EMBEDDER",
	)
	setIfUnset := func(key, val string) {
		if strings.TrimSpace(val) == "" {
			return
		}
		if strings.TrimSpace(os.Getenv(key)) == "" {
			_ = os.Setenv(key, val)
		}
	}
	setIfUnset("COMPOUND_BREADTH_EMBED_MODEL", embed.Model)
	setIfUnset("COMPOUND_BREADTH_EMBED_PROVIDER", embed.Provider)
	setIfUnset("COMPOUND_BREADTH_EMBED_BASE_URL", embed.BaseURL)
	setIfUnset("COMPOUND_MEM_ENGINE_EMBEDDER", embed.Embedder)
	return restore
}

type memoryVectorCacheManifest struct {
	SchemaVersion string `json:"schema_version"`
	Embedder      struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Dim      int    `json:"dim"`
		Bits     string `json:"bits"`
	} `json:"embedder"`
	CorpusGen     int64 `json:"corpus_gen"`
	ClaimCount    int   `json:"claim_count"`
	EmbeddedCount int   `json:"embedded_count"`
}

func memorySemanticReadiness(homePath string, eng mem.Engine) *mem.SemanticReadiness {
	cfg, cfgPath, err := loadMemoryEngineConfig(homePath)
	readiness := &mem.SemanticReadiness{
		Status:            "lexical_only",
		ConfigPath:        cfgPath,
		Sidecar:           "none",
		VectorCacheStatus: "not_configured",
		Detail:            "breadth is lexical only; no semantic embedder model is configured",
	}
	if err != nil {
		readiness.Status = "semantic_degraded"
		readiness.Detail = "memory engine config unreadable: " + err.Error()
		return readiness
	}
	readiness.Profile = cfg.Profile
	if sidecar := strings.TrimSpace(os.Getenv("COMPOUND_MEM_ENGINE_BIN")); sidecar != "" {
		readiness.Sidecar = "configured"
	}
	if eng != nil {
		caps := eng.Capabilities()
		readiness.Engine = firstNonEmptyString(caps.Name, "configured-engine")
	}

	model := strings.TrimSpace(os.Getenv("COMPOUND_BREADTH_EMBED_MODEL"))
	if model == "" && cfg.Embed != nil {
		model = strings.TrimSpace(cfg.Embed.Model)
	}
	guard := strings.TrimSpace(os.Getenv("COMPOUND_MEM_ENGINE_EMBEDDER"))
	if guard == "" && cfg.Embed != nil {
		guard = strings.TrimSpace(cfg.Embed.Embedder)
	}
	readiness.EmbedderGuard = guard
	if model == "" {
		if readiness.Sidecar == "configured" {
			readiness.Detail = fmt.Sprintf("profile=%s sidecar=%s engine=%s; semantic embedder model is not configured", firstNonEmptyString(readiness.Profile, "default"), readiness.Sidecar, firstNonEmptyString(readiness.Engine, "none"))
		}
		return readiness
	}
	readiness.EmbedderConfigured = true

	embedder, ok := breadth.ResolveEmbedder(homePath)
	if !ok || embedder == nil {
		readiness.Status = "semantic_degraded"
		readiness.VectorCacheStatus = "unknown"
		readiness.Detail = fmt.Sprintf("profile=%s sidecar=%s; semantic embedder config did not resolve or guard mismatched", firstNonEmptyString(readiness.Profile, "default"), readiness.Sidecar)
		return readiness
	}
	identity := embedder.Identity()
	readiness.EmbedderIdentity = identity
	cacheStatus, cacheDetail := memoryVectorCacheReadiness(homePath, identity, 0)
	readiness.VectorCacheStatus = cacheStatus.Status
	readiness.VectorCachePath = cacheStatus.Path
	readiness.VectorCacheCorpusGen = cacheStatus.CorpusGen
	readiness.VectorCacheClaims = cacheStatus.ClaimCount
	readiness.VectorCacheEmbedded = cacheStatus.EmbeddedCount

	if readiness.Sidecar != "configured" {
		readiness.Status = "semantic_inactive"
		readiness.Detail = fmt.Sprintf("profile=%s sidecar=none embedder=%s vector_cache=%s; semantic embedder configured but no breadth sidecar is active", firstNonEmptyString(readiness.Profile, "default"), identity, readiness.VectorCacheStatus)
		return readiness
	}
	if eng == nil {
		readiness.Status = "semantic_degraded"
		readiness.Detail = fmt.Sprintf("profile=%s sidecar=configured embedder=%s vector_cache=%s; memory doctor did not attach engine", firstNonEmptyString(readiness.Profile, "default"), identity, readiness.VectorCacheStatus)
		return readiness
	}
	caps := eng.Capabilities()
	readiness.Engine = firstNonEmptyString(caps.Name, "configured-engine")
	if caps.Embedder != "" && caps.Embedder != identity {
		readiness.Status = "semantic_degraded"
		readiness.Detail = fmt.Sprintf("profile=%s sidecar=configured engine=%s embedder_mismatch configured=%s engine=%s vector_cache=%s", firstNonEmptyString(readiness.Profile, "default"), readiness.Engine, identity, caps.Embedder, readiness.VectorCacheStatus)
		return readiness
	}
	if caps.Embedder == "" || caps.Embedder == breadth.DefaultEmbedder {
		readiness.Status = "semantic_inactive"
		readiness.Detail = fmt.Sprintf("profile=%s sidecar=configured engine=%s embedder=%s vector_cache=%s; sidecar reported lexical/no semantic embedder", firstNonEmptyString(readiness.Profile, "default"), readiness.Engine, identity, readiness.VectorCacheStatus)
		return readiness
	}
	if readiness.VectorCacheStatus == "embedder_mismatch" || readiness.VectorCacheStatus == "unreadable" || readiness.VectorCacheStatus == "schema_mismatch" {
		readiness.Status = "semantic_degraded"
		readiness.Detail = fmt.Sprintf("profile=%s sidecar=configured engine=%s embedder=%s vector_cache=%s: %s", firstNonEmptyString(readiness.Profile, "default"), readiness.Engine, identity, readiness.VectorCacheStatus, cacheDetail)
		return readiness
	}
	readiness.Status = "semantic_active"
	readiness.Detail = fmt.Sprintf("profile=%s sidecar=configured engine=%s embedder=%s vector_cache=%s", firstNonEmptyString(readiness.Profile, "default"), readiness.Engine, identity, readiness.VectorCacheStatus)
	if cacheDetail != "" {
		readiness.Detail += " (" + cacheDetail + ")"
	}
	return readiness
}

type memoryVectorCacheStatus struct {
	Status        string
	Path          string
	CorpusGen     int64
	ClaimCount    int
	EmbeddedCount int
}

func memoryVectorCacheReadiness(homePath, identity string, engineGen int64) (memoryVectorCacheStatus, string) {
	status := memoryVectorCacheStatus{Status: "not_configured"}
	if strings.TrimSpace(identity) == "" {
		return status, ""
	}
	status.Status = "missing"
	status.Path = filepath.Join(homePath, "indexes", "breadth", memoryStampSlug(identity), "manifest.json")
	raw, err := os.ReadFile(status.Path)
	if os.IsNotExist(err) {
		return status, "manifest missing"
	}
	if err != nil {
		status.Status = "unreadable"
		return status, err.Error()
	}
	var manifest memoryVectorCacheManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		status.Status = "unreadable"
		return status, "parse manifest: " + err.Error()
	}
	status.CorpusGen = manifest.CorpusGen
	status.ClaimCount = manifest.ClaimCount
	status.EmbeddedCount = manifest.EmbeddedCount
	if manifest.SchemaVersion != "compound.breadth.index.v1" {
		status.Status = "schema_mismatch"
		return status, "schema=" + manifest.SchemaVersion
	}
	if got := memoryManifestEmbedderIdentity(manifest); got != identity {
		status.Status = "embedder_mismatch"
		return status, fmt.Sprintf("cache=%s configured=%s", got, identity)
	}
	if manifest.EmbeddedCount < manifest.ClaimCount {
		status.Status = "partial"
		return status, fmt.Sprintf("%d/%d embedded", manifest.EmbeddedCount, manifest.ClaimCount)
	}
	if engineGen > 0 && manifest.CorpusGen < engineGen {
		status.Status = "stale"
		return status, fmt.Sprintf("cache_gen=%d engine_gen=%d", manifest.CorpusGen, engineGen)
	}
	status.Status = "healthy"
	return status, fmt.Sprintf("gen=%d embedded=%d/%d", manifest.CorpusGen, manifest.EmbeddedCount, manifest.ClaimCount)
}

func memoryManifestEmbedderIdentity(manifest memoryVectorCacheManifest) string {
	return fmt.Sprintf("%s:%s:%d:%s", manifest.Embedder.Provider, manifest.Embedder.Model, manifest.Embedder.Dim, manifest.Embedder.Bits)
}

func memoryStampSlug(identity string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(identity) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func configureMemoryEngineProfile(profile string) (func(), error) {
	profile, err := normalizeMemoryEngineProfile(profile)
	if err != nil {
		return func() {}, err
	}
	if profile == "" {
		return func() {}, nil
	}
	restore := captureEnv("COMPOUND_MEM_ENGINE_BIN", "COMPOUND_MEMORY_CLI_PROFILE")
	if profile == "breadth" {
		bin, err := defaultBreadthSidecarCommand()
		if err != nil {
			return restore, err
		}
		if err := os.Setenv("COMPOUND_MEM_ENGINE_BIN", bin); err != nil {
			return restore, err
		}
		_ = os.Unsetenv("COMPOUND_MEMORY_CLI_PROFILE")
		return restore, nil
	}
	if strings.TrimSpace(os.Getenv("COMPOUND_MEM_ENGINE_BIN")) == "" {
		if err := os.Setenv("COMPOUND_MEM_ENGINE_BIN", defaultMemoryCLISidecarCommand()); err != nil {
			return restore, err
		}
	}
	if err := os.Setenv("COMPOUND_MEMORY_CLI_PROFILE", profile); err != nil {
		restore()
		return func() {}, err
	}
	return restore, nil
}

func defaultMemoryCLISidecarCommand() string {
	for _, path := range []string{"./cmd/compound-memory-cli-engine", "../compound-memory-cli-engine"} {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return "go run " + path
		}
	}
	return "compound-memory-cli-engine"
}

func defaultBreadthSidecarCommand() (string, error) {
	path, err := exec.LookPath("compound-breadth-engine")
	if err != nil {
		return "", fmt.Errorf("resolve compound-breadth-engine: %w", err)
	}
	return path, nil
}

func captureEnv(names ...string) func() {
	type value struct {
		text string
		ok   bool
	}
	saved := map[string]value{}
	for _, name := range names {
		text, ok := os.LookupEnv(name)
		saved[name] = value{text: text, ok: ok}
	}
	return func() {
		for _, name := range names {
			v := saved[name]
			if v.ok {
				_ = os.Setenv(name, v.text)
			} else {
				_ = os.Unsetenv(name)
			}
		}
	}
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
		fmt.Fprintln(os.Stderr, routeUsage(""))
		os.Exit(2)
	}
	if hasUsageFlag(args) {
		fmt.Println(routeUsage(args[0]))
		return
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	if tryRoutePromotionCommand(args, homePath) {
		return
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
			if hasFlag(args[1:], "--json") {
				printJSON(plan)
				return
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
				fmt.Printf("- lane: %d %s tier=%s cost_class=%s role=%s claim=%s harness=%s provider_route=%s effort=%s\n",
					lane.Position,
					lane.ModelID,
					lane.Tier,
					emptyDash(lane.CostClass),
					lane.GateRole,
					lane.ClaimLevel,
					emptyDash(lane.Harness),
					emptyDash(lane.ProviderRoute),
					emptyDash(lane.EffortDefault),
				)
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
		receiptID := flagValue(args[1:], "--receipt-id")
		workID := flagValue(args[1:], "--work-id")
		targetPath := flagValue(args[1:], "--target")
		ev, rec, err := routeevidence.ResolveOutcomeReviewInputs(homePath, routeevidence.OutcomeReviewSelector{
			RouteID:           routeID,
			RouteEvidencePath: routePath,
			ReceiptPath:       receiptPath,
			ReceiptID:         receiptID,
			WorkID:            workID,
		})
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
		fmt.Fprintln(os.Stderr, routeUsage(""))
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
	parsed, err := parseHookArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	homePath := ""
	fx, err := loadHookFixture(parsed, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	deps := hook.Deps{Registry: harness.DefaultRegistry(), Orient: orientPolicy(), Policy: policyConfig()}
	if resolvedHome, err := home.Resolve(""); err == nil {
		homePath = resolvedHome
		deps.HomePath = homePath
		if store, err := work.Open(homePath); err == nil {
			deps.Store = store // session-end needs it; others tolerate nil
		}
		// Resolve the memory-engine profile so a session-start / user-prompt-submit hook injects
		// ambient recall from the SAME owned engine `memory query` uses (else it stays lexical).
		if restoreEngine, err := configureMemoryEngineFromHome(homePath, ""); err == nil {
			defer restoreEngine()
		}
	}

	res, err := hook.Run(fx, deps, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if parsed.Live && strings.TrimSpace(parsed.ParityLogDir) != "" {
		if err := writeLiveHookParityLog(parsed.ParityLogDir, res, time.Now()); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
	}
	if parsed.Live {
		printJSON(liveHookResponse(parsed.Harness, parsed.HookName, res))
	} else {
		printJSON(res)
	}
	if parsed.HookName == "session-end" {
		autoConsumeCapabilityFeedback(homePath)
	}
	fmt.Fprintf(os.Stderr, "[%s/%s] %s — %s\n", fx.Harness, res.Event.Status, res.Event.HookName, res.Summary)
	if parsed.Enforce {
		return hookEnforceExit(parsed.HookName, res)
	}
	return 0
}

func parseHookArgs(args []string) (hookCommandArgs, error) {
	var parsed hookCommandArgs
	if len(args) < 2 {
		return parsed, fmt.Errorf(hookUsage)
	}
	parsed.HookName = args[0]
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--enforce" {
			parsed.Enforce = true
			continue
		}
		if arg == "--live" {
			parsed.Live = true
			continue
		}
		if arg == "--harness" {
			if i+1 >= len(args) {
				return parsed, fmt.Errorf(hookUsage)
			}
			i++
			parsed.Harness = args[i]
			continue
		}
		if arg == "--parity-log" {
			if i+1 >= len(args) {
				return parsed, fmt.Errorf(hookUsage)
			}
			i++
			parsed.ParityLogDir = args[i]
			continue
		}
		if parsed.FixturePath != "" {
			return parsed, fmt.Errorf(hookUsage)
		}
		parsed.FixturePath = arg
	}
	if parsed.Live {
		if strings.TrimSpace(parsed.Harness) == "" || parsed.FixturePath != "" {
			return parsed, fmt.Errorf(hookUsage)
		}
		return parsed, nil
	}
	if parsed.FixturePath == "" {
		return parsed, fmt.Errorf(hookUsage)
	}
	if parsed.ParityLogDir != "" {
		return parsed, fmt.Errorf(hookUsage)
	}
	return parsed, nil
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
		fmt.Fprintln(os.Stderr, "usage: compound policy <evaluate|import-placement-rules|placement-authority|import-standing-grants|standing-grants-authority|import-operator-state|operator-authority|import-operator-prompts|operator-prompts-authority|import-operator-approval-frontier|operator-approval-frontier-authority|import-release-goal-holds|release-goal-holds-authority|autonomy-halt|editable-surfaces> ...")
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
			NeedsMeCount     int                                         `json:"needs_me_count,omitempty"`
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
			report.NeedsMeCount = len(state.NeedsMe)
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
	case "autonomy-halt":
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound policy autonomy-halt [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		type autonomyHaltClass struct {
			Class  string `json:"class"`
			Reason string `json:"reason,omitempty"`
		}
		type autonomyHaltReport struct {
			AuthorityRef       string              `json:"authority_ref"`
			AuthorityPath      string              `json:"authority_path"`
			Source             string              `json:"source"`
			Installed          bool                `json:"installed"`
			GlobalHalt         bool                `json:"global_halt"`
			Reason             string              `json:"reason,omitempty"`
			DisabledClasses    []autonomyHaltClass `json:"disabled_classes,omitempty"`
			DisabledClassCount int                 `json:"disabled_class_count"`
		}
		halt, source, err := policy.LoadAutonomyHalt(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report := autonomyHaltReport{
			AuthorityRef:  policy.AutonomyHaltAuthorityRef,
			AuthorityPath: policy.AutonomyHaltPath(homePath),
			Source:        source,
			Installed:     source != "unset",
			GlobalHalt:    halt.Global,
			Reason:        halt.Reason,
		}
		for class, reason := range halt.DisabledClasses {
			report.DisabledClasses = append(report.DisabledClasses, autonomyHaltClass{Class: class, Reason: reason})
		}
		sort.Slice(report.DisabledClasses, func(i, j int) bool { return report.DisabledClasses[i].Class < report.DisabledClasses[j].Class })
		report.DisabledClassCount = len(report.DisabledClasses)
		if len(args) == 2 && args[1] == "--json" {
			printJSON(report)
			return
		}
		fmt.Printf("autonomy halt authority: %s\n", report.AuthorityRef)
		fmt.Printf("source: %s\n", report.Source)
		fmt.Printf("global_halt: %t\n", report.GlobalHalt)
		if report.Reason != "" {
			fmt.Printf("reason: %s\n", report.Reason)
		}
		fmt.Printf("disabled_classes: %d\n", report.DisabledClassCount)
		for _, dc := range report.DisabledClasses {
			if dc.Reason != "" {
				fmt.Printf("  - %s: %s\n", dc.Class, dc.Reason)
			} else {
				fmt.Printf("  - %s\n", dc.Class)
			}
		}
	case "editable-surfaces":
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound policy editable-surfaces [--json]")
			os.Exit(2)
		}
		homePath, err := home.Resolve("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		reg, source, err := policy.LoadEditableSurfaces(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		report := buildEditableSurfacesReport(reg, source, policy.EditableSurfacesPath(homePath))
		if len(args) == 2 && args[1] == "--json" {
			printJSON(report)
			return
		}
		fmt.Printf("editable surfaces authority: %s\n", report.AuthorityRef)
		fmt.Printf("source: %s\n", report.Source)
		fmt.Printf("installed: %t\n", report.Installed)
		fmt.Printf("valid: %t\n", report.Valid)
		fmt.Printf("nothing_editable: %t\n", report.NothingEditable)
		fmt.Printf("hard_deny_core: %s\n", strings.Join(report.HardDenyCore, ", "))
		fmt.Printf("entries: %d (active: %d)\n", report.EntryCount, report.ActiveCount)
		for _, e := range report.Entries {
			fmt.Printf("  - %s [%s] class=%s", e.Surface, e.Status, e.ProposalClass)
			if e.Description != "" {
				fmt.Printf(" — %s", e.Description)
			}
			fmt.Println()
		}
		for _, ve := range report.ValidationErrors {
			fmt.Printf("  ! validation_error: %s\n", ve)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: compound policy <evaluate|import-placement-rules|placement-authority|import-operator-state|operator-authority|import-operator-prompts|operator-prompts-authority|import-operator-approval-frontier|operator-approval-frontier-authority|import-release-goal-holds|release-goal-holds-authority|autonomy-halt|editable-surfaces> ...")
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

func validateKnownFlags(args []string, valueFlags, boolFlags map[string]bool) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		if boolFlags[arg] {
			continue
		}
		if valueFlags[arg] {
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a value", arg)
			}
			i++
			continue
		}
		return fmt.Errorf("unknown flag %s", arg)
	}
	return nil
}

func cmdReviews(args []string) {
	if len(args) > 0 && args[0] == "record-finding" {
		cmdReviewsRecordFinding(args[1:])
		return
	}
	jsonOut := hasFlag(args, "--json")
	area := valueFlag(args, "--area")
	includeArchived := hasFlag(args, "--include-archived")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
		case "--include-archived":
		case "--area":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: compound reviews [--json] [--area A] [--include-archived]")
				os.Exit(2)
			}
			i++
		default:
			fmt.Fprintln(os.Stderr, "usage: compound reviews [--json] [--area A] [--include-archived]")
			os.Exit(2)
		}
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err, "(run `compound home init`)")
		os.Exit(1)
	}
	report, err := buildReviewQueueReport(homePath, area, includeArchived)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(report)
		return
	}
	fmt.Printf("reviews total=%d review_required=%d ready_to_apply=%d ok=%d\n", report.Counts.Total, report.Counts.ReviewRequired, report.Counts.ReadyToApply, report.Counts.OK)
	if report.Counts.DecisionCardCount > 0 || report.Counts.ActionableCount > 0 || report.Counts.ReviewRequiredMissingDecisionCard > 0 || report.Counts.ReviewRequiredMissingAction > 0 || report.Counts.FallbackSummaryCount > 0 || report.Counts.NeedsYouFallbackGroupCount > 0 {
		fmt.Printf("review_surface decision_cards=%d actionable=%d missing_cards=%d missing_actions=%d fallback_summaries=%d needs_you_fallback_groups=%d\n", report.Counts.DecisionCardCount, report.Counts.ActionableCount, report.Counts.ReviewRequiredMissingDecisionCard, report.Counts.ReviewRequiredMissingAction, report.Counts.FallbackSummaryCount, report.Counts.NeedsYouFallbackGroupCount)
	}
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
	if len(report.Groups) > 0 {
		fmt.Println("groups:")
		for _, group := range report.Groups {
			fmt.Printf("- %s count=%d area=%s", group.Key, group.Count, group.Area)
			if group.ContextLabel != "" {
				fmt.Printf(" context=%q", group.ContextLabel)
			}
			if group.SafeBatchAction != nil {
				fmt.Printf(" action=%s", group.SafeBatchAction.Command)
			} else if group.TerminalReason != "" {
				fmt.Printf(" reason=%q", group.TerminalReason)
			}
			fmt.Println()
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
		if _, err := store.Advance(workID, work.AdvanceInput{To: "running", Actor: "compound reviews", Note: "review finding recorded"}, now); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if _, err := store.Advance(workID, work.AdvanceInput{To: "verifying", Actor: "compound reviews", VerificationID: target, Note: "review finding artifact written"}, now); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if _, err := store.Close(workID, work.CloseInput{
			Outcome:   "completed",
			MoveType:  "control_plane_repair",
			Actor:     "compound reviews",
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
	var harness, cwd, task, repoKind, mode, topic string
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
		case "--topic":
			if i+1 < len(args) {
				i++
				topic = args[i]
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
	return contract.Input{Harness: harness, Cwd: cwd, Task: task, RepoKind: kind, RepoKindVia: via, Mode: mode, Topic: topic}, nil
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
		_, err = contract.RecordEffectiveContract(homePath, ec)
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
			AuthorityRef                string                                   `json:"authority_ref"`
			AuthorityPath               string                                   `json:"authority_path"`
			Installed                   bool                                     `json:"installed"`
			HookCount                   int                                      `json:"hook_count,omitempty"`
			Harnesses                   []string                                 `json:"harnesses,omitempty"`
			PreToolUseReadiness         []contract.HookGateReadiness             `json:"pre_tool_use_readiness,omitempty"`
			HookReadiness               []contract.InstalledHookRuntimeReadiness `json:"hook_readiness,omitempty"`
			SourceRoleCounts            map[string]int                           `json:"source_role_counts,omitempty"`
			RetiredSourceAuthorityCount int                                      `json:"retired_source_authority_count"`
			RetiredSourceAuthorityRefs  []string                                 `json:"retired_source_authority_refs,omitempty"`
			SourceAuthority             []contract.InstalledHooksSourceAuthority `json:"source_authority,omitempty"`
			SourceRecordPath            string                                   `json:"source_record_path"`
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
			report.PreToolUseReadiness = contract.InstalledHookGateReadiness(manifest, nativePreToolUseBlockMap(harness.DefaultRegistry()))
			report.HookReadiness = contract.InstalledHookRuntimeReadinessRows(manifest, installedHookRuntimeSupport())
		}
		records, err := contract.LoadInstalledHooksSourceAuthority(homePath)
		die(err)
		sourceSummary := contract.SummarizeInstalledHooksSourceAuthority(records)
		report.SourceRoleCounts = sourceSummary.SourceRoleCounts
		report.RetiredSourceAuthorityCount = sourceSummary.RetiredSourceAuthorityCount
		report.RetiredSourceAuthorityRefs = sourceSummary.RetiredSourceAuthorityRefs
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

func installedHookRuntimeSupport() []contract.HookRuntimeSupport {
	return []contract.HookRuntimeSupport{
		hookRuntimeSupport("claude_code", "SessionStart", "fixtures/hooks/session-start.json", true),
		hookRuntimeSupport("claude_code", "UserPromptSubmit", "fixtures/hooks/user-prompt-submit.json", true),
		hookRuntimeSupport("claude_code", "PreToolUse", "fixtures/hooks/pre-tool-use.claude.json", true),
		hookRuntimeSupport("claude_code", "PostToolUse", "fixtures/hooks/post-tool-use.json", true),
		hookRuntimeSupport("claude_code", "SessionEnd", "", true),
		hookRuntimeSupport("codex", "SessionStart", "fixtures/hooks/session-start.json", true),
		hookRuntimeSupport("codex", "UserPromptSubmit", "fixtures/hooks/user-prompt-submit.json", true),
		hookRuntimeSupport("codex", "PreToolUse", "fixtures/hooks/pre-tool-use.codex.json", false),
	}
}

func hookRuntimeSupport(harnessName, hookName, fixtureRef string, liveAdapter bool) contract.HookRuntimeSupport {
	return contract.HookRuntimeSupport{
		Harness:        harnessName,
		Hook:           hookName,
		FixtureRef:     fixtureRef,
		FixturePresent: fileExists(fixtureRef),
		LiveAdapter:    liveAdapter,
		LiveCommand:    liveHookCommand(harnessName, hookName, liveAdapter),
	}
}

func liveHookCommand(harnessName, hookName string, liveAdapter bool) string {
	if !liveAdapter {
		return ""
	}
	harnessArg := strings.ReplaceAll(harnessName, "_", "-")
	return fmt.Sprintf("compound hook %s --live --harness %s", hookCommandName(hookName), harnessArg)
}

func hookCommandName(hookName string) string {
	switch strings.TrimSpace(hookName) {
	case "SessionStart":
		return "session-start"
	case "UserPromptSubmit":
		return "user-prompt-submit"
	case "PreToolUse":
		return "pre-tool-use"
	case "PostToolUse":
		return "post-tool-use"
	case "SessionEnd":
		return "session-end"
	case "Stop":
		return "stop"
	default:
		return strings.ToLower(strings.TrimSpace(hookName))
	}
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join("..", "..", path)); err == nil {
		return true
	}
	return false
}

// nativePreToolUseBlockMap exposes harness-native gate facts to installed-hook authority reporting.
func nativePreToolUseBlockMap(reg harness.Registry) map[string]bool {
	out := map[string]bool{}
	for name, h := range reg.Harnesses {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = h.Capabilities.PreToolUseBlock
	}
	return out
}

// cmdOrient assembles ContextPack an agent orients from. The excluded list records what was not loaded and why.
func cmdOrient(args []string) {
	homePath, err := home.Resolve("")
	die(err)
	// Resolve the configured memory-engine profile (e.g. breadth → COMPOUND_MEM_ENGINE_BIN) so the
	// ContextPack's ambient recall engages the SAME owned engine the operator's `memory query` uses.
	// Without this the profile is resolved only in the `memory` dispatch and the agent-facing recall
	// path silently stays lexical — the "landed but dormant" trap.
	restoreEngine, err := configureMemoryEngineFromHome(homePath, "")
	die(err)
	defer restoreEngine()
	if hasFlag(args, "--operator") {
		cmdOrientOperator(homePath, args)
		return
	}
	in, err := parseOrientArgsWithHome(args, homePath)
	die(err)
	if in.Harness == "" {
		fmt.Fprintln(os.Stderr, "usage: compound orient --harness <h> [--cwd <dir>] [--task <t>] [--repo-kind <k>] [--mode <m>] [--topic <topic-slug>]")
		os.Exit(2)
	}
	now := time.Now()
	ec, err := contract.CompileWithRuntime(in, orientPolicy(), homePath, now)
	die(err)
	_, err = contract.RecordEffectiveContract(homePath, ec)
	die(err)
	pack, err := contract.PackWithCapabilities(in, orientPolicy(), homePath, now)
	die(err)
	if pack.IDs.ContractID != ec.ID {
		die(fmt.Errorf("context pack contract_id %q does not match effective contract %q", pack.IDs.ContractID, ec.ID))
	}
	_, err = contract.RecordContextPack(homePath, pack)
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
		report := home.Doctor(resolve())
		if hasFlag(args, "--json") {
			printJSON(buildHomeDoctorReport(report))
			return
		}
		printHomeDoctor(report)
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
	case "archive-history":
		if hasUsageFlag(args) {
			fmt.Println("usage: compound home archive-history [--kind work|lease|review|comparison|create-plan|memory-candidate] [--path rel] [--dry-run]")
			return
		}
		report, err := home.ArchiveOperationalHistory(resolve(), home.OperationalArchiveOptions{
			DryRun: hasFlag(args, "--dry-run"),
			Kinds:  valuesFlag(args, "--kind"),
			Paths:  valuesFlag(args, "--path"),
		}, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "home archive-history:", err)
			os.Exit(1)
		}
		printHomeOperationalArchiveReport("archive-history", report)
	case "restore-history":
		if hasUsageFlag(args) {
			fmt.Println("usage: compound home restore-history (--all|--id id[,id...]) [--dry-run]")
			return
		}
		report, err := home.RestoreOperationalArchive(resolve(), home.OperationalRestoreOptions{
			DryRun: hasFlag(args, "--dry-run"),
			All:    hasFlag(args, "--all"),
			IDs:    valuesFlag(args, "--id"),
		}, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "home restore-history:", err)
			os.Exit(1)
		}
		printHomeOperationalArchiveReport("restore-history", report)
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
		fmt.Fprintln(os.Stderr, "usage: compound home <init|doctor|where|backup|export|verify|restore|archive-history|restore-history|compact> [--home <path>] [--local|--global]; archive-history [--kind work|lease|review|comparison|create-plan|memory-candidate] [--path rel] [--dry-run]")
		os.Exit(2)
	}
}

type editableSurfaceEntryReport struct {
	Surface       string `json:"surface"`
	Description   string `json:"description,omitempty"`
	ProposalClass string `json:"proposal_class,omitempty"`
	Status        string `json:"status"`
}

type editableSurfacesReport struct {
	AuthorityRef     string                       `json:"authority_ref"`
	AuthorityPath    string                       `json:"authority_path"`
	Source           string                       `json:"source"`
	Installed        bool                         `json:"installed"`
	Valid            bool                         `json:"valid"`
	NothingEditable  bool                         `json:"nothing_editable"`
	HardDenyCore     []string                     `json:"hard_deny_core"`
	Entries          []editableSurfaceEntryReport `json:"entries,omitempty"`
	EntryCount       int                          `json:"entry_count"`
	ActiveCount      int                          `json:"active_count"`
	ValidationErrors []string                     `json:"validation_errors,omitempty"`
}

// buildEditableSurfacesReport mirrors the autonomy-halt report shape (authority_ref,
// authority_path, source, installed, ...) so both governance switches read alike.
func buildEditableSurfacesReport(reg policy.EditableSurfaces, source, authorityPath string) editableSurfacesReport {
	report := editableSurfacesReport{
		AuthorityRef:     policy.EditableSurfacesAuthorityRef,
		AuthorityPath:    authorityPath,
		Source:           source,
		Installed:        source != "unset",
		Valid:            reg.Valid(),
		NothingEditable:  reg.NothingEditable(),
		HardDenyCore:     policy.HardDenyCoreSurfaces(),
		ActiveCount:      reg.ActiveCount(),
		ValidationErrors: reg.ValidationErrors,
	}
	for _, e := range reg.Entries {
		report.Entries = append(report.Entries, editableSurfaceEntryReport{
			Surface:       e.Surface,
			Description:   e.Description,
			ProposalClass: e.ProposalClass,
			Status:        e.Status,
		})
	}
	report.EntryCount = len(report.Entries)
	return report
}

// homeDoctorGovernanceReport co-locates the two policy-owned governance switches read live
// from COMPOUND_HOME/config: the autonomy kill-switch and the editable-surfaces registry.
type homeDoctorGovernanceReport struct {
	AutonomyHalt     homeDoctorAutonomyHaltState     `json:"autonomy_halt"`
	EditableSurfaces homeDoctorEditableSurfacesState `json:"editable_surfaces"`
}

type homeDoctorAutonomyHaltState struct {
	Installed          bool `json:"installed"`
	GlobalHalt         bool `json:"global_halt"`
	DisabledClassCount int  `json:"disabled_class_count"`
}

type homeDoctorEditableSurfacesState struct {
	Installed        bool `json:"installed"`
	Valid            bool `json:"valid"`
	NothingEditable  bool `json:"nothing_editable"`
	EntryCount       int  `json:"entry_count"`
	ActiveCount      int  `json:"active_count"`
	ValidationErrors int  `json:"validation_errors"`
}

func buildHomeGovernanceReport(homePath string) homeDoctorGovernanceReport {
	var gov homeDoctorGovernanceReport
	if halt, source, err := policy.LoadAutonomyHalt(homePath); err == nil {
		gov.AutonomyHalt = homeDoctorAutonomyHaltState{
			Installed:          source != "unset",
			GlobalHalt:         halt.Global,
			DisabledClassCount: len(halt.DisabledClasses),
		}
	}
	if reg, source, err := policy.LoadEditableSurfaces(homePath); err == nil {
		gov.EditableSurfaces = homeDoctorEditableSurfacesState{
			Installed:        source != "unset",
			Valid:            reg.Valid(),
			NothingEditable:  reg.NothingEditable(),
			EntryCount:       len(reg.Entries),
			ActiveCount:      reg.ActiveCount(),
			ValidationErrors: len(reg.ValidationErrors),
		}
	}
	return gov
}

func printHomeGovernanceDoctor(homePath string) {
	gov := buildHomeGovernanceReport(homePath)
	fmt.Printf("autonomy_halt: installed=%t global=%t disabled_classes=%d\n",
		gov.AutonomyHalt.Installed, gov.AutonomyHalt.GlobalHalt, gov.AutonomyHalt.DisabledClassCount)
	fmt.Printf("editable_surfaces: installed=%t valid=%t nothing_editable=%t entries=%d active=%d validation_errors=%d\n",
		gov.EditableSurfaces.Installed, gov.EditableSurfaces.Valid, gov.EditableSurfaces.NothingEditable,
		gov.EditableSurfaces.EntryCount, gov.EditableSurfaces.ActiveCount, gov.EditableSurfaces.ValidationErrors)
}

func homeOKStr(b bool) string {
	if b {
		return "ok"
	}
	return "MISSING"
}

func homeCleanStr(b bool) string {
	if b {
		return "ok"
	}
	return "DIRTY"
}

type homeDoctorJSONReport struct {
	SchemaVersion              string                     `json:"schema_version"`
	Home                       string                     `json:"home"`
	Kind                       string                     `json:"kind,omitempty"`
	ManifestOK                 bool                       `json:"manifest_ok"`
	DirsOK                     int                        `json:"dirs_ok"`
	DirsTotal                  int                        `json:"dirs_total"`
	InsideGitRepo              bool                       `json:"inside_git_repo"`
	GeneratedStateInRepoSource bool                       `json:"generated_state_in_repo_source"`
	OperationalHygiene         homeDoctorHygieneReport    `json:"operational_hygiene"`
	OperationalHygieneActions  []homeDoctorHygieneAction  `json:"operational_hygiene_actions,omitempty"`
	BackupWarnings             []home.CheckItem           `json:"backup_warnings,omitempty"`
	Governance                 homeDoctorGovernanceReport `json:"governance"`
	Setup                      home.SetupReport           `json:"setup"`
	SetupOK                    bool                       `json:"setup_ok"`
	Verdict                    string                     `json:"verdict"`
}

type homeDoctorHygieneReport struct {
	OK                   bool     `json:"ok"`
	TerminalHotWorkCount int      `json:"terminal_hot_work_count"`
	StaleLeaseCount      int      `json:"stale_lease_count"`
	TerminalHotWork      []string `json:"terminal_hot_work,omitempty"`
	StaleLeases          []string `json:"stale_leases,omitempty"`
}

type homeDoctorHygieneAction struct {
	ID                 string   `json:"id"`
	Kind               string   `json:"kind"`
	Title              string   `json:"title"`
	Command            []string `json:"command"`
	DryRunCommand      []string `json:"dry_run_command,omitempty"`
	ExpectedValidation []string `json:"expected_validation,omitempty"`
	StopConditions     []string `json:"stop_conditions,omitempty"`
	Boundary           string   `json:"boundary"`
	Proof              string   `json:"proof"`
}

func buildHomeDoctorReport(r home.Report) homeDoctorJSONReport {
	smell := r.InsideGitRepo && r.Manifest.HomeKind != "local"
	operational := homeDoctorHygieneReport{OK: r.HygieneOK}
	if hygiene, err := home.OperationalHygiene(r.Home); err == nil {
		operational.TerminalHotWork = append([]string{}, hygiene.TerminalHotWork...)
		operational.StaleLeases = append([]string{}, hygiene.StaleLeases...)
	}
	operational.TerminalHotWorkCount = len(operational.TerminalHotWork)
	operational.StaleLeaseCount = len(operational.StaleLeases)
	verdict := "ok"
	if !r.OK || smell {
		verdict = "fail"
	}
	return homeDoctorJSONReport{
		SchemaVersion:              "compound.home_doctor_report.v0.1",
		Home:                       r.Home,
		Kind:                       r.Manifest.HomeKind,
		ManifestOK:                 r.ManifestOK,
		DirsOK:                     r.DirsOK,
		DirsTotal:                  len(home.Dirs),
		InsideGitRepo:              r.InsideGitRepo,
		GeneratedStateInRepoSource: smell,
		OperationalHygiene:         operational,
		OperationalHygieneActions:  buildHomeDoctorHygieneActions(operational),
		BackupWarnings:             r.BackupWarnings,
		Governance:                 buildHomeGovernanceReport(r.Home),
		Setup:                      r.Setup,
		SetupOK:                    r.SetupOK,
		Verdict:                    verdict,
	}
}

func buildHomeDoctorHygieneActions(h homeDoctorHygieneReport) []homeDoctorHygieneAction {
	var actions []homeDoctorHygieneAction
	if h.TerminalHotWorkCount > 0 {
		actions = append(actions, homeDoctorHygieneAction{
			ID:            "archive-terminal-work",
			Kind:          "operational_hygiene_repair",
			Title:         "Archive terminal WorkEnvelope history",
			Command:       []string{"compound", "home", "archive-history", "--kind", "work"},
			DryRunCommand: []string{"compound", "home", "archive-history", "--kind", "work", "--dry-run"},
			ExpectedValidation: []string{
				"terminal_hot_work_count becomes 0",
				"receipts, transitions, and archive index remain resolvable",
				"held/open WorkEnvelope state remains visible in workbench",
			},
			StopConditions: []string{
				"dry-run includes non-terminal work",
				"archive-history reports unexpected kind",
				"workbench held/open work would be cleared",
			},
			Boundary: "reversible COMPOUND_HOME operational archive only; no held/open work clearing, no receipts/transitions deletion, no external side effects",
			Proof:    "home doctor --json reports terminal_hot_work_count=0 and workbench still shows held valves after archive-history",
		})
	}
	if h.StaleLeaseCount > 0 {
		actions = append(actions, homeDoctorHygieneAction{
			ID:            "archive-stale-leases",
			Kind:          "operational_hygiene_repair",
			Title:         "Archive stale WorkEnvelope leases",
			Command:       []string{"compound", "home", "archive-history", "--kind", "lease"},
			DryRunCommand: []string{"compound", "home", "archive-history", "--kind", "lease", "--dry-run"},
			ExpectedValidation: []string{
				"stale_lease_count becomes 0",
				"leases for live non-terminal work remain hot",
				"held WorkEnvelope state remains visible in workbench",
			},
			StopConditions: []string{
				"dry-run includes a lease for live non-terminal work",
				"archive-history reports unexpected kind",
				"workbench held/open work would be cleared",
			},
			Boundary: "reversible COMPOUND_HOME lease archive only; no held/open work clearing, no work state mutation, no external side effects",
			Proof:    "home doctor --json reports stale_lease_count=0 and workbench still shows held valves after archive-history",
		})
	}
	return actions
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
	fmt.Printf("operational_hygiene: %s\n", homeCleanStr(r.HygieneOK))
	for _, item := range r.Hygiene {
		fmt.Printf("%s: %s\n", item.Name, homeCleanStr(item.OK))
	}
	for _, item := range r.BackupWarnings {
		fmt.Printf("%s: %s\n", item.Name, homeCleanStr(item.OK))
	}
	for _, action := range buildHomeDoctorReport(r).OperationalHygieneActions {
		fmt.Printf("hygiene_action: %s kind=%s\n", action.ID, action.Kind)
		fmt.Printf("  command: %s\n", strings.Join(action.Command, " "))
		if len(action.DryRunCommand) > 0 {
			fmt.Printf("  dry_run: %s\n", strings.Join(action.DryRunCommand, " "))
		}
		if len(action.ExpectedValidation) > 0 {
			fmt.Printf("  expected_validation: %s\n", strings.Join(action.ExpectedValidation, "; "))
		}
		if len(action.StopConditions) > 0 {
			fmt.Printf("  stop_conditions: %s\n", strings.Join(action.StopConditions, "; "))
		}
		fmt.Printf("  boundary: %s\n", action.Boundary)
		fmt.Printf("  proof: %s\n", action.Proof)
	}
	fmt.Printf("setup: %s\n", homeOKStr(r.SetupOK))
	fmt.Printf("setup.install_dir: %s\n", r.Setup.InstallDir)
	if r.Setup.RequiredGoVersion != "" {
		fmt.Printf("setup.required_go: %s\n", r.Setup.RequiredGoVersion)
	}
	for _, item := range r.Setup.Checks {
		fmt.Printf("%s: %s — %s\n", item.Name, homeOKStr(item.OK), item.Detail)
		if !item.OK && item.Fix != "" {
			fmt.Printf("%s.fix: %s\n", item.Name, item.Fix)
		}
	}
	printHomeGovernanceDoctor(r.Home)
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

func printHomeOperationalArchiveReport(action string, r home.OperationalArchiveReport) {
	fmt.Printf("# home %s\n", action)
	fmt.Printf("- home: %s\n", r.Home)
	fmt.Printf("- dry_run: %v\n", r.DryRun)
	if r.ArchivedAt != "" {
		fmt.Printf("- archived_at: %s\n", r.ArchivedAt)
	}
	if r.RestoredAt != "" {
		fmt.Printf("- restored_at: %s\n", r.RestoredAt)
	}
	if r.ArchivedCount > 0 {
		fmt.Printf("- archived: %d\n", r.ArchivedCount)
	}
	if r.RestoredCount > 0 {
		fmt.Printf("- restored: %d\n", r.RestoredCount)
	}
	for _, kind := range sortedMapKeys(r.Counts) {
		fmt.Printf("- %s: %d\n", kind, r.Counts[kind])
	}
	for _, entry := range r.Entries {
		fmt.Printf("- entry: %s %s %s -> %s\n", entry.ID, entry.Kind, entry.OriginalPath, entry.ArchivePath)
	}
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
		fmt.Fprintln(os.Stderr, "usage: compound authority import [registry.yaml] | path <path-authority.yaml> | path-registry <os-path-authority.yaml> | git <repo-path> | dirty-plan <repo-path> [--work-id id] | import-os-absorption <matrix.json> | os-absorption [--json] | os-absorption-doctor [--json] | import-job-roster <cron-jobs.json> | import-job-token-governance <cron-token-usage.json> | job-roster-authority [--json] | job-health-plan | job-health-review <review.yaml> | resolve <token> | <entity>")
		os.Exit(2)
	}
	store, homePath := openMemStore()
	if args[0] == "import-os-absorption" {
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority import-os-absorption <matrix.json>")
			os.Exit(2)
		}
		report, err := authority.ImportOSAbsorptionMatrix(homePath, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority import-os-absorption:", err)
			os.Exit(1)
		}
		printJSON(report)
		return
	}
	if args[0] == "os-absorption" {
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound authority os-absorption [--json]")
			os.Exit(2)
		}
		matrix, err := authority.LoadOSAbsorptionMatrix(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority os-absorption:", err)
			os.Exit(1)
		}
		if len(args) == 2 && args[1] == "--json" {
			printJSON(matrix)
			return
		}
		absorbed := 0
		pending := 0
		for _, row := range matrix.Rows {
			switch row.Status {
			case "absorbed", "wrapper", "evidence_only", "retired":
				absorbed++
			case "pending":
				pending++
			}
		}
		fmt.Printf("os absorption authority: %s\n", matrix.AuthorityRef)
		fmt.Printf("rows: %d absorbed=%d pending=%d\n", len(matrix.Rows), absorbed, pending)
		fmt.Printf("source: %s %s\n", matrix.SourceMatrixPath, matrix.SourceMatrixRevision)
		return
	}
	if args[0] == "os-absorption-doctor" {
		if len(args) != 1 && !(len(args) == 2 && args[1] == "--json") {
			fmt.Fprintln(os.Stderr, "usage: compound authority os-absorption-doctor [--json]")
			os.Exit(2)
		}
		report, err := authority.OSAbsorptionDoctor(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority os-absorption-doctor:", err)
			os.Exit(1)
		}
		if len(args) == 2 && args[1] == "--json" {
			printJSON(report)
			if report.Status != "ok" {
				os.Exit(1)
			}
			return
		}
		fmt.Printf("os absorption doctor: %s\n", report.Status)
		fmt.Printf("rows: %d auto_proved=%d pending_with_gate=%d missing=%d failed_auto=%d\n", report.RowCount, report.AutoProofCount, report.PendingWithGateCount, report.MissingRequiredCount, report.FailedAutoProofCount)
		fmt.Printf("proof split: machine_proven=%d asserted=%d pending=%d failed_proof=%d (absorbed_count=%d is status-string only, not proof)\n", report.MachineProvenCount, report.AssertedCount, report.PendingCount, report.FailedProofCount, report.AbsorbedCount)
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
		return
	}
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
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority git <repo-path> [--json]")
			os.Exit(2)
		}
		jsonOut := hasFlag(args[2:], "--json")
		for _, arg := range args[2:] {
			if arg != "--json" {
				fmt.Fprintln(os.Stderr, "usage: compound authority git <repo-path> [--json]")
				os.Exit(2)
			}
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
		if jsonOut {
			out := struct {
				SchemaVersion   string `json:"schema_version"`
				Status          string `json:"status"`
				AuthorityRef    string `json:"authority_ref"`
				EntityRef       string `json:"entity_ref"`
				GitRoot         string `json:"git_root"`
				Branch          string `json:"branch,omitempty"`
				Head            string `json:"head,omitempty"`
				Remote          string `json:"remote,omitempty"`
				Upstream        string `json:"upstream,omitempty"`
				Ahead           int    `json:"ahead"`
				Behind          int    `json:"behind"`
				Staged          int    `json:"staged"`
				Unstaged        int    `json:"unstaged"`
				Untracked       int    `json:"untracked"`
				ChangedPaths    int    `json:"changed_paths"`
				CandidateCount  int    `json:"candidate_count"`
				AuthorityClaims int    `json:"authority_claims"`
				EvidenceRef     string `json:"evidence_ref"`
			}{
				SchemaVersion:   authority.SourceControlSchemaVersion,
				Status:          report.Status,
				AuthorityRef:    "compound://ledger/source-control",
				EntityRef:       report.EntityRef,
				GitRoot:         report.GitRoot,
				Branch:          report.Branch,
				Head:            report.Head,
				Remote:          report.Remote,
				Upstream:        report.Upstream,
				Ahead:           report.Ahead,
				Behind:          report.Behind,
				Staged:          report.Staged,
				Unstaged:        report.Unstaged,
				Untracked:       report.Untracked,
				ChangedPaths:    report.ChangedPaths,
				CandidateCount:  report.CandidateCount,
				AuthorityClaims: promoted,
				EvidenceRef:     report.EvidenceRef,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", " ")
			if err := enc.Encode(out); err != nil {
				fmt.Fprintln(os.Stderr, "authority git:", err)
				os.Exit(1)
			}
			return
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
			fmt.Fprintln(os.Stderr, "usage: compound authority dirty-plan <repo-path> [--work-id id] [--json]")
			os.Exit(2)
		}
		jsonOut := hasFlag(args[2:], "--json")
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--json":
			case "--work-id":
				if i+1 >= len(args) {
					fmt.Fprintln(os.Stderr, "usage: compound authority dirty-plan <repo-path> [--work-id id] [--json]")
					os.Exit(2)
				}
				i++
			default:
				fmt.Fprintln(os.Stderr, "usage: compound authority dirty-plan <repo-path> [--work-id id] [--json]")
				os.Exit(2)
			}
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
		if jsonOut {
			printJSON(struct {
				SchemaVersion string                                 `json:"schema_version"`
				Status        string                                 `json:"status"`
				AuthorityRef  string                                 `json:"authority_ref"`
				ID            string                                 `json:"id"`
				RepoID        string                                 `json:"repo_id"`
				SourceStatus  string                                 `json:"source_status"`
				ChangedPaths  int                                    `json:"changed_paths"`
				Staged        int                                    `json:"staged"`
				Unstaged      int                                    `json:"unstaged"`
				Untracked     int                                    `json:"untracked"`
				Plan          authority.SourceControlReviewPlan      `json:"plan"`
				Paths         authority.SourceControlReviewPlanPaths `json:"paths"`
			}{
				SchemaVersion: authority.SourceControlReviewPlanSchemaVersion,
				Status:        plan.Status,
				AuthorityRef:  "compound://reviews/source-control-cleanup",
				ID:            plan.ID,
				RepoID:        plan.RepoID,
				SourceStatus:  plan.SourceStatus,
				ChangedPaths:  plan.ChangedPaths,
				Staged:        plan.Staged,
				Unstaged:      plan.Unstaged,
				Untracked:     plan.Untracked,
				Plan:          plan,
				Paths:         paths,
			})
			return
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
		fmt.Printf("- review_yaml: %s\n", paths.ReviewYAMLPath)
		return
	}
	if args[0] == "dirty-review" {
		jsonOut := hasFlag(args[1:], "--json")
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: compound authority dirty-review <review.yaml> [--json]")
			os.Exit(2)
		}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--json":
			default:
				if i != 1 {
					fmt.Fprintln(os.Stderr, "usage: compound authority dirty-review <review.yaml> [--json]")
					os.Exit(2)
				}
			}
		}
		review, err := authority.LoadSourceControlReview(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority dirty-review:", err)
			os.Exit(1)
		}
		report, err := authority.RecordSourceControlReview(homePath, review, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "authority dirty-review:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Printf("# source-control dirty-review %s\n", report.ID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- plan: %s\n", report.PlanID)
		fmt.Printf("- review: %s\n", report.ReviewID)
		fmt.Printf("- repo: %s\n", report.RepoID)
		fmt.Printf("- decision: %s\n", report.Decision)
		if report.NextAction != "" {
			fmt.Printf("- next_action: %s\n", report.NextAction)
		}
		fmt.Printf("- evidence_refs: %d\n", len(report.EvidenceRefs))
		fmt.Printf("- reviewed: %s\n", report.ReviewPath)
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
	return "usage: compound product authority [--json] | import <product.yaml>|--project-contract <project.yaml> [--release <release.yaml>] | signal <signal.yaml>|--asc-datapoints <json> --product <id> --app-id <asc_id> [--metric <metric>]|--asc-sales-series <jsonl> --product <id> --app-id <asc_id> --before <range> --after <range> --change-event <text> [--metric <metric>]|--gsc-report <json> --product <id> --site <site> [--metric <metric>]|--ga4-report <json> --product <id> --property <id> [--metric <metric>]|--adsense-report <json> --product <id> --account <id> [--metric <metric>] | loops [--product <id>] [--json] [--recent N] [--stale-days D] | canonical-metrics <json> --product <id> | change-stream <candidates.md> | hypothesis-plan --candidate <candidate_id> [--target <file>] | observation-verdict-plan --verdict <panel-verdict.json> [--target <file>] | observation-verdict-review <review.yaml> | review-plan --hypothesis <hypothesis_id> --signal <signal_id> [--target <file>] | action-plan --product <product_id> --card <card_id> [--target <file>] | action-work-plan --reviewed <reviewed.json> [--actor A] [--json] | source-issue-plan --product <product_id> --issue <issue_id> [--target <file>] | outbound-dry-run-plan --reviewed-receipt <reviewed.json> [--target <file>] | review <review.yaml> | action-review <review.yaml> | source-issue-review <review.yaml> | outbound-dry-run-review <review.yaml> | timeline <product_id> | doctor <product_id> [--writeback]"
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
	case "loops":
		cmdProductLoops(args[1:], homePath)
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
		if tryProductGoogleSignal(args[1:], homePath, store) {
			return
		}
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
	case "observation-verdict-plan":
		verdictPath := flagValue(args[1:], "--verdict")
		targetPath := flagValue(args[1:], "--target")
		if verdictPath == "" {
			fmt.Fprintln(os.Stderr, "usage: compound product observation-verdict-plan --verdict <panel-verdict.json> [--target <file>]")
			os.Exit(2)
		}
		plan, err := product.BuildObservationVerdictPlan(homePath, verdictPath, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product observation-verdict-plan:", err)
			os.Exit(1)
		}
		paths, err := product.RecordObservationVerdictPlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "product observation-verdict-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# product observation-verdict-plan %s\n\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- product: %s\n", plan.ProductID)
		fmt.Printf("- verdict: %s\n", plan.VerdictID)
		fmt.Printf("- panel_decision: %s\n", plan.PanelDecision)
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
	case "action-plan":
		productID := flagValue(args[1:], "--product")
		cardID := flagValue(args[1:], "--card")
		targetPath := flagValue(args[1:], "--target")
		if productID == "" || cardID == "" {
			fmt.Fprintln(os.Stderr, "usage: compound product action-plan --product <product_id> --card <card_id> [--target <file>]")
			os.Exit(2)
		}
		plan, err := product.BuildActionPlan(homePath, productID, cardID, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product action-plan:", err)
			os.Exit(1)
		}
		paths, err := product.RecordActionPlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "product action-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# product action-plan %s\n\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- product: %s\n", plan.ProductID)
		fmt.Printf("- card: %s\n", plan.CardID)
		fmt.Printf("- card_kind: %s\n", plan.CardKind)
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
	case "outbound-dry-run-plan":
		reviewedReceiptPath := flagValue(args[1:], "--reviewed-receipt")
		targetPath := flagValue(args[1:], "--target")
		if reviewedReceiptPath == "" {
			fmt.Fprintln(os.Stderr, "usage: compound product outbound-dry-run-plan --reviewed-receipt <reviewed.json> [--target <file>]")
			os.Exit(2)
		}
		plan, err := product.BuildOutboundDryRunEvidencePlan(homePath, reviewedReceiptPath, targetPath, time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product outbound-dry-run-plan:", err)
			os.Exit(1)
		}
		paths, err := product.RecordOutboundDryRunEvidencePlan(homePath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "product outbound-dry-run-plan:", err)
			os.Exit(1)
		}
		fmt.Printf("# product outbound-dry-run-plan %s\n\n", plan.ID)
		fmt.Printf("- status: %s\n", plan.Status)
		fmt.Printf("- product: %s\n", plan.ProductID)
		fmt.Printf("- receipt: %s\n", plan.ReceiptID)
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
	case "observation-verdict-review":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound product observation-verdict-review <review.yaml>")
			os.Exit(2)
		}
		review, err := product.LoadObservationVerdictReview(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "product observation verdict review:", err)
			os.Exit(1)
		}
		report, err := product.ApplyObservationVerdictReview(homePath, store, review, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product observation verdict review:", err)
			os.Exit(1)
		}
		fmt.Printf("# product observation-verdict-review %s\n\n", report.ReviewID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- product: %s\n", report.ProductID)
		fmt.Printf("- verdict: %s\n", report.VerdictID)
		fmt.Printf("- decision: %s\n", report.Decision)
		fmt.Printf("- evidence: %s\n", report.EvidenceID)
		fmt.Printf("- evidence_refs: %d\n", len(report.EvidenceRefs))
		if report.HypothesisID != "" {
			fmt.Printf("- hypothesis: %s\n", report.HypothesisID)
		}
		if report.TransitionID != "" {
			fmt.Printf("- transition: %s\n", report.TransitionID)
		}
	case "action-review":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound product action-review <review.yaml>")
			os.Exit(2)
		}
		review, err := product.LoadActionReview(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "product action review:", err)
			os.Exit(1)
		}
		report, err := product.ApplyActionReview(homePath, review, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product action review:", err)
			os.Exit(1)
		}
		fmt.Printf("# product action-review %s\n\n", report.ReviewID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- product: %s\n", report.ProductID)
		fmt.Printf("- card: %s\n", report.CardID)
		fmt.Printf("- card_kind: %s\n", report.CardKind)
		fmt.Printf("- decision: %s\n", report.Decision)
		fmt.Printf("- evidence_refs: %d\n", len(report.EvidenceRefs))
		fmt.Printf("- reviewed: %s\n", report.ReviewedPath)
	case "action-work-plan":
		reviewedPath := flagValue(args[1:], "--reviewed")
		actor := flagValue(args[1:], "--actor")
		jsonOut := hasFlag(args[1:], "--json")
		if actor == "" {
			actor = "codex"
		}
		if reviewedPath == "" {
			fmt.Fprintln(os.Stderr, "usage: compound product action-work-plan --reviewed <reviewed.json> [--actor A] [--json]")
			os.Exit(2)
		}
		draft, err := product.BuildActionWorkDraft(homePath, reviewedPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "product action work-plan:", err)
			os.Exit(1)
		}
		workStore, err := work.Open(homePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "product action work-plan:", err)
			os.Exit(1)
		}
		plan, err := workStore.CreatePlan(work.CreateInput{
			WorkID:           draft.WorkID,
			Owner:            draft.WorkOwner,
			Actor:            actor,
			SuccessPredicate: draft.SuccessPredicate,
			DesiredOutcome:   draft.DesiredOutcome,
			NonGoals:         draft.NonGoals,
			EvidenceRefs:     draft.EvidenceRefs,
			State:            "proposed",
			Risk:             "tier2",
			Privacy:          "private",
		}, time.Now(), work.CreatePlanOptions{
			Actor:         actor,
			Capability:    "product.action_review",
			Action:        "plan_work_from_reviewed_product_action",
			MutationClass: "local_reversible",
			Reversibility: "easy",
			BlastRadius:   "local",
			TargetKind:    "product_action_review",
			TargetID:      draft.ReviewReportID,
			TargetPath:    reviewedPath,
			ActionContext: &work.WorkActionContext{
				Kind:               "product_action_review",
				SourceRef:          "compound://reviews/" + product.ProductActionReviewArea + "/" + filepath.Base(reviewedPath),
				SourceID:           draft.ReviewReportID,
				Title:              draft.CardTitle,
				ActionSummary:      draft.ActionSummary,
				Command:            draft.ActionCommand,
				RequiredInputs:     draft.RequiredInputs,
				ExpectedValidation: draft.ExpectedValidation,
				StopConditions:     draft.StopConditions,
				Boundary:           draft.Boundary,
				Proof:              draft.Proof,
				EvidenceRefs:       draft.EvidenceRefs,
			},
			Record: true,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "product action work-plan:", err)
			os.Exit(1)
		}
		out := struct {
			SchemaVersion string                  `json:"schema_version"`
			Status        string                  `json:"status"`
			ProductAction product.ActionWorkDraft `json:"product_action"`
			WorkPlan      work.CreatePlan         `json:"work_plan"`
		}{
			SchemaVersion: "compound.product_action_work_plan_report.v0.1",
			Status:        "recorded",
			ProductAction: draft,
			WorkPlan:      plan,
		}
		if jsonOut {
			printJSON(out)
			return
		}
		fmt.Printf("# product action-work-plan %s\n\n", draft.ID)
		fmt.Printf("- status: %s\n", out.Status)
		fmt.Printf("- product: %s\n", draft.ProductID)
		fmt.Printf("- review: %s\n", draft.ReviewReportID)
		fmt.Printf("- work: %s\n", plan.WorkID)
		fmt.Printf("- work_state: %s\n", plan.WorkState)
		fmt.Printf("- policy: %s allowed=%t\n", plan.Policy.Outcome, plan.Policy.Allowed)
		fmt.Printf("- plan: %s\n", plan.PlanPath)
		if draft.ActionSummary != "" {
			fmt.Printf("- action_summary: %s\n", draft.ActionSummary)
		}
		if len(draft.ActionCommand) > 0 {
			fmt.Printf("- action_command: %s\n", strings.Join(draft.ActionCommand, " "))
		}
		fmt.Printf("- proof: %s\n", draft.Proof)
	case "outbound-dry-run-review":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: compound product outbound-dry-run-review <review.yaml>")
			os.Exit(2)
		}
		review, err := product.LoadOutboundDryRunEvidenceReview(args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "product outbound dry-run review:", err)
			os.Exit(1)
		}
		report, err := product.ApplyOutboundDryRunEvidenceReview(homePath, store, review, args[1], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "product outbound dry-run review:", err)
			os.Exit(1)
		}
		fmt.Printf("# product outbound-dry-run-review %s\n\n", report.ReviewID)
		fmt.Printf("- status: %s\n", report.Status)
		fmt.Printf("- product: %s\n", report.ProductID)
		fmt.Printf("- receipt: %s\n", report.ReceiptID)
		fmt.Printf("- decision: %s\n", report.Decision)
		fmt.Printf("- evidence: %s\n", report.EvidenceID)
		fmt.Printf("- evidence_refs: %d\n", len(report.EvidenceRefs))
		if report.HypothesisID != "" {
			fmt.Printf("- hypothesis: %s\n", report.HypothesisID)
		}
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
		report, err := product.DoctorForRegisteredProduct(homePath, store, args[1], time.Now())
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
		for _, stale := range report.ASCSignalStaleness {
			fmt.Printf("- asc_signal_stale: %s last=%s age_hours=%d\n", stale.LastSignalID, stale.LastObservedAt, stale.AgeHours)
			fmt.Printf("  fix: %s\n", stale.FixCommand)
		}
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
		jsonOut := hasFlag(args[1:], "--json")
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
		if jsonOut {
			out := struct {
				SchemaVersion   string                            `json:"schema_version"`
				Status          string                            `json:"status"`
				AuthorityRef    string                            `json:"authority_ref"`
				RecordCount     int                               `json:"record_count"`
				Eligible        int                               `json:"eligible"`
				Covered         int                               `json:"covered"`
				Missing         int                               `json:"missing"`
				CountsByKind    map[string]int                    `json:"counts_by_kind"`
				CountsByStatus  map[string]int                    `json:"counts_by_status"`
				EligibleSources []string                          `json:"eligible_sources,omitempty"`
				MissingSources  []string                          `json:"missing_sources,omitempty"`
				Records         []decisions.SourceAuthorityRecord `json:"records,omitempty"`
			}{
				SchemaVersion:   "compound.decision_source_authority_coverage.v0.1",
				Status:          report.Status,
				AuthorityRef:    "compound://registries/decision-source-authority",
				RecordCount:     len(report.Records),
				Eligible:        len(report.Eligible),
				Covered:         len(report.Eligible) - len(report.Missing),
				Missing:         len(report.Missing),
				CountsByKind:    report.CountsByKind,
				CountsByStatus:  report.CountsByStatus,
				EligibleSources: report.Eligible,
				MissingSources:  report.Missing,
				Records:         report.Records,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", " ")
			if err := enc.Encode(out); err != nil {
				fmt.Fprintln(os.Stderr, "decisions authority:", err)
				os.Exit(1)
			}
			if len(report.Eligible) > 0 && len(report.Missing) > 0 {
				os.Exit(1)
			}
			return
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
	case "record":
		jsonOut := hasFlag(args[1:], "--json")
		in := decisions.RecordInput{
			Kind:          value("--kind"),
			ID:            value("--id"),
			Title:         value("--title"),
			ReviewedBy:    value("--reviewed-by"),
			Scope:         value("--scope"),
			OwnerGlobs:    values("--owner-glob"),
			Keywords:      values("--keyword"),
			Domains:       values("--domain"),
			Falsifier:     value("--falsifier"),
			ReviewTrigger: value("--review-trigger"),
			UndoOf:        value("--undo-of"),
			Now:           time.Now(),
		}
		report, err := decisions.Record(store, homePath, in)
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions record:", err)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(report)
			return
		}
		fmt.Printf("# decisions record %s\n\n", report.Import.Lifecycle.ID)
		fmt.Printf("- path: %s\n", report.Path)
		fmt.Printf("- kind: %s\n", report.Import.Lifecycle.Kind)
		fmt.Printf("- status: %s\n", report.Import.Lifecycle.Status)
		fmt.Printf("- scope: %s\n", report.Import.Lifecycle.Scope)
		fmt.Printf("- reviewed_by: %s\n", report.Import.Lifecycle.ReviewedBy)
		fmt.Printf("- verdict: %s\n", report.Import.Verdict)
		fmt.Printf("- claim: %s\n", report.Import.ClaimID)
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
	case "repair-review":
		cmdRuntimeRepairReview(args[1:])
	default:
		fmt.Fprintln(os.Stderr, runtimeUsage())
		os.Exit(2)
	}
}

func runtimeUsage() string {
	return "usage: compound runtime doctor [--project-contract] <yaml> [--live] [--writeback] | runtime repair-plan [--project-contract] <yaml> [--live] | runtime repair-review <review.yaml>"
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
	fmt.Printf("- review_yaml: %s\n", paths.ReviewYAMLPath)
}

func cmdRuntimeRepairReview(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: compound runtime repair-review <review.yaml>")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime repair-review:", err)
		os.Exit(1)
	}
	review, err := runtimebinding.LoadRepairReview(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime repair-review:", err)
		os.Exit(1)
	}
	report, err := runtimebinding.RecordRepairReview(homePath, review, args[0], time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime repair-review:", err)
		os.Exit(1)
	}
	fmt.Printf("# runtime repair-review %s\n\n", report.ID)
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- plan: %s\n", report.PlanID)
	fmt.Printf("- service: %s\n", report.ServiceID)
	if report.Owner != "" {
		fmt.Printf("- owner: %s\n", report.Owner)
	}
	fmt.Printf("- decision: %s\n", report.Decision)
	if report.NextAction != "" {
		fmt.Printf("- next_action: %s\n", report.NextAction)
	}
	fmt.Printf("- reviewed: %s\n", report.ReviewPath)
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
	if paths.ReviewYAMLPath != "" {
		fmt.Printf("- review_yaml: %s\n", paths.ReviewYAMLPath)
	}
	fmt.Printf("- artifact: %s\n", paths.PlanPath)
}

func cmdProjectionRepairReview(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: compound projection repair-review <review.yaml>")
		os.Exit(2)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection repair-review:", err)
		os.Exit(1)
	}
	review, err := projection.LoadRegenerationReview(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection repair-review:", err)
		os.Exit(1)
	}
	report, err := projection.RecordRegenerationReview(homePath, review, args[0], time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "projection repair-review:", err)
		os.Exit(1)
	}
	fmt.Printf("# projection repair-review %s\n\n", report.ID)
	fmt.Printf("- status: %s\n", report.Status)
	fmt.Printf("- plan: %s\n", report.PlanID)
	fmt.Printf("- projection: %s\n", report.ProjectionID)
	fmt.Printf("- view: %s\n", report.ViewRef)
	fmt.Printf("- decision: %s\n", report.Decision)
	if report.NextAction != "" {
		fmt.Printf("- next_action: %s\n", report.NextAction)
	}
	fmt.Printf("- reviewed: %s\n", report.ReviewPath)
}

func projectionUsage() string {
	return "usage: compound projection doctor <freshness.yaml> [--writeback] [--source-observe] [--record-context --repo-kind K] | projection dashboard-read-model <read-model.json> [--max-age-hours N] [--writeback] [--source-observe] [--record-context --repo-kind K] | projection repair-plan <freshness.yaml> | projection repair-review <review.yaml>"
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
	if args[0] == "repair-review" {
		cmdProjectionRepairReview(args[1:])
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
