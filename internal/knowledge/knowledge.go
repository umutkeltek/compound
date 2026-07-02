// Package knowledge imports captured source exports into evidence plus proposed
// memory candidates. It is intentionally medium-agnostic: source.kind is data,
// not a core enum.
package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/mem"
)

const (
	ExportSchemaVersion            = "compound.knowledge_export.v0.1"
	ExtractionRequestSchemaVersion = "compound.knowledge_extraction_request.v0.1"
	maxEvidenceQuoteChars          = 4000
)

type Export struct {
	SchemaVersion string   `json:"schema_version"`
	Source        Source   `json:"source"`
	Privacy       string   `json:"privacy,omitempty"`
	Producer      Producer `json:"producer,omitempty"`
	Items         []Item   `json:"items"`
}

type Source struct {
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	CapturedAt string `json:"captured_at"`
}

type Producer struct {
	Kind    string `json:"kind,omitempty"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type Item struct {
	Ref         string   `json:"ref"`
	URL         string   `json:"url,omitempty"`
	Title       string   `json:"title,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	PublishedAt string   `json:"published_at,omitempty"`
	Topics      []string `json:"topics,omitempty"`
	Content     string   `json:"content,omitempty"`
	ContentRef  string   `json:"content_ref,omitempty"`
	Lang        string   `json:"lang,omitempty"`
}

type ImportReport struct {
	Items         int      `json:"items"`
	Imported      int      `json:"imported"`
	Echoes        int      `json:"echoes"`
	Reused        int      `json:"reused"`
	EvidenceIDs   []string `json:"evidence_ids"`
	CandidateIDs  []string `json:"candidate_ids"`
	ExtractionIDs []string `json:"extraction_ids"`
}

type ExtractionRequest struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	ItemRef       string `json:"item_ref"`
	EvidenceRef   string `json:"evidence_ref"`
	SourceKind    string `json:"source_kind"`
	Title         string `json:"title,omitempty"`
	ContentDigest string `json:"content_digest"`
	State         string `json:"state"`
	CreatedAt     string `json:"created_at"`
}

type DistributionEvent struct {
	CanonicalRef     string `json:"canonical_ref"`
	EchoSourceKind   string `json:"echo_source_kind"`
	EchoSourceRef    string `json:"echo_source_ref"`
	FirstEvidenceRef string `json:"first_evidence_ref"`
	ObservedAt       string `json:"observed_at"`
}

type SourceRegistryRow struct {
	SourceID       string  `json:"source_id"`
	Kind           string  `json:"kind"`
	Ref            string  `json:"ref"`
	FirstSeen      string  `json:"first_seen"`
	TrustPosterior float64 `json:"trust_posterior"`
}

type ImportIndexRow struct {
	CanonicalRef string `json:"canonical_ref"`
	EvidenceRef  string `json:"evidence_ref"`
	ImportedAt   string `json:"imported_at"`
	ExportDigest string `json:"export_digest"`
}

func LoadExport(path string) (Export, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Export{}, nil, err
	}
	export, err := DecodeExport(data)
	if err != nil {
		return Export{}, nil, fmt.Errorf("parse knowledge export %s: %w", path, err)
	}
	return export, data, nil
}

func DecodeExport(data []byte) (Export, error) {
	var export Export
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&export); err != nil {
		return Export{}, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Export{}, fmt.Errorf("trailing JSON")
		}
		return Export{}, fmt.Errorf("trailing JSON: %w", err)
	}
	if err := export.Validate(); err != nil {
		return Export{}, err
	}
	return export, nil
}

func (e *Export) Validate() error {
	if strings.TrimSpace(e.SchemaVersion) == "" {
		return fmt.Errorf("schema_version required")
	}
	if e.SchemaVersion != ExportSchemaVersion {
		return fmt.Errorf("schema_version must be %s, got %q", ExportSchemaVersion, e.SchemaVersion)
	}
	e.Source.Kind = strings.TrimSpace(e.Source.Kind)
	e.Source.Ref = strings.TrimSpace(e.Source.Ref)
	e.Source.CapturedAt = strings.TrimSpace(e.Source.CapturedAt)
	if e.Source.Kind == "" {
		return fmt.Errorf("source.kind required")
	}
	if e.Source.Ref == "" {
		return fmt.Errorf("source.ref required")
	}
	if e.Source.CapturedAt == "" {
		return fmt.Errorf("source.captured_at required")
	}
	if _, err := time.Parse(time.RFC3339, e.Source.CapturedAt); err != nil {
		return fmt.Errorf("source.captured_at must be RFC3339: %w", err)
	}
	e.Privacy = strings.TrimSpace(e.Privacy)
	if e.Privacy == "" {
		e.Privacy = "public"
	}
	if e.Privacy != "public" && e.Privacy != "private" {
		return fmt.Errorf("privacy must be public or private, got %q", e.Privacy)
	}
	if len(e.Items) == 0 {
		return fmt.Errorf("items nonempty")
	}
	for i := range e.Items {
		if err := e.Items[i].validate(i); err != nil {
			return err
		}
	}
	return nil
}

func (i *Item) validate(index int) error {
	i.Ref = strings.TrimSpace(i.Ref)
	i.URL = strings.TrimSpace(i.URL)
	i.Title = strings.TrimSpace(i.Title)
	i.PublishedAt = strings.TrimSpace(i.PublishedAt)
	i.Content = strings.TrimSpace(i.Content)
	i.ContentRef = strings.TrimSpace(i.ContentRef)
	i.Lang = strings.TrimSpace(i.Lang)
	if i.Ref == "" {
		return fmt.Errorf("items[%d].ref required", index)
	}
	if i.Content == "" && i.ContentRef == "" {
		return fmt.Errorf("items[%d] needs content or content_ref", index)
	}
	i.Authors = cleanStrings(i.Authors)
	i.Topics = cleanStrings(i.Topics)
	return nil
}

func ImportFile(homePath string, store *mem.Store, path string, now time.Time) (ImportReport, error) {
	export, data, err := LoadExport(path)
	if err != nil {
		return ImportReport{}, err
	}
	return Import(homePath, store, export, digestBytes(data), now)
}

func Import(homePath string, store *mem.Store, export Export, exportDigest string, now time.Time) (ImportReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return ImportReport{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if store == nil {
		return ImportReport{}, fmt.Errorf("memory store required")
	}
	if exportDigest == "" {
		exportDigest = digestStruct(export)
	}
	if err := export.Validate(); err != nil {
		return ImportReport{}, err
	}
	if err := ensureSourceRegistry(homePath, export.Source, now); err != nil {
		return ImportReport{}, err
	}

	imports, err := loadImportIndex(homePath)
	if err != nil {
		return ImportReport{}, err
	}
	report := ImportReport{Items: len(export.Items), EvidenceIDs: []string{}, CandidateIDs: []string{}, ExtractionIDs: []string{}}
	for _, item := range export.Items {
		canonical := canonicalRef(item)
		if prior, ok := findExactImport(imports, canonical, exportDigest); ok {
			report.Reused++
			report.EvidenceIDs = append(report.EvidenceIDs, prior.EvidenceRef)
			continue
		}

		prior, hasRecent := findRecentImport(imports, canonical, now, 7*24*time.Hour)
		evidence, err := store.RecordEvidence(mem.EvidenceItem{
			SourceKind: export.Source.Kind,
			SourceID:   item.Ref,
			QuoteText:  boundedQuote(item.Content),
			EventTime:  item.PublishedAt,
			Actor:      "tool",
			Visibility: evidenceVisibility(export.Privacy),
		}, now)
		if err != nil {
			return report, err
		}
		report.EvidenceIDs = append(report.EvidenceIDs, evidence.ID)
		row := ImportIndexRow{
			CanonicalRef: canonical,
			EvidenceRef:  evidence.ID,
			ImportedAt:   rfc3339(now),
			ExportDigest: exportDigest,
		}

		if hasRecent {
			if err := appendDistributionEvent(homePath, DistributionEvent{
				CanonicalRef:     canonical,
				EchoSourceKind:   export.Source.Kind,
				EchoSourceRef:    item.Ref,
				FirstEvidenceRef: prior.EvidenceRef,
				ObservedAt:       rfc3339(now),
			}); err != nil {
				return report, err
			}
			if err := appendImportIndex(homePath, row); err != nil {
				return report, err
			}
			imports = append(imports, row)
			report.Echoes++
			continue
		}

		candidate := candidateForItem(export, item, evidence.ID, now)
		if _, err := store.RecordStableCandidate(candidate); err != nil {
			return report, err
		}
		extraction, err := writeExtractionRequest(homePath, export, item, evidence.ID, now)
		if err != nil {
			return report, err
		}
		if err := appendImportIndex(homePath, row); err != nil {
			return report, err
		}
		imports = append(imports, row)
		report.Imported++
		report.CandidateIDs = append(report.CandidateIDs, candidate.ID)
		report.ExtractionIDs = append(report.ExtractionIDs, extraction.ID)
	}
	return report, nil
}

func ensureSourceRegistry(homePath string, source Source, now time.Time) error {
	path := filepath.Join(homePath, "registries", "knowledge-sources.jsonl")
	rows, err := loadSourceRegistry(path)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Kind == source.Kind && row.Ref == source.Ref {
			return nil
		}
	}
	return appendJSONL(path, SourceRegistryRow{
		SourceID:       "source:" + source.Kind + ":" + source.Ref,
		Kind:           source.Kind,
		Ref:            source.Ref,
		FirstSeen:      rfc3339(now),
		TrustPosterior: 0.5,
	})
}

func candidateForItem(export Export, item Item, evidenceID string, now time.Time) mem.Candidate {
	entity := "knowledge:" + export.Source.Kind + ":" + item.Ref
	return mem.Candidate{
		ID:              "cand_" + shortDigest(entity, evidenceID, item.Title, strings.Join(item.Authors, "\x00"), strings.Join(item.Topics, "\x00")),
		Entity:          entity,
		Predicate:       "reference_metadata",
		Object:          metadataClaim(item),
		ObservationDate: item.PublishedAt,
		Confidence:      0.8,
		Derivation:      "deterministic",
		Qualifiers: map[string]string{
			"source":      "knowledge_import",
			"source_kind": export.Source.Kind,
			"source_ref":  export.Source.Ref,
			"item_ref":    item.Ref,
			"canonical":   canonicalRef(item),
		},
		SourceTraceRef: evidenceID,
		SnippetHash:    shortDigest(item.Content, item.ContentRef, evidenceID),
		Visibility:     candidateVisibility(export.Privacy),
		State:          "proposed",
		CreatedAt:      rfc3339(now),
	}
}

func metadataClaim(item Item) string {
	parts := []string{}
	if item.Title != "" {
		parts = append(parts, "title="+item.Title)
	}
	if len(item.Authors) > 0 {
		parts = append(parts, "authors="+strings.Join(item.Authors, ", "))
	}
	if item.PublishedAt != "" {
		parts = append(parts, "published_at="+item.PublishedAt)
	}
	if len(item.Topics) > 0 {
		parts = append(parts, "topics="+strings.Join(item.Topics, ", "))
	}
	if item.URL != "" {
		parts = append(parts, "url="+item.URL)
	}
	if len(parts) == 0 {
		return "reference " + item.Ref
	}
	return strings.Join(parts, "; ")
}

func writeExtractionRequest(homePath string, export Export, item Item, evidenceID string, now time.Time) (ExtractionRequest, error) {
	id := "kx_" + shortDigest(canonicalRef(item), evidenceID)
	req := ExtractionRequest{
		SchemaVersion: ExtractionRequestSchemaVersion,
		ID:            id,
		ItemRef:       item.Ref,
		EvidenceRef:   evidenceID,
		SourceKind:    export.Source.Kind,
		Title:         item.Title,
		ContentDigest: contentDigest(item),
		State:         "pending",
		CreatedAt:     rfc3339(now),
	}
	path := filepath.Join(homePath, "inbox", "knowledge-extractions", id+".json")
	if _, err := os.Stat(path); err == nil {
		return req, nil
	} else if !os.IsNotExist(err) {
		return ExtractionRequest{}, err
	}
	if err := writeJSON(path, req); err != nil {
		return ExtractionRequest{}, err
	}
	return req, nil
}

func appendDistributionEvent(homePath string, event DistributionEvent) error {
	return appendJSONL(filepath.Join(homePath, "ledger", "knowledge-distribution.jsonl"), event)
}

func appendImportIndex(homePath string, row ImportIndexRow) error {
	return appendJSONL(filepath.Join(homePath, "indexes", "knowledge-imports.jsonl"), row)
}

func loadImportIndex(homePath string) ([]ImportIndexRow, error) {
	var rows []ImportIndexRow
	return rows, readJSONL(filepath.Join(homePath, "indexes", "knowledge-imports.jsonl"), func(line []byte) error {
		var row ImportIndexRow
		if err := json.Unmarshal(line, &row); err != nil || row.CanonicalRef == "" {
			return nil
		}
		rows = append(rows, row)
		return nil
	})
}

func loadSourceRegistry(path string) ([]SourceRegistryRow, error) {
	var rows []SourceRegistryRow
	return rows, readJSONL(path, func(line []byte) error {
		var row SourceRegistryRow
		if err := json.Unmarshal(line, &row); err != nil || row.Kind == "" || row.Ref == "" {
			return nil
		}
		rows = append(rows, row)
		return nil
	})
}

func findExactImport(rows []ImportIndexRow, canonical, exportDigest string) (ImportIndexRow, bool) {
	for _, row := range rows {
		if row.CanonicalRef == canonical && row.ExportDigest == exportDigest {
			return row, true
		}
	}
	return ImportIndexRow{}, false
}

func findRecentImport(rows []ImportIndexRow, canonical string, now time.Time, window time.Duration) (ImportIndexRow, bool) {
	var best ImportIndexRow
	var bestTime time.Time
	for _, row := range rows {
		if row.CanonicalRef != canonical {
			continue
		}
		importedAt, err := time.Parse(time.RFC3339, row.ImportedAt)
		if err != nil {
			continue
		}
		age := now.Sub(importedAt)
		if age < 0 || age > window {
			continue
		}
		if best.EvidenceRef == "" || importedAt.After(bestTime) {
			best = row
			bestTime = importedAt
		}
	}
	return best, best.EvidenceRef != ""
}

func canonicalRef(item Item) string {
	if strings.TrimSpace(item.URL) != "" {
		return strings.TrimSpace(item.URL)
	}
	return strings.TrimSpace(item.Ref)
}

func boundedQuote(content string) string {
	content = strings.TrimSpace(content)
	if len(content) > maxEvidenceQuoteChars {
		return content[:maxEvidenceQuoteChars]
	}
	return content
}

func contentDigest(item Item) string {
	content := item.Content
	if strings.TrimSpace(content) == "" {
		content = item.ContentRef
	}
	return digestString(content)
}

func evidenceVisibility(privacy string) string {
	if privacy == "private" {
		return "private"
	}
	return "operational"
}

func candidateVisibility(privacy string) string {
	if privacy == "private" {
		return "private"
	}
	return "candidate"
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func appendJSONL(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func readJSONL(path string, fn func([]byte) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	for _, line := range strings.Split(trimmed, "\n") {
		if err := fn([]byte(line)); err != nil {
			return err
		}
	}
	return nil
}

func cleanStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func digestString(value string) string {
	return digestBytes([]byte(value))
}

func digestStruct(v any) string {
	data, _ := json.Marshal(v)
	return digestBytes(data)
}

func shortDigest(parts ...string) string {
	return digestString(strings.Join(parts, "\x00"))[:16]
}

func rfc3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
