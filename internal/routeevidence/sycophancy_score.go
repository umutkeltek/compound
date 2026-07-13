package routeevidence

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// SycophancyScoreSchemaVersion versions the per-lane sycophancy measurement row.
	SycophancyScoreSchemaVersion = "compound.lane_sycophancy_score.v0.1"
	laneSycophancyScoreLedger    = "lane-sycophancy-scores.jsonl"

	// MethodPairedStanceProbe names the measurement that produced a score: the same
	// artifact judged twice per lane under inverted operator framing, with the signed
	// endorsement shift attributed to stance alone.
	MethodPairedStanceProbe = "paired_stance_probe"
)

// SycophancyScore is per-lane-tuple route evidence: how far a judging lane's verdict
// moves when only the operator's stance is inverted (the artifact held constant). It
// lives alongside RouteDecision evidence because, like a RouteDecision, it describes a
// LANE's behavior, not a task's truth — a lane that shifts with the operator is
// tracking the operator, and that is route-selection evidence.
//
// It is EVIDENCE, never a gate. It deliberately carries NO pass/fail threshold: a
// route-authority max-stance-shift threshold is a separate, later decision and MUST
// NOT be inferred from a single measurement (predicate law — at least one real lane's
// shift must be recorded before any threshold is set).
type SycophancyScore struct {
	SchemaVersion   string   `json:"schema_version"`
	ID              string   `json:"id"`
	CreatedAt       string   `json:"created_at"`
	LaneNodeID      string   `json:"lane_node_id"`
	ShortLaneID     string   `json:"short_lane_id,omitempty"`
	Method          string   `json:"method"`
	ProbeCount      int      `json:"probe_count"`
	MeanStanceShift float64  `json:"mean_stance_shift"`
	MaxStanceShift  float64  `json:"max_stance_shift"`
	EvidenceRefs    []string `json:"evidence_refs,omitempty"`
	RecordedVia     string   `json:"recorded_via"`
}

// BuildSycophancyScoreInput carries the plain measurement fields. The measuring organ
// (internal/sycophancy) hands these across without routeevidence importing it, so the
// score record's home stays in the route-evidence family while the probe harness stays
// a separate evaluator organ.
type BuildSycophancyScoreInput struct {
	CreatedAt       time.Time
	LaneNodeID      string
	ShortLaneID     string
	Method          string
	ProbeCount      int
	MeanStanceShift float64
	MaxStanceShift  float64
	EvidenceRefs    []string
}

func BuildSycophancyScore(in BuildSycophancyScoreInput) (SycophancyScore, error) {
	now := in.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	method := strings.TrimSpace(in.Method)
	if method == "" {
		method = MethodPairedStanceProbe
	}
	sc := SycophancyScore{
		SchemaVersion:   SycophancyScoreSchemaVersion,
		CreatedAt:       rfc3339(now),
		LaneNodeID:      strings.TrimSpace(in.LaneNodeID),
		ShortLaneID:     strings.TrimSpace(in.ShortLaneID),
		Method:          method,
		ProbeCount:      in.ProbeCount,
		MeanStanceShift: in.MeanStanceShift,
		MaxStanceShift:  in.MaxStanceShift,
		EvidenceRefs:    dedupeRefs(in.EvidenceRefs),
		RecordedVia:     RecorderGeneric,
	}
	sc.ID = idFor("sycophancy_", sc.LaneNodeID, sc.CreatedAt, sc.Method, strconv.Itoa(sc.ProbeCount))
	if err := ValidateSycophancyScore(sc); err != nil {
		return SycophancyScore{}, err
	}
	return sc, nil
}

func ValidateSycophancyScore(sc SycophancyScore) error {
	var errs []string
	req := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			errs = append(errs, name+" required")
		}
	}
	req("schema_version", sc.SchemaVersion)
	req("id", sc.ID)
	req("created_at", sc.CreatedAt)
	req("lane_node_id", sc.LaneNodeID)
	req("method", sc.Method)
	if sc.SchemaVersion != SycophancyScoreSchemaVersion {
		errs = append(errs, fmt.Sprintf("schema_version=%q, want %q", sc.SchemaVersion, SycophancyScoreSchemaVersion))
	}
	if sc.ProbeCount < 0 {
		errs = append(errs, "probe_count must be non-negative")
	}
	if sc.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, sc.CreatedAt); err != nil {
			errs = append(errs, fmt.Sprintf("created_at invalid: %v", err))
		}
	}
	switch sc.RecordedVia {
	case "", RecorderGeneric, RecorderExternalImport, RecorderTrustedPanelRuntime:
	default:
		errs = append(errs, fmt.Sprintf("recorded_via=%q is not recognized", sc.RecordedVia))
	}
	if len(errs) > 0 {
		return fmt.Errorf("lane sycophancy score invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

func DecodeSycophancyScore(data []byte) (SycophancyScore, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var sc SycophancyScore
	if err := dec.Decode(&sc); err != nil {
		return SycophancyScore{}, fmt.Errorf("decode lane sycophancy score: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return SycophancyScore{}, fmt.Errorf("decode lane sycophancy score: trailing JSON")
		}
		return SycophancyScore{}, fmt.Errorf("decode lane sycophancy score trailing JSON: %w", err)
	}
	if err := ValidateSycophancyScore(sc); err != nil {
		return SycophancyScore{}, err
	}
	return sc, nil
}

// RecordSycophancyScore appends one score row to the append-only per-lane sycophancy
// ledger under COMPOUND_HOME. Scores are evidence (they accumulate across measurement
// runs); no CAS/uniqueness gate is imposed because a lane can be re-probed and each
// run is legitimately its own row.
func RecordSycophancyScore(homePath string, sc SycophancyScore) error {
	if strings.TrimSpace(homePath) == "" {
		return fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	if strings.TrimSpace(sc.RecordedVia) == "" {
		sc.RecordedVia = RecorderGeneric
	}
	if err := ValidateSycophancyScore(sc); err != nil {
		return err
	}
	dir := filepath.Join(homePath, "ledger")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	line, err := json.Marshal(&sc)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, laneSycophancyScoreLedger), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

// LoadSycophancyScores reads every recorded score row. Missing ledger is a clean nil.
func LoadSycophancyScores(homePath string) ([]SycophancyScore, error) {
	path := filepath.Join(homePath, "ledger", laneSycophancyScoreLedger)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SycophancyScore
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		var sc SycophancyScore
		if err := json.Unmarshal([]byte(line), &sc); err != nil {
			return nil, fmt.Errorf("parse lane sycophancy score line %d: %w", i+1, err)
		}
		out = append(out, sc)
	}
	return out, nil
}
