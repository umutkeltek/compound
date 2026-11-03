package mem

// lesson.go — the lesson claim: an agent-written "why + how" that PROMOTES ON
// WRITE, and the status rule that keeps machine status out of memory canon.
//
// WHY THIS EXISTS
// ===============
// Eight collect -> propose -> review loops all stopped at one unstaffed review
// step: tens of thousands of parked candidates, a handful of adjudications ever.
// The learning that did work bypassed that machinery: an agent that tripped on
// a problem wrote a dated why/how note and edited the skill it tripped on. It
// works because the writer is the adjudicator and the consumer is known.
//
// Operator ruling (2026-11-03, standing grant):
//   - an agent-written lesson with why, how and a date promotes immediately;
//   - status rows never promote;
//   - contradictions park, and the next consumer that records a use supersedes
//     the other;
//   - each lesson is also written into the skill that does the job (the CLI
//     owns that file write; this file owns the ledger write).
//
// Humans gate by setting the envelope, not by being paged: the lesson door is
// the envelope (why + how + actor + date are required, status is refused,
// contradictions are kept and marked instead of silently overwritten).

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
)

const (
	// ClaimTypeLesson is the claim taxonomy value for agent-written lessons. It is
	// deliberately NOT in ClaimTypes: a candidate cannot be captured as a lesson
	// and promoted later. Only WriteLesson mints lesson claims.
	ClaimTypeLesson = "lesson"
	// ClaimTypeStatus is machine status (git counts, deploy hosts, run output).
	ClaimTypeStatus = "status"
	// LessonDomain is the topic qualifier every lesson carries, so lesson
	// positions read as <entity>::lesson::slot-<hash>.
	LessonDomain = "lesson"
	// LessonSchemaVersion versions the `compound memory lesson` JSON receipt.
	LessonSchemaVersion = "compound.memory_lesson.v0.1"

	// LessonOutcomePromoted: a new lesson claim was appended and is current.
	LessonOutcomePromoted = "promoted"
	// LessonOutcomeDuplicate: the slot already holds this how; nothing written.
	LessonOutcomeDuplicate = "duplicate"
	// LessonOutcomeContested: the slot held a lesson with a different how. Both
	// are kept, each is marked contested by the other, the fold serves the newest.
	LessonOutcomeContested = "contested"

	// lessonTextMax bounds why/how. A lesson is one line; detail goes in evidence.
	lessonTextMax = 600
)

// ErrStatusNeverPromotes is the typed refusal for a status-typed candidate on a
// promotion path. Status is state, not a lesson: it changes on every run, it
// is already true in the system that produced it, and it crowded out the
// lessons (98% of the claim ledger was imported status rows).
var ErrStatusNeverPromotes = errors.New("status rows never promote: machine status is state, not a lesson (operator ruling 2026-11-03); keep it in the producing system or a state projection")

// StatusNeverPromotes returns a refusal when claimType is status. Every
// promotion path that serves learning calls it: the operator adjudicate door,
// the explicit-confirmation door, the governed promote driver (plan and
// frozen apply), and the source-control import. Existing status claims are
// history and are not rewritten.
func StatusNeverPromotes(id, claimType string) error {
	if NormalizeClaimValue(claimType) == ClaimTypeStatus {
		return fmt.Errorf("candidate %s: %w", strings.TrimSpace(id), ErrStatusNeverPromotes)
	}
	return nil
}

// LessonInput is one agent-written lesson.
type LessonInput struct {
	Why      string   // what went wrong or what was learned (the trap)
	How      string   // what to do instead (the fix); the slot's current value
	Skill    string   // the skill that does the job; also the default slot entity
	Trigger  string   // optional "<tool>:<regex>" the hook matches against a tool call
	Evidence []string // evidence refs (memory notes, work ids, commits, receipts)
	Slot     string   // optional explicit "entity::predicate"
	Actor    string   // who wrote it (required)
	Harness  string   // which harness the writer ran in (provenance)
	WorkID   string   // optional work id the lesson came from
}

// LessonResult is the receipt for one WriteLesson call.
type LessonResult struct {
	SchemaVersion string `json:"schema_version"`
	Outcome       string `json:"outcome"`
	Claim         Claim  `json:"claim"`
	PositionID    string `json:"position_id"`
	ContestedWith string `json:"contested_with,omitempty"`
	TransitionID  string `json:"transition_id,omitempty"`
}

// LessonTrigger is the parsed form of a trigger string.
//
// Format: "<tool>:<regex>". <tool> is the harness tool name the hook sees
// (Bash, Edit, Write, Read, ...) or "*" for any tool; it may not contain ':'.
// <regex> is a Go RE2 regular expression matched against the tool call's
// primary input (the command for Bash, the file path for Edit/Write/Read).
// Example: `Bash:proof-check.*\|.*jq` fires when a Bash command pipes
// proof-check output into jq.
type LessonTrigger struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern"`
}

var lessonTriggerTool = regexp.MustCompile(`^(\*|[A-Za-z][A-Za-z0-9_.-]*)$`)

// ParseLessonTrigger validates and splits a trigger. Empty input is no trigger.
func ParseLessonTrigger(raw string) (LessonTrigger, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return LessonTrigger{}, false, nil
	}
	tool, pattern, ok := strings.Cut(raw, ":")
	tool, pattern = strings.TrimSpace(tool), strings.TrimSpace(pattern)
	if !ok || tool == "" || pattern == "" {
		return LessonTrigger{}, false, fmt.Errorf("trigger %q must be <tool>:<regex>, e.g. Bash:proof-check.*jq", raw)
	}
	if !lessonTriggerTool.MatchString(tool) {
		return LessonTrigger{}, false, fmt.Errorf("trigger tool %q must be a tool name or *", tool)
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return LessonTrigger{}, false, fmt.Errorf("trigger regex %q does not compile: %w", pattern, err)
	}
	return LessonTrigger{Tool: tool, Pattern: pattern}, true, nil
}

// Matches reports whether the trigger fires for one tool call. It is the
// reference matcher for the hook that injects lessons.
func (t LessonTrigger) Matches(tool, input string) bool {
	if t.Tool != "*" && !strings.EqualFold(t.Tool, strings.TrimSpace(tool)) {
		return false
	}
	re, err := regexp.Compile(t.Pattern)
	return err == nil && re.MatchString(input)
}

// LessonSlot resolves the lesson's fact slot. An explicit slot is
// "entity::predicate". Otherwise the entity is skill:<name> (or "lessons") and
// the predicate is lesson.<hash of the normalized why>, so two lessons about
// the same trap land in one slot and a different how is a contradiction.
func LessonSlot(in LessonInput) (entity, predicate string, err error) {
	if raw := strings.TrimSpace(in.Slot); raw != "" {
		e, p, ok := strings.Cut(raw, "::")
		e, p = strings.TrimSpace(e), strings.TrimSpace(p)
		if !ok || e == "" || p == "" || strings.Contains(p, "::") {
			return "", "", fmt.Errorf("slot %q must be entity::predicate", raw)
		}
		return e, p, nil
	}
	entity = "lessons"
	if skill := strings.TrimSpace(in.Skill); skill != "" {
		entity = "skill:" + skill
	}
	return entity, "lesson." + shortHash(NormalizeClaimValue(in.Why)), nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func normalizeLessonInput(in LessonInput) (LessonInput, error) {
	in.Why, in.How = oneLine(in.Why), oneLine(in.How)
	in.Skill, in.Actor, in.Harness = strings.TrimSpace(in.Skill), strings.TrimSpace(in.Actor), strings.TrimSpace(in.Harness)
	in.Trigger, in.WorkID = strings.TrimSpace(in.Trigger), strings.TrimSpace(in.WorkID)
	switch {
	case in.Why == "":
		return in, fmt.Errorf("lesson needs --why (what went wrong or was learned)")
	case in.How == "":
		return in, fmt.Errorf("lesson needs --how (what to do instead)")
	case in.Actor == "":
		return in, fmt.Errorf("lesson needs --by <actor>")
	case len(in.Why) > lessonTextMax || len(in.How) > lessonTextMax:
		return in, fmt.Errorf("lesson why/how must each be at most %d characters; put detail behind --evidence", lessonTextMax)
	}
	if in.Skill != "" && strings.ContainsAny(in.Skill, `/\`+"\x00") {
		return in, fmt.Errorf("skill %q must be a bare skill name", in.Skill)
	}
	if _, _, err := ParseLessonTrigger(in.Trigger); err != nil {
		return in, err
	}
	evidence := make([]string, 0, len(in.Evidence))
	for _, ref := range in.Evidence {
		if ref = strings.TrimSpace(ref); ref != "" {
			evidence = append(evidence, ref)
		}
	}
	in.Evidence = evidence
	return in, nil
}

// WriteLesson validates a lesson and promotes it on write: one immutable claim
// (claim_type lesson, valid_from now, promotion_basis lesson_on_write) plus its
// memory_delta Transition, under the memory lock. No candidate and no
// adjudication step.
//
// Slot rules: the same how again is a duplicate (nothing written). A different
// how parks the contradiction: both claims stay current, each gets a
// needs_review edge naming the other (contested_by), so the position fold
// serves the newest with status "contested" and default recall holds both
// until a consumer resolves it (ResolveContestedLessonOnUse).
func (s *Store) WriteLesson(in LessonInput, now time.Time) (LessonResult, error) {
	res := LessonResult{SchemaVersion: LessonSchemaVersion}
	in, err := normalizeLessonInput(in)
	if err != nil {
		return res, err
	}
	entity, predicate, err := LessonSlot(in)
	if err != nil {
		return res, err
	}
	trigger, hasTrigger, _ := ParseLessonTrigger(in.Trigger)
	now = now.UTC()
	qualifiers := map[string]string{
		"domain":     LessonDomain,
		"why":        in.Why,
		"written_by": in.Actor,
		"written_on": now.Format("2006-01-02"),
	}
	if in.Skill != "" {
		qualifiers["skill"] = in.Skill
	}
	if in.Harness != "" {
		qualifiers["harness"] = in.Harness
	}
	if hasTrigger {
		qualifiers["trigger"] = trigger.Tool + ":" + trigger.Pattern
	}
	if in.WorkID != "" {
		qualifiers["work_id"] = in.WorkID
	}
	slotKey := ClaimSlotKey(entity, predicate, ClaimTypeLesson, qualifiers, "")

	err = s.withLock(func() error {
		claims, err := s.loadClaimsRaw()
		if err != nil {
			return err
		}
		maxSeq, err := s.maxLedgerSeq()
		if err != nil {
			return err
		}
		superseded, err := s.supersededAsOf(maxSeq)
		if err != nil {
			return err
		}
		var current []Claim
		for _, c := range claims {
			if c.ClaimType != ClaimTypeLesson || superseded[c.ID] {
				continue
			}
			if ClaimSlotKey(c.Entity, c.Predicate, c.ClaimType, c.Qualifiers, c.ID) == slotKey {
				current = append(current, c)
			}
		}
		sort.Slice(current, func(i, j int) bool { return current[i].Seq < current[j].Seq })
		for _, c := range current {
			if NormalizeClaimValue(c.Object) == NormalizeClaimValue(in.How) {
				res.Outcome = LessonOutcomeDuplicate
				res.Claim = c
				return nil
			}
		}
		var contested Claim
		if len(current) > 0 {
			contested = current[len(current)-1]
		}

		seq, err := s.nextSeq()
		if err != nil {
			return err
		}
		evidence := append([]string{"lesson-writer:" + in.Actor}, in.Evidence...)
		if contested.ID != "" {
			evidence = append(evidence, "contested_by:"+contested.ID)
		}
		claim := Claim{
			ID:             idFor("claim_", entity, predicate, in.How, fmt.Sprint(seq)),
			Seq:            seq,
			Entity:         entity,
			Predicate:      predicate,
			Object:         in.How,
			ValidFrom:      rfc3339(now),
			Confidence:     0.9,
			Derivation:     "inferred",
			ProvenanceLane: in.Harness,
			ClaimType:      ClaimTypeLesson,
			Qualifiers:     qualifiers,
			PromotionBasis: candidates.BasisLessonOnWrite,
			EvidenceRefs:   uniqueStrings(evidence),
			Visibility:     "contextual",
			CreatedAt:      rfc3339(now),
		}
		if err := appendJSONL(s.claimsLog(), &claim); err != nil {
			return err
		}
		if contested.ID != "" {
			// Park the contradiction: append-only marks in both directions. A
			// needs_review edge From=X To=Y reads "Y is contested by X".
			for _, pair := range [][2]string{{claim.ID, contested.ID}, {contested.ID, claim.ID}} {
				edgeSeq, err := s.nextSeq()
				if err != nil {
					return err
				}
				edge := Edge{
					Seq: edgeSeq, Kind: EdgeKindNeedsReview, From: pair[0], To: pair[1], Confidence: claim.Confidence,
					EvidenceRefs: []string{"lesson-contradiction", "contested_by:" + pair[0]},
					CreatedAt:    rfc3339(now),
				}
				if err := appendJSONL(s.edgesLog(), &edge); err != nil {
					return err
				}
			}
			res.ContestedWith = contested.ID
		}
		transition := s.memoryTransition(claim, Claim{}, false, in.WorkID, now)
		if err := transition.Validate(); err != nil {
			return fmt.Errorf("lesson transition invalid: %w", err)
		}
		if err := s.writePromotionTransition(&transition); err != nil {
			return err
		}
		res.TransitionID = transition.ID
		res.Claim = claim
		res.Outcome = LessonOutcomePromoted
		if contested.ID != "" {
			res.Outcome = LessonOutcomeContested
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	res.PositionID = positionIDForClaim(res.Claim)
	return res, nil
}

// LessonSkillLine renders the one-line, dated entry the lesson door appends to
// the named skill's "## Lessons" section.
func LessonSkillLine(claim Claim) string {
	date := strings.TrimSpace(claim.Qualifiers["written_on"])
	if date == "" && len(claim.ValidFrom) >= 10 {
		date = claim.ValidFrom[:10]
	}
	line := fmt.Sprintf("- %s: %s. How: %s (lesson %s", date, strings.TrimSuffix(claim.Qualifiers["why"], "."), claim.Object, claim.ID)
	if trigger := claim.Qualifiers["trigger"]; trigger != "" {
		line += ", trigger `" + strings.ReplaceAll(trigger, "`", "'") + "`"
	}
	return line + ")"
}

// LessonUseResult reports what recording a use of a lesson changed.
type LessonUseResult struct {
	ClaimID    string   `json:"claim_id"`
	Outcome    string   `json:"outcome"` // not_lesson | uncontested | superseded_contested
	Superseded []string `json:"superseded,omitempty"`
}

// ResolveContestedLessonOnUse is the consumer-supersedes step. When a consumer
// records that it USED lesson claimID (for example `work close --used
// <claim_id>`) and that lesson is contested, the used lesson wins: each
// contesting lesson in the same slot gets a supersedes edge from the used one,
// and the contested marks on both sides are cleared. Uncontested lessons and
// non-lesson refs are a no-op.
func (s *Store) ResolveContestedLessonOnUse(claimID, actor, workID string, now time.Time) (LessonUseResult, error) {
	claimID = strings.TrimSpace(claimID)
	res := LessonUseResult{ClaimID: claimID, Outcome: "not_lesson"}
	err := s.withLock(func() error {
		claims, err := s.loadClaimsRaw()
		if err != nil {
			return err
		}
		byID := map[string]Claim{}
		for _, c := range claims {
			byID[c.ID] = c
		}
		used, ok := byID[claimID]
		if !ok || used.ClaimType != ClaimTypeLesson {
			return nil
		}
		maxSeq, err := s.maxLedgerSeq()
		if err != nil {
			return err
		}
		superseded, err := s.supersededAsOf(maxSeq)
		if err != nil {
			return err
		}
		res.Outcome = "uncontested"
		if superseded[used.ID] {
			return nil
		}
		marks, err := s.claimReviewMarksAsOf(0)
		if err != nil {
			return err
		}
		usedSlot := ClaimSlotKey(used.Entity, used.Predicate, used.ClaimType, used.Qualifiers, used.ID)
		peers := map[string]bool{}
		consider := func(id string) {
			peer, ok := byID[id]
			if !ok || id == used.ID || peer.ClaimType != ClaimTypeLesson || superseded[id] {
				return
			}
			if ClaimSlotKey(peer.Entity, peer.Predicate, peer.ClaimType, peer.Qualifiers, peer.ID) == usedSlot {
				peers[id] = true
			}
		}
		for _, ref := range marks[used.ID].Refs {
			consider(ref)
		}
		for target, mark := range marks {
			for _, ref := range mark.Refs {
				if ref == used.ID {
					consider(target)
				}
			}
		}
		if len(peers) == 0 {
			return nil
		}
		ids := make([]string, 0, len(peers))
		for id := range peers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		evidence := []string{"lesson-used", "used_by:" + orDef(strings.TrimSpace(actor), "unknown")}
		if strings.TrimSpace(workID) != "" {
			evidence = append(evidence, "work:"+strings.TrimSpace(workID))
		}
		appendEdge := func(kind, from, to string) error {
			seq, err := s.nextSeq()
			if err != nil {
				return err
			}
			edge := Edge{Seq: seq, Kind: kind, From: from, To: to, Confidence: used.Confidence, EvidenceRefs: evidence, CreatedAt: rfc3339(now.UTC())}
			return appendJSONL(s.edgesLog(), &edge)
		}
		for _, peer := range ids {
			if err := appendEdge(EdgeKindSupersedes, used.ID, peer); err != nil {
				return err
			}
			if err := appendEdge(EdgeKindReviewCleared, peer, used.ID); err != nil {
				return err
			}
			if err := appendEdge(EdgeKindReviewCleared, used.ID, peer); err != nil {
				return err
			}
		}
		res.Outcome = "superseded_contested"
		res.Superseded = ids
		return nil
	})
	return res, err
}
