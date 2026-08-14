package home

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// leaseLivenessFreshness is the staleness window used to judge a non-terminal
// RunAttempt as "live". It matches runattempt.DefaultSweepThreshold (30m) so a
// pane that the runattempt sweep already considers abandoned cannot masquerade
// as alive here. Defaulting rather than threading a clock argument keeps the
// archive call sites — which use time.Now() implicitly via OperationalHygiene
// — from drifting apart from the runattempt sweep.
const leaseLivenessFreshness = 30 * time.Minute

// liveRunAttemptForWork reports whether any RunAttempt under homePath for
// workID is BOTH non-terminal AND has a heartbeat (or, lacking one, a CreatedAt)
// no older than leaseLivenessFreshness.
//
// A lease whose work item carries a live delegate pane must never be classified
// as stale by `home archive-history --kind lease` — see work_9d627f1cb724 /
// success_predicate. The terminal/TTL-only heuristic that predates this helper
// is blind to the pane: it would mark a lease stale purely because the hot
// work envelope is "completed", even while a long-running delegate session is
// still pinging. This helper is the liveness override.
//
// Read-only and best-effort: a missing run_attempts directory returns false
// (no live pane), a torn file is skipped, a foreign id is ignored. The
// archive sweep must never be blocked by a probe failure — silence reads as
// "no live pane" so the orphan backstop still fires.
func liveRunAttemptForWork(homePath, workID string, now time.Time) bool {
	if strings.TrimSpace(workID) == "" {
		return false
	}
	dir := filepath.Join(homePath, "run_attempts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if fs.ErrNotExist == err || os.IsNotExist(err) {
			return false
		}
		// unreadable dir is read as "no live pane" — the orphan backstop
		// is not the layer that surfaces probe errors.
		return false
	}
	threshold := now.UTC().Add(-leaseLivenessFreshness)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			continue
		}
		var ra struct {
			WorkID          string     `json:"work_id"`
			Status          string     `json:"status"`
			CreatedAt       time.Time  `json:"created_at"`
			LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
		}
		if jerr := json.Unmarshal(data, &ra); jerr != nil {
			continue
		}
		if ra.WorkID != workID {
			continue
		}
		// only non-terminal panes are alive; succeeded/failed/abandoned/
		// superseded are by definition done.
		switch ra.Status {
		case "succeeded", "failed", "abandoned", "superseded":
			continue
		}
		last := ra.CreatedAt
		if ra.LastHeartbeatAt != nil {
			last = *ra.LastHeartbeatAt
		}
		if last.UTC().After(threshold) {
			return true
		}
	}
	return false
}
