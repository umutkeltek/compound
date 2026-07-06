package mem

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const PositionFoldSchemaVersion = "compound.memory_position_fold.v0.1"

type PositionFoldOptions struct {
	Entity  string
	Topic   string
	AsOfSeq int64
}

type PositionFoldReport struct {
	SchemaVersion string     `json:"schema_version"`
	FoldedAt      string     `json:"folded_at"`
	AsOfSeq       int64      `json:"as_of_seq"`
	Entity        string     `json:"entity,omitempty"`
	Topic         string     `json:"topic,omitempty"`
	TopicSubtree  []string   `json:"topic_subtree,omitempty"`
	Count         int        `json:"count"`
	Positions     []Position `json:"positions"`
}

type PositionDetail struct {
	Position Position       `json:"position"`
	Claims   []Claim        `json:"claims"`
	Evidence []EvidenceItem `json:"evidence,omitempty"`
	Sources  []string       `json:"sources,omitempty"`
}

type PositionQueryResult struct {
	SchemaVersion string           `json:"schema_version"`
	Entity        string           `json:"entity"`
	AsOfSeq       int64            `json:"as_of_seq"`
	Topic         string           `json:"topic,omitempty"`
	TopicSubtree  []string         `json:"topic_subtree,omitempty"`
	Positions     []PositionDetail `json:"positions"`
	Coverage      string           `json:"coverage"`
}

func (s *Store) positionsLog() string { return s.home + "/ledger/positions.jsonl" }

func (s *Store) FoldPositions(opts PositionFoldOptions, now time.Time) (PositionFoldReport, error) {
	positions, asOf, subtree, err := s.derivePositions(opts, now)
	if err != nil {
		return PositionFoldReport{}, err
	}
	if err := writePositionsLog(s.positionsLog(), positions); err != nil {
		return PositionFoldReport{}, err
	}
	return PositionFoldReport{
		SchemaVersion: PositionFoldSchemaVersion,
		FoldedAt:      rfc3339(now),
		AsOfSeq:       asOf,
		Entity:        strings.TrimSpace(opts.Entity),
		Topic:         strings.TrimSpace(opts.Topic),
		TopicSubtree:  subtree,
		Count:         len(positions),
		Positions:     positions,
	}, nil
}

func (s *Store) LoadFoldedPositions() ([]Position, error) {
	var out []Position
	err := readJSONL(s.positionsLog(), func(line []byte) error {
		var p Position
		if err := json.Unmarshal(line, &p); err != nil {
			return err
		}
		out = append(out, p)
		return nil
	})
	return out, err
}

func (s *Store) Position(entity string, opts PositionFoldOptions) (PositionQueryResult, error) {
	opts.Entity = strings.TrimSpace(entity)
	positions, asOf, subtree, err := s.derivePositions(opts, time.Time{})
	if err != nil {
		return PositionQueryResult{Entity: opts.Entity}, err
	}
	claims, err := s.loadClaimsAsOf(asOf)
	if err != nil {
		return PositionQueryResult{Entity: opts.Entity}, err
	}
	claimByID := map[string]Claim{}
	for _, claim := range claims {
		claimByID[claim.ID] = claim
	}
	evidence, err := s.Evidence()
	if err != nil {
		return PositionQueryResult{Entity: opts.Entity}, err
	}
	evidenceByID := map[string]EvidenceItem{}
	for _, item := range evidence {
		evidenceByID[item.ID] = item
	}
	resultEntity := opts.Entity
	if len(positions) > 0 {
		resultEntity = positions[0].Entity
	}
	result := PositionQueryResult{
		SchemaVersion: PositionFoldSchemaVersion,
		Entity:        resultEntity,
		AsOfSeq:       asOf,
		Topic:         strings.TrimSpace(opts.Topic),
		TopicSubtree:  subtree,
	}
	for _, p := range positions {
		detail := PositionDetail{Position: p}
		for _, claimID := range p.ClaimChain {
			claim, ok := claimByID[claimID]
			if !ok {
				continue
			}
			detail.Claims = append(detail.Claims, claim)
			for _, ref := range claim.EvidenceRefs {
				if item, ok := evidenceByID[ref]; ok {
					detail.Evidence = append(detail.Evidence, item)
					if item.SourceID != "" {
						detail.Sources = append(detail.Sources, item.SourceID)
					}
				} else if ref != "" {
					detail.Sources = append(detail.Sources, ref)
				}
			}
		}
		detail.Sources = normalizeStringRefs(detail.Sources)
		result.Positions = append(result.Positions, detail)
	}
	switch {
	case len(result.Positions) > 0:
		result.Coverage = fmt.Sprintf("%d position(s) for %q as of seq %d", len(result.Positions), resultEntity, asOf)
	case strings.TrimSpace(opts.Topic) != "":
		result.Coverage = fmt.Sprintf("no positions for %q under topic %q as of seq %d", resultEntity, opts.Topic, asOf)
	default:
		result.Coverage = fmt.Sprintf("no positions for %q as of seq %d", resultEntity, asOf)
	}
	return result, nil
}

func (s *Store) Positions() ([]Position, error) {
	positions, _, _, err := s.derivePositions(PositionFoldOptions{}, time.Time{})
	return positions, err
}

func (s *Store) derivePositions(opts PositionFoldOptions, now time.Time) ([]Position, int64, []string, error) {
	claims, err := s.loadClaimsAsOf(opts.AsOfSeq)
	if err != nil {
		return nil, 0, nil, err
	}
	asOf := opts.AsOfSeq
	if asOf == 0 {
		asOf, err = s.maxLedgerSeq()
		if err != nil {
			return nil, 0, nil, err
		}
	}
	superseded, err := s.supersededAsOf(asOf)
	if err != nil {
		return nil, 0, nil, err
	}
	topicSet, subtree, err := s.topicScopeSet(opts.Topic)
	if err != nil {
		return nil, 0, nil, err
	}
	foldedAt := ""
	if !now.IsZero() {
		foldedAt = rfc3339(now)
	}
	type key struct{ entity, topic string }
	groups := map[key][]Claim{}
	var order []key
	entity := strings.TrimSpace(opts.Entity)
	if entity != "" {
		entity, err = s.canonicalRecallEntity(entity, claims, false)
		if err != nil {
			return nil, 0, nil, err
		}
	}
	for _, c := range claims {
		if c.Seq > asOf {
			continue
		}
		if entity != "" && c.Entity != entity {
			continue
		}
		if len(topicSet) > 0 && !claimHasTopicRef(c, topicSet) {
			continue
		}
		k := key{c.Entity, c.Topic()}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], c)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].entity != order[j].entity {
			return order[i].entity < order[j].entity
		}
		return order[i].topic < order[j].topic
	})
	out := make([]Position, 0, len(order))
	for _, k := range order {
		cs := groups[k]
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].Seq == cs[j].Seq {
				return cs[i].ID < cs[j].ID
			}
			return cs[i].Seq < cs[j].Seq
		})
		position, ok := buildPosition(k.entity, k.topic, cs, superseded, asOf, foldedAt)
		if ok {
			out = append(out, position)
		}
	}
	return out, asOf, subtree, nil
}

func buildPosition(entity, topic string, claims []Claim, superseded map[string]bool, asOf int64, foldedAt string) (Position, bool) {
	chain := make([]string, 0, len(claims))
	supporting := []string{}
	history := []string{}
	reviewRefs := []string{}
	evidenceRefs := []string{}
	topicRefs := []string{}
	var active Claim
	found := false
	contested := false
	predicate := ""
	for _, claim := range claims {
		chain = append(chain, claim.ID)
		if predicate == "" {
			predicate = claim.Predicate
		}
		evidenceRefs = append(evidenceRefs, claim.EvidenceRefs...)
		topicRefs = append(topicRefs, claim.TopicRefs...)
		if claim.State == ClaimStateNeedsReview {
			contested = true
			reviewRefs = append(reviewRefs, claim.ReviewRefs...)
		}
		if superseded[claim.ID] {
			history = append(history, claim.ID)
			continue
		}
		active, found = claim, true
		supporting = []string{claim.ID}
	}
	if !found {
		return Position{}, false
	}
	status := "active"
	if active.Visibility == "retired" {
		status = "retired"
	}
	if contested {
		status = "contested"
	}
	return Position{
		ID:                    entity + "::" + topic,
		Entity:                entity,
		Topic:                 topic,
		Predicate:             predicate,
		ActiveClaimID:         active.ID,
		ActiveObject:          active.Object,
		Status:                status,
		SupportingClaimRefs:   normalizeStringRefs(supporting),
		SupersededHistoryRefs: normalizeStringRefs(history),
		ClaimChain:            chain,
		ReviewRefs:            normalizeStringRefs(reviewRefs),
		EvidenceRefs:          normalizeStringRefs(evidenceRefs),
		TopicRefs:             normalizeStringRefs(topicRefs),
		FoldedAt:              foldedAt,
		AsOfSeq:               asOf,
	}, true
}

func (s *Store) topicScopeSet(topic string) (map[string]bool, []string, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, nil, nil
	}
	subtree, err := s.TopicSubtree(topic)
	if err != nil {
		return nil, nil, err
	}
	set := map[string]bool{}
	for _, slug := range subtree {
		set[slug] = true
	}
	return set, subtree, nil
}

func claimHasTopicRef(claim Claim, topicSet map[string]bool) bool {
	for _, ref := range claim.TopicRefs {
		if topicSet[ref] {
			return true
		}
	}
	return false
}

func writePositionsLog(path string, positions []Position) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, position := range positions {
		if err := enc.Encode(position); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
