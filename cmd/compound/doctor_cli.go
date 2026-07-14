package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/finding"
	"compound/internal/home"
	"compound/internal/hookplan"
	"compound/internal/landlease"
	"compound/internal/policy"
)

// cmdDoctor is the single legible operator health surface. The newest organs each expose real state,
// but scattered behind their own subcommands or raw JSON — so a first-time operator cannot answer
// "did my install work / who holds the landing lease / is anything waiting for me?" from one place.
// doctor composes the states that already exist — per-harness hook install/drift/absent, the
// land-lease (free/held), pending operational findings, and the placement guard mode — into one
// read-only report. It writes nothing and makes no provider call.
func cmdDoctor(args []string) {
	if code := runDoctor(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

type doctorHook struct {
	Harness    string `json:"harness"`
	Status     string `json:"status"` // installed|ok|drift|absent
	ConfigPath string `json:"config_path"`
}

type doctorReport struct {
	Home               string       `json:"home"`
	Hooks              []doctorHook `json:"hooks"`
	HookHint           string       `json:"hook_hint,omitempty"`
	LeaseHeld          bool         `json:"lease_held"`
	LeaseHolder        string       `json:"lease_holder,omitempty"`
	LeaseExpiresAt     string       `json:"lease_expires_at,omitempty"`
	LeaseStealReason   string       `json:"lease_steal_reason,omitempty"`
	FindingsTotal      int          `json:"findings_total"`
	FindingsLive       int          `json:"findings_live"`
	FindingsSuperseded int          `json:"findings_superseded"`
	PlacementMode      string       `json:"placement_guard_mode"`
	PlacementNote      string       `json:"placement_note,omitempty"`

	// Surfacing coverage: rows = agent lanes/harnesses, columns = communication surfaces
	// (guard hook · orient · memory injection · peer visibility). Read-only, derived from
	// state that already exists; it answers "do all harnesses actually see compound?".
	Surfacing          []surfacingRow     `json:"surfacing"`
	SurfacingNote      string             `json:"surfacing_note,omitempty"`
	SurfacingInjection surfacingInjection `json:"surfacing_injection"`
}

// surfacingRow is one lane/harness × the communication surfaces it should see. Cell vocabulary
// is deliberately small: guard is wired|drift|absent|unknown, orient is wired|never, peers is
// wired|absent (evidence-read from the latest orient-recorded peers-bearing ContextPack for the
// harness). BelowExpectation names the expected-but-unwired columns (the trailing "!" in the text
// render is derived from it) so text and JSON never disagree about what is short; peers is
// informational and never marked below-expectation.
type surfacingRow struct {
	Lane             string   `json:"lane"`     // lane node name, or harness for fallback rows
	Harness          string   `json:"harness"`  // aliased harness the guard/orient columns join on
	Expected         bool     `json:"expected"` // harness-aliased lane => guard + orient expected (v1 derived)
	Guard            string   `json:"guard"`
	Orient           string   `json:"orient"`
	OrientAge        string   `json:"orient_age,omitempty"`
	Injection        string   `json:"injection"`
	Peers            string   `json:"peers"`
	BelowExpectation []string `json:"below_expectation,omitempty"`
}

// surfacingInjection is the memory-injection column's evidence. It is GLOBAL by construction:
// the injection-observation report attributes sessions to work ids, not to harnesses, so an
// honest matrix shows the same numbers on every row rather than faking a per-lane split. Scope
// is always "global" and Note says so; a future per-lane consumption signal would refine this.
type surfacingInjection struct {
	Scope          string `json:"scope"` // always "global" in v1
	ServedEvents   int    `json:"served_events"`
	ConsumedEvents int    `json:"consumed_events"`
	LastConsumed   string `json:"last_consumed"` // coarse age or "never"
	Note           string `json:"note"`
}

func doctorUsage() string {
	return "usage: compound doctor [--base DIR] [--binary PATH] [--home P] [--json]\n" +
		"  read-only operator health: per-harness hook install/drift, land-lease state, pending findings, placement guard mode.\n" +
		"  --base/--binary scope the hook check (default: user home + this binary); writes nothing."
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	if hasUsageFlag(args) {
		fmt.Fprintln(stderr, doctorUsage())
		return 2
	}
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	rep := doctorReport{Home: homePath}

	// 1) Hooks — per-harness installed/ok/drift/absent via the install CHECK path (writes nothing).
	base := strings.TrimSpace(flagValue(args, "--base"))
	if base == "" {
		if h, herr := os.UserHomeDir(); herr == nil {
			base = h
		}
	}
	binary := strings.TrimSpace(flagValue(args, "--binary"))
	if binary == "" {
		if exe, eerr := os.Executable(); eerr == nil {
			binary = exe
		}
	}
	if base != "" && binary != "" {
		if hookRep, herr := hookplan.InstallHooks(base, binary, "", nil, true); herr == nil {
			for _, e := range hookRep.Entries {
				rep.Hooks = append(rep.Hooks, doctorHook{Harness: e.Harness, Status: e.Action, ConfigPath: e.ConfigPath})
			}
		}
	}
	for _, h := range rep.Hooks {
		if h.Status == "absent" || h.Status == "drift" {
			rep.HookHint = "run `compound hook install` to (re)wire the guard"
			break
		}
	}

	// 2) Land-lease — who (if anyone) holds the right to land right now.
	if st, lerr := landlease.Status(homePath, time.Now()); lerr == nil {
		rep.LeaseHeld = st.Held
		rep.LeaseHolder = st.Holder
		rep.LeaseExpiresAt = st.ExpiresAt
		rep.LeaseStealReason = st.StealReason
	}

	// 3) Findings — total, superseded (any id named by another finding's Supersedes), live.
	if fs, ferr := finding.List(homePath, ""); ferr == nil {
		superseded := map[string]bool{}
		for _, f := range fs {
			if s := strings.TrimSpace(f.Supersedes); s != "" {
				superseded[s] = true
			}
		}
		rep.FindingsTotal = len(fs)
		for _, f := range fs {
			if superseded[f.ID] {
				rep.FindingsSuperseded++
			} else {
				rep.FindingsLive++
			}
		}
	}

	// 4) Placement guard mode — observe is fail-open (record-only).
	if cfg, _, cerr := policy.LoadConfig(homePath); cerr == nil {
		rep.PlacementMode = cfg.PlacementGuardMode
		if cfg.PlacementGuardMode == "observe" {
			rep.PlacementNote = "observe = record-only (a mis-placed write is NOT blocked); set to block to enforce"
		}
	}

	// 5) Surfacing coverage — read-only lanes/harnesses × communication-surfaces matrix.
	rep.Surfacing, rep.SurfacingNote, rep.SurfacingInjection = buildSurfacingCoverage(homePath, rep.Hooks, time.Now())

	if hasFlag(args, "--json") {
		if err := encodeJSON(stdout, rep); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	printDoctor(stdout, rep)
	return 0
}

func printDoctor(stdout io.Writer, rep doctorReport) {
	fmt.Fprintln(stdout, "# compound doctor")
	fmt.Fprintf(stdout, "home: %s\n", rep.Home)

	fmt.Fprintln(stdout, "\n## hooks (PreToolUse guard)")
	if len(rep.Hooks) == 0 {
		fmt.Fprintln(stdout, "- (could not read harness configs)")
	}
	for _, h := range rep.Hooks {
		fmt.Fprintf(stdout, "- %-12s %-9s %s\n", h.Harness, h.Status, h.ConfigPath)
	}
	if rep.HookHint != "" {
		fmt.Fprintf(stdout, "  -> %s\n", rep.HookHint)
	}

	fmt.Fprintln(stdout, "\n## land-lease (concurrent landing)")
	if rep.LeaseHeld {
		fmt.Fprintf(stdout, "- HELD by %q until %s\n", rep.LeaseHolder, rep.LeaseExpiresAt)
		fmt.Fprintln(stdout, "  -> wait, or `compound land-lease steal --reason ...` if stale")
	} else {
		fmt.Fprintln(stdout, "- free")
	}
	if rep.LeaseStealReason != "" {
		fmt.Fprintf(stdout, "  last steal: %s\n", rep.LeaseStealReason)
	}

	fmt.Fprintln(stdout, "\n## findings (operational evidence)")
	fmt.Fprintf(stdout, "- %d total (%d live, %d superseded)\n", rep.FindingsTotal, rep.FindingsLive, rep.FindingsSuperseded)
	if rep.FindingsTotal > 0 {
		fmt.Fprintln(stdout, "  -> `compound finding list` to review, `compound finding doctor` for stale/superseded")
	}

	fmt.Fprintln(stdout, "\n## placement guard")
	fmt.Fprintf(stdout, "- mode: %s\n", rep.PlacementMode)
	if rep.PlacementNote != "" {
		fmt.Fprintf(stdout, "  note: %s\n", rep.PlacementNote)
	}

	printSurfacing(stdout, rep)
}

// printSurfacing renders the surfacing-coverage matrix. Cells stay narrow (no table wider than
// ~100 cols, no color); the global injection numbers and the S2 peers caveat are stated once in
// note lines rather than repeated per row, and a trailing "!" marks an expected-but-unwired cell.
func printSurfacing(stdout io.Writer, rep doctorReport) {
	fmt.Fprintln(stdout, "\n## surfacing coverage")
	fmt.Fprintln(stdout, "rows = agent lanes/harnesses · cols = communication surfaces · ! = expected surface not wired (guard+orient expected)")
	if len(rep.Surfacing) == 0 {
		fmt.Fprintln(stdout, "- (no lanes or harnesses to report)")
		return
	}
	fmt.Fprintf(stdout, "%-30s %-9s %-15s %-14s %s\n", "lane", "guard", "orient", "injection", "peers")
	seen := map[string]bool{}
	for _, r := range rep.Surfacing {
		below := map[string]bool{}
		for _, c := range r.BelowExpectation {
			below[c] = true
		}
		guard := r.Guard
		if below["guard"] {
			guard += "!"
		}
		orient := r.Orient
		if r.Orient == "wired" && r.OrientAge != "" {
			orient = "wired (" + r.OrientAge + ")"
		}
		if below["orient"] {
			orient += "!"
		}
		line := fmt.Sprintf("%-30s %-9s %-15s %-14s %s", surfacingLaneCell(r.Lane, 30), guard, orient, r.Injection, r.Peers)
		// Byte-identical rendered rows collapse to one; the JSON report still lists every row.
		if seen[line] {
			continue
		}
		seen[line] = true
		fmt.Fprintln(stdout, line)
	}
	if rep.SurfacingNote != "" {
		fmt.Fprintf(stdout, "-> %s\n", rep.SurfacingNote)
	}
	inj := rep.SurfacingInjection
	fmt.Fprintf(stdout, "-> injection %s: served %d, consumed %d, last-consumed %s (%s)\n",
		inj.Scope, inj.ServedEvents, inj.ConsumedEvents, inj.LastConsumed, inj.Note)
	fmt.Fprintln(stdout, "-> peers: active leases + agent-lane cards render in each harness's orient ContextPack (contextual, display-only)")
}

// surfacingLaneCell renders a lane name into the fixed-width lane column. When the name exceeds the
// column it is truncated, but a short deterministic fingerprint of the FULL name is appended so two
// lanes that share a prefix up to the cut still render as distinct rows (rather than collapsing into
// one indistinguishable truncated cell). The fingerprint is pure ASCII so the fmt width verb pads
// predictably and the table stays within its column budget.
func surfacingLaneCell(name string, width int) string {
	if width <= 0 {
		return name
	}
	r := []rune(name)
	if len(r) <= width {
		return name
	}
	const fp = 6 // hex chars of fingerprint
	keep := width - (1 + fp)
	if keep < 1 {
		keep = 1
	}
	return string(r[:keep]) + "~" + laneCellFingerprint(name, fp)
}

func laneCellFingerprint(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	h := hex.EncodeToString(sum[:])
	if n > len(h) {
		n = len(h)
	}
	return h[:n]
}

// buildSurfacingCoverage derives the read-only surfacing matrix from state that already exists:
// the per-harness guard status (reused from the hook check), the last orient-produced evidence
// per harness (recorded EffectiveContracts), and the GLOBAL memory-injection numbers (the
// observation report does not record a per-harness split). Rows are the capgraph's agent-lane
// nodes when any are imported; otherwise the hook harness list, with an explicit note so the
// matrix is never silently empty on a fresh home. It writes nothing and makes no provider call.
func buildSurfacingCoverage(homePath string, hooks []doctorHook, now time.Time) ([]surfacingRow, string, surfacingInjection) {
	guardByHarness := map[string]string{}
	for _, h := range hooks {
		guardByHarness[surfacingHarnessAlias(h.Harness)] = guardStatus(h.Status)
	}
	orientByHarness := lastOrientByHarness(homePath)
	peersByHarness := lastPeersByHarness(homePath)
	inj := injectionGlobalSummary(homePath, now)

	var rows []surfacingRow
	note := ""
	if lanes := agentLaneRows(homePath); len(lanes) > 0 {
		for _, ln := range lanes {
			rows = append(rows, surfacingRowFor(ln.Name, ln.Harness, guardByHarness, orientByHarness, peersByHarness, inj, now))
		}
	} else {
		note = "no agent lanes imported — showing hook harnesses only"
		for _, harness := range hookHarnessList(hooks) {
			rows = append(rows, surfacingRowFor(harness, harness, guardByHarness, orientByHarness, peersByHarness, inj, now))
		}
	}
	return rows, note, inj
}

// surfacingHarnessAlias normalizes a harness token to the join key the matrix uses across lane
// rows, hook entries, and recorded contracts. It mirrors capgraph's agent-lane harness alias
// (lowercase, trailing "-code" stripped) so "claude" and "claude-code" resolve to one row.
func surfacingHarnessAlias(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	return strings.TrimSuffix(v, "-code")
}

// guardStatus maps a hook install/check action to the matrix guard vocabulary
// (installed/ok/updated -> wired; drift; absent; anything else -> unknown).
func guardStatus(action string) string {
	switch action {
	case "ok", "installed", "updated":
		return "wired"
	case "drift":
		return "drift"
	case "absent":
		return "absent"
	default:
		return "unknown"
	}
}

// surfacingRowFor assembles one row and derives its below-expectation columns: a harness-aliased
// lane is expected to have its guard wired and to have oriented at least once, so an absent/drift
// guard or a never-oriented harness renders with a trailing "!".
func surfacingRowFor(lane, rawHarness string, guardBy map[string]string, orientBy, peersBy map[string]time.Time, inj surfacingInjection, now time.Time) surfacingRow {
	alias := surfacingHarnessAlias(rawHarness)
	guard := guardBy[alias]
	if guard == "" {
		guard = "unknown"
	}
	orient := "never"
	orientAge := ""
	if t, ok := orientBy[alias]; ok {
		orient = "wired"
		orientAge = surfacingAge(now.Sub(t))
	}
	// peers is evidence-read: wired when the harness's latest orient-recorded ContextPack carried a
	// non-empty peers section, else absent. Display-only — never marked below-expectation.
	peers := "absent"
	if t, ok := peersBy[alias]; ok {
		peers = "wired (last " + surfacingAge(now.Sub(t)) + ")"
	}
	row := surfacingRow{
		Lane:      lane,
		Harness:   alias,
		Expected:  true,
		Guard:     guard,
		Orient:    orient,
		OrientAge: orientAge,
		Injection: fmt.Sprintf("%d/%d (global)", inj.ServedEvents, inj.ConsumedEvents),
		Peers:     peers,
	}
	if guard == "absent" || guard == "drift" {
		row.BelowExpectation = append(row.BelowExpectation, "guard")
	}
	if orient == "never" {
		row.BelowExpectation = append(row.BelowExpectation, "orient")
	}
	return row
}

// agentLaneRows lists the non-retired agent-lane nodes for this home. An absent capability graph
// (fresh home) is not an error — it just means the fallback hook-harness rows are used.
func agentLaneRows(homePath string) []capgraph.AgentLaneNode {
	if !capgraph.Exists(homePath) {
		return nil
	}
	store, err := capgraph.OpenReadOnly(homePath)
	if err != nil {
		return nil
	}
	nodes, err := store.AgentLaneNodes()
	if err != nil {
		return nil
	}
	out := make([]capgraph.AgentLaneNode, 0, len(nodes))
	for _, n := range nodes {
		if n.Retired {
			continue
		}
		out = append(out, n)
	}
	return out
}

// hookHarnessList is the fallback row set when no agent lanes are imported. It uses the harnesses
// the hook check already reported, and falls back to the canonical two so the matrix is never
// silently empty even when the harness configs could not be read.
func hookHarnessList(hooks []doctorHook) []string {
	if len(hooks) == 0 {
		return []string{"claude-code", "codex"}
	}
	out := make([]string, 0, len(hooks))
	for _, h := range hooks {
		out = append(out, h.Harness)
	}
	return out
}

// lastOrientByHarness scans the recorded EffectiveContracts (the orient-produced evidence that
// carries a harness) and returns the most recent orient time per aliased harness. Reading these
// artifacts directly keeps doctor read-only; a torn or foreign file is skipped, never fatal.
func lastOrientByHarness(homePath string) map[string]time.Time {
	out := map[string]time.Time{}
	base := filepath.Join(homePath, "contracts", "effective")
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var head struct {
			SchemaVersion string `json:"schema_version"`
			Harness       string `json:"harness"`
			CreatedAt     string `json:"created_at"`
		}
		if json.Unmarshal(data, &head) != nil || head.SchemaVersion != envelope.SVEffectiveContract {
			return nil
		}
		t, terr := time.Parse(time.RFC3339, strings.TrimSpace(head.CreatedAt))
		if terr != nil {
			return nil
		}
		alias := surfacingHarnessAlias(head.Harness)
		if cur, ok := out[alias]; !ok || t.After(cur) {
			out[alias] = t
		}
		return nil
	})
	return out
}

// lastPeersByHarness returns the most recent orient time per aliased harness for which a RECORDED
// ContextPack carried a non-empty peers section. A recorded pack carries no harness of its own, so
// its harness is resolved through its effective-contract link (ids.contract_id) against the recorded
// EffectiveContracts the orient cell already reads. Reading these artifacts directly keeps doctor
// read-only; a torn or foreign file is skipped, never fatal.
func lastPeersByHarness(homePath string) map[string]time.Time {
	// contract_id -> aliased harness, from the recorded EffectiveContracts (they carry the harness).
	harnessByContract := map[string]string{}
	_ = filepath.WalkDir(filepath.Join(homePath, "contracts", "effective"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var head struct {
			SchemaVersion string `json:"schema_version"`
			ID            string `json:"id"`
			Harness       string `json:"harness"`
		}
		if json.Unmarshal(data, &head) != nil || head.SchemaVersion != envelope.SVEffectiveContract {
			return nil
		}
		if id := strings.TrimSpace(head.ID); id != "" {
			harnessByContract[id] = surfacingHarnessAlias(head.Harness)
		}
		return nil
	})

	out := map[string]time.Time{}
	_ = filepath.WalkDir(filepath.Join(homePath, "context", "packs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var head struct {
			SchemaVersion string `json:"schema_version"`
			CreatedAt     string `json:"created_at"`
			IDs           struct {
				ContractID string `json:"contract_id"`
			} `json:"ids"`
			Peers *struct {
				ActiveLeases []json.RawMessage `json:"active_leases"`
				Lanes        []json.RawMessage `json:"lanes"`
			} `json:"peers"`
		}
		if json.Unmarshal(data, &head) != nil || head.SchemaVersion != envelope.SVContextPack {
			return nil
		}
		// Recorded but empty peers is NOT evidence of a wired peers cell.
		if head.Peers == nil || (len(head.Peers.ActiveLeases) == 0 && len(head.Peers.Lanes) == 0) {
			return nil
		}
		alias, ok := harnessByContract[strings.TrimSpace(head.IDs.ContractID)]
		if !ok || alias == "" {
			return nil
		}
		t, terr := time.Parse(time.RFC3339, strings.TrimSpace(head.CreatedAt))
		if terr != nil {
			return nil
		}
		if cur, ok := out[alias]; !ok || t.After(cur) {
			out[alias] = t
		}
		return nil
	})
	return out
}

// injectionGlobalSummary reuses the memory-injection observation report and rolls it up to global
// served/consumed event totals plus the most recent consumption age. The report attributes
// sessions to work ids, not harnesses, so this is explicitly global (per-lane split not recorded).
func injectionGlobalSummary(homePath string, now time.Time) surfacingInjection {
	inj := surfacingInjection{Scope: "global", LastConsumed: "never", Note: "per-lane split not recorded"}
	rep, err := capgraph.BuildInjectionObservationReport(homePath, capgraph.InjectionObservationOptions{GeneratedAt: now})
	if err != nil {
		return inj
	}
	for _, c := range rep.Conditions {
		inj.ServedEvents += c.ServedClaimEvents
		inj.ConsumedEvents += c.ConsumedClaimEvents
	}
	var last time.Time
	for _, s := range rep.Sessions {
		if s.ConsumedCount <= 0 {
			continue
		}
		if t, perr := time.Parse(time.RFC3339, strings.TrimSpace(s.CreatedAt)); perr == nil && t.After(last) {
			last = t
		}
	}
	if !last.IsZero() {
		inj.LastConsumed = surfacingAge(now.Sub(last))
	}
	return inj
}

// surfacingAge humanizes a duration coarsely (m/h/d) for the matrix. Ages are computed against a
// runtime clock and a runtime-written timestamp, never a fixed fixture date, so no fixture ages
// into a failure over time.
func surfacingAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}
