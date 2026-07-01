// Package eventsource adapts existing append-only event streams into Compound's
// typed trace envelope spine. It observes what happened; it never promotes
// memory or mutates the source event log.
package eventsource

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
)

type ImportOptions struct {
	Limit     int
	SourceRef string
	Stream    string
}

type ImportReport struct {
	Status            string                `json:"status"`
	Stream            string                `json:"stream"`
	AuthorityRef      string                `json:"authority_ref,omitempty"`
	SourceAuthorityID string                `json:"source_authority_id,omitempty"`
	SourceAuthority   SourceAuthorityRecord `json:"source_authority,omitempty"`
	Imported          int                   `json:"imported"`
	Skipped           int                   `json:"skipped"`
	TraceIDs          []string              `json:"trace_ids"`
}

type AuthorityReport struct {
	SchemaVersion       string                  `json:"schema_version"`
	Status              string                  `json:"status"`
	Installed           bool                    `json:"installed"`
	AuthorityRef        string                  `json:"authority_ref"`
	TraceCount          int                     `json:"trace_count"`
	SourceAuthorityPath string                  `json:"source_authority_path"`
	SourceAuthority     []SourceAuthorityRecord `json:"source_authority"`
	RecentTraces        []TraceSummary          `json:"recent_traces"`
}

type TraceSummary struct {
	ID            string `json:"id"`
	CreatedAt     string `json:"created_at"`
	Stream        string `json:"stream,omitempty"`
	SourceKind    string `json:"source_kind,omitempty"`
	EventClass    string `json:"event_class,omitempty"`
	MutationClass string `json:"mutation_class,omitempty"`
	Summary       string `json:"summary,omitempty"`
}

const (
	TraceEventsAuthorityRef    = "compound://events/trace-envelopes"
	TraceAuthorityReportSchema = "compound.event_trace_authority_report.v0.1"
	SourceAuthoritySchema      = "compound.event_source_authority.v0.1"
	eventSourceAuthorityFile   = "event-source-authority.jsonl"
	eventSourceImportCommand   = "event import-os"
	eventSourceRetiredRoleName = "source_event_stream_authority"
)

type SourceAuthorityRecord struct {
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
	Stream              string   `json:"stream"`
	Imported            int      `json:"imported"`
	Skipped             int      `json:"skipped"`
	TraceIDs            []string `json:"trace_ids,omitempty"`
	EvidenceRefs        []string `json:"evidence_refs"`
}

type osEvent struct {
	SchemaVersion   int            `json:"schema_version"`
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	Type            string         `json:"type"`
	EventClass      string         `json:"event_class"`
	MetadataQuality string         `json:"metadata_quality"`
	MutationClass   string         `json:"mutation_class"`
	Producer        objectMap      `json:"producer"`
	Subject         objectMap      `json:"subject"`
	Evidence        osEvidenceList `json:"evidence"`
	Summary         string         `json:"summary"`
	TS              string         `json:"ts"`
	Timestamp       string         `json:"timestamp"`
	Details         map[string]any `json:"details"`
	Dashboard       map[string]any `json:"dashboard"`
	Provider        string         `json:"provider"`
	Scope           []string       `json:"scope"`
	Source          string         `json:"source"`
	Event           string         `json:"event"`
	Project         string         `json:"project"`
	Artifact        string         `json:"artifact"`
	Actor           string         `json:"actor"`
}

type objectMap map[string]any

func (m *objectMap) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*m = nil
		return nil
	}
	if strings.HasPrefix(raw, "{") {
		var obj map[string]any
		if err := json.Unmarshal(data, &obj); err != nil {
			return err
		}
		*m = obj
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*m = map[string]any{"value": value}
	return nil
}

type osEvidence struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Redacted bool   `json:"redacted"`
}

type osEvidenceList []osEvidence

func (l *osEvidenceList) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*l = nil
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var rows []osEvidence
		if err := json.Unmarshal(data, &rows); err != nil {
			return err
		}
		*l = rows
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	rows := make([]osEvidence, 0, len(obj))
	for key, value := range obj {
		rows = append(rows, osEvidence{Kind: key, Value: evidenceValueString(value)})
	}
	*l = rows
	return nil
}

func ImportOSEvents(homePath, path string, opts ImportOptions, now time.Time) (ImportReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return ImportReport{}, fmt.Errorf("home path required")
	}
	revision, err := fileRevision(path)
	if err != nil {
		return ImportReport{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return ImportReport{}, err
	}
	defer f.Close()

	stream := strings.TrimSpace(opts.Stream)
	if stream == "" {
		stream = filepath.Base(path)
	}
	report := ImportReport{Status: "ok", Stream: stream}
	scanner := bufio.NewScanner(f)
	// Event rows may carry detailed evidence arrays; keep headroom above Scanner's
	// default 64K token limit without pretending this is a transcript store.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	seen := 0
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		seen++
		if opts.Limit > 0 && seen > opts.Limit {
			break
		}
		var ev osEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return report, fmt.Errorf("parse %s:%d: %w", path, lineNo, err)
		}
		trace, err := BuildTraceEnvelope(ev, path, lineNo, opts, now)
		if err != nil {
			return report, fmt.Errorf("build trace %s:%d: %w", path, lineNo, err)
		}
		if err := trace.Validate(); err != nil {
			return report, fmt.Errorf("validate trace %s:%d: %w", path, lineNo, err)
		}
		wrote, err := recordTrace(homePath, trace)
		if err != nil {
			return report, err
		}
		if !wrote {
			report.Skipped++
			continue
		}
		report.Imported++
		report.TraceIDs = append(report.TraceIDs, trace.ID)
	}
	if err := scanner.Err(); err != nil {
		return report, err
	}
	rec := SourceAuthorityRecord{
		SchemaVersion:       SourceAuthoritySchema,
		ID:                  idFor("evsrc_", path, revision, stream),
		CreatedAt:           rfc3339(now),
		SourcePath:          path,
		SourceRevision:      revision,
		ImportedVia:         eventSourceImportCommand,
		AuthorityRef:        TraceEventsAuthorityRef,
		AuthorityStatus:     "compound_primary",
		SourceRole:          "evidence_only_input_after_import",
		Retires:             eventSourceRetiredRoleName + ":" + path,
		RetirementCondition: "compound event import-os writes TraceEnvelope files under COMPOUND_HOME/events; source stream remains append-only rollback/evidence input",
		Rollback:            "re-import source event stream or remove COMPOUND_HOME/events trace envelopes for this stream after review",
		Stream:              stream,
		Imported:            report.Imported,
		Skipped:             report.Skipped,
		TraceIDs:            append([]string(nil), report.TraceIDs...),
		EvidenceRefs:        []string{revision, TraceEventsAuthorityRef},
	}
	if err := appendSourceAuthority(homePath, rec); err != nil {
		return report, err
	}
	report.AuthorityRef = TraceEventsAuthorityRef
	report.SourceAuthorityID = rec.ID
	report.SourceAuthority = rec
	return report, nil
}

func BuildTraceEnvelope(ev osEvent, path string, lineNo int, opts ImportOptions, now time.Time) (envelope.TraceEnvelope, error) {
	ev = normalizeOSEvent(ev)
	if ev.SchemaVersion == 0 {
		return envelope.TraceEnvelope{}, fmt.Errorf("schema_version required")
	}
	sourceID := strings.TrimSpace(ev.ID)
	if sourceID == "" {
		sourceID = idFor("os_event_", path, itoa(lineNo), ev.TS, ev.Kind, ev.Summary)
	}
	createdAt := strings.TrimSpace(ev.TS)
	if createdAt == "" {
		createdAt = now.UTC().Format(time.RFC3339)
	}
	stream := strings.TrimSpace(opts.Stream)
	if stream == "" {
		stream = filepath.Base(path)
	}
	rawRef := strings.TrimSpace(opts.SourceRef)
	if rawRef == "" {
		rawRef = path
	}
	caller := anyString(ev.Producer["caller"])
	agent := anyString(ev.Producer["agent"])
	runID := anyString(ev.Producer["run_id"])
	actor := firstNonEmpty(agent, caller)
	mutation := normalizeMutationClass(ev.MutationClass)
	files := filesTouched(ev)
	evidenceRefs := evidenceRefs(ev)
	return envelope.TraceEnvelope{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTraceEnvelope,
			ID:            idFor("trace_", sourceID),
			CreatedAt:     createdAt,
			Producer: envelope.Producer{
				Kind:    "event_source",
				Name:    firstNonEmpty(caller, "os-event"),
				Version: anyString(ev.Producer["version"]),
			},
			IDs: envelope.IDs{
				RunID: runID,
			},
			Scope: envelope.Scope{
				ProjectID: anyString(ev.Subject["project"]),
				Surface:   anyString(ev.Subject["path"]),
			},
			Authority: envelope.Authority{
				SourceClass: "runtime",
				Status:      "current",
			},
			Privacy:      "private",
			Risk:         riskForMutation(mutation),
			EvidenceRefs: evidenceRefs,
			Uncertainty: envelope.Uncertainty{
				Level: uncertaintyForQuality(ev.MetadataQuality),
			},
		},
		Source: envelope.TraceSource{
			System:      "os-event",
			Stream:      stream,
			SourceID:    sourceID,
			SourceKind:  ev.Kind,
			EventClass:  ev.EventClass,
			RawRef:      rawRef,
			Line:        lineNo,
			Quality:     ev.MetadataQuality,
			MutationRaw: ev.MutationClass,
		},
		Actor:           actor,
		StartedAt:       createdAt,
		EndedAt:         createdAt,
		MutationClass:   mutation,
		Summary:         ev.Summary,
		ToolsModelsUsed: toolsModelsUsed(ev),
		FilesTouched:    files,
		Outputs:         evidenceRefs,
		Cost:            envelope.ReceiptCost{ModelCalls: []string{}},
	}, nil
}

func normalizeOSEvent(ev osEvent) osEvent {
	if ev.SchemaVersion == 0 && (strings.TrimSpace(ev.Type) != "" ||
		strings.TrimSpace(ev.Timestamp) != "" ||
		strings.TrimSpace(ev.Event) != "" ||
		(strings.TrimSpace(ev.Kind) != "" && strings.TrimSpace(ev.TS) != "")) {
		ev.SchemaVersion = 1
	}
	if strings.TrimSpace(ev.Kind) == "" {
		ev.Kind = firstNonEmpty(ev.Type, ev.Event)
	}
	if strings.TrimSpace(ev.TS) == "" {
		ev.TS = strings.TrimSpace(ev.Timestamp)
	}
	if strings.TrimSpace(ev.EventClass) == "" && strings.TrimSpace(ev.Type) != "" {
		ev.EventClass = "legacy"
	}
	if strings.TrimSpace(ev.MetadataQuality) == "" {
		ev.MetadataQuality = "partial"
	}
	if ev.Producer == nil {
		ev.Producer = objectMap{}
	}
	if _, ok := ev.Producer["caller"]; !ok {
		if value := anyString(ev.Producer["value"]); value != "" {
			ev.Producer["caller"] = value
		}
	}
	if _, ok := ev.Producer["caller"]; !ok && strings.TrimSpace(ev.Source) != "" {
		ev.Producer["caller"] = ev.Source
	}
	if _, ok := ev.Producer["agent"]; !ok && strings.TrimSpace(ev.Actor) != "" {
		ev.Producer["agent"] = ev.Actor
	}
	if ev.Subject == nil {
		ev.Subject = objectMap{}
	}
	if _, ok := ev.Subject["project"]; !ok && strings.TrimSpace(ev.Project) != "" {
		ev.Subject["project"] = ev.Project
	}
	if _, ok := ev.Subject["path"]; !ok && strings.TrimSpace(ev.Artifact) != "" {
		ev.Subject["path"] = ev.Artifact
	}
	if _, ok := ev.Subject["provider"]; !ok && strings.TrimSpace(ev.Provider) != "" {
		ev.Subject["provider"] = ev.Provider
	}
	if len(ev.Evidence) == 0 && strings.TrimSpace(ev.Artifact) != "" {
		ev.Evidence = osEvidenceList{{Kind: "path", Value: ev.Artifact}}
	}
	if _, ok := ev.Subject["scope"]; !ok && len(ev.Scope) > 0 {
		ev.Subject["scope"] = strings.Join(ev.Scope, ",")
	}
	return ev
}

func recordTrace(homePath string, trace envelope.TraceEnvelope) (bool, error) {
	path := filepath.Join(homePath, "events", trace.ID+".json")
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	b, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return false, err
	}
	streamPath := filepath.Join(homePath, "events", "traces.ndjson")
	f, err := os.OpenFile(streamPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	row, err := json.Marshal(trace)
	if err != nil {
		return false, err
	}
	if _, err := f.Write(append(row, '\n')); err != nil {
		return false, err
	}
	return true, nil
}

func SourceAuthorityPath(homePath string) string {
	return filepath.Join(homePath, "registries", eventSourceAuthorityFile)
}

func LoadSourceAuthority(homePath string) ([]SourceAuthorityRecord, error) {
	data, err := os.ReadFile(SourceAuthorityPath(homePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []SourceAuthorityRecord
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec SourceAuthorityRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parse event source authority line %d: %w", i+1, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func LoadAuthority(homePath string, recentLimit int) (AuthorityReport, error) {
	if recentLimit < 0 {
		recentLimit = 0
	}
	records, err := LoadSourceAuthority(homePath)
	if err != nil {
		return AuthorityReport{}, err
	}
	traceCount, err := countTraceFiles(homePath)
	if err != nil {
		return AuthorityReport{}, err
	}
	recent, err := loadRecentTraceSummaries(homePath, recentLimit)
	if err != nil {
		return AuthorityReport{}, err
	}
	installed := traceCount > 0 || len(records) > 0
	status := "missing"
	if installed {
		status = "ok"
	}
	return AuthorityReport{
		SchemaVersion:       TraceAuthorityReportSchema,
		Status:              status,
		Installed:           installed,
		AuthorityRef:        TraceEventsAuthorityRef,
		TraceCount:          traceCount,
		SourceAuthorityPath: SourceAuthorityPath(homePath),
		SourceAuthority:     records,
		RecentTraces:        recent,
	}, nil
}

func countTraceFiles(homePath string) (int, error) {
	entries, err := os.ReadDir(filepath.Join(homePath, "events"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "trace_") && strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	return count, nil
}

func loadRecentTraceSummaries(homePath string, limit int) ([]TraceSummary, error) {
	if limit == 0 {
		return nil, nil
	}
	path := filepath.Join(homePath, "events", "traces.ndjson")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
		if len(lines) > limit {
			lines = lines[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	summaries := make([]TraceSummary, 0, len(lines))
	for i, line := range lines {
		var trace envelope.TraceEnvelope
		if err := json.Unmarshal([]byte(line), &trace); err != nil {
			return nil, fmt.Errorf("parse trace summary line %d: %w", i+1, err)
		}
		summaries = append(summaries, TraceSummary{
			ID:            trace.ID,
			CreatedAt:     trace.CreatedAt,
			Stream:        trace.Source.Stream,
			SourceKind:    trace.Source.SourceKind,
			EventClass:    trace.Source.EventClass,
			MutationClass: trace.MutationClass,
			Summary:       trace.Summary,
		})
	}
	return summaries, nil
}

func appendSourceAuthority(homePath string, rec SourceAuthorityRecord) error {
	path := SourceAuthorityPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func fileRevision(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func evidenceRefs(ev osEvent) []string {
	var refs []string
	for _, e := range ev.Evidence {
		kind := strings.TrimSpace(e.Kind)
		if kind == "" {
			kind = "evidence"
		}
		if e.Redacted {
			refs = append(refs, kind+":<redacted>")
			continue
		}
		if v := strings.TrimSpace(e.Value); v != "" {
			refs = append(refs, kind+":"+v)
		}
	}
	return mergeStrings(refs)
}

func filesTouched(ev osEvent) []string {
	var out []string
	if p := anyString(ev.Subject["path"]); p != "" {
		out = append(out, p)
	}
	for _, e := range ev.Evidence {
		if e.Redacted || strings.TrimSpace(e.Kind) != "path" {
			continue
		}
		if v := strings.TrimSpace(e.Value); v != "" {
			out = append(out, v)
		}
	}
	return mergeStrings(out)
}

func toolsModelsUsed(ev osEvent) []string {
	var out []string
	for _, key := range []string{"service", "provider", "model"} {
		if v := anyString(ev.Subject[key]); v != "" {
			out = append(out, key+":"+v)
		}
	}
	if v := anyString(ev.Producer["caller"]); v != "" {
		out = append(out, "caller:"+v)
	}
	if v := anyString(ev.Producer["agent"]); v != "" {
		out = append(out, "agent:"+v)
	}
	return mergeStrings(out)
}

func normalizeMutationClass(raw string) string {
	switch strings.TrimSpace(raw) {
	case "", "none", "read", "read_only":
		return "read"
	case "local_artifact_write", "local_write", "local_reversible":
		return "local_reversible"
	case "external_side_effect", "public", "destructive", "financial", "security":
		return raw
	default:
		return "local_reversible"
	}
}

func riskForMutation(mutation string) string {
	switch mutation {
	case "read":
		return "tier0"
	case "local_reversible":
		return "tier1"
	case "external_side_effect", "public":
		return "tier2"
	default:
		return "tier3"
	}
}

func uncertaintyForQuality(q string) string {
	switch strings.TrimSpace(q) {
	case "complete":
		return "known"
	case "":
		return "unknown"
	default:
		return "inferred"
	}
}

func anyString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func evidenceValueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")
	default:
		b, err := json.Marshal(x)
		if err == nil {
			return string(b)
		}
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func mergeStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func idFor(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + hex.EncodeToString(h[:])[:12]
}

func itoa(v int) string {
	return fmt.Sprintf("%d", v)
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339)
}
