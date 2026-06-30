package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	PlacementRulesAuthorityRef          = "compound://registries/placement-rules"
	PlacementSourceAuthoritySchema      = "compound.placement_source_authority.v0.1"
	placementRulesFile                  = "placement-rules.json"
	placementSourceAuthorityFile        = "placement-source-authority.jsonl"
	placementSourceAuthorityImportedVia = "policy import-placement-rules"
)

type PlacementRules struct {
	GeneratedFrom  string               `json:"generated_from,omitempty"`
	Note           string               `json:"note,omitempty"`
	OpenWriteGlobs []string             `json:"open_write_globs,omitempty"`
	Owners         []PlacementOwnerRule `json:"owners,omitempty"`
}

type PlacementOwnerRule struct {
	ID                  string   `json:"id"`
	Owner               string   `json:"owner,omitempty"`
	Status              string   `json:"status,omitempty"`
	CanonicalWriteGlobs []string `json:"canonical_write_globs,omitempty"`
	DenyGlobs           []string `json:"deny_globs,omitempty"`
	CanonFilenameGlobs  []string `json:"canon_filename_globs,omitempty"`
	WriteRoles          []string `json:"write_roles,omitempty"`
	Confidence          string   `json:"confidence,omitempty"`
}

type PlacementRulesImportReport struct {
	AuthorityRef      string                         `json:"authority_ref"`
	AuthorityPath     string                         `json:"authority_path"`
	SourceAuthorityID string                         `json:"source_authority_id"`
	SourceAuthority   PlacementSourceAuthorityRecord `json:"source_authority"`
	OwnerCount        int                            `json:"owner_count"`
	OpenWriteCount    int                            `json:"open_write_count"`
}

type PlacementSourceAuthorityRecord struct {
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
	OwnerIDs            []string `json:"owner_ids"`
	EvidenceRefs        []string `json:"evidence_refs"`
}

func LoadPlacementRules(path string) (PlacementRules, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PlacementRules{}, err
	}
	return parsePlacementRules(data)
}

func validatePlacementRules(rules PlacementRules) error {
	if len(rules.Owners) == 0 {
		return fmt.Errorf("placement rules need owners")
	}
	for _, owner := range rules.Owners {
		if strings.TrimSpace(owner.ID) == "" {
			return fmt.Errorf("placement owner id required")
		}
		confidence := strings.TrimSpace(owner.Confidence)
		if confidence != "" && confidence != "warn" && confidence != "block" {
			return fmt.Errorf("placement owner %q has invalid confidence %q", owner.ID, owner.Confidence)
		}
	}
	return nil
}

func parsePlacementRules(data []byte) (PlacementRules, error) {
	var rules PlacementRules
	if err := json.Unmarshal(data, &rules); err != nil {
		return PlacementRules{}, fmt.Errorf("parse placement rules: %w", err)
	}
	if err := validatePlacementRules(rules); err != nil {
		return PlacementRules{}, err
	}
	return rules, nil
}

func ImportPlacementRules(homePath, sourcePath string, now time.Time) (PlacementRulesImportReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return PlacementRulesImportReport{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(sourcePath) == "" {
		return PlacementRulesImportReport{}, fmt.Errorf("source path required")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return PlacementRulesImportReport{}, err
	}
	rules, err := parsePlacementRules(data)
	if err != nil {
		return PlacementRulesImportReport{}, err
	}
	if err := os.MkdirAll(filepath.Join(homePath, "registries"), 0o755); err != nil {
		return PlacementRulesImportReport{}, err
	}
	authorityPath := PlacementRulesPath(homePath)
	if err := os.WriteFile(authorityPath, data, 0o644); err != nil {
		return PlacementRulesImportReport{}, err
	}
	sourceRevision := digestHex(data)
	rec := PlacementSourceAuthorityRecord{
		SchemaVersion:       PlacementSourceAuthoritySchema,
		ID:                  placementSourceAuthorityID(sourcePath, sourceRevision),
		CreatedAt:           now.UTC().Format(time.RFC3339),
		SourcePath:          sourcePath,
		SourceRevision:      "sha256:" + sourceRevision,
		ImportedVia:         placementSourceAuthorityImportedVia,
		AuthorityRef:        PlacementRulesAuthorityRef,
		AuthorityStatus:     "compound_primary",
		SourceRole:          "evidence_only_input_after_import",
		Retires:             "source_view_authority:" + sourcePath,
		RetirementCondition: "PreToolUse reads compound://registries/placement-rules from COMPOUND_HOME; source view remains rollback/evidence input",
		Rollback:            "re-import source placement-rules.json or remove COMPOUND_HOME registries/placement-rules.json",
		OwnerIDs:            placementOwnerIDs(rules),
		EvidenceRefs:        []string{"sha256:" + sourceRevision, PlacementRulesAuthorityRef},
	}
	if err := appendPlacementSourceAuthority(homePath, rec); err != nil {
		return PlacementRulesImportReport{}, err
	}
	return PlacementRulesImportReport{
		AuthorityRef:      PlacementRulesAuthorityRef,
		AuthorityPath:     authorityPath,
		SourceAuthorityID: rec.ID,
		SourceAuthority:   rec,
		OwnerCount:        len(rules.Owners),
		OpenWriteCount:    len(rules.OpenWriteGlobs),
	}, nil
}

func LoadPlacementRulesFromHome(homePath string) (PlacementRules, error) {
	return LoadPlacementRules(PlacementRulesPath(homePath))
}

func PlacementRulesPath(homePath string) string {
	return filepath.Join(homePath, "registries", placementRulesFile)
}

func PlacementSourceAuthorityPath(homePath string) string {
	return filepath.Join(homePath, "registries", placementSourceAuthorityFile)
}

func LoadPlacementSourceAuthority(homePath string) ([]PlacementSourceAuthorityRecord, error) {
	data, err := os.ReadFile(PlacementSourceAuthorityPath(homePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []PlacementSourceAuthorityRecord
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec PlacementSourceAuthorityRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parse placement source authority line %d: %w", i+1, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func PlacementContextFromHome(homePath, targetPath string) (*PlacementGuardContext, []string, error) {
	rules, err := LoadPlacementRulesFromHome(homePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	ctx := rules.PlacementContext(targetPath)
	if ctx == nil {
		return nil, []string{filepath.Join("registries", placementRulesFile)}, nil
	}
	return ctx, []string{filepath.Join("registries", placementRulesFile)}, nil
}

func (rules PlacementRules) PlacementContext(targetPath string) *PlacementGuardContext {
	target := normalizePlacementPath(targetPath)
	if target == "" || anyPlacementPathMatch(target, rules.OpenWriteGlobs) {
		return nil
	}
	for _, owner := range rules.Owners {
		if !placementOwnerActive(owner) {
			continue
		}
		if anyPlacementPathMatch(target, owner.DenyGlobs) {
			return owner.placementContext(target, "deny-zone")
		}
		if anyPlacementNameMatch(target, owner.CanonFilenameGlobs) && !anyPlacementPathMatch(target, owner.CanonicalWriteGlobs) {
			return owner.placementContext(target, "canon-filename-outside-owner-home")
		}
	}
	return nil
}

func (owner PlacementOwnerRule) placementContext(targetPath, rule string) *PlacementGuardContext {
	confidence := strings.TrimSpace(owner.Confidence)
	if confidence == "" {
		confidence = "warn"
	}
	ownerID := strings.TrimSpace(owner.ID)
	return &PlacementGuardContext{
		Confidence:   confidence,
		OwnerRef:     "placement-owner:" + ownerID,
		TargetRef:    targetPath,
		Reason:       rule,
		EvidenceRefs: []string{PlacementRulesAuthorityRef, "placement-owner:" + ownerID},
	}
}

func placementOwnerActive(owner PlacementOwnerRule) bool {
	status := strings.TrimSpace(owner.Status)
	return status == "" || status == "active" || status == "draft-active"
}

func anyPlacementPathMatch(target string, globs []string) bool {
	for _, glob := range globs {
		pattern := expandPlacementPath(glob)
		if pattern == "" {
			continue
		}
		if ok, _ := regexp.MatchString(globToRegexp(filepath.ToSlash(pattern)), target); ok {
			return true
		}
	}
	return false
}

func anyPlacementNameMatch(target string, globs []string) bool {
	name := filepath.Base(target)
	for _, glob := range globs {
		if ok, _ := regexp.MatchString(globToRegexp(filepath.ToSlash(glob)), name); ok {
			return true
		}
	}
	return false
}

func normalizePlacementPath(path string) string {
	return filepath.ToSlash(expandPlacementPath(strings.TrimSpace(path)))
}

func expandPlacementPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	if strings.HasPrefix(path, "$HOME/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[6:])
		}
	}
	return path
}

func placementOwnerIDs(rules PlacementRules) []string {
	ids := make([]string, 0, len(rules.Owners))
	for _, owner := range rules.Owners {
		id := strings.TrimSpace(owner.ID)
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func appendPlacementSourceAuthority(homePath string, rec PlacementSourceAuthorityRecord) error {
	path := PlacementSourceAuthorityPath(homePath)
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

func placementSourceAuthorityID(path, revision string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{path, revision, placementSourceAuthorityImportedVia}, "|")))
	return "plsrc_" + hex.EncodeToString(sum[:])[:12]
}

func digestHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
