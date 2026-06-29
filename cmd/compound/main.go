// Command compound is the CLI for the compound information kernel / transition substrate.
// Built envelope-first: the proven spine is P0-P6 first slice (home, envelopes, contract/orient, work,
// policy, harness/hook, memory ledger). Memory is one slice on top (P6, re-bound through the spine), not the
// system boundary. Gen-1 memory-era commands are retired tombstones; canonical memory lives
// under `compound memory ...`.
// Architecture: docs/architecture/{substrate,runtime,transition}.md, docs/roadmap/phases.md.
package main

import (
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
	"compound/internal/capgraph"
	"compound/internal/contract"
	"compound/internal/decisions"
	"compound/internal/distiller"
	"compound/internal/envelope"
	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/mem"
	"compound/internal/policy"
	"compound/internal/projection"
	"compound/internal/runtimebinding"
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
  compound memory <candidate|adjudicate|recall|timeline|query|doctor>
                                                                 spine memory: candidates, claims, positions, patterns, evidence
  compound distiller run <trace.json>                           Trace -> RetrievedPriorState -> CandidateSet -> Critic -> Adjudicator
  compound capability <add|surface|observe|consume|list>         capability graph: auto-surface, never auto-execute
compound authority import [f] | authority <entity>             import/query authority through spine memory
compound decisions import | decisions [--kind=K] | decisions doctor
                                                                 import/query decisions through spine memory
compound runtime doctor [--project-contract] <yaml>            RuntimeBinding + ServiceContract health
compound projection doctor <freshness.yaml> [--writeback]      ProjectionFreshness + gated writeback
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

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
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
		fmt.Fprintln(os.Stderr, "usage: compound authority import [registry.yaml] | authority <entity>")
		os.Exit(2)
	}
	store, _ := openMemStore()
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

func cmdDecisions(args []string) {
	store, _ := openMemStore()
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "import":
		var facts []mem.Candidate
		if root := strings.TrimSpace(os.Getenv("COMPOUND_POLICY_DECISIONS_DIR")); root != "" {
			got, err := decisions.ImportDEC(root)
			if err != nil {
				fmt.Fprintln(os.Stderr, "policy decisions import:", err)
				os.Exit(1)
			}
			facts = append(facts, got...)
		}
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
			verdict := "park"
			if c.PromotionBasis != "" {
				verdict = "promote"
			}
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
		fmt.Printf("imported %d decision candidates through spine memory (%d promoted, %d parked)\n", len(captured), promoted, parked)
	case "doctor":
		claims, err := store.AllCurrent()
		if err != nil {
			fmt.Fprintln(os.Stderr, "decisions doctor:", err)
			os.Exit(1)
		}
		counts := map[string]int{}
		statuses := map[string]int{}
		missingLifecycle := 0
		for _, c := range claims {
			if c.ClaimType != "decision" {
				continue
			}
			l := decisions.LifecycleFromClaim(c)
			counts[l.Kind]++
			statuses[l.Status]++
			if len(decisions.MissingRequired(l)) > 0 {
				missingLifecycle++
			}
		}
		fmt.Printf("# decisions doctor\n\n")
		fmt.Printf("- policy: %d\n- doctrine: %d\n- operational: %d\n", counts[decisions.KindPolicy], counts[decisions.KindDoctrine], counts[decisions.KindOperational])
		fmt.Printf("- proposed: %d\n- accepted: %d\n- superseded: %d\n- deprecated: %d\n- rejected: %d\n", statuses[decisions.StatusProposed], statuses[decisions.StatusAccepted], statuses[decisions.StatusSuperseded], statuses[decisions.StatusDeprecated], statuses[decisions.StatusRejected])
		fmt.Printf("- missing_lifecycle_fields: %d\n", missingLifecycle)
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

func cmdRuntime(args []string) {
	if len(args) < 1 || args[0] != "doctor" {
		fmt.Fprintln(os.Stderr, "usage: compound runtime doctor [--project-contract] <yaml>")
		os.Exit(2)
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: compound runtime doctor [--project-contract] <yaml>")
		os.Exit(2)
	}
	path := args[1]
	load := runtimebinding.Load
	if args[1] == "--project-contract" {
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: compound runtime doctor --project-contract <project.yaml>")
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
		os.Exit(1)
	}
}

type projectionWritebackResult struct {
	WorkID           string `json:"work_id"`
	ActionIntentID   string `json:"action_intent_id"`
	PolicyDecisionID string `json:"policy_decision_id"`
	GateID           string `json:"gate_id"`
	GateStatus       string `json:"gate_status"`
	ReceiptID        string `json:"receipt_id,omitempty"`
	TransitionID     string `json:"transition_id,omitempty"`
}

func runProjectionWriteback(report projection.Freshness, now time.Time) (projectionWritebackResult, error) {
	var result projectionWritebackResult
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
	result = projectionWritebackResult{WorkID: w.ID, ActionIntentID: ai.ID, PolicyDecisionID: pd.ID, GateID: checked.ID, GateStatus: checked.Status}
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

func cmdProjection(args []string) {
	if len(args) < 1 || args[0] != "doctor" {
		fmt.Fprintln(os.Stderr, "usage: compound projection doctor <freshness.yaml> [--writeback]")
		os.Exit(2)
	}
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: compound projection doctor <freshness.yaml> [--writeback]")
		os.Exit(2)
	}
	now := time.Now()
	writebackEnabled := hasFlag(args[2:], "--writeback")
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
			return
		}
		os.Exit(1)
	}
}
