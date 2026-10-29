package corpus

import (
	"compound/internal/retrievalparity"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const SearchUsage = "usage: compound memory search <question> [--lane L] [--k N] [--neighbors R] [--history] [--include-private-text] [--json]"

func SearchCLI(ctx context.Context, hp string, lanes []LaneConfig, args []string, out, errout io.Writer) int {
	laneName, query := "", ""
	k, neighbors := 10, 0
	opts := QueryOptions{}
	asJSON := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--lane", "--k", "--neighbors":
			if i+1 >= len(args) {
				fmt.Fprintln(errout, SearchUsage)
				return 2
			}
			i++
			v := args[i]
			if a == "--lane" {
				laneName = v
				continue
			}
			n, e := strconv.Atoi(v)
			if e != nil || n < 0 {
				fmt.Fprintln(errout, SearchUsage)
				return 2
			}
			if a == "--k" {
				k = n
			} else {
				neighbors = n
			}
		case "--include-private-text", "--include-text":
			opts.IncludeText = true
		case "--history":
			opts.IncludeHistory = true
		case "--json":
			asJSON = true
		default:
			if strings.HasPrefix(a, "-") || query != "" {
				fmt.Fprintln(errout, SearchUsage)
				return 2
			}
			query = a
		}
	}
	opts.NeighborRadius = neighbors
	if query == "" || opts.validate() != nil || k < 1 || k > 50 {
		fmt.Fprintln(errout, SearchUsage)
		return 2
	}
	if laneName != "" {
		for _, l := range lanes {
			if l.Lane != laneName {
				continue
			}
			e, err := NewConfiguredEmbedder(l)
			if err != nil {
				fmt.Fprintln(errout, "memory search: embedder unavailable")
				return 1
			}
			started := time.Now()
			r, err := QueryWithOptions(ctx, hp, l, e, query, k, opts)
			refs := make([]string, 0, len(r.Hits))
			for _, hit := range r.Hits {
				refs = append(refs, hit.ChunkID)
			}
			retrievalparity.Record(hp, retrievalparity.NewRow(retrievalparity.ConsumerSearch, query, refs, time.Since(started), err == nil, time.Now()))
			if err != nil {
				fmt.Fprintln(errout, "memory search: source query unavailable")
				return 1
			}
			if asJSON {
				return encodeCLI(out, errout, r)
			}
			fmt.Fprintf(out, "%d source hit(s) for %q\n", len(r.Hits), query)
			for _, hit := range r.Hits {
				fmt.Fprintf(out, "%s [%s] %s\n", hit.ChunkID, hit.TextStatus, hit.Text)
			}
			return 0
		}
		fmt.Fprintln(errout, "memory search: lane not configured")
		return 1
	}
	if neighbors == 0 {
		opts.NeighborRadius = 1
	}
	r := Search(ctx, hp, lanes, NewConfiguredEmbedder, query, k, opts)
	if asJSON {
		if code := encodeCLI(out, errout, r); code != 0 {
			return code
		}
	} else {
		fmt.Fprintf(out, "Recall: %s; %d current claim(s), %d source candidate(s).\n", r.Status, len(r.CurrentClaims), len(r.Evidence))
		fmt.Fprintln(out, r.EvidenceMeaning)
		for _, c := range r.CurrentClaims {
			fmt.Fprintf(out, "current %s: %s.%s = %s\n", c.ID, c.Entity, c.Predicate, c.Object)
		}
		for _, e := range r.Evidence {
			fmt.Fprintf(out, "source %s [%s, %s, %s]\n%s\n", e.EvidenceRef, e.VersionState, e.SnapshotTime, e.TextStatus, e.Text)
			for _, n := range e.Neighbors {
				fmt.Fprintf(out, "  context %s: %s\n", n.ChunkID, n.Text)
			}
		}
		for _, c := range r.Coverage {
			fmt.Fprintf(out, "coverage %s: %s (indexed %s)\n", c.Lane, c.Status, c.IndexedAt)
		}
	}
	if r.Status != "ok" {
		return 1
	}
	return 0
}
func encodeCLI(out, errout io.Writer, v any) int {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(errout, "cannot write recall report")
		return 1
	}
	return 0
}
