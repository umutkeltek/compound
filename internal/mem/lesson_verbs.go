package mem

// lesson_verbs.go — retire, amend and restore one lesson in one step.
//
// "Live at once without the owner" is only safe if any agent can undo a lesson
// in one command. Each verb appends a new version of the lesson claim, in the
// same slot, that supersedes the head of the lesson's version chain, plus a
// memory_delta Transition, under the memory lock. Nothing is deleted: the
// chain is the history. Each verb is undone by its opposite (retire <->
// restore; an amend by amending back). No carrier work item and no owner
// provenance are needed: the actor that runs the verb is recorded on the new
// version as the one who changed the lesson.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// CooledReasonLessonRetired marks a lesson version retired by
	// `compound memory lesson retire`.
	CooledReasonLessonRetired = "lesson_retired"

	// LessonChangeRetired, LessonChangeAmended and LessonChangeRestored name
	// what a lesson verb did; the new version carries it as qualifier "change".
	LessonChangeRetired  = "retired"
	LessonChangeAmended  = "amended"
	LessonChangeRestored = "restored"

	// LessonChangeSchemaVersion versions the retire/amend/restore receipt.
	LessonChangeSchemaVersion = "compound.memory_lesson_change.v0.1"

	// Qualifiers a changed version carries. They are not slot identity: the
	// slot key reads entity, domain, predicate and claim type only.
	lessonChangeKey       = "change"
	lessonChangedByKey    = "changed_by"
	lessonChangedOnKey    = "changed_on"
	lessonChangeReasonKey = "change_reason"
)

// LessonChangeInput is one retire, amend or restore request.
type LessonChangeInput struct {
	ID          string // any claim id in the lesson's version chain
	Actor       string // who changes it (required; recorded as changed_by)
	Reason      string // why (required for retire)
	How         string // amend: the new how; empty keeps the current one
	Trigger     string // amend: the new trigger; empty keeps the current one
	DropTrigger bool   // amend: remove the trigger (the lesson stays for recall)
	// AllowBroadTrigger keeps a new trigger that matches ordinary calls.
	AllowBroadTrigger bool
	WorkID            string // optional work id for the Transition
}

// LessonChangeResult is the receipt of one lesson verb.
type LessonChangeResult struct {
	SchemaVersion string `json:"schema_version"`
	Change        string `json:"change"`
	// Prior is the version the verb replaced; Claim is the new version.
	Prior Claim `json:"prior"`
	Claim Claim `json:"claim"`
	// ClearedContest lists lessons whose contest with the retired one was
	// cleared: they stand alone in the slot now.
	ClearedContest []string `json:"cleared_contest,omitempty"`
	// ContestedWith names the current lesson a restored one contradicts.
	ContestedWith string `json:"contested_with,omitempty"`
	TransitionID  string `json:"transition_id"`
	// SkippedLedgerRows counts malformed claims rows the change read past.
	SkippedLedgerRows int    `json:"skipped_ledger_rows,omitempty"`
	PositionID        string `json:"position_id"`
}

func lessonRetired(c Claim) bool {
	return strings.EqualFold(strings.TrimSpace(c.Visibility), "retired")
}

// RetireLesson stops a lesson from being served or recalled. The new version
// has visibility retired and records who retired it and why. A contested
// lesson's marks are cleared, so the lesson it contradicted stands alone.
func (s *Store) RetireLesson(in LessonChangeInput, now time.Time) (LessonChangeResult, error) {
	in, err := normalizeLessonChangeInput(in)
	if err != nil {
		return LessonChangeResult{}, err
	}
	if in.Reason == "" {
		return LessonChangeResult{}, fmt.Errorf("retire needs --reason (why the lesson no longer holds)")
	}
	return s.changeLesson(in, LessonChangeRetired, now)
}

// AmendLesson replaces a lesson's how and/or trigger. The old version stays
// in the chain as history; amending back undoes it.
func (s *Store) AmendLesson(in LessonChangeInput, now time.Time) (LessonChangeResult, error) {
	in, err := normalizeLessonChangeInput(in)
	if err != nil {
		return LessonChangeResult{}, err
	}
	if in.How == "" && in.Trigger == "" && !in.DropTrigger {
		return LessonChangeResult{}, fmt.Errorf("amend needs --how, --trigger or --no-trigger")
	}
	if in.Trigger != "" && in.DropTrigger {
		return LessonChangeResult{}, fmt.Errorf("--trigger and --no-trigger cannot be combined")
	}
	if len(in.How) > lessonTextMax {
		return LessonChangeResult{}, fmt.Errorf("lesson how must be at most %d characters; put detail behind --evidence", lessonTextMax)
	}
	trigger, hasTrigger, err := ParseLessonTrigger(in.Trigger)
	if err != nil {
		return LessonChangeResult{}, err
	}
	if hasTrigger {
		if err := checkNewLessonTriggerBroad(trigger, in.AllowBroadTrigger); err != nil {
			return LessonChangeResult{}, err
		}
	}
	return s.changeLesson(in, LessonChangeAmended, now)
}

// RestoreLesson undoes a retirement: the new version is served again.
func (s *Store) RestoreLesson(in LessonChangeInput, now time.Time) (LessonChangeResult, error) {
	in, err := normalizeLessonChangeInput(in)
	if err != nil {
		return LessonChangeResult{}, err
	}
	return s.changeLesson(in, LessonChangeRestored, now)
}

func normalizeLessonChangeInput(in LessonChangeInput) (LessonChangeInput, error) {
	in.ID = strings.TrimPrefix(strings.TrimSpace(in.ID), "lesson:")
	in.Actor, in.Reason = strings.TrimSpace(in.Actor), oneLine(in.Reason)
	in.How, in.Trigger, in.WorkID = oneLine(in.How), strings.TrimSpace(in.Trigger), strings.TrimSpace(in.WorkID)
	switch {
	case in.ID == "":
		return in, fmt.Errorf("name the lesson: compound memory lesson <retire|amend|restore> <claim_id>")
	case in.Actor == "":
		return in, fmt.Errorf("lesson change needs --by <actor>")
	case checkLessonActor(in.Actor) != nil:
		return in, checkLessonActor(in.Actor)
	case len(in.Actor) > LessonActorMaxBytes:
		return in, fmt.Errorf("--by is %d bytes; the limit is %d", len(in.Actor), LessonActorMaxBytes)
	case len(in.Reason) > lessonTextMax:
		return in, fmt.Errorf("reason must be at most %d characters", lessonTextMax)
	}
	return in, nil
}

func (s *Store) changeLesson(in LessonChangeInput, change string, now time.Time) (LessonChangeResult, error) {
	now = now.UTC()
	res := LessonChangeResult{SchemaVersion: LessonChangeSchemaVersion, Change: change}
	err := s.withLock(func() error {
		claims, superseded, skipped, err := s.lessonLedgerLocked()
		if err != nil {
			return err
		}
		res.SkippedLedgerRows = skipped
		head, err := s.lessonChainHead(claims, superseded, in.ID)
		if err != nil {
			return err
		}
		peers := lessonSlotPeers(claims, superseded, head)
		marks, err := s.claimReviewMarksAsOf(0)
		if err != nil {
			return err
		}
		contested := lessonContestPeers(marks, head, peers)

		var mutate func(*Claim)
		stamp := func(n *Claim) {
			n.Qualifiers = copyQualifiers(n.Qualifiers)
			n.Qualifiers[lessonChangeKey] = change
			n.Qualifiers[lessonChangedByKey] = in.Actor
			n.Qualifiers[lessonChangedOnKey] = now.Format("2006-01-02")
			delete(n.Qualifiers, lessonChangeReasonKey)
			if in.Reason != "" {
				n.Qualifiers[lessonChangeReasonKey] = in.Reason
			}
		}
		switch change {
		case LessonChangeRetired:
			if lessonRetired(head) {
				return fmt.Errorf("lesson %s is already retired (reason: %s); undo with `compound memory lesson restore %s --by <actor>`",
					head.ID, orDef(head.Qualifiers[lessonChangeReasonKey], "none given"), head.ID)
			}
			mutate = func(n *Claim) {
				stamp(n)
				n.Visibility = "retired"
				n.CooledReason = CooledReasonLessonRetired
			}
		case LessonChangeAmended:
			if lessonRetired(head) {
				return fmt.Errorf("lesson %s is retired; restore it first (`compound memory lesson restore %s --by <actor>`)", head.ID, head.ID)
			}
			if len(contested) > 0 {
				return fmt.Errorf("lesson %s is contested by %s; retire the wrong one first (`compound memory lesson retire <claim_id> --reason ... --by <actor>`), then amend the one that stays",
					head.ID, strings.Join(contested, ","))
			}
			how := head.Object
			if in.How != "" {
				how = in.How
			}
			trigger := head.Qualifiers["trigger"]
			if in.DropTrigger {
				trigger = ""
			} else if in.Trigger != "" {
				parsed, _, _ := ParseLessonTrigger(in.Trigger)
				trigger = parsed.Tool + ":" + parsed.Pattern
			}
			if trigger != "" && len(how) > LessonServedHowMaxBytes {
				return fmt.Errorf("a lesson with a trigger is injected as one line: its how is %d bytes, the limit is %d", len(how), LessonServedHowMaxBytes)
			}
			if NormalizeClaimValue(how) == NormalizeClaimValue(head.Object) && trigger == head.Qualifiers["trigger"] {
				return fmt.Errorf("lesson %s already has this how and trigger; nothing to amend", head.ID)
			}
			newTrigger := trigger != head.Qualifiers["trigger"]
			broad := false
			if parsed, ok, _ := ParseLessonTrigger(trigger); ok {
				broad = len(parsed.OrdinaryCallMatches()) > 0
			}
			mutate = func(n *Claim) {
				stamp(n)
				n.Object = how
				delete(n.Qualifiers, "trigger")
				if trigger != "" {
					n.Qualifiers["trigger"] = trigger
				}
				// The broad-trigger record follows the trigger it describes.
				if newTrigger {
					delete(n.Qualifiers, LessonBroadTriggerQualifier)
					if broad && in.AllowBroadTrigger {
						n.Qualifiers[LessonBroadTriggerQualifier] = "allowed"
					}
				}
			}
		case LessonChangeRestored:
			if !lessonRetired(head) {
				return fmt.Errorf("lesson %s is not retired; nothing to restore", head.ID)
			}
			for _, peer := range peers {
				if NormalizeClaimValue(peer.Object) == NormalizeClaimValue(head.Object) {
					return fmt.Errorf("lesson %s already serves the same how in this slot; nothing to restore", peer.ID)
				}
			}
			visibility := "contextual"
			for _, c := range claims {
				if c.ID == head.Supersedes && !lessonRetired(c) && strings.TrimSpace(c.Visibility) != "" {
					visibility = c.Visibility
				}
			}
			mutate = func(n *Claim) {
				stamp(n)
				n.Visibility = visibility
				n.CooledReason = ""
			}
		default:
			return fmt.Errorf("unknown lesson change %q", change)
		}

		next, err := s.appendClaimVersionLocked(head, now, mutate)
		if err != nil {
			return err
		}
		switch change {
		case LessonChangeRetired:
			// The retired version carries no marks; clear the old ones on both
			// sides so the lesson it contradicted is served as a lesson again.
			for _, peer := range contested {
				for _, pair := range [][2]string{{head.ID, peer}, {peer, head.ID}} {
					if err := s.appendLessonEdgeLocked(EdgeKindReviewCleared, pair[0], pair[1], head.Confidence,
						[]string{"lesson-retired", "changed_by:" + in.Actor}, now); err != nil {
						return err
					}
				}
			}
			res.ClearedContest = contested
		case LessonChangeRestored:
			// A restored lesson that differs from the slot's current lesson is
			// a contradiction again, parked exactly as the lesson door parks it.
			if len(peers) > 0 {
				latest := peers[len(peers)-1]
				if err := s.markLessonContestLocked(next, latest.ID, now); err != nil {
					return err
				}
				res.ContestedWith = latest.ID
			}
		}

		transition := s.memoryTransition(next, head, true, in.WorkID, now)
		transition.StateDelta = fmt.Sprintf("lesson %s: %s.%s (%s -> %s)", change, next.Entity, next.Predicate, head.ID, next.ID)
		if in.Reason != "" {
			transition.StateDelta += ": " + in.Reason
		}
		transition.EvidenceRefs = uniqueStrings(append(append([]string(nil), next.EvidenceRefs...), "lesson-"+change+"-by:"+in.Actor))
		if change == LessonChangeRetired {
			transition.Projections.Typed[0].Role = "rejected"
		}
		if err := transition.Validate(); err != nil {
			return fmt.Errorf("lesson transition invalid: %w", err)
		}
		if err := s.writePromotionTransition(&transition); err != nil {
			return err
		}
		res.Prior, res.Claim, res.TransitionID = head, next, transition.ID
		return nil
	})
	if err != nil {
		return res, err
	}
	res.PositionID = positionIDForClaim(res.Claim)
	return res, nil
}

// lessonLedgerLocked reads the claims and the supersession state the lesson
// door and verbs need. A malformed claims row is skipped and counted, so one
// bad row never blocks a lesson write or a retire. Caller holds the lock.
func (s *Store) lessonLedgerLocked() ([]Claim, map[string]bool, int, error) {
	var claims []Claim
	skipped := 0
	err := readJSONL(s.claimsLog(), func(line []byte) error {
		var c Claim
		if json.Unmarshal(line, &c) != nil {
			skipped++
			return nil
		}
		claims = append(claims, c)
		return nil
	})
	if err != nil {
		return nil, nil, 0, err
	}
	superseded := map[string]bool{}
	err = readJSONL(s.edgesLog(), func(line []byte) error {
		var e Edge
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		if e.Kind == EdgeKindSupersedes {
			superseded[e.To] = true
		}
		return nil
	})
	if err != nil {
		return nil, nil, 0, err
	}
	for _, c := range claims {
		if c.Supersedes != "" {
			superseded[c.Supersedes] = true
		}
	}
	return claims, superseded, skipped, nil
}

// lessonChainHead resolves id to the head of its lesson version chain, so the
// id an agent saw in a hook line keeps working after a retire or an amend.
// Only version links (a claim's own Supersedes pointer, which every lesson
// verb writes) are followed. A supersedes edge written when a contest was
// resolved replaces the lesson with a different lesson, so it stops the walk.
func (s *Store) lessonChainHead(claims []Claim, superseded map[string]bool, id string) (Claim, error) {
	byID := make(map[string]Claim, len(claims))
	next := map[string]string{}
	for _, c := range claims {
		byID[c.ID] = c
		if c.Supersedes != "" {
			next[c.Supersedes] = c.ID
		}
	}
	cur, ok := byID[id]
	if !ok {
		return Claim{}, fmt.Errorf("lesson %s not found", id)
	}
	if cur.ClaimType != ClaimTypeLesson {
		return Claim{}, fmt.Errorf("claim %s is a %s claim, not a lesson", id, orDef(cur.ClaimType, "untyped"))
	}
	seen := map[string]bool{cur.ID: true}
	for {
		nextID, ok := next[cur.ID]
		if !ok || seen[nextID] {
			break
		}
		successor, ok := byID[nextID]
		if !ok || successor.ClaimType != ClaimTypeLesson {
			break
		}
		seen[nextID] = true
		cur = successor
	}
	if superseded[cur.ID] {
		by := ""
		_ = readJSONL(s.edgesLog(), func(line []byte) error {
			var e Edge
			if decodeErr := json.Unmarshal(line, &e); decodeErr == nil && e.Kind == EdgeKindSupersedes && e.To == cur.ID {
				by = e.From
			}
			return nil
		})
		return Claim{}, fmt.Errorf("lesson %s was replaced by a different lesson (%s) when a contest was resolved; act on that lesson instead", cur.ID, orDef(by, "unknown"))
	}
	return cur, nil
}

// lessonSlotPeers returns the other current, non-retired lessons in head's
// slot, oldest first.
func lessonSlotPeers(claims []Claim, superseded map[string]bool, head Claim) []Claim {
	slot := ClaimSlotKey(head.Entity, head.Predicate, head.ClaimType, head.Qualifiers, head.ID)
	var peers []Claim
	for _, c := range claims {
		if c.ID == head.ID || c.ClaimType != ClaimTypeLesson || superseded[c.ID] || lessonRetired(c) {
			continue
		}
		if ClaimSlotKey(c.Entity, c.Predicate, c.ClaimType, c.Qualifiers, c.ID) == slot {
			peers = append(peers, c)
		}
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Seq < peers[j].Seq })
	return peers
}

// lessonContestPeers returns the slot peers that hold an open contest mark
// with head, in either direction.
func lessonContestPeers(marks map[string]claimReviewMark, head Claim, peers []Claim) []string {
	var out []string
	for _, peer := range peers {
		open := false
		for _, ref := range marks[head.ID].Refs {
			open = open || ref == peer.ID
		}
		for _, ref := range marks[peer.ID].Refs {
			open = open || ref == head.ID
		}
		if open {
			out = append(out, peer.ID)
		}
	}
	return out
}

// markLessonContestLocked parks a contradiction between two lessons in one
// slot: append-only needs_review marks in both directions. A needs_review edge
// From=X To=Y reads "Y is contested by X". Caller holds the memory lock.
func (s *Store) markLessonContestLocked(claim Claim, otherID string, now time.Time) error {
	for _, pair := range [][2]string{{claim.ID, otherID}, {otherID, claim.ID}} {
		if err := s.appendLessonEdgeLocked(EdgeKindNeedsReview, pair[0], pair[1], claim.Confidence,
			[]string{"lesson-contradiction", "contested_by:" + pair[0]}, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) appendLessonEdgeLocked(kind, from, to string, confidence float64, evidence []string, now time.Time) error {
	seq, err := s.nextSeq()
	if err != nil {
		return err
	}
	edge := Edge{Seq: seq, Kind: kind, From: from, To: to, Confidence: confidence, EvidenceRefs: evidence, CreatedAt: rfc3339(now)}
	return appendJSONL(s.edgesLog(), &edge)
}

func copyQualifiers(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+4)
	for k, v := range in {
		out[k] = v
	}
	return out
}
