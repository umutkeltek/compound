package routeevidence

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

	"compound/internal/envelope"
)

const SchemaVersion = "compound.execution_route_evidence.v0.1"

type Usage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

type Target struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Protocol string `json:"protocol"`
}

type RejectedTarget struct {
	Target Target `json:"target"`
	Reason string `json:"reason"`
}

type Evidence struct {
	SchemaVersion  string           `json:"schema_version"`
	ID             string           `json:"id"`
	CreatedAt      string           `json:"created_at"`
	RequestID      string           `json:"request_id"`
	WorkID         string           `json:"work_id,omitempty"`
	TransitionID   string           `json:"transition_id,omitempty"`
	ClientProfile  string           `json:"client_profile"`
	Protocol       string           `json:"protocol"`
	SelectedTarget Target           `json:"selected_target"`
	Fallbacks      []Target         `json:"fallbacks,omitempty"`
	Rejected       []RejectedTarget `json:"rejected,omitempty"`
	Usage          Usage            `json:"usage,omitempty"`
	CostMicros     int64            `json:"cost_micros,omitempty"`
	LatencyMS      int64            `json:"latency_ms,omitempty"`
	Status         string           `json:"status"`
	EvidenceRefs   []string         `json:"evidence_refs,omitempty"`
}

type BuildInput struct {
	CreatedAt     time.Time
	RequestID     string
	WorkID        string
	ClientProfile string
	Protocol      string
	Provider      string
	Model         string
	Usage         Usage
	CostMicros    int64
	LatencyMS     int64
	Status        string
	EvidenceRefs  []string
}

type ImportResult struct {
	Evidence   Evidence            `json:"evidence"`
	Transition envelope.Transition `json:"transition"`
}

func Build(in BuildInput) (Evidence, error) {
	now := in.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = "completed"
	}
	ev := Evidence{
		SchemaVersion: SchemaVersion,
		CreatedAt:     rfc3339(now),
		RequestID:     strings.TrimSpace(in.RequestID),
		WorkID:        strings.TrimSpace(in.WorkID),
		ClientProfile: strings.TrimSpace(in.ClientProfile),
		Protocol:      strings.TrimSpace(in.Protocol),
		SelectedTarget: Target{
			Provider: strings.TrimSpace(in.Provider),
			Model:    strings.TrimSpace(in.Model),
			Protocol: strings.TrimSpace(in.Protocol),
		},
		Usage:        in.Usage,
		CostMicros:   in.CostMicros,
		LatencyMS:    in.LatencyMS,
		Status:       status,
		EvidenceRefs: dedupeRefs(in.EvidenceRefs),
	}
	ev.ID = idFor("route_", ev.RequestID, ev.CreatedAt, ev.SelectedTarget.Provider, ev.SelectedTarget.Model, ev.Status)
	if err := Validate(ev); err != nil {
		return Evidence{}, err
	}
	return ev, nil
}

func Decode(data []byte) (Evidence, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var ev Evidence
	if err := dec.Decode(&ev); err != nil {
		return Evidence{}, fmt.Errorf("decode execution route evidence: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Evidence{}, fmt.Errorf("decode execution route evidence: trailing JSON")
		}
		return Evidence{}, fmt.Errorf("decode execution route evidence trailing JSON: %w", err)
	}
	if err := Validate(ev); err != nil {
		return Evidence{}, err
	}
	return ev, nil
}

func ImportFile(homePath, path string) (ImportResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("read execution route evidence %s: %w", path, err)
	}
	ev, err := Decode(data)
	if err != nil {
		return ImportResult{}, err
	}
	return RecordJoined(homePath, ev)
}

func RecordJoined(homePath string, ev Evidence) (ImportResult, error) {
	ev, tr, err := JoinTransition(ev)
	if err != nil {
		return ImportResult{}, err
	}
	if err := RecordEvidence(homePath, ev); err != nil {
		return ImportResult{}, err
	}
	if err := RecordTransition(homePath, tr); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Evidence: ev, Transition: tr}, nil
}

func JoinTransition(ev Evidence) (Evidence, envelope.Transition, error) {
	if err := Validate(ev); err != nil {
		return Evidence{}, envelope.Transition{}, err
	}
	tr, err := BuildTransition(ev)
	if err != nil {
		return Evidence{}, envelope.Transition{}, err
	}
	if ev.TransitionID != "" && ev.TransitionID != tr.ID {
		return Evidence{}, envelope.Transition{}, fmt.Errorf("transition_id=%q does not match computed %q", ev.TransitionID, tr.ID)
	}
	ev.TransitionID = tr.ID
	return ev, tr, nil
}

func BuildTransition(ev Evidence) (envelope.Transition, error) {
	if err := Validate(ev); err != nil {
		return envelope.Transition{}, err
	}
	workID := ev.WorkID
	if workID == "" {
		workID = idFor("work_route_", ev.ID)
	}
	evidenceRefs := dedupeRefs(append([]string{ev.ID}, ev.EvidenceRefs...))
	costRefs := []string{}
	if ev.CostMicros > 0 || ev.Usage.TotalTokens > 0 {
		costRefs = []string{ev.ID}
	}
	tr := envelope.Transition{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTransition,
			ID:            idFor("tr_", ev.ID, ev.RequestID, ev.CreatedAt),
			CreatedAt:     ev.CreatedAt,
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  evidenceRefs,
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		SourceTraceID: ev.RequestID,
		BeforeRefs:    append([]string{}, ev.EvidenceRefs...),
		ActionRefs:    []string{ev.RequestID},
		AfterRefs:     []string{ev.ID},
		StateDelta:    transitionDelta(ev),
		Projections: envelope.Projections{
			Memory:         []string{},
			Skill:          []string{},
			Product:        []string{},
			Cost:           costRefs,
			Risk:           []string{},
			Taste:          []string{},
			Routing:        []string{ev.ID},
			HumanAttention: []string{},
		},
		SecondOrder: envelope.SecondOrder{FutureBehaviorChanged: false},
	}
	if err := tr.Validate(); err != nil {
		return envelope.Transition{}, err
	}
	return tr, nil
}

func RecordEvidence(homePath string, ev Evidence) error {
	if err := Validate(ev); err != nil {
		return err
	}
	path := filepath.Join(homePath, "ledger", "execution-route-evidence.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(&ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func RecordTransition(homePath string, tr envelope.Transition) error {
	if err := tr.Validate(); err != nil {
		return err
	}
	path := filepath.Join(homePath, "events", tr.ID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(&tr, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func Validate(ev Evidence) error {
	var errs []string
	req := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, name+" required")
		}
	}
	req("schema_version", ev.SchemaVersion)
	req("id", ev.ID)
	req("created_at", ev.CreatedAt)
	req("request_id", ev.RequestID)
	req("client_profile", ev.ClientProfile)
	req("protocol", ev.Protocol)
	req("selected_target.provider", ev.SelectedTarget.Provider)
	req("selected_target.model", ev.SelectedTarget.Model)
	req("selected_target.protocol", ev.SelectedTarget.Protocol)
	req("status", ev.Status)
	if ev.SchemaVersion != SchemaVersion {
		errs = append(errs, fmt.Sprintf("schema_version=%q, want %q", ev.SchemaVersion, SchemaVersion))
	}
	if ev.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, ev.CreatedAt); err != nil {
			errs = append(errs, fmt.Sprintf("created_at invalid: %v", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("execution route evidence invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

func transitionDelta(ev Evidence) string {
	parts := []string{
		"route evidence recorded",
		ev.SelectedTarget.Provider + "/" + ev.SelectedTarget.Model,
		"via " + ev.Protocol,
		"status " + ev.Status,
	}
	if ev.Usage.TotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("%d tokens", ev.Usage.TotalTokens))
	}
	if ev.LatencyMS > 0 {
		parts = append(parts, fmt.Sprintf("%dms", ev.LatencyMS))
	}
	parts = append(parts, "not task success")
	return strings.Join(parts, "; ")
}

func dedupeRefs(refs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

func idFor(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(h[:])[:12]
}

func rfc3339(now time.Time) string { return now.UTC().Format(time.RFC3339) }
