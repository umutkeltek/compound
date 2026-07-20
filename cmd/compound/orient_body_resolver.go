// orient_body_resolver.go owns the production BodyResolver implementations wired
// into `compound orient --body-budget <n>`. There are two body sources in slice 1
// (per the packet's settled design):
//
//  1. corpus hits: per configured corpus lane, top hits by score, text re-derived
//     per query (the index never stores chunk text). Suppressed-private hits land
//     in omitted with reason suppressed_private — there is no override flag.
//  2. included source refs that resolve to repo files (AGENTS.md, constitution://…,
//     control-plane://…) — ranked BELOW all corpus hits; ordered by pack Included
//     order; ties broken by ref string.
//
// The materializer (internal/contract.MaterializeBodyPack) owns the budget-aware
// packing + the exclusion laws; this resolver only PROPOSES candidates. Private-
// overlay IncludedRefs (SourceClass == "private_overlay") never reach a candidate
// here — the materializer-side law (contract.go:1262) is the load-bearing gate, but
// the resolver filters at source too.
//
// This file is the ONLY place internal/corpus is imported by the body-materializer
// surface (the dependency-inversion LAW: internal/contract stays corpus-free).
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"compound/internal/contract"
	"compound/internal/corpus"
	"compound/internal/envelope"
)

// corpusQueryTopK is the per-lane hit cap the body resolver asks the corpus for.
// The materializer (not the resolver) is responsible for budget-aware selection,
// so the resolver returns up to this many hits per lane and lets the packer choose.
const corpusQueryTopK = 10

// orientBodyResolver is the production body resolver for `compound orient`: corpus
// hits (per configured lane) plus source-ref file bodies (per pack.Included order).
type orientBodyResolver struct {
	homePath string
	lanes    []corpus.LaneConfig
	pack     envelope.ContextPack
	cwd      string
}

// newOrientBodyResolver constructs the production resolver from a pack + the memory
// engine config's corpus lanes. The pack's Included ordering drives source-ref rank;
// its Excluded list is honored by the materializer, not here.
func newOrientBodyResolver(homePath string, pack envelope.ContextPack, cwd string) orientBodyResolver {
	lanes := []corpus.LaneConfig{}
	if strings.TrimSpace(homePath) != "" {
		if cfg, _, err := loadMemoryEngineConfig(homePath); err == nil {
			lanes = append(lanes, cfg.CorpusLanes...)
		}
		// A config read error is non-fatal for the body resolver: we degrade to
		// source-refs-only and let orient continue. The corpus subsystem already
		// reports its own doctor state elsewhere.
	}
	return orientBodyResolver{homePath: homePath, lanes: lanes, pack: pack, cwd: cwd}
}

// Resolve implements contract.BodyResolver. The corpus half queries every configured
// lane (IncludeText=true so the resolver returns the re-derived text along with the
// suppressed/private status); the source-ref half reads files for non-private-overlay
// Included refs. Corpus ranks above source refs; source refs rank by Included index
// (lower index = higher rank). Ties broken by ref asc inside the materializer.
func (r orientBodyResolver) Resolve(task string) ([]contract.BodyCandidate, error) {
	corpusCands, minCorpusRank, err := r.resolveCorpus(task)
	if err != nil {
		return nil, err
	}
	sourceCands := r.resolveSourceRefs(minCorpusRank)
	if len(corpusCands) == 0 {
		return sourceCands, nil
	}
	out := make([]contract.BodyCandidate, 0, len(corpusCands)+len(sourceCands))
	out = append(out, corpusCands...)
	out = append(out, sourceCands...)
	return out, nil
}

// resolveCorpus queries each configured lane and emits one BodyCandidate per hit.
// Returns the minimum hit score observed (used to anchor source-ref ranks BELOW all
// corpus hits). Errors degrade: per the packet, the corpus lane is consulted
// read-only — failures return an error so the caller can decide whether to proceed
// source-refs-only or fail the orient.
func (r orientBodyResolver) resolveCorpus(task string) ([]contract.BodyCandidate, float64, error) {
	if strings.TrimSpace(task) == "" || len(r.lanes) == 0 {
		return nil, 0, nil
	}
	var out []contract.BodyCandidate
	minRank := 0.0
	gotAny := false
	for _, lane := range r.lanes {
		embedder, err := newCorpusEmbedder(lane)
		if err != nil {
			return nil, 0, fmt.Errorf("body resolver: corpus lane %s embedder: %w", lane.Lane, err)
		}
		report, err := corpus.QueryWithOptions(context.Background(), r.homePath, lane, embedder, task, corpusQueryTopK, corpus.QueryOptions{IncludeText: true})
		if err != nil {
			return nil, 0, fmt.Errorf("body resolver: corpus lane %s query: %w", lane.Lane, err)
		}
		for _, hit := range report.Hits {
			out = append(out, contract.BodyCandidate{
				Ref:    hit.ChunkID,
				Source: "corpus:" + lane.Lane,
				Text:   hit.Text,
				Rank:   hit.Score,
				Status: hit.TextStatus,
			})
			if !gotAny || hit.Score < minRank {
				minRank = hit.Score
				gotAny = true
			}
		}
	}
	return out, minRank, nil
}

// resolveSourceRefs emits one BodyCandidate per Included ref that (a) is NOT a
// private-overlay ref and (b) resolves to one or more repo files via
// contract.SourceRefPaths. Source-ref rank is anchored BELOW all corpus hits: each
// ref gets minCorpusRank - 1 - (index*0.001), so lower Included indices (earlier in
// the pack) rank higher. Missing/unreadable files surface status source_unavailable;
// unmapped refs surface unresolvable — neither contributes body text, and the
// materializer lands them in omitted accordingly.
func (r orientBodyResolver) resolveSourceRefs(minCorpusRank float64) []contract.BodyCandidate {
	if len(r.pack.Included) == 0 {
		return nil
	}
	out := make([]contract.BodyCandidate, 0, len(r.pack.Included))
	idx := 0
	for _, inc := range r.pack.Included {
		ref := strings.TrimSpace(inc.Ref)
		if ref == "" {
			continue
		}
		// Private-overlay law: never propose as a body candidate. The materializer
		// also gates this (defense-in-depth), but filtering at source keeps the
		// candidate list honest.
		if inc.SourceClass == "private_overlay" {
			continue
		}
		rank := minCorpusRank - 1.0 - float64(idx)*0.001
		idx++
		paths := contract.SourceRefPaths(ref, r.cwd)
		if len(paths) == 0 {
			out = append(out, contract.BodyCandidate{
				Ref:    ref,
				Source: "source-ref",
				Rank:   rank,
				Status: contract.BodyStatusUnresolvable,
			})
			continue
		}
		text, status := readSourceRefBody(paths)
		out = append(out, contract.BodyCandidate{
			Ref:    ref,
			Source: "source-ref",
			Text:   text,
			Rank:   rank,
			Status: status,
		})
	}
	return out
}

// readSourceRefBody concatenates the file contents backing a source ref. The ref →
// paths mapping is many-to-one for some refs (e.g. control-plane://orientation-map
// → docs/ARCHITECTURE.md + docs/LAYERS.md), so a multi-path ref materializes ONE
// concatenated body. A clear separator keeps provenance legible to a model.
//
// Returns BodyStatusOK only when every path read successfully; any unreadable path
// degrades to source_unavailable with empty text (the materializer lands it in
// omitted — never a partial body, never a silent blank).
func readSourceRefBody(paths []string) (string, string) {
	var b strings.Builder
	for i, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return "", contract.BodyStatusSourceUnavailable
		}
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.Write(data)
	}
	return b.String(), contract.BodyStatusOK
}

// compile-time assertion that orientBodyResolver satisfies the seam.
var _ contract.BodyResolver = orientBodyResolver{}
