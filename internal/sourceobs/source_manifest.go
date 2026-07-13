package sourceobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const SourceManifestSchemaVersion = "compound.source_manifest.v0.1"

type SourceManifest struct {
	SchemaVersion string                    `json:"schema_version"`
	ID            string                    `json:"id"`
	GeneratedAt   string                    `json:"generated_at"`
	SourceFamily  string                    `json:"source_family"`
	AuthorityRef  string                    `json:"authority_ref"`
	FamilyMeta    *SourceManifestFamilyMeta `json:"family_meta,omitempty"`
	Items         []SourceManifestItem      `json:"items"`
}

type SourceManifestItem struct {
	ID                 string                   `json:"id"`
	SourceKind         string                   `json:"source_kind"`
	SourceRef          string                   `json:"source_ref"`
	FileHash           string                   `json:"file_hash"`
	GitRevision        string                   `json:"git_revision,omitempty"`
	SnapshotTime       string                   `json:"snapshot_time"`
	Privacy            string                   `json:"privacy"`
	BodyPolicy         string                   `json:"body_policy"`
	StructureOverlays  []SourceStructureOverlay `json:"structure_overlays,omitempty"`
	CompoundEntityRefs []string                 `json:"compound_entity_refs,omitempty"`
	EvidenceRefs       []string                 `json:"evidence_refs,omitempty"`
	// Gbrain export lossless fields (family gbrain_db_export_manifest).
	PageSlug         string                   `json:"page_slug,omitempty"`
	SourcePath       string                   `json:"source_path,omitempty"`
	DBOnly           bool                     `json:"db_only,omitempty"`
	PageHash         string                   `json:"page_hash,omitempty"`
	IndexState       string                   `json:"index_state,omitempty"`
	Chunks           []SourceManifestChunk    `json:"chunks,omitempty"`
	Embedding        *SourceManifestEmbedding `json:"embedding,omitempty"`
	CorpusGeneration string                   `json:"corpus_generation,omitempty"`
	ImportedAt       string                   `json:"imported_at,omitempty"`
}

type SourceStructureOverlay struct {
	Kind   string `json:"kind"`
	Count  int    `json:"count,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type SourceManifestReport struct {
	ManifestID   string `json:"manifest_id"`
	ManifestPath string `json:"manifest_path"`
	AuthorityRef string `json:"authority_ref"`
	ItemCount    int    `json:"item_count"`
}

type SourceManifestDoctorReport struct {
	SchemaVersion     string                      `json:"schema_version"`
	Status            string                      `json:"status"`
	ManifestID        string                      `json:"manifest_id,omitempty"`
	ManifestPath      string                      `json:"manifest_path"`
	AuthorityRef      string                      `json:"authority_ref,omitempty"`
	ItemCount         int                         `json:"item_count"`
	MissingFieldCount int                         `json:"missing_field_count"`
	HashMismatchCount int                         `json:"hash_mismatch_count"`
	Checks            []SourceManifestDoctorCheck `json:"checks"`
	Items             []SourceManifestDoctorItem  `json:"items,omitempty"`
}

type SourceManifestDoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type SourceManifestDoctorItem struct {
	ID            string   `json:"id"`
	SourceRef     string   `json:"source_ref"`
	HashStatus    string   `json:"hash_status"`
	MissingFields []string `json:"missing_fields,omitempty"`
}

type SourceManifestDrilldown struct {
	CompactRowRef      string                   `json:"compact_row_ref"`
	ManifestRef        string                   `json:"manifest_ref"`
	SourceRef          string                   `json:"source_ref"`
	FileHash           string                   `json:"file_hash"`
	GitRevision        string                   `json:"git_revision,omitempty"`
	BodyPolicy         string                   `json:"body_policy"`
	SourceAvailable    bool                     `json:"source_available"`
	HashMatches        bool                     `json:"hash_matches"`
	StructureOverlays  []SourceStructureOverlay `json:"structure_overlays,omitempty"`
	CompoundEntityRefs []string                 `json:"compound_entity_refs,omitempty"`
	DrilldownPath      []string                 `json:"drilldown_path"`
}

func BuildSourceManifest(sourceFamily string, paths []string, now time.Time) (SourceManifest, error) {
	sourceFamily = strings.TrimSpace(sourceFamily)
	if sourceFamily == "" {
		return SourceManifest{}, fmt.Errorf("source family required")
	}
	if len(paths) == 0 {
		return SourceManifest{}, fmt.Errorf("at least one source path required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	manifest := SourceManifest{
		SchemaVersion: SourceManifestSchemaVersion,
		ID:            "srcman_" + safeManifestName(sourceFamily),
		SourceFamily:  sourceFamily,
		AuthorityRef:  "compound://registries/source-manifests/" + safeManifestName(sourceFamily),
	}
	for _, sourcePath := range paths {
		item, err := buildSourceManifestItem(sourceFamily, sourcePath)
		if err != nil {
			return SourceManifest{}, err
		}
		manifest.Items = append(manifest.Items, item)
	}
	sort.SliceStable(manifest.Items, func(i, j int) bool { return manifest.Items[i].ID < manifest.Items[j].ID })
	manifest.GeneratedAt = sourceManifestGeneratedAt(manifest.Items, now)
	return manifest, nil
}

func RecordSourceManifest(homePath string, manifest SourceManifest) (SourceManifestReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return SourceManifestReport{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if err := validateSourceManifest(manifest); err != nil {
		return SourceManifestReport{}, err
	}
	path := SourceManifestPath(homePath, manifest.SourceFamily)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return SourceManifestReport{}, err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return SourceManifestReport{}, err
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return SourceManifestReport{}, err
	}
	return SourceManifestReport{ManifestID: manifest.ID, ManifestPath: path, AuthorityRef: manifest.AuthorityRef, ItemCount: len(manifest.Items)}, nil
}

func LoadSourceManifest(homePath, sourceFamily string) (SourceManifest, error) {
	data, err := os.ReadFile(SourceManifestPath(homePath, sourceFamily))
	if err != nil {
		return SourceManifest{}, err
	}
	var manifest SourceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return SourceManifest{}, err
	}
	if err := validateSourceManifest(manifest); err != nil {
		return SourceManifest{}, err
	}
	return manifest, nil
}

func DoctorSourceManifest(homePath, sourceFamily string) (SourceManifestDoctorReport, error) {
	report := SourceManifestDoctorReport{
		SchemaVersion: SourceManifestSchemaVersion,
		Status:        "fail",
		ManifestPath:  SourceManifestPath(homePath, sourceFamily),
	}
	manifest, err := LoadSourceManifest(homePath, sourceFamily)
	if err != nil {
		if os.IsNotExist(err) {
			report.Checks = append(report.Checks, sourceManifestCheck("installed", false, "manifest not found"))
			return report, nil
		}
		return report, err
	}
	report.ManifestID = manifest.ID
	report.AuthorityRef = manifest.AuthorityRef
	report.ItemCount = len(manifest.Items)
	for _, item := range manifest.Items {
		di := SourceManifestDoctorItem{ID: item.ID, SourceRef: item.SourceRef, HashStatus: "unchecked"}
		di.MissingFields = missingSourceManifestFields(item)
		if len(di.MissingFields) > 0 {
			report.MissingFieldCount++
			di.HashStatus = "missing_required"
		} else if item.BodyPolicy == "source_in_place" {
			hash, err := hashFile(item.SourceRef)
			switch {
			case err != nil:
				report.HashMismatchCount++
				di.HashStatus = "source_unavailable"
			case hash != item.FileHash:
				report.HashMismatchCount++
				di.HashStatus = "mismatch"
			default:
				di.HashStatus = "ok"
			}
		}
		report.Items = append(report.Items, di)
	}
	report.Checks = append(report.Checks,
		sourceManifestCheck("installed", true, report.ManifestPath),
		sourceManifestCheck("items", report.ItemCount > 0, fmt.Sprintf("items=%d", report.ItemCount)),
		sourceManifestCheck("required_fields", report.MissingFieldCount == 0, fmt.Sprintf("missing=%d", report.MissingFieldCount)),
		sourceManifestCheck("source_recovery", report.HashMismatchCount == 0, fmt.Sprintf("hash_mismatch=%d", report.HashMismatchCount)),
	)
	if manifest.SourceFamily == GbrainDBExportManifestFamily {
		doctorGbrainManifestExtras(manifest, &report)
	}
	report.Status = "ok"
	for _, check := range report.Checks {
		if check.Status != "ok" {
			report.Status = "fail"
			break
		}
	}
	// Gbrain required_fields can rise during extras; re-evaluate after.
	if report.MissingFieldCount > 0 {
		for i := range report.Checks {
			if report.Checks[i].Name == "required_fields" {
				report.Checks[i] = sourceManifestCheck("required_fields", false, fmt.Sprintf("missing=%d", report.MissingFieldCount))
			}
		}
		report.Status = "fail"
	}
	if report.HashMismatchCount > 0 {
		for i := range report.Checks {
			if report.Checks[i].Name == "source_recovery" {
				report.Checks[i] = sourceManifestCheck("source_recovery", false, fmt.Sprintf("hash_mismatch=%d", report.HashMismatchCount))
			}
		}
		report.Status = "fail"
	}
	return report, nil
}

func DrilldownSourceManifest(homePath, sourceFamily, itemID string) (SourceManifestDrilldown, error) {
	manifest, err := LoadSourceManifest(homePath, sourceFamily)
	if err != nil {
		return SourceManifestDrilldown{}, err
	}
	for _, item := range manifest.Items {
		if item.ID != itemID {
			continue
		}
		hash, err := hashFile(item.SourceRef)
		available := err == nil
		matches := available && hash == item.FileHash
		compactRef := "source-manifest:" + manifest.SourceFamily + "#" + item.ID
		manifestRef := manifest.AuthorityRef + "#" + item.ID
		return SourceManifestDrilldown{
			CompactRowRef:      compactRef,
			ManifestRef:        manifestRef,
			SourceRef:          item.SourceRef,
			FileHash:           item.FileHash,
			GitRevision:        item.GitRevision,
			BodyPolicy:         item.BodyPolicy,
			SourceAvailable:    available,
			HashMatches:        matches,
			StructureOverlays:  item.StructureOverlays,
			CompoundEntityRefs: item.CompoundEntityRefs,
			DrilldownPath: []string{
				compactRef,
				manifestRef,
				"source:" + item.SourceRef + "@" + item.FileHash,
			},
		}, nil
	}
	return SourceManifestDrilldown{}, fmt.Errorf("manifest item %q not found", itemID)
}

func SourceManifestPath(homePath, sourceFamily string) string {
	return filepath.Join(homePath, "registries", "source-manifests", safeManifestName(sourceFamily)+".json")
}

func buildSourceManifestItem(sourceFamily, sourcePath string) (SourceManifestItem, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return SourceManifestItem{}, fmt.Errorf("source path required")
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return SourceManifestItem{}, fmt.Errorf("stat source %s: %w", sourcePath, err)
	}
	hash, err := hashFile(sourcePath)
	if err != nil {
		return SourceManifestItem{}, fmt.Errorf("hash source %s: %w", sourcePath, err)
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return SourceManifestItem{}, fmt.Errorf("read source %s: %w", sourcePath, err)
	}
	item := SourceManifestItem{
		ID:                 idFor("srcitem_", sourceFamily, sourcePath),
		SourceKind:         inferSourceKind(sourcePath),
		SourceRef:          sourcePath,
		FileHash:           hash,
		GitRevision:        gitRevision(sourcePath),
		SnapshotTime:       info.ModTime().UTC().Format(time.RFC3339),
		Privacy:            "private",
		BodyPolicy:         "source_in_place",
		StructureOverlays:  structureOverlays(sourcePath, data),
		CompoundEntityRefs: inferCompoundEntityRefs(sourcePath),
		EvidenceRefs:       []string{"source:" + sourcePath, hash},
	}
	return item, nil
}

func sourceManifestGeneratedAt(items []SourceManifestItem, fallback time.Time) string {
	var latest time.Time
	for _, item := range items {
		t, err := time.Parse(time.RFC3339, item.SnapshotTime)
		if err != nil {
			continue
		}
		if latest.IsZero() || t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		if fallback.IsZero() {
			fallback = time.Now()
		}
		latest = fallback.UTC()
	}
	return latest.UTC().Format(time.RFC3339)
}

func validateSourceManifest(manifest SourceManifest) error {
	if manifest.SchemaVersion != SourceManifestSchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.SourceFamily) == "" {
		return fmt.Errorf("source manifest requires id and source_family")
	}
	if len(manifest.Items) == 0 {
		return fmt.Errorf("source manifest requires items")
	}
	seen := map[string]bool{}
	for _, item := range manifest.Items {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("source manifest item missing id")
		}
		if seen[item.ID] {
			return fmt.Errorf("duplicate source manifest item %q", item.ID)
		}
		seen[item.ID] = true
	}
	return nil
}

func missingSourceManifestFields(item SourceManifestItem) []string {
	fields := map[string]string{
		"id":            item.ID,
		"source_kind":   item.SourceKind,
		"source_ref":    item.SourceRef,
		"file_hash":     item.FileHash,
		"snapshot_time": item.SnapshotTime,
		"privacy":       item.Privacy,
		"body_policy":   item.BodyPolicy,
	}
	var missing []string
	for field, value := range fields {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	return missing
}

func structureOverlays(sourcePath string, data []byte) []SourceStructureOverlay {
	if inferSourceKind(sourcePath) != "markdown" {
		return nil
	}
	text := string(data)
	var overlays []SourceStructureOverlay
	if strings.HasPrefix(text, "---\n") {
		overlays = append(overlays, SourceStructureOverlay{Kind: "frontmatter", Count: 1})
	}
	headingCount := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") {
			headingCount++
		}
	}
	if headingCount > 0 {
		overlays = append(overlays, SourceStructureOverlay{Kind: "heading_index", Count: headingCount})
	}
	if strings.Contains(text, "## Facts") {
		overlays = append(overlays, SourceStructureOverlay{Kind: "facts_fence", Count: strings.Count(text, "## Facts")})
	}
	return overlays
}

func inferCompoundEntityRefs(sourcePath string) []string {
	normalized := filepath.ToSlash(sourcePath)
	base := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
	switch {
	case strings.Contains(normalized, "/brain/projects/"):
		return []string{"project:" + base, "source:brain"}
	case strings.Contains(normalized, "/brain/operating-system/"):
		return []string{"system:" + base, "source:brain"}
	default:
		return nil
	}
}

func inferSourceKind(sourcePath string) string {
	switch strings.ToLower(filepath.Ext(sourcePath)) {
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	default:
		return "file"
	}
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func gitRevision(sourcePath string) string {
	dir := filepath.Dir(sourcePath)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func sourceManifestCheck(name string, ok bool, detail string) SourceManifestDoctorCheck {
	status := "ok"
	if !ok {
		status = "fail"
	}
	return SourceManifestDoctorCheck{Name: name, Status: status, Detail: detail}
}

func safeManifestName(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "source_manifest"
	}
	return out
}
