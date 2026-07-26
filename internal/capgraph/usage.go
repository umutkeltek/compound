// usage.go — the capability/skill usage telemetry projection.
//
// This is the receipt-feedback surface: it sweeps close receipts (events/*.json)
// plus the context packs they reference and aggregates, per capability/skill ref,
// how often it was OFFERED vs actually USED vs MISSED vs FALSE-TRIGGERED, and for
// every ContextPack.included[] ref whether it was CONSUMED (echoed in a receipt's
// used refs) or IGNORED (injected but never used) — the reinforcement signal the
// memory-temperature design needs.
//
// Honest attribution (the anti-famine rule): CONSUMED increments from ANY receipt that
// lists the injected ref in Used. IGNORED increments ONLY from receipts whose Skills.
// UsedDeclared=true — a receipt that affirmatively declared what it used. A receipt that
// never declared (most ceremony closes; a session-end whose consumption detector could not
// read a transcript) leaves its injected refs at the INJECTED count only: unknown, not
// ignored. Undeclared silence must never be read as a cooling signal — that would punish a
// claim for the harness never reporting, the same error as treating a never-injected claim
// as cold. (Falsifier: an undeclared receipt with injected-but-unused refs keeps ignored 0.)
//
// It is a projection, never truth: rebuilt from scratch on every call, derived only
// from receipts, and written only under projections/ (never the ledger). See
// docs/architecture/runtime.md (§ home layout) and epistemics (generated view can
// prove freshness/drift, it cannot promote truth).
package capgraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/home"
)

const (
	// CapabilityUsageSchemaVersion versions the projection body.
	CapabilityUsageSchemaVersion = "compound.capability_usage.v0.1"
	// CapabilityUsageProjectionRef is the stable ref for this generated view.
	CapabilityUsageProjectionRef = "compound://projections/capability-usage"
	// CapabilityUsageRelPath is the home-relative path the projection is written to.
	CapabilityUsageRelPath = "projections/capability-usage.json"

	defaultSourceReceiptCap = 5
	defaultSummaryTop       = 20
)

// CapabilityUsage is per-capability/skill-ref usage telemetry.
type CapabilityUsage struct {
	Ref                 string   `json:"ref"`
	OfferedCount        int      `json:"offered_count"`
	UsedCount           int      `json:"used_count"`
	ValidatedCount      int      `json:"validated_count"`
	MissedCount         int      `json:"missed_count"`
	FalseTriggeredCount int      `json:"false_triggered_count"`
	LastUsed            string   `json:"last_used,omitempty"`
	LastOffered         string   `json:"last_offered,omitempty"`
	SourceReceipts      []string `json:"source_receipts"`
}

// InjectedRefUsage is per-ContextPack.included[] ref reinforcement telemetry:
// injected (loaded into a work's context) vs consumed (echoed in that work's
// receipt used refs) vs ignored (injected but never used).
//
// ProbeGroup is additive (work_8713c4206232, S4): "<class>/<group>" parsed from the
// ref's IncludedRef.Reason via parseProbeGroupLabel (probe_group.go) when the ref was
// selected through internal/contract's probe-group path (probe_contracts.go). It is
// omitempty and left unset for a legacy ref (no probe label at all) or a ref whose label
// never parses (malformed) — so a pre-probe-group fixture's projection JSON stays
// byte-identical (pinned by TestBuildCapabilityUsageLegacyPacksOmitProbeGroup).
type InjectedRefUsage struct {
	Ref            string   `json:"ref"`
	SourceClass    string   `json:"source_class,omitempty"`
	ProbeGroup     string   `json:"probe_group,omitempty"`
	InjectedCount  int      `json:"injected_count"`
	ConsumedCount  int      `json:"consumed_count"`
	IgnoredCount   int      `json:"ignored_count"`
	LastInjected   string   `json:"last_injected,omitempty"`
	SourceReceipts []string `json:"source_receipts"`
}

// FailureCauseCount aggregates the optional close-receipt failure_cause field.
type FailureCauseCount struct {
	SurfaceOutcome string `json:"surface_outcome"`
	Mechanism      string `json:"mechanism"`
	Count          int    `json:"count"`
}

// UsageSummaryEntry is one ranked ref in a summary list.
type UsageSummaryEntry struct {
	Ref   string `json:"ref"`
	Count int    `json:"count"`
}

// UsageSummary is the operator/Bitter-Lesson-watch quick view.
type UsageSummary struct {
	// TopIgnored ranks injected refs by how often they were injected but never used.
	TopIgnored []UsageSummaryEntry `json:"top_ignored"`
	// NeverFired lists refs that were offered but never used (used_count==0),
	// ranked by offered_count — the receipt-feedback famine made visible.
	NeverFired []UsageSummaryEntry `json:"never_fired"`
}

// CapabilityUsageProjection is the full derived surface.
type CapabilityUsageProjection struct {
	SchemaVersion       string              `json:"schema_version"`
	ProjectionRef       string              `json:"projection_ref"`
	GeneratedAt         string              `json:"generated_at"`
	Since               string              `json:"since,omitempty"`
	ReceiptsScanned     int                 `json:"receipts_scanned"`
	ReceiptsInWindow    int                 `json:"receipts_in_window"`
	ContextPacksScanned int                 `json:"context_packs_scanned"`
	Capabilities        []CapabilityUsage   `json:"capabilities"`
	InjectedRefs        []InjectedRefUsage  `json:"injected_refs"`
	FailureCauses       []FailureCauseCount `json:"failure_causes"`
	Summary             UsageSummary        `json:"summary"`
}

// UsageBuildOptions parameterizes the sweep. GeneratedAt is injected so the build
// is deterministic (byte-identical for equal inputs + equal GeneratedAt).
type UsageBuildOptions struct {
	Since            time.Time // zero = no window filter
	GeneratedAt      time.Time // stamped into GeneratedAt; inject for determinism
	SourceReceiptCap int       // per-ref evidence cap (last N); 0 => default
	SummaryTop       int       // summary list length; 0 => default
}

// --- internal accumulators ---

type refReceipt struct {
	id string
	ts string
}

type capAccum struct {
	offered        int
	used           int
	validated      int
	missed         int
	falseTriggered int
	lastUsed       string
	lastOffered    string
	usedIn         []refReceipt // receipts where this ref was used (evidence)
}

type injAccum struct {
	sourceClass  string
	probeGroup   string
	injected     int
	consumed     int
	ignored      int
	lastInjected string
	injectedIn   []refReceipt
}

// BuildCapabilityUsage sweeps receipts under homePath/events and the context packs
// they reference, and returns the aggregated usage projection. It never mutates the
// ledger; the caller persists the result under projections/ via WriteCapabilityUsage.
func BuildCapabilityUsage(homePath string, opts UsageBuildOptions) (CapabilityUsageProjection, error) {
	srcCap := opts.SourceReceiptCap
	if srcCap <= 0 {
		srcCap = defaultSourceReceiptCap
	}
	top := opts.SummaryTop
	if top <= 0 {
		top = defaultSummaryTop
	}
	gen := opts.GeneratedAt
	if gen.IsZero() {
		gen = time.Now()
	}

	proj := CapabilityUsageProjection{
		SchemaVersion: CapabilityUsageSchemaVersion,
		ProjectionRef: CapabilityUsageProjectionRef,
		GeneratedAt:   gen.UTC().Format(time.RFC3339),
		Capabilities:  []CapabilityUsage{},
		InjectedRefs:  []InjectedRefUsage{},
		FailureCauses: []FailureCauseCount{},
		Summary:       UsageSummary{TopIgnored: []UsageSummaryEntry{}, NeverFired: []UsageSummaryEntry{}},
	}
	if !opts.Since.IsZero() {
		proj.Since = opts.Since.UTC().Format(time.RFC3339)
	}

	caps := map[string]*capAccum{}
	injs := map[string]*injAccum{}
	failures := map[string]*FailureCauseCount{}
	packCache := map[string]*envelope.ContextPack{} // id -> pack (nil = missing/invalid)
	packsLoaded := map[string]bool{}

	events, err := eventstore.Open(homePath)
	if err != nil {
		return proj, err
	}
	entries, err := events.Scan(eventstore.ScanOptions{Prefixes: []string{"rec_"}})
	if err != nil {
		if os.IsNotExist(err) {
			return proj, nil
		}
		return proj, err
	}

	for _, entry := range entries {
		record, err := entry.Read()
		if err != nil {
			return proj, err
		}
		data := record.Bytes
		var head struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := json.Unmarshal(data, &head); err != nil {
			continue // torn/foreign file — skip, do not brick the sweep
		}
		if head.SchemaVersion != envelope.SVReceipt {
			continue
		}
		var rec envelope.Receipt
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		if rec.Validate() != nil {
			continue
		}
		proj.ReceiptsScanned++

		if !opts.Since.IsZero() {
			created, perr := time.Parse(time.RFC3339, strings.TrimSpace(rec.CreatedAt))
			if perr == nil && created.Before(opts.Since) {
				continue
			}
		}
		proj.ReceiptsInWindow++

		ts := strings.TrimSpace(rec.CreatedAt)
		validated := rec.Outcome == "completed" && rec.EffectiveChanged()
		usedSet := dedupeSet(rec.Skills.Used)

		for ref := range usedSet {
			a := capFor(caps, ref)
			a.used++
			if validated {
				a.validated++
			}
			a.lastUsed = laterTS(a.lastUsed, ts)
			a.usedIn = append(a.usedIn, refReceipt{id: rec.ID, ts: ts})
		}
		offeredSet := dedupeSet(append(append([]string{}, rec.Skills.CapabilitiesOffered...), rec.Skills.Candidate...))
		for ref := range offeredSet {
			a := capFor(caps, ref)
			a.offered++
			a.lastOffered = laterTS(a.lastOffered, ts)
		}
		for ref := range dedupeSet(rec.Skills.Missed) {
			capFor(caps, ref).missed++
		}
		for ref := range dedupeSet(rec.Skills.FalseTriggered) {
			capFor(caps, ref).falseTriggered++
		}

		if rec.FailureCause != nil {
			so := strings.TrimSpace(rec.FailureCause.SurfaceOutcome)
			mech := strings.TrimSpace(rec.FailureCause.Mechanism)
			if so != "" || mech != "" {
				key := so + "\x00" + mech
				if fc := failures[key]; fc != nil {
					fc.Count++
				} else {
					failures[key] = &FailureCauseCount{SurfaceOutcome: so, Mechanism: mech, Count: 1}
				}
			}
		}

		// injected-ref reinforcement: resolve the receipt's context pack and join
		// its included refs against this receipt's used set.
		packID := strings.TrimSpace(rec.IDs.ContextPackID)
		if packID == "" {
			continue
		}
		pack, ok := packCache[packID]
		if !ok {
			pack = loadContextPack(homePath, packID)
			packCache[packID] = pack
			if pack != nil {
				packsLoaded[packID] = true
			}
		}
		if pack == nil {
			continue
		}
		for _, inc := range pack.Included {
			ref := strings.TrimSpace(inc.Ref)
			if ref == "" {
				continue
			}
			a := injs[ref]
			if a == nil {
				a = &injAccum{}
				injs[ref] = a
			}
			a.injected++
			if strings.TrimSpace(inc.SourceClass) != "" {
				a.sourceClass = inc.SourceClass
			}
			if label, ok := parseProbeGroupLabel(inc.Reason); ok {
				a.probeGroup = label
			}
			if usedSet[ref] {
				a.consumed++
			} else if rec.Skills.UsedDeclared {
				// IGNORED only when the receipt DECLARED its used-list (see ReceiptSkills.UsedDeclared):
				// absence from a declaring receipt is real evidence the injected ref was not used.
				a.ignored++
			}
			// else: undeclared receipt — the injected ref stays at its injected count only. Absence of
			// a declaration is UNKNOWN, never ignored (mirrors temperature's "never-injected = neutral").
			a.lastInjected = laterTS(a.lastInjected, ts)
			a.injectedIn = append(a.injectedIn, refReceipt{id: rec.ID, ts: ts})
		}
	}
	proj.ContextPacksScanned = len(packsLoaded)

	// materialize capabilities, sorted by ref.
	for ref, a := range caps {
		proj.Capabilities = append(proj.Capabilities, CapabilityUsage{
			Ref:                 ref,
			OfferedCount:        a.offered,
			UsedCount:           a.used,
			ValidatedCount:      a.validated,
			MissedCount:         a.missed,
			FalseTriggeredCount: a.falseTriggered,
			LastUsed:            a.lastUsed,
			LastOffered:         a.lastOffered,
			SourceReceipts:      lastReceiptIDs(a.usedIn, srcCap),
		})
	}
	sort.Slice(proj.Capabilities, func(i, j int) bool {
		return proj.Capabilities[i].Ref < proj.Capabilities[j].Ref
	})

	// materialize injected refs, sorted by ref.
	for ref, a := range injs {
		proj.InjectedRefs = append(proj.InjectedRefs, InjectedRefUsage{
			Ref:            ref,
			SourceClass:    a.sourceClass,
			ProbeGroup:     a.probeGroup,
			InjectedCount:  a.injected,
			ConsumedCount:  a.consumed,
			IgnoredCount:   a.ignored,
			LastInjected:   a.lastInjected,
			SourceReceipts: lastReceiptIDs(a.injectedIn, srcCap),
		})
	}
	sort.Slice(proj.InjectedRefs, func(i, j int) bool {
		return proj.InjectedRefs[i].Ref < proj.InjectedRefs[j].Ref
	})

	// materialize failure causes, sorted by (surface_outcome, mechanism).
	for _, fc := range failures {
		proj.FailureCauses = append(proj.FailureCauses, *fc)
	}
	sort.Slice(proj.FailureCauses, func(i, j int) bool {
		if proj.FailureCauses[i].SurfaceOutcome != proj.FailureCauses[j].SurfaceOutcome {
			return proj.FailureCauses[i].SurfaceOutcome < proj.FailureCauses[j].SurfaceOutcome
		}
		return proj.FailureCauses[i].Mechanism < proj.FailureCauses[j].Mechanism
	})

	proj.Summary = buildUsageSummary(proj.Capabilities, proj.InjectedRefs, top)
	return proj, nil
}

// WriteCapabilityUsage persists the projection under projections/, returning the
// absolute path written. This is derived state; it must never touch the ledger.
func WriteCapabilityUsage(homePath string, proj CapabilityUsageProjection) (string, error) {
	path := filepath.Join(homePath, filepath.FromSlash(CapabilityUsageRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(proj, "", "  ")
	if err != nil {
		return "", err
	}
	// Atomic: BuildTemperature reads this body, and that chain terminates on the live
	// orient path. See home.WriteFileAtomic.
	if err := home.WriteFileAtomic(path, append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func buildUsageSummary(capsList []CapabilityUsage, injList []InjectedRefUsage, top int) UsageSummary {
	sum := UsageSummary{TopIgnored: []UsageSummaryEntry{}, NeverFired: []UsageSummaryEntry{}}

	ignored := make([]UsageSummaryEntry, 0, len(injList))
	for _, inj := range injList {
		if inj.IgnoredCount > 0 && inj.ConsumedCount == 0 {
			ignored = append(ignored, UsageSummaryEntry{Ref: inj.Ref, Count: inj.IgnoredCount})
		}
	}
	sort.Slice(ignored, func(i, j int) bool {
		if ignored[i].Count != ignored[j].Count {
			return ignored[i].Count > ignored[j].Count
		}
		return ignored[i].Ref < ignored[j].Ref
	})
	sum.TopIgnored = capEntries(ignored, top)

	never := make([]UsageSummaryEntry, 0, len(capsList))
	for _, c := range capsList {
		if c.OfferedCount > 0 && c.UsedCount == 0 {
			never = append(never, UsageSummaryEntry{Ref: c.Ref, Count: c.OfferedCount})
		}
	}
	sort.Slice(never, func(i, j int) bool {
		if never[i].Count != never[j].Count {
			return never[i].Count > never[j].Count
		}
		return never[i].Ref < never[j].Ref
	})
	sum.NeverFired = capEntries(never, top)
	return sum
}

func capEntries(entries []UsageSummaryEntry, top int) []UsageSummaryEntry {
	if top > 0 && len(entries) > top {
		entries = entries[:top]
	}
	if entries == nil {
		return []UsageSummaryEntry{}
	}
	return entries
}

func capFor(m map[string]*capAccum, ref string) *capAccum {
	a := m[ref]
	if a == nil {
		a = &capAccum{}
		m[ref] = a
	}
	return a
}

func dedupeSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		if t := strings.TrimSpace(v); t != "" {
			out[t] = true
		}
	}
	return out
}

// lastReceiptIDs returns the ids of the last N receipts (by created_at, then id),
// in chronological order — bounded evidence, deterministic.
func lastReceiptIDs(list []refReceipt, srcCap int) []string {
	sort.Slice(list, func(i, j int) bool {
		if list[i].ts != list[j].ts {
			return list[i].ts < list[j].ts
		}
		return list[i].id < list[j].id
	})
	if srcCap > 0 && len(list) > srcCap {
		list = list[len(list)-srcCap:]
	}
	out := make([]string, 0, len(list))
	for _, r := range list {
		out = append(out, r.id)
	}
	return out
}

// laterTS returns the later of two RFC3339 timestamps; parse failures sort as zero.
func laterTS(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	ta, ea := time.Parse(time.RFC3339, a)
	tb, eb := time.Parse(time.RFC3339, b)
	if ea != nil || eb != nil {
		if a >= b {
			return a
		}
		return b
	}
	if tb.After(ta) {
		return b
	}
	return a
}

func loadContextPack(homePath, packID string) *envelope.ContextPack {
	path := filepath.Join(homePath, "context", "packs", packID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pack envelope.ContextPack
	if json.Unmarshal(data, &pack) != nil {
		return nil
	}
	if pack.Validate() != nil {
		return nil
	}
	return &pack
}
