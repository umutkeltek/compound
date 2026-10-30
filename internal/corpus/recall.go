package corpus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/breadth"
	"compound/internal/mem"
	"compound/internal/sourceobs"
)

type Evidence struct {
	TextTruncated  bool `json:"text_truncated,omitempty"`
	ContextOmitted bool `json:"context_omitted,omitempty"`
	QueryHit
	Lane         string `json:"lane"`
	EvidenceRef  string `json:"evidence_ref"`
	SnapshotTime string `json:"snapshot_time"`
	VersionState string `json:"version_state"`
	SupersededBy string `json:"superseded_by,omitempty"`
	// FileHash is a deduplication key, not source text or a source path.
	FileHash string `json:"file_hash"`
}
type RecallReport struct {
	SchemaVersion   string         `json:"schema_version"`
	Query           string         `json:"query"`
	Status          string         `json:"status"`
	EvidenceMeaning string         `json:"evidence_meaning"`
	CurrentClaims   []CurrentClaim `json:"current_claims"`
	CurrentStatus   string         `json:"current_status"`
	Evidence        []Evidence     `json:"evidence"`
	Coverage        []Coverage     `json:"coverage"`
}

// Search is the common explicit-recall consumer. It neither captures nor promotes.
// Ranks from distinct embedding identities are interleaved; scores from unlike models are never
// compared. A hit is evidence to inspect, never proof the question is answered.
func Search(ctx context.Context, homePath string, lanes []LaneConfig, factory EmbedderFactory, query string, k int, opts QueryOptions) RecallReport {
	r := RecallReport{SchemaVersion: "compound.recall.v0.1", Query: query, Status: "ok",
		EvidenceMeaning: "retrieval candidates; source statements are not adjudicated current facts",
		CurrentClaims:   []CurrentClaim{}, CurrentStatus: "lexical", Evidence: []Evidence{}, Coverage: []Coverage{}}
	if k < 1 || k > 50 {
		r.Status = "invalid_limit"
		return r
	}
	terms := questionTerms(query)
	store, err := mem.OpenReadOnly(homePath)
	if err == nil {
		var engine mem.Engine
		engine, err = breadth.NewMemoryReader(ctx, homePath)
		if err == nil {
			var current mem.ReadOnlyBreadthQueryResult
			current, err = store.QueryBreadthReadOnlyResult(query, k, engine)
			r.CurrentStatus = "compound-breadth-readonly"
			if len(current.DegradedLanes) > 0 {
				r.CurrentStatus = "degraded: " + strings.Join(current.DegradedLanes, ",")
				r.Status = "degraded"
			}
			for _, claim := range current.Claims {
				r.CurrentClaims = append(r.CurrentClaims, claimView(claim, terms))
			}
		}
	}
	if err != nil {
		r.CurrentStatus = "unavailable"
		r.Status = "degraded"
	}
	groups := make([][]Evidence, 0, len(lanes))
	identityGroups := map[string]int{}
	for _, lane := range lanes {
		c := Coverage{Lane: lane.Lane, Status: "snapshot_only", FreshnessBasis: "owned snapshot; original source freshness is not monitored"}
		var prior Coverage
		if lane.Source != nil {
			if err := readJSONFile(refreshPath(homePath, lane), &prior); err == nil {
				c = prior
				if stamp, err := time.Parse(time.RFC3339, c.CheckedAt); err != nil || time.Since(stamp) > 15*time.Minute {
					c.Status = "stale"
				}
			} else {
				c.Status = "never_refreshed"
			}
		}
		if c.Status == "empty" {
			r.Coverage = append(r.Coverage, c)
			continue
		}
		var group []Evidence
		embedder, e := factory(lane)
		if e != nil {
			c.Status = "embedder_unavailable"
		} else {
			local, qe := queryWithOptions(ctx, homePath, lane, embedder, query, k*3, opts, false)
			if qe != nil {
				switch {
				case errors.Is(qe, context.Canceled), errors.Is(qe, context.DeadlineExceeded):
					c.Status = "cancelled"
				case errors.Is(qe, os.ErrNotExist):
					c.Status = "not_indexed"
				case errors.Is(qe, ErrEmbedderMismatch):
					c.Status = "identity_mismatch"
				default:
					c.Status = "query_failed"
				}
			} else {
				manifest, me := sourceobs.LoadSourceManifest(homePath, lane.SourceFamily)
				if me != nil {
					c.Status = "source_manifest_unavailable"
				} else {
					c.Items = len(manifest.Items)
					c.InventoryKnown = true
					var progress ProgressFile
					if pe := readJSONFile(filepath.Join(IndexDir(homePath, lane), "progress.json"), &progress); pe == nil {
						c.Chunks = progress.TotalChunks
						c.IndexPendingChunks = progress.PendingChunks
						if c.IndexedAt == "" {
							c.IndexedAt = progress.UpdatedAt
						}
						if progress.PendingChunks > 0 {
							c.Status = "index_incomplete"
						}
					}
					items := map[string]sourceobs.SourceManifestItem{}
					for _, item := range manifest.Items {
						items[item.ID] = item
					}
					for _, hit := range local.Hits {
						item, ok := items[hit.ItemID]
						if !ok {
							c.Status = "stale_index"
							continue
						}
						if !opts.IncludeHistory && item.VersionState == "superseded" {
							continue
						}
						group = append(group, Evidence{QueryHit: hit, Lane: lane.Lane,
							EvidenceRef: manifest.AuthorityRef + "#" + item.ID, SnapshotTime: item.SnapshotTime,
							VersionState: item.VersionState, SupersededBy: item.SupersededBy, FileHash: item.FileHash})
					}
				}
			}
		}
		if c.Status != "ok" && c.Status != "snapshot_only" {
			r.Status = "degraded"
		}
		r.Coverage = append(r.Coverage, c)
		identity := lane.Embedder.Identity()
		idx, ok := identityGroups[identity]
		if !ok {
			idx = len(groups)
			identityGroups[identity] = idx
			groups = append(groups, nil)
		}
		groups[idx] = append(groups[idx], group...)
	}
	for _, group := range groups {
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].Score != group[j].Score {
				return group[i].Score > group[j].Score
			}
			if group[i].Lane != group[j].Lane {
				return group[i].Lane < group[j].Lane
			}
			return group[i].ChunkID < group[j].ChunkID
		})
	}
	// One best chunk per distinct body keeps duplicate exports and long sessions
	// from consuming every slot. Requested adjacent context remains attached.
	seen := map[string]bool{}
	for rank := 0; len(r.Evidence) < k; rank++ {
		found := false
		for _, group := range groups {
			if rank >= len(group) {
				continue
			}
			found = true
			hit := group[rank]
			key := hit.FileHash
			if opts.IncludeHistory {
				key += ":" + hit.SnapshotTime
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			r.Evidence = append(r.Evidence, hit)
			if len(r.Evidence) == k {
				break
			}
		}
		if !found {
			break
		}
	}
	hydrateRecallEvidence(ctx, homePath, lanes, opts, &r)
	boundRecallContext(&r, terms)
	if len(lanes) == 0 {
		r.Status = "not_configured"
	}
	return r
}
