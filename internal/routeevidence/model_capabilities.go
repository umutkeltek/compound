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

	"gopkg.in/yaml.v3"
)

const (
	ModelCapabilitiesAuthorityRef          = "compound://registries/model-router"
	ModelCapabilitiesSourceAuthoritySchema = "compound.model_capabilities_source_authority.v0.1"
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
	PrivacySensitive bool
}

type CapabilityRoutePlan struct {
	SchemaVersion     string                `json:"schema_version"`
	ID                string                `json:"id"`
	CreatedAt         string                `json:"created_at"`
	Status            string                `json:"status"`
	AuthorityRef      string                `json:"authority_ref"`
	AuthorityPath     string                `json:"authority_path"`
	SourceAuthorityID string                `json:"source_authority_id,omitempty"`
	SourceRevision    string                `json:"source_revision,omitempty"`
	CapabilityClass   string                `json:"capability_class"`
	PrivacySensitive  bool                  `json:"privacy_sensitive"`
	RoutingRule       string                `json:"routing_rule"`
	LaneJustification []string              `json:"lane_justification"`
	Chain             []CapabilityRouteLane `json:"chain"`
	Omitted           []CapabilityRouteLane `json:"omitted,omitempty"`
	Warnings          []string              `json:"warnings,omitempty"`
}

type CapabilityRouteLane struct {
	Position            int    `json:"position"`
	ModelID             string `json:"model_id"`
	Lane                string `json:"lane"`
	Tier                string `json:"tier"`
	PacketCeiling       string `json:"packet_ceiling,omitempty"`
	ClaimLevel          string `json:"claim_level,omitempty"`
	ClaimEvidence       string `json:"claim_evidence,omitempty"`
	GateRole            string `json:"gate_role"`
	SupervisionRequired bool   `json:"supervision_required,omitempty"`
	Justification       string `json:"justification"`
	OmitReason          string `json:"omit_reason,omitempty"`
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
	path := ModelCapabilitiesPath(homePath)
	data, err := os.ReadFile(path)
	if err != nil {
		return ModelCapabilitiesManifest{}, path, err
	}
	manifest, err := DecodeModelCapabilitiesYAML(data)
	if err != nil {
		return ModelCapabilitiesManifest{}, path, err
	}
	if err := validateModelCapabilities(manifest); err != nil {
		return ModelCapabilitiesManifest{}, path, err
	}
	return manifest, path, nil
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

func PlanCapabilityRoute(homePath, capabilityClass string, opts CapabilityRoutePlanOptions, now time.Time) (CapabilityRoutePlan, error) {
	capabilityClass = strings.TrimSpace(capabilityClass)
	if capabilityClass == "" {
		return CapabilityRoutePlan{}, fmt.Errorf("capability class required")
	}
	manifest, authorityPath, err := LoadModelCapabilitiesFromHome(homePath)
	if err != nil {
		return CapabilityRoutePlan{}, fmt.Errorf("load model capabilities authority: %w", err)
	}
	defaults, ok := manifest.RoutingDefaults[capabilityClass]
	if !ok || len(defaults) == 0 {
		return CapabilityRoutePlan{}, fmt.Errorf("capability class %q has no routing_defaults entry", capabilityClass)
	}
	sourceAuthorityID, sourceRevision, err := latestModelCapabilitiesSource(homePath)
	if err != nil {
		return CapabilityRoutePlan{}, err
	}
	plan := CapabilityRoutePlan{
		SchemaVersion:     CapabilityRoutePlanSchemaVersion,
		ID:                idFor("crp_", capabilityClass, fmt.Sprint(opts.PrivacySensitive), authorityPath, sourceRevision),
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

func latestModelCapabilitiesSource(homePath string) (id, revision string, err error) {
	records, err := LoadModelCapabilitiesSourceAuthority(homePath)
	if err != nil {
		return "", "", err
	}
	if len(records) == 0 {
		return "", "", nil
	}
	latest := records[len(records)-1]
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

func laneJustification(class string, position int, modelID string, model ModelCapabilityEntry, claim CapabilityClaim, role string) string {
	level := strings.TrimSpace(claim.Level)
	if level == "" {
		level = "missing"
	}
	return fmt.Sprintf("%s[%d] -> %s (%s) claim=%s role=%s", "routing_defaults["+class+"]", position, modelID, model.Tier, level, role)
}

func modelCapabilityIDs(manifest ModelCapabilitiesManifest) []string {
	ids := make([]string, 0, len(manifest.Models))
	for id := range manifest.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
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
