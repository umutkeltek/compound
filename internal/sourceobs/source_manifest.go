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
	"strconv"
	"strings"
	"time"
	"unicode"

	"compound/internal/home"
)

const SourceManifestSchemaVersion = "compound.source_manifest.v0.3"

const legacySourceManifestSchemaVersion = "compound.source_manifest.v0.1"

const custodySourceManifestSchemaVersion = "compound.source_manifest.v0.2"

const (
	SourceManifestStorageCanonical      = "canonical"
	SourceManifestStorageLegacyFallback = "legacy_fallback"
)

type SourceManifest struct {
	SchemaVersion string                    `json:"schema_version"`
	ID            string                    `json:"id"`
	GeneratedAt   string                    `json:"generated_at"`
	SourceFamily  string                    `json:"source_family"`
	AuthorityRef  string                    `json:"authority_ref"`
	Lifecycle     *SourceManifestLifecycle  `json:"lifecycle,omitempty"`
	FamilyMeta    *SourceManifestFamilyMeta `json:"family_meta,omitempty"`
	Items         []SourceManifestItem      `json:"items"`
}

type SourceManifestItem struct {
	ID                 string                   `json:"id"`
	SourceKey          string                   `json:"source_key,omitempty"`
	VersionState       string                   `json:"version_state,omitempty"`
	Supersedes         string                   `json:"supersedes,omitempty"`
	SupersededBy       string                   `json:"superseded_by,omitempty"`
	SourceKind         string                   `json:"source_kind"`
	SourceRef          string                   `json:"source_ref"`
	FileHash           string                   `json:"file_hash"`
	GitRevision        string                   `json:"git_revision,omitempty"`
	SnapshotTime       string                   `json:"snapshot_time"`
	Privacy            string                   `json:"privacy"`
	BodyPolicy         string                   `json:"body_policy"`
	BodyRef            string                   `json:"body_ref,omitempty"`
	BodyBytes          int64                    `json:"body_bytes,omitempty"`
	MediaType          string                   `json:"media_type,omitempty"`
	FileMode           string                   `json:"file_mode,omitempty"`
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
	SchemaVersion                  string                      `json:"schema_version"`
	Status                         string                      `json:"status"`
	ManifestID                     string                      `json:"manifest_id,omitempty"`
	ManifestPath                   string                      `json:"manifest_path"`
	LoadedManifestPath             string                      `json:"loaded_manifest_path,omitempty"`
	StorageStatus                  string                      `json:"storage_status,omitempty"`
	AuthorityRef                   string                      `json:"authority_ref,omitempty"`
	ItemCount                      int                         `json:"item_count"`
	MissingFieldCount              int                         `json:"missing_field_count"`
	HashMismatchCount              int                         `json:"hash_mismatch_count"`
	LifecycleEvidenceMissingCount  int                         `json:"lifecycle_evidence_missing_count"`
	LifecycleEvidenceMismatchCount int                         `json:"lifecycle_evidence_mismatch_count"`
	OwnedBodyCount                 int                         `json:"owned_body_count"`
	InventoryOnlyCount             int                         `json:"inventory_only_count"`
	CustodyComplete                bool                        `json:"custody_complete"`
	Lifecycle                      SourceLifecycleStatus       `json:"lifecycle"`
	Checks                         []SourceManifestDoctorCheck `json:"checks"`
	Items                          []SourceManifestDoctorItem  `json:"items,omitempty"`
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
	BodyStatus    string   `json:"body_status,omitempty"`
	SourceStatus  string   `json:"source_status,omitempty"`
	MissingFields []string `json:"missing_fields,omitempty"`
}

type SourceManifestDrilldown struct {
	CompactRowRef      string                   `json:"compact_row_ref"`
	ManifestRef        string                   `json:"manifest_ref"`
	SourceRef          string                   `json:"source_ref"`
	FileHash           string                   `json:"file_hash"`
	GitRevision        string                   `json:"git_revision,omitempty"`
	BodyPolicy         string                   `json:"body_policy"`
	BodyRef            string                   `json:"body_ref,omitempty"`
	BodyAvailable      bool                     `json:"body_available"`
	BodyMatches        bool                     `json:"body_matches"`
	SourceAvailable    bool                     `json:"source_available"`
	HashMatches        bool                     `json:"hash_matches"`
	StructureOverlays  []SourceStructureOverlay `json:"structure_overlays,omitempty"`
	CompoundEntityRefs []string                 `json:"compound_entity_refs,omitempty"`
	DrilldownPath      []string                 `json:"drilldown_path"`
}

type SourceManifestMaterializeReport struct {
	ManifestRef   string `json:"manifest_ref"`
	ItemID        string `json:"item_id"`
	BodyRef       string `json:"body_ref"`
	TargetPath    string `json:"target_path"`
	Bytes         int64  `json:"bytes"`
	AlreadyExists bool   `json:"already_exists"`
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
		AuthorityRef:  SourceManifestAuthorityRef(sourceFamily),
		Lifecycle:     defaultSourceManifestLifecycle(),
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

// BuildOwnedSourceManifest captures exact source bytes into Compound's immutable
// artifact store. SourceRef remains provenance; it is no longer needed to recover
// the body after this function succeeds.
func BuildOwnedSourceManifest(homePath, sourceFamily string, paths []string, now time.Time) (SourceManifest, error) {
	manifest, err := BuildSourceManifest(sourceFamily, paths, now)
	if err != nil {
		return SourceManifest{}, err
	}
	store, err := home.NewArtifactStore(homePath)
	if err != nil {
		return SourceManifest{}, err
	}
	for i := range manifest.Items {
		body, err := os.ReadFile(manifest.Items[i].SourceRef)
		if err != nil {
			return SourceManifest{}, fmt.Errorf("capture source %s: %w", manifest.Items[i].SourceRef, err)
		}
		ref, err := store.Put(body)
		if err != nil {
			return SourceManifest{}, fmt.Errorf("capture source %s: %w", manifest.Items[i].SourceRef, err)
		}
		manifest.Items[i].BodyPolicy = "owned_snapshot"
		manifest.Items[i].BodyRef = ref
		manifest.Items[i].BodyBytes = int64(len(body))
		manifest.Items[i].MediaType = inferMediaType(manifest.Items[i].SourceRef)
		manifest.Items[i].EvidenceRefs = append(manifest.Items[i].EvidenceRefs, ref)
		manifest.Items[i].ID = idFor("srcitem_", sourceFamily, manifest.Items[i].SourceKey, manifest.Items[i].FileHash)
	}
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
	if existing, err := LoadSourceManifest(homePath, manifest.SourceFamily); err == nil {
		manifest = mergeSourceManifest(existing, manifest)
	} else if !os.IsNotExist(err) {
		return SourceManifestReport{}, err
	}
	manifest.SchemaVersion = SourceManifestSchemaVersion
	manifest.AuthorityRef = SourceManifestAuthorityRef(manifest.SourceFamily)
	if manifest.Lifecycle == nil {
		manifest.Lifecycle = defaultSourceManifestLifecycle()
	}
	manifest.GeneratedAt = sourceManifestGeneratedAt(manifest.Items, time.Time{})
	// Merge can introduce lineage and normalize a legacy v0.1 manifest. Treat
	// that result as a new authority publication and validate the exact object
	// that will be made visible, not only the caller's pre-merge fragment.
	if err := validateSourceManifest(manifest); err != nil {
		return SourceManifestReport{}, fmt.Errorf("validate merged source manifest: %w", err)
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return SourceManifestReport{}, err
	}
	if err := writeSourceManifestAtomic(path, append(body, '\n')); err != nil {
		return SourceManifestReport{}, err
	}
	return SourceManifestReport{ManifestID: manifest.ID, ManifestPath: path, AuthorityRef: manifest.AuthorityRef, ItemCount: len(manifest.Items)}, nil
}

func mergeSourceManifest(existing, incoming SourceManifest) SourceManifest {
	merged := existing
	merged.SchemaVersion = SourceManifestSchemaVersion
	merged.AuthorityRef = incoming.AuthorityRef
	if merged.Lifecycle == nil {
		merged.Lifecycle = cloneSourceManifestLifecycle(incoming.Lifecycle)
	}
	if incoming.FamilyMeta != nil {
		merged.FamilyMeta = incoming.FamilyMeta
	}
	for i := range merged.Items {
		normalizeSourceManifestItem(&merged.Items[i], existing.SourceFamily)
	}
	for _, next := range incoming.Items {
		normalizeSourceManifestItem(&next, incoming.SourceFamily)
		matched := -1
		for i := range merged.Items {
			if merged.Items[i].ID == next.ID || (merged.Items[i].SourceKey == next.SourceKey && merged.Items[i].FileHash == next.FileHash) {
				matched = i
				break
			}
		}
		if matched >= 0 {
			// Same version: enrich legacy inventory with owned custody without
			// destroying its stable compact row id.
			next.ID = merged.Items[matched].ID
			next.Supersedes = merged.Items[matched].Supersedes
			next.SupersededBy = merged.Items[matched].SupersededBy
			if next.SupersededBy != "" {
				next.VersionState = "superseded"
			}
			merged.Items[matched] = next
			continue
		}
		latest := -1
		for i := range merged.Items {
			if merged.Items[i].SourceKey != next.SourceKey || merged.Items[i].VersionState == "superseded" {
				continue
			}
			if latest < 0 || merged.Items[i].SnapshotTime > merged.Items[latest].SnapshotTime {
				latest = i
			}
		}
		if latest >= 0 {
			next.Supersedes = merged.Items[latest].ID
			for i := range merged.Items {
				if merged.Items[i].SourceKey == next.SourceKey && merged.Items[i].VersionState != "superseded" {
					merged.Items[i].VersionState = "superseded"
					merged.Items[i].SupersededBy = next.ID
				}
			}
		}
		next.VersionState = "current"
		merged.Items = append(merged.Items, next)
	}
	sort.SliceStable(merged.Items, func(i, j int) bool {
		if merged.Items[i].SourceKey != merged.Items[j].SourceKey {
			return merged.Items[i].SourceKey < merged.Items[j].SourceKey
		}
		if merged.Items[i].SnapshotTime != merged.Items[j].SnapshotTime {
			return merged.Items[i].SnapshotTime < merged.Items[j].SnapshotTime
		}
		return merged.Items[i].ID < merged.Items[j].ID
	})
	return merged
}

func normalizeSourceManifestItem(item *SourceManifestItem, sourceFamily string) {
	if strings.TrimSpace(item.SourceKey) == "" {
		identity := item.SourceRef
		if len(item.CompoundEntityRefs) > 0 {
			identity = strings.Join(item.CompoundEntityRefs, "|")
		}
		item.SourceKey = idFor("source_", sourceFamily, identity)
	}
	if strings.TrimSpace(item.VersionState) == "" {
		item.VersionState = "current"
	}
}

func writeSourceManifestAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".source-manifest-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func LoadSourceManifest(homePath, sourceFamily string) (SourceManifest, error) {
	manifest, _, _, err := loadSourceManifest(homePath, sourceFamily)
	return manifest, err
}

func loadSourceManifest(homePath, sourceFamily string) (SourceManifest, string, string, error) {
	canonicalPath := SourceManifestPath(homePath, sourceFamily)
	manifest, err := loadSourceManifestFile(canonicalPath)
	if err == nil {
		return manifest, canonicalPath, SourceManifestStorageCanonical, nil
	}
	if !os.IsNotExist(err) {
		return SourceManifest{}, canonicalPath, SourceManifestStorageCanonical, err
	}
	legacyPath := LegacySourceManifestPath(homePath, sourceFamily)
	manifest, err = loadSourceManifestFile(legacyPath)
	if err != nil {
		return SourceManifest{}, legacyPath, SourceManifestStorageLegacyFallback, err
	}
	return manifest, legacyPath, SourceManifestStorageLegacyFallback, nil
}

func loadSourceManifestFile(path string) (SourceManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SourceManifest{}, err
	}
	var manifest SourceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return SourceManifest{}, fmt.Errorf("parse source manifest %s: %w", path, err)
	}
	if err := validateSourceManifest(manifest); err != nil {
		return SourceManifest{}, fmt.Errorf("validate source manifest %s: %w", path, err)
	}
	if manifest.Lifecycle == nil {
		manifest.Lifecycle = defaultSourceManifestLifecycle()
	}
	return manifest, nil
}

func DoctorSourceManifest(homePath, sourceFamily string) (SourceManifestDoctorReport, error) {
	report := SourceManifestDoctorReport{
		SchemaVersion: SourceManifestSchemaVersion,
		Status:        "fail",
		ManifestPath:  SourceManifestPath(homePath, sourceFamily),
	}
	manifest, loadedPath, storageStatus, err := loadSourceManifest(homePath, sourceFamily)
	if err != nil {
		if os.IsNotExist(err) {
			report.Checks = append(report.Checks, sourceManifestCheck("installed", false, "manifest not found"))
			return report, nil
		}
		return report, err
	}
	report.LoadedManifestPath = loadedPath
	report.StorageStatus = storageStatus
	report.ManifestID = manifest.ID
	report.AuthorityRef = manifest.AuthorityRef
	report.ItemCount = len(manifest.Items)
	report.Lifecycle = SourceManifestLifecycleStatus(manifest)
	store, storeErr := home.NewArtifactStore(homePath)
	for _, item := range manifest.Items {
		di := SourceManifestDoctorItem{ID: item.ID, SourceRef: item.SourceRef, HashStatus: "unchecked", BodyStatus: "not_owned"}
		di.MissingFields = missingSourceManifestFields(item)
		if len(di.MissingFields) > 0 {
			report.MissingFieldCount++
			di.HashStatus = "missing_required"
		}
		sourceHash, sourceErr := hashFile(item.SourceRef)
		switch {
		case sourceErr != nil:
			di.SourceStatus = "source_unavailable"
		case sourceHash != item.FileHash:
			di.SourceStatus = "source_changed"
		default:
			di.SourceStatus = "ok"
		}
		switch item.BodyPolicy {
		case "owned_snapshot":
			report.OwnedBodyCount++
			if storeErr != nil {
				report.HashMismatchCount++
				di.HashStatus, di.BodyStatus = "body_unavailable", "body_unavailable"
				break
			}
			body, err := store.Get(item.BodyRef)
			switch {
			case err != nil && os.IsNotExist(err):
				report.HashMismatchCount++
				di.HashStatus, di.BodyStatus = "body_unavailable", "body_unavailable"
			case err != nil:
				report.HashMismatchCount++
				di.HashStatus, di.BodyStatus = "body_hash_mismatch", "body_hash_mismatch"
			case hashBytes(body) != item.FileHash:
				report.HashMismatchCount++
				di.HashStatus, di.BodyStatus = "body_hash_mismatch", "body_hash_mismatch"
			case int64(len(body)) != item.BodyBytes:
				report.HashMismatchCount++
				di.HashStatus, di.BodyStatus = "body_size_mismatch", "body_size_mismatch"
			default:
				di.HashStatus, di.BodyStatus = "ok", "ok"
			}
		case "source_in_place":
			report.InventoryOnlyCount++
			di.HashStatus = di.SourceStatus
			if sourceErr != nil || sourceHash != item.FileHash {
				report.HashMismatchCount++
			}
		default:
			report.InventoryOnlyCount++
			di.HashStatus = "inventory_only"
		}
		report.Items = append(report.Items, di)
	}
	report.CustodyComplete = report.ItemCount > 0 && report.OwnedBodyCount == report.ItemCount && report.HashMismatchCount == 0 && report.MissingFieldCount == 0
	boundEvidence, missingEvidence, mismatchedEvidence := doctorSourceLifecycleEvidence(homePath, manifest)
	report.LifecycleEvidenceMissingCount = missingEvidence
	report.LifecycleEvidenceMismatchCount = mismatchedEvidence
	report.Checks = append(report.Checks,
		sourceManifestCheck("installed", true, loadedPath),
		sourceManifestStatusCheck("storage_location", storageStatus == SourceManifestStorageCanonical, fmt.Sprintf("loaded_from=%s canonical=%s", loadedPath, report.ManifestPath)),
		sourceManifestCheck("items", report.ItemCount > 0, fmt.Sprintf("items=%d", report.ItemCount)),
		sourceManifestCheck("required_fields", report.MissingFieldCount == 0, fmt.Sprintf("missing=%d", report.MissingFieldCount)),
		sourceManifestCheck("source_recovery", report.HashMismatchCount == 0, fmt.Sprintf("hash_mismatch=%d", report.HashMismatchCount)),
		sourceManifestStatusCheck("physical_custody", report.CustodyComplete, fmt.Sprintf("owned=%d inventory_only=%d", report.OwnedBodyCount, report.InventoryOnlyCount)),
		sourceManifestCheck("lifecycle_evidence", missingEvidence == 0 && mismatchedEvidence == 0, fmt.Sprintf("bound=%d missing=%d hash_or_contract_mismatch=%d", boundEvidence, missingEvidence, mismatchedEvidence)),
		sourceManifestStatusCheck("retirement_lifecycle", report.Lifecycle.Terminal, fmt.Sprintf("state=%s next=%s read=%s write=%s", report.Lifecycle.State, report.Lifecycle.NextState, report.Lifecycle.ReadPosture, report.Lifecycle.WritePosture)),
	)
	if manifest.SourceFamily == GbrainDBExportManifestFamily {
		doctorGbrainManifestExtras(homePath, manifest, &report)
	}
	report.Status = "ok"
	if storageStatus == SourceManifestStorageLegacyFallback {
		report.Status = "degraded"
	}
	for _, check := range report.Checks {
		if check.Status == "fail" {
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
		bodyAvailable, bodyMatches := false, false
		if item.BodyPolicy == "owned_snapshot" {
			if store, storeErr := home.NewArtifactStore(homePath); storeErr == nil {
				if body, bodyErr := store.Get(item.BodyRef); bodyErr == nil {
					bodyAvailable = true
					bodyMatches = hashBytes(body) == item.FileHash && int64(len(body)) == item.BodyBytes
				}
			}
		}
		compactRef := "source-manifest:" + manifest.SourceFamily + "#" + item.ID
		manifestRef := manifest.AuthorityRef + "#" + item.ID
		return SourceManifestDrilldown{
			CompactRowRef:      compactRef,
			ManifestRef:        manifestRef,
			SourceRef:          item.SourceRef,
			FileHash:           item.FileHash,
			GitRevision:        item.GitRevision,
			BodyPolicy:         item.BodyPolicy,
			BodyRef:            item.BodyRef,
			BodyAvailable:      bodyAvailable,
			BodyMatches:        bodyMatches,
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

func MaterializeSourceManifestItem(homePath, sourceFamily, itemID, targetPath string) (SourceManifestMaterializeReport, error) {
	manifest, err := LoadSourceManifest(homePath, sourceFamily)
	if err != nil {
		return SourceManifestMaterializeReport{}, err
	}
	var item *SourceManifestItem
	for i := range manifest.Items {
		if manifest.Items[i].ID == itemID {
			item = &manifest.Items[i]
			break
		}
	}
	if item == nil {
		return SourceManifestMaterializeReport{}, fmt.Errorf("manifest item %q not found", itemID)
	}
	if item.BodyPolicy != "owned_snapshot" || strings.TrimSpace(item.BodyRef) == "" {
		return SourceManifestMaterializeReport{}, fmt.Errorf("manifest item %q has no Compound-owned body", itemID)
	}
	store, err := home.NewArtifactStore(homePath)
	if err != nil {
		return SourceManifestMaterializeReport{}, err
	}
	expectedBodyRef := home.ArtifactRefPrefix + strings.TrimPrefix(item.FileHash, "sha256:")
	if item.BodyRef != expectedBodyRef {
		return SourceManifestMaterializeReport{}, fmt.Errorf("materialize %s: manifest/body mismatch", item.BodyRef)
	}
	verifiedBytes, err := store.Verify(item.BodyRef)
	if err != nil {
		return SourceManifestMaterializeReport{}, fmt.Errorf("materialize %s: %w", item.BodyRef, err)
	}
	if verifiedBytes != item.BodyBytes {
		return SourceManifestMaterializeReport{}, fmt.Errorf("materialize %s: manifest/body mismatch", item.BodyRef)
	}
	mode := os.FileMode(0o644)
	if strings.TrimSpace(item.FileMode) != "" {
		mode, err = parseFileMode(item.FileMode)
		if err != nil {
			return SourceManifestMaterializeReport{}, fmt.Errorf("materialize %s: invalid file_mode %q: %w", item.BodyRef, item.FileMode, err)
		}
	}
	written, alreadyExists, err := store.Materialize(item.BodyRef, targetPath, mode)
	if err != nil {
		return SourceManifestMaterializeReport{}, fmt.Errorf("materialize %s: %w", item.BodyRef, err)
	}
	return SourceManifestMaterializeReport{
		ManifestRef:   manifest.AuthorityRef,
		ItemID:        item.ID,
		BodyRef:       item.BodyRef,
		TargetPath:    strings.TrimSpace(targetPath),
		Bytes:         written,
		AlreadyExists: alreadyExists,
	}, nil
}

func SourceManifestPath(homePath, sourceFamily string) string {
	return filepath.Join(homePath, "sources", "manifests", safeManifestName(sourceFamily)+".json")
}

func LegacySourceManifestPath(homePath, sourceFamily string) string {
	return filepath.Join(homePath, "registries", "source-manifests", safeManifestName(sourceFamily)+".json")
}

func SourceManifestAuthorityRef(sourceFamily string) string {
	return "compound://sources/manifests/" + safeManifestName(sourceFamily)
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
		FileMode:           fmt.Sprintf("%04o", info.Mode().Perm()),
		StructureOverlays:  structureOverlays(sourcePath, data),
		CompoundEntityRefs: inferCompoundEntityRefs(sourcePath),
		EvidenceRefs:       []string{"source:" + sourcePath, hash},
	}
	normalizeSourceManifestItem(&item, sourceFamily)
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
	if manifest.SchemaVersion != SourceManifestSchemaVersion && manifest.SchemaVersion != legacySourceManifestSchemaVersion && manifest.SchemaVersion != custodySourceManifestSchemaVersion {
		return fmt.Errorf("unsupported schema_version %q", manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.SourceFamily) == "" {
		return fmt.Errorf("source manifest requires id and source_family")
	}
	if len(manifest.Items) == 0 {
		return fmt.Errorf("source manifest requires items")
	}
	if err := validateSourceManifestLifecycle(manifest); err != nil {
		return err
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
	if item.BodyPolicy == "owned_snapshot" {
		if strings.TrimSpace(item.BodyRef) == "" {
			missing = append(missing, "body_ref")
		}
		if strings.TrimSpace(item.MediaType) == "" {
			missing = append(missing, "media_type")
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

func inferMediaType(sourcePath string) string {
	switch strings.ToLower(filepath.Ext(sourcePath)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	default:
		return "application/octet-stream"
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

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func parseFileMode(value string) (os.FileMode, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 4 {
		return 0, fmt.Errorf("expected 1-4 octal permission digits")
	}
	mode, err := strconv.ParseUint(value, 8, 12)
	if err != nil {
		return 0, fmt.Errorf("expected octal permission digits: %w", err)
	}
	if mode > 0o777 {
		return 0, fmt.Errorf("permission mode %04o exceeds 0777", mode)
	}
	return os.FileMode(mode), nil
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

func sourceManifestStatusCheck(name string, ok bool, detail string) SourceManifestDoctorCheck {
	status := "pending"
	if ok {
		status = "ok"
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
