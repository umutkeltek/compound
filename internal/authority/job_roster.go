package authority

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
)

const (
	JobRosterAuthorityRef          = "compound://registries/job-roster"
	JobRosterAuthoritySchema       = "compound.job_roster_authority.v0.1"
	JobRosterSourceAuthoritySchema = "compound.job_roster_source_authority.v0.1"

	jobRosterFile                = "job-roster.json"
	jobRosterSourceAuthorityFile = "job-roster-source-authority.jsonl"
	jobRosterImportedVia         = "authority import-job-roster"
	jobRosterRetirement          = "job status reads compound://registries/job-roster; source job registry remains scheduler write owner and rollback/evidence input"
	jobRosterRollback            = "re-import source job registry or remove COMPOUND_HOME registries/job-roster.json after review"
)

type JobRosterAuthority struct {
	SchemaVersion  string       `json:"schema_version"`
	GeneratedAt    string       `json:"generated_at"`
	AuthorityRef   string       `json:"authority_ref"`
	SourcePath     string       `json:"source_path"`
	SourceRevision string       `json:"source_revision"`
	JobCount       int          `json:"job_count"`
	EnabledCount   int          `json:"enabled_count"`
	RecentRunCount int          `json:"recent_run_count"`
	ErrorCount     int          `json:"error_count"`
	Jobs           []JobSummary `json:"jobs"`
	RecentErrors   []JobSummary `json:"recent_errors,omitempty"`
}

type JobSummary struct {
	JobID             string `json:"job_id,omitempty"`
	Name              string `json:"name"`
	Enabled           bool   `json:"enabled"`
	State             string `json:"state,omitempty"`
	Schedule          string `json:"schedule,omitempty"`
	Deliver           string `json:"deliver,omitempty"`
	Script            string `json:"script,omitempty"`
	NoAgent           bool   `json:"no_agent,omitempty"`
	Lane              string `json:"lane,omitempty"`
	LastStatus        string `json:"last_status,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	LastRunAt         string `json:"last_run_at,omitempty"`
	RecommendedAction string `json:"recommended_action,omitempty"`
}

type JobRosterSourceAuthorityRecord struct {
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
	JobCount            int      `json:"job_count"`
	EnabledCount        int      `json:"enabled_count"`
	ErrorCount          int      `json:"error_count"`
	JobIDs              []string `json:"job_ids,omitempty"`
	EvidenceRefs        []string `json:"evidence_refs"`
}

type JobRosterImportReport struct {
	AuthorityRef      string                         `json:"authority_ref"`
	AuthorityPath     string                         `json:"authority_path"`
	SourceAuthorityID string                         `json:"source_authority_id"`
	SourceAuthority   JobRosterSourceAuthorityRecord `json:"source_authority"`
	JobCount          int                            `json:"job_count"`
	EnabledCount      int                            `json:"enabled_count"`
	RecentRunCount    int                            `json:"recent_run_count"`
	ErrorCount        int                            `json:"error_count"`
	RecentErrors      []JobSummary                   `json:"recent_errors,omitempty"`
}

func ImportJobRoster(homePath, sourcePath string, now time.Time) (JobRosterImportReport, error) {
	if strings.TrimSpace(homePath) == "" {
		return JobRosterImportReport{}, fmt.Errorf("COMPOUND_HOME required")
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return JobRosterImportReport{}, fmt.Errorf("job roster source path required")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return JobRosterImportReport{}, fmt.Errorf("read job roster %s: %w", sourcePath, err)
	}
	jobs, err := parseJobRoster(data)
	if err != nil {
		return JobRosterImportReport{}, fmt.Errorf("parse job roster %s: %w", sourcePath, err)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Name != jobs[j].Name {
			return jobs[i].Name < jobs[j].Name
		}
		return jobs[i].JobID < jobs[j].JobID
	})
	revision := "sha256:" + digestBytes(data)
	authority := JobRosterAuthority{
		SchemaVersion:  JobRosterAuthoritySchema,
		GeneratedAt:    now.UTC().Format(time.RFC3339),
		AuthorityRef:   JobRosterAuthorityRef,
		SourcePath:     sourcePath,
		SourceRevision: revision,
		JobCount:       len(jobs),
		Jobs:           jobs,
	}
	for _, job := range jobs {
		if job.Enabled {
			authority.EnabledCount++
			if job.LastStatus == "error" {
				authority.ErrorCount++
				authority.RecentErrors = append(authority.RecentErrors, job)
			}
			if job.LastRunAt != "" {
				if t, err := time.Parse(time.RFC3339, job.LastRunAt); err == nil && now.Sub(t) < 48*time.Hour {
					authority.RecentRunCount++
				}
			}
		}
	}
	authorityPath := JobRosterAuthorityPath(homePath)
	if err := os.MkdirAll(filepath.Dir(authorityPath), 0o755); err != nil {
		return JobRosterImportReport{}, err
	}
	body, err := json.MarshalIndent(authority, "", "  ")
	if err != nil {
		return JobRosterImportReport{}, err
	}
	if err := os.WriteFile(authorityPath, append(body, '\n'), 0o644); err != nil {
		return JobRosterImportReport{}, err
	}
	rec := JobRosterSourceAuthorityRecord{
		SchemaVersion:       JobRosterSourceAuthoritySchema,
		ID:                  jobRosterSourceAuthorityID(sourcePath, revision),
		CreatedAt:           now.UTC().Format(time.RFC3339),
		SourcePath:          sourcePath,
		SourceRevision:      revision,
		ImportedVia:         jobRosterImportedVia,
		AuthorityRef:        JobRosterAuthorityRef,
		AuthorityStatus:     "compound_primary",
		SourceRole:          "evidence_only_input_after_import",
		Retires:             "cron_registry_read_authority:" + sourcePath,
		RetirementCondition: jobRosterRetirement,
		Rollback:            jobRosterRollback,
		JobCount:            authority.JobCount,
		EnabledCount:        authority.EnabledCount,
		ErrorCount:          authority.ErrorCount,
		JobIDs:              jobIDs(jobs),
		EvidenceRefs:        []string{revision, JobRosterAuthorityRef},
	}
	if err := appendJobRosterSourceAuthority(homePath, rec); err != nil {
		return JobRosterImportReport{}, err
	}
	return JobRosterImportReport{
		AuthorityRef:      JobRosterAuthorityRef,
		AuthorityPath:     authorityPath,
		SourceAuthorityID: rec.ID,
		SourceAuthority:   rec,
		JobCount:          authority.JobCount,
		EnabledCount:      authority.EnabledCount,
		RecentRunCount:    authority.RecentRunCount,
		ErrorCount:        authority.ErrorCount,
		RecentErrors:      authority.RecentErrors,
	}, nil
}

func LoadJobRosterAuthority(homePath string) (JobRosterAuthority, error) {
	data, err := os.ReadFile(JobRosterAuthorityPath(homePath))
	if err != nil {
		return JobRosterAuthority{}, err
	}
	var authority JobRosterAuthority
	if err := json.Unmarshal(data, &authority); err != nil {
		return JobRosterAuthority{}, err
	}
	return authority, nil
}

func LoadJobRosterSourceAuthority(homePath string) ([]JobRosterSourceAuthorityRecord, error) {
	data, err := os.ReadFile(JobRosterSourceAuthorityPath(homePath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []JobRosterSourceAuthorityRecord
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec JobRosterSourceAuthorityRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("parse job roster source authority line %d: %w", i+1, err)
		}
		records = append(records, rec)
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].CreatedAt < records[j].CreatedAt })
	return records, nil
}

func JobRosterAuthorityPath(homePath string) string {
	return filepath.Join(homePath, "registries", jobRosterFile)
}

func JobRosterSourceAuthorityPath(homePath string) string {
	return filepath.Join(homePath, "registries", jobRosterSourceAuthorityFile)
}

func parseJobRoster(data []byte) ([]JobSummary, error) {
	var doc struct {
		Jobs json.RawMessage `json:"jobs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	raw := doc.Jobs
	if len(raw) == 0 {
		raw = data
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		var byKey map[string]map[string]any
		if err2 := json.Unmarshal(raw, &byKey); err2 != nil {
			return nil, err
		}
		for key, row := range byKey {
			if _, ok := row["name"]; !ok {
				row["name"] = key
			}
			rows = append(rows, row)
		}
	}
	jobs := make([]JobSummary, 0, len(rows))
	for _, row := range rows {
		job := JobSummary{
			JobID:             firstJSONText(row, "job_id", "id"),
			Name:              firstJSONText(row, "name", "label"),
			Enabled:           jsonBool(row["enabled"]),
			State:             firstJSONText(row, "state", "status"),
			Schedule:          firstJSONText(row, "schedule", "schedule_display", "cron"),
			Deliver:           firstJSONText(row, "deliver"),
			Script:            firstJSONText(row, "script", "program"),
			NoAgent:           jsonBool(row["no_agent"]),
			Lane:              firstJSONText(row, "lane"),
			LastStatus:        firstJSONText(row, "last_status"),
			LastError:         firstJSONText(row, "last_error"),
			LastRunAt:         firstJSONText(row, "last_run_at", "last_ran_at"),
			RecommendedAction: firstJSONText(row, "recommended_action"),
		}
		if strings.TrimSpace(job.Name) == "" {
			continue
		}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("no jobs found")
	}
	return jobs, nil
}

func appendJobRosterSourceAuthority(homePath string, rec JobRosterSourceAuthorityRecord) error {
	path := JobRosterSourceAuthorityPath(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	existing, err := LoadJobRosterSourceAuthority(homePath)
	if err != nil {
		return err
	}
	for _, old := range existing {
		if old.ID == rec.ID {
			return nil
		}
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(body, '\n'))
	return err
}

func jobRosterSourceAuthorityID(sourcePath, revision string) string {
	return "jobsrc_" + digest12(sourcePath+"\n"+revision)
}

func jobIDs(jobs []JobSummary) []string {
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if job.JobID != "" {
			ids = append(ids, job.JobID)
		}
	}
	sort.Strings(ids)
	return ids
}

func firstJSONText(row map[string]any, keys ...string) string {
	for _, key := range keys {
		switch v := row[key].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case float64:
			return fmt.Sprintf("%.0f", v)
		}
	}
	return ""
}

func jsonBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func digest12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
