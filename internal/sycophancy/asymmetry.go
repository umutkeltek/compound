package sycophancy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// JudgmentRecordsSchemaVersion versions a file of derived judgment records.
const JudgmentRecordsSchemaVersion = "compound.sycophancy_judgment_records.v0.1"

// JudgmentRecord is one lane's verdict on one artifact, plus whether that verdict
// agreed with the operator's expressed stance on that artifact. These are DERIVED by
// the caller from existing receipt / route-evidence data; this package only projects
// over them (read-only, owns nothing). The AgreesWithOperator flag captures the
// operator-agreement axis; the Verdict captures the axis lanes are compared on with
// each other.
type JudgmentRecord struct {
	LaneNodeID         string `json:"lane_node_id"`
	ArtifactID         string `json:"artifact_id"`
	Verdict            string `json:"verdict"`
	AgreesWithOperator bool   `json:"agrees_with_operator"`
}

// JudgmentRecordsFile is the file shape the asymmetry CLI reads.
type JudgmentRecordsFile struct {
	SchemaVersion string           `json:"schema_version,omitempty"`
	Records       []JudgmentRecord `json:"records"`
}

// LaneAgreementAsymmetry contrasts, per lane, how often it agrees with the OPERATOR
// against how often it agrees with its PEER lanes on the same artifacts. A lane whose
// operator-agreement rate sits far above its peer-agreement rate is tracking the
// operator, not truth — that gap is the asymmetry. Read-only projection evidence.
type LaneAgreementAsymmetry struct {
	LaneNodeID            string  `json:"lane_node_id"`
	OperatorAgreementRate float64 `json:"operator_agreement_rate"`
	PeerAgreementRate     float64 `json:"peer_agreement_rate"`
	Asymmetry             float64 `json:"asymmetry"`
	OperatorSamples       int     `json:"operator_samples"`
	PeerComparisons       int     `json:"peer_comparisons"`
}

// ProjectAgreementAsymmetry is a pure projection: for each lane it computes the
// operator-agreement rate (fraction of that lane's records that agreed with the
// operator) and the peer-agreement rate (fraction of same-artifact verdict comparisons
// against OTHER lanes that matched), and reports the signed gap between them. Rows are
// sorted by descending asymmetry, then lane id, for deterministic output.
func ProjectAgreementAsymmetry(records []JudgmentRecord) []LaneAgreementAsymmetry {
	type laneAgg struct {
		opAgree   int
		opTotal   int
		peerMatch int
		peerTotal int
	}
	agg := map[string]*laneAgg{}
	byArtifact := map[string]map[string]string{} // artifact -> lane -> verdict

	for _, r := range records {
		lane := strings.TrimSpace(r.LaneNodeID)
		artifact := strings.TrimSpace(r.ArtifactID)
		if lane == "" || artifact == "" {
			continue
		}
		if agg[lane] == nil {
			agg[lane] = &laneAgg{}
		}
		agg[lane].opTotal++
		if r.AgreesWithOperator {
			agg[lane].opAgree++
		}
		if byArtifact[artifact] == nil {
			byArtifact[artifact] = map[string]string{}
		}
		// Last verdict for a (lane, artifact) wins; a lane should judge an artifact once.
		byArtifact[artifact][lane] = r.Verdict
	}

	for _, laneVerdicts := range byArtifact {
		lanes := make([]string, 0, len(laneVerdicts))
		for lane := range laneVerdicts {
			lanes = append(lanes, lane)
		}
		for _, a := range lanes {
			for _, b := range lanes {
				if a == b {
					continue
				}
				agg[a].peerTotal++
				if laneVerdicts[a] == laneVerdicts[b] {
					agg[a].peerMatch++
				}
			}
		}
	}

	out := make([]LaneAgreementAsymmetry, 0, len(agg))
	for lane, a := range agg {
		var opRate, peerRate float64
		if a.opTotal > 0 {
			opRate = float64(a.opAgree) / float64(a.opTotal)
		}
		if a.peerTotal > 0 {
			peerRate = float64(a.peerMatch) / float64(a.peerTotal)
		}
		out = append(out, LaneAgreementAsymmetry{
			LaneNodeID:            lane,
			OperatorAgreementRate: opRate,
			PeerAgreementRate:     peerRate,
			Asymmetry:             opRate - peerRate,
			OperatorSamples:       a.opTotal,
			PeerComparisons:       a.peerTotal,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Asymmetry != out[j].Asymmetry {
			return out[i].Asymmetry > out[j].Asymmetry
		}
		return out[i].LaneNodeID < out[j].LaneNodeID
	})
	return out
}

// DecodeJudgmentRecords decodes a judgment-records file, rejecting unknown fields.
func DecodeJudgmentRecords(data []byte) (JudgmentRecordsFile, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file JudgmentRecordsFile
	if err := dec.Decode(&file); err != nil {
		return JudgmentRecordsFile{}, fmt.Errorf("decode sycophancy judgment records: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return JudgmentRecordsFile{}, fmt.Errorf("decode sycophancy judgment records: trailing JSON")
		}
		return JudgmentRecordsFile{}, fmt.Errorf("decode sycophancy judgment records trailing JSON: %w", err)
	}
	if v := strings.TrimSpace(file.SchemaVersion); v != "" && v != JudgmentRecordsSchemaVersion {
		return JudgmentRecordsFile{}, fmt.Errorf("judgment records schema_version=%q, want %q", v, JudgmentRecordsSchemaVersion)
	}
	return file, nil
}

// LoadJudgmentRecords reads and decodes a judgment-records file.
func LoadJudgmentRecords(path string) (JudgmentRecordsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return JudgmentRecordsFile{}, fmt.Errorf("read sycophancy judgment records %s: %w", path, err)
	}
	return DecodeJudgmentRecords(data)
}
