package home

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"compound/internal/candidates"
)

// HabitatClass describes what loss of a path would mean. Backup and compaction
// policy is recorded separately and validated against this class.
type HabitatClass string

const (
	HabitatClassAuthority   HabitatClass = "authority"
	HabitatClassEvidence    HabitatClass = "evidence"
	HabitatClassConfig      HabitatClass = "configuration"
	HabitatClassOperational HabitatClass = "operational"
	HabitatClassRebuildable HabitatClass = "rebuildable"
	HabitatClassTransient   HabitatClass = "transient"
	HabitatClassExport      HabitatClass = "export"
)

type HabitatStatus string

const (
	HabitatStatusActive HabitatStatus = "active"
	HabitatStatusBridge HabitatStatus = "bridge"
)

const HabitatClosePlans = "legacy.work.close-plans"

// HabitatEntry is one prefix rule in the COMPOUND_HOME habitat. Longest-prefix
// resolution lets a durable cursor or receipt remain protected even while its
// historical parent directory is named projections.
type HabitatEntry struct {
	ID                  string        `json:"id"`
	Prefix              string        `json:"prefix"`
	Class               HabitatClass  `json:"class"`
	Status              HabitatStatus `json:"status"`
	Scaffold            bool          `json:"scaffold,omitempty"`
	TopLevelFile        bool          `json:"top_level_file,omitempty"`
	Backup              bool          `json:"backup"`
	Compact             bool          `json:"compact"`
	PlacementAllowed    bool          `json:"placement_allowed"`
	CanonicalPrefix     string        `json:"canonical_prefix,omitempty"`
	RetirementCondition string        `json:"retirement_condition,omitempty"`
}

type HabitatInventory struct {
	UnknownRoots   []string `json:"unknown_roots"`
	UnknownEntries []string `json:"unknown_entries"`
	BridgeRoots    []string `json:"bridge_roots"`
	OK             bool     `json:"ok"`
}

var habitatEntries = buildHabitatEntries()

func buildHabitatEntries() []HabitatEntry {
	active := func(id, prefix string, class HabitatClass, scaffold, backup, compact bool) HabitatEntry {
		return HabitatEntry{
			ID: id, Prefix: prefix, Class: class, Status: HabitatStatusActive,
			Scaffold: scaffold, Backup: backup, Compact: compact, PlacementAllowed: true,
		}
	}
	child := func(id, prefix string, class HabitatClass, scaffold, backup, compact bool) HabitatEntry {
		return active(id, prefix, class, scaffold, backup, compact)
	}
	bridge := func(id, prefix, canonical string) HabitatEntry {
		return HabitatEntry{
			ID: id, Prefix: prefix, Class: HabitatClassOperational, Status: HabitatStatusBridge,
			Backup: true, Compact: false, PlacementAllowed: false, CanonicalPrefix: canonical,
			RetirementCondition: "new writes use " + canonical + "; real consumers replay with " + prefix + " unavailable; retained bytes are archived before explicit operator retirement",
		}
	}

	entries := []HabitatEntry{
		{ID: "public-fixture-summary", Prefix: PublicFixtureSummaryName, Class: HabitatClassExport, Status: HabitatStatusActive, TopLevelFile: true, Backup: true},
		active("lock", "lock", HabitatClassTransient, true, false, false),
		active("config", "config", HabitatClassConfig, true, true, false),
		child("config.overlays", "config/overlays", HabitatClassConfig, true, true, false),
		child("config.harnesses", "config/harnesses", HabitatClassConfig, true, true, false),
		child("config.adapters", "config/adapters", HabitatClassConfig, true, true, false),
		active("contracts", "contracts", HabitatClassAuthority, true, true, false),
		child("contracts.effective", "contracts/effective", HabitatClassAuthority, true, true, false),
		child("contracts.schemas", "contracts/schemas", HabitatClassAuthority, true, true, false),
		active("sources", "sources", HabitatClassEvidence, true, true, false),
		child("sources.manifests", "sources/manifests", HabitatClassEvidence, true, true, false),
		active("events", "events", HabitatClassEvidence, true, true, false),
		active("work", "work", HabitatClassOperational, true, true, false),
		child("work.items", "work/work", HabitatClassOperational, true, true, false),
		child("work.runs", "work/runs", HabitatClassOperational, true, true, false),
		child("work.leases", "work/leases", HabitatClassOperational, true, true, false),
		child("work.checkpoints", "work/checkpoints", HabitatClassOperational, true, true, false),
		child("work.transactions", "work/transactions", HabitatClassOperational, true, true, false),
		active("run-attempts", "run_attempts", HabitatClassEvidence, true, true, false),
		// content-post-authorizations/<draft_id>.json embeds the approved
		// draft's ActionIntent + PolicyDecision + body ref for the S4 act-post
		// consumer; modeled on action-execution-plans, owned from birth.
		active("content-post-authorizations", "content-post-authorizations", HabitatClassOperational, false, true, false),
		active("actuator", "actuator", HabitatClassOperational, true, true, false),
		child("actuator.budget", "actuator/budget", HabitatClassOperational, true, true, false),
		child("actuator.outbox", "actuator/outbox", HabitatClassOperational, true, true, false),
		child("actuator.signal-checks", "actuator/signal-checks", HabitatClassOperational, true, true, false),
		// profiles/overlay.json is the home-side operator overlay candidate
		// internal/profiles resolves before the repo-side private overlay.
		active("profiles", "profiles", HabitatClassConfig, false, true, false),
		// recovery/ holds the content-addressed worktree-retirement recovery
		// tars (internal/authority records their absolute paths + digests);
		// retirement proofs must stay resolvable, so the root is evidence.
		active("recovery", "recovery", HabitatClassEvidence, false, true, false),
		// secrets/ custodies live lane credentials (agent tokens, env bodies).
		// Backup=false is deliberate: credentials must NEVER flow into backup
		// bundles — a lost secret is re-provisioned, not restored. Scaffold=false
		// like profiles: only lanes that custody credentials create it.
		active("secrets", "secrets", HabitatClassConfig, false, false, false),
		active("context", "context", HabitatClassOperational, true, true, false),
		child("context.requests", "context/requests", HabitatClassOperational, true, true, false),
		child("context.packs", "context/packs", HabitatClassOperational, true, true, false),
		child("context.injections", "context/injections", HabitatClassOperational, true, true, false),
		active("inbox", "inbox", HabitatClassOperational, true, true, false),
		child("inbox.knowledge-extractions", "inbox/knowledge-extractions", HabitatClassOperational, true, true, false),
		child("inbox.parked", "inbox/parked", HabitatClassOperational, true, true, false),
		child("inbox.rejected", "inbox/rejected", HabitatClassOperational, true, true, false),
		child("inbox.contradictions", "inbox/contradictions", HabitatClassOperational, true, true, false),
		active("ledger", "ledger", HabitatClassAuthority, true, true, false),
		active("observed", "observed", HabitatClassEvidence, true, true, false),
		child("observed.tool-output", "observed/tool-output", HabitatClassRebuildable, true, false, true),
		child("observed.findings", "observed/findings", HabitatClassEvidence, true, true, false),
		child("observed.activity", "observed/activity", HabitatClassEvidence, true, true, false),
		active("registries", "registries", HabitatClassAuthority, true, true, false),
		active("artifacts", "artifacts", HabitatClassEvidence, true, true, false),
		child("artifacts.sha256", "artifacts/sha256", HabitatClassEvidence, true, true, false),
		child("artifacts.refs", "artifacts/refs", HabitatClassEvidence, true, true, false),
		active("indexes", "indexes", HabitatClassRebuildable, true, false, true),
		active("projections", "projections", HabitatClassRebuildable, true, false, true),
		child("projections.account-admission", "projections/account-projection.json", HabitatClassOperational, false, true, false),
		child("projections.narrate-thread-seen", "projections/narrate-thread-seen.json", HabitatClassOperational, false, true, false),
		child("projections.orient-cursor", "projections/orient-operator-last-seq.json", HabitatClassOperational, false, true, false),
		child("projections.director-runs", "projections/director/runs", HabitatClassEvidence, false, true, false),
		child("projections.source-intake-records", "projections/source-intake/reports", HabitatClassEvidence, false, true, false),
		active("reviews", "reviews", HabitatClassEvidence, true, true, false),
		active("archives", "archives", HabitatClassEvidence, true, true, false),
		active("exports", "exports", HabitatClassExport, true, false, false),
		active("tmp", "tmp", HabitatClassTransient, true, false, true),

		bridge("legacy.work.action-execution-plans", "action-execution-plans", "work/plans/action-execution"),
		bridge("legacy.work.advance-plans", "advance-plans", "work/plans/advance"),
		bridge("legacy.reviews.bench-suites", "bench-suites", "reviews/bench-suites"),
		bridge("legacy.work.claim-plans", "claim-plans", "work/plans/claim"),
		bridge(HabitatClosePlans, "close-plans", "work/plans/close"),
		bridge("legacy.work.continue-runs", "continue-runs", "work/runs/continue"),
		bridge("legacy.work.continues", "continues", "work/continuations"),
		bridge("legacy.work.create-plans", "create-plans", "work/plans/create"),
		bridge("legacy.work.credit-harvest", "credit-harvest", "work/runs/credit-harvest"),
		bridge("legacy.ledger.decisions", "decisions", "ledger/decisions"),
		bridge("legacy.work.dispatch-advances", "dispatch-advances", "work/dispatch/advances"),
		bridge("legacy.work.dispatch-claims", "dispatch-claims", "work/dispatch/claims"),
		bridge("legacy.work.dispatch-close-plans", "dispatch-close-plans", "work/dispatch/close-plans"),
		bridge("legacy.work.dispatch-closes", "dispatch-closes", "work/dispatch/closes"),
		bridge("legacy.work.dispatch-plans", "dispatch-plans", "work/dispatch/plans"),
		bridge("legacy.work.dispatch-step-runs", "dispatch-step-runs", "work/runs/dispatch-step"),
		bridge("legacy.work.dispatch-steps", "dispatch-steps", "work/dispatch/steps"),
		bridge("legacy.work.distill", "distill", "work/runs/distill"),
		bridge("legacy.sources.evidence", "evidence", "sources/evidence"),
		bridge("legacy.sources.imports", "imports", "sources/imports"),
		bridge("legacy.sources.manifests", "registries/source-manifests", "sources/manifests"),
		bridge("legacy.registries.lane-beliefs", "lanebeliefs", "registries/lane-beliefs"),
		bridge("legacy.lock", "locks", "lock"),
		bridge("legacy.events.logs", "logs", "events/logs"),
		bridge("legacy.context.packs", "packs", "context/packs"),
		bridge("legacy.work.panel-runs", "panel-runs", "work/runs/panel"),
		bridge("legacy.config.private", "private", "config/private"),
		bridge("legacy.reviews.reports", "reports", "reviews/reports"),
		bridge("legacy.reviews.fixtures", "reviewed-fixtures", "reviews/fixtures"),
		bridge("legacy.work.run-observations", "run-observations", "work/run-observations"),
		bridge("legacy.work.runs", "runs", "work/runs"),
		bridge("legacy.registries.runtime", "runtime", "registries/runtime-bindings"),
		bridge("legacy.artifacts.snapshots", "snapshots", "artifacts/snapshots"),
		bridge("legacy.sources.state", "state", "sources/state"),
		bridge("legacy.work.transactions", "transactions", "work/transactions"),
	}
	for _, dir := range candidates.AllInboxDirs() {
		entries = append(entries, child("inbox.candidate."+strings.ReplaceAll(dir, "/", "."), dir, HabitatClassOperational, true, true, false))
	}
	if err := validateHabitatEntries(entries); err != nil {
		panic(err)
	}
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i].Prefix) == len(entries[j].Prefix) {
			return entries[i].Prefix < entries[j].Prefix
		}
		return len(entries[i].Prefix) > len(entries[j].Prefix)
	})
	return entries
}

func validateHabitatEntries(entries []HabitatEntry) error {
	ids := map[string]bool{}
	prefixes := map[string]bool{}
	for _, entry := range entries {
		if entry.ID == "" || entry.Prefix == "" || filepath.IsAbs(entry.Prefix) || filepath.ToSlash(filepath.Clean(entry.Prefix)) != entry.Prefix {
			return fmt.Errorf("invalid habitat entry: %+v", entry)
		}
		if ids[entry.ID] || prefixes[entry.Prefix] {
			return fmt.Errorf("duplicate habitat entry id or prefix: %+v", entry)
		}
		ids[entry.ID], prefixes[entry.Prefix] = true, true
		if entry.Status != HabitatStatusActive && entry.Status != HabitatStatusBridge {
			return fmt.Errorf("habitat %s has invalid status %q", entry.ID, entry.Status)
		}
		if entry.Status == HabitatStatusBridge && (entry.CanonicalPrefix == "" || entry.RetirementCondition == "" || entry.PlacementAllowed || entry.Scaffold) {
			return fmt.Errorf("habitat bridge %s must expose destination and retirement debt without becoming canonical", entry.ID)
		}
		if entry.Compact && entry.Class != HabitatClassRebuildable && entry.Class != HabitatClassTransient {
			return fmt.Errorf("habitat %s is behavior-bearing but compactable", entry.ID)
		}
		if entry.TopLevelFile && (strings.Contains(entry.Prefix, "/") || entry.Scaffold || entry.PlacementAllowed || entry.Compact) {
			return fmt.Errorf("top-level habitat file %s has directory policy", entry.ID)
		}
	}
	return nil
}

func HabitatEntries() []HabitatEntry {
	return append([]HabitatEntry(nil), habitatEntries...)
}

func HabitatForPath(rel string) (HabitatEntry, bool) {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	if rel == "." || rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "../") {
		return HabitatEntry{}, false
	}
	for _, entry := range habitatEntries {
		if rel == entry.Prefix || strings.HasPrefix(rel, entry.Prefix+"/") {
			return entry, true
		}
	}
	return HabitatEntry{}, false
}

func HabitatDirs() []string {
	var dirs []string
	for _, entry := range habitatEntries {
		if entry.Scaffold {
			dirs = append(dirs, entry.Prefix)
		}
	}
	sort.Strings(dirs)
	return dirs
}

func HabitatPlacementDirs() []string {
	var dirs []string
	for _, entry := range habitatEntries {
		if entry.PlacementAllowed && !entry.TopLevelFile && !strings.Contains(entry.Prefix, "/") {
			dirs = append(dirs, entry.Prefix)
		}
	}
	sort.Strings(dirs)
	return dirs
}

func HabitatPath(homePath, id string, elems ...string) (string, error) {
	var found *HabitatEntry
	for i := range habitatEntries {
		if habitatEntries[i].ID == id {
			entry := habitatEntries[i]
			found = &entry
			break
		}
	}
	if found == nil {
		return "", fmt.Errorf("unknown habitat id %q", id)
	}
	parts := []string{homePath, filepath.FromSlash(found.Prefix)}
	for _, elem := range elems {
		clean := filepath.Clean(strings.TrimSpace(elem))
		if clean == "." || clean == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("invalid habitat path segment %q", elem)
		}
		parts = append(parts, clean)
	}
	return filepath.Join(parts...), nil
}

func InspectHabitat(homePath string) (HabitatInventory, error) {
	entries, err := os.ReadDir(homePath)
	if err != nil {
		if os.IsNotExist(err) {
			return HabitatInventory{OK: true}, nil
		}
		return HabitatInventory{}, err
	}
	knownRoots := map[string]HabitatEntry{}
	knownFiles := map[string]HabitatEntry{}
	for _, entry := range habitatEntries {
		if entry.TopLevelFile {
			knownFiles[entry.Prefix] = entry
			continue
		}
		root := strings.Split(entry.Prefix, "/")[0]
		if _, exists := knownRoots[root]; !exists || entry.Prefix == root {
			knownRoots[root] = entry
		}
	}
	var inventory HabitatInventory
	for _, entry := range entries {
		if entry.Name() == ManifestName && entry.Type().IsRegular() {
			continue
		}
		if _, ok := knownFiles[entry.Name()]; ok && entry.Type().IsRegular() {
			continue
		}
		owned, ok := knownRoots[entry.Name()]
		if !ok || !entry.IsDir() {
			if entry.IsDir() {
				inventory.UnknownRoots = append(inventory.UnknownRoots, entry.Name())
			} else {
				inventory.UnknownEntries = append(inventory.UnknownEntries, entry.Name())
			}
			continue
		}
		if owned.Status == HabitatStatusBridge {
			inventory.BridgeRoots = append(inventory.BridgeRoots, entry.Name())
		}
	}
	sort.Strings(inventory.UnknownRoots)
	sort.Strings(inventory.UnknownEntries)
	sort.Strings(inventory.BridgeRoots)
	inventory.OK = len(inventory.UnknownRoots) == 0 && len(inventory.UnknownEntries) == 0
	return inventory, nil
}

func habitatBackupIncluded(rel string, isDir bool) bool {
	if rel == ManifestName {
		return true
	}
	entry, ok := HabitatForPath(rel)
	if ok && entry.Backup {
		return true
	}
	if !isDir {
		return false
	}
	prefix := strings.TrimSuffix(filepath.ToSlash(rel), "/") + "/"
	for _, candidate := range habitatEntries {
		if candidate.Backup && strings.HasPrefix(candidate.Prefix, prefix) {
			return true
		}
	}
	return false
}

func habitatBackupExcludedRoots() []string {
	var out []string
	for _, entry := range habitatEntries {
		if entry.TopLevelFile || strings.Contains(entry.Prefix, "/") || entry.Backup {
			continue
		}
		out = append(out, entry.Prefix)
	}
	sort.Strings(out)
	return out
}

func habitatCompactRoots(rebuildable bool) []string {
	var out []string
	for _, entry := range habitatEntries {
		if entry.TopLevelFile || !entry.Compact || strings.Contains(entry.Prefix, "/") {
			continue
		}
		if entry.Class == HabitatClassRebuildable && !rebuildable {
			continue
		}
		out = append(out, entry.Prefix)
	}
	sort.Strings(out)
	return out
}

func habitatCompactable(rel string, rebuildable bool) bool {
	entry, ok := HabitatForPath(rel)
	if !ok || !entry.Compact {
		return false
	}
	return entry.Class == HabitatClassTransient || (rebuildable && entry.Class == HabitatClassRebuildable)
}
