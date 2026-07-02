package breadth

import (
	"encoding/json"
	"io"
	"math"
	"sort"
	"strings"
	"unicode"

	"compound/internal/mem"
)

const (
	Name            = "compound-breadth"
	DefaultEmbedder = "compound-breadth:lexical:0:0"
	k1              = 1.2
	b               = 0.75
)

type Engine struct {
	claims []mem.ClaimText
	docs   []doc
	gen    int64
	ready  bool
}

type doc struct {
	claim  mem.ClaimText
	tokens []string
	freq   map[string]int
}

func (e *Engine) CorpusGen() int64 { return e.gen }
func (e *Engine) Ready() bool      { return e.ready }

func (e *Engine) Index(claims []mem.ClaimText, gen int64) int {
	if e.ready && e.gen == gen {
		return 0
	}
	e.claims = append([]mem.ClaimText(nil), claims...)
	e.docs = make([]doc, 0, len(claims))
	for _, c := range claims {
		tokens := Tokenize(c.Text)
		freq := make(map[string]int, len(tokens))
		for _, tok := range tokens {
			freq[tok]++
		}
		e.docs = append(e.docs, doc{claim: c, tokens: tokens, freq: freq})
	}
	e.gen = gen
	e.ready = true
	return len(claims)
}

func (e *Engine) Query(text, gate string, allowlist []string, k int) []mem.Hit {
	if k <= 0 || !e.ready {
		return nil
	}
	allowed := allowSet(gate, allowlist)
	allowedDocs := make([]doc, 0, len(e.docs))
	for _, d := range e.docs {
		if gate != mem.GateAll && !allowed[d.claim.ClaimID] {
			continue
		}
		allowedDocs = append(allowedDocs, d)
	}
	if len(allowedDocs) == 0 {
		return nil
	}

	bm25 := e.bm25(text, allowedDocs)
	reference := referenceContains(text, allowedDocs)
	scores := reciprocalRankFusion([]rankedLane{{weight: 1, hits: bm25}})
	out := make([]mem.Hit, 0, len(scores)+len(reference))
	seen := make(map[string]bool, len(scores)+len(reference))
	for _, h := range reference {
		if seen[h.ClaimID] {
			continue
		}
		seen[h.ClaimID] = true
		out = append(out, h)
	}
	extras := make([]mem.Hit, 0, len(scores))
	for id, score := range scores {
		if seen[id] {
			continue
		}
		extras = append(extras, mem.Hit{ClaimID: id, Score: score})
	}
	sort.SliceStable(extras, func(i, j int) bool {
		if extras[i].Score == extras[j].Score {
			return extras[i].ClaimID < extras[j].ClaimID
		}
		return extras[i].Score > extras[j].Score
	})
	out = append(out, extras...)
	if len(out) > k {
		out = out[:k]
	}
	return out
}

func (e *Engine) bm25(text string, docs []doc) []mem.Hit {
	queryTerms := unique(Tokenize(text))
	if len(queryTerms) == 0 {
		return nil
	}
	avgLen := 0.0
	for _, d := range docs {
		avgLen += float64(len(d.tokens))
	}
	avgLen /= float64(len(docs))
	df := make(map[string]int, len(queryTerms))
	for _, term := range queryTerms {
		for _, d := range docs {
			if d.freq[term] > 0 {
				df[term]++
			}
		}
	}
	hits := make([]mem.Hit, 0, len(docs))
	n := float64(len(docs))
	for _, d := range docs {
		score := 0.0
		dl := float64(len(d.tokens))
		if dl == 0 {
			continue
		}
		for _, term := range queryTerms {
			tf := float64(d.freq[term])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[term])+0.5)/(float64(df[term])+0.5))
			denom := tf + k1*(1-b+b*dl/avgLen)
			score += idf * (tf * (k1 + 1) / denom)
		}
		if score > 0 {
			hits = append(hits, mem.Hit{ClaimID: d.claim.ClaimID, Score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	return hits
}

type rankedLane struct {
	weight float64
	hits   []mem.Hit
}

func reciprocalRankFusion(lanes []rankedLane) map[string]float64 {
	out := map[string]float64{}
	for _, lane := range lanes {
		if lane.weight <= 0 {
			continue
		}
		for rank, h := range lane.hits {
			out[h.ClaimID] += lane.weight / (60 + float64(rank+1))
		}
	}
	return out
}

func referenceContains(text string, docs []doc) []mem.Hit {
	terms := strings.Fields(strings.ToLower(text))
	if len(terms) == 0 {
		return nil
	}
	hits := make([]mem.Hit, 0, len(docs))
	for _, d := range docs {
		hay := strings.ToLower(d.claim.Text)
		score := 0.0
		for _, term := range terms {
			if strings.Contains(hay, term) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, mem.Hit{ClaimID: d.claim.ClaimID, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].ClaimID < hits[j].ClaimID
		}
		return hits[i].Score > hits[j].Score
	})
	return hits
}

func allowSet(gate string, allowlist []string) map[string]bool {
	if gate == mem.GateAll {
		return nil
	}
	allow := make(map[string]bool, len(allowlist))
	for _, id := range allowlist {
		allow[id] = true
	}
	return allow
}

func unique(tokens []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok == "" || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

func Tokenize(text string) []string {
	var out []string
	for _, chunk := range alnumChunks(text) {
		out = append(out, splitIdentifier(chunk)...)
	}
	return out
}

func alnumChunks(text string) []string {
	var chunks []string
	var cur []rune
	flush := func() {
		if len(cur) == 0 {
			return
		}
		chunks = append(chunks, string(cur))
		cur = cur[:0]
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	return chunks
}

func splitIdentifier(s string) []string {
	rs := []rune(s)
	var parts []string
	start := 0
	for i := 1; i < len(rs); i++ {
		prev, cur := rs[i-1], rs[i]
		var next rune
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		if unicode.IsUpper(cur) && (unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && next != 0 && unicode.IsLower(next))) {
			parts = append(parts, strings.ToLower(string(rs[start:i])))
			start = i
		}
	}
	parts = append(parts, strings.ToLower(string(rs[start:])))
	return parts
}

func RunProtocol(in io.Reader, out io.Writer, embedder string) error {
	if embedder == "" {
		embedder = DefaultEmbedder
	}
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	var engine Engine
	for {
		var req mem.Request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		resp := mem.Response{RequestID: req.RequestID, SchemaVersion: mem.ProtocolVersion}
		if req.SchemaVersion != 0 && req.SchemaVersion != mem.ProtocolVersion {
			resp.OK, resp.Error = false, &mem.WireError{Code: mem.CodeBadRequest, Message: "unsupported schema_version"}
			if err := enc.Encode(&resp); err != nil {
				return err
			}
			continue
		}
		switch req.Op {
		case mem.OpCapabilities:
			resp.OK, resp.Name, resp.Breadth, resp.Embedder, resp.Ready, resp.CorpusGen = true, Name, true, embedder, engine.Ready(), engine.CorpusGen()
		case mem.OpIndex:
			resp.OK, resp.Indexed, resp.CorpusGen = true, engine.Index(req.Claims, req.CorpusGen), engine.CorpusGen()
		case mem.OpQuery:
			if !engine.Ready() {
				resp.OK = false
				resp.Error = &mem.WireError{Code: mem.CodeIndexMissing, Message: "index before query"}
			} else {
				resp.OK, resp.Hits, resp.CorpusGen = true, engine.Query(req.Text, req.Gate, req.Allowlist, req.K), engine.CorpusGen()
			}
		default:
			resp.OK = false
			resp.Error = &mem.WireError{Code: mem.CodeBadRequest, Message: "unknown op"}
		}
		if err := enc.Encode(&resp); err != nil {
			return err
		}
	}
}
