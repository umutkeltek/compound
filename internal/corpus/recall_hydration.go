package corpus

import "context"

// Resolve only selected evidence, rechecking current privacy after ranking.
func hydrateRecallEvidence(ctx context.Context, homePath string, lanes []LaneConfig, opts QueryOptions, r *RecallReport) {
	type laneView struct {
		lane     LaneConfig
		manifest Manifest
		meta     map[string]chunkInfo
		err      error
	}
	views := map[string]laneView{}
	fail := func(lane, status string) {
		r.Status = "degraded"
		for i := range r.Coverage {
			if r.Coverage[i].Lane == lane {
				r.Coverage[i].Status = status
				break
			}
		}
	}
	kept := r.Evidence[:0]
	for _, hit := range r.Evidence {
		if ctx.Err() != nil {
			fail(hit.Lane, "cancelled")
			continue
		}
		view, seen := views[hit.Lane]
		if !seen {
			for _, lane := range lanes {
				if lane.Lane == hit.Lane {
					view.lane = lane
					break
				}
			}
			view.manifest, view.err = validateQueryItemAccess(homePath, view.lane)
			if view.err == nil {
				view.meta = metaFromManifest(view.manifest)
			}
			views[hit.Lane] = view
		}
		if view.err != nil {
			fail(hit.Lane, "source_manifest_unavailable")
			continue
		}
		item, ok := findManifestItem(view.manifest, hit.ItemID)
		info, hasChunk := view.meta[hit.ChunkID]
		if !ok || !hasChunk || (!opts.IncludeHistory && item.VersionState == "superseded") {
			fail(hit.Lane, "stale_index")
			continue
		}
		hit.Title, hit.SourceKind = info.title, info.sourceKind
		hit.VersionState, hit.SupersededBy = item.VersionState, item.SupersededBy
		resolved, err := resolveQueryHitText(ctx, homePath, view.manifest, view.lane, opts, hit.QueryHit)
		if err != nil {
			status := "query_failed"
			if ctx.Err() != nil {
				status = "cancelled"
			}
			fail(hit.Lane, status)
			continue
		}
		hit.QueryHit = resolved
		if hit.TextStatus != string(ChunkTextOK) && hit.TextStatus != string(ChunkTextSuppressedPrivate) {
			fail(hit.Lane, hit.TextStatus)
		}
		kept = append(kept, hit)
	}
	r.Evidence = kept
}
