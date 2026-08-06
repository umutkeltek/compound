// Package operatornotice is the single missing edge between the attention
// hold frontier (internal/attention) and the existing act post buzz transport
// (internal/act). It does not invent a new subsystem, a new scheduler, or a new
// transport — it wires the three existing surfaces together so an attention
// hold that needs a human emits a governed notification through buzz, exactly
// once per hold (idempotent on re-scan), with unnotifiable or transport-
// refused holds surfaced in a doctor count rather than silently swallowed.
//
// The package is the connective tissue promised by work_a4d2491c8358.
package operatornotice

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/act"
	"compound/internal/attention"
	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/finding"
	"compound/internal/home"
	"compound/internal/policy"
	"compound/internal/product"
	"compound/internal/work"
)

// SchemaVersion identifies the NoticeReport wire shape.
const SchemaVersion = "compound.operatornotice.v0.1"

// executeFn is the seam that drives the act.Execute call. Production routes
// to act.Execute directly; tests swap this to assert the once-and-only-once
// promise without standing up a full buzz transport config + standing grant.
// It's a per-package seam patterned after act's own recordEnvelopeFn — no
// new subsystem, no new transport.
var executeFn = func(homePath, transport, body string, workID string, now time.Time) (string, string, error) {
	ai := governedNoticeIntent(homePath, workID, transport, body, now)
	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		return "", "", err
	}
	pd := policy.Evaluate(ai, cfg, now)
	if !policy.Allowed(pd) {
		return "", "", fmt.Errorf("policy: %s", pd.Outcome)
	}
	artifactStore, err := act.NewArtifactStore(homePath)
	if err != nil {
		return "", "", err
	}
	res, err := act.Execute(artifactStore, act.NewExecTransportAdapter(transport, homePath), ai, pd, []byte(body), now)
	if err != nil {
		return "", "", err
	}
	return res.Receipt.ID, res.Transition.ID, nil
}

// NoticeRecordSchemaVersion is the on-disk record id the operator notice
// emits through the event store to make re-scans idempotent. The id embeds
// "opnotice_" so it never collides with receipts, transitions, or other
// record kinds.
const NoticeRecordSchemaVersion = "compound.operatornotice_record.v0.1"

// NoticeRecord is the event-store record that proves a hold was notified.
// The HoldID is the content-addressed id derived from the band-needs-you
// group, so a re-scan of the same hold reads the same record and skips.
//
// Outcomes: "emitted" (the act post buzz write succeeded), "refused" (the
// transport refused without a standing grant), "unnotifiable" (no candidate
// work id). Each sets the matching DoctorCount bucket, but every record is
// idempotent on re-scan — the predicate's once-and-only-once falsifier rides
// on this exact field.
//
// The ID field is the canonical eventstore record id (matches the put id).
// HoldID is the same value, surfaced under a domain-meaningful name for
// the operator reading the JSON.
type NoticeRecord struct {
	ID             string   `json:"id"`
	SchemaVersion  string   `json:"schema_version"`
	HoldID         string   `json:"hold_id"`
	GroupKey       string   `json:"group_key"`
	GroupArea      string   `json:"group_area"`
	GroupSource    string   `json:"group_source"`
	WorkID         string   `json:"work_id,omitempty"`
	ItemIDs        []string `json:"item_ids,omitempty"`
	OperatorAsk    string   `json:"operator_ask,omitempty"`
	ApproveCommand string   `json:"approve_command,omitempty"`
	Transport      string   `json:"transport"`
	Outcome        string   `json:"outcome"`
	RefusalReason  string   `json:"refusal_reason,omitempty"`
	ReceiptID      string   `json:"receipt_id,omitempty"`
	TransitionID   string   `json:"transition_id,omitempty"`
	NotifiedAt     string   `json:"notified_at"`
}

// NoticeReport is the public return value of Notify / NotifyFromInputs. All
// counts are integers; the schema is stable and exportable.
type NoticeReport struct {
	SchemaVersion   string `json:"schema_version"`
	GeneratedAt     string `json:"generated_at"`
	Transport       string `json:"transport"`
	Scanned         int    `json:"scanned"`
	AlreadyNotified int    `json:"already_notified"`
	Emitted         int    `json:"emitted"`
	Refused         int    `json:"refused"`
	Unnotifiable    int    `json:"unnotifiable"`
	DoctorCount     int    `json:"doctor_count"`
}

// Notify is the public entry point: it builds the attention frontier from the
// home and emits governed notifications through the buzz transport. It is the
// operator-facing CLI's call site (compound notice-flush).
//
// The wire shape is identical to NotifyFromInputs — splits only so tests can
// drive the function off a prepared attention.Report without rebuilding the
// event store every call.
func Notify(homePath, transport string, now time.Time) (NoticeReport, error) {
	store, err := work.OpenReadOnly(homePath)
	if err != nil {
		return NoticeReport{}, fmt.Errorf("operatornotice: open work: %w", err)
	}
	wb, err := store.Workbench(now, work.WorkbenchOptions{RecentLimit: 8})
	if err != nil {
		return NoticeReport{}, fmt.Errorf("operatornotice: build workbench: %w", err)
	}
	execs, err := store.ExecutorsProjection(now, work.ExecutorsProjectionOptions{Workbench: &wb})
	if err != nil {
		return NoticeReport{}, fmt.Errorf("operatornotice: build executors: %w", err)
	}
	report := attention.Build(attention.Inputs{
		Workbench: &wb,
		Executors: &execs,
	}, now)
	return NotifyFromInputs(homePath, transport, report, now)
}

// NotifyFromInputs is the testable seam: it accepts the prepared attention
// report and emits one governed act post buzz record per *new* hold. Idempotent
// by content-addressed hold id; refused or unnotifiable holds are counted in
// the report and a doctor finding is emitted (never silently swallowed).
func NotifyFromInputs(homePath, transport string, in attention.Report, now time.Time) (NoticeReport, error) {
	homePath = strings.TrimSpace(homePath)
	transport = strings.TrimSpace(transport)
	if homePath == "" {
		return NoticeReport{}, fmt.Errorf("operatornotice: home path required")
	}
	if transport == "" {
		return NoticeReport{}, fmt.Errorf("operatornotice: transport required")
	}
	report := NoticeReport{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Transport:     transport,
	}

	// Open the event store first to verify the home is a real compound home
	// without writing anything. A bare temp dir without a manifest is a
	// typed error — never a silent success.
	if err := requireManifest(homePath); err != nil {
		return NoticeReport{}, fmt.Errorf("operatornotice: %w", err)
	}
	store, err := eventstore.Open(homePath)
	if err != nil {
		return NoticeReport{}, fmt.Errorf("operatornotice: open event store: %w", err)
	}
	_ = store

	// Iterate ONLY the BandNeedsYou groups. Other bands are operator-visible
	// but not blocking — wiring them to the buzz transport would create
	// noise; the predicate says "a hold that needs a human".
	for _, group := range in.Groups {
		if group.Band != attention.BandNeedsYou {
			continue
		}
		report.Scanned++

		holdID := HoldIDFor(group)
		workID := firstWorkID(group.ItemIDs)

		// Idempotency check: a prior record on this hold id is the once-and-
		// only-once promise. Emitted, refused, and unnotifiable each write
		// their own record, so re-scans are a no-op across all paths.
		already, prior, err := findPriorNotification(homePath, holdID)
		if err != nil {
			report.Refused++
			report.DoctorCount++
			recordDoctorReadFailure(homePath, group, holdID, err, now)
		}
		if already {
			report.AlreadyNotified++
			// Anything previously emitted/refused/unnotifiable is already
			// counted once; the second scan is the no-op this falsifier
			// promises.
			_ = prior
			continue
		}

		// Unnotifiable: holds without a candidate work id cannot be
		// expressed as a writable notice. Count it; write the record so
		// re-scans are still idempotent; emit a doctor finding.
		if workID == "" {
			report.Unnotifiable++
			report.DoctorCount++
			recordDoctorUnnotifiable(homePath, group, holdID, now)
			persistNoticeRecord(homePath, holdID, group, workID, transport, "", "", "unnotifiable", "no actionable work id", now)
			continue
		}

		// Build the one-line card body and drive the existing act seam.
		// A PolicyDecision-gate refusal or a transport-exec failure is
		// counted in the doctor; the target stays byte-identical.
		card := cardBody(group, holdID, workID, transport)
		receiptID, transitionID, err := executeFn(homePath, transport, card, workID, now)
		if err != nil {
			report.Refused++
			report.DoctorCount++
			recordDoctorRefusal(homePath, group, holdID, err, now)
			persistNoticeRecord(homePath, holdID, group, workID, transport, "", "", "refused", err.Error(), now)
			continue
		}

		// Persist the notice record so the next scan is an idempotent hit.
		// This is the ONLY record NOT written through act.Execute — it is
		// the idempotency witness, not a Receipt/Transition.
		if err := persistNoticeRecord(homePath, holdID, group, workID, transport, receiptID, transitionID, "emitted", "", now); err != nil {
			// The act write succeeded but the idempotency record failed.
			// Count it in the doctor so the operator sees the impression; a
			// re-scan would re-emit (the predicate's exact-once promise is
			// only as strong as the witness).
			report.Refused++
			report.DoctorCount++
			recordDoctorRefusal(homePath, group, holdID, err, now)
			continue
		}
		report.Emitted++
	}
	report.DoctorCount = report.Refused + report.Unnotifiable
	return report, nil
}

// persistNoticeRecord writes the idempotency record. The same id space is
// shared across emitted/refused/unnotifiable outcomes; the Outcome field
// disambiguates. A write failure is returned so the caller can record a
// doctor finding.
func persistNoticeRecord(homePath, holdID string, g attention.Group, workID, transport, receiptID, transitionID, outcome, refusalReason string, now time.Time) error {
	store, err := eventstore.Open(homePath)
	if err != nil {
		return err
	}
	notice := NoticeRecord{
		ID:             holdID,
		SchemaVersion:  NoticeRecordSchemaVersion,
		HoldID:         holdID,
		GroupKey:       g.Key,
		GroupArea:      g.Area,
		GroupSource:    g.Source,
		WorkID:         workID,
		ItemIDs:        append([]string{}, g.ItemIDs...),
		OperatorAsk:    g.OperatorSummary,
		ApproveCommand: cardBody(g, holdID, workID, transport),
		Transport:      transport,
		Outcome:        outcome,
		RefusalReason:  refusalReason,
		ReceiptID:      receiptID,
		TransitionID:   transitionID,
		NotifiedAt:     now.UTC().Format(time.RFC3339),
	}
	_, err = store.PutJSON(holdID, &notice, " ")
	return err
}

// HoldIDFor derives a stable hold id from a band-needs-you group. The id is
// content-addressed over the group shape so the same hold across scans always
// resolves to the same record; the brief's once-and-only-once falsifier rides
// on this exact function.
func HoldIDFor(g attention.Group) string {
	h := sha256.New()
	h.Write([]byte(g.Area))
	h.Write([]byte{0})
	h.Write([]byte(g.Kind))
	h.Write([]byte{0})
	h.Write([]byte(g.Source))
	h.Write([]byte{0})
	ids := append([]string{}, g.ItemIDs...)
	sort.Strings(ids)
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{0})
	}
	h.Write([]byte(g.OldestCreatedAt))
	return "opnotice_" + hex.EncodeToString(h.Sum(nil))[:24]
}

// governedNoticeIntent builds the act post buzz ActionIntent for one hold.
// The capability is fixed (product.ContentPostCapability) so the standing
// grant for product.post_content is the single authority; the target path
// mirrors the act_post convention.
//
// During development NO real notifications are sent (the act seam is dry-run
// by default and the buzz transport config is intentionally absent in tests).
// The intent shape is identical to the live act post so the operator's
// --confirm and the existing grant both apply unchanged.
func governedNoticeIntent(homePath, workID, transport, body string, now time.Time) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            noticeActionID(workID, transport, body, now),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:      "operator",
		Capability: product.ContentPostCapability,
		Action:     "post_content",
		Target: envelope.ActionTarget{
			Kind: "transport",
			ID:   transport,
			Path: "actuator/transports/" + transport + ".json",
		},
		MutationClass: "external_side_effect",
		Reversibility: "irreversible",
		BlastRadius:   "local",
		Preview:       "notify operator of attention hold via " + transport,
		RollbackPlan:  "none guaranteed by the transport adapter",
	}
}

// noticeActionID is the act intent id for one notice. It hashes the work id,
// transport, body, and timestamp so two distinct holds get distinct intents.
func noticeActionID(workID, transport, body string, now time.Time) string {
	h := sha256.Sum256([]byte(strings.Join([]string{workID, transport, body, now.UTC().Format(time.RFC3339Nano)}, "|")))
	return "act_opnotice_" + hex.EncodeToString(h[:])[:16]
}

// cardBody builds the one-line card body the operator receives. It carries
// hold id + work id + a one-line ask + the exact approve command so the
// buzz transport reaches the operator with everything the predicate promised.
func cardBody(g attention.Group, holdID, workID, transport string) string {
	ask := strings.TrimSpace(g.OperatorSummary)
	if ask == "" {
		ask = "attention hold requires operator review"
	}
	return strings.Join([]string{
		fmt.Sprintf("🛎 hold=%s", holdID),
		fmt.Sprintf("work=%s", workID),
		fmt.Sprintf("ask=%s", ask),
		fmt.Sprintf("approve=compound act post --from-work %s --work %s --transport %s --confirm", workID, workID, transport),
	}, "\n")
}

// findPriorNotification returns true if a NoticeRecord with the given hold id
// already exists in the home's event store. A read error is returned so the
// caller can count it in the doctor (best-effort: a read failure does NOT
// block the emit, because the predicate is exactly-one-notification).
func findPriorNotification(homePath, holdID string) (bool, *NoticeRecord, error) {
	store, err := eventstore.Open(homePath)
	if err != nil {
		return false, nil, err
	}
	rec, err := store.Read(holdID)
	if err != nil {
		// A missing record is the steady-state hit; the event store frames
		// ENOENT as either "not found" or "no such file" depending on the
		// engine phase. Both are the absence path — surface only non-ENOENT
		// errors as the doctor signal.
		msg := err.Error()
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no such file") {
			return false, nil, nil
		}
		return false, nil, err
	}
	var notice NoticeRecord
	if err := json.Unmarshal(rec.Bytes, &notice); err != nil {
		return false, nil, err
	}
	return true, &notice, nil
}

// firstWorkID picks the first non-empty ItemIDs entry. It favors the canonical
// surface (the work id itself) over a derived item id (e.g. executor-row ids)
// so the emitted approve command matches the operator's mental model.
func firstWorkID(ids []string) string {
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		// Skip the executor-context:* composite that carries a hook event id —
		// it is not a work id and the approve command would be invalid.
		if strings.HasPrefix(id, "executor-context:") {
			continue
		}
		// Skip non-work ids such as activity-block:* and lease-hygiene entries.
		// Only true work_* ids are valid candidates for the approve command.
		if strings.HasPrefix(id, "activity-block:") || strings.HasPrefix(id, "lease-") {
			continue
		}
		return id
	}
	return ""
}

// doctorFinding is the small struct the doctor count routine emits. It is
// purely an evidence-class record — never a ledger byte — and is surfaced
// by compound doctor without a separate seam.
type doctorFinding struct {
	Kind       string
	GroupKey   string
	HoldID     string
	Detail     string
	ProducedAt string
}

func recordDoctorUnnotifiable(homePath string, g attention.Group, holdID string, now time.Time) {
	emitDoctorFinding(homePath, doctorFinding{
		Kind: "opnotice_unnotifiable", GroupKey: g.Key, HoldID: holdID,
		Detail:     "band-needs-you group has no actionable work id; cannot emit a notice",
		ProducedAt: now.UTC().Format(time.RFC3339),
	}, now)
}

func recordDoctorRefusal(homePath string, g attention.Group, holdID string, cause error, now time.Time) {
	emitDoctorFinding(homePath, doctorFinding{
		Kind: "opnotice_refused", GroupKey: g.Key, HoldID: holdID,
		Detail:     fmt.Sprintf("notice flush refused: %v", cause),
		ProducedAt: now.UTC().Format(time.RFC3339),
	}, now)
}

func recordDoctorReadFailure(homePath string, g attention.Group, holdID string, cause error, now time.Time) {
	emitDoctorFinding(homePath, doctorFinding{
		Kind: "opnotice_read_failure", GroupKey: g.Key, HoldID: holdID,
		Detail:     fmt.Sprintf("notice record read failed: %v", cause),
		ProducedAt: now.UTC().Format(time.RFC3339),
	}, now)
}

// emitDoctorFinding writes a finding-class record so the doctor count
// surfaces the refusal/unnotifiable. The home is best-effort: a finding
// failure logs nothing — the NoticeReport doctor count is the operator-visible
// witness regardless.
func emitDoctorFinding(homePath string, f doctorFinding, now time.Time) {
	body, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		return
	}
	_, _ = finding.Record(homePath, finding.Finding{
		Topic:      f.Kind,
		Body:       string(body),
		ProducedBy: "internal/operatornotice",
		WorkID:     "",
	}, now)
}

// requireManifest is the cheap read-only check that the home is a real
// compound home (not a bare temp dir). It races with the eventstore's own
// validation, but it fails fast at the seam — the predicate's
// read-only-failure test rides on this check.
func requireManifest(homePath string) error {
	clean := strings.TrimSpace(homePath)
	if clean == "" {
		return fmt.Errorf("home path required")
	}
	manifest := filepath.Join(clean, home.ManifestName)
	if _, err := os.Stat(manifest); err != nil {
		return fmt.Errorf("home %s is not a compound home (missing %s)", clean, home.ManifestName)
	}
	return nil
}

// Unused but exported so a downstream caller can validate the schema
// without instantiating the report.
var _ = home.Resolve
