package sourceobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// GbrainDBExportManifestFamily is the lossless source-manifest family for
// gbrain DB exports (W5-J5 / P0.2). One door: source-manifests registry.
const GbrainDBExportManifestFamily = "gbrain_db_export_manifest"

// GbrainExportSchemaVersion is the on-disk export JSON shape version.
const GbrainExportSchemaVersion = "compound.gbrain_db_export.v0.1"

// GbrainDBExport is the synthetic/real export envelope ingested by
// BuildGbrainExportManifest. No page bodies — hashes only (public-safe).
type GbrainDBExport struct {
	SchemaVersion   string             `json:"schema_version"`
	DBHost          string             `json:"db_host"`
	DBName          string             `json:"db_name"`
	DumpRef         string             `json:"dump_ref"`
	ExportTime      string             `json:"export_time"`
	GenerationLabel string             `json:"generation_label"`
	Pages           []GbrainExportPage `json:"pages"`
}

// GbrainExportPage is one lossless page index row.
type GbrainExportPage struct {
	PageSlug         string                `json:"page_slug"`
	SourcePath       string                `json:"source_path,omitempty"`
	DBOnly           bool                  `json:"db_only,omitempty"`
	PageHash         string                `json:"page_hash"`
	Chunks           []GbrainExportChunk   `json:"chunks"`
	Embedding        GbrainExportEmbedding `json:"embedding"`
	CorpusGeneration string                `json:"corpus_generation"`
	ImportedAt       string                `json:"imported_at"`
	SourceKind       string                `json:"source_kind"`
}

// GbrainExportChunk is an ordered chunk fingerprint (ordinal is required).
type GbrainExportChunk struct {
	Ordinal   int    `json:"ordinal"`
	ChunkHash string `json:"chunk_hash"`
}

// GbrainExportEmbedding is the embedding identity for the page's vectors.
type GbrainExportEmbedding struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Dims     int    `json:"dims"`
}

// SourceManifestFamilyMeta carries family-level export provenance (optional on
// generic manifests; required for gbrain_db_export_manifest).
type SourceManifestFamilyMeta struct {
	DBHost          string `json:"db_host,omitempty"`
	DBName          string `json:"db_name,omitempty"`
	DumpRef         string `json:"dump_ref,omitempty"`
	ExportTime      string `json:"export_time,omitempty"`
	GenerationLabel string `json:"generation_label,omitempty"`
}

// SourceManifestChunk is the stored ordered chunk fingerprint on a manifest item.
type SourceManifestChunk struct {
	Ordinal   int    `json:"ordinal"`
	ChunkHash string `json:"chunk_hash"`
}

// SourceManifestEmbedding is the stored embedding identity on a manifest item.
type SourceManifestEmbedding struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Dims     int    `json:"dims"`
}

// BuildGbrainExportManifest converts a gbrain export JSON into a SourceManifest
// under family gbrain_db_export_manifest. Validates lossless fields; refuses
// lossy exports (missing chunk order / embedding identity / generation).
func BuildGbrainExportManifest(exportPath string, now time.Time) (SourceManifest, error) {
	exportPath = strings.TrimSpace(exportPath)
	if exportPath == "" {
		return SourceManifest{}, fmt.Errorf("export path required")
	}
	data, err := os.ReadFile(exportPath)
	if err != nil {
		return SourceManifest{}, fmt.Errorf("read export %s: %w", exportPath, err)
	}
	var exp GbrainDBExport
	if err := json.Unmarshal(data, &exp); err != nil {
		return SourceManifest{}, fmt.Errorf("parse export %s: %w", exportPath, err)
	}
	if err := validateGbrainExport(exp); err != nil {
		return SourceManifest{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	manifest := SourceManifest{
		SchemaVersion: SourceManifestSchemaVersion,
		ID:            "srcman_" + safeManifestName(GbrainDBExportManifestFamily),
		SourceFamily:  GbrainDBExportManifestFamily,
		AuthorityRef:  "compound://registries/source-manifests/" + safeManifestName(GbrainDBExportManifestFamily),
		FamilyMeta: &SourceManifestFamilyMeta{
			DBHost:          strings.TrimSpace(exp.DBHost),
			DBName:          strings.TrimSpace(exp.DBName),
			DumpRef:         strings.TrimSpace(exp.DumpRef),
			ExportTime:      strings.TrimSpace(exp.ExportTime),
			GenerationLabel: strings.TrimSpace(exp.GenerationLabel),
		},
	}
	for _, page := range exp.Pages {
		item, err := gbrainPageToManifestItem(page)
		if err != nil {
			return SourceManifest{}, err
		}
		manifest.Items = append(manifest.Items, item)
	}
	sort.SliceStable(manifest.Items, func(i, j int) bool {
		if manifest.Items[i].CorpusGeneration != manifest.Items[j].CorpusGeneration {
			return manifest.Items[i].CorpusGeneration < manifest.Items[j].CorpusGeneration
		}
		return manifest.Items[i].PageSlug < manifest.Items[j].PageSlug
	})
	manifest.GeneratedAt = sourceManifestGeneratedAt(manifest.Items, now)
	if strings.TrimSpace(manifest.GeneratedAt) == "" {
		manifest.GeneratedAt = now.UTC().Format(time.RFC3339)
	}
	return manifest, nil
}

// FilterSourceManifestByGeneration returns items whose corpus_generation matches.
// Generations are disjoint: A never returns B.
func FilterSourceManifestByGeneration(manifest SourceManifest, generation string) []SourceManifestItem {
	generation = strings.TrimSpace(generation)
	if generation == "" {
		return nil
	}
	var out []SourceManifestItem
	for _, item := range manifest.Items {
		if strings.TrimSpace(item.CorpusGeneration) == generation {
			out = append(out, item)
		}
	}
	return out
}

// HashGbrainPageFromChunks is the lossless page fingerprint derived from the
// ordered chunk set. Doctor uses this to detect page_hash / chunk-set mismatch
// without requiring page bodies in the public fixture.
func HashGbrainPageFromChunks(chunks []SourceManifestChunk) string {
	sorted := append([]SourceManifestChunk(nil), chunks...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Ordinal < sorted[j].Ordinal })
	var b strings.Builder
	b.WriteString("gbrain_page_v1\n")
	for _, c := range sorted {
		fmt.Fprintf(&b, "%d:%s\n", c.Ordinal, strings.TrimSpace(c.ChunkHash))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validateGbrainExport(exp GbrainDBExport) error {
	if strings.TrimSpace(exp.SchemaVersion) != "" && exp.SchemaVersion != GbrainExportSchemaVersion {
		return fmt.Errorf("unsupported gbrain export schema_version %q", exp.SchemaVersion)
	}
	if strings.TrimSpace(exp.GenerationLabel) == "" {
		return fmt.Errorf("gbrain export requires generation_label")
	}
	if strings.TrimSpace(exp.DumpRef) == "" {
		return fmt.Errorf("gbrain export requires dump_ref")
	}
	if strings.TrimSpace(exp.ExportTime) == "" {
		return fmt.Errorf("gbrain export requires export_time")
	}
	if len(exp.Pages) == 0 {
		return fmt.Errorf("gbrain export requires pages")
	}
	seen := map[string]bool{}
	for i, page := range exp.Pages {
		if err := validateGbrainExportPage(page); err != nil {
			return fmt.Errorf("pages[%d]: %w", i, err)
		}
		key := strings.TrimSpace(page.CorpusGeneration) + "\x00" + strings.TrimSpace(page.PageSlug)
		if seen[key] {
			return fmt.Errorf("duplicate page %q in generation %q", page.PageSlug, page.CorpusGeneration)
		}
		seen[key] = true
	}
	return nil
}

func validateGbrainExportPage(page GbrainExportPage) error {
	if strings.TrimSpace(page.PageSlug) == "" {
		return fmt.Errorf("page_slug required")
	}
	if strings.TrimSpace(page.PageHash) == "" {
		return fmt.Errorf("page_hash required")
	}
	if strings.TrimSpace(page.CorpusGeneration) == "" {
		return fmt.Errorf("corpus_generation required")
	}
	if strings.TrimSpace(page.ImportedAt) == "" {
		return fmt.Errorf("imported_at required")
	}
	if strings.TrimSpace(page.SourceKind) == "" {
		return fmt.Errorf("source_kind required")
	}
	if !page.DBOnly && strings.TrimSpace(page.SourcePath) == "" {
		return fmt.Errorf("source_path required when db_only is false")
	}
	if page.DBOnly && strings.TrimSpace(page.SourcePath) != "" {
		return fmt.Errorf("source_path must be empty when db_only is true")
	}
	if len(page.Chunks) == 0 {
		return fmt.Errorf("chunks required (lossy without chunk order)")
	}
	if err := validateGbrainChunks(page.Chunks); err != nil {
		return err
	}
	if err := validateGbrainEmbedding(page.Embedding); err != nil {
		return err
	}
	chunks := make([]SourceManifestChunk, 0, len(page.Chunks))
	for _, c := range page.Chunks {
		chunks = append(chunks, SourceManifestChunk{Ordinal: c.Ordinal, ChunkHash: c.ChunkHash})
	}
	expected := HashGbrainPageFromChunks(chunks)
	if strings.TrimSpace(page.PageHash) != expected {
		return fmt.Errorf("page_hash mismatches recorded chunk set for %q (want %s)", page.PageSlug, expected)
	}
	return nil
}

func validateGbrainChunks(chunks []GbrainExportChunk) error {
	ords := make([]int, 0, len(chunks))
	seen := map[int]bool{}
	for _, c := range chunks {
		if strings.TrimSpace(c.ChunkHash) == "" {
			return fmt.Errorf("chunk_hash required for every chunk")
		}
		if seen[c.Ordinal] {
			return fmt.Errorf("duplicate chunk ordinal %d", c.Ordinal)
		}
		seen[c.Ordinal] = true
		ords = append(ords, c.Ordinal)
	}
	sort.Ints(ords)
	// Contiguous 0..n-1 required so order is lossless / reconstructible.
	for i, o := range ords {
		if o != i {
			return fmt.Errorf("chunk ordinals must be contiguous from 0 (got %v)", ords)
		}
	}
	return nil
}

func validateGbrainEmbedding(emb GbrainExportEmbedding) error {
	if strings.TrimSpace(emb.Provider) == "" {
		return fmt.Errorf("embedding.provider required")
	}
	if strings.TrimSpace(emb.Model) == "" {
		return fmt.Errorf("embedding.model required")
	}
	if emb.Dims <= 0 {
		return fmt.Errorf("embedding.dims must be > 0")
	}
	return nil
}

func gbrainPageToManifestItem(page GbrainExportPage) (SourceManifestItem, error) {
	chunks := make([]SourceManifestChunk, 0, len(page.Chunks))
	for _, c := range page.Chunks {
		chunks = append(chunks, SourceManifestChunk{
			Ordinal:   c.Ordinal,
			ChunkHash: strings.TrimSpace(c.ChunkHash),
		})
	}
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].Ordinal < chunks[j].Ordinal })

	sourceRef := strings.TrimSpace(page.SourcePath)
	bodyPolicy := "source_in_place"
	if page.DBOnly {
		sourceRef = "db_only:" + strings.TrimSpace(page.PageSlug)
		bodyPolicy = "db_only"
	}
	item := SourceManifestItem{
		ID:               idFor("srcitem_", GbrainDBExportManifestFamily, page.CorpusGeneration, page.PageSlug),
		SourceKind:       strings.TrimSpace(page.SourceKind),
		SourceRef:        sourceRef,
		FileHash:         strings.TrimSpace(page.PageHash),
		SnapshotTime:     strings.TrimSpace(page.ImportedAt),
		Privacy:          "private",
		BodyPolicy:       bodyPolicy,
		EvidenceRefs:     []string{"gbrain_page:" + page.PageSlug, page.PageHash, "corpus_generation:" + page.CorpusGeneration},
		PageSlug:         strings.TrimSpace(page.PageSlug),
		SourcePath:       strings.TrimSpace(page.SourcePath),
		DBOnly:           page.DBOnly,
		PageHash:         strings.TrimSpace(page.PageHash),
		Chunks:           chunks,
		Embedding:        &SourceManifestEmbedding{Provider: page.Embedding.Provider, Model: page.Embedding.Model, Dims: page.Embedding.Dims},
		CorpusGeneration: strings.TrimSpace(page.CorpusGeneration),
		ImportedAt:       strings.TrimSpace(page.ImportedAt),
	}
	return item, nil
}

func missingGbrainManifestFields(item SourceManifestItem) []string {
	var missing []string
	if strings.TrimSpace(item.PageSlug) == "" {
		missing = append(missing, "page_slug")
	}
	if strings.TrimSpace(item.PageHash) == "" {
		missing = append(missing, "page_hash")
	}
	if strings.TrimSpace(item.CorpusGeneration) == "" {
		missing = append(missing, "corpus_generation")
	}
	if strings.TrimSpace(item.ImportedAt) == "" {
		missing = append(missing, "imported_at")
	}
	if strings.TrimSpace(item.SourceKind) == "" {
		missing = append(missing, "source_kind")
	}
	if !item.DBOnly && strings.TrimSpace(item.SourcePath) == "" && !strings.HasPrefix(item.SourceRef, "db_only:") {
		missing = append(missing, "source_path")
	}
	if len(item.Chunks) == 0 {
		missing = append(missing, "chunks")
	} else {
		for i, c := range item.Chunks {
			if strings.TrimSpace(c.ChunkHash) == "" {
				missing = append(missing, fmt.Sprintf("chunks[%d].chunk_hash", i))
			}
		}
		// ordinal order check reported via doctor checks, not missing fields
	}
	if item.Embedding == nil {
		missing = append(missing, "embedding")
	} else {
		if strings.TrimSpace(item.Embedding.Provider) == "" {
			missing = append(missing, "embedding.provider")
		}
		if strings.TrimSpace(item.Embedding.Model) == "" {
			missing = append(missing, "embedding.model")
		}
		if item.Embedding.Dims <= 0 {
			missing = append(missing, "embedding.dims")
		}
	}
	sort.Strings(missing)
	return missing
}

func doctorGbrainManifestExtras(manifest SourceManifest, report *SourceManifestDoctorReport) {
	metaOK := manifest.FamilyMeta != nil &&
		strings.TrimSpace(manifest.FamilyMeta.GenerationLabel) != "" &&
		strings.TrimSpace(manifest.FamilyMeta.DumpRef) != "" &&
		strings.TrimSpace(manifest.FamilyMeta.ExportTime) != ""
	detail := "family_meta missing"
	if metaOK {
		detail = "generation_label=" + manifest.FamilyMeta.GenerationLabel
	}
	report.Checks = append(report.Checks, sourceManifestCheck("gbrain_family_meta", metaOK, detail))
	if !metaOK {
		report.MissingFieldCount++
	}

	chunkMismatch := 0
	lossyChunkOrder := 0
	for i := range report.Items {
		// find matching item
		var item SourceManifestItem
		for _, it := range manifest.Items {
			if it.ID == report.Items[i].ID {
				item = it
				break
			}
		}
		gbrainMissing := missingGbrainManifestFields(item)
		if len(gbrainMissing) > 0 {
			report.MissingFieldCount++
			report.Items[i].MissingFields = append(report.Items[i].MissingFields, gbrainMissing...)
			if report.Items[i].HashStatus == "ok" || report.Items[i].HashStatus == "unchecked" {
				report.Items[i].HashStatus = "missing_required"
			}
		}
		if err := validateGbrainChunkOrder(item.Chunks); err != nil {
			lossyChunkOrder++
			report.Items[i].MissingFields = append(report.Items[i].MissingFields, "chunks.ordinal_order")
		}
		if len(item.Chunks) > 0 && strings.TrimSpace(item.PageHash) != "" {
			expected := HashGbrainPageFromChunks(item.Chunks)
			if item.PageHash != expected {
				chunkMismatch++
				report.HashMismatchCount++
				report.Items[i].HashStatus = "chunk_set_mismatch"
			}
		}
	}
	report.Checks = append(report.Checks,
		sourceManifestCheck("gbrain_chunk_order", lossyChunkOrder == 0, fmt.Sprintf("lossy_chunk_order=%d", lossyChunkOrder)),
		sourceManifestCheck("gbrain_page_chunk_match", chunkMismatch == 0, fmt.Sprintf("chunk_set_mismatch=%d", chunkMismatch)),
		sourceManifestCheck("gbrain_generation_label", metaOK && manifest.FamilyMeta != nil && strings.TrimSpace(manifest.FamilyMeta.GenerationLabel) != "", "generation_label present"),
	)
}

func validateGbrainChunkOrder(chunks []SourceManifestChunk) error {
	if len(chunks) == 0 {
		return fmt.Errorf("empty")
	}
	ords := make([]int, 0, len(chunks))
	seen := map[int]bool{}
	for _, c := range chunks {
		if seen[c.Ordinal] {
			return fmt.Errorf("duplicate ordinal")
		}
		seen[c.Ordinal] = true
		ords = append(ords, c.Ordinal)
	}
	sort.Ints(ords)
	for i, o := range ords {
		if o != i {
			return fmt.Errorf("non-contiguous")
		}
	}
	return nil
}
