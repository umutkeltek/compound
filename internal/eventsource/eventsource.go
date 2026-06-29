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
	Status   string   `json:"status"`
	Stream   string   `json:"stream"`
	Imported int      `json:"imported"`
	Skipped  int      `json:"skipped"`
	TraceIDs []string `json:"trace_ids"`
}

type osEvent struct {
	SchemaVersion   int            `json:"schema_version"`
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	EventClass      string         `json:"event_class"`
	MetadataQuality string         `json:"metadata_quality"`
	MutationClass   string         `json:"mutation_class"`
	Producer        map[string]any `json:"producer"`
	Subject         map[string]any `json:"subject"`
	Evidence        []osEvidence   `json:"evidence"`
	Summary         string         `json:"summary"`
	TS              string         `json:"ts"`
	Details         map[string]any `json:"details"`
	Dashboard       map[string]any `json:"dashboard"`
}

type osEvidence struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Redacted bool   `json:"redacted"`
}

func ImportOSEvents(homePath, path string, opts ImportOptions, now time.Time) (ImportReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return ImportReport{}, fmt.Errorf("home path required")
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
	return report, nil
}

func BuildTraceEnvelope(ev osEvent, path string, lineNo int, opts ImportOptions, now time.Time) (envelope.TraceEnvelope, error) {
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
