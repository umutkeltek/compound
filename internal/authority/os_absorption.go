package authority

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/capgraph"
	"compound/internal/decisions"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/policy"
	"compound/internal/work"
)

const (
	OSAbsorptionMatrixSchema = "compound.os_absorption_source_matrix.v0.1"
	OSAbsorptionAuthorityRef = "compound://registries/os-absorption-source-matrix"

	osAbsorptionMatrixFile = "os-absorption-source-matrix.json"

	// osAbsorptionDecisionsLedgerRef is the compound_owner ref used by the
	// os_source_decisions_repo absorption row. It is proven by the decisions
	// source-authority ledger (compound/internal/decisions), not asserted.
	osAbsorptionDecisionsLedgerRef = "compound://ledger/decisions"

	// proof_mode values a matrix row can set explicitly to opt into an
	// auto-proof mechanism regardless of its compound_owner string.
	osAbsorptionProofModeCapability    = "capability_source_authority"
	osAbsorptionProofModeDecisions     = "decisions_source_authority"
	osAbsorptionProofModeOperatorState = "operator_state_registry"
	osAbsorptionProofModeWorkPacket    = "work_packet_authority"

	osAbsorptionWorkAuthorityRef  = "compound://work"
	osAbsorptionPacketEvidenceRef = "packet_sha256:"
)

type OSAbsorptionSourceMatrix struct {
	SchemaVersion        string                  `json:"schema_version"`
	GeneratedAt          string                  `json:"generated_at"`
	AuthorityRef         string                  `json:"authority_ref"`
	SourceMatrixPath     string                  `json:"source_matrix_path,omitempty"`
	SourceMatrixRevision string                  `json:"source_matrix_revision,omitempty"`
	Rows                 []OSAbsorptionMatrixRow `json:"rows"`
}

type OSAbsorptionMatrixRow struct {
	ID             string   `json:"id"`
	SourceRef      string   `json:"source_ref"`
	SourceKind     string   `json:"source_kind,omitempty"`
	SourceRevision string   `json:"source_revision,omitempty"`
	OldOwner       string   `json:"old_owner"`
	OldRole        string   `json:"old_role"`
	CurrentRole    string   `json:"current_role"`
	Privacy        string   `json:"privacy"`
	CompoundOwner  string   `json:"compound_owner"`
	Consumer       string   `json:"consumer"`
	Proof          string   `json:"proof"`
	ProofMode      string   `json:"proof_mode,omitempty"`
	RetirementGate string   `json:"retirement_gate"`
	Status         string   `json:"status"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
}

type OSAbsorptionImportReport struct {
	AuthorityRef         string `json:"authority_ref"`
	AuthorityPath        string `json:"authority_path"`
	SourceMatrixPath     string `json:"source_matrix_path"`
	SourceMatrixRevision string `json:"source_matrix_revision"`
	RowCount             int    `json:"row_count"`
	AbsorbedCount        int    `json:"absorbed_count"`
	PendingCount         int    `json:"pending_count"`
}

type OSAbsorptionDoctorReport struct {
	SchemaVersion        string `json:"schema_version"`
	AuthorityRef         string `json:"authority_ref"`
	AuthorityPath        string `json:"authority_path"`
	Status               string `json:"status"`
	Installed            bool   `json:"installed"`
	RowCount             int    `json:"row_count"`
	AbsorbedCount        int    `json:"absorbed_count"`
	PendingCount         int    `json:"pending_count"`
	AutoProofCount       int    `json:"auto_proof_count"`
	PendingWithGateCount int    `json:"pending_with_gate_count"`
	MissingRequiredCount int    `json:"missing_required_count"`
	FailedAutoProofCount int    `json:"failed_auto_proof_count"`
	// MachineProvenCount/AssertedCount/FailedProofCount split the misleading
	// single AbsorbedCount bucket (status-string based) into what proof_status
	// actually says (evidence based): a row can carry status=absorbed and
	// still be proof_status=manual. PendingCount is unchanged/existing.
	MachineProvenCount int `json:"machine_proven_count"`
	AssertedCount      int `json:"asserted_count"`
	FailedProofCount   int `json:"failed_proof_count"`

	Checks                 []OSAbsorptionDoctorCheck        `json:"checks"`
	Rows                   []OSAbsorptionDoctorRow          `json:"rows,omitempty"`
	SourceAuthorityRecords []capgraph.SourceAuthorityRecord `json:"-"`
}

type OSAbsorptionDoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type OSAbsorptionDoctorRow struct {
	ID            string   `json:"id"`
	Status        string   `json:"status"`
	SourceRef     string   `json:"source_ref"`
	CompoundOwner string   `json:"compound_owner"`
	ProofStatus   string   `json:"proof_status"`
	ProofRef      string   `json:"proof_ref,omitempty"`
	ProofDetail   string   `json:"proof_detail,omitempty"`
	MissingFields []string `json:"missing_fields,omitempty"`
}

func ImportOSAbsorptionMatrix(homePath, sourcePath string, now time.Time) (OSAbsorptionImportReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return OSAbsorptionImportReport{}, fmt.Errorf("COMPOUND_HOME required")
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return OSAbsorptionImportReport{}, fmt.Errorf("os absorption matrix source path required")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return OSAbsorptionImportReport{}, fmt.Errorf("read os absorption matrix %s: %w", sourcePath, err)
	}
	matrix, err := parseOSAbsorptionMatrix(data)
	if err != nil {
		return OSAbsorptionImportReport{}, fmt.Errorf("parse os absorption matrix %s: %w", sourcePath, err)
	}
	if now.IsZero() {
		now = time.Now()
	}
	matrix.GeneratedAt = firstNonEmpty(matrix.GeneratedAt, now.UTC().Format(time.RFC3339))
	matrix.AuthorityRef = firstNonEmpty(matrix.AuthorityRef, OSAbsorptionAuthorityRef)
	matrix.SourceMatrixPath = sourcePath
	matrix.SourceMatrixRevision = "sha256:" + digestBytes(data)
	normalizeOSAbsorptionRows(&matrix)
	if err := validateOSAbsorptionMatrix(matrix); err != nil {
		return OSAbsorptionImportReport{}, err
	}
	path := OSAbsorptionMatrixPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return OSAbsorptionImportReport{}, err
	}
	body, err := json.MarshalIndent(matrix, "", "  ")
	if err != nil {
		return OSAbsorptionImportReport{}, err
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return OSAbsorptionImportReport{}, err
	}
	absorbed, pending := osAbsorptionCounts(matrix.Rows)
	return OSAbsorptionImportReport{
		AuthorityRef:         matrix.AuthorityRef,
		AuthorityPath:        path,
		SourceMatrixPath:     matrix.SourceMatrixPath,
		SourceMatrixRevision: matrix.SourceMatrixRevision,
		RowCount:             len(matrix.Rows),
		AbsorbedCount:        absorbed,
		PendingCount:         pending,
	}, nil
}

func LoadOSAbsorptionMatrix(homePath string) (OSAbsorptionSourceMatrix, error) {
	data, err := os.ReadFile(OSAbsorptionMatrixPath(homePath))
	if err != nil {
		return OSAbsorptionSourceMatrix{}, err
	}
	matrix, err := parseOSAbsorptionMatrix(data)
	if err != nil {
		return OSAbsorptionSourceMatrix{}, err
	}
	normalizeOSAbsorptionRows(&matrix)
	return matrix, nil
}

func OSAbsorptionDoctor(homePath string) (OSAbsorptionDoctorReport, error) {
	report := OSAbsorptionDoctorReport{
		SchemaVersion: OSAbsorptionMatrixSchema,
		AuthorityRef:  OSAbsorptionAuthorityRef,
		AuthorityPath: OSAbsorptionMatrixPath(homePath),
		Status:        "fail",
	}
	matrix, err := LoadOSAbsorptionMatrix(homePath)
	if err != nil {
		if os.IsNotExist(err) {
			report.Checks = append(report.Checks, OSAbsorptionDoctorCheck{Name: "installed", Status: "fail", Detail: "matrix not imported"})
			return report, nil
		}
		return report, err
	}
	report.Installed = true
	report.AuthorityRef = firstNonEmpty(matrix.AuthorityRef, OSAbsorptionAuthorityRef)
	absorbed, pending := osAbsorptionCounts(matrix.Rows)
	report.RowCount = len(matrix.Rows)
	report.AbsorbedCount = absorbed
	report.PendingCount = pending
	capRecords, err := capgraph.LoadSourceAuthority(homePath)
	if err != nil {
		return report, err
	}
	for _, row := range matrix.Rows {
		dr := OSAbsorptionDoctorRow{
			ID:            row.ID,
			Status:        row.Status,
			SourceRef:     row.SourceRef,
			CompoundOwner: row.CompoundOwner,
			ProofStatus:   "manual",
		}
		dr.MissingFields = missingOSAbsorptionFields(row)
		if len(dr.MissingFields) > 0 {
			report.MissingRequiredCount++
			dr.ProofStatus = "missing_required"
		}
		if row.Status == "pending" && row.CompoundOwner != "" && row.Consumer != "" && row.RetirementGate != "" {
			report.PendingWithGateCount++
		}
		switch {
		case osAbsorptionNeedsWorkPacketProof(row):
			applyOSAbsorptionAutoProof(&report, &dr, osAbsorptionWorkPacketProof(homePath, row))
		case osAbsorptionNeedsCapabilityProof(row):
			if rec, ok := capabilityAuthorityProofRecord(row, capRecords); ok {
				applyOSAbsorptionAutoProof(&report, &dr, osAbsorptionProofResult{ok: true, proofRef: rec.ID})
			} else {
				applyOSAbsorptionAutoProof(&report, &dr, osAbsorptionProofResult{reason: "no matching capability source-authority record for " + row.SourceRef})
			}
		case osAbsorptionNeedsDecisionsProof(row):
			applyOSAbsorptionAutoProof(&report, &dr, osAbsorptionDecisionsProof(homePath))
		case osAbsorptionNeedsOperatorStateProof(row):
			applyOSAbsorptionAutoProof(&report, &dr, osAbsorptionOperatorStateProof(homePath))
		case row.Status == "pending":
			dr.ProofStatus = "pending"
		}
		switch dr.ProofStatus {
		case "ok":
			report.MachineProvenCount++
		case "manual":
			report.AssertedCount++
		case "failed":
			report.FailedProofCount++
		}
		report.Rows = append(report.Rows, dr)
	}
	report.Checks = append(report.Checks,
		doctorCheck("installed", report.Installed, fmt.Sprintf("%s rows=%d", report.AuthorityPath, report.RowCount)),
		doctorCheck("required_fields", report.MissingRequiredCount == 0, fmt.Sprintf("missing=%d", report.MissingRequiredCount)),
		doctorCheck("capability_auto_proofs", report.AutoProofCount >= 3 && report.FailedAutoProofCount == 0, fmt.Sprintf("auto=%d failed=%d", report.AutoProofCount, report.FailedAutoProofCount)),
		doctorCheck("pending_retirement_gates", report.PendingWithGateCount >= 4, fmt.Sprintf("pending_with_gate=%d", report.PendingWithGateCount)),
	)
	report.Status = "ok"
	for _, check := range report.Checks {
		if check.Status != "ok" {
			report.Status = "fail"
			break
		}
	}
	return report, nil
}

func OSAbsorptionMatrixPath(homePath string) string {
	return filepath.Join(homePath, "registries", osAbsorptionMatrixFile)
}

func parseOSAbsorptionMatrix(data []byte) (OSAbsorptionSourceMatrix, error) {
	var matrix OSAbsorptionSourceMatrix
	if err := json.Unmarshal(data, &matrix); err != nil {
		return matrix, err
	}
	if strings.TrimSpace(matrix.SchemaVersion) == "" {
		matrix.SchemaVersion = OSAbsorptionMatrixSchema
	}
	if matrix.SchemaVersion != OSAbsorptionMatrixSchema {
		return matrix, fmt.Errorf("unsupported schema_version %q", matrix.SchemaVersion)
	}
	return matrix, nil
}

func normalizeOSAbsorptionRows(matrix *OSAbsorptionSourceMatrix) {
	matrix.AuthorityRef = firstNonEmpty(matrix.AuthorityRef, OSAbsorptionAuthorityRef)
	for i := range matrix.Rows {
		row := &matrix.Rows[i]
		row.ID = strings.TrimSpace(row.ID)
		row.SourceRef = strings.TrimSpace(row.SourceRef)
		row.SourceKind = strings.TrimSpace(row.SourceKind)
		row.SourceRevision = strings.TrimSpace(row.SourceRevision)
		row.OldOwner = strings.TrimSpace(row.OldOwner)
		row.OldRole = strings.TrimSpace(row.OldRole)
		row.CurrentRole = strings.TrimSpace(row.CurrentRole)
		row.Privacy = firstNonEmpty(row.Privacy, "private")
		row.CompoundOwner = strings.TrimSpace(row.CompoundOwner)
		row.Consumer = strings.TrimSpace(row.Consumer)
		row.Proof = strings.TrimSpace(row.Proof)
		row.ProofMode = strings.TrimSpace(row.ProofMode)
		row.RetirementGate = strings.TrimSpace(row.RetirementGate)
		row.Status = firstNonEmpty(row.Status, "pending")
		row.EvidenceRefs = cleanStrings(row.EvidenceRefs)
	}
	sort.SliceStable(matrix.Rows, func(i, j int) bool { return matrix.Rows[i].ID < matrix.Rows[j].ID })
}

func validateOSAbsorptionMatrix(matrix OSAbsorptionSourceMatrix) error {
	if len(matrix.Rows) == 0 {
		return fmt.Errorf("os absorption matrix requires rows")
	}
	seen := map[string]bool{}
	for _, row := range matrix.Rows {
		if row.ID == "" {
			return fmt.Errorf("os absorption matrix row missing id")
		}
		if seen[row.ID] {
			return fmt.Errorf("duplicate os absorption row id %q", row.ID)
		}
		seen[row.ID] = true
	}
	return nil
}

func osAbsorptionCounts(rows []OSAbsorptionMatrixRow) (absorbed int, pending int) {
	for _, row := range rows {
		switch row.Status {
		case "absorbed", "wrapper", "evidence_only", "retired":
			absorbed++
		case "pending":
			pending++
		}
	}
	return absorbed, pending
}

func capabilityAuthorityProofRecord(row OSAbsorptionMatrixRow, records []capgraph.SourceAuthorityRecord) (capgraph.SourceAuthorityRecord, bool) {
	for _, rec := range records {
		if rec.AuthorityRef != capgraph.CapabilityGraphAuthorityRef {
			continue
		}
		if strings.TrimSpace(rec.SourcePath) != row.SourceRef {
			continue
		}
		if row.SourceRevision != "" && strings.TrimSpace(rec.SourceRevision) != row.SourceRevision {
			continue
		}
		return rec, true
	}
	return capgraph.SourceAuthorityRecord{}, false
}

func missingOSAbsorptionFields(row OSAbsorptionMatrixRow) []string {
	var missing []string
	required := map[string]string{
		"id":              row.ID,
		"source_ref":      row.SourceRef,
		"old_owner":       row.OldOwner,
		"old_role":        row.OldRole,
		"current_role":    row.CurrentRole,
		"privacy":         row.Privacy,
		"compound_owner":  row.CompoundOwner,
		"consumer":        row.Consumer,
		"proof":           row.Proof,
		"retirement_gate": row.RetirementGate,
		"status":          row.Status,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	return missing
}

func osAbsorptionNeedsCapabilityProof(row OSAbsorptionMatrixRow) bool {
	if row.ProofMode == osAbsorptionProofModeCapability {
		return true
	}
	return row.CompoundOwner == capgraph.CapabilityGraphAuthorityRef && row.Status == "absorbed"
}

// osAbsorptionNeedsWorkPacketProof matches the OS packet directory wrapper.
// An explicit proof_mode wins over tuple inference so a row cannot silently
// receive a different proof mechanism than the source matrix requested.
func osAbsorptionNeedsWorkPacketProof(row OSAbsorptionMatrixRow) bool {
	if row.ProofMode != "" {
		return row.ProofMode == osAbsorptionProofModeWorkPacket
	}
	return row.CompoundOwner == osAbsorptionWorkAuthorityRef && row.SourceKind == "packet_dir" && row.Status == "wrapper"
}

// osAbsorptionNeedsDecisionsProof matches the os_source_decisions_repo row (and
// any row that opts in via proof_mode): it is proven by the decisions
// source-authority ledger having at least one record, not by the row's status
// string (today "evidence_only").
func osAbsorptionNeedsDecisionsProof(row OSAbsorptionMatrixRow) bool {
	if row.ProofMode == osAbsorptionProofModeDecisions {
		return true
	}
	return row.CompoundOwner == osAbsorptionDecisionsLedgerRef
}

// osAbsorptionNeedsOperatorStateProof matches the os_source_operator_state row
// (and any row that opts in via proof_mode): it is proven by the same
// operator-state registry reader internal/policy uses to apply autonomy state
// and cite compound://registries/operator-state on policy decisions.
func osAbsorptionNeedsOperatorStateProof(row OSAbsorptionMatrixRow) bool {
	if row.ProofMode == osAbsorptionProofModeOperatorState {
		return true
	}
	return row.CompoundOwner == policy.OperatorStateAuthorityRef
}

// osAbsorptionProofResult is the outcome of one row's auto-proof attempt.
type osAbsorptionProofResult struct {
	ok       bool
	proofRef string
	reason   string
}

// osAbsorptionDecisionsProof is the auto-proof for os_source_decisions_repo:
// the decisions source-authority ledger (compound/internal/decisions) loads
// and has at least one record. Falsifier: an absent/empty ledger fails, it
// does not silently pass.
func osAbsorptionDecisionsProof(homePath string) osAbsorptionProofResult {
	records, err := decisions.LoadSourceAuthority(homePath)
	if err != nil {
		return osAbsorptionProofResult{reason: "decisions source authority: " + err.Error()}
	}
	if len(records) == 0 {
		return osAbsorptionProofResult{reason: "decisions source authority ledger has record_count=0"}
	}
	return osAbsorptionProofResult{ok: true, proofRef: osAbsorptionDecisionsLedgerRef}
}

// osAbsorptionOperatorStateProof is the auto-proof for os_source_operator_state:
// the operator-state registry (compound/internal/policy's own reader) loads
// and is non-empty. That reader is what feeds ApplyOperatorState, which is
// what makes policy decisions cite compound://registries/operator-state — so
// "loads + non-empty" is the weakest real check that still proves the live
// consumer path, without inventing new state.
func osAbsorptionOperatorStateProof(homePath string) osAbsorptionProofResult {
	path := policy.OperatorStatePath(homePath)
	state, err := policy.LoadOperatorState(path)
	if err != nil {
		if os.IsNotExist(err) {
			return osAbsorptionProofResult{reason: "operator-state registry not found at " + path}
		}
		return osAbsorptionProofResult{reason: "operator-state registry: " + err.Error()}
	}
	if len(state.Raw) == 0 {
		return osAbsorptionProofResult{reason: "operator-state registry at " + path + " is empty"}
	}
	return osAbsorptionProofResult{ok: true, proofRef: policy.OperatorStateAuthorityRef}
}

// osAbsorptionWorkPacketProof proves both halves of packet absorption:
// every explicitly cited WorkEnvelope is a completed packet-backed chain, and
// at least one current packet beneath source_ref is content-bound to the full
// WorkEnvelope -> ContextPack -> Receipt -> Transition consumer path. The
// second condition prevents unrelated packet imports from proving a directory.
func osAbsorptionWorkPacketProof(homePath string, row OSAbsorptionMatrixRow) osAbsorptionProofResult {
	var result osAbsorptionProofResult
	err := home.WithAutonomyAdmissionLock(homePath, func() error {
		result = osAbsorptionWorkPacketProofLocked(homePath, row)
		return nil
	})
	if err != nil {
		return osAbsorptionProofResult{reason: "work packet authority snapshot: " + err.Error()}
	}
	return result
}

func osAbsorptionWorkPacketProofLocked(homePath string, row OSAbsorptionMatrixRow) osAbsorptionProofResult {
	workIDs, err := osAbsorptionWorkEvidenceIDs(row.EvidenceRefs)
	if err != nil {
		return osAbsorptionProofResult{reason: err.Error()}
	}
	if len(workIDs) == 0 {
		return osAbsorptionProofResult{reason: "work packet authority requires at least one work_* evidence_ref"}
	}
	store, err := work.Open(homePath)
	if err != nil {
		return osAbsorptionProofResult{reason: "work packet authority: " + err.Error()}
	}
	chainReport, err := store.ProofChainDoctor(time.Now(), work.ProofChainDoctorOptions{IncludeArchived: true})
	if err != nil {
		return osAbsorptionProofResult{reason: "work packet authority proof chain: " + err.Error()}
	}
	proofItems := make(map[string]work.ProofChainDoctorItem, len(chainReport.Items))
	for _, item := range chainReport.Items {
		proofItems[item.WorkID] = item
	}
	for _, workID := range workIDs {
		w, err := store.Get(workID)
		if err != nil {
			return osAbsorptionProofResult{reason: fmt.Sprintf("work packet authority %s: %v", workID, err)}
		}
		if err := validateOSAbsorptionPacketWork(homePath, w, workID, proofItems); err != nil {
			return osAbsorptionProofResult{reason: fmt.Sprintf("work packet authority %s: %v", workID, err)}
		}
	}

	sourceWorkID, err := osAbsorptionSourcePacketWork(homePath, store, row, proofItems)
	if err != nil {
		return osAbsorptionProofResult{reason: err.Error()}
	}
	prerequisiteIDs := append([]string(nil), workIDs...)
	sort.Strings(prerequisiteIDs)
	proofRef := osAbsorptionProofModeWorkPacket + ":prerequisite=" + strings.Join(prerequisiteIDs, ",") + ";source_consumer=" + sourceWorkID
	return osAbsorptionProofResult{ok: true, proofRef: proofRef}
}

func osAbsorptionWorkEvidenceIDs(refs []string) ([]string, error) {
	seen := map[string]bool{}
	var ids []string
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		workID := ""
		switch {
		case strings.HasPrefix(ref, "work_"):
			workID = ref
		case strings.HasPrefix(ref, "work:work_"):
			workID = strings.TrimPrefix(ref, "work:")
		default:
			continue
		}
		if !safeOSAbsorptionRuntimeID(workID, "work_") {
			return nil, fmt.Errorf("invalid work evidence_ref %q", ref)
		}
		if !seen[workID] {
			seen[workID] = true
			ids = append(ids, workID)
		}
	}
	return ids, nil
}

func validateOSAbsorptionPacketWork(homePath string, w envelope.WorkEnvelope, workID string, proofItems map[string]work.ProofChainDoctorItem) error {
	if w.ID != workID {
		return fmt.Errorf("resolved work id %q does not match requested id", w.ID)
	}
	if err := w.Validate(); err != nil {
		return fmt.Errorf("invalid WorkEnvelope: %w", err)
	}
	if w.Authority.SourceClass != "source" {
		return fmt.Errorf("authority.source_class=%q, want source", w.Authority.SourceClass)
	}
	if !strings.HasPrefix(w.CanonicalSurface, "packet:") || strings.TrimSpace(strings.TrimPrefix(w.CanonicalSurface, "packet:")) == "" {
		return fmt.Errorf("canonical_surface=%q, want non-empty packet:*", w.CanonicalSurface)
	}
	if _, err := osAbsorptionPacketEvidence(w); err != nil {
		return err
	}
	if w.State != "completed" {
		return fmt.Errorf("state=%q, want completed", w.State)
	}
	item, ok := proofItems[workID]
	if !ok {
		return fmt.Errorf("canonical proof chain has no terminal item")
	}
	if !item.ProofComplete {
		return fmt.Errorf("canonical proof chain incomplete: %v", item.Problems)
	}
	if !safeOSAbsorptionRuntimeID(item.ReceiptID, "rec_") {
		return fmt.Errorf("canonical proof chain receipt_id=%q is missing or invalid", item.ReceiptID)
	}
	data, err := os.ReadFile(filepath.Join(homePath, "events", item.ReceiptID+".json"))
	if err != nil {
		return fmt.Errorf("read canonical receipt %s: %w", item.ReceiptID, err)
	}
	var receipt envelope.Receipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return fmt.Errorf("decode canonical receipt %s: %w", item.ReceiptID, err)
	}
	if err := receipt.Validate(); err != nil {
		return fmt.Errorf("canonical receipt %s invalid: %w", item.ReceiptID, err)
	}
	if receipt.ID != item.ReceiptID || receipt.IDs.WorkID != workID {
		return fmt.Errorf("canonical receipt %s does not match work %s", item.ReceiptID, workID)
	}
	if receipt.Outcome != "completed" {
		return fmt.Errorf("canonical receipt %s outcome=%q, want completed", receipt.ID, receipt.Outcome)
	}
	return nil
}

func osAbsorptionPacketEvidence(w envelope.WorkEnvelope) (string, error) {
	rawRevision := strings.TrimSpace(w.Authority.SourceRevision)
	if !strings.HasPrefix(rawRevision, "sha256:") {
		return "", fmt.Errorf("authority.source_revision=%q, want sha256 packet digest", w.Authority.SourceRevision)
	}
	revision := strings.TrimPrefix(rawRevision, "sha256:")
	if len(revision) != 64 || !isOSAbsorptionHex(revision) {
		return "", fmt.Errorf("authority.source_revision=%q, want sha256 plus 64 lowercase hex characters", w.Authority.SourceRevision)
	}
	for _, ref := range w.EvidenceRefs {
		if !strings.HasPrefix(ref, osAbsorptionPacketEvidenceRef) {
			continue
		}
		digest := strings.TrimPrefix(ref, osAbsorptionPacketEvidenceRef)
		if len(digest) == 16 && isOSAbsorptionHex(digest) && strings.HasPrefix(revision, digest) {
			return ref, nil
		}
	}
	return "", fmt.Errorf("evidence_refs lack canonical 16-character packet_sha256 digest matching authority.source_revision")
}

func osAbsorptionSourcePacketWork(homePath string, store *work.Store, row OSAbsorptionMatrixRow, proofItems map[string]work.ProofChainDoctorItem) (string, error) {
	if row.SourceKind != "packet_dir" {
		return "", fmt.Errorf("work packet authority source_kind=%q, want packet_dir", row.SourceKind)
	}
	rawSourceDir := strings.TrimSpace(row.SourceRef)
	if rawSourceDir == "" || !filepath.IsAbs(rawSourceDir) {
		return "", fmt.Errorf("work packet authority source directory %q must be absolute", row.SourceRef)
	}
	sourceDir := filepath.Clean(rawSourceDir)
	info, err := os.Stat(sourceDir)
	if err != nil {
		return "", fmt.Errorf("work packet authority source directory %s: %v", sourceDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("work packet authority source directory %s is not a directory", sourceDir)
	}

	works, err := store.ListWithArchived()
	if err != nil {
		return "", fmt.Errorf("work packet authority list: %w", err)
	}
	knownSurfaces := map[string]bool{}
	for _, w := range works {
		if strings.HasPrefix(w.CanonicalSurface, "packet:") {
			knownSurfaces[w.CanonicalSurface] = true
		}
	}
	type sourceCandidate struct {
		path  string
		proof work.PacketSourceProof
	}
	var candidates []sourceCandidate
	packetFiles := 0
	parseFailures := 0
	err = filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".md" {
			return nil
		}
		packetFiles++
		proof, err := work.PacketSourceProofForPath(path)
		if err != nil {
			parseFailures++
			return nil
		}
		if knownSurfaces[proof.CanonicalSurface] {
			candidates = append(candidates, sourceCandidate{path: path, proof: proof})
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("work packet authority source directory %s: %w", sourceDir, err)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	for _, candidate := range candidates {
		report, err := store.ActivePacketProof(time.Now(), candidate.proof.CanonicalSurface, candidate.proof.EvidenceRef)
		if err != nil {
			return "", fmt.Errorf("work packet authority source directory proof %s: %w", candidate.path, err)
		}
		if report.Status != "ok" {
			continue
		}
		w, err := store.Get(report.WorkID)
		if err != nil {
			return "", fmt.Errorf("work packet authority source work %s: %w", report.WorkID, err)
		}
		if err := validateOSAbsorptionPacketWork(homePath, w, report.WorkID, proofItems); err != nil {
			return "", fmt.Errorf("work packet authority source work %s: %w", report.WorkID, err)
		}
		return report.WorkID, nil
	}
	return "", fmt.Errorf("work packet authority source directory %s has no content-bound completed packet proof (packet_files=%d candidate_surfaces=%d parse_failures=%d)", sourceDir, packetFiles, len(candidates), parseFailures)
}

func safeOSAbsorptionRuntimeID(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix) || len(id) <= len(prefix) {
		return false
	}
	for _, r := range id[len(prefix):] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func isOSAbsorptionHex(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

// applyOSAbsorptionAutoProof records one row's auto-proof outcome onto the
// doctor report/row in place, mirroring the capability-proof bookkeeping:
// proof_status=ok + proof_ref on success, proof_status=failed on failure
// (never manual, never a status-string change).
func applyOSAbsorptionAutoProof(report *OSAbsorptionDoctorReport, dr *OSAbsorptionDoctorRow, result osAbsorptionProofResult) {
	if result.ok {
		report.AutoProofCount++
		dr.ProofStatus = "ok"
		dr.ProofRef = result.proofRef
		return
	}
	report.FailedAutoProofCount++
	dr.ProofStatus = "failed"
	dr.ProofDetail = result.reason
}

func doctorCheck(name string, ok bool, detail string) OSAbsorptionDoctorCheck {
	status := "ok"
	if !ok {
		status = "fail"
	}
	return OSAbsorptionDoctorCheck{Name: name, Status: status, Detail: detail}
}

func cleanStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
