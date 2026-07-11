package work

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// codexJobsStateRelDir is the machine-local runtime path (relative to the user
// home) where the codex companion records its job state as
// <home>/<rel>/<repo-hash>/jobs/<task-id>.json. It is a generic local
// filesystem location derived at runtime from os.UserHomeDir; it is never a
// personal path or credential and must not be hardcoded to any account.
const codexJobsStateRelDir = ".claude/plugins/data/codex-openai-codex/state"

// ExecutorJobsProbeSourceRef labels the opt-in job-liveness probe on the
// executors board so operators can see the projection consulted it.
const ExecutorJobsProbeSourceRef = "compound.executors.codex_jobs_probe"

// workRefTokenPattern matches an explicit work_<id> reference embedded in a job
// prompt/summary. Matching is only trusted when the token equals a real work id.
var workRefTokenPattern = regexp.MustCompile(`work_[0-9A-Za-z]+`)

// DefaultCodexJobsStateDir returns the machine-local codex jobs state directory
// derived from the user home. ok is false when no home can be resolved; callers
// treat that as "no probe input" rather than an error (the probe is opt-in).
func DefaultCodexJobsStateDir() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", false
	}
	return filepath.Join(home, codexJobsStateRelDir), true
}

// stalledCodexJob is one running-status job whose owning process is not alive.
type stalledCodexJob struct {
	JobID      string
	Status     string
	Phase      string
	PID        int
	AgeSeconds int64
	LogPath    string
	Reason     string
	WorkIDs    []string
}

type codexJobFile struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	Phase     string          `json:"phase"`
	PID       int             `json:"pid"`
	StartedAt string          `json:"startedAt"`
	LogFile   string          `json:"logFile"`
	Summary   string          `json:"summary"`
	Title     string          `json:"title"`
	Request   json.RawMessage `json:"request"`
}

// scanStalledCodexJobs walks <dir>/*/jobs/*.json and returns every job whose
// JSON says status "running" but whose pid is missing or not alive. It never
// executes ps; liveness is a signal-0 syscall (see pidAlive). Matching to work
// is explicit only: the job's prompt/summary/title text is scanned for
// work_<id> tokens. A missing dir yields no results and no warning because the
// probe is opt-in.
func scanStalledCodexJobs(dir string, now time.Time) ([]stalledCodexJob, []string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	repoEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{"codex jobs state unreadable: " + err.Error()}
	}
	var out []stalledCodexJob
	var warnings []string
	for _, repoEntry := range repoEntries {
		if !repoEntry.IsDir() {
			continue
		}
		jobsDir := filepath.Join(dir, repoEntry.Name(), "jobs")
		jobEntries, err := os.ReadDir(jobsDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			warnings = append(warnings, "codex jobs dir unreadable: "+err.Error())
			continue
		}
		for _, jobEntry := range jobEntries {
			if jobEntry.IsDir() || !strings.HasSuffix(jobEntry.Name(), ".json") {
				continue
			}
			path := filepath.Join(jobsDir, jobEntry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				warnings = append(warnings, "codex job "+jobEntry.Name()+" unreadable")
				continue
			}
			var job codexJobFile
			if err := json.Unmarshal(data, &job); err != nil {
				warnings = append(warnings, "codex job "+jobEntry.Name()+" unparseable")
				continue
			}
			if strings.ToLower(strings.TrimSpace(job.Status)) != "running" {
				continue
			}
			if job.PID > 0 && PIDAlive(job.PID) {
				continue // a live process still owns this running job
			}
			reason := fmt.Sprintf("job reports running but pid %d is not alive", job.PID)
			if job.PID <= 0 {
				reason = "job reports running but records no pid"
			}
			jobID := firstNonEmptyExecutorString(job.ID, strings.TrimSuffix(jobEntry.Name(), ".json"))
			out = append(out, stalledCodexJob{
				JobID:      jobID,
				Status:     strings.TrimSpace(job.Status),
				Phase:      strings.TrimSpace(job.Phase),
				PID:        job.PID,
				AgeSeconds: executorAgeSeconds(now, job.StartedAt),
				LogPath:    strings.TrimSpace(job.LogFile),
				Reason:     reason,
				WorkIDs:    extractWorkRefTokens(job),
			})
		}
	}
	return out, warnings
}

// extractWorkRefTokens pulls unique work_<id> tokens from a job's text fields
// (prompt/summary/title). The tokens are candidate references; a caller only
// acts when a token equals an actual work item id.
func extractWorkRefTokens(job codexJobFile) []string {
	var b strings.Builder
	b.WriteString(job.Summary)
	b.WriteByte(' ')
	b.WriteString(job.Title)
	b.WriteByte(' ')
	if len(job.Request) > 0 {
		b.Write(job.Request)
	}
	seen := map[string]bool{}
	var out []string
	for _, tok := range workRefTokenPattern.FindAllString(b.String(), -1) {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}
