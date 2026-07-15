// Package executorlens is an evidence-only collector that turns THIS repo's own executor lanes'
// (Codex, Claude Code) real execution receipts/events into behavioral-lens Observations, so the
// agents that EXECUTE compound's work are measured for behavior-bending even as they execute.
//
// It owns no authority object. It scans append-only envelope objects through
// internal/eventstore (rec_ receipts, act_ action-intents, tr_ transitions, trace_ imported event
// traces), classifies each to an executor lane subject, and builds two classes of Observation
// that snap into the existing behaviorallens framework — it reimplements NONE of it:
//
//   - PUBLIC_SAFE hedging: over an executor's OUTPUT text (a receipt/trace summary, an action
//     preview) it emits an Observation carrying a behaviorallens.HedgingPayload so the hedging lens
//     scores the output's calibration polarity. Ships in the generic product: it reads OUTPUT text,
//     not the operator's real interactions.
//   - OPERATOR_PRIVATE agreement-asymmetry: from real receipts it assembles, per executor lane, the
//     lane's verdict on each work it touched (the receipt Outcome), whether that verdict matched the
//     operator's stated position for the work (a work is admitted so it should reach `completed`), and
//     the verdicts OTHER executor lanes reached on the SAME work (peer verdicts). This reads real
//     relationship data, so it is operator_private: it runs ONLY when the home Config opts the
//     agreement-asymmetry lens in (behaviorallens.Compose's cfg gate), a stranger clone runs none
//     (fail-closed), and its signal NEVER leaves $COMPOUND_HOME (behaviorallens.PublicProjection
//     drops it).
//
// Invariants (inherited from behaviorallens, enforced here by reuse — never reimplemented):
//   - EVIDENCE ONLY. Signals are recorded; nothing is gated, promoted, or routed.
//   - VISIBILITY BOUNDARY. The outward projection goes through behaviorallens.PublicProjection /
//     PublicSignals, NEVER LoadSignals, so operator_private executor signals stay home-local.
//   - NO PROVIDER/NETWORK CALL in any path — it reads recorded envelope files off disk only.
package executorlens

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"compound/internal/behaviorallens"
	"compound/internal/envelope"
	"compound/internal/eventstore"
)

// Schema versions for this collector's read-model shapes (evidence reports, not persisted authority).
const (
	CollectionSchemaVersion = "compound.executor_lens_collection.v0.1"
	RunReportSchemaVersion  = "compound.executor_lens_run.v0.1"
)

// OperatorCompletedPosition is the operator's stated position a receipt is scored against for the
// agreement axis: a work is admitted/granted BECAUSE the operator wants it to reach `completed`, so a
// lane's verdict "agrees with the operator" when it reports the work completed. A lane that reports
// completed far more often than peers agree with each other on the SAME work is tracking the
// operator's desired outcome, not ground truth — exactly the agreement-asymmetry the private lens
// measures. This is a documented heuristic proxy over the always-present Outcome field: evidence, not
// truth, never a gate.
const OperatorCompletedPosition = "completed"

// executorFamily maps a canonical executor family to the lowercased tokens that identify it in an
// envelope's lane / actor / producer-name fields. Codex is one executor lane; the Claude Code family
// covers the orchestrator/executor identities that run under Claude Code (claude/fable/opus/sonnet/
// haiku). Matching is substring-on-lowercase so a lane tuple like "fable-opus-loc" or "codex-native"
// classifies. The set is deliberately narrow (no bare "gpt"/"llm") so control-plane producers such as
// "os-event" or "compound" are NOT mistaken for executors.
var executorFamilies = []struct {
	family string
	tokens []string
}{
	{family: "codex", tokens: []string{"codex"}},
	{family: "claude-code", tokens: []string{"claude-code", "claude_code", "claude", "fable", "opus", "sonnet", "haiku"}},
}

// classifyExecutor walks identity candidates in priority order (most specific first) and returns the
// FIRST candidate that self-identifies as an executor lane, along with its canonical family. The
// returned subject is therefore always an executor-identifying string (a control-plane lane id that
// happens to be most-specific but carries no executor token is skipped in favour of a later candidate
// that does). ok=false means no candidate identifies an executor — the envelope is not ours to score.
func classifyExecutor(candidates ...string) (subject, family string, ok bool) {
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		lc := strings.ToLower(c)
		for _, fam := range executorFamilies {
			for _, tok := range fam.tokens {
				if strings.Contains(lc, tok) {
					return c, fam.family, true
				}
			}
		}
	}
	return "", "", false
}

// executorView is the per-kind projection of one decoded envelope into (identity candidates, output
// text). Identity candidates are ordered most-specific first (lane_id, then an explicit actor, then
// producer name). Output text is the executor-authored narration the hedging lens scores. Only the
// kinds an executor authors are projected; anything else returns no candidates and is skipped.
func executorView(e envelope.Envelope) (candidates []string, text string) {
	switch v := e.(type) {
	case *envelope.Receipt:
		return []string{v.Producer.LaneID, v.Producer.Name}, strings.TrimSpace(v.StateDelta.Summary)
	case *envelope.TraceEnvelope:
		return []string{v.Producer.LaneID, v.Actor, v.Producer.Name}, strings.TrimSpace(v.Summary)
	case *envelope.ActionIntent:
		return []string{v.Producer.LaneID, v.Actor, v.Producer.Name}, strings.TrimSpace(v.Preview)
	case *envelope.Transition:
		return []string{v.Producer.LaneID, v.Producer.Name}, strings.TrimSpace(v.StateDelta)
	default:
		return nil, ""
	}
}

// executorFilePrefixes are the envelope-object id prefixes this collector scans through eventstore.
// The trace compatibility stream is intentionally not read: recordTrace also writes a trace_<id>
// object, so scanning records covers the same traces without double-counting.
var executorFilePrefixes = []string{"rec_", "act_", "tr_", "trace_"}

// decodeEnvelope probes an events file's schema_version and decodes it into the matching concrete
// envelope. A file whose schema_version is unknown/blank (a policy decision, a work envelope, a
// non-envelope json) yields ok=false and is skipped: this is a resilient evidence reader over a
// heterogeneous events directory, not a validator. It does NOT call Validate — real receipts may be
// partial, and the collector only ever reads fields, never trusts them as authority.
func decodeEnvelope(data []byte) (envelope.Envelope, bool) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil || strings.TrimSpace(probe.SchemaVersion) == "" {
		return nil, false
	}
	e := envelope.New(probe.SchemaVersion)
	if e == nil {
		return nil, false
	}
	if err := json.Unmarshal(data, e); err != nil {
		return nil, false
	}
	return e, true
}

// receiptVerdict is one executor lane's recorded verdict on one work, kept for agreement-asymmetry
// assembly. createdAt lets a re-measured (lane, work) pair collapse to the latest verdict — a lane
// judges an artifact once, which the agreement-asymmetry lens also requires (unique artifact_id).
type receiptVerdict struct {
	subject   string
	outcome   string
	createdAt string
}

// Collection is the evidence this collector materializes from a home's events: hedging Observations
// (public_safe, over executor output text) and agreement-asymmetry Observations (operator_private,
// per executor lane). It is a read-model — building it records nothing and gates nothing.
type Collection struct {
	SchemaVersion string                       `json:"schema_version"`
	Subjects      []string                     `json:"subjects"`
	Hedging       []behaviorallens.Observation `json:"hedging"`
	Agreement     []behaviorallens.Observation `json:"agreement"`
}

// CollectObservations reads a home's executor receipts/events and builds the behavioral-lens
// Observations. It makes NO provider/network call and mutates nothing. A missing events directory is
// a clean empty Collection (a fresh home has nothing to measure yet).
func CollectObservations(homePath string) (Collection, error) {
	col := Collection{SchemaVersion: CollectionSchemaVersion}
	if strings.TrimSpace(homePath) == "" {
		return col, fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	store, err := eventstore.Open(homePath)
	if err != nil {
		return col, err
	}
	entries, err := store.Scan(eventstore.ScanOptions{Prefixes: executorFilePrefixes, RegularOnly: true})
	if err != nil {
		if os.IsNotExist(err) {
			return col, nil
		}
		return col, fmt.Errorf("scan event store: %w", err)
	}

	subjectSeen := map[string]bool{}
	// work_id -> subject -> latest verdict; the peer axis is other subjects on the same work.
	byWork := map[string]map[string]receiptVerdict{}

	for _, entry := range entries {
		record, readErr := entry.Read()
		if readErr != nil {
			return col, fmt.Errorf("read events file %s: %w", entry.Name, readErr)
		}
		e, ok := decodeEnvelope(record.Bytes)
		if !ok {
			continue // not an envelope kind we score; skip resiliently
		}
		candidates, text := executorView(e)
		subject, _, isExec := classifyExecutor(candidates...)
		if !isExec {
			continue // not an executor lane's object
		}
		subjectSeen[subject] = true

		// PUBLIC_SAFE hedging: an Observation over the executor's output text.
		if text != "" {
			payload, mErr := json.Marshal(behaviorallens.HedgingPayload{Text: text})
			if mErr != nil {
				return col, mErr
			}
			col.Hedging = append(col.Hedging, behaviorallens.Observation{
				SchemaVersion: behaviorallens.ObservationSchemaVersion,
				Subject:       subject,
				Kind:          "executor_output",
				Payload:       payload,
				EvidenceRefs:  []string{record.Reference.String()},
				Meta:          map[string]string{"source": entry.Name, "lens": behaviorallens.HedgingLensID},
			})
		}

		// OPERATOR_PRIVATE agreement axis: only receipts carry a verdict (Outcome) on a work.
		rec, isReceipt := e.(*envelope.Receipt)
		if !isReceipt {
			continue
		}
		workID := strings.TrimSpace(rec.IDs.WorkID)
		outcome := strings.TrimSpace(rec.Outcome)
		if workID == "" || outcome == "" {
			continue
		}
		if byWork[workID] == nil {
			byWork[workID] = map[string]receiptVerdict{}
		}
		prev, exists := byWork[workID][subject]
		if !exists || rec.CreatedAt > prev.createdAt {
			byWork[workID][subject] = receiptVerdict{subject: subject, outcome: outcome, createdAt: rec.CreatedAt}
		}
	}

	col.Agreement = buildAgreementObservations(byWork)

	col.Subjects = make([]string, 0, len(subjectSeen))
	for s := range subjectSeen {
		col.Subjects = append(col.Subjects, s)
	}
	sort.Strings(col.Subjects)
	return col, nil
}

// buildAgreementObservations turns the work_id -> subject -> verdict map into one
// AgreementAsymmetryPayload Observation per executor lane. For each work a lane judged: the artifact
// is the work_id, the verdict is the lane's outcome, agrees_with_operator is (outcome == completed),
// and peer_verdicts are the outcomes OTHER lanes reached on the same work. Deterministic ordering
// (works and peers sorted) keeps signal ids stable.
func buildAgreementObservations(byWork map[string]map[string]receiptVerdict) []behaviorallens.Observation {
	// Invert to subject -> []AgreementRecord.
	perSubject := map[string][]behaviorallens.AgreementRecord{}
	workIDs := make([]string, 0, len(byWork))
	for wid := range byWork {
		workIDs = append(workIDs, wid)
	}
	sort.Strings(workIDs)
	for _, wid := range workIDs {
		bySubject := byWork[wid]
		subjects := make([]string, 0, len(bySubject))
		for s := range bySubject {
			subjects = append(subjects, s)
		}
		sort.Strings(subjects)
		for _, subj := range subjects {
			v := bySubject[subj]
			var peers []string
			for _, other := range subjects {
				if other == subj {
					continue
				}
				peers = append(peers, bySubject[other].outcome)
			}
			perSubject[subj] = append(perSubject[subj], behaviorallens.AgreementRecord{
				ArtifactID:         wid,
				AgreesWithOperator: v.outcome == OperatorCompletedPosition,
				Verdict:            v.outcome,
				PeerVerdicts:       peers,
			})
		}
	}
	subjects := make([]string, 0, len(perSubject))
	for s := range perSubject {
		subjects = append(subjects, s)
	}
	sort.Strings(subjects)
	var out []behaviorallens.Observation
	for _, subj := range subjects {
		records := perSubject[subj]
		payload, err := json.Marshal(behaviorallens.AgreementAsymmetryPayload{Records: records})
		if err != nil {
			continue
		}
		out = append(out, behaviorallens.Observation{
			SchemaVersion: behaviorallens.ObservationSchemaVersion,
			Subject:       subj,
			Kind:          "executor_agreement",
			Payload:       payload,
			Meta:          map[string]string{"lens": behaviorallens.AgreementAsymmetryLensID},
		})
	}
	return out
}

// RunReport is the outcome of a collector run: what was seen, what was composed/recorded, and the
// OUTWARD-safe signal projection. PublicSignals is built through behaviorallens.PublicProjection, so
// operator_private executor signals recorded under $COMPOUND_HOME never appear here. Reading this
// field is safe to emit; the full ledger (public + private) stays behind behaviorallens.LoadSignals,
// which this organ never uses on an emitting path.
type RunReport struct {
	SchemaVersion    string                          `json:"schema_version"`
	HomePath         string                          `json:"home_path"`
	Subjects         []string                        `json:"subjects"`
	HedgingObs       int                             `json:"hedging_observations"`
	AgreementObs     int                             `json:"agreement_observations"`
	Composed         int                             `json:"composed_signals"`
	Recorded         int                             `json:"recorded_signals"`
	AgreementEnabled bool                            `json:"agreement_lens_enabled"`
	Skipped          []behaviorallens.SkipNote       `json:"skipped,omitempty"`
	PublicSignals    []behaviorallens.InferredSignal `json:"public_signals"`
}

// Run collects a home's executor observations, composes each through the home registry (which
// visibility-gates operator_private lenses by the home Config — default off, stranger clone
// fail-closed), optionally records every produced signal to the home-local ledger, and returns the
// OUTWARD-safe public projection. It makes NO provider/network call.
//
// The visibility boundary is upheld by construction: RecordSignal persists all produced signals under
// $COMPOUND_HOME (home-local), but RunReport.PublicSignals is the ONLY outward surface and is filtered
// through behaviorallens.PublicProjection. Swapping that projection for the raw signal slice (or for
// LoadSignals) is exactly the leak the boundary falsifier catches.
func Run(homePath string, record bool, now time.Time) (RunReport, error) {
	report := RunReport{SchemaVersion: RunReportSchemaVersion, HomePath: homePath}
	cfg, err := behaviorallens.LoadConfig(homePath)
	if err != nil {
		return report, err
	}
	for _, id := range cfg.EnabledPrivateLenses {
		if strings.TrimSpace(id) == behaviorallens.AgreementAsymmetryLensID {
			report.AgreementEnabled = true
		}
	}
	col, err := CollectObservations(homePath)
	if err != nil {
		return report, err
	}
	report.Subjects = col.Subjects
	report.HedgingObs = len(col.Hedging)
	report.AgreementObs = len(col.Agreement)

	reg := behaviorallens.RegistryForHome(cfg)
	var produced []behaviorallens.InferredSignal
	observations := append(append([]behaviorallens.Observation{}, col.Hedging...), col.Agreement...)
	for _, obs := range observations {
		res, cErr := reg.Compose(context.Background(), obs, cfg, now)
		if cErr != nil {
			return report, cErr
		}
		produced = append(produced, res.Signals...)
		report.Skipped = append(report.Skipped, res.Skipped...)
	}
	report.Composed = len(produced)

	if record {
		for _, sig := range produced {
			if rErr := behaviorallens.RecordSignal(homePath, sig); rErr != nil {
				return report, rErr
			}
			report.Recorded++
		}
	}

	// THE visibility gate on the way out: only the public_safe projection leaves the home.
	report.PublicSignals = behaviorallens.PublicProjection(produced)
	return report, nil
}
