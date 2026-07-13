package sycophancy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"compound/internal/capgraph"
)

const (
	// TranscriptSchemaVersion versions one lane's recorded probe transcript.
	TranscriptSchemaVersion = "compound.sycophancy_probe_transcript.v0.1"
	// TranscriptBundleSchemaVersion versions a file carrying many lanes' transcripts.
	TranscriptBundleSchemaVersion = "compound.sycophancy_probe_transcript_bundle.v0.1"
)

// ProbeTranscript is a recorded run of a REAL lane against the probe set: the lane's
// supportive- and critical-framed endorsements per case, captured out of band (with
// whatever provider calls the real lane made) and handed to this harness as data.
//
// This is the orchestrator's real-lane door — the post-land act named in the slice
// scope. The code path that scores a real lane reads numbers from a file and makes NO
// provider call itself, so a real lane can be measured after this slice lands without
// widening the tested code paths.
type ProbeTranscript struct {
	SchemaVersion string           `json:"schema_version,omitempty"`
	LaneNodeID    string           `json:"lane_node_id,omitempty"`
	ShortLaneID   string           `json:"short_lane_id,omitempty"`
	Cases         []TranscriptCase `json:"cases"`
}

// TranscriptCase is one artifact's recorded pair of framed endorsements.
type TranscriptCase struct {
	ArtifactID            string  `json:"artifact_id"`
	SupportiveEndorsement float64 `json:"supportive_endorsement"`
	CriticalEndorsement   float64 `json:"critical_endorsement"`
}

// TranscriptBundle carries transcripts for many lanes so a single probe run over all
// lanes is one file.
type TranscriptBundle struct {
	SchemaVersion string            `json:"schema_version,omitempty"`
	Transcripts   []ProbeTranscript `json:"transcripts"`
}

// ResolveLaneNodeID returns the canonical capgraph agent-lane node id for a transcript.
// A transcript may carry the canonical node id directly, or only a short lane id
// (harness/route/model), in which case it is resolved through the same lane-tuple
// identity every other lane-scoped record uses.
func (t ProbeTranscript) ResolveLaneNodeID() (string, error) {
	if id := strings.TrimSpace(t.LaneNodeID); id != "" {
		return id, nil
	}
	short := strings.TrimSpace(t.ShortLaneID)
	if short == "" {
		return "", fmt.Errorf("transcript requires lane_node_id or short_lane_id")
	}
	nodeID, ok := capgraph.AgentLaneNodeIDFromShortLaneID(short)
	if !ok {
		return "", fmt.Errorf("cannot resolve lane node id from short lane id %q", short)
	}
	return nodeID, nil
}

// ScoreTranscript computes a lane's sycophancy measurement from its recorded transcript.
// It replays the recorded endorsements through the exact same paired-stance harness a
// synthetic judge flows through, so the real-lane path and the synthetic path compute
// identically — the only difference is where the endorsements come from.
func ScoreTranscript(t ProbeTranscript) (LaneProbeResult, error) {
	nodeID, err := t.ResolveLaneNodeID()
	if err != nil {
		return LaneProbeResult{}, err
	}
	if len(t.Cases) == 0 {
		return LaneProbeResult{}, fmt.Errorf("transcript for lane %q has no cases", nodeID)
	}
	supportive := map[string]float64{}
	critical := map[string]float64{}
	cases := make([]ProbeCase, 0, len(t.Cases))
	for _, c := range t.Cases {
		id := strings.TrimSpace(c.ArtifactID)
		if id == "" {
			return LaneProbeResult{}, fmt.Errorf("transcript for lane %q has a case missing artifact_id", nodeID)
		}
		if _, dup := supportive[id]; dup {
			return LaneProbeResult{}, fmt.Errorf("transcript for lane %q repeats artifact_id %q", nodeID, id)
		}
		supportive[id] = c.SupportiveEndorsement
		critical[id] = c.CriticalEndorsement
		cases = append(cases, ProbeCase{ArtifactID: id})
	}
	replay := func(req JudgeRequest) JudgeVerdict {
		if req.OperatorStance == StanceCritical {
			return JudgeVerdict{Endorsement: critical[req.ArtifactID]}
		}
		return JudgeVerdict{Endorsement: supportive[req.ArtifactID]}
	}
	return RunPairedStanceProbes(nodeID, strings.TrimSpace(t.ShortLaneID), replay, cases)
}

// DecodeTranscriptBundle decodes a probe transcript bundle, rejecting unknown fields so
// a malformed file fails loudly rather than silently scoring partial data.
func DecodeTranscriptBundle(data []byte) (TranscriptBundle, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var bundle TranscriptBundle
	if err := dec.Decode(&bundle); err != nil {
		return TranscriptBundle{}, fmt.Errorf("decode sycophancy probe transcript bundle: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return TranscriptBundle{}, fmt.Errorf("decode sycophancy probe transcript bundle: trailing JSON")
		}
		return TranscriptBundle{}, fmt.Errorf("decode sycophancy probe transcript bundle trailing JSON: %w", err)
	}
	if v := strings.TrimSpace(bundle.SchemaVersion); v != "" && v != TranscriptBundleSchemaVersion {
		return TranscriptBundle{}, fmt.Errorf("transcript bundle schema_version=%q, want %q", v, TranscriptBundleSchemaVersion)
	}
	if len(bundle.Transcripts) == 0 {
		return TranscriptBundle{}, fmt.Errorf("transcript bundle has no transcripts")
	}
	return bundle, nil
}

// LoadTranscriptBundle reads and decodes a transcript bundle file.
func LoadTranscriptBundle(path string) (TranscriptBundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TranscriptBundle{}, fmt.Errorf("read sycophancy probe transcript bundle %s: %w", path, err)
	}
	return DecodeTranscriptBundle(data)
}
