package routeevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
	homepkg "compound/internal/home"
	"compound/internal/reviewartifact"
	autonomyproof "compound/internal/routeevidence/autonomy"
	"compound/internal/writeback"
	"gopkg.in/yaml.v3"
)

const (
	ModelCapabilitiesAuthorityRef          = "compound://registries/model-router"
	ModelCapabilitiesSourceAuthoritySchema = "compound.model_capabilities_source_authority.v0.1"
	ModelCapabilitiesProjectionSchema      = "compound.model_capabilities_projection.v0.1"
	CapabilityRoutePlanSchemaVersion       = "compound.capability_route_plan.v0.1"

	modelCapabilitiesFile              = "model-capabilities.yaml"
	modelCapabilitiesSourceAuthority   = "model-capabilities-source-authority.jsonl"
	modelCapabilitiesImportCommandName = "route import-model-capabilities"
)

type CapabilityClaim struct {
	Level    string `json:"level" yaml:"level"`
	Evidence string `json:"evidence" yaml:"evidence"`
}

type ModelCapabilityEntry struct {
	Lane          string                     `json:"lane" yaml:"lane"`
	Harness       string                     `json:"harness,omitempty" yaml:"harness,omitempty"`
	ProviderRoute string                     `json:"provider_route,omitempty" yaml:"provider_route,omitempty"`
	EffortDefault string                     `json:"effort_default,omitempty" yaml:"effort_default,omitempty"`
	CostClass     string                     `json:"cost_class,omitempty" yaml:"cost_class,omitempty"`
	Tier          string                     `json:"tier" yaml:"tier"`
	PacketCeiling string                     `json:"packet_ceiling" yaml:"packet_ceiling"`
	Capabilities  map[string]CapabilityClaim `json:"capabilities" yaml:"capabilities"`
	Notes         string                     `json:"notes,omitempty" yaml:"notes,omitempty"`
}

type ModelCapabilitiesManifest struct {
	Version           int                             `json:"version" yaml:"version"`
	Purpose           string                          `json:"purpose" yaml:"purpose"`
	Rules             []string                        `json:"rules" yaml:"rules"`
	CapabilityClasses []string                        `json:"capability_classes" yaml:"capability_classes"`
	Models            map[string]ModelCapabilityEntry `json:"models" yaml:"models"`
	RoutingDefaults   map[string][]string             `json:"routing_defaults" yaml:"routing_defaults"`
}

type ModelCapabilitiesProjection struct {
	SchemaVersion       string                          `json:"schema_version"`
	GeneratedAt         string                          `json:"generated_at"`
	AuthorityRef        string                          `json:"authority_ref"`
	AuthorityPath       string                          `json:"authority_path"`
	SourceAuthorityID   string                          `json:"source_authority_id,omitempty"`
	SourceRevision      string                          `json:"source_revision,omitempty"`
	ModelCount          int                             `json:"model_count"`
	CapabilityClasses   []string                        `json:"capability_classes"`
	RoutingDefaultCount int                             `json:"routing_default_count"`
	Lanes               []ModelCapabilityProjectionLane `json:"lanes"`
}

type ModelCapabilityProjectionLane struct {
	ModelID         string                           `json:"model_id"`
	Label           string                           `json:"label"`
	Lane            string                           `json:"lane"`
	Harness         string                           `json:"harness,omitempty"`
	ProviderRoute   string                           `json:"provider_route,omitempty"`
	EffortDefault   string                           `json:"effort_default,omitempty"`
	CostClass       string                           `json:"cost_class,omitempty"`
	Tier            string                           `json:"tier"`
	PacketCeiling   string                           `json:"packet_ceiling,omitempty"`
	Capabilities    []ModelCapabilityProjectionClaim `json:"capabilities,omitempty"`
	RoutingDefaults []string                         `json:"routing_defaults,omitempty"`
	Notes           string                           `json:"notes,omitempty"`
}

type ModelCapabilityProjectionClaim struct {
	CapabilityClass string `json:"capability_class"`
	Level           string `json:"level"`
	Evidence        string `json:"evidence,omitempty"`
	GateRole        string `json:"gate_role"`
}

type ModelCapabilitiesImportReport struct {
	AuthorityRef         string                           `json:"authority_ref"`
	AuthorityPath        string                           `json:"authority_path"`
	SourceAuthorityID    string                           `json:"source_authority_id"`
	SourceAuthority      ModelCapabilitiesSourceAuthority `json:"source_authority"`
	ModelCount           int                              `json:"model_count"`
	CapabilityClassCount int                              `json:"capability_class_count"`
	RoutingDefaultCount  int                              `json:"routing_default_count"`
}

type ModelCapabilitiesSourceAuthority struct {
	SchemaVersion       string   `json:"schema_version"`
	ID                  string   `json:"id"`
	CreatedAt           string   `json:"created_at"`
	SourcePath          string   `json:"source_path"`
	SourceRevision      string   `json:"source_revision"`
	ImportedVia         string   `json:"imported_via"`
	AuthorityRef        string   `json:"authority_ref"`
	AuthorityStatus     string   `json:"authority_status"`
	SourceRole          string   `json:"source_role"`
	Retires             string   `json:"retires"`
	RetirementCondition string   `json:"retirement_condition"`
	Rollback            string   `json:"rollback"`
	ModelIDs            []string `json:"model_ids"`
	CapabilityClasses   []string `json:"capability_classes"`
	RoutingDefaultCount int      `json:"routing_default_count"`
	EvidenceRefs        []string `json:"evidence_refs"`
}

type CapabilityRoutePlanOptions struct {
	PrivacySensitive            bool
	SuppressPendingObservations bool
}

type CapabilityRoutePlan struct {
	SchemaVersion       string                    `json:"schema_version"`
	ID                  string                    `json:"id"`
	CreatedAt           string                    `json:"created_at"`
	Status              string                    `json:"status"`
	AuthorityRef        string                    `json:"authority_ref"`
	AuthorityPath       string                    `json:"authority_path"`
	SourceAuthorityID   string                    `json:"source_authority_id,omitempty"`
	SourceRevision      string                    `json:"source_revision,omitempty"`
	CapabilityClass     string                    `json:"capability_class"`
	PrivacySensitive    bool                      `json:"privacy_sensitive"`
	RoutingRule         string                    `json:"routing_rule"`
	LaneJustification   []string                  `json:"lane_justification"`
	Chain               []CapabilityRouteLane     `json:"chain"`
	Omitted             []CapabilityRouteLane     `json:"omitted,omitempty"`
	PendingObservations []PendingRouteObservation `json:"pending_observations,omitempty"`
	Warnings            []string                  `json:"warnings,omitempty"`
}

type CapabilityRouteLane struct {
	Position            int    `json:"position"`
	ModelID             string `json:"model_id"`
	Lane                string `json:"lane"`
	Harness             string `json:"harness,omitempty"`
	ProviderRoute       string `json:"provider_route,omitempty"`
	EffortDefault       string `json:"effort_default,omitempty"`
	CostClass           string `json:"cost_class,omitempty"`
	Tier                string `json:"tier"`
	PacketCeiling       string `json:"packet_ceiling,omitempty"`
	ClaimLevel          string `json:"claim_level,omitempty"`
	ClaimEvidence       string `json:"claim_evidence,omitempty"`
	GateRole            string `json:"gate_role"`
	SupervisionRequired bool   `json:"supervision_required,omitempty"`
	Justification       string `json:"justification"`
	OmitReason          string `json:"omit_reason,omitempty"`
}

// PendingRouteObservation is read-only review debt visible to route planners.
// It points at existing route_observation candidates; it never mutates
// model-router, gateway, provider config, or claims.
type PendingRouteObservation struct {
	CandidateID     string   `json:"candidate_id"`
	Subject         string   `json:"subject,omitempty"`
	Statement       string   `json:"statement,omitempty"`
	Detail          string   `json:"detail,omitempty"`
	Verdict         string   `json:"verdict,omitempty"`
	EvidenceRefs    []string `json:"evidence_refs,omitempty"`
	MatchReasons    []string `json:"match_reasons"`
	NoPromoteID     string   `json:"no_promote_id,omitempty"`
	NoPromoteBasis  string   `json:"no_promote_basis,omitempty"`
	OwnerTargetKind string   `json:"owner_target_kind,omitempty"`
	OwnerTargetRef  string   `json:"owner_target_ref,omitempty"`
	ReviewCommand   string   `json:"review_command"`
}

func ImportModelCapabilitiesYAML(homePath, sourcePath string, now time.Time) (ModelCapabilitiesImportReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return ModelCapabilitiesImportReport{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(sourcePath) == "" {
		return ModelCapabilitiesImportReport{}, fmt.Errorf("source path required")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return ModelCapabilitiesImportReport{}, fmt.Errorf("read model capabilities %s: %w", sourcePath, err)
	}
	manifest, err := DecodeModelCapabilitiesYAML(data)
	if err != nil {
		return ModelCapabilitiesImportReport{}, err
	}
	if err := validateModelCapabilities(manifest); err != nil {
		return ModelCapabilitiesImportReport{}, err
	}
	var report ModelCapabilitiesImportReport
	err = homepkg.WithAutonomyAdmissionLock(homePath, func() error {
		var installErr error
		report, installErr = installModelCapabilities(homePath, sourcePath, data, manifest, now)
		return installErr
	})
	return report, err
}

func installModelCapabilities(homePath, sourcePath string, data []byte, manifest ModelCapabilitiesManifest, now time.Time) (ModelCapabilitiesImportReport, error) {
	if err := os.MkdirAll(filepath.Join(homePath, "registries"), 0o755); err != nil {
		return ModelCapabilitiesImportReport{}, err
	}
	authorityPath := ModelCapabilitiesPath(homePath)
	if err := os.WriteFile(authorityPath, data, 0o644); err != nil {
		return ModelCapabilitiesImportReport{}, err
	}
	revision := "sha256:" + digestHex(data)
	rec := ModelCapabilitiesSourceAuthority{
		SchemaVersion:       ModelCapabilitiesSourceAuthoritySchema,
		ID:                  modelCapabilitiesSourceAuthorityID(sourcePath, revision),
		CreatedAt:           rfc3339(now),
		SourcePath:          sourcePath,
		SourceRevision:      revision,
		ImportedVia:         modelCapabilitiesImportCommandName,
		AuthorityRef:        ModelCapabilitiesAuthorityRef,
		AuthorityStatus:     "compound_primary",
		SourceRole:          "evidence_only_input_after_import",
		Retires:             "source_view_authority:" + sourcePath,
		RetirementCondition: "compound route plan reads compound://registries/model-router from COMPOUND_HOME; OS generated view remains rollback/evidence input",
		Rollback:            "re-import source model-capabilities.yaml or remove COMPOUND_HOME registries/model-capabilities.yaml",
		ModelIDs:            modelCapabilityIDs(manifest),
		CapabilityClasses:   sortedStrings(manifest.CapabilityClasses),
		RoutingDefaultCount: len(manifest.RoutingDefaults),
		EvidenceRefs:        []string{revision, ModelCapabilitiesAuthorityRef},
	}
	if err := appendModelCapabilitiesSourceAuthority(homePath, rec); err != nil {
		return ModelCapabilitiesImportReport{}, err
	}
	return ModelCapabilitiesImportReport{
		AuthorityRef:         ModelCapabilitiesAuthorityRef,
		AuthorityPath:        authorityPath,
		SourceAuthorityID:    rec.ID,
		SourceAuthority:      rec,
		ModelCount:           len(manifest.Models),
		CapabilityClassCount: len(manifest.CapabilityClasses),
		RoutingDefaultCount:  len(manifest.RoutingDefaults),
	}, nil
}

func DecodeModelCapabilitiesYAML(data []byte) (ModelCapabilitiesManifest, error) {
	var manifest ModelCapabilitiesManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return ModelCapabilitiesManifest{}, fmt.Errorf("parse model capabilities: %w", err)
	}
	return manifest, nil
}

func LoadModelCapabilitiesFromHome(homePath string) (ModelCapabilitiesManifest, string, error) {
	manifest, path, _, err := loadModelCapabilitiesSnapshot(homePath)
	return manifest, path, err
}

func loadModelCapabilitiesSnapshot(homePath string) (ModelCapabilitiesManifest, string, string, error) {
	path := ModelCapabilitiesPath(homePath)
	data, err := os.ReadFile(path)
	if err != nil {
		return ModelCapabilitiesManifest{}, path, "", err
	}
	manifest, err := DecodeModelCapabilitiesYAML(data)
	if err != nil {
		return ModelCapabilitiesManifest{}, path, "", err
	}
	if err := validateModelCapabilities(manifest); err != nil {
		return ModelCapabilitiesManifest{}, path, "", err
	}
	return manifest, path, "sha256:" + digestHex(data), nil
}

func LoadModelCapabilitiesSourceAuthority(homePath string) ([]ModelCapabilitiesSourceAuthority, error) {
	data, err := os.ReadFile(ModelCapabilitiesSourceAuthorityPath(homePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []ModelCapabilitiesSourceAuthority
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec ModelCapabilitiesSourceAuthority
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parse model capabilities source authority line %d: %w", i+1, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func ProjectModelCapabilities(homePath string, now time.Time) (ModelCapabilitiesProjection, error) {
	manifest, authorityPath, authorityRevision, err := loadModelCapabilitiesSnapshot(homePath)
	if err != nil {
		return ModelCapabilitiesProjection{}, fmt.Errorf("load model capabilities authority: %w", err)
	}
	sourceAuthorityID, sourceRevision, err := latestModelCapabilitiesSource(homePath, authorityRevision)
	if err != nil {
		return ModelCapabilitiesProjection{}, err
	}

	projection := ModelCapabilitiesProjection{
		SchemaVersion:       ModelCapabilitiesProjectionSchema,
		GeneratedAt:         rfc3339(now),
		AuthorityRef:        ModelCapabilitiesAuthorityRef,
		AuthorityPath:       authorityPath,
		SourceAuthorityID:   sourceAuthorityID,
		SourceRevision:      sourceRevision,
		ModelCount:          len(manifest.Models),
		CapabilityClasses:   sortedStrings(manifest.CapabilityClasses),
		RoutingDefaultCount: len(manifest.RoutingDefaults),
		Lanes:               []ModelCapabilityProjectionLane{},
	}

	for _, modelID := range modelCapabilityIDs(manifest) {
		model := manifest.Models[modelID]
		label := strings.TrimSpace(model.Lane)
		if label == "" {
			label = modelID
		}
		lane := ModelCapabilityProjectionLane{
			ModelID:         modelID,
			Label:           label,
			Lane:            strings.TrimSpace(model.Lane),
			Harness:         strings.TrimSpace(model.Harness),
			ProviderRoute:   strings.TrimSpace(model.ProviderRoute),
			EffortDefault:   strings.TrimSpace(model.EffortDefault),
			CostClass:       strings.TrimSpace(model.CostClass),
			Tier:            strings.TrimSpace(model.Tier),
			PacketCeiling:   strings.TrimSpace(model.PacketCeiling),
			RoutingDefaults: routingDefaultsForModel(manifest.RoutingDefaults, modelID),
			Notes:           strings.TrimSpace(model.Notes),
		}
		for _, class := range sortedCapabilityClaimClasses(model.Capabilities) {
			claim := model.Capabilities[class]
			lane.Capabilities = append(lane.Capabilities, ModelCapabilityProjectionClaim{
				CapabilityClass: class,
				Level:           strings.TrimSpace(claim.Level),
				Evidence:        strings.TrimSpace(claim.Evidence),
				GateRole:        gateRole(class, model.Tier, strings.TrimSpace(claim.Level)),
			})
		}
		projection.Lanes = append(projection.Lanes, lane)
	}

	return projection, nil
}

func PlanCapabilityRoute(homePath, capabilityClass string, opts CapabilityRoutePlanOptions, now time.Time) (CapabilityRoutePlan, error) {
	capabilityClass = strings.TrimSpace(capabilityClass)
	if capabilityClass == "" {
		return CapabilityRoutePlan{}, fmt.Errorf("capability class required")
	}
	manifest, authorityPath, authorityRevision, err := loadModelCapabilitiesSnapshot(homePath)
	if err != nil {
		return CapabilityRoutePlan{}, fmt.Errorf("load model capabilities authority: %w", err)
	}
	defaults, ok := manifest.RoutingDefaults[capabilityClass]
	if !ok || len(defaults) == 0 {
		return CapabilityRoutePlan{}, fmt.Errorf("capability class %q has no routing_defaults entry", capabilityClass)
	}
	sourceAuthorityID, sourceRevision, err := latestModelCapabilitiesSource(homePath, authorityRevision)
	if err != nil {
		return CapabilityRoutePlan{}, err
	}
	plan := CapabilityRoutePlan{
		SchemaVersion:     CapabilityRoutePlanSchemaVersion,
		ID:                autonomyproof.RoutePlanID(capabilityClass, opts.PrivacySensitive, authorityPath, sourceRevision),
		CreatedAt:         rfc3339(now),
		Status:            "planned",
		AuthorityRef:      ModelCapabilitiesAuthorityRef,
		AuthorityPath:     authorityPath,
		SourceAuthorityID: sourceAuthorityID,
		SourceRevision:    sourceRevision,
		CapabilityClass:   capabilityClass,
		PrivacySensitive:  opts.PrivacySensitive,
		RoutingRule:       "routing_defaults[" + capabilityClass + "]",
	}
	for i, modelID := range defaults {
		model := manifest.Models[modelID]
		lane := capabilityRouteLane(capabilityClass, modelID, model, i)
		if opts.PrivacySensitive && model.Tier != "local-batch" {
			lane.OmitReason = "privacy-sensitive route requires local-batch tier"
			plan.Omitted = append(plan.Omitted, lane)
			continue
		}
		plan.Chain = append(plan.Chain, lane)
		plan.LaneJustification = append(plan.LaneJustification, lane.Justification)
	}
	if opts.PrivacySensitive && len(plan.Omitted) > 0 {
		plan.Warnings = append(plan.Warnings, "privacy-sensitive plan omitted non-local-batch lanes")
	}
	for _, lane := range plan.Chain {
		if lane.ClaimLevel == "" {
			plan.Warnings = append(plan.Warnings, "routing default references "+lane.ModelID+" without explicit "+capabilityClass+" claim")
		}
		if lane.SupervisionRequired {
			plan.Warnings = append(plan.Warnings, "lane "+lane.ModelID+" claim is assumed; supervised-only until promoted by evidence")
		}
		if lane.GateRole == "tripwire_only" {
			plan.Warnings = append(plan.Warnings, "free judge/critique lane "+lane.ModelID+" may object but must not gate verdicts")
		}
	}
	if !opts.SuppressPendingObservations {
		pending, err := pendingRouteObservations(homePath, capabilityClass, defaults, now)
		if err != nil {
			return CapabilityRoutePlan{}, err
		}
		plan.PendingObservations = pending
		if len(pending) > 0 {
			plan.Warnings = append(plan.Warnings, "pending route observations require owner review before routing policy changes")
		}
	}
	plan.Warnings = sortedStrings(plan.Warnings)
	if len(plan.Chain) == 0 {
		plan.Status = "blocked_no_lane"
		plan.Warnings = append(plan.Warnings, "no lane remains after policy filters")
	}
	return plan, nil
}

func ModelCapabilitiesPath(homePath string) string {
	return filepath.Join(homePath, "registries", modelCapabilitiesFile)
}

func ModelCapabilitiesSourceAuthorityPath(homePath string) string {
	return filepath.Join(homePath, "registries", modelCapabilitiesSourceAuthority)
}

func validateModelCapabilities(manifest ModelCapabilitiesManifest) error {
	var errs []string
	if manifest.Version == 0 {
		errs = append(errs, "version required")
	}
	if len(manifest.Models) == 0 {
		errs = append(errs, "models required")
	}
	if len(manifest.RoutingDefaults) == 0 {
		errs = append(errs, "routing_defaults required")
	}
	for id, model := range manifest.Models {
		if strings.TrimSpace(id) == "" {
			errs = append(errs, "model id required")
		}
		if strings.TrimSpace(model.Lane) == "" {
			errs = append(errs, "model "+id+" lane required")
		}
		if strings.TrimSpace(model.Tier) == "" {
			errs = append(errs, "model "+id+" tier required")
		}
	}
	for class, chain := range manifest.RoutingDefaults {
		if strings.TrimSpace(class) == "" {
			errs = append(errs, "routing default class required")
		}
		if len(chain) == 0 {
			errs = append(errs, "routing default "+class+" chain required")
		}
		for _, modelID := range chain {
			if _, ok := manifest.Models[modelID]; !ok {
				errs = append(errs, "routing default "+class+" references unknown model "+modelID)
			}
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("model capabilities invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

func appendModelCapabilitiesSourceAuthority(homePath string, rec ModelCapabilitiesSourceAuthority) error {
	path := ModelCapabilitiesSourceAuthorityPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func latestModelCapabilitiesSource(homePath, authorityRevision string) (id, revision string, err error) {
	records, err := LoadModelCapabilitiesSourceAuthority(homePath)
	if err != nil {
		return "", "", err
	}
	if len(records) == 0 {
		return "", "", nil
	}
	latest := records[len(records)-1]
	if latest.SourceRevision != authorityRevision {
		return "", "", fmt.Errorf("model-router source authority revision %q does not match parsed authority %q", latest.SourceRevision, authorityRevision)
	}
	return latest.ID, latest.SourceRevision, nil
}

func capabilityRouteLane(class, modelID string, model ModelCapabilityEntry, position int) CapabilityRouteLane {
	claim := model.Capabilities[class]
	level := strings.TrimSpace(claim.Level)
	role := gateRole(class, model.Tier, level)
	lane := CapabilityRouteLane{
		Position:      position,
		ModelID:       modelID,
		Lane:          model.Lane,
		Harness:       strings.TrimSpace(model.Harness),
		ProviderRoute: strings.TrimSpace(model.ProviderRoute),
		EffortDefault: strings.TrimSpace(model.EffortDefault),
		CostClass:     strings.TrimSpace(model.CostClass),
		Tier:          model.Tier,
		PacketCeiling: model.PacketCeiling,
		ClaimLevel:    level,
		ClaimEvidence: strings.TrimSpace(claim.Evidence),
		GateRole:      role,
		Justification: laneJustification(class, position, modelID, model, claim, role),
	}
	if level == "assumed" {
		lane.SupervisionRequired = true
	}
	return lane
}

func gateRole(class, tier, level string) string {
	return autonomyproof.GateRole(class, tier, level)
}

func laneJustification(class string, position int, modelID string, model ModelCapabilityEntry, claim CapabilityClaim, role string) string {
	level := strings.TrimSpace(claim.Level)
	if level == "" {
		level = "missing"
	}
	cost := strings.TrimSpace(model.CostClass)
	if cost == "" {
		cost = "unknown"
	}
	return fmt.Sprintf("%s[%d] -> %s (%s cost=%s) claim=%s role=%s", "routing_defaults["+class+"]", position, modelID, model.Tier, cost, level, role)
}

func pendingRouteObservations(homePath, capabilityClass string, modelIDs []string, now time.Time) ([]PendingRouteObservation, error) {
	dir := filepath.Join(homePath, candidates.InboxDir(candidates.RouteObservation))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	store, err := writeback.Open(homePath)
	if err != nil {
		return nil, err
	}
	var out []PendingRouteObservation
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		candidateID := strings.TrimSuffix(entry.Name(), ".json")
		c, err := readRouteCandidate(homePath, candidateID)
		if err != nil {
			return nil, err
		}
		if c.Kind != string(candidates.RouteObservation) {
			continue
		}
		matchReasons := routeObservationMatchReasons(c, capabilityClass, modelIDs)
		if len(matchReasons) == 0 {
			continue
		}
		pending := PendingRouteObservation{
			CandidateID:   c.ID,
			Subject:       c.Subject,
			Statement:     c.Statement,
			Detail:        c.Detail,
			Verdict:       c.Verdict,
			EvidenceRefs:  reviewartifact.UniqueSorted(c.EvidenceRefs),
			MatchReasons:  matchReasons,
			ReviewCommand: "compound route plan --candidate " + c.ID,
		}
		if nop, ok, err := store.ActiveNoPromote(c.ID, now); err != nil {
			return nil, err
		} else if ok {
			pending.NoPromoteID = nop.ID
			pending.NoPromoteBasis = nop.Basis
			pending.OwnerTargetKind = nop.OwnerTarget.Kind
			pending.OwnerTargetRef = nop.OwnerTarget.Ref
		}
		out = append(out, pending)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CandidateID < out[j].CandidateID
	})
	return out, nil
}

func routeObservationMatchReasons(c routeCandidate, capabilityClass string, modelIDs []string) []string {
	haystack := strings.ToLower(strings.Join([]string{c.Subject, c.Statement, c.Detail}, " "))
	var reasons []string
	if containsRouteToken(haystack, capabilityClass) {
		reasons = append(reasons, "capability_class:"+capabilityClass)
	}
	for _, modelID := range modelIDs {
		if containsRouteToken(haystack, modelID) {
			reasons = append(reasons, "model:"+modelID)
		}
	}
	if strings.Contains(haystack, "provider-ledger") || strings.Contains(haystack, "cost-coverage") {
		reasons = append(reasons, "global:provider_cost_coverage")
	}
	return reviewartifact.UniqueSorted(reasons)
}

func containsRouteToken(haystack, token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	return token != "" && strings.Contains(haystack, token)
}

func modelCapabilityIDs(manifest ModelCapabilitiesManifest) []string {
	ids := make([]string, 0, len(manifest.Models))
	for id := range manifest.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedCapabilityClaimClasses(claims map[string]CapabilityClaim) []string {
	out := make([]string, 0, len(claims))
	for class := range claims {
		if strings.TrimSpace(class) != "" {
			out = append(out, class)
		}
	}
	sort.Strings(out)
	return out
}

func routingDefaultsForModel(defaults map[string][]string, modelID string) []string {
	var out []string
	for class, chain := range defaults {
		for position, candidate := range chain {
			if candidate == modelID {
				out = append(out, fmt.Sprintf("%s[%d]", class, position))
			}
		}
	}
	sort.Strings(out)
	return out
}

func sortedStrings(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func modelCapabilitiesSourceAuthorityID(path, revision string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{path, revision, modelCapabilitiesImportCommandName}, "|")))
	return "mrcsrc_" + hex.EncodeToString(sum[:])[:12]
}

func digestHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
