package corpus

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/sourceobs"
)

type SourceConfig struct {
	StateDatabase string   `json:"state_database,omitempty" yaml:"state_database,omitempty"`
	Database      string   `json:"database,omitempty" yaml:"database,omitempty"`
	Roots         []string `json:"roots,omitempty" yaml:"roots,omitempty"`
	RootsCommand  []string `json:"roots_command,omitempty" yaml:"roots_command,omitempty"`

	Root   string `json:"root" yaml:"root"`
	Format string `json:"format" yaml:"format"`
}
type EmbedderFactory func(LaneConfig) (Embedder, error)
type Coverage struct {
	LastAttemptAt   string                `json:"last_attempt_at,omitempty"`
	ShardCompaction ShardCompactionReport `json:"shard_compaction"`

	NativeRowsCaptured     int   `json:"native_rows_captured,omitempty"`
	NativeRowBytesCaptured int64 `json:"native_row_bytes_captured,omitempty"`

	NativeMetadataRows  int    `json:"native_metadata_rows,omitempty"`
	NativeBodyRows      int    `json:"native_body_rows,omitempty"`
	NativeBodyBytes     int64  `json:"native_body_bytes,omitempty"`
	NativeSessionBodies int    `json:"native_session_bodies,omitempty"`
	Error               string `json:"error,omitempty"`
	BodyBytesRead       int64  `json:"body_bytes_read"`
	VectorBytesRead     int64  `json:"vector_bytes_read"`
	IndexBytesWritten   int64  `json:"index_bytes_written"`
	PreparedItems       int    `json:"prepared_items"`
	ReusedItems         int    `json:"reused_items"`
	NewEmbeddedChunks   int    `json:"new_embedded_chunks"`
	RefreshMilliseconds int64  `json:"refresh_milliseconds"`
	IndexUnchanged      bool   `json:"index_unchanged"`
	Roots               int    `json:"roots"`

	RefusedSymlinks    int    `json:"refused_symlinks,omitempty"`
	IndexPendingChunks int    `json:"index_pending_chunks,omitempty"`
	InventoryKnown     bool   `json:"inventory_known"`
	MissingFiles       int    `json:"missing_files,omitempty"`
	OversizedFiles     int    `json:"oversized_files,omitempty"`
	DeferredFiles      int    `json:"deferred_files,omitempty"`
	FreshnessBasis     string `json:"freshness_basis,omitempty"`
	CapturedFiles      int    `json:"captured_files,omitempty"`
	UnchangedFiles     int    `json:"unchanged_files,omitempty"`
	PendingFiles       int    `json:"pending_files,omitempty"`
	Lane               string `json:"lane"`
	Status             string `json:"status"`
	CheckedAt          string `json:"checked_at,omitempty"`
	CapturedAt         string `json:"captured_at,omitempty"`
	IndexedAt          string `json:"indexed_at,omitempty"`
	Items              int    `json:"items"`
	Chunks             int    `json:"chunks"`
}
type RefreshReport struct {
	Status  string     `json:"status"`
	Sources []Coverage `json:"sources"`
}

// hardRefreshStatuses are lane outcomes where a refresh could not do its job:
// the configuration is invalid, nothing could be captured, the captured bodies
// could not be indexed, the status could not be written, or the run was cut
// short. Every other non-ok lane status (capture_incomplete from refused
// symlinks, sources deleted after capture, oversized or deferred files;
// backfilling; empty) means the lane captured and indexed everything it is
// allowed to, and the report carries that limit in the open.
var hardRefreshStatuses = map[string]bool{
	"invalid_config":       true,
	"capture_failed":       true,
	"embedder_unavailable": true,
	"index_failed":         true,
	"status_write_failed":  true,
	"cancelled":            true,
}

// Failed reports whether a scheduled refresh must exit non-zero: a lane hit a
// hard failure, or no lane is configured. A degraded report whose lanes are
// only partial captures is a successful run with visible limits, not a failed
// one; treating it as failed made every unattended cycle red in a home where a
// source root holds a symlink or a transcript was deleted after capture.
func (r RefreshReport) Failed() bool {
	if r.Status == "not_configured" {
		return true
	}
	for _, c := range r.Sources {
		if hardRefreshStatuses[c.Status] {
			return true
		}
	}
	return false
}

func refreshPath(homePath string, lane LaneConfig) string {
	return filepath.Join(filepath.Dir(IndexDir(homePath, lane)), "refresh.json")
}

// refreshLanesByAge gives skipped and newly configured sources their turn after
// a bounded pass. The caller's configuration remains unchanged.
func refreshLanesByAge(homePath string, lanes []LaneConfig, now time.Time) []LaneConfig {
	ordered := append([]LaneConfig(nil), lanes...)
	checked := make(map[string]time.Time, len(lanes))
	for _, lane := range lanes {
		var previous Coverage
		if readJSONFile(refreshPath(homePath, lane), &previous) == nil {
			last := previous.LastAttemptAt
			if last == "" {
				last = previous.CheckedAt
			}
			stamp, err := time.Parse(time.RFC3339Nano, last)
			if err == nil && !stamp.After(now) {
				checked[lane.Lane] = stamp
			}
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return checked[ordered[i].Lane].Before(checked[ordered[j].Lane])
	})
	return ordered
}

func Refresh(ctx context.Context, homePath string, lanes []LaneConfig, factory EmbedderFactory, now time.Time) RefreshReport {
	report := RefreshReport{Status: "ok", Sources: []Coverage{}}
	for _, lane := range refreshLanesByAge(homePath, lanes, now) {
		if lane.Source == nil {
			continue
		}
		laneStarted := time.Now()
		c := Coverage{Lane: lane.Lane, Status: "ok", CheckedAt: now.UTC().Format(time.RFC3339Nano)}
		if err := ctx.Err(); err != nil {
			c.Status = "cancelled"
			c.CheckedAt = ""
			report.Status = "degraded"
			report.Sources = append(report.Sources, c)
			// This source was not attempted. Keep its previous receipt and age
			// instead of marking skipped work freshly checked.
			continue
		} else {
			// Retain observed coverage while recording the attempt before any
			// expensive capture. A crash cannot monopolize the next pass.
			path := refreshPath(homePath, lane)
			var prior Coverage
			_ = readJSONFile(path, &prior)
			prior.Lane, prior.Status, prior.LastAttemptAt = lane.Lane, "refreshing", c.CheckedAt
			c.LastAttemptAt = c.CheckedAt
			writeErr := os.MkdirAll(filepath.Dir(path), 0700)
			if writeErr == nil {
				writeErr = writeJSONFile(path, prior)
			}
			if writeErr != nil {
				c.Status, c.Error = "status_write_failed", writeErr.Error()
				report.Status = "degraded"
				report.Sources = append(report.Sources, c)
				continue
			}
			valid, err := ValidateLaneConfig(lane)
			if err != nil {
				c.Status = "invalid_config"
				c.Error = err.Error()
			} else {
				roots, rootErr := valid.Source.approvedRoots(ctx)
				captured := sourceobs.CaptureBatchReport{}
				err := rootErr
				c.Roots = len(roots)
				if err == nil {
					for i, root := range roots {
						ns := ""
						if i > 0 || valid.Source.Root == "" {
							ns = root
						}
						var part sourceobs.CaptureBatchReport
						var e error
						if valid.Source.Format == "codex-history" {
							part, e = sourceobs.CaptureCodexHistory(ctx, homePath, valid.SourceFamily, root, valid.Source.Database, valid.Source.StateDatabase, now)
						} else if valid.Source.Format == "opencode" || valid.Source.Format == "hermes" {
							part, e = sourceobs.CaptureNativeConversations(ctx, homePath, valid.SourceFamily, root, valid.Source.Format, valid.Source.Database, now)
						} else {
							part, e = sourceobs.CaptureApprovedDirectoryBatchWithOptions(ctx, homePath, valid.SourceFamily, root, valid.Source.Format, now, sourceobs.CaptureOptions{ContinueAfterRefusedSymlink: true, SourceNamespace: ns})
						}
						if e != nil {
							err = e
							break
						}
						captured.NativeRowsCaptured += part.NativeRowsCaptured
						captured.NativeRowBytesCaptured += part.NativeRowBytesCaptured
						captured.NativeMetadataRows += part.NativeMetadataRows
						captured.NativeBodyRows += part.NativeBodyRows
						captured.NativeBodyBytes += part.NativeBodyBytes
						captured.NativeSessionBodies += part.NativeSessionBodies
						captured.RefusedSymlinks += part.RefusedSymlinks
						captured.Missing += part.Missing
						captured.Oversized += part.Oversized
						captured.Deferred += part.Deferred
						captured.Captured += part.Captured
						captured.Unchanged += part.Unchanged
						captured.Pending += part.Pending
						captured.ItemCount = part.ItemCount
					}
				}
				if err != nil {
					c.Status = "capture_failed"
					c.Error = err.Error()
				} else {
					c.NativeRowsCaptured = captured.NativeRowsCaptured
					c.NativeRowBytesCaptured = captured.NativeRowBytesCaptured
					c.NativeMetadataRows = captured.NativeMetadataRows
					c.NativeBodyRows = captured.NativeBodyRows
					c.NativeBodyBytes = captured.NativeBodyBytes
					c.NativeSessionBodies = captured.NativeSessionBodies
					c.RefusedSymlinks = captured.RefusedSymlinks
					c.MissingFiles = captured.Missing
					c.OversizedFiles = captured.Oversized
					c.DeferredFiles = captured.Deferred
					c.FreshnessBasis = "inode_size_mtime_ctime; capture hashes changed bytes"
					if valid.Source.Format == "opencode" || valid.Source.Format == "hermes" || valid.Source.Format == "codex-history" {
						c.FreshnessBasis = "sqlite_transaction; database_wal_identity; session_metadata; daily_full_row_reconciliation"
						if valid.Source.Format == "codex-history" {
							c.FreshnessBasis = "sqlite_transaction; database_wal_identity; indexed_ordinal_ranges; bounded_resumable_daily_reconciliation"
						}
					}
					c.CapturedFiles = captured.Captured
					c.UnchangedFiles = captured.Unchanged
					c.PendingFiles = captured.Pending
					c.CapturedAt = c.CheckedAt
					c.Items = captured.ItemCount
					c.InventoryKnown = true
					if captured.ItemCount == 0 {
						c.Status = "empty"
					} else {
						embedder, err := factory(valid)
						if err != nil {
							c.Status = "embedder_unavailable"
							c.Error = err.Error()
						} else {
							indexed, err := Index(ctx, homePath, valid, embedder, IndexOptions{Incremental: true})
							// Costs and partial progress are observed even when preparation,
							// a later provider batch, or publication fails.
							c.ShardCompaction = indexed.ShardCompaction
							c.BodyBytesRead = indexed.BodyBytesRead
							c.VectorBytesRead = indexed.IndexBytesRead
							c.IndexBytesWritten = indexed.IndexBytesWritten
							c.PreparedItems = indexed.PreparedItems
							c.ReusedItems = indexed.ReusedItems
							c.NewEmbeddedChunks = indexed.NewEmbeddedChunks
							c.IndexUnchanged = indexed.Unchanged
							if indexed.Items > 0 {
								c.Items = indexed.Items
							}
							c.Chunks = indexed.TotalChunks
							c.IndexPendingChunks = indexed.TotalChunks - indexed.EmbeddedChunks

							if err != nil {
								c.Status = "index_failed"
								c.Error = err.Error()
							} else {
								c.IndexedAt = c.CheckedAt
								if c.PendingFiles > 0 {
									c.Status = "backfilling"
								}
							}
						}
					}
				}
			}
		}
		if (c.MissingFiles > 0 || c.OversizedFiles > 0 || c.DeferredFiles > 0) && !hardRefreshStatuses[c.Status] {
			c.Status = "capture_incomplete"
		}
		if c.RefusedSymlinks > 0 && (c.Status == "ok" || c.Status == "empty" || c.Status == "backfilling") {
			c.Status = "capture_incomplete"
		}
		c.RefreshMilliseconds = time.Since(laneStarted).Milliseconds()
		path := refreshPath(homePath, lane)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			c.Status = "status_write_failed"
		} else if err := writeJSONFile(path, c); err != nil {
			c.Status = "status_write_failed"
		}
		if c.Status == "backfilling" && report.Status == "ok" {
			report.Status = "backfilling"
		} else if c.Status != "ok" && c.Status != "empty" && c.Status != "backfilling" {
			report.Status = "degraded"
		}
		report.Sources = append(report.Sources, c)
	}
	if len(report.Sources) == 0 {
		report.Status = "not_configured"
	}
	return report
}
func (s SourceConfig) validate() error {
	if s.Root != "" && !filepath.IsAbs(s.Root) || s.Root == "" && len(s.Roots) == 0 && len(s.RootsCommand) == 0 {
		return fmt.Errorf("approved source root must be absolute")
	}
	for _, root := range s.Roots {
		if !filepath.IsAbs(root) {
			return fmt.Errorf("approved source roots must be absolute")
		}
	}
	if s.Database != "" && (filepath.IsAbs(s.Database) || filepath.Clean(s.Database) == ".." || strings.HasPrefix(filepath.Clean(s.Database), ".."+string(filepath.Separator))) {
		return fmt.Errorf("source database must stay beneath approved root")
	}
	if s.StateDatabase != "" {
		if s.Format != "codex-history" || filepath.IsAbs(s.StateDatabase) || filepath.Clean(s.StateDatabase) == "." || filepath.Clean(s.StateDatabase) == ".." || strings.HasPrefix(filepath.Clean(s.StateDatabase), ".."+string(filepath.Separator)) {
			return fmt.Errorf("state database must be a relative Codex metadata database beneath approved root")
		}
	}
	switch s.Format {
	case "markdown", "claude", "codex", "omp", "opencode", "hermes", "codex-history":
		return nil
	default:
		return fmt.Errorf("source format must be markdown, claude, codex, omp, opencode, hermes or codex-history")
	}
}

// RefreshDue is read-only and cheap enough for a post-tool hook. Errors do not
// authorize work. A stopped or failed refresh remains visibly stale.
func RefreshDue(hp string, now time.Time, changedSource ...string) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(hp, "config", "memory-engine.yaml"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var cfg struct {
		Lanes []LaneConfig `yaml:"corpus_lanes"`
	}
	if err = yaml.Unmarshal(raw, &cfg); err != nil {
		return false, err
	}
	for _, lane := range cfg.Lanes {
		if lane.Source == nil {
			continue
		}
		var c Coverage
		if err := readJSONFile(refreshPath(hp, lane), &c); err != nil {
			return true, nil
		}
		stamp, err := time.Parse(time.RFC3339, c.CheckedAt)
		if len(changedSource) > 0 && filepath.IsAbs(changedSource[0]) {
			rel, relErr := filepath.Rel(lane.Source.Root, changedSource[0])
			if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
				if st, statErr := os.Stat(changedSource[0]); statErr == nil && st.ModTime().After(stamp) {
					return true, nil
				}
			}
		}
		if err != nil || now.Sub(stamp) >= 5*time.Minute {
			return true, nil
		}
	}
	return false, nil
}
