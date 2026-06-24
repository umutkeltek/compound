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
func ToLedger(l *ledger.Ledger, decisions []Decision, sessionPath, primaryScope string) (int, error) {
	now := time.Now().UTC()
	src := sessionBase(sessionPath)
	n := 0
	for idx, d := range decisions {
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
		if err := l.Append("evidence", ev); err != nil {
			return n, err
		}
		subject := primaryScope
		if subject == "" {
			subject = scopeFromDomain(d.Domain)
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
		}
		if d.Domain != "" {
			claim.Qualifiers = map[string]any{"domain": d.Domain}
		}
		if err := l.Append("claims", claim); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
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
