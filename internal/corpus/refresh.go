package corpus

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/sourceobs"
)

type SourceConfig struct {
	Root   string `json:"root" yaml:"root"`
	Format string `json:"format" yaml:"format"`
}
type EmbedderFactory func(LaneConfig) (Embedder, error)
type Coverage struct {
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

func refreshPath(homePath string, lane LaneConfig) string {
	return filepath.Join(filepath.Dir(IndexDir(homePath, lane)), "refresh.json")
}
func Refresh(ctx context.Context, homePath string, lanes []LaneConfig, factory EmbedderFactory, now time.Time) RefreshReport {
	report := RefreshReport{Status: "ok", Sources: []Coverage{}}
	for _, lane := range lanes {
		if lane.Source == nil {
			continue
		}
		c := Coverage{Lane: lane.Lane, Status: "ok", CheckedAt: now.UTC().Format(time.RFC3339Nano)}
		if err := ctx.Err(); err != nil {
			c.Status = "cancelled"
		} else {
			valid, err := ValidateLaneConfig(lane)
			if err != nil {
				c.Status = "invalid_config"
			} else {
				captured, err := sourceobs.CaptureApprovedDirectoryBatchWithOptions(ctx, homePath, valid.SourceFamily, valid.Source.Root, valid.Source.Format, now, sourceobs.CaptureOptions{ContinueAfterRefusedSymlink: true})
				if err != nil {
					c.Status = "capture_failed"
				} else {
					c.RefusedSymlinks = captured.RefusedSymlinks
					c.MissingFiles = captured.Missing
					c.OversizedFiles = captured.Oversized
					c.DeferredFiles = captured.Deferred
					c.FreshnessBasis = "size_and_nanosecond_mtime; capture hashes changed bytes"
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
						} else {
							indexed, err := Index(ctx, homePath, valid, embedder, IndexOptions{})
							if err != nil {
								c.Status = "index_failed"
							} else {
								c.IndexedAt = c.CheckedAt
								c.Items = indexed.Items
								c.Chunks = indexed.TotalChunks
								if c.PendingFiles > 0 {
									c.Status = "backfilling"
								}
							}
						}
					}
				}
			}
		}
		if c.MissingFiles > 0 || c.OversizedFiles > 0 || c.DeferredFiles > 0 {
			c.Status = "capture_incomplete"
		}
		if c.RefusedSymlinks > 0 && (c.Status == "ok" || c.Status == "empty" || c.Status == "backfilling") {
			c.Status = "capture_incomplete"
		}
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
	if !filepath.IsAbs(s.Root) {
		return fmt.Errorf("approved source root must be absolute")
	}
	switch s.Format {
	case "markdown", "claude", "codex":
		return nil
	default:
		return fmt.Errorf("source format must be markdown, claude or codex")
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
