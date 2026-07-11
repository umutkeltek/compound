package attention

import (
	"math"
	"sort"
	"strings"
	"time"

	"compound/internal/reviewartifact"
	"compound/internal/work"
)

const (
	SchemaVersion = "compound.attention.v0.1"

	BandNeedsYou = "needs_you"
	BandWatching = "watching"
	BandFiled    = "filed"

	SourceReviews   = "reviews"
	SourceWorkbench = "workbench"
	SourceExecutor  = "executor"

	KindGateAdmit     = "gate_admit"
	KindVerdict       = "verdict"
	KindPromotion     = "promotion"
	KindContradiction = "contradiction"
	KindAdjudicate    = "adjudicate"
	KindIntake        = "intake"
	KindAdvance       = "advance"
	KindHygiene       = "hygiene"
	KindJobHealth     = "job_health"
	KindProductMove   = "product_move"

	BlastRead               = "read"
	BlastLocalReversible    = "local_reversible"
	BlastExternalSideEffect = "external_side_effect"
	BlastPublic             = "public"
	BlastDestructive        = "destructive"
	BlastFinancial          = "financial"
	BlastSecurity           = "security"

	Reversible   = "reversible"
	Irreversible = "irreversible"

	ActionOneClick = "one_click"
	ActionBatch    = "batch"
	ActionCopyOnly = "copy_only"
)

type Inputs struct {
	Reviews   *reviewartifact.QueueReport
	Workbench *work.WorkbenchReport
	Executors *work.ExecutorsProjection
}

type Report struct {
	SchemaVersion string  `json:"schema_version"`
	GeneratedAt   string  `json:"generated_at"`
	Counts        Counts  `json:"counts"`
	Top           *Group  `json:"top"`
	Groups        []Group `json:"groups"`
}

type Counts struct {
	NeedsYou       int `json:"needs_you"`
	NeedsYouGroups int `json:"needs_you_groups"`
	ActionableNow  int `json:"actionable_now"`
	Watching       int `json:"watching"`
	BlockedOnYou   int `json:"blocked_on_you"`
	FallbackGroups int `json:"fallback_groups"`
}

type Group struct {
	Key             string    `json:"key"`
	Area            string    `json:"area"`
	Band            string    `json:"band"`
	Source          string    `json:"source"`
	Kind            string    `json:"kind"`
	Count           int       `json:"count"`
	OperatorSummary string    `json:"operator_summary"`
	OperatorUnlock  string    `json:"operator_unlock"`
	BlastRadius     string    `json:"blast_radius"`
	Reversibility   string    `json:"reversibility"`
	Leverage        float64   `json:"leverage"`
	OldestCreatedAt string    `json:"oldest_created_at"`
	Action          *Action   `json:"action"`
	TerminalReason  string    `json:"terminal_reason"`
	ItemIDs         []string  `json:"item_ids"`
	Freshness       Freshness `json:"freshness"`
}

type Action struct {
	ID                   string   `json:"id"`
	Label                string   `json:"label"`
	Argv                 []string `json:"argv"`
	Kind                 string   `json:"kind"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
}

type Freshness struct {
	GeneratedAt string `json:"generated_at"`
	Source      string `json:"source"`
}

type areaClassification struct {
	Kind          string
	BlastRadius   string
	Reversibility string
}

var knownAreaClassifications = map[string]areaClassification{
	"panel-verdicts":                 {Kind: KindVerdict},
	"route-promotions":               {Kind: KindPromotion},
	"synthesis-contradictions":       {Kind: KindContradiction},
	"external-repo-intake":           {Kind: KindIntake},
	"job-health":                     {Kind: KindJobHealth},
	"product-action-cards":           {Kind: KindProductMove},
	"bench":                          {Kind: KindHygiene},
	"synthesis-sweeps":               {Kind: KindHygiene},
	"capability-evaluation-intake":   {Kind: KindIntake},
	"source-control-cleanup":         {Kind: KindHygiene},
	"capability-lifecycle-proposals": {Kind: KindPromotion},
	"observation-candidates":         {Kind: KindAdjudicate},
	"source-observation-verdicts":    {Kind: KindVerdict},
	"distiller-improvements":         {Kind: KindPromotion},
	"distiller-requests":             {Kind: KindIntake},
	"parity":                         {Kind: KindHygiene},
	"route-remorse":                  {Kind: KindContradiction},
}

func Build(in Inputs, now time.Time) Report {
	if now.IsZero() {
		now = time.Now()
	}
	generatedAt := now.UTC().Format(time.RFC3339)
	report := Report{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   generatedAt,
		Groups:        []Group{},
	}
	if in.Reviews != nil {
		report.Groups = append(report.Groups, groupsFromReviews(*in.Reviews, now)...)
		report.Counts.FallbackGroups = fallbackGroupCount(*in.Reviews)
	}
	if in.Workbench != nil {
		report.Groups = append(report.Groups, groupsFromWorkbench(*in.Workbench, now)...)
	}
	if in.Executors != nil {
		report.Groups = append(report.Groups, groupsFromExecutors(*in.Executors, now)...)
	}
	for i := range report.Groups {
		normalizeGroup(&report.Groups[i], generatedAt, now)
	}
	sortGroups(report.Groups)
	report.Counts = countGroups(report.Groups, report.Counts.FallbackGroups)
	if len(report.Groups) > 0 {
		top := report.Groups[0]
		report.Top = &top
	}
	return report
}

func Class(g Group) string {
	switch classRank(g) {
	case 0:
		return "gate"
	case 1:
		return "judge"
	default:
		return "tidy"
	}
}

func ClassifyArea(area string) (kind, blastRadius, reversibility string) {
	c := classifyArea(area)
	return c.Kind, c.BlastRadius, c.Reversibility
}

func groupsFromReviews(report reviewartifact.QueueReport, now time.Time) []Group {
	out := make([]Group, 0, len(report.Groups))
	freshness := Freshness{GeneratedAt: firstNonEmpty(report.GeneratedAt, now.UTC().Format(time.RFC3339)), Source: SourceReviews}
	for _, group := range report.Groups {
		g := Group{
			Key:             firstNonEmpty(group.Key, group.Area),
			Area:            group.Area,
			Band:            firstNonEmpty(group.Band, reviewartifact.BandForStatus(group.Status, group.Readiness)),
			Source:          SourceReviews,
			Count:           maxInt(group.Count, len(group.ItemIDs)),
			OperatorSummary: firstNonEmpty(group.OperatorSummary, group.ContextLabel, group.Area),
			OperatorUnlock:  group.OperatorUnlock,
			OldestCreatedAt: oldestReviewGroupCreatedAt(group, report.Items),
			Action:          actionFromReviewGroup(group),
			TerminalReason:  group.TerminalReason,
			ItemIDs:         append([]string{}, group.ItemIDs...),
			Freshness:       freshness,
		}
		out = append(out, g)
	}
	return out
}

func groupsFromWorkbench(report work.WorkbenchReport, now time.Time) []Group {
	freshness := Freshness{GeneratedAt: firstNonEmpty(report.GeneratedAt, now.UTC().Format(time.RFC3339)), Source: SourceWorkbench}
	var out []Group
	canMove := filterWorkbenchItems(report.Open, func(item work.WorkbenchItem) bool {
		return workbenchItemCanMove(item) && !workbenchItemNeedsOperator(item)
	})
	if len(canMove) > 0 {
		var action *Action
		if workbenchItemsContain(canMove, report.DispatchNext.WorkID) {
			action = actionFromWorkbenchDispatch(report.DispatchNext)
		}
		out = append(out, Group{
			Key:             "workbench:can-move",
			Area:            "workbench-needs-claim",
			Band:            BandWatching,
			Source:          SourceWorkbench,
			Kind:            KindAdvance,
			Count:           len(canMove),
			OperatorSummary: plural(len(canMove), "safe open work item can move.", "safe open work items can move."),
			OperatorUnlock:  "Executors can claim or advance this work inside the admitted envelope.",
			OldestCreatedAt: oldestWorkbenchItemTime(canMove, freshness.GeneratedAt),
			Action:          action,
			ItemIDs:         workbenchItemIDs(canMove),
			Freshness:       freshness,
		})
	}
	blocked := filterWorkbenchItems(report.Open, func(item work.WorkbenchItem) bool {
		return item.State == "held" || item.State == "blocked" || (workbenchItemCanMove(item) && workbenchItemNeedsOperator(item))
	})
	if len(blocked) > 0 {
		blastRadius, reversibility := workbenchGateRisk(blocked)
		out = append(out, Group{
			Key:             "workbench:blocked-on-you",
			Area:            "workbench-blocked-on-you",
			Band:            BandNeedsYou,
			Source:          SourceWorkbench,
			Kind:            KindGateAdmit,
			Count:           len(blocked),
			OperatorSummary: plural(len(blocked), "open work item is blocked on operator review.", "open work items are blocked on operator review."),
			OperatorUnlock:  "Resolve the hold before executors can continue.",
			BlastRadius:     blastRadius,
			Reversibility:   reversibility,
			OldestCreatedAt: oldestWorkbenchItemTime(blocked, freshness.GeneratedAt),
			TerminalReason:  firstWorkbenchReason(blocked),
			ItemIDs:         workbenchItemIDs(blocked),
			Freshness:       freshness,
		})
	}
	if !report.DispatchNext.CanRun && (strings.TrimSpace(report.DispatchNext.WaitReason) != "" || strings.TrimSpace(report.DispatchNext.Reason) != "") {
		out = append(out, Group{
			Key:             "workbench:dispatch-next",
			Area:            "workbench-dispatch-next",
			Band:            BandWatching,
			Source:          SourceWorkbench,
			Kind:            KindAdvance,
			Count:           1,
			OperatorSummary: "Dispatch-next is waiting.",
			OperatorUnlock:  strings.TrimSpace(report.DispatchNext.Action),
			OldestCreatedAt: freshness.GeneratedAt,
			Action:          actionFromWorkbenchDispatch(report.DispatchNext),
			TerminalReason:  firstNonEmpty(report.DispatchNext.WaitReason, report.DispatchNext.Reason),
			ItemIDs:         compactStrings([]string{report.DispatchNext.WorkID}),
			Freshness:       freshness,
		})
	}
	return out
}

func groupsFromExecutors(report work.ExecutorsProjection, now time.Time) []Group {
	freshness := Freshness{GeneratedAt: firstNonEmpty(report.GeneratedAt, now.UTC().Format(time.RFC3339)), Source: SourceExecutor}
	var out []Group
	for _, row := range report.Executors {
		count, itemIDs := executorOwnedBurden(row)
		if count <= 0 {
			continue
		}
		summary := row.Label
		if strings.TrimSpace(summary) == "" {
			summary = row.ID
		}
		summary = summary + " needs operator attention."
		out = append(out, Group{
			Key:             "executor:" + firstNonEmpty(row.ID, row.Label),
			Area:            "job-health",
			Band:            BandNeedsYou,
			Source:          SourceExecutor,
			Kind:            KindJobHealth,
			Count:           count,
			OperatorSummary: summary,
			OperatorUnlock:  firstNonEmpty(row.WaitReason, row.ProjectionNote),
			OldestCreatedAt: oldestExecutorTime(row, freshness.GeneratedAt, now),
			TerminalReason:  firstNonEmpty(row.WaitReason, row.ProjectionNote),
			ItemIDs:         itemIDs,
			Freshness:       freshness,
		})
	}
	return out
}

func normalizeGroup(g *Group, generatedAt string, now time.Time) {
	if strings.TrimSpace(g.Key) == "" {
		g.Key = firstNonEmpty(g.Area, g.Source, "attention")
	}
	if strings.TrimSpace(g.Area) == "" {
		g.Area = "unknown"
	}
	if strings.TrimSpace(g.Band) == "" {
		g.Band = BandNeedsYou
	}
	if strings.TrimSpace(g.Kind) == "" {
		g.Kind = classifyArea(g.Area).Kind
	}
	classification := classifyArea(g.Area)
	if strings.TrimSpace(g.BlastRadius) == "" {
		g.BlastRadius = deriveBlastRadius(*g, classification.BlastRadius)
	}
	if strings.TrimSpace(g.Reversibility) == "" {
		g.Reversibility = deriveReversibility(*g, classification.Reversibility)
	}
	if g.Count <= 0 {
		g.Count = maxInt(1, len(g.ItemIDs))
	}
	if strings.TrimSpace(g.OperatorSummary) == "" {
		g.OperatorSummary = reviewartifact.PlainLabel(g.Area) + " needs attention."
	}
	if strings.TrimSpace(g.OldestCreatedAt) == "" {
		g.OldestCreatedAt = generatedAt
	}
	if strings.TrimSpace(g.Freshness.GeneratedAt) == "" {
		g.Freshness.GeneratedAt = generatedAt
	}
	if strings.TrimSpace(g.Freshness.Source) == "" {
		g.Freshness.Source = g.Source
	}
	if g.Action != nil {
		normalizeAction(g.Action, *g)
	}
	g.Leverage = leverage(g.Count, g.OldestCreatedAt, g.OperatorUnlock, now)
	g.ItemIDs = uniqueStrings(g.ItemIDs)
}

func classifyArea(area string) areaClassification {
	if c, ok := knownAreaClassifications[strings.TrimSpace(area)]; ok {
		return withDefaults(c)
	}
	return areaClassification{Kind: KindAdjudicate, BlastRadius: BlastLocalReversible, Reversibility: Reversible}
}

func withDefaults(c areaClassification) areaClassification {
	if c.Kind == "" {
		c.Kind = KindAdjudicate
	}
	if c.BlastRadius == "" {
		c.BlastRadius = BlastLocalReversible
	}
	if c.Reversibility == "" {
		c.Reversibility = Reversible
	}
	return c
}

func deriveBlastRadius(g Group, fallback string) string {
	text := strings.ToLower(strings.Join([]string{
		g.Area,
		g.Kind,
		g.OperatorSummary,
		g.OperatorUnlock,
		g.TerminalReason,
		actionText(g.Action),
	}, " "))
	switch {
	case strings.Contains(text, "security") || strings.Contains(text, "secret") || strings.Contains(text, "credential"):
		return BlastSecurity
	case strings.Contains(text, "financial") || strings.Contains(text, "spend") || strings.Contains(text, "billing") || strings.Contains(text, "cost"):
		return BlastFinancial
	case strings.Contains(text, "destructive") || strings.Contains(text, "delete") || strings.Contains(text, "reset") || strings.Contains(text, "archive"):
		return BlastDestructive
	case strings.Contains(text, "public") || strings.Contains(text, "publish") || strings.Contains(text, "release") || strings.Contains(text, "push"):
		return BlastPublic
	case strings.Contains(text, "external") || strings.Contains(text, "send") || strings.Contains(text, "submit"):
		return BlastExternalSideEffect
	case strings.Contains(text, "read-only") || strings.Contains(text, "read only"):
		return BlastRead
	default:
		return firstNonEmpty(fallback, BlastLocalReversible)
	}
}

func deriveReversibility(g Group, fallback string) string {
	text := strings.ToLower(strings.Join([]string{
		g.Area,
		g.Kind,
		g.OperatorSummary,
		g.OperatorUnlock,
		g.TerminalReason,
		actionText(g.Action),
	}, " "))
	if strings.Contains(text, "irreversible") || g.BlastRadius == BlastDestructive || g.BlastRadius == BlastFinancial || g.BlastRadius == BlastSecurity || g.BlastRadius == BlastPublic {
		return Irreversible
	}
	return firstNonEmpty(fallback, Reversible)
}

func actionFromReviewGroup(group reviewartifact.QueueGroup) *Action {
	if group.SafeBatchAction != nil {
		return actionFromReviewAction(*group.SafeBatchAction, group.Count)
	}
	if group.Count == 1 && group.Sample != nil && len(group.Sample.Actions) == 1 {
		return actionFromReviewAction(group.Sample.Actions[0], 1)
	}
	return nil
}

func actionFromReviewAction(action reviewartifact.Action, count int) *Action {
	kind := ActionCopyOnly
	if cleanCompoundArgv(action.Argv) {
		kind = ActionOneClick
		if count > 1 {
			kind = ActionBatch
		}
	}
	return &Action{
		ID:                   firstNonEmpty(action.ID, "review.action"),
		Label:                firstNonEmpty(action.Label, "Review"),
		Argv:                 append([]string{}, action.Argv...),
		Kind:                 kind,
		RequiresConfirmation: action.RequiresReview || kind == ActionBatch || kind == ActionCopyOnly,
	}
}

func actionFromWorkbenchDispatch(dispatch work.WorkbenchDispatchNext) *Action {
	if !dispatch.CanRun || !cleanCompoundArgv(dispatch.Command) {
		return nil
	}
	return &Action{
		ID:                   "workbench.dispatch_next",
		Label:                firstNonEmpty(dispatch.Action, "Dispatch next work"),
		Argv:                 append([]string{}, dispatch.Command...),
		Kind:                 ActionOneClick,
		RequiresConfirmation: false,
	}
}

func normalizeAction(action *Action, g Group) {
	if action == nil {
		return
	}
	if len(action.Argv) == 0 || !cleanCompoundArgv(action.Argv) {
		action.Kind = ActionCopyOnly
		action.RequiresConfirmation = true
	} else if action.Kind == "" {
		action.Kind = ActionOneClick
	}
	if classRank(g) == 0 {
		action.RequiresConfirmation = true
	}
	if action.Argv == nil {
		action.Argv = []string{}
	}
}

func cleanCompoundArgv(argv []string) bool {
	return len(argv) > 0 && argv[0] == "compound"
}

func sortGroups(groups []Group) {
	sort.SliceStable(groups, func(i, j int) bool {
		ci, cj := classRank(groups[i]), classRank(groups[j])
		if ci != cj {
			return ci < cj
		}
		if groups[i].Leverage != groups[j].Leverage {
			return groups[i].Leverage > groups[j].Leverage
		}
		ti, iOK := parseTime(groups[i].OldestCreatedAt)
		tj, jOK := parseTime(groups[j].OldestCreatedAt)
		if iOK && jOK && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if iOK != jOK {
			return iOK
		}
		return groups[i].Key < groups[j].Key
	})
}

func classRank(g Group) int {
	if g.Reversibility == Irreversible || highBlast(g.BlastRadius) {
		return 0
	}
	switch g.Kind {
	case KindVerdict, KindPromotion, KindContradiction, KindAdjudicate, KindIntake, KindAdvance, KindProductMove:
		return 1
	default:
		return 2
	}
}

func highBlast(blast string) bool {
	switch blast {
	case BlastPublic, BlastDestructive, BlastFinancial, BlastSecurity:
		return true
	default:
		return false
	}
}

func leverage(count int, oldestCreatedAt, operatorUnlock string, now time.Time) float64 {
	if count < 1 {
		count = 1
	}
	if now.IsZero() {
		now = time.Now()
	}
	ageDays := 1.0
	stale := false
	if oldest, ok := parseTime(oldestCreatedAt); ok {
		age := now.UTC().Sub(oldest.UTC())
		if age < 0 {
			age = 0
		}
		ageDays = clamp(age.Hours()/24, 1, 14)
		stale = age > 48*time.Hour
	}
	score := (1 + math.Log2(float64(count))) * ageDays
	if strings.TrimSpace(operatorUnlock) != "" {
		score *= 1.25
	}
	if stale {
		score *= 1.25
	}
	return score
}

func countGroups(groups []Group, fallbackGroups int) Counts {
	counts := Counts{FallbackGroups: fallbackGroups}
	for _, group := range groups {
		switch group.Band {
		case BandNeedsYou:
			counts.NeedsYou += group.Count
			counts.NeedsYouGroups++
			if group.Action != nil && group.Action.Kind != ActionCopyOnly {
				counts.ActionableNow++
			}
			if group.Source == SourceWorkbench || group.Source == SourceExecutor {
				counts.BlockedOnYou += group.Count
			}
		case BandWatching:
			counts.Watching += group.Count
		}
	}
	return counts
}

func fallbackGroupCount(report reviewartifact.QueueReport) int {
	if report.Counts.NeedsYouFallbackGroupCount > 0 {
		return report.Counts.NeedsYouFallbackGroupCount
	}
	count := 0
	for _, group := range report.Groups {
		if group.Band == reviewartifact.BandNeedsYou && group.SummarySource == reviewartifact.SummarySourceFallback {
			count++
		}
	}
	return count
}

func oldestReviewGroupCreatedAt(group reviewartifact.QueueGroup, items []reviewartifact.QueueItem) string {
	ids := map[string]bool{}
	for _, id := range group.ItemIDs {
		ids[id] = true
	}
	oldest := ""
	for _, item := range items {
		if item.Area != group.Area {
			continue
		}
		if len(ids) > 0 && !ids[item.ID] {
			continue
		}
		oldest = olderTimestamp(oldest, item.CreatedAt)
	}
	if oldest == "" && group.Sample != nil {
		oldest = group.Sample.CreatedAt
	}
	return oldest
}

func oldestWorkbenchItemTime(items []work.WorkbenchItem, fallback string) string {
	oldest := ""
	for _, item := range items {
		oldest = olderTimestamp(oldest, item.ClaimedAt)
		for _, obs := range item.RunObservations {
			oldest = olderTimestamp(oldest, obs.GeneratedAt)
		}
		for _, outcome := range item.RouteOutcomes {
			oldest = olderTimestamp(oldest, outcome.CreatedAt)
		}
	}
	return firstNonEmpty(oldest, fallback)
}

func oldestExecutorTime(row work.ExecutorRow, fallback string, now time.Time) string {
	oldest := ""
	for _, ref := range row.CurrentWork {
		oldest = olderTimestamp(oldest, ref.ClaimedAt)
	}
	for _, lease := range row.Leases {
		oldest = olderTimestamp(oldest, lease.ClaimedAt)
		if lease.AgeSeconds > 0 {
			oldest = olderTimestamp(oldest, now.UTC().Add(-time.Duration(lease.AgeSeconds)*time.Second).Format(time.RFC3339))
		}
	}
	for _, job := range row.StalledJobs {
		if job.AgeSeconds > 0 {
			oldest = olderTimestamp(oldest, now.UTC().Add(-time.Duration(job.AgeSeconds)*time.Second).Format(time.RFC3339))
		}
	}
	if row.OldestLeaseAgeSeconds != nil && *row.OldestLeaseAgeSeconds > 0 {
		oldest = olderTimestamp(oldest, now.UTC().Add(-time.Duration(*row.OldestLeaseAgeSeconds)*time.Second).Format(time.RFC3339))
	}
	return firstNonEmpty(oldest, fallback)
}

func filterWorkbenchItems(items []work.WorkbenchItem, keep func(work.WorkbenchItem) bool) []work.WorkbenchItem {
	out := make([]work.WorkbenchItem, 0, len(items))
	for _, item := range items {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}

func workbenchItemCanMove(item work.WorkbenchItem) bool {
	switch strings.TrimSpace(item.State) {
	case "ready", "admitted", "proposed":
		return true
	default:
		return false
	}
}

func workbenchItemNeedsOperator(item work.WorkbenchItem) bool {
	if item.State == "held" || item.State == "blocked" {
		return true
	}
	if item.ActionContext != nil && item.ActionContext.RequiresOperator {
		return true
	}
	for _, card := range item.ReviewCards {
		if card.RequiresOperator {
			return true
		}
	}
	text := workbenchItemRiskText(item)
	if strings.Contains(text, "conflict") || strings.Contains(text, "operator required") || strings.Contains(text, "requires operator") {
		return true
	}
	blast := deriveBlastRadius(Group{OperatorSummary: text}, BlastLocalReversible)
	return blast == BlastExternalSideEffect || highBlast(blast)
}

func workbenchGateRisk(items []work.WorkbenchItem) (string, string) {
	blast := BlastLocalReversible
	reversibility := Reversible
	for _, item := range items {
		candidate := deriveBlastRadius(Group{OperatorSummary: workbenchItemRiskText(item)}, BlastLocalReversible)
		if blastSeverity(candidate) > blastSeverity(blast) {
			blast = candidate
		}
		if deriveReversibility(Group{BlastRadius: candidate, OperatorSummary: workbenchItemRiskText(item)}, Reversible) == Irreversible {
			reversibility = Irreversible
		}
	}
	return blast, reversibility
}

func workbenchItemRiskText(item work.WorkbenchItem) string {
	// Read only the action-bearing fields. Boundaries, stop conditions, and
	// success predicates commonly name forbidden public/external actions (for
	// example "do not publish") and must not turn safe work into a false gate.
	parts := []string{item.CanonicalSurface, item.ReviewTrigger, item.NextAction, item.OpenReason}
	if item.ActionContext != nil {
		ctx := item.ActionContext
		parts = append(parts, ctx.Kind, ctx.SourceRef, ctx.SourceID, ctx.Title, ctx.ActionSummary)
		parts = append(parts, ctx.Command...)
	}
	for _, card := range item.ReviewCards {
		parts = append(parts, card.Kind, card.Title, card.NextAction, card.GateID, card.SourceKind)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func blastSeverity(blast string) int {
	switch blast {
	case BlastSecurity:
		return 6
	case BlastFinancial:
		return 5
	case BlastDestructive:
		return 4
	case BlastPublic:
		return 3
	case BlastExternalSideEffect:
		return 2
	case BlastLocalReversible:
		return 1
	default:
		return 0
	}
}

func workbenchItemsContain(items []work.WorkbenchItem, workID string) bool {
	for _, item := range items {
		if item.WorkID == workID {
			return true
		}
	}
	return false
}

func workbenchItemIDs(items []work.WorkbenchItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.WorkID)
	}
	return out
}

func executorOwnedBurden(row work.ExecutorRow) (int, []string) {
	workbenchHeld := 0
	var out []string
	for _, ref := range row.CurrentWork {
		if ref.State == "held" || ref.State == "blocked" {
			workbenchHeld++
			continue
		}
		if strings.TrimSpace(ref.NeedsMe) != "" {
			out = append(out, ref.WorkID)
		}
	}
	executorNeeds := row.NeedsMeCount - workbenchHeld
	if executorNeeds < 0 {
		executorNeeds = 0
	}
	for _, lease := range row.Leases {
		if row.StaleLeaseCount > 0 {
			out = append(out, lease.WorkID)
		}
	}
	for _, job := range row.StalledJobs {
		out = append(out, firstNonEmpty(job.WorkID, job.JobID))
	}
	return executorNeeds + row.StaleLeaseCount + len(row.StalledJobs), uniqueStrings(out)
}

func firstWorkbenchReason(items []work.WorkbenchItem) string {
	for _, item := range items {
		if strings.TrimSpace(item.NextAction) != "" {
			return item.NextAction
		}
		if strings.TrimSpace(item.OpenReason) != "" {
			return item.OpenReason
		}
	}
	return ""
}

func actionText(action *Action) string {
	if action == nil {
		return ""
	}
	return strings.Join(append([]string{action.ID, action.Label, action.Kind}, action.Argv...), " ")
}

func olderTimestamp(a, b string) string {
	b = strings.TrimSpace(b)
	if b == "" {
		return a
	}
	if a == "" {
		return b
	}
	at, aOK := parseTime(a)
	bt, bOK := parseTime(b)
	switch {
	case aOK && bOK && bt.Before(at):
		return b
	case !aOK && bOK:
		return b
	default:
		return a
	}
}

func parseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func plural(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return itoa(count) + " " + many
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range compactStrings(values) {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func maxInt(values ...int) int {
	max := 0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func clamp(value, lo, hi float64) float64 {
	if value < lo {
		return lo
	}
	if value > hi {
		return hi
	}
	return value
}
