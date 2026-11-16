package corpus

import (
	"encoding/json"
	"io"
	"os"
	"time"
)

// IndexingHealth is a cheap projection of the last refresh attempt. It never
// opens source bodies, manifests or vectors, and never changes backend readiness.
type IndexingHealth struct {
	Lane                string `json:"lane"`
	Status              string `json:"status"`
	Freshness           string `json:"freshness"`
	CheckedAt           string `json:"checked_at,omitempty"`
	IndexedAt           string `json:"indexed_at,omitempty"`
	CheckedAgeSeconds   *int64 `json:"checked_age_seconds,omitempty"`
	IngestionLagSeconds *int64 `json:"ingestion_lag_seconds,omitempty"`
	IngestionLagBasis   string `json:"ingestion_lag_basis"`
	IngestionLagStatus  string `json:"ingestion_lag_status"`
	PendingFiles        int    `json:"pending_files"`
	PendingChunks       int    `json:"index_pending_chunks"`
	CapturedFiles       int    `json:"captured_files"`
	UnchangedFiles      int    `json:"unchanged_files"`
	Items               int    `json:"items"`
	Chunks              int    `json:"chunks"`
	CostStatus          string `json:"cost_status"`
	RefreshMilliseconds *int64 `json:"refresh_milliseconds,omitempty"`
	BodyBytesRead       *int64 `json:"body_bytes_read,omitempty"`
	VectorBytesRead     *int64 `json:"vector_bytes_read,omitempty"`
	IndexBytesWritten   *int64 `json:"index_bytes_written,omitempty"`
	NewEmbeddedChunks   *int   `json:"new_embedded_chunks,omitempty"`
}

const indexingHealthStaleAfter = 15 * time.Minute

func indexingHealth(homePath string, lanes []LaneConfig, now time.Time) (string, []IndexingHealth) {
	overall := "ready"
	checks := []IndexingHealth{}
	for _, lane := range lanes {
		if lane.Source == nil {
			continue
		}
		check := IndexingHealth{Lane: lane.Lane, Status: "never_refreshed", Freshness: "unknown",
			IngestionLagBasis: "time_since_last_successful_index", IngestionLagStatus: "unknown", CostStatus: "unknown"}
		f, err := os.Open(refreshPath(homePath, lane))
		if err != nil {
			if !os.IsNotExist(err) {
				check.Status = "coverage_unavailable"
			}
		} else {
			raw, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
			closeErr := f.Close()
			if readErr != nil || closeErr != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &check) != nil {
				check = IndexingHealth{Lane: lane.Lane, Status: "coverage_unavailable", Freshness: "unknown",
					IngestionLagBasis: "time_since_last_successful_index", IngestionLagStatus: "unknown", CostStatus: "unknown"}
			}
		}
		// Lane identity and the meaning of age are owned by this projection rather
		// than mutable report bytes. Missing timestamps stay unknown rather than zero.
		check.Lane = lane.Lane
		check.IngestionLagBasis = "time_since_last_successful_index"
		check.IngestionLagStatus = "unknown"
		check.IngestionLagSeconds = nil
		check.Freshness = "unknown"
		check.CheckedAgeSeconds = nil
		check.CostStatus = "unknown"
		if check.RefreshMilliseconds != nil {
			check.CostStatus = "reported"
		}
		if stamp, e := time.Parse(time.RFC3339Nano, check.CheckedAt); e == nil && !stamp.After(now.Add(time.Minute)) {
			age := int64(now.Sub(stamp) / time.Second)
			if age < 0 {
				age = 0
			}
			check.CheckedAgeSeconds = &age
			check.Freshness = "fresh"
			if now.Sub(stamp) > indexingHealthStaleAfter {
				check.Freshness = "stale"
			}
		}
		if stamp, e := time.Parse(time.RFC3339Nano, check.IndexedAt); e == nil && !stamp.After(now.Add(time.Minute)) {
			age := int64(now.Sub(stamp) / time.Second)
			if age < 0 {
				age = 0
			}
			check.IngestionLagSeconds = &age
			check.IngestionLagStatus = "known"
		}
		if check.Status == "ok" || check.Status == "snapshot_only" {
			if check.IngestionLagSeconds == nil {
				check.Freshness = "unknown"
			} else if *check.IngestionLagSeconds > int64(indexingHealthStaleAfter/time.Second) {
				check.Freshness = "stale"
			}
		}
		switch check.Status {
		case "ok", "empty", "snapshot_only":
			if check.Freshness != "fresh" && overall == "ready" {
				overall = "stale"
			}
		case "never_refreshed":
			if overall == "ready" {
				overall = "stale"
			}
		default:
			overall = "degraded"
		}
		if check.PendingFiles > 0 || check.PendingChunks > 0 {
			overall = "degraded"
		}
		checks = append(checks, check)
	}
	return overall, checks
}
