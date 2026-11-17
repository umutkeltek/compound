package attention

import (
	"sort"
	"strings"
	"time"
)

// Four more places hold things that wait for the operator: memory proposals,
// product results ready to judge, approval requests raised by hooks, and data
// sources that need someone. Each owner keeps its own store; the CLI reads it
// through the owner's existing reader and hands the facts in here, and this
// file judges every item under the same rules as review records: live items
// are counted under needs_you, anything that is not his is set aside with a
// reason and listed, never dropped. Each queue is its own kind, and every
// item carries enough identity (Group.Items) for a page to link it.
//
// Read-only, like the rest of the projection.

const (
	SourceMemory       = "memory"
	SourceProduct      = "product"
	SourceHookRequests = "hook_requests"
	SourceDataSources  = "data_sources"

	// KindMemoryProposal is a proposed memory only the operator may promote
	// (agents may only set one aside).
	KindMemoryProposal = "memory_proposal"
	// KindProductResult is a product change whose result is ready to judge:
	// did it work?
	KindProductResult = "product_result"
	// KindHookApproval is an action a hook held for his approval.
	KindHookApproval = "hook_approval"
	// KindSourceSetup is a data source that needs his private setup.
	KindSourceSetup = "source_setup"

	// SetAsidePlaceholderDraft: the ask rests on a draft nobody wrote (its
	// expected effect is still the owner's placeholder text).
	SetAsidePlaceholderDraft = "placeholder_draft"
	// SetAsideWindowClosed: a held action whose approval window closed.
	SetAsideWindowClosed = "window_closed"
	// SetAsideAgentWork: the owner's next step needs no input from him.
	SetAsideAgentWork = "agent_work"
)

// HookApprovalWindow is the longest an exact action approval can stay open
// after a hook holds an action: the policy owner refuses any approval plan
// longer than this, so a request older than it can no longer be answered.
// Pinned against the policy owner by TestHookApprovalWindowMatchesThePolicyCap.
const HookApprovalWindow = 15 * time.Minute

// MemoryProposalsInput is the memory owner's proposed candidates.
type MemoryProposalsInput struct {
	GeneratedAt string
	Items       []MemoryProposal
}

// MemoryProposal is one proposed memory candidate.
type MemoryProposal struct {
	ID            string
	Claim         string // entity, predicate and object, as the owner states it
	ClaimKey      string // the owner's normalized claim: two proposals with one key are one ask
	Context       string // what the claim is about (its entity)
	CreatedAt     string
	Path          string
	AdjudicatedBy string // set once someone ruled on it
}

// ProductResultsInput is the product owner's hypotheses whose results are
// ready to judge, one per hypothesis.
type ProductResultsInput struct {
	GeneratedAt string
	Items       []ProductResult
}

// ProductResult is one hypothesis whose result is ready to judge.
type ProductResult struct {
	ProductID      string
	HypothesisID   string
	ChangeEvent    string
	ExpectedEffect string
	CreatedAt      string
	SignalIDs      []string
}

// HookRequestsInput is the approval requests hooks filed when they held an
// action, as the policy owner's frontier registry holds them.
type HookRequestsInput struct {
	GeneratedAt string
	Path        string // the registry file every request lives in
	Items       []HookRequest
}

// HookRequest is one approval request a hook filed.
type HookRequest struct {
	ID             string
	ActionIntentID string
	Action         string
	Capability     string
	Harness        string
	MintedAt       string
	Status         string
}

// DataSourcesInput is the source owner's connections that need someone.
type DataSourcesInput struct {
	GeneratedAt string
	Items       []DataSource
}

// DataSource is one source connection the owner holds as needing action.
type DataSource struct {
	ID       string
	Label    string
	Status   string // the owner's status: stale, needs_private_input, ...
	LastSeen string
	// PrivateSetup is the owner's flag that the source runs on private
	// setup. It is carried for display; the owner's status decides whose
	// next step it is (a stale source's next step is a refresh).
	PrivateSetup bool
	NextStep     string
}

// ItemRef identifies one item of a group so a page can link it.
type ItemRef struct {
	ID string `json:"id"`
	// Title is the plain question or subject.
	Title string `json:"title,omitempty"`
	// Context names what it is about: the app, the source, the harness.
	Context string `json:"context,omitempty"`
	// Ref is the owner's address for the item.
	Ref       string `json:"ref,omitempty"`
	Path      string `json:"path,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// queueUnit is one item of a non-review queue with its verdict.
type queueUnit struct {
	ref    ItemRef
	status string
	reason string
	dupOf  string
}

// queueSpec is what one queue's group looks like.
type queueSpec struct {
	source, area, kind string
	one, many, unlock  string
	generatedAt        string
	blastText          string
}

// queueResult is one queue's share of the report.
type queueResult struct {
	source   string
	read     int
	live     *Group
	setAside []SetAside
	fresh    SourceFreshness
}

// queueResults judges every queue the inputs carry, in a fixed order.
// reviewAsks maps a product hypothesis to the live review group already
// asking about it (nil when only freshness is wanted).
func queueResults(in Inputs, reviewAsks map[string]string, now time.Time) []queueResult {
	var out []queueResult
	if in.MemoryProposals != nil {
		out = append(out, memoryQueue(*in.MemoryProposals, now))
	}
	if in.ProductResults != nil {
		out = append(out, productQueue(*in.ProductResults, in.DraftPlaceholders, reviewAsks, now))
	}
	if in.HookRequests != nil {
		out = append(out, hookQueue(*in.HookRequests, now))
	}
	if in.DataSources != nil {
		out = append(out, sourceQueue(*in.DataSources, now))
	}
	return out
}

func buildQueue(spec queueSpec, units []queueUnit, now time.Time) queueResult {
	out := queueResult{source: spec.source, read: len(units)}
	key := spec.source + ":" + spec.area
	newest := ""
	var live []queueUnit
	batches := map[string]*SetAside{}
	var reasons []string
	for _, u := range units {
		newest = newerTimestamp(newest, u.ref.CreatedAt)
		if u.reason == "" {
			live = append(live, u)
			continue
		}
		b := batches[u.reason]
		if b == nil {
			b = &SetAside{
				Key:             key + "#" + u.reason,
				Band:            BandSetAside,
				Reason:          u.reason,
				ReasonText:      setAsideReasonText[u.reason],
				Area:            spec.area,
				Source:          spec.source,
				Kind:            spec.kind,
				OperatorSummary: plural(0, spec.one, spec.many),
				SourceGroupKey:  key,
				DuplicateOf:     u.dupOf,
				Closed:          setAsideClosed[u.reason],
				NextStep:        setAsideNextStep[u.reason],
				Reversibility:   Reversible,
				RiskGrade:       RiskGradeJudge,
			}
			batches[u.reason] = b
			reasons = append(reasons, u.reason)
		}
		b.Count++
		b.ItemIDs = append(b.ItemIDs, u.ref.ID)
		b.Records = append(b.Records, SetAsideRecord{ID: u.ref.ID, Path: u.ref.Path, CreatedAt: u.ref.CreatedAt, Status: u.status})
	}
	for _, reason := range reasons {
		b := batches[reason]
		b.OperatorSummary = plural(b.Count, spec.one, spec.many)
		b.ItemIDs = uniqueStrings(b.ItemIDs)
		sort.SliceStable(b.Records, func(i, j int) bool { return b.Records[i].ID < b.Records[j].ID })
		b.Digest = setAsideDigest(b.Records)
		out.setAside = append(out.setAside, *b)
	}
	if len(live) > 0 {
		sort.SliceStable(live, func(i, j int) bool { return live[i].ref.ID < live[j].ref.ID })
		g := &Group{
			Key:             key,
			Area:            spec.area,
			Band:            BandNeedsYou,
			Source:          spec.source,
			Kind:            spec.kind,
			Count:           len(live),
			OperatorSummary: plural(len(live), spec.one, spec.many),
			OperatorUnlock:  spec.unlock,
			Freshness:       Freshness{GeneratedAt: firstNonEmpty(spec.generatedAt, now.UTC().Format(time.RFC3339)), Source: spec.source},
		}
		for _, u := range live {
			g.ItemIDs = append(g.ItemIDs, u.ref.ID)
			g.Items = append(g.Items, u.ref)
			g.OldestCreatedAt = olderTimestamp(g.OldestCreatedAt, u.ref.CreatedAt)
		}
		if spec.blastText != "" {
			// The held actions' own capabilities set the reach, never the
			// card's wording.
			g.BlastRadius = deriveBlastRadius(Group{OperatorSummary: spec.blastText}, BlastLocalReversible)
		}
		out.live = g
	}
	out.fresh = SourceFreshness{Source: spec.source, ReadAt: spec.generatedAt, NewestRecordAt: newest, Records: len(units)}
	return out
}

func memoryQueue(in MemoryProposalsInput, now time.Time) queueResult {
	units := make([]queueUnit, 0, len(in.Items))
	items := append([]MemoryProposal{}, in.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt != items[j].CreatedAt {
			return olderTimestamp(items[i].CreatedAt, items[j].CreatedAt) == items[i].CreatedAt
		}
		return items[i].ID < items[j].ID
	})
	byClaim := map[string]string{}
	for _, p := range items {
		u := queueUnit{ref: ItemRef{ID: p.ID, Title: p.Claim, Context: p.Context, Ref: "compound://memory/candidates/" + p.ID, Path: p.Path, CreatedAt: p.CreatedAt}, status: "proposed"}
		key := normalizeQuestion(p.ClaimKey)
		switch {
		case strings.TrimSpace(p.AdjudicatedBy) != "":
			u.reason = SetAsideAlreadyDecided
		case key != "" && byClaim[key] != "":
			u.reason, u.dupOf = SetAsideDuplicate, byClaim[key]
		}
		if u.reason == "" && key != "" {
			byClaim[key] = p.ID
		}
		units = append(units, u)
	}
	return buildQueue(queueSpec{
		source: SourceMemory, area: "memory-proposals", kind: KindMemoryProposal, generatedAt: in.GeneratedAt,
		one: "proposed memory waits for your yes or no.", many: "proposed memories wait for your yes or no.",
		unlock: "Only you can make a proposed memory part of what agents know; agents may only set one aside.",
	}, units, now)
}

func productQueue(in ProductResultsInput, placeholders []string, reviewAsks map[string]string, now time.Time) queueResult {
	units := make([]queueUnit, 0, len(in.Items))
	for _, r := range in.Items {
		u := queueUnit{ref: ItemRef{ID: r.HypothesisID, Title: r.ExpectedEffect, Context: r.ProductID, Ref: "compound://product/" + r.ProductID + "/hypotheses/" + r.HypothesisID, CreatedAt: r.CreatedAt}, status: "review_ready"}
		switch {
		case placeholderText(r.ExpectedEffect, placeholders):
			u.reason = SetAsidePlaceholderDraft
		case reviewAsks[r.HypothesisID] != "":
			u.reason, u.dupOf = SetAsideDuplicate, reviewAsks[r.HypothesisID]
		}
		units = append(units, u)
	}
	return buildQueue(queueSpec{
		source: SourceProduct, area: "product-results-due", kind: KindProductResult, generatedAt: in.GeneratedAt,
		one: "product change has a result ready for your verdict: did it work?", many: "product changes have results ready for your verdict: did they work?",
		unlock: "Say whether each change worked, did not, or is unclear; the result is on file.",
	}, units, now)
}

func hookQueue(in HookRequestsInput, now time.Time) queueResult {
	units := make([]queueUnit, 0, len(in.Items))
	var capabilities []string
	for _, r := range in.Items {
		title := strings.TrimSpace(strings.Join(compactStrings([]string{r.Action, r.Capability}), " · "))
		u := queueUnit{ref: ItemRef{ID: r.ID, Title: title, Context: r.Harness, Ref: "compound://registries/operator-approval-frontier#" + r.ID, Path: in.Path, CreatedAt: r.MintedAt}, status: r.Status}
		minted, ok := parseTime(r.MintedAt)
		switch {
		case strings.TrimSpace(r.Status) != "" && strings.TrimSpace(r.Status) != "pending":
			u.reason = SetAsideAlreadyDecided
		case ok && !now.Before(minted.Add(HookApprovalWindow)):
			u.reason = SetAsideWindowClosed
		default:
			capabilities = append(capabilities, r.Capability)
		}
		units = append(units, u)
	}
	return buildQueue(queueSpec{
		source: SourceHookRequests, area: "hook-approval-requests", kind: KindHookApproval, generatedAt: in.GeneratedAt,
		one: "action an agent tried is held for your approval right now.", many: "actions agents tried are held for your approval right now.",
		unlock:    "Approve or refuse each held action while its window is open; the agent waits or takes another way.",
		blastText: strings.Join(uniqueStrings(capabilities), " "),
	}, units, now)
}

func sourceQueue(in DataSourcesInput, now time.Time) queueResult {
	units := make([]queueUnit, 0, len(in.Items))
	for _, s := range in.Items {
		u := queueUnit{ref: ItemRef{ID: s.ID, Title: firstNonEmpty(s.NextStep, s.Status), Context: s.Label, Ref: "compound://sources/connections#" + s.ID, CreatedAt: s.LastSeen}, status: s.Status}
		if !sourceStatusNeedsOperator(s.Status) {
			u.reason = SetAsideAgentWork
		}
		units = append(units, u)
	}
	return buildQueue(queueSpec{
		source: SourceDataSources, area: "data-source-setup", kind: KindSourceSetup, generatedAt: in.GeneratedAt,
		one: "data source needs your private setup to stay current.", many: "data sources need your private setup to stay current.",
		unlock: "Supply the account or credential each one names; agents cannot supply private setup.",
	}, units, now)
}

// sourceStatusNeedsOperator names the source owner's statuses whose next step
// is his: supplying private input, or an action card waiting for a review.
// Every other status's next step (refresh, preflight, mapping) is agent work.
func sourceStatusNeedsOperator(status string) bool {
	switch strings.TrimSpace(status) {
	case "blocked_private_config", "needs_private_input", "needs_review":
		return true
	default:
		return false
	}
}

// placeholderText reports text that is still an owner's unwritten-draft
// placeholder.
func placeholderText(text string, placeholders []string) bool {
	text = strings.TrimSpace(text)
	for _, prefix := range placeholders {
		if prefix = strings.TrimSpace(prefix); prefix != "" && strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}
