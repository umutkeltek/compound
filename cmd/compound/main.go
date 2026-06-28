// Command compound is the CLI for the compound information kernel / transition substrate.
// Built envelope-first: the proven spine is P0-P6 first slice (home, envelopes, contract/orient, work,
// policy, harness/hook, memory ledger). Memory is one slice on top (P6, re-bound through the spine), not the
// system boundary. Legacy/prototype memory-era commands still exist below and will be migrated.
// Architecture: docs/architecture/{substrate,runtime,transition}.md, docs/roadmap/phases.md.
package main

import (
	"context"
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
	"compound/internal/capgraph"
	"compound/internal/contract"
	"compound/internal/decisions"
	"compound/internal/distill"
	"compound/internal/distiller"
	"compound/internal/engine"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/ledger"
	"compound/internal/mem"
	"compound/internal/memory"
	"compound/internal/pattern"
	"compound/internal/policy"
	"compound/internal/position"
	"compound/internal/recall"
	"compound/internal/reconcile"
	"compound/internal/scope"
	"compound/internal/work"
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
	case "distill":
		cmdDistill(os.Args[2:])
	case "scope":
		cmdScope(os.Args[2:])
	case "promote":
		cmdPromote(os.Args[2:])
	case "build":
		cmdBuild()
	case "reconcile":
		cmdReconcile(os.Args[2:])
	case "timeline":
		cmdTimeline(os.Args[2:])
	case "authority":
		cmdAuthority(os.Args[2:])
	case "decisions":
		cmdDecisions(os.Args[2:])
	case "recall":
		cmdRecall(os.Args[2:])
	case "pack":
		cmdPack(os.Args[2:])
	case "ledger":
		cmdLedger(os.Args[2:])
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
  compound distill <session.jsonl> [--model=M] [--ingest]   deep-distill -> candidate claims
  compound scope <session.jsonl> [--explain]                attribute a session to entities (no cwd)
  compound recall "<query>" [--ambient]   planner: positions + claims + breadth (ambient = promoted-only)
  compound promote <claim-id> --basis=<b>   candidate -> promoted (the only path to canon)
  compound build                derive current Positions + recurring Patterns from Claims
  compound reconcile [--entity=E]   alive memory: supersede contradicted/duplicate claims (LLM)
  compound timeline <entity>    how positions on this entity evolved (walks supersedes edges)
  compound authority import [f] | authority <project>   where projects live (from a registry)
  compound decisions import | decisions [--kind=K] | decisions doctor   one store, kinds: policy/doctrine/operational
  compound pack "<topic>"       breadth via the rented engine
  compound ledger stat          record counts per object kind
  compound home <init|doctor|where> [--home P] [--local]   the COMPOUND_HOME runtime body
  compound envelope <validate <file>...|kinds>             typed runtime envelopes
  compound contract compile --harness H --cwd D --task T   compile the EffectiveContract in force
  compound orient --harness H [--cwd D] [--task T]         the ContextPack to orient from (records excluded)
  compound work <create|show|list|claim|advance|close|act> one work_id: intent->context->action->receipt->transition
  compound policy evaluate <action-intent.json>            governance v0: ActionIntent -> PolicyDecision (rc 0/3/4)
  compound hook <name> <fixture.json>                      run a lifecycle hook over a fixture (capability-aware)
  compound harness <list|NAME>                             the HarnessRegistry: what each harness supports + limits
  compound transition show <id>                            inspect a Transition (or any envelope) under COMPOUND_HOME/events
  compound memory <candidate|adjudicate|recall|timeline|query|relate|graph|doctor>  P6: claim ledger + as-of recall + rented-engine breadth + task-shaped graph recall
  compound distiller run <trace.json>                      P6: trace -> RetrievedPriorState -> CandidateSet -> Critic -> Adjudicator (model extracts, code decides)
  compound capability <add|surface|observe|consume|list>   P8: capability graph — auto-surface (never auto-execute), learns from observations
`)
}

// cmdCapability is the P8 capability graph: surface relevant capabilities for a task (never
// execute them), and learn which capability fits which context from the distiller's observations.
func cmdCapability(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound capability <add|surface|observe|consume|list> ...")
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
		fmt.Fprintln(os.Stderr, "usage: compound capability <add|surface|observe|consume|list> ...")
		os.Exit(2)
	}
}

// cmdDistiller is the P6 distillation front-end: trace → prior state → candidate set → critic →
// adjudicator. Memory candidates flow through the mem ledger; everything else is candidate-first
// and never becomes canon here; self-improvement signals (eval/method/capability) never auto-apply.
func cmdDistiller(args []string) {
	if len(args) < 2 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: compound distiller run <trace.json>")
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
	data, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	res, err := distiller.Run(homePath, store, data, distiller.DefaultPolicy(), distiller.ReferenceExtractor{}, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("# distilled %s under policy %s — %d candidate(s)\n", res.TraceRef, res.Policy, len(res.Decisions))
	for _, d := range res.Decisions {
		extra := ""
		if d.ClaimID != "" {
			extra = " → claim " + d.ClaimID
		}
		fmt.Printf("%-22s %-14s %s%s\n", d.Kind, d.Verdict, d.Basis, extra)
	}
}

// cmdMemory is P6: the owned memory ledger re-bound through the spine. candidate-first
// (reject|park|promote), MVCC-by-fold (as-of recall), provenance + contradiction-over-delete.
func cmdMemory(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: compound memory <candidate <trace.json>|adjudicate <id> --verdict V|recall <entity> [--as-of N]|timeline <entity>|doctor>")
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
			fmt.Fprintln(os.Stderr, "usage: compound memory relate <from-entity> <relates|contradicts|depends_on> <to-entity> [--conf 0.8]")
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
	case "query":
		// breadth via the RENTED engine, relevance-gated by the ledger. With COMPOUND_ENGINE_BIN
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
		fmt.Fprintln(os.Stderr, "usage: compound memory <candidate|adjudicate|recall|timeline|doctor> ...")
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
	if homePath, err := home.Resolve(""); err == nil {
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
			Outcome:  flag("--outcome"),
			MoveType: flag("--move-type"),
			Summary:  flag("--summary"),
			Changed:  has("--changed"),
		}, time.Now())
		die(err)
		fmt.Printf("%s closed: %s  move_type=%s  receipt=%s  transition=%s\n",
			res.Work.ID, res.Work.State, res.Receipt.MoveType, res.Receipt.ID, res.Transition.ID)
		printJSON(res)
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
	kind, via := contract.ResolveRepoKind(repoKind, cwd)
	return contract.Input{Harness: harness, Cwd: cwd, Task: task, RepoKind: kind, RepoKindVia: via, Mode: mode}
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

// cmdContract compiles the EffectiveContract — the instructions/hooks/policies in force
// for a harness+repo+task (docs/architecture/contracts/README.md).
func cmdContract(args []string) {
	if len(args) == 0 || args[0] != "compile" {
		fmt.Fprintln(os.Stderr, "usage: compound contract compile --harness <h> --cwd <dir> --task <t> [--repo-kind <k>] [--mode <m>]")
		os.Exit(2)
	}
	in := parseOrientArgs(args[1:])
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
	in := parseOrientArgs(args)
	if in.Harness == "" {
		fmt.Fprintln(os.Stderr, "usage: compound orient --harness <h> [--cwd <dir>] [--task <t>] [--repo-kind <k>] [--mode <m>]")
		os.Exit(2)
	}
	printJSON(contract.Pack(in, orientPolicy(), time.Now()))
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

func ledgerRoot() string {
	if r := os.Getenv("COMPOUND_LEDGER"); r != "" {
		return r
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".compound", "ledger")
}

func scopeSuffix(primary string) string {
	if primary == "" {
		return ""
	}
	return " · primary=" + primary
}

func cmdRecall(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, `usage: compound recall "<query>"`)
		os.Exit(2)
	}
	var terms []string
	ambient := false
	for _, a := range args {
		if a == "--ambient" {
			ambient = true
			continue
		}
		terms = append(terms, a)
	}
	query := strings.Join(terms, " ")
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	plan := recall.MakePlan(query, scope.SeedEntities(), ambient)
	res, err := recall.Run(l, engine.NewConfigured(), plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recall: %v\n", err)
		os.Exit(1)
	}
	note := ""
	if plan.Entity != "" {
		note = " · entity=" + plan.Entity
	}
	if ambient {
		note += " · ambient(promoted-only)"
	}
	fmt.Printf("mode: %s%s\n\n", plan.Mode, note)
	if len(res.Positions) > 0 {
		fmt.Println("## Current positions")
		for _, p := range res.Positions {
			fmt.Printf("- [%s] %s/%s → active %s (%d in chain)\n", p.Status, p.EntitySlug, p.TopicSlug, p.ActiveClaimID, len(p.ClaimChain))
		}
		fmt.Println()
	}
	if len(res.Claims) > 0 {
		fmt.Println("## Owned claims (ledger)")
		for _, c := range res.Claims {
			fmt.Printf("- [%s] %s\n", c.Status, c.Object)
		}
		fmt.Println()
	}
	if len(res.Hits) > 0 {
		fmt.Println("## Breadth (engine)")
		for _, h := range res.Hits {
			fmt.Printf("- `%s` — %s\n", h.Slug, h.Snippet)
		}
	}
	if len(res.Positions) == 0 && len(res.Claims) == 0 && len(res.Hits) == 0 {
		fmt.Println("(nothing — zero_result_not_proof; check coverage)")
	}
}

func cmdBuild() {
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	claims, err := recall.LoadClaims(l)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build: %v\n", err)
		os.Exit(1)
	}
	positions := position.Build(claims)
	patterns := pattern.Build(claims)
	for _, p := range positions {
		if err := l.Append("positions", p); err != nil {
			fmt.Fprintf(os.Stderr, "build: %v\n", err)
			os.Exit(1)
		}
	}
	for _, p := range patterns {
		if err := l.Append("patterns", p); err != nil {
			fmt.Fprintf(os.Stderr, "build: %v\n", err)
			os.Exit(1)
		}
	}
	fmt.Printf("built %d positions, %d patterns from %d claims\n", len(positions), len(patterns), len(claims))
}

func cmdReconcile(args []string) {
	var entity string
	for _, a := range args {
		if strings.HasPrefix(a, "--entity=") {
			entity = strings.TrimPrefix(a, "--entity=")
		}
	}
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	sup, dups, err := reconcile.Reconcile(context.Background(), l, "", entity)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconcile: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("reconciled: %d superseded, %d duplicates resolved\n", sup, dups)
	fmt.Fprintln(os.Stderr, "run `compound build` to refresh positions")
}

func cmdPromote(args []string) {
	var id, basis string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--basis="):
			basis = strings.TrimPrefix(a, "--basis=")
		case !strings.HasPrefix(a, "--") && id == "":
			id = a
		}
	}
	if id == "" || basis == "" {
		fmt.Fprintln(os.Stderr, "usage: compound promote <claim-id> --basis=explicit_confirmation|curated_page|decision_record|runtime_proof")
		os.Exit(2)
	}
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	c, err := recall.Promote(l, id, memory.PromotionBasis(basis))
	if err != nil {
		fmt.Fprintf(os.Stderr, "promote: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("promoted %s (basis: %s)\n  %s\n", c.ID, c.PromotionBasis, c.Object)
}

func cmdPack(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, `usage: compound pack "<topic-or-entity>"`)
		os.Exit(2)
	}
	query := strings.Join(args, " ")
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	entity := recall.MakePlan(query, scope.SeedEntities(), false).Entity

	positions, _ := recall.LoadPositions(l)
	claims, _ := recall.LoadClaims(l)
	patterns, _ := recall.LoadPatterns(l)
	evidence, _ := recall.LoadEvidence(l)
	claimByID := make(map[string]memory.Claim, len(claims))
	for _, c := range claims {
		claimByID[c.ID] = c
	}

	var b strings.Builder
	fmt.Fprintf(&b, "---\ntype: context-pack\nquery: %q\nentity: %s\ngenerated_at: %s\n---\n\n", query, entity, today())
	fmt.Fprintf(&b, "# Context Pack — %s\n\n> Owned, evidence-backed context from your history. Inject this before a task instead of re-explaining yourself.\n\n", query)

	fmt.Fprintln(&b, "## Current positions")
	posN := 0
	for _, p := range positions {
		if entity != "" && p.EntitySlug != entity {
			continue
		}
		ac, ok := claimByID[p.ActiveClaimID]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "- **[%s]** %s/%s — %s\n", p.Status, p.EntitySlug, p.TopicSlug, ac.Object)
		if q := quoteOf(ac, evidence); q != "" {
			fmt.Fprintf(&b, "  - _\"%s\"_\n", q)
		}
		posN++
	}
	if posN == 0 {
		fmt.Fprintln(&b, "- _(no positions yet — distill + build first)_")
	}

	patN := 0
	var pb strings.Builder
	for _, p := range patterns {
		if entity != "" && p.ScopeEntitySlug != entity {
			continue
		}
		fmt.Fprintf(&pb, "- %s\n", p.Description)
		patN++
	}
	if patN > 0 {
		fmt.Fprintf(&b, "\n## Recurring patterns\n%s", pb.String())
	}

	if hits, herr := engine.NewConfigured().Query(query, 6); herr == nil && len(hits) > 0 {
		fmt.Fprintln(&b, "\n## Related (engine breadth)")
		for _, h := range hits {
			fmt.Fprintf(&b, "- `%s` — %s\n", h.Slug, h.Snippet)
		}
	}

	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".compound", "packs")
	_ = os.MkdirAll(dir, 0o755)
	out := filepath.Join(dir, slugify(query)+".md")
	_ = os.WriteFile(out, []byte(b.String()), 0o644)
	fmt.Println(out)
	fmt.Fprintf(os.Stderr, "pack: %d positions, %d patterns, entity=%s\n", posN, patN, entity)
}

func latestOperatorDecisionLedger() string {
	pattern := strings.TrimSpace(os.Getenv("COMPOUND_OPERATOR_DECISIONS_GLOB"))
	if pattern == "" {
		return ""
	}
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

func cmdDecisions(args []string) {
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "import":
		var policy, doctrine []memory.Claim
		if root := strings.TrimSpace(os.Getenv("COMPOUND_POLICY_DECISIONS_DIR")); root != "" {
			var err error
			policy, err = decisions.ImportDEC(root)
			if err != nil {
				fmt.Fprintf(os.Stderr, "policy decisions import: %v\n", err)
				os.Exit(1)
			}
		}
		if ledgerPath := latestOperatorDecisionLedger(); ledgerPath != "" {
			var err error
			doctrine, err = decisions.ImportOperatorLedger(ledgerPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "operator decisions import: %v\n", err)
				os.Exit(1)
			}
		}
		for _, c := range policy {
			_ = l.Append("claims", c)
		}
		for _, c := range doctrine {
			_ = l.Append("claims", c)
		}
		fmt.Printf("imported %d policy + %d doctrine decisions into one store (kind-separated)\n", len(policy), len(doctrine))
		if len(policy)+len(doctrine) == 0 {
			fmt.Fprintln(os.Stderr, "no decision sources configured; set COMPOUND_POLICY_DECISIONS_DIR and/or COMPOUND_OPERATOR_DECISIONS_GLOB")
		} else {
			fmt.Fprintln(os.Stderr, "`compound decisions` lists imported claims; `compound decisions doctor` checks coherence")
		}
		return

	case "doctor":
		decisionsDoctor(l)
		return

	default:
		kindFilter := ""
		for _, a := range args {
			if strings.HasPrefix(a, "--kind=") {
				kindFilter = strings.TrimPrefix(a, "--kind=")
			}
		}
		claims, _ := recall.LoadClaims(l)
		groups := map[string][]memory.Claim{}
		for _, c := range claims {
			if c.CreatedBy != "decision-import" {
				continue
			}
			k := decisions.Kind(c)
			if kindFilter != "" && k != kindFilter {
				continue
			}
			groups[k] = append(groups[k], c)
		}
		any := false
		for _, k := range []string{decisions.KindPolicy, decisions.KindDoctrine, decisions.KindOperational} {
			cs := groups[k]
			if len(cs) == 0 {
				continue
			}
			any = true
			fmt.Printf("## %s (%d)\n", k, len(cs))
			for _, c := range cs {
				fmt.Printf("- [%s] %s — %s\n", c.Status, c.ID, c.Object)
			}
			fmt.Println()
		}
		if !any {
			fmt.Println("(no decisions — run `compound decisions import`)")
		}
	}
}

func decisionsDoctor(l *ledger.Ledger) {
	claims, _ := recall.LoadClaims(l)
	var policy, doctrine, decided, open, superseded, noTrigger int
	for _, c := range claims {
		if c.CreatedBy != "decision-import" {
			continue
		}
		switch decisions.Kind(c) {
		case decisions.KindPolicy:
			policy++
		case decisions.KindDoctrine:
			doctrine++
		}
		switch c.Status {
		case memory.StatusPromoted:
			decided++
		case memory.StatusCandidate:
			open++
		case memory.StatusSuperseded:
			superseded++
		}
		if c.Qualifiers == nil || c.Qualifiers["review_trigger"] == nil {
			noTrigger++
		}
	}
	fmt.Printf("# decisions doctor (one store, kind-separated)\n\n")
	fmt.Printf("- store:     %d policy + %d doctrine\n", policy, doctrine)
	fmt.Printf("- lifecycle: %d decided · %d open · %d superseded\n", decided, open, superseded)
	fmt.Printf("- coherence: %d missing a review_trigger (E4 — a decision needs its falsifier/review)\n", noTrigger)
}

func cmdAuthority(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: compound authority import [registry.yaml]  |  compound authority <project>")
		os.Exit(2)
	}
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}

	if args[0] == "import" {
		path := authority.DefaultRegistry()
		if len(args) > 1 {
			path = args[1]
		}
		if strings.TrimSpace(path) == "" {
			fmt.Fprintln(os.Stderr, "authority import needs a registry path or COMPOUND_AUTHORITY_REGISTRY")
			os.Exit(2)
		}
		entities, claims, err := authority.Import(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "authority import: %v\n", err)
			os.Exit(1)
		}
		for _, e := range entities {
			_ = l.Append("entities", e)
		}
		for _, c := range claims {
			_ = l.Append("claims", c)
		}
		fmt.Printf("imported %d projects, %d authority facts from %s\n", len(entities), len(claims), filepath.Base(path))
		fmt.Fprintln(os.Stderr, "run `compound build` to make positions; `compound authority <project>` to query")
		return
	}

	project := args[0]
	claims, _ := recall.LoadClaims(l)
	fmt.Printf("# Authority — %s (what's true about where it lives)\n\n", project)
	found := false
	for _, c := range claims {
		if c.SubjectEntity == project && c.CreatedBy == "authority-import" {
			fmt.Printf("- **%s**: %s\n", c.Predicate, c.Object)
			found = true
		}
	}
	if !found {
		fmt.Printf("(no authority facts for %s — run `compound authority import` first)\n", project)
	}
}

func cmdTimeline(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: compound timeline <entity>")
		os.Exit(2)
	}
	entity := args[0]
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	positions, _ := recall.LoadPositions(l)
	claims, _ := recall.LoadClaims(l)
	edges, _ := recall.LoadEdges(l)
	claimByID := make(map[string]memory.Claim, len(claims))
	for _, c := range claims {
		claimByID[c.ID] = c
	}
	supBy := map[string]string{} // olderID -> newerID
	for _, e := range edges {
		if e.EdgeType == memory.EdgeSupersedes {
			supBy[e.ToID] = e.FromID
		}
	}

	found := false
	for _, p := range positions {
		if p.EntitySlug != entity && p.ID != entity {
			continue
		}
		if len(p.ClaimChain) < 2 {
			continue // only show topics that actually evolved
		}
		found = true
		fmt.Printf("## %s/%s — evolution (%d claims, now [%s])\n", p.EntitySlug, p.TopicSlug, len(p.ClaimChain), p.Status)
		for i, cid := range p.ClaimChain {
			c := claimByID[cid]
			marker := ""
			if cid == p.ActiveClaimID {
				marker = "  ← current"
			}
			fmt.Printf("  %d. [%s] %s%s\n", i+1, c.Status, c.Object, marker)
			if newer, ok := supBy[cid]; ok {
				fmt.Printf("       ⤷ superseded by %s\n", newer)
			}
		}
		fmt.Println()
	}
	if !found {
		fmt.Printf("(no evolved positions for %s — needs >=2 claims on a topic; try `compound reconcile` first)\n", entity)
	}
}

func quoteOf(c memory.Claim, ev map[string]memory.EvidenceItem) string {
	for _, ref := range c.EvidenceRefs {
		if e, ok := ev[ref]; ok && e.QuoteText != "" {
			return e.QuoteText
		}
	}
	return ""
}

func today() string { return time.Now().Format("2006-01-02") }

func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = out[:60]
	}
	if out == "" {
		out = "pack"
	}
	return out
}

func cmdLedger(args []string) {
	l, err := ledger.Open(ledgerRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("ledger: %s\n", l.Root())
	for _, kind := range ledger.Kinds {
		n, _ := l.Count(kind)
		fmt.Printf("  %-18s %d\n", kind, n)
	}
}

func cmdDistill(args []string) {
	var session, model string
	ingest := false
	for _, a := range args {
		switch {
		case a == "--ingest":
			ingest = true
		case strings.HasPrefix(a, "--model="):
			model = strings.TrimPrefix(a, "--model=")
		case !strings.HasPrefix(a, "--") && session == "":
			session = a
		}
	}
	if session == "" {
		fmt.Fprintln(os.Stderr, "usage: compound distill <session.jsonl> [--model=M] [--ingest]")
		os.Exit(2)
	}
	ctx := context.Background()

	fmt.Fprintf(os.Stderr, "[1/3] cleaning + distilling %s ...\n", session)
	res, err := distill.Run(ctx, session, distill.Options{Model: model})
	if err != nil {
		fmt.Fprintf(os.Stderr, "distill: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "      %d user turns · %d decision-turns · %d chunks · %d calls · %d/%d tok\n",
		res.Clean.UserKept, res.DecisionTurns, res.Chunks, res.Calls, res.PromptTokens, res.CompletionTokens)

	fmt.Fprintln(os.Stderr, "[2/3] extracting durable decisions ...")
	decisions, err := distill.ExtractDecisions(ctx, res.Model, res.Distillation)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "      %d durable decisions\n", len(decisions))

	out := writeDecisionLedger(session, decisions)
	fmt.Println(out)

	if ingest {
		l, err := ledger.Open(ledgerRoot())
		if err != nil {
			fmt.Fprintf(os.Stderr, "ledger: %v\n", err)
			os.Exit(1)
		}
		primary := ""
		if pkt, e := scope.Resolve(session, scope.SeedEntities()); e == nil {
			primary = pkt.PrimaryScope
			fmt.Fprintf(os.Stderr, "      scope: %s%s\n", pkt.Status, scopeSuffix(primary))
		}
		added, dropped, err := distill.ToLedger(l, decisions, session, primary)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "[3/3] appended %d candidate claims (+ evidence), %d deduped → %s\n", added, dropped, l.Root())
	} else {
		fmt.Fprintln(os.Stderr, "[3/3] not ingested (pass --ingest to append candidate claims to the ledger)")
	}
}

func writeDecisionLedger(session string, ds []distill.Decision) string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".compound", "distill")
	_ = os.MkdirAll(dir, 0o755)
	out := filepath.Join(dir, "decision-ledger.go.md")
	var b strings.Builder
	fmt.Fprintf(&b, "# Decisions (Go distiller)\n\nSource: `%s`\n\n", session)
	for _, d := range ds {
		fmt.Fprintf(&b, "- **[SELECT]** %s\n  - confidence: %s · domain: %s\n", d.Statement, d.Confidence, d.Domain)
		if d.Quote != "" {
			fmt.Fprintf(&b, "  - quote: %q\n", d.Quote)
		}
	}
	_ = os.WriteFile(out, []byte(b.String()), 0o644)
	return out
}

func cmdScope(args []string) {
	var session string
	explain := false
	for _, a := range args {
		switch {
		case a == "--explain":
			explain = true
		case !strings.HasPrefix(a, "--") && session == "":
			session = a
		}
	}
	if session == "" {
		fmt.Fprintln(os.Stderr, "usage: compound scope <session.jsonl> [--explain]")
		os.Exit(2)
	}
	pkt, err := scope.Resolve(session, scope.SeedEntities())
	if err != nil {
		fmt.Fprintf(os.Stderr, "scope: %v\n", err)
		os.Exit(1)
	}
	if explain {
		fmt.Printf("session: %s\nstatus:  %s\nprimary: %s\n\n", pkt.SessionID, pkt.Status, pkt.PrimaryScope)
		fmt.Printf("  %-14s %5s  %-13s  signals\n", "entity", "score", "relation")
		for _, a := range pkt.Assignments {
			fmt.Printf("  %-14s %5d  %-13s  %s\n", a.Entity, a.Score, a.Relation, strings.Join(a.Signals, ", "))
		}
		return
	}
	b, _ := json.MarshalIndent(pkt, "", "  ")
	fmt.Println(string(b))
}
