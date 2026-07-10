// Package autonomy owns the pure, content-addressed proof that route and
// terminal-outcome evidence satisfies one class-scoped admission grant. It is
// nested under routeevidence so routing remains one owner, but deliberately
// imports neither the parent routeevidence package nor work: work can recheck
// this proof at AdmitAuto without creating work -> routeevidence -> writeback
// -> work.
package autonomy

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"compound/internal/envelope"
	homepkg "compound/internal/home"

	"gopkg.in/yaml.v3"
)

const (
	SchemaVersion              = "compound.autonomy_eligibility.v0.1"
	AuthorityRef               = "compound://artifacts/autonomy-eligibility"
	terminalSampleLookbackDays = 30
)

type GrantSnapshot struct {
	DecisionID         string `json:"decision_id"`
	ClaimID            string `json:"claim_id"`
	GrantRef           string `json:"grant_ref"`
	ClaimRef           string `json:"claim_ref"`
	ReviewedBy         string `json:"reviewed_by"`
	Class              string `json:"class"`
	MinTerminalSamples int    `json:"min_terminal_samples"`
	MinWindowDays      int    `json:"min_window_days"`
	MaxFailures        int    `json:"max_failures"`
}

type LaneSnapshot struct {
	ModelID    string `json:"model_id"`
	Tier       string `json:"tier"`
	ClaimLevel string `json:"claim_level,omitempty"`
	GateRole   string `json:"gate_role"`
}

type RoutePlanSnapshot struct {
	ID                string         `json:"id"`
	AuthorityPath     string         `json:"authority_path"`
	SourceAuthorityID string         `json:"source_authority_id,omitempty"`
	SourceRevision    string         `json:"source_revision"`
	CapabilityClass   string         `json:"capability_class"`
	PrivacySensitive  bool           `json:"privacy_sensitive"`
	Chain             []LaneSnapshot `json:"chain"`
}

type Sample struct {
	WorkID              string `json:"work_id"`
	WorkSHA256          string `json:"work_sha256"`
	ReceiptID           string `json:"receipt_id,omitempty"`
	ReceiptSHA256       string `json:"receipt_sha256,omitempty"`
	ReceiptCreatedAt    string `json:"receipt_created_at,omitempty"`
	ReceiptOutcome      string `json:"receipt_outcome,omitempty"`
	ReceiptChanged      bool   `json:"receipt_changed"`
	RoutePlanID         string `json:"route_plan_id,omitempty"`
	RouteEvidenceID     string `json:"route_evidence_id,omitempty"`
	RouteEvidenceSHA256 string `json:"route_evidence_sha256,omitempty"`
	RouteStatus         string `json:"route_status,omitempty"`
	ModelID             string `json:"model_id,omitempty"`
	GateRole            string `json:"gate_role,omitempty"`
	Valid               bool   `json:"valid"`
	InvalidReason       string `json:"invalid_reason,omitempty"`
}

type Artifact struct {
	SchemaVersion              string            `json:"schema_version"`
	ID                         string            `json:"id"`
	GeneratedAt                string            `json:"generated_at"`
	Status                     string            `json:"status"`
	Class                      string            `json:"class"`
	CapabilityClass            string            `json:"capability_class"`
	Grant                      GrantSnapshot     `json:"grant"`
	RoutePlan                  RoutePlanSnapshot `json:"route_plan"`
	CohortClass                string            `json:"cohort_class"`
	LookbackDays               int               `json:"lookback_days"`
	LookbackStart              string            `json:"lookback_start"`
	LookbackEnd                string            `json:"lookback_end"`
	ExcludedStaleTerminalCount int               `json:"excluded_stale_terminal_count"`
	TerminalSampleCount        int               `json:"terminal_sample_count"`
	FailureCount               int               `json:"failure_count"`
	WindowStart                string            `json:"window_start,omitempty"`
	WindowEnd                  string            `json:"window_end,omitempty"`
	WindowSeconds              int64             `json:"window_seconds"`
	Samples                    []Sample          `json:"samples"`
	Reasons                    []string          `json:"reasons,omitempty"`
	EvidenceRefs               []string          `json:"evidence_refs"`
}

type workSnapshot struct {
	Work envelope.WorkEnvelope
	Raw  []byte
}

type routeRow struct {
	SchemaVersion  string `json:"schema_version"`
	ID             string `json:"id"`
	CreatedAt      string `json:"created_at"`
	RequestID      string `json:"request_id"`
	WorkID         string `json:"work_id,omitempty"`
	ClientProfile  string `json:"client_profile"`
	Protocol       string `json:"protocol"`
	Status         string `json:"status"`
	SelectedTarget struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Protocol string `json:"protocol"`
	} `json:"selected_target"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

type routeRaw struct {
	Row routeRow
	Raw []byte
}

type receiptEvidence struct {
	Refs       []string
	TerminalAt string
}

type decidingRouteBinding struct {
	ClientProfile string
	Protocol      string
	Provider      string
}

type modelManifest struct {
	Models map[string]struct {
		Tier         string `yaml:"tier"`
		Capabilities map[string]struct {
			Level string `yaml:"level"`
		} `yaml:"capabilities"`
	} `yaml:"models"`
	RoutingDefaults map[string][]string `yaml:"routing_defaults"`
}

func GateRole(class, tier, level string) string {
	switch {
	case level == "avoid":
		return "avoid"
	case level == "assumed":
		return "supervised_only"
	case tier == "free" && (class == "judge-critique" || class == "certify"):
		return "tripwire_only"
	case level == "":
		return "review_missing_claim"
	default:
		return "eligible"
	}
}

func RoutePlanID(capabilityClass string, privacySensitive bool, authorityPath, sourceRevision string) string {
	return idFor("crp_", capabilityClass, fmt.Sprint(privacySensitive), authorityPath, sourceRevision)
}

func Compile(homePath string, grant GrantSnapshot, plan RoutePlanSnapshot, now time.Time) (Artifact, error) {
	if now.IsZero() {
		now = time.Now()
	}
	evaluatedAt := now.UTC()
	cohortClass := strings.TrimSpace(grant.Class)
	lookbackStart := evaluatedAt.Add(-terminalSampleLookbackDays * 24 * time.Hour)
	artifact := Artifact{
		SchemaVersion:   SchemaVersion,
		GeneratedAt:     evaluatedAt.Format(time.RFC3339),
		Status:          "ineligible",
		Class:           cohortClass,
		CapabilityClass: strings.TrimSpace(plan.CapabilityClass),
		Grant:           grant,
		RoutePlan:       plan,
		CohortClass:     cohortClass,
		LookbackDays:    terminalSampleLookbackDays,
		LookbackStart:   lookbackStart.Format(time.RFC3339),
		LookbackEnd:     evaluatedAt.Format(time.RFC3339),
		Samples:         []Sample{},
		EvidenceRefs:    baseEvidenceRefs(grant, plan),
	}
	if reason, err := validateGrantSnapshot(grant); err != nil {
		return Artifact{}, err
	} else if reason != "" {
		artifact.Reasons = append(artifact.Reasons, reason)
	}
	if reason, err := validateRoutePlanSnapshot(homePath, plan); err != nil {
		return Artifact{}, err
	} else if reason != "" {
		artifact.Reasons = append(artifact.Reasons, reason)
	}

	works, err := loadWorks(homePath)
	if err != nil {
		return Artifact{}, err
	}
	receipts, err := receiptEvidenceByWork(homePath)
	if err != nil {
		return Artifact{}, err
	}
	routes, err := loadRouteRows(homePath)
	if err != nil {
		return Artifact{}, err
	}

	var candidates []Sample
	for _, ws := range works {
		if autonomyClassOf(ws.Work) != artifact.Class || !terminalWorkState(ws.Work.State) {
			continue
		}
		sample := buildSample(homePath, ws, receipts[ws.Work.ID], routes, artifact.Class, plan)
		if terminalSamplePredates(sample, lookbackStart) {
			artifact.ExcludedStaleTerminalCount++
			continue
		}
		candidates = append(candidates, sample)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ReceiptCreatedAt == candidates[j].ReceiptCreatedAt {
			return candidates[i].WorkID > candidates[j].WorkID
		}
		return candidates[i].ReceiptCreatedAt > candidates[j].ReceiptCreatedAt
	})
	limit := grant.MinTerminalSamples
	if limit > len(candidates) {
		limit = len(candidates)
	}
	artifact.Samples = append(artifact.Samples, candidates[:limit]...)
	artifact.TerminalSampleCount = len(artifact.Samples)
	if artifact.TerminalSampleCount < grant.MinTerminalSamples {
		artifact.Reasons = append(artifact.Reasons, fmt.Sprintf("terminal samples %d below minimum %d", artifact.TerminalSampleCount, grant.MinTerminalSamples))
	}

	var sampleTimes []time.Time
	for _, sample := range artifact.Samples {
		artifact.EvidenceRefs = append(artifact.EvidenceRefs, sample.ReceiptID, sample.RouteEvidenceID)
		if !sample.Valid {
			artifact.Reasons = append(artifact.Reasons, sample.WorkID+": "+sample.InvalidReason)
		}
		if sample.ReceiptOutcome != "completed" || !sample.ReceiptChanged {
			artifact.FailureCount++
		}
		if parsed, err := time.Parse(time.RFC3339, sample.ReceiptCreatedAt); err == nil {
			sampleTimes = append(sampleTimes, parsed)
		}
	}
	if len(sampleTimes) > 0 {
		sort.Slice(sampleTimes, func(i, j int) bool { return sampleTimes[i].Before(sampleTimes[j]) })
		artifact.WindowStart = sampleTimes[0].UTC().Format(time.RFC3339)
		artifact.WindowEnd = sampleTimes[len(sampleTimes)-1].UTC().Format(time.RFC3339)
		artifact.WindowSeconds = int64(sampleTimes[len(sampleTimes)-1].Sub(sampleTimes[0]).Seconds())
	}
	minimumWindow := int64(grant.MinWindowDays) * int64((24 * time.Hour).Seconds())
	if artifact.WindowSeconds < minimumWindow {
		artifact.Reasons = append(artifact.Reasons, fmt.Sprintf("window %ds below minimum %d days", artifact.WindowSeconds, grant.MinWindowDays))
	}
	if artifact.FailureCount > grant.MaxFailures {
		artifact.Reasons = append(artifact.Reasons, fmt.Sprintf("failures %d exceed maximum %d", artifact.FailureCount, grant.MaxFailures))
	}
	artifact.Reasons = uniqueSorted(artifact.Reasons)
	artifact.EvidenceRefs = uniqueSorted(artifact.EvidenceRefs)
	if len(artifact.Reasons) == 0 {
		artifact.Status = "eligible"
	}
	artifact.ID = contentID(artifact)
	return artifact, nil
}

func Record(homePath string, artifact Artifact) (string, error) {
	if err := Validate(artifact); err != nil {
		return "", err
	}
	dir := filepath.Join(homePath, "artifacts", "autonomy-eligibility")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, artifact.ID+".json")
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return "", err
	}
	if existing, err := os.ReadFile(path); err == nil {
		var prior Artifact
		if json.Unmarshal(existing, &prior) == nil && prior.ID == artifact.ID {
			return path, nil
		}
		return "", fmt.Errorf("autonomy eligibility path collision for %s", artifact.ID)
	}
	return path, os.WriteFile(path, append(data, '\n'), 0o644)
}

func LoadAndRevalidate(homePath, ref, expectedClass, expectedRoutePlanID string) (Artifact, error) {
	id := strings.TrimSpace(ref)
	id = strings.TrimPrefix(id, "eligibility:")
	if !strings.HasPrefix(id, "autelig_") {
		return Artifact{}, fmt.Errorf("eligibility ref %q is not an autelig artifact", ref)
	}
	path := filepath.Join(homePath, "artifacts", "autonomy-eligibility", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("read eligibility artifact %s: %w", id, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var artifact Artifact
	if err := dec.Decode(&artifact); err != nil {
		return Artifact{}, fmt.Errorf("decode eligibility artifact %s: %w", id, err)
	}
	if err := Validate(artifact); err != nil {
		return Artifact{}, err
	}
	if artifact.ID != id {
		return Artifact{}, fmt.Errorf("eligibility artifact path id %s does not match content id %s", id, artifact.ID)
	}
	if artifact.Class != expectedClass {
		return Artifact{}, fmt.Errorf("eligibility class %q does not match work class %q", artifact.Class, expectedClass)
	}
	if artifact.RoutePlan.ID != expectedRoutePlanID {
		return Artifact{}, fmt.Errorf("eligibility route plan %q does not match work route plan %q", artifact.RoutePlan.ID, expectedRoutePlanID)
	}
	generatedAt, err := time.Parse(time.RFC3339, artifact.GeneratedAt)
	if err != nil {
		return Artifact{}, fmt.Errorf("eligibility generated_at invalid: %w", err)
	}
	rebuilt, err := Compile(homePath, artifact.Grant, artifact.RoutePlan, generatedAt)
	if err != nil {
		return Artifact{}, err
	}
	before, _ := json.Marshal(artifact)
	after, _ := json.Marshal(rebuilt)
	if !bytes.Equal(before, after) {
		return Artifact{}, fmt.Errorf("eligibility artifact %s is stale or source evidence changed", artifact.ID)
	}
	return artifact, nil
}

func Validate(artifact Artifact) error {
	var problems []string
	if artifact.SchemaVersion != SchemaVersion {
		problems = append(problems, "schema_version must be "+SchemaVersion)
	}
	if artifact.Status != "eligible" && artifact.Status != "ineligible" {
		problems = append(problems, "status must be eligible|ineligible")
	}
	if strings.TrimSpace(artifact.ID) == "" || !strings.HasPrefix(artifact.ID, "autelig_") {
		problems = append(problems, "id must start with autelig_")
	} else if expected := contentID(artifact); artifact.ID != expected {
		problems = append(problems, fmt.Sprintf("content digest mismatch: expected %s", expected))
	}
	if strings.TrimSpace(artifact.Class) == "" || artifact.Class != artifact.Grant.Class {
		problems = append(problems, "class must match grant class")
	}
	if artifact.CohortClass != artifact.Class {
		problems = append(problems, "cohort_class must match artifact class")
	}
	if artifact.LookbackDays != terminalSampleLookbackDays {
		problems = append(problems, fmt.Sprintf("lookback_days must be %d", terminalSampleLookbackDays))
	}
	if artifact.ExcludedStaleTerminalCount < 0 {
		problems = append(problems, "excluded_stale_terminal_count must not be negative")
	}
	if generatedAt, err := time.Parse(time.RFC3339, artifact.GeneratedAt); err != nil {
		problems = append(problems, "generated_at must be RFC3339")
	} else {
		wantEnd := generatedAt.UTC().Format(time.RFC3339)
		wantStart := generatedAt.UTC().Add(-terminalSampleLookbackDays * 24 * time.Hour).Format(time.RFC3339)
		if artifact.LookbackStart != wantStart || artifact.LookbackEnd != wantEnd {
			problems = append(problems, "lookback bounds must derive from generated_at")
		}
	}
	if !containsExact(artifact.EvidenceRefs, terminalSampleLookbackRef(artifact.CohortClass)) {
		problems = append(problems, "evidence_refs must name the terminal-sample lookback")
	}
	if strings.TrimSpace(artifact.RoutePlan.ID) == "" || artifact.RoutePlan.ID != RoutePlanID(artifact.RoutePlan.CapabilityClass, artifact.RoutePlan.PrivacySensitive, artifact.RoutePlan.AuthorityPath, artifact.RoutePlan.SourceRevision) {
		problems = append(problems, "route plan identity missing or invalid")
	}
	if len(problems) > 0 {
		return fmt.Errorf("autonomy eligibility invalid: %s", strings.Join(problems, "; "))
	}
	return nil
}

func (artifact Artifact) Ref() string { return "eligibility:" + artifact.ID }

func (artifact Artifact) AdmissionRefs() []string {
	return uniqueSorted(append([]string{artifact.Ref()}, artifact.EvidenceRefs...))
}

func validateGrantSnapshot(grant GrantSnapshot) (string, error) {
	if strings.TrimSpace(grant.DecisionID) == "" || strings.TrimSpace(grant.ClaimID) == "" || strings.TrimSpace(grant.ReviewedBy) == "" {
		return "grant identity, claim, and reviewed_by are required", nil
	}
	if grant.GrantRef != "grant:"+grant.DecisionID || grant.ClaimRef != "claim:"+grant.ClaimID {
		return "grant or claim ref does not match grant identity", nil
	}
	if strings.TrimSpace(grant.Class) == "" || grant.MinTerminalSamples <= 0 || grant.MinWindowDays <= 0 || grant.MaxFailures < 0 || grant.MaxFailures >= grant.MinTerminalSamples {
		return "grant threshold is incomplete or unsafe", nil
	}
	return "", nil
}

func validateRoutePlanSnapshot(homePath string, plan RoutePlanSnapshot) (string, error) {
	wantPath := filepath.Join(homePath, "registries", "model-capabilities.yaml")
	if filepath.Clean(plan.AuthorityPath) != filepath.Clean(wantPath) {
		return "route plan authority path is not the current model-router authority", nil
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		return "", fmt.Errorf("read model-router authority: %w", err)
	}
	wantRevision := "sha256:" + shaHex(data)
	if plan.SourceRevision != wantRevision {
		return fmt.Sprintf("router revision %q is not current %q", plan.SourceRevision, wantRevision), nil
	}
	if plan.ID != RoutePlanID(plan.CapabilityClass, plan.PrivacySensitive, plan.AuthorityPath, plan.SourceRevision) {
		return "route plan id does not match current class/revision", nil
	}
	var manifest modelManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("parse model-router authority: %w", err)
	}
	defaults := manifest.RoutingDefaults[plan.CapabilityClass]
	var expected []LaneSnapshot
	for _, modelID := range defaults {
		model, ok := manifest.Models[modelID]
		if !ok {
			return "route plan references an unknown model", nil
		}
		if plan.PrivacySensitive && model.Tier != "local-batch" {
			continue
		}
		level := strings.TrimSpace(model.Capabilities[plan.CapabilityClass].Level)
		expected = append(expected, LaneSnapshot{ModelID: modelID, Tier: strings.TrimSpace(model.Tier), ClaimLevel: level, GateRole: GateRole(plan.CapabilityClass, strings.TrimSpace(model.Tier), level)})
	}
	if !sameLanes(plan.Chain, expected) {
		return "route plan chain does not match current model-router authority", nil
	}
	return "", nil
}

func buildSample(homePath string, ws workSnapshot, receiptEvidence receiptEvidence, routes map[string][]routeRaw, autonomyClass string, plan RoutePlanSnapshot) Sample {
	sample := Sample{WorkID: ws.Work.ID, WorkSHA256: "sha256:" + shaHex(ws.Raw), ReceiptCreatedAt: ws.Work.CreatedAt, RoutePlanID: ws.Work.RouteDecisionID}
	invalid := func(reason string) Sample {
		sample.Valid = false
		sample.InvalidReason = reason
		return sample
	}
	if strings.TrimSpace(receiptEvidence.TerminalAt) != "" {
		sample.ReceiptCreatedAt = strings.TrimSpace(receiptEvidence.TerminalAt)
	}
	if ws.Work.RouteDecisionID != plan.ID || !containsExact(ws.Work.EvidenceRefs, "route_plan:"+plan.ID) {
		return invalid("route plan does not match current plan and revision")
	}
	if len(receiptEvidence.Refs) == 0 {
		return invalid("terminal work has no receipt")
	}
	if len(receiptEvidence.Refs) != 1 {
		return invalid(fmt.Sprintf("terminal work has %d receipt refs; exactly one required", len(receiptEvidence.Refs)))
	}
	receiptID := strings.TrimSpace(receiptEvidence.Refs[0])
	receiptPath := filepath.Join(homePath, "events", receiptID+".json")
	receiptRaw, err := os.ReadFile(receiptPath)
	if err != nil {
		return invalid("receipt unreadable: " + err.Error())
	}
	var receipt envelope.Receipt
	if err := json.Unmarshal(receiptRaw, &receipt); err != nil {
		return invalid("receipt invalid: " + err.Error())
	}
	if err := receipt.Validate(); err != nil {
		return invalid("receipt invalid: " + err.Error())
	}
	sample.ReceiptID = receipt.ID
	sample.ReceiptSHA256 = "sha256:" + shaHex(receiptRaw)
	sample.ReceiptCreatedAt = receipt.CreatedAt
	sample.ReceiptOutcome = receipt.Outcome
	sample.ReceiptChanged = receiptHasGenuineDelta(receipt, routes)
	if receipt.IDs.WorkID != ws.Work.ID {
		return invalid("receipt work_id does not match")
	}
	binding, ok := decidingRouteBindingFor(autonomyClass, plan.CapabilityClass)
	if !ok {
		return invalid("no exact deciding route binding exists for autonomy class " + autonomyClass + " and capability class " + plan.CapabilityClass)
	}

	var matching []struct {
		row  routeRaw
		lane LaneSnapshot
	}
	var nonEligibleRoles []string
	for _, ref := range receipt.ProofRefs {
		if !strings.HasPrefix(strings.TrimSpace(ref), "route_") {
			continue
		}
		rows := routes[strings.TrimSpace(ref)]
		if len(rows) == 0 {
			return invalid("route evidence ref " + ref + " is missing")
		}
		if len(rows) != 1 {
			return invalid("route evidence ref " + ref + " is ambiguous")
		}
		lanes := lanesForModel(plan.Chain, rows[0].Row.SelectedTarget.Model)
		if len(lanes) > 1 {
			return invalid("selected model is ambiguous in current route plan")
		}
		if len(lanes) == 0 {
			continue
		}
		if lanes[0].GateRole != "eligible" {
			nonEligibleRoles = append(nonEligibleRoles, lanes[0].GateRole)
			continue
		}
		matching = append(matching, struct {
			row  routeRaw
			lane LaneSnapshot
		}{row: rows[0], lane: lanes[0]})
	}
	if len(matching) != 1 {
		if len(matching) == 0 && len(nonEligibleRoles) > 0 {
			return invalid("deciding lane gate role is " + strings.Join(uniqueSorted(nonEligibleRoles), ","))
		}
		return invalid(fmt.Sprintf("route evidence has %d eligible deciding rows; exactly one required", len(matching)))
	}
	chosen := matching[0]
	sample.RouteEvidenceID = chosen.row.Row.ID
	sample.RouteEvidenceSHA256 = "sha256:" + shaHex(chosen.row.Raw)
	sample.RouteStatus = chosen.row.Row.Status
	sample.ModelID = chosen.row.Row.SelectedTarget.Model
	sample.GateRole = chosen.lane.GateRole
	if strings.TrimSpace(chosen.row.Row.WorkID) == "" {
		return invalid("route evidence work_id is required")
	}
	if strings.TrimSpace(chosen.row.Row.WorkID) != ws.Work.ID {
		return invalid("route evidence work_id does not match")
	}
	if chosen.row.Row.SchemaVersion != "compound.execution_route_evidence.v0.1" {
		return invalid("route evidence schema_version is not compound.execution_route_evidence.v0.1")
	}
	if strings.TrimSpace(chosen.row.Row.RequestID) == "" {
		return invalid("route evidence request_id is required")
	}
	if countRouteRequestID(routes, chosen.row.Row.RequestID) != 1 {
		return invalid("route evidence request_id is ambiguous")
	}
	if strings.TrimSpace(chosen.row.Row.ClientProfile) != binding.ClientProfile {
		return invalid("route evidence client_profile does not match deciding profile " + binding.ClientProfile)
	}
	if strings.TrimSpace(chosen.row.Row.Protocol) != binding.Protocol || strings.TrimSpace(chosen.row.Row.SelectedTarget.Protocol) != binding.Protocol {
		return invalid("route evidence protocol does not match deciding protocol " + binding.Protocol)
	}
	if strings.TrimSpace(chosen.row.Row.SelectedTarget.Provider) != binding.Provider {
		return invalid("route evidence provider does not match deciding provider " + binding.Provider)
	}
	if !containsExact(chosen.row.Row.EvidenceRefs, "compound://registries/model-router") {
		return invalid("route evidence does not cite the model-router authority")
	}
	if reason := routeTimeBindingReason(ws.Work.CreatedAt, receipt.CreatedAt, chosen.row.Row.CreatedAt); reason != "" {
		return invalid(reason)
	}
	if chosen.row.Row.Status != "completed" {
		return invalid("route evidence status is " + chosen.row.Row.Status)
	}
	if receipt.Outcome == "completed" && !sample.ReceiptChanged {
		return invalid("completed receipt has no genuine state delta independent of route evidence")
	}
	sample.Valid = true
	return sample
}

func loadWorks(homePath string) ([]workSnapshot, error) {
	paths := []string{}
	entries, err := os.ReadDir(filepath.Join(homePath, "work", "work"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, filepath.Join(homePath, "work", "work", entry.Name()))
		}
	}
	archive, err := homepkg.LoadOperationalArchiveIndex(homePath)
	if err != nil {
		return nil, err
	}
	for _, entry := range archive {
		if entry.Kind == "work" && strings.HasPrefix(entry.OriginalPath, "work/work/") && strings.HasSuffix(entry.OriginalPath, ".json") {
			paths = append(paths, filepath.Join(homePath, filepath.FromSlash(entry.ArchivePath)))
		}
	}
	sort.Strings(paths)
	var out []workSnapshot
	seen := map[string]bool{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var work envelope.WorkEnvelope
		if err := json.Unmarshal(raw, &work); err != nil {
			return nil, fmt.Errorf("decode work %s: %w", path, err)
		}
		if err := work.Validate(); err != nil {
			return nil, err
		}
		if seen[work.ID] {
			continue
		}
		seen[work.ID] = true
		out = append(out, workSnapshot{Work: work, Raw: bytes.TrimSpace(raw)})
	}
	return out, nil
}

func receiptEvidenceByWork(homePath string) (map[string]receiptEvidence, error) {
	out := map[string]receiptEvidence{}
	path := filepath.Join(homePath, "events", "work.jsonl")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var event struct {
			TS       string   `json:"ts"`
			WorkID   string   `json:"work_id"`
			To       string   `json:"to"`
			Attached []string `json:"attached"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, err
		}
		evidence := out[event.WorkID]
		if terminalWorkState(event.To) && strings.TrimSpace(event.TS) != "" {
			evidence.TerminalAt = normalizedEvidenceTime(event.TS)
		}
		for _, ref := range event.Attached {
			if strings.HasPrefix(ref, "rec_") {
				evidence.Refs = append(evidence.Refs, strings.TrimSpace(ref))
			}
		}
		out[event.WorkID] = evidence
	}
	return out, scanner.Err()
}

func normalizedEvidenceTime(value string) string {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	if nanos, err := strconv.ParseInt(value, 10, 64); err == nil {
		return time.Unix(0, nanos).UTC().Format(time.RFC3339)
	}
	return value
}

func loadRouteRows(homePath string) (map[string][]routeRaw, error) {
	out := map[string][]routeRaw{}
	path := filepath.Join(homePath, "ledger", "execution-route-evidence.jsonl")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		raw := bytes.TrimSpace(append([]byte(nil), scanner.Bytes()...))
		if len(raw) == 0 {
			continue
		}
		var row routeRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		if strings.TrimSpace(row.ID) == "" {
			return nil, fmt.Errorf("route evidence row missing id")
		}
		out[row.ID] = append(out[row.ID], routeRaw{Row: row, Raw: raw})
	}
	return out, scanner.Err()
}

func decidingRouteBindingFor(autonomyClass, capabilityClass string) (decidingRouteBinding, bool) {
	// Evidence-threshold autonomy is intentionally deny-by-default. Each newly
	// enabled autonomy/capability pair must name the exact production caller
	// that can supply its deciding route row; a model match alone is not a
	// deciding-call identity.
	if strings.TrimSpace(autonomyClass) == "job_health" && strings.TrimSpace(capabilityClass) == "judge-critique" {
		return decidingRouteBinding{
			ClientProfile: "compound.panel.judge",
			Protocol:      "panel-lane",
			Provider:      "compound-route-plan",
		}, true
	}
	return decidingRouteBinding{}, false
}

func receiptHasGenuineDelta(receipt envelope.Receipt, routes map[string][]routeRaw) bool {
	if receipt.Outcome != "completed" {
		return false
	}
	for _, path := range receipt.StateDelta.FilesChanged {
		if strings.TrimSpace(path) != "" {
			return true
		}
	}
	refs := append([]string{}, receipt.ProofRefs...)
	refs = append(refs, receipt.StateDelta.Artifacts...)
	refs = append(refs, receipt.StateDelta.Verification...)
	refs = nonRoutingRefs(refs, routes)
	switch receipt.MoveType {
	case "memory_delta":
		memoryRefs := append([]string{}, receipt.Memory.Candidates...)
		memoryRefs = append(memoryRefs, receipt.Memory.Promoted...)
		memoryRefs = append(memoryRefs, receipt.Memory.Parked...)
		memoryRefs = append(memoryRefs, receipt.Memory.Rejected...)
		return len(nonEmptyStrings(memoryRefs)) > 0
	case "rule_delta":
		return hasAnyPrefix(refs, []string{"failure:", "failure_pattern:", "rule:", "check:", "guard:"})
	case "control_plane_repair", "outward_delta":
		return len(refs) > 0
	default:
		return false
	}
}

func nonRoutingRefs(refs []string, routes map[string][]routeRaw) []string {
	out := []string{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || isRoutingOnlyRef(ref, routes) {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func isRoutingOnlyRef(ref string, routes map[string][]routeRaw) bool {
	if len(routes[ref]) > 0 {
		return true
	}
	for _, prefix := range []string{
		"route_",
		"route:",
		"route_plan:",
		"router_revision:",
		"compound://ledger/execution-route-evidence",
		"compound://registries/model-router",
	} {
		if strings.HasPrefix(ref, prefix) {
			return true
		}
	}
	return false
}

func nonEmptyStrings(values []string) []string {
	out := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func hasAnyPrefix(values, prefixes []string) bool {
	for _, value := range values {
		for _, prefix := range prefixes {
			if strings.HasPrefix(value, prefix) {
				return true
			}
		}
	}
	return false
}

func countRouteRequestID(routes map[string][]routeRaw, requestID string) int {
	requestID = strings.TrimSpace(requestID)
	count := 0
	for _, rows := range routes {
		for _, row := range rows {
			if strings.TrimSpace(row.Row.RequestID) == requestID {
				count++
			}
		}
	}
	return count
}

func routeTimeBindingReason(workCreatedAt, receiptCreatedAt, routeCreatedAt string) string {
	workTime, err := time.Parse(time.RFC3339, strings.TrimSpace(workCreatedAt))
	if err != nil {
		return "work created_at is invalid for route binding"
	}
	receiptTime, err := time.Parse(time.RFC3339, strings.TrimSpace(receiptCreatedAt))
	if err != nil {
		return "receipt created_at is invalid for route binding"
	}
	routeTime, err := time.Parse(time.RFC3339, strings.TrimSpace(routeCreatedAt))
	if err != nil {
		return "route evidence created_at is invalid"
	}
	if routeTime.Before(workTime) {
		return "route evidence predates its work"
	}
	if routeTime.After(receiptTime) {
		return "route evidence postdates its receipt"
	}
	return ""
}

func baseEvidenceRefs(grant GrantSnapshot, plan RoutePlanSnapshot) []string {
	return uniqueSorted([]string{
		grant.GrantRef,
		grant.ClaimRef,
		terminalSampleLookbackRef(strings.TrimSpace(grant.Class)),
		"route_plan:" + plan.ID,
		"router_revision:" + plan.SourceRevision,
		fmt.Sprintf("threshold:min_terminal_samples=%d", grant.MinTerminalSamples),
		fmt.Sprintf("threshold:min_window_days=%d", grant.MinWindowDays),
		fmt.Sprintf("threshold:max_failures=%d", grant.MaxFailures),
	})
}

func terminalSamplePredates(sample Sample, cutoff time.Time) bool {
	terminalAt, err := time.Parse(time.RFC3339, strings.TrimSpace(sample.ReceiptCreatedAt))
	return err == nil && terminalAt.Before(cutoff)
}

func terminalSampleLookbackRef(class string) string {
	return fmt.Sprintf("cohort:terminal_samples:class=%s:lookback=%dd", strings.TrimSpace(class), terminalSampleLookbackDays)
}

func contentID(artifact Artifact) string {
	artifact.ID = ""
	data, _ := json.Marshal(artifact)
	return "autelig_" + shaHex(data)[:12]
}

func idFor(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(sum[:])[:12]
}

func shaHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sameLanes(a, b []LaneSnapshot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lanesForModel(lanes []LaneSnapshot, modelID string) []LaneSnapshot {
	var out []LaneSnapshot
	for _, lane := range lanes {
		if lane.ModelID == modelID {
			out = append(out, lane)
		}
	}
	return out
}

func autonomyClassOf(work envelope.WorkEnvelope) string {
	for _, ref := range work.EvidenceRefs {
		if !strings.HasPrefix(ref, "director_trigger:") {
			continue
		}
		rest := strings.TrimPrefix(ref, "director_trigger:")
		if index := strings.Index(rest, ":"); index > 0 {
			return rest[:index]
		}
	}
	return ""
}

func terminalWorkState(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled"
}

func containsExact(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = true
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
