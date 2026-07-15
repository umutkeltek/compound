package authority

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/capgraph"
	"compound/internal/decisions"
	"compound/internal/envelope"
	"compound/internal/eventsource"
	"compound/internal/eventstore"
	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/policy"
	"compound/internal/routeevidence"
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
	osAbsorptionProofModeMemoryLedger  = "memory_ledger_authority"
	osAbsorptionProofModeOperatorState = "operator_state_registry"
	osAbsorptionProofModeWorkPacket    = "work_packet_authority"
	osAbsorptionProofModeOSCapability  = "os_capability_wrapper_authority"
	osAbsorptionProofModeServedOrient  = "served_orientation_authority"
	// osAbsorptionProofModeOSSkills and osAbsorptionProofModeOSStatus prove the
	// os_source_os_skills_cli and os_source_os_status_cli wrapper rows. Unlike
	// os_capability_wrapper_authority (a pure zero-native-logic proxy), `os
	// skills` and `os status` are dashboards that blend genuine live compound
	// authority reads with retained-native operational probes (skill corpus
	// registries; daemon/launchd, switchback, ledger, capacity telemetry).
	// These proof modes verify ONLY the compound-sourced slice is real
	// (digest-anchored entrypoint, no fabrication before the genuine
	// compoundCommand()+execFileSync/spawnSync call, and a live parity probe
	// cross-checked against a direct `compound` invocation); the retained
	// native reads are not required to disappear, only to stay honestly named
	// in the matrix row.
	osAbsorptionProofModeOSSkills = "os_skills_wrapper_authority"
	osAbsorptionProofModeOSStatus = "os_status_wrapper_authority"
	// osAbsorptionProofModeInfraRegistry proves an infra-os operational
	// registry (database.yaml/services.yaml) row: installed registry file
	// exists, its recorded source hash matches the live YAML's current
	// sha256, and the row's evidence_refs resolve to that ledger record. A
	// stale hash (the live source changed since import) proves as "pending"
	// drift, never a silent "ok" or an opaque "failed".
	osAbsorptionProofModeInfraRegistry = "infra_registry_authority"
	// osAbsorptionProofModeRegistryOwnership proves ownership (not health) of
	// an already-live compound_primary registry: the installed registry file
	// exists under $COMPOUND_HOME/registries/ and its corresponding Load*
	// authority function parses it without error. It deliberately does not
	// evaluate the loaded content's own status field (e.g. an
	// operator-approval-frontier row can be proof_status=ok while its
	// projected Status says "operator_approval_frontier_failed" — ownership
	// and health are different axes).
	osAbsorptionProofModeRegistryOwnership = "registry_ownership_authority"
	// osAbsorptionProofModeEventSource proves an events_dir row (an absorbed
	// operating system's events/ directory): every live .ndjson file under
	// source_ref (README.md and any non-.ndjson file excluded) has at least one
	// compound/internal/eventsource source-authority record, the highest
	// historical Imported+Skipped total ever recorded for that file covers its
	// CURRENT non-blank line count (a file that grew since the last `event
	// import-os` run proves "pending" drift naming the file, never a silent
	// "ok"), and a sample of TraceIDs from the most recent record that actually
	// imported new traces resolves to a real TraceEnvelope file under
	// COMPOUND_HOME/events/ (a missing trace file fails the row closed).
	osAbsorptionProofModeEventSource = "event_source_authority"

	osAbsorptionWorkAuthorityRef              = "compound://work"
	osAbsorptionEffectiveContractAuthorityRef = "compound://contracts/effective-contract"
	osAbsorptionPacketEvidenceRef             = "packet_sha256:"
	osAbsorptionServedEntrypointEvidenceRef   = "served_entrypoint:"
	osAbsorptionContextPackEvidenceRef        = "context_pack:"
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
	// SkippedProofCount counts rows whose auto-proof declined to run because
	// its declared dependency (e.g. an absorbed-OS root) is not configured on
	// this machine — the public-clone case. It is separate from
	// AutoProofCount/FailedAutoProofCount: a skip is neither a pass nor a
	// failure, so it must never move either of those counters.
	SkippedProofCount int `json:"skipped_proof_count"`

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
		case osAbsorptionNeedsOSCapabilityProof(row):
			applyOSAbsorptionAutoProof(&report, &dr, osAbsorptionOSCapabilityProof(homePath, row))
		case osAbsorptionNeedsOSSkillsProof(row):
			result, drift := osAbsorptionOSSkillsProof(homePath, row)
			if drift {
				dr.ProofStatus = "pending"
				dr.ProofDetail = result.reason
			} else {
				applyOSAbsorptionAutoProof(&report, &dr, result)
			}
		case osAbsorptionNeedsOSStatusProof(row):
			result, drift := osAbsorptionOSStatusProof(homePath, row)
			if drift {
				dr.ProofStatus = "pending"
				dr.ProofDetail = result.reason
			} else {
				applyOSAbsorptionAutoProof(&report, &dr, result)
			}
		case osAbsorptionNeedsCapabilityProof(row):
			result, drift := capabilityAuthorityProof(row, capRecords)
			if drift {
				dr.ProofStatus = "pending"
				dr.ProofDetail = result.reason
			} else {
				applyOSAbsorptionAutoProof(&report, &dr, result)
			}
		case osAbsorptionNeedsDecisionsProof(row):
			applyOSAbsorptionAutoProof(&report, &dr, osAbsorptionDecisionsProof(homePath))
		case osAbsorptionNeedsOperatorStateProof(row):
			applyOSAbsorptionAutoProof(&report, &dr, osAbsorptionOperatorStateProof(homePath))
		case osAbsorptionNeedsServedOrientationProof(row):
			applyOSAbsorptionAutoProof(&report, &dr, osAbsorptionServedOrientationProof(homePath, row))
		case osAbsorptionNeedsMemoryLedgerProof(row):
			if row.ProofMode == osAbsorptionProofModeMemoryLedger {
				applyOSAbsorptionAutoProof(&report, &dr, memoryLedgerAuthorityProof(homePath, row))
			} else if rec, ok := memoryLedgerProofRecord(homePath, row); ok {
				dr.ProofStatus = "ok"
				dr.ProofRef = rec
			} else {
				dr.ProofStatus = "pending"
			}
		case osAbsorptionNeedsInfraRegistryProof(row):
			result, drift := infraRegistryProof(homePath, row)
			if drift {
				dr.ProofStatus = "pending"
				dr.ProofDetail = result.reason
			} else {
				applyOSAbsorptionAutoProof(&report, &dr, result)
			}
		case osAbsorptionNeedsEventSourceProof(row):
			result, drift := eventSourceAuthorityProof(homePath, row)
			if drift {
				dr.ProofStatus = "pending"
				dr.ProofDetail = result.reason
			} else {
				applyOSAbsorptionAutoProof(&report, &dr, result)
			}
		case osAbsorptionNeedsRegistryOwnershipProof(row):
			applyOSAbsorptionAutoProof(&report, &dr, registryOwnershipProof(homePath, row))
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
		case "skipped":
			// SkippedProofCount is already incremented by
			// applyOSAbsorptionAutoProof at the moment the skip happens;
			// nothing to add here beyond leaving it out of proven/asserted/
			// failed.
		}
		report.Rows = append(report.Rows, dr)
	}
	report.Checks = append(report.Checks,
		doctorCheck("installed", report.Installed, fmt.Sprintf("%s rows=%d", report.AuthorityPath, report.RowCount)),
		doctorCheck("required_fields", report.MissingRequiredCount == 0, fmt.Sprintf("missing=%d", report.MissingRequiredCount)),
		doctorCheck("capability_auto_proofs", report.AutoProofCount >= 3 && report.FailedAutoProofCount == 0, fmt.Sprintf("auto=%d failed=%d", report.AutoProofCount, report.FailedAutoProofCount)),
		// This floor is a liveness/honesty sanity check ("the matrix still
		// names real, gated pending work — it hasn't been gamed into
		// 100% absorbed"), not a target to hit. It tracks the real,
		// currently-known pending backlog and is expected to step DOWN as
		// rows are honestly resolved (most recently: os_source_os_skills_cli
		// and os_source_os_status_cli moved from pending to proven wrapper
		// rows here, dropping the live count from 5 to 3 — see
		// os_source_brain_solo_builder_doctrine, os_source_gbrain,
		// os_source_umut_events). Re-tune it down again the next time a
		// pending row is honestly resolved; never raise it to paper over a
		// regression.
		doctorCheck("pending_retirement_gates", report.PendingWithGateCount >= 3, fmt.Sprintf("pending_with_gate=%d", report.PendingWithGateCount)),
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

// capabilityAuthorityProof dispatches capability_source_authority proof for
// one row. A row with no absorb_subset_file evidence_refs takes the legacy,
// unchanged whole-directory path (exact (SourcePath, SourceRevision) string
// equality against a capsrc record) so already-green rows like the recipes
// row (os_source_brain_solo_builder_os) are unaffected. A row that declares
// an enumerated absorb-subset (e.g. os_source_brain_solo_builder_doctrine,
// which absorbed only 2 of 36 files in its source_ref tree) instead takes the
// subset-scoped path: only the named files are ever read or hashed, so
// out-of-row churn elsewhere in the same directory can never flip the row.
// The bool return is true only for subset drift (pending), mirroring
// infraRegistryProof's (result, drift) shape.
func capabilityAuthorityProof(row OSAbsorptionMatrixRow, records []capgraph.SourceAuthorityRecord) (osAbsorptionProofResult, bool) {
	subset := osAbsorptionCapabilitySubsetFiles(row.EvidenceRefs)
	if len(subset) == 0 {
		if rec, ok := capabilityAuthorityProofRecord(row, records); ok {
			return osAbsorptionProofResult{ok: true, proofRef: rec.ID}, false
		}
		return osAbsorptionProofResult{reason: "no matching capability source-authority record for " + row.SourceRef}, false
	}
	return capabilityAuthoritySubsetProof(row, records, subset)
}

// osAbsorptionCapabilitySubsetFiles parses "absorb_subset_file:<relative
// path>:sha256:<hash>" entries out of a ref list into relative-path -> sha256
// hex digest. A row (or capsrc record) with no such entries declares no
// subset, which is exactly the legacy whole-directory signal.
func osAbsorptionCapabilitySubsetFiles(refs []string) map[string]string {
	const prefix = "absorb_subset_file:"
	const hashMarker = ":sha256:"
	subset := map[string]string{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if !strings.HasPrefix(ref, prefix) {
			continue
		}
		rest := strings.TrimPrefix(ref, prefix)
		idx := strings.LastIndex(rest, hashMarker)
		if idx < 0 {
			continue
		}
		file := strings.TrimSpace(rest[:idx])
		hash := strings.TrimSpace(rest[idx+len(hashMarker):])
		if file == "" || hash == "" {
			continue
		}
		subset[file] = hash
	}
	return subset
}

// capabilityAuthoritySubsetProof re-hashes, on every doctor run, exactly the
// files the row's absorb-subset names (never the rest of source_ref) and
// requires a capsrc record whose own absorb_subset_file evidence matches that
// exact declared subset — tying the row to the specific import that absorbed
// it. A subset file whose live sha256 no longer matches the absorbed hash is
// reported as pending drift naming the file (never a silent ok); files
// outside the subset are never read, so evidence-only/out-of-row churn (e.g.
// PORTFOLIO-MAP.md next to ETHOS.md) can never affect the row.
func capabilityAuthoritySubsetProof(row OSAbsorptionMatrixRow, records []capgraph.SourceAuthorityRecord, subset map[string]string) (osAbsorptionProofResult, bool) {
	sourceDir := strings.TrimSpace(row.SourceRef)
	if sourceDir == "" {
		return osAbsorptionProofResult{reason: "capability source authority subset: row missing source_ref"}, false
	}

	var rec *capgraph.SourceAuthorityRecord
	for i := range records {
		candidate := records[i]
		if candidate.AuthorityRef != capgraph.CapabilityGraphAuthorityRef {
			continue
		}
		if strings.TrimSpace(candidate.SourcePath) != sourceDir {
			continue
		}
		if capabilitySubsetsEqual(osAbsorptionCapabilitySubsetFiles(candidate.EvidenceRefs), subset) {
			rec = &records[i]
			break
		}
	}
	if rec == nil {
		return osAbsorptionProofResult{reason: "no capability source-authority record corroborates the declared absorb-subset for " + sourceDir}, false
	}

	files := make([]string, 0, len(subset))
	for file := range subset {
		files = append(files, file)
	}
	sort.Strings(files)

	for _, file := range files {
		wantHash := subset[file]
		livePath := filepath.Join(sourceDir, file)
		body, err := os.ReadFile(livePath)
		if err != nil {
			return osAbsorptionProofResult{reason: fmt.Sprintf("capability source authority subset file %s: %v", livePath, err)}, false
		}
		liveHash := digestBytes(body)
		if liveHash != wantHash {
			return osAbsorptionProofResult{reason: fmt.Sprintf("capability source authority subset file %s has drifted since import (live sha256:%s, absorbed sha256:%s); re-run capability import-skills to refresh", livePath, liveHash, wantHash)}, true
		}
	}

	return osAbsorptionProofResult{
		ok:       true,
		proofRef: fmt.Sprintf("%s;absorb_subset=%s", rec.ID, strings.Join(files, ",")),
	}, false
}

func capabilitySubsetsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for file, hash := range a {
		if b[file] != hash {
			return false
		}
	}
	return true
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

func osAbsorptionNeedsMemoryLedgerProof(row OSAbsorptionMatrixRow) bool {
	if row.ProofMode != "" {
		return row.ProofMode == osAbsorptionProofModeMemoryLedger
	}
	return row.CompoundOwner == "compound://ledger/memory"
}

func memoryLedgerProofRecord(homePath string, row OSAbsorptionMatrixRow) (proofRef string, ok bool) {
	store, err := mem.OpenReadOnly(homePath)
	if err != nil {
		return "", false
	}
	claims, err := store.LedgerClaims()
	if err != nil {
		return "", false
	}
	evidence, err := store.Evidence()
	if err != nil {
		return "", false
	}

	rowRefs := make(map[string]bool, len(row.EvidenceRefs))
	for _, ref := range row.EvidenceRefs {
		if ref = memoryLedgerCanonicalRef(ref); ref != "" {
			rowRefs[ref] = true
		}
	}
	evidenceByID := make(map[string]mem.EvidenceItem, len(evidence))
	for _, item := range evidence {
		evidenceByID[item.ID] = item
	}

	for _, claim := range claims {
		if strings.TrimSpace(claim.PromotionBasis) == "" {
			continue
		}
		if evidenceRef, matched := memoryLedgerClaimSourceRef(row, rowRefs, evidenceByID, claim); matched {
			return fmt.Sprintf("%s:claim=%s;evidence=%s", osAbsorptionProofModeMemoryLedger, claim.ID, evidenceRef), true
		}
	}
	return "", false
}

func memoryLedgerClaimSourceRef(row OSAbsorptionMatrixRow, rowRefs map[string]bool, evidenceByID map[string]mem.EvidenceItem, claim mem.Claim) (string, bool) {
	proofEvidence, hasProofEvidence := memoryLedgerClaimEvidenceRef(claim)
	for _, ref := range claim.EvidenceRefs {
		canonical := memoryLedgerCanonicalRef(ref)
		if canonical != "" && rowRefs[canonical] {
			if strings.HasPrefix(canonical, "evidence:") {
				return memoryLedgerProofEvidenceRef(ref), true
			}
			if hasProofEvidence {
				return proofEvidence, true
			}
		}
		if strings.TrimSpace(ref) == row.SourceRef && hasProofEvidence {
			return proofEvidence, true
		}
		item, exists := evidenceByID[strings.TrimPrefix(canonical, "evidence:")]
		if !exists {
			continue
		}
		if rowRefs[memoryLedgerCanonicalRef(item.SourceID)] || strings.TrimSpace(item.SourceID) == row.SourceRef {
			return item.ID, true
		}
	}
	if pageID := strings.TrimSpace(claim.Qualifiers["page_id"]); hasProofEvidence && pageID != "" && rowRefs[memoryLedgerCanonicalRef("knowledge_page:"+pageID)] {
		return proofEvidence, true
	}
	for _, key := range []string{"source_path", "source_ref"} {
		if sourceRef := strings.TrimSpace(claim.Qualifiers[key]); hasProofEvidence && sourceRef != "" && sourceRef == row.SourceRef {
			return proofEvidence, true
		}
	}
	return "", false
}

func memoryLedgerCanonicalRef(ref string) string {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "evidence:"):
		return "evidence:" + strings.TrimSpace(strings.TrimPrefix(ref, "evidence:"))
	case strings.HasPrefix(ref, "knowledge_page:"):
		return "knowledge_page:" + strings.TrimSpace(strings.TrimPrefix(ref, "knowledge_page:"))
	case strings.HasPrefix(ref, "ev_"):
		return "evidence:" + ref
	case strings.HasPrefix(ref, "knowledge_"):
		return "knowledge_page:" + ref
	default:
		return ref
	}
}

func memoryLedgerProofEvidenceRef(ref string) string {
	return strings.TrimPrefix(memoryLedgerCanonicalRef(ref), "evidence:")
}

func memoryLedgerClaimEvidenceRef(claim mem.Claim) (string, bool) {
	for _, ref := range claim.EvidenceRefs {
		if canonical := memoryLedgerCanonicalRef(ref); strings.HasPrefix(canonical, "evidence:") {
			return strings.TrimPrefix(canonical, "evidence:"), true
		}
	}
	return "", false
}

func memoryLedgerAuthorityProof(homePath string, row OSAbsorptionMatrixRow) osAbsorptionProofResult {
	contextPackID, err := memoryLedgerContextPackEvidenceID(row.EvidenceRefs)
	if err != nil {
		return osAbsorptionProofResult{reason: err.Error()}
	}
	pack, err := readOSAbsorptionContextPack(homePath, contextPackID)
	if err != nil {
		return osAbsorptionProofResult{reason: err.Error()}
	}
	if pack.Authority.SourceClass != "runtime" || pack.Authority.Status != "current" {
		return osAbsorptionProofResult{reason: fmt.Sprintf("ContextPack %s is not runtime/current authority", pack.ID)}
	}
	if pack.Privacy != row.Privacy {
		return osAbsorptionProofResult{reason: fmt.Sprintf("ContextPack %s privacy %q differs from row privacy %q", pack.ID, pack.Privacy, row.Privacy)}
	}
	if strings.TrimSpace(pack.TaskContract.Intent) == "" {
		return osAbsorptionProofResult{reason: fmt.Sprintf("ContextPack %s missing task intent", pack.ID)}
	}
	contractID := strings.TrimSpace(pack.IDs.ContractID)
	if !safeOSAbsorptionRuntimeID(contractID, "contract_") {
		return osAbsorptionProofResult{reason: fmt.Sprintf("ContextPack %s missing or invalid EffectiveContract id %q", pack.ID, contractID)}
	}
	memoryRevision := osAbsorptionContextPackRevision(pack, "memory://relevance-gated")
	if !knownMemoryLedgerRevision(memoryRevision) {
		return osAbsorptionProofResult{reason: fmt.Sprintf("ContextPack %s missing known source revision for memory://relevance-gated", pack.ID)}
	}
	contract, err := readMemoryLedgerEffectiveContract(homePath, pack, contractID)
	if err != nil {
		return osAbsorptionProofResult{reason: err.Error()}
	}
	if contract.Authority.SourceClass != "runtime" || contract.Authority.Status != "current" {
		return osAbsorptionProofResult{reason: fmt.Sprintf("EffectiveContract %s is not runtime/current authority", contract.ID)}
	}
	if contract.Privacy != row.Privacy {
		return osAbsorptionProofResult{reason: fmt.Sprintf("EffectiveContract %s privacy %q differs from row privacy %q", contract.ID, contract.Privacy, row.Privacy)}
	}
	if !sameMemoryLedgerTaskContract(pack.TaskContract, contract.TaskContract) {
		return osAbsorptionProofResult{reason: fmt.Sprintf("EffectiveContract %s task contract differs from ContextPack %s", contract.ID, pack.ID)}
	}
	if contractRevision := osAbsorptionEffectiveContractRevision(contract, "memory://relevance-gated"); contractRevision != memoryRevision {
		return osAbsorptionProofResult{reason: fmt.Sprintf("EffectiveContract %s memory revision %q differs from ContextPack %s revision %q", contract.ID, contractRevision, pack.ID, memoryRevision)}
	}
	packCreatedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(pack.CreatedAt))
	if err != nil {
		return osAbsorptionProofResult{reason: fmt.Sprintf("ContextPack %s created_at invalid: %v", pack.ID, err)}
	}

	store, err := mem.OpenReadOnly(homePath)
	if err != nil {
		return osAbsorptionProofResult{reason: "memory ledger: " + err.Error()}
	}
	claims, err := store.LedgerClaims()
	if err != nil {
		return osAbsorptionProofResult{reason: "memory ledger claims: " + err.Error()}
	}
	evidence, err := store.Evidence()
	if err != nil {
		return osAbsorptionProofResult{reason: "memory ledger evidence: " + err.Error()}
	}
	rowRefs := make(map[string]bool, len(row.EvidenceRefs))
	for _, ref := range row.EvidenceRefs {
		if ref = memoryLedgerCanonicalRef(ref); ref != "" {
			rowRefs[ref] = true
		}
	}
	evidenceByID := make(map[string]mem.EvidenceItem, len(evidence))
	for _, item := range evidence {
		evidenceByID[item.ID] = item
	}

	sourceMatch := false
	temporalMismatch := false
	packConsumedMatch := false
	for _, claim := range claims {
		if !safeOSAbsorptionRuntimeID(strings.TrimSpace(claim.ID), "claim_") ||
			!candidates.ValidBasis(strings.TrimSpace(claim.PromotionBasis)) {
			continue
		}
		evidenceRef, matched := memoryLedgerClaimSourceRef(row, rowRefs, evidenceByID, claim)
		if !matched {
			continue
		}
		if !safeOSAbsorptionRuntimeID(evidenceRef, "ev_") {
			continue
		}
		if _, exists := evidenceByID[evidenceRef]; !exists {
			continue
		}
		sourceMatch = true
		claimCreatedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(claim.CreatedAt))
		if err != nil || claimCreatedAt.After(packCreatedAt) {
			temporalMismatch = true
			continue
		}
		claimRef := "claim://" + claim.ID
		if !containsOSAbsorptionRef(pack.ActiveMemory, claimRef) ||
			!memoryLedgerPackIncludesCurrentClaim(pack, claimRef) {
			continue
		}
		packConsumedMatch = true
		if !containsOSAbsorptionRef(contract.ActiveMemory, claimRef) {
			continue
		}
		return osAbsorptionProofResult{
			ok: true,
			proofRef: fmt.Sprintf(
				"%s:claim=%s;evidence=%s;context_pack=%s;contract=%s;memory_rev=%s",
				osAbsorptionProofModeMemoryLedger,
				claim.ID,
				evidenceRef,
				pack.ID,
				contractID,
				memoryRevision,
			),
		}
	}
	if packConsumedMatch {
		return osAbsorptionProofResult{reason: fmt.Sprintf("EffectiveContract %s missing selected claim consumed by ContextPack %s", contract.ID, pack.ID)}
	}
	if temporalMismatch {
		return osAbsorptionProofResult{reason: fmt.Sprintf("source-linked promoted claim postdates ContextPack %s", pack.ID)}
	}
	if sourceMatch {
		return osAbsorptionProofResult{reason: fmt.Sprintf("durably promoted source-linked claim not consumed by ContextPack %s in both ActiveMemory and Included(runtime/current)", pack.ID)}
	}
	return osAbsorptionProofResult{reason: "no durably promoted source-linked claim with persisted evidence"}
}

func memoryLedgerContextPackEvidenceID(refs []string) (string, error) {
	id := ""
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if !strings.HasPrefix(ref, osAbsorptionContextPackEvidenceRef) {
			continue
		}
		candidate := strings.TrimSpace(strings.TrimPrefix(ref, osAbsorptionContextPackEvidenceRef))
		if !safeOSAbsorptionRuntimeID(candidate, "ctx_") {
			return "", fmt.Errorf("memory ledger authority has invalid ContextPack id %q", candidate)
		}
		if id != "" {
			return "", fmt.Errorf("memory ledger authority has duplicate ContextPack ref")
		}
		id = candidate
	}
	if id == "" {
		return "", fmt.Errorf("memory ledger authority missing ContextPack ref")
	}
	return id, nil
}

func readMemoryLedgerEffectiveContract(homePath string, pack envelope.ContextPack, contractID string) (envelope.EffectiveContract, error) {
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(pack.CreatedAt))
	if err != nil {
		return envelope.EffectiveContract{}, fmt.Errorf("ContextPack %s created_at invalid: %w", pack.ID, err)
	}
	path := filepath.Join(homePath, "contracts", "effective", createdAt.UTC().Format("2006-01-02"), contractID+".json")
	body, err := os.ReadFile(path)
	if err != nil {
		return envelope.EffectiveContract{}, fmt.Errorf("EffectiveContract %s at %s: %w", contractID, path, err)
	}
	var contract envelope.EffectiveContract
	if err := json.Unmarshal(body, &contract); err != nil {
		return envelope.EffectiveContract{}, fmt.Errorf("EffectiveContract %s parse: %w", contractID, err)
	}
	if contract.ID != contractID {
		return envelope.EffectiveContract{}, fmt.Errorf("EffectiveContract path id %s contains %s", contractID, contract.ID)
	}
	if err := contract.Validate(); err != nil {
		return envelope.EffectiveContract{}, fmt.Errorf("EffectiveContract %s invalid: %w", contractID, err)
	}
	return contract, nil
}

func knownMemoryLedgerRevision(revision string) bool {
	const prefix = "sha256:"
	revision = strings.TrimSpace(revision)
	digest := strings.TrimPrefix(revision, prefix)
	return strings.HasPrefix(revision, prefix) && len(digest) == 64 && isOSAbsorptionHex(digest)
}

func sameMemoryLedgerTaskContract(left, right envelope.TaskContract) bool {
	return left.Intent == right.Intent &&
		left.Mode == right.Mode &&
		left.DesiredOutcome == right.DesiredOutcome &&
		left.SuccessPredicate == right.SuccessPredicate &&
		slices.Equal(left.NonGoals, right.NonGoals) &&
		left.ProofMedium == right.ProofMedium
}

func osAbsorptionEffectiveContractRevision(contract envelope.EffectiveContract, ref string) string {
	for _, revision := range contract.SourceRevisions {
		if revision.Source == ref {
			return strings.TrimSpace(revision.Rev)
		}
	}
	return ""
}

func memoryLedgerPackIncludesCurrentClaim(pack envelope.ContextPack, claimRef string) bool {
	for _, included := range pack.Included {
		if included.Ref == claimRef && included.SourceClass == "runtime" && included.Status == "current" {
			return true
		}
	}
	return false
}

func containsOSAbsorptionRef(refs []string, want string) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
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

func osAbsorptionNeedsOSCapabilityProof(row OSAbsorptionMatrixRow) bool {
	tupleMatches := row.ID == "os_source_os_capability_cli" &&
		row.SourceRef == "cli:os capability" &&
		row.SourceKind == "compatibility_cli" &&
		row.CompoundOwner == capgraph.CapabilityGraphAuthorityRef &&
		row.Status == "wrapper"
	if row.ProofMode != "" {
		return row.ProofMode == osAbsorptionProofModeOSCapability && tupleMatches
	}
	return tupleMatches
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

// osAbsorptionNeedsServedOrientationProof selects the served-orientation
// proof by explicit proof_mode alone once a row opts in — it no longer
// requires the row.ID to be one of two hardcoded operator-specific source
// rows. Any row that names osAbsorptionProofModeServedOrient gets the
// proof; the legacy (row.ID, status, compound_owner) tuple match remains
// only as the fallback for rows that never set proof_mode at all.
func osAbsorptionNeedsServedOrientationProof(row OSAbsorptionMatrixRow) bool {
	if row.ProofMode != "" {
		return row.ProofMode == osAbsorptionProofModeServedOrient
	}
	return row.Status == "absorbed" &&
		row.CompoundOwner == osAbsorptionEffectiveContractAuthorityRef &&
		(row.ID == "os_source_umut_live_start" || row.ID == "os_source_umut_whole_system_map")
}

// osAbsorptionProofResult is the outcome of one row's auto-proof attempt.
// skipped is distinct from ok/failed: it means the proof declined to run at
// all because a declared dependency (most commonly an absorbed-OS root) is
// not configured on this machine — the public-clone case, not a pass or a
// failure.
type osAbsorptionProofResult struct {
	ok       bool
	skipped  bool
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

type osAbsorptionServedOrientationEvidence struct {
	entrypoints   map[string]string
	contextPackID string
}

func osAbsorptionServedOrientationProof(homePath string, row OSAbsorptionMatrixRow) osAbsorptionProofResult {
	evidence, err := parseOSAbsorptionServedOrientationEvidence(row.EvidenceRefs)
	if err != nil {
		return osAbsorptionProofResult{reason: err.Error()}
	}

	digests := make(map[string]string, len(evidence.entrypoints))
	for _, harness := range []string{"codex", "claude"} {
		path := evidence.entrypoints[harness]
		body, err := os.ReadFile(path)
		if err != nil {
			return osAbsorptionProofResult{reason: fmt.Sprintf("%s entrypoint %s: %v", harness, path, err)}
		}
		if err := validateOSAbsorptionServedEntrypoint(harness, row.SourceRef, string(body)); err != nil {
			return osAbsorptionProofResult{reason: err.Error()}
		}
		digests[harness] = digestBytes(body)
	}

	pack, err := readOSAbsorptionContextPack(homePath, evidence.contextPackID)
	if err != nil {
		return osAbsorptionProofResult{reason: err.Error()}
	}
	for _, ref := range []string{
		"control-plane://orientation-map",
		"control-plane://workflow-contract",
		"control-plane://sop-packets",
		"memory://relevance-gated",
	} {
		if !osAbsorptionContextPackIncludes(pack, ref) {
			return osAbsorptionProofResult{reason: fmt.Sprintf("ContextPack %s missing included ref %s", pack.ID, ref)}
		}
		rev := osAbsorptionContextPackRevision(pack, ref)
		if rev == "" || rev == "unknown" {
			return osAbsorptionProofResult{reason: fmt.Sprintf("ContextPack %s missing source revision for %s", pack.ID, ref)}
		}
	}
	if strings.TrimSpace(pack.IDs.ContractID) == "" {
		return osAbsorptionProofResult{reason: fmt.Sprintf("ContextPack %s missing EffectiveContract id", pack.ID)}
	}
	if strings.TrimSpace(pack.TaskContract.Intent) == "" {
		return osAbsorptionProofResult{reason: fmt.Sprintf("ContextPack %s missing task intent", pack.ID)}
	}

	return osAbsorptionProofResult{
		ok: true,
		proofRef: fmt.Sprintf(
			"%s:context_pack=%s;contract=%s;codex_sha256=%s;claude_sha256=%s",
			osAbsorptionProofModeServedOrient,
			pack.ID,
			pack.IDs.ContractID,
			digests["codex"],
			digests["claude"],
		),
	}
}

func parseOSAbsorptionServedOrientationEvidence(refs []string) (osAbsorptionServedOrientationEvidence, error) {
	evidence := osAbsorptionServedOrientationEvidence{entrypoints: map[string]string{}}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		switch {
		case strings.HasPrefix(ref, osAbsorptionServedEntrypointEvidenceRef):
			parts := strings.SplitN(strings.TrimPrefix(ref, osAbsorptionServedEntrypointEvidenceRef), ":", 2)
			if len(parts) != 2 || (parts[0] != "codex" && parts[0] != "claude") || strings.TrimSpace(parts[1]) == "" {
				return evidence, fmt.Errorf("served orientation evidence has invalid entrypoint ref %q", ref)
			}
			if evidence.entrypoints[parts[0]] != "" {
				return evidence, fmt.Errorf("served orientation evidence has duplicate %s entrypoint", parts[0])
			}
			evidence.entrypoints[parts[0]] = strings.TrimSpace(parts[1])
		case strings.HasPrefix(ref, osAbsorptionContextPackEvidenceRef):
			id := strings.TrimSpace(strings.TrimPrefix(ref, osAbsorptionContextPackEvidenceRef))
			if !safeOSAbsorptionRuntimeID(id, "ctx_") {
				return evidence, fmt.Errorf("served orientation evidence has invalid ContextPack id %q", id)
			}
			if evidence.contextPackID != "" {
				return evidence, fmt.Errorf("served orientation evidence has duplicate ContextPack ref")
			}
			evidence.contextPackID = id
		}
	}
	for _, harness := range []string{"codex", "claude"} {
		if evidence.entrypoints[harness] == "" {
			return evidence, fmt.Errorf("served orientation evidence missing %s entrypoint ref", harness)
		}
	}
	if evidence.contextPackID == "" {
		return evidence, fmt.Errorf("served orientation evidence missing ContextPack ref")
	}
	return evidence, nil
}

func validateOSAbsorptionServedEntrypoint(harness, sourceRef, body string) error {
	required := []string{
		"## Orient first — Compound (the served contract)",
		"compound orient --harness " + harness + " --task \"<task>\"",
		"source-in-place evidence",
		"rollback",
		sourceRef,
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			return fmt.Errorf("%s entrypoint missing %q", harness, want)
		}
	}
	for _, retired := range []string{
		"## Orient first — umut-os",
		"FIRST action is recall",
		"**Recall first:**",
	} {
		if strings.Contains(body, retired) {
			return fmt.Errorf("%s entrypoint retains retired first-action directive %q", harness, retired)
		}
	}
	return nil
}

func readOSAbsorptionContextPack(homePath, id string) (envelope.ContextPack, error) {
	path := filepath.Join(homePath, "context", "packs", id+".json")
	body, err := os.ReadFile(path)
	if err != nil {
		return envelope.ContextPack{}, fmt.Errorf("ContextPack %s at %s: %w", id, path, err)
	}
	var pack envelope.ContextPack
	if err := json.Unmarshal(body, &pack); err != nil {
		return envelope.ContextPack{}, fmt.Errorf("ContextPack %s parse: %w", id, err)
	}
	if pack.ID != id {
		return envelope.ContextPack{}, fmt.Errorf("ContextPack path id %s contains %s", id, pack.ID)
	}
	if err := pack.Validate(); err != nil {
		return envelope.ContextPack{}, fmt.Errorf("ContextPack %s invalid: %w", id, err)
	}
	return pack, nil
}

func osAbsorptionContextPackIncludes(pack envelope.ContextPack, ref string) bool {
	for _, included := range pack.Included {
		if included.Ref == ref {
			return true
		}
	}
	return false
}

func osAbsorptionContextPackRevision(pack envelope.ContextPack, ref string) string {
	for _, revision := range pack.SourceRevisions {
		if revision.Source == ref {
			return strings.TrimSpace(revision.Rev)
		}
	}
	return ""
}

// osAbsorptionWorkPacketProof proves both halves of packet absorption:
// every explicitly cited WorkEnvelope is a completed packet-backed chain, and
// at least one current packet beneath source_ref is content-bound to the full
// WorkEnvelope -> ContextPack -> Receipt -> Transition consumer path. The
// second condition prevents unrelated packet imports from proving a directory.
func osAbsorptionWorkPacketProof(homePath string, row OSAbsorptionMatrixRow) osAbsorptionProofResult {
	workIDs, err := osAbsorptionWorkEvidenceIDs(row.EvidenceRefs)
	if err != nil {
		return osAbsorptionProofResult{reason: err.Error()}
	}
	if len(workIDs) == 0 {
		return osAbsorptionProofResult{reason: "work packet authority requires at least one work_* evidence_ref"}
	}
	candidates, packetFiles, parseFailures, err := osAbsorptionSourcePacketCandidates(row)
	if err != nil {
		return osAbsorptionProofResult{reason: err.Error()}
	}
	var result osAbsorptionProofResult
	err = home.WithAutonomyAdmissionLock(homePath, func() error {
		result = osAbsorptionWorkPacketProofLocked(homePath, workIDs, candidates, packetFiles, parseFailures)
		return nil
	})
	if err != nil {
		return osAbsorptionProofResult{reason: "work packet authority snapshot: " + err.Error()}
	}
	return result
}

func osAbsorptionWorkPacketProofLocked(homePath string, workIDs []string, candidates []osAbsorptionSourceCandidate, packetFiles, parseFailures int) osAbsorptionProofResult {
	store, err := work.Open(homePath)
	if err != nil {
		return osAbsorptionProofResult{reason: "work packet authority: " + err.Error()}
	}
	for _, workID := range workIDs {
		w, err := store.Get(workID)
		if err != nil {
			return osAbsorptionProofResult{reason: fmt.Sprintf("work packet authority %s: %v", workID, err)}
		}
		item, err := store.ProofChainForWork(workID)
		if err != nil {
			return osAbsorptionProofResult{reason: fmt.Sprintf("work packet authority %s proof chain: %v", workID, err)}
		}
		if err := validateOSAbsorptionPacketWork(homePath, w, workID, item); err != nil {
			return osAbsorptionProofResult{reason: fmt.Sprintf("work packet authority %s: %v", workID, err)}
		}
	}

	sourceWorkID, err := osAbsorptionSourcePacketWork(homePath, store, candidates, packetFiles, parseFailures)
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

func validateOSAbsorptionPacketWork(homePath string, w envelope.WorkEnvelope, workID string, item work.ProofChainDoctorItem) error {
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
	if item.WorkID != workID {
		return fmt.Errorf("canonical proof chain has no terminal item")
	}
	if !item.ProofComplete {
		return fmt.Errorf("canonical proof chain incomplete: %v", item.Problems)
	}
	if !safeOSAbsorptionRuntimeID(item.ReceiptID, "rec_") {
		return fmt.Errorf("canonical proof chain receipt_id=%q is missing or invalid", item.ReceiptID)
	}
	store, err := eventstore.Open(homePath)
	if err != nil {
		return err
	}
	record, err := store.Read(item.ReceiptID)
	if err != nil {
		return fmt.Errorf("read canonical receipt %s: %w", item.ReceiptID, err)
	}
	var receipt envelope.Receipt
	if err := json.Unmarshal(record.Bytes, &receipt); err != nil {
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

type osAbsorptionSourceCandidate struct {
	path  string
	proof work.PacketSourceProof
}

func osAbsorptionSourcePacketCandidates(row OSAbsorptionMatrixRow) ([]osAbsorptionSourceCandidate, int, int, error) {
	if row.SourceKind != "packet_dir" {
		return nil, 0, 0, fmt.Errorf("work packet authority source_kind=%q, want packet_dir", row.SourceKind)
	}
	rawSourceDir := strings.TrimSpace(row.SourceRef)
	if rawSourceDir == "" || !filepath.IsAbs(rawSourceDir) {
		return nil, 0, 0, fmt.Errorf("work packet authority source directory %q must be absolute", row.SourceRef)
	}
	sourceDir := filepath.Clean(rawSourceDir)
	info, err := os.Stat(sourceDir)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("work packet authority source directory %s: %v", sourceDir, err)
	}
	if !info.IsDir() {
		return nil, 0, 0, fmt.Errorf("work packet authority source directory %s is not a directory", sourceDir)
	}
	var candidates []osAbsorptionSourceCandidate
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
		candidates = append(candidates, osAbsorptionSourceCandidate{path: path, proof: proof})
		return nil
	})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("work packet authority source directory %s: %w", sourceDir, err)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	return candidates, packetFiles, parseFailures, nil
}

func osAbsorptionSourcePacketWork(homePath string, store *work.Store, candidates []osAbsorptionSourceCandidate, packetFiles, parseFailures int) (string, error) {
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
	matchedSurfaces := 0
	for _, candidate := range candidates {
		if !knownSurfaces[candidate.proof.CanonicalSurface] {
			continue
		}
		matchedSurfaces++
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
		item, err := store.ProofChainForWork(report.WorkID)
		if err != nil {
			return "", fmt.Errorf("work packet authority source work %s proof chain: %w", report.WorkID, err)
		}
		if err := validateOSAbsorptionPacketWork(homePath, w, report.WorkID, item); err != nil {
			return "", fmt.Errorf("work packet authority source work %s: %w", report.WorkID, err)
		}
		return report.WorkID, nil
	}
	return "", fmt.Errorf("work packet authority source directory has no content-bound completed packet proof (packet_files=%d candidate_surfaces=%d parse_failures=%d)", packetFiles, matchedSurfaces, parseFailures)
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
	if result.skipped {
		report.SkippedProofCount++
		dr.ProofStatus = "skipped"
		dr.ProofDetail = result.reason
		return
	}
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

// osAbsorptionNeedsInfraRegistryProof matches the os_source_infra_database
// and os_source_infra_services rows (and any row that opts in via
// proof_mode): both own a real live infra-os YAML source that can drift
// independently of the installed $COMPOUND_HOME copy, so their proof must
// re-hash the live source on every doctor run, not just check installation.
func osAbsorptionNeedsInfraRegistryProof(row OSAbsorptionMatrixRow) bool {
	if row.ProofMode == osAbsorptionProofModeInfraRegistry {
		return true
	}
	return row.CompoundOwner == InfraDatabaseAuthorityRef || row.CompoundOwner == InfraServicesAuthorityRef
}

// infraRegistrySourceAuthorityRefID extracts the single required
// "source_authority:<id>" evidence_ref that pins a matrix row to one exact
// ledger record. Requiring this (rather than "any record for this
// authority_ref") means a matrix row cannot silently ride along on a later,
// unrelated re-import; the row's own evidence has to name the record.
func infraRegistrySourceAuthorityRefID(refs []string) (string, error) {
	const prefix = "source_authority:"
	id := ""
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if !strings.HasPrefix(ref, prefix) {
			continue
		}
		candidate := strings.TrimSpace(strings.TrimPrefix(ref, prefix))
		if candidate == "" {
			return "", fmt.Errorf("infra registry evidence has empty source_authority ref")
		}
		if id != "" {
			return "", fmt.Errorf("infra registry evidence has duplicate source_authority ref")
		}
		id = candidate
	}
	if id == "" {
		return "", fmt.Errorf("infra registry evidence missing source_authority ref")
	}
	return id, nil
}

// infraRegistryProof proves an infra-os operational registry row. The bool
// return is true only for the drift case: the installed registry and its
// cited ledger record agree with each other, but the live infra-os source
// file has since changed. That is reported as proof_status=pending (not
// failed, not ok) so a source-side edit surfaces as review debt instead of
// silently flipping the row to unproven with no explanation, or staying
// falsely "ok".
func infraRegistryProof(homePath string, row OSAbsorptionMatrixRow) (osAbsorptionProofResult, bool) {
	var (
		installedPath string
		loadRecords   func(string) ([]InfraRegistrySourceAuthorityRecord, error)
		label         string
	)
	switch row.CompoundOwner {
	case InfraDatabaseAuthorityRef:
		installedPath, loadRecords, label = InfraDatabaseRegistryPath(homePath), LoadInfraDatabaseSourceAuthority, "infra-database"
	case InfraServicesAuthorityRef:
		installedPath, loadRecords, label = InfraServicesRegistryPath(homePath), LoadInfraServicesSourceAuthority, "infra-services"
	default:
		return osAbsorptionProofResult{reason: fmt.Sprintf("infra registry authority: unrecognized compound_owner %q", row.CompoundOwner)}, false
	}

	evidenceID, err := infraRegistrySourceAuthorityRefID(row.EvidenceRefs)
	if err != nil {
		return osAbsorptionProofResult{reason: label + " " + err.Error()}, false
	}
	records, err := loadRecords(homePath)
	if err != nil {
		return osAbsorptionProofResult{reason: fmt.Sprintf("%s source authority ledger: %v", label, err)}, false
	}
	var rec *InfraRegistrySourceAuthorityRecord
	for i := range records {
		if records[i].ID == evidenceID {
			rec = &records[i]
			break
		}
	}
	if rec == nil {
		return osAbsorptionProofResult{reason: fmt.Sprintf("%s evidence ref source_authority:%s does not resolve to a ledger record", label, evidenceID)}, false
	}
	if rec.AuthorityRef != row.CompoundOwner {
		return osAbsorptionProofResult{reason: fmt.Sprintf("%s ledger record %s authority_ref %q differs from row compound_owner %q", label, rec.ID, rec.AuthorityRef, row.CompoundOwner)}, false
	}

	installedBody, err := os.ReadFile(installedPath)
	if err != nil {
		return osAbsorptionProofResult{reason: fmt.Sprintf("installed %s registry: %v", label, err)}, false
	}
	installedRevision := "sha256:" + digestBytes(installedBody)
	if installedRevision != rec.SourceRevision {
		return osAbsorptionProofResult{reason: fmt.Sprintf("%s installed registry sha256 %s does not match ledger record %s revision %s (re-import)", label, installedRevision, rec.ID, rec.SourceRevision)}, false
	}

	liveSourcePath := strings.TrimSpace(row.SourceRef)
	if liveSourcePath == "" {
		return osAbsorptionProofResult{reason: label + " row missing source_ref"}, false
	}
	liveBody, err := os.ReadFile(liveSourcePath)
	if err != nil {
		return osAbsorptionProofResult{reason: fmt.Sprintf("%s live source %s: %v", label, liveSourcePath, err)}, false
	}
	liveRevision := "sha256:" + digestBytes(liveBody)
	if liveRevision != rec.SourceRevision {
		return osAbsorptionProofResult{reason: fmt.Sprintf("%s live source %s sha256 %s has drifted from imported record %s revision %s; re-run authority import-%s to refresh", label, liveSourcePath, liveRevision, rec.ID, rec.SourceRevision, label)}, true
	}

	return osAbsorptionProofResult{
		ok: true,
		proofRef: fmt.Sprintf(
			"%s:source_authority=%s;revision=%s;installed=%s",
			osAbsorptionProofModeInfraRegistry,
			rec.ID,
			rec.SourceRevision,
			installedPath,
		),
	}, false
}

// osAbsorptionNeedsEventSourceProof matches the os_source_umut_events row
// (and any row that opts in via proof_mode): its compound_owner is the real
// trace-envelope authority ref eventsource.TraceEventsAuthorityRef, and its
// source_kind marks it as an events directory rather than a single file.
func osAbsorptionNeedsEventSourceProof(row OSAbsorptionMatrixRow) bool {
	if row.ProofMode == osAbsorptionProofModeEventSource {
		return true
	}
	return row.CompoundOwner == eventsource.TraceEventsAuthorityRef && row.SourceKind == "events_dir"
}

// osAbsorptionEventSourceTraceSpotCheckSample bounds how many TraceIDs from
// the most recent import-bearing record are spot-checked per file, so a
// stream with hundreds of trace ids doesn't turn every doctor run into a
// full filesystem walk.
const osAbsorptionEventSourceTraceSpotCheckSample = 5

// eventSourceAuthorityProof proves the os_source_umut_events row directly
// against live eventsource.LoadSourceAuthority evidence, with zero new
// imports: every live *.ndjson file under row.SourceRef (README.md and any
// non-.ndjson file excluded) must have at least one matching source-authority
// record, and the highest historical Imported+Skipped total ever recorded for
// that file must cover the file's CURRENT non-blank line count. Because
// compound event import-os always rescans a source file from line 1 on every
// run, one run's Imported+Skipped already equals the non-blank lines it saw
// at that time; taking the max across a file's records is therefore a
// monotonically safe "best known coverage" for an append-only source (any
// lines appended after the last full pass, without a re-import, make the
// live count exceed that max and correctly report pending drift). The bool
// return is true only for that drift case, mirroring infraRegistryProof's
// (result, drift) shape; a resolvable trace-id mismatch is reported as a hard
// failure (drift=false, ok=false), never a silent pending or ok.
func eventSourceAuthorityProof(homePath string, row OSAbsorptionMatrixRow) (osAbsorptionProofResult, bool) {
	sourceDir := strings.TrimSpace(row.SourceRef)
	if sourceDir == "" || !filepath.IsAbs(sourceDir) {
		return osAbsorptionProofResult{reason: "event source authority: source_ref must be an absolute directory"}, false
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return osAbsorptionProofResult{reason: fmt.Sprintf("event source authority: source directory %s: %v", sourceDir, err)}, false
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "README.md" {
			continue
		}
		if strings.ToLower(filepath.Ext(name)) != ".ndjson" {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return osAbsorptionProofResult{reason: fmt.Sprintf("event source authority: no .ndjson files found under %s", sourceDir)}, false
	}

	records, err := eventsource.LoadSourceAuthority(homePath)
	if err != nil {
		return osAbsorptionProofResult{reason: "event source authority: " + err.Error()}, false
	}
	byFile := map[string][]eventsource.SourceAuthorityRecord{}
	for _, rec := range records {
		recPath := strings.TrimSpace(rec.SourcePath)
		if filepath.Dir(recPath) != sourceDir {
			continue
		}
		base := filepath.Base(recPath)
		byFile[base] = append(byFile[base], rec)
	}

	eventStore, err := eventstore.Open(homePath)
	if err != nil {
		return osAbsorptionProofResult{reason: "event source authority: " + err.Error()}, false
	}
	var proofRefs []string
	for _, name := range files {
		fileRecords := byFile[name]
		if len(fileRecords) == 0 {
			return osAbsorptionProofResult{reason: fmt.Sprintf("event source authority: %s has no eventsource.LoadSourceAuthority record under %s", name, sourceDir)}, true
		}

		maxCoverage := 0
		var latestWithTraces eventsource.SourceAuthorityRecord
		haveLatestWithTraces := false
		var bestCoverageRecord eventsource.SourceAuthorityRecord
		for _, rec := range fileRecords {
			coverage := rec.Imported + rec.Skipped
			if coverage >= maxCoverage {
				maxCoverage = coverage
				bestCoverageRecord = rec
			}
			if len(rec.TraceIDs) == 0 {
				continue
			}
			if !haveLatestWithTraces || rec.CreatedAt > latestWithTraces.CreatedAt {
				latestWithTraces = rec
				haveLatestWithTraces = true
			}
		}

		currentLines, err := osAbsorptionNonBlankLineCount(filepath.Join(sourceDir, name))
		if err != nil {
			return osAbsorptionProofResult{reason: fmt.Sprintf("event source authority: read %s: %v", name, err)}, false
		}
		if maxCoverage < currentLines {
			return osAbsorptionProofResult{reason: fmt.Sprintf(
				"event source authority: %s has %d line(s) appended since import (best recorded imported+skipped=%d, current non-blank lines=%d); re-run `compound event import-os` to refresh",
				name, currentLines-maxCoverage, maxCoverage, currentLines,
			)}, true
		}

		if haveLatestWithTraces {
			sample := latestWithTraces.TraceIDs
			if len(sample) > osAbsorptionEventSourceTraceSpotCheckSample {
				sample = sample[:osAbsorptionEventSourceTraceSpotCheckSample]
			}
			for _, traceID := range sample {
				exists, err := eventStore.Exists(traceID)
				if err != nil {
					return osAbsorptionProofResult{reason: fmt.Sprintf("event source authority: trace %s from record %s does not resolve under event store: %v", traceID, latestWithTraces.ID, err)}, false
				}
				if !exists {
					return osAbsorptionProofResult{reason: fmt.Sprintf("event source authority: trace %s from record %s does not resolve under event store: not found", traceID, latestWithTraces.ID)}, false
				}
			}
			proofRefs = append(proofRefs, latestWithTraces.ID)
		} else {
			proofRefs = append(proofRefs, bestCoverageRecord.ID)
		}
	}

	proofRefs = mergeStrings(proofRefs)
	sort.Strings(proofRefs)
	return osAbsorptionProofResult{ok: true, proofRef: strings.Join(proofRefs, ",")}, false
}

// osAbsorptionNonBlankLineCount counts non-blank lines in path the same way
// eventsource.ImportOSEvents does (raw := strings.TrimSpace(line); blank
// lines are skipped and never counted), so the comparison against a record's
// Imported+Skipped total is apples-to-apples.
func osAbsorptionNonBlankLineCount(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	count := 0
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

// osAbsorptionNeedsRegistryOwnershipProof matches already-live
// compound_primary registries whose ownership proof is "installed file
// exists + its own authority command loads it without error" rather than a
// source-hash drift check (unlike the infra registries, these rows do not
// each carry one stable, independently-editable upstream source file this
// doctor can re-hash on every run).
func osAbsorptionNeedsRegistryOwnershipProof(row OSAbsorptionMatrixRow) bool {
	if row.ProofMode == osAbsorptionProofModeRegistryOwnership {
		return true
	}
	_, ok := registryOwnershipSpecs("")[row.ID]
	return ok
}

// registryOwnershipSpec pairs one matrix row id with the installed-file path
// and Load* authority call that proves compound owns that registry. load
// returning a non-nil error (including "not installed") fails the row;
// load succeeding proves ownership regardless of what the loaded content's
// own status/health field says (see osAbsorptionProofModeRegistryOwnership).
type registryOwnershipSpec struct {
	path string
	load func() error
}

func registryOwnershipSpecs(homePath string) map[string]registryOwnershipSpec {
	return map[string]registryOwnershipSpec{
		"os_source_standing_grants": {
			path: policy.StandingGrantsPath(homePath),
			load: func() error { _, err := policy.LoadStandingGrantsFromHome(homePath); return err },
		},
		"os_source_operator_approval_frontier": {
			path: policy.OperatorApprovalFrontierPath(homePath),
			load: func() error { _, err := policy.LoadOperatorApprovalFrontierFromHome(homePath); return err },
		},
		"os_source_release_goal_holds": {
			path: policy.ReleaseGoalHoldsPath(homePath),
			load: func() error { _, err := policy.LoadReleaseGoalHoldsFromHome(homePath); return err },
		},
		"os_source_placement_rules": {
			path: policy.PlacementRulesPath(homePath),
			load: func() error { _, err := policy.LoadPlacementRulesFromHome(homePath); return err },
		},
		"os_source_promotion_criteria": {
			path: routeevidence.PromotionCriteriaPath(homePath),
			load: func() error { _, _, err := routeevidence.LoadPromotionCriteria(homePath); return err },
		},
		"os_source_model_capabilities": {
			path: routeevidence.ModelCapabilitiesPath(homePath),
			load: func() error { _, _, err := routeevidence.LoadModelCapabilitiesFromHome(homePath); return err },
		},
		"os_source_job_roster": {
			path: JobRosterAuthorityPath(homePath),
			load: func() error { _, err := LoadJobRosterAuthority(homePath); return err },
		},
	}
}

func registryOwnershipProof(homePath string, row OSAbsorptionMatrixRow) osAbsorptionProofResult {
	spec, ok := registryOwnershipSpecs(homePath)[row.ID]
	if !ok {
		return osAbsorptionProofResult{reason: fmt.Sprintf("registry ownership authority: no proof spec for row id %q", row.ID)}
	}
	if _, err := os.Stat(spec.path); err != nil {
		return osAbsorptionProofResult{reason: fmt.Sprintf("installed registry file: %v", err)}
	}
	if err := spec.load(); err != nil {
		return osAbsorptionProofResult{reason: fmt.Sprintf("authority command: %v", err)}
	}
	return osAbsorptionProofResult{
		ok:       true,
		proofRef: fmt.Sprintf("%s:file=%s", osAbsorptionProofModeRegistryOwnership, spec.path),
	}
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
