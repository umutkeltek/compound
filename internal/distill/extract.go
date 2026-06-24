package distill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/umutkeltek/compound/internal/ledger"
	"github.com/umutkeltek/compound/internal/memory"
)

// Decision is one durable, sourced position from the decision-ledger extract.
type Decision struct {
	Statement  string `json:"statement"`
	Confidence string `json:"confidence"`
	Domain     string `json:"domain"`
	Quote      string `json:"quote"`
}

// ExtractDecisions runs the authority-laundering-guarded extract over the final
// distillation and returns durable decisions only.
func ExtractDecisions(ctx context.Context, model, finalDistillation string) ([]Decision, error) {
	llm, err := newLLM(model)
	if err != nil {
		return nil, err
	}
	content, _, err := llm.call(ctx, ledgerExtractSystem, finalDistillation)
	if err != nil {
		return nil, err
	}
	return parseDecisions(content)
}

func parseDecisions(s string) ([]Decision, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	i := strings.IndexByte(s, '[')
	j := strings.LastIndexByte(s, ']')
	if i < 0 || j < i {
		return nil, fmt.Errorf("no JSON array in extract output")
	}
	var ds []Decision
	if err := json.Unmarshal([]byte(s[i:j+1]), &ds); err != nil {
		return nil, err
	}
	return ds, nil
}

func confToFloat(c string) float64 {
	switch strings.ToLower(c) {
	case "high":
		return 0.9
	case "low":
		return 0.5
	default:
		return 0.7
	}
}

// ToLedger appends an EvidenceItem (the verbatim quote) plus a CANDIDATE Claim per
// decision. Claims enter as candidates — promotion is a separate gate, never the
// distiller's call (preserves evidence ≠ canon). primaryScope (from the scope
// resolver, passed by the caller to avoid an import cycle) sets the subject entity
// when resolved; otherwise the coarse domain slug is used.
func ToLedger(l *ledger.Ledger, decisions []Decision, sessionPath, primaryScope string) (added, dropped int, err error) {
	now := time.Now().UTC()
	src := sessionBase(sessionPath)
	existing, _ := loadClaimTokens(l) // (entity::topic) -> token sets, for dedup (NONE)
	for idx, d := range decisions {
		subject := primaryScope
		if subject == "" {
			subject = scopeFromDomain(d.Domain)
		}
		topic := d.Domain
		if topic == "" {
			topic = "general"
		}
		// Key dedup on the ENTITY (not the LLM-assigned, volatile topic slug) so a
		// near-identical statement is caught regardless of how this run sliced topics.
		// This is the cheap EXACT/near-exact pass; semantic paraphrase dedup is the
		// LLM reconcile's job (mem0's reason for using an LLM here).
		nw := tokenize(d.Statement)
		if isDuplicate(existing[subject], nw) {
			dropped++ // alive-memory NONE: a near-identical claim already exists
			continue
		}
		existing[subject] = append(existing[subject], nw) // dedup within this batch too

		evID := fmt.Sprintf("ev_%s_%03d", src, idx)
		ev := memory.EvidenceItem{
			ID:          evID,
			SourceKind:  "session",
			SourceID:    src,
			SessionID:   src,
			QuoteText:   d.Quote,
			ContentHash: hashHex(d.Quote),
			ObservedAt:  now,
			Actor:       memory.ActorUser,
			Visibility:  memory.VisWorld,
			Provenance:  "distill:" + src,
		}
		if err = l.Append("evidence", ev); err != nil {
			return added, dropped, err
		}
		claim := memory.Claim{
			ID:            fmt.Sprintf("cl_%s_%03d", src, idx),
			SubjectEntity: subject,
			Predicate:     "decided",
			Object:        d.Statement,
			ClaimType:     memory.ClaimDecision,
			Confidence:    confToFloat(d.Confidence),
			Visibility:    memory.VisWorld,
			Status:        memory.StatusCandidate,
			EvidenceRefs:  []string{evID},
			CreatedBy:     "distiller",
			Qualifiers:    map[string]any{"domain": topic},
		}
		if err = l.Append("claims", claim); err != nil {
			return added, dropped, err
		}
		added++
	}
	return added, dropped, nil
}

// --- deterministic dedup (mem0 "NONE" / gbrain no-dup) ---

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

func jaccard(a, b []string) float64 {
	sa := make(map[string]bool, len(a))
	for _, w := range a {
		sa[w] = true
	}
	sb := make(map[string]bool, len(b))
	for _, w := range b {
		sb[w] = true
	}
	inter := 0
	for w := range sa {
		if sb[w] {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func isDuplicate(existing [][]string, nw []string) bool {
	for _, ex := range existing {
		if jaccard(ex, nw) >= 0.82 {
			return true
		}
	}
	return false
}

func loadClaimTokens(l *ledger.Ledger) (map[string][][]string, error) {
	m := map[string][][]string{}
	err := l.Each("claims", func(b []byte) error {
		var c memory.Claim
		if json.Unmarshal(b, &c) != nil {
			return nil
		}
		m[c.SubjectEntity] = append(m[c.SubjectEntity], tokenize(c.Object))
		return nil
	})
	return m, err
}

func sessionBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return strings.TrimSuffix(p, ".jsonl")
}

func hashHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// scopeFromDomain is a placeholder entity until the scope resolver assigns a real
// Entity. Distiller domains are coarse topic slugs, not resolved entities.
func scopeFromDomain(d string) string {
	if d == "" {
		return "unscoped"
	}
	return d
}
