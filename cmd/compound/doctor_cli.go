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
	"sort"
	"strings"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/finding"
	"compound/internal/harness"
	"compound/internal/home"
	"compound/internal/hookplan"
	"compound/internal/landlease"
	"compound/internal/policy"
	"compound/internal/runattempt"
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

// doctorPermissionPosture is one harness's read-only permission-authority
// drift row (packet work_0fc6126d8fa2 -- the operator-legibility half: a
// silent posture reversion must surface here even if nobody ran
// `compound harness drift --emit-divergence`). It calls harness.PermissionDrift
// only; doctor's "writes nothing" contract holds -- emission of an
// observation batch lives exclusively on the drift verb's --emit-divergence
// flag, never here.
type doctorPermissionPosture struct {
	Harness    string   `json:"harness"`
	Status     string   `json:"status"` // clean|drift|unknown
	TargetPath string   `json:"target_path,omitempty"`
	Details    []string `json:"details,omitempty"`
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

	// Permission posture: one row per configured harness-permission authority
	// (config/harness-permissions/*.yaml), read-only via harness.PermissionDrift.
	PermissionPosture []doctorPermissionPosture `json:"permission_posture,omitempty"`

	// C3: RunAttempt crash terminality — how many attempts are stuck
	// (pending/running) and how old the oldest is. Doctor surfaces them as
	// a finding until swept (`compound run-attempt sweep`).
	RunAttemptsStuck       int    `json:"run_attempts_stuck"`
	RunAttemptsStuckOldest string `json:"run_attempts_stuck_oldest,omitempty"`

	// Surfacing coverage: rows = agent lanes/harnesses, columns = communication surfaces
	// (guard hook · orient · memory injection · peer visibility). Read-only, derived from
	// state that already exists; it answers "do all harnesses actually see compound?".
	Surfacing          []surfacingRow     `json:"surfacing"`
	SurfacingNote      string             `json:"surfacing_note,omitempty"`
	SurfacingInjection surfacingInjection `json:"surfacing_injection"`

	// Projection freshness: which persisted projections predate the running binary.
	// Answers "is a fix I just installed actually being served?" — see
	// home.ProjectionFreshnessReport and find_b27a3de99682.
	Projections        []home.ProjectionFreshness `json:"projections,omitempty"`
	ProjectionsStale   int                        `json:"projections_stale"`
	ProjectionsUndated int                        `json:"projections_undated"`
	ProjectionsOldest  string                     `json:"projections_oldest,omitempty"`
	ProjectionsNote    string                     `json:"projections_note,omitempty"`
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

	// 3b) C3: RunAttempt crash terminality — count stuck (pending/running)
	// attempts and report the oldest. Read-only; sweeping lives on the
	// `compound run-attempt sweep` verb, never here.
	if stuck, serr := runattempt.Open(homePath).ListNonTerminal(); serr == nil && len(stuck) > 0 {
		rep.RunAttemptsStuck = len(stuck)
		oldest := stuck[0].CreatedAt // List sorts by CreatedAt ascending
		for _, ra := range stuck {
			if ra.CreatedAt.Before(oldest) {
				oldest = ra.CreatedAt
			}
		}
		rep.RunAttemptsStuckOldest = surfacingAge(time.Since(oldest))
	}

	// 4) Placement guard mode — observe is fail-open (record-only).
	if cfg, _, cerr := policy.LoadConfig(homePath); cerr == nil {
		rep.PlacementMode = cfg.PlacementGuardMode
		if cfg.PlacementGuardMode == "observe" {
			rep.PlacementNote = "observe = record-only (a mis-placed write is NOT blocked); set to block to enforce"
		}
	}

	// 5) Permission posture — per-harness authority drift, read-only (harness.PermissionDrift
	// only reads; it never renders/applies). Emission of an observation batch stays exclusively
	// on `compound harness drift --emit-divergence`, never here.
	rep.PermissionPosture = buildPermissionPostureRows(homePath)

	// 6) Surfacing coverage — read-only lanes/harnesses × communication-surfaces matrix.
	rep.Surfacing, rep.SurfacingNote, rep.SurfacingInjection = buildSurfacingCoverage(homePath, rep.Hooks, time.Now())

	// 7) Projection freshness — which persisted projections predate the running
	// binary, i.e. "is the fix I just installed actually being served?". Read-only:
	// doctor reports the gap and never rebuilds, because a health command that
	// mutates shared projections would both surprise concurrent lanes and destroy
	// the evidence it is showing.
	if codeBuiltAt, exeErr := home.RunningBinaryBuiltAt(); exeErr == nil {
		if rows, projErr := home.ProjectionFreshnessReport(homePath, codeBuiltAt, time.Now()); projErr == nil {
			rep.Projections = rows
			rep.ProjectionsStale, rep.ProjectionsUndated, rep.ProjectionsOldest = home.StaleProjectionSummary(rows)
		} else {
			rep.ProjectionsNote = "projection freshness unavailable: " + projErr.Error()
		}
	} else {
		rep.ProjectionsNote = "projection freshness unavailable: cannot resolve the running binary: " + exeErr.Error()
	}

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

	fmt.Fprintln(stdout, "\n## run attempts (crash terminality)")
	if rep.RunAttemptsStuck == 0 {
		fmt.Fprintln(stdout, "- no stuck attempts (all terminal)")
	} else {
		fmt.Fprintf(stdout, "- %d stuck (pending/running), oldest %s ago\n", rep.RunAttemptsStuck, rep.RunAttemptsStuckOldest)
		fmt.Fprintln(stdout, "  -> `compound run-attempt sweep` to terminalize stale attempts")
	}

	fmt.Fprintln(stdout, "\n## placement guard")
	fmt.Fprintf(stdout, "- mode: %s\n", rep.PlacementMode)
	if rep.PlacementNote != "" {
		fmt.Fprintf(stdout, "  note: %s\n", rep.PlacementNote)
	}

	printProjectionFreshness(stdout, rep)
	printPermissionPosture(stdout, rep)
	printSurfacing(stdout, rep)
}

// printProjectionFreshness renders which persisted projections were NOT built by the
// code now installed.
//
// Deliberately worded as "not built by the running code" rather than "stale": the
// body may be perfectly correct, and only matters when the change being trusted
// touched that projection's producer — which doctor cannot know. Overstating it here
// would train the operator to skip the section, which costs exactly what the silent
// gap cost (find_b27a3de99682).
func printProjectionFreshness(stdout io.Writer, rep doctorReport) {
	fmt.Fprintln(stdout, "\n## projections (is an installed fix actually being served?)")
	if rep.ProjectionsNote != "" {
		fmt.Fprintf(stdout, "- %s\n", rep.ProjectionsNote)
		return
	}
	if len(rep.Projections) == 0 {
		fmt.Fprintln(stdout, "- (no persisted projections)")
		return
	}
	if rep.ProjectionsStale == 0 {
		fmt.Fprintf(stdout, "- all %d projection(s) built by the running binary or newer\n", len(rep.Projections))
	} else {
		// Conditional framing first, count second. Rebuilding the binary makes nearly
		// every projection older than it, so a row here is normal and only matters
		// when the change being trusted touched that projection's producer. Leading
		// with alarm would make the section skippable, which is the failure this
		// section exists to prevent.
		fmt.Fprintf(stdout, "- %d of %d were not built by the running binary — harmless unless your change touched their producer\n",
			rep.ProjectionsStale, len(rep.Projections))
		for _, row := range rep.Projections {
			if !row.OlderThanCode {
				continue
			}
			line := fmt.Sprintf("  %-30s %s=%s (%.0fh old)", row.Name, row.TimestampField, row.ComputedAt, row.AgeHours)
			if row.RebuildCommand != "" {
				line += "  rebuild: " + row.RebuildCommand
			}
			fmt.Fprintln(stdout, line)
		}
		fmt.Fprintln(stdout, "  -> a fix to a projection's PRODUCER is not served until that projection is rebuilt;")
		fmt.Fprintln(stdout, "     rebuild the affected one, then re-observe the REAL path (a test that rebuilds its")
		fmt.Fprintln(stdout, "     own inputs cannot see a stale persisted input)")
	}
	if rep.ProjectionsUndated > 0 {
		fmt.Fprintf(stdout, "- %d projection(s) record no build time, so freshness cannot be proven either way\n", rep.ProjectionsUndated)
	}
}

// printPermissionPosture renders the per-harness permission-authority drift
// rows. Read-only: it never renders or applies -- a drifted row only means
// `compound harness drift --harness <h> --emit-divergence` will declare a
// divergence_declared card the next time it runs.
func printPermissionPosture(stdout io.Writer, rep doctorReport) {
	fmt.Fprintln(stdout, "\n## permission posture (per-harness authority drift)")
	if len(rep.PermissionPosture) == 0 {
		fmt.Fprintln(stdout, "- (no harness-permission authorities configured)")
		return
	}
	for _, p := range rep.PermissionPosture {
		fmt.Fprintf(stdout, "- %-12s %-7s %s\n", p.Harness, p.Status, p.TargetPath)
		for _, d := range p.Details {
			fmt.Fprintf(stdout, "    detail: %s\n", d)
		}
		if p.Status == "drift" {
			fmt.Fprintf(stdout, "  -> `compound harness drift --harness %s --emit-divergence` to declare a divergence card\n", p.Harness)
		}
	}
}

// buildPermissionPostureRows discovers configured harness-permission
// authorities (config/harness-permissions/*.yaml) and probes each with
// harness.PermissionDrift -- read-only, writes nothing. A harness whose
// probe errors (e.g. an unreadable target file) still gets a visible row
// (status "unknown") rather than silently vanishing from the report.
func buildPermissionPostureRows(homePath string) []doctorPermissionPosture {
	matches, err := filepath.Glob(filepath.Join(homePath, "config", "harness-permissions", "*.yaml"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	var rows []doctorPermissionPosture
	for _, match := range matches {
		name := strings.TrimSuffix(filepath.Base(match), ".yaml")
		report, err := harness.PermissionDrift(homePath, name)
		if err != nil {
			rows = append(rows, doctorPermissionPosture{Harness: name, Status: "unknown", Details: []string{err.Error()}})
			continue
		}
		status := "clean"
		if report.Drifted {
			status = "drift"
		}
		rows = append(rows, doctorPermissionPosture{Harness: name, Status: status, TargetPath: report.TargetPath, Details: report.Details})
	}
	return rows
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
	orientByHarness, harnessByContract := scanEffectiveContracts(homePath)
	peersByHarness := lastPeersByHarness(homePath, harnessByContract)
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
	v = strings.ReplaceAll(v, "_", "-")
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

// scanEffectiveContracts walks contracts/effective EXACTLY ONCE and returns
// the two artifacts that used to come from two independent walks of the same
// directory: orientByHarness (the most recent orient time per aliased
// harness -- what lastOrientByHarness used to return) and harnessByContract
// (contract_id -> aliased harness, the join table lastPeersByHarness's own
// first walk used to build). Both are derived from one decoded header per
// file (P3, find_9edaa8449d55 -- doctor no longer parses every
// EffectiveContract twice). The two outputs preserve their original,
// slightly different validity rules: harnessByContract only requires a
// non-empty id (lastPeersByHarness's join never looked at created_at), while
// orientByHarness additionally requires created_at to parse. Reading these
// artifacts directly keeps doctor read-only; a torn or foreign file is
// skipped, never fatal.
func scanEffectiveContracts(homePath string) (orientByHarness map[string]time.Time, harnessByContract map[string]string) {
	orientByHarness = map[string]time.Time{}
	harnessByContract = map[string]string{}
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
			ID            string `json:"id"`
			Harness       string `json:"harness"`
			CreatedAt     string `json:"created_at"`
		}
		if json.Unmarshal(data, &head) != nil || head.SchemaVersion != envelope.SVEffectiveContract {
			return nil
		}
		alias := surfacingHarnessAlias(head.Harness)
		if id := strings.TrimSpace(head.ID); id != "" {
			harnessByContract[id] = alias
		}
		t, terr := time.Parse(time.RFC3339, strings.TrimSpace(head.CreatedAt))
		if terr != nil {
			return nil
		}
		if cur, ok := orientByHarness[alias]; !ok || t.After(cur) {
			orientByHarness[alias] = t
		}
		return nil
	})
	return orientByHarness, harnessByContract
}

// lastPeersByHarness returns the most recent orient time per aliased harness for which a RECORDED
// ContextPack carried a non-empty peers section. A recorded pack carries no harness of its own, so
// its harness is resolved through its effective-contract link (ids.contract_id) against
// harnessByContract, built by the shared contracts/effective walk (scanEffectiveContracts).
// Reading these artifacts directly keeps doctor read-only; a torn or foreign file is skipped,
// never fatal.
func lastPeersByHarness(homePath string, harnessByContract map[string]string) map[string]time.Time {
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
