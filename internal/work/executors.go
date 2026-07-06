package work

import (
	"sort"
	"strings"
	"time"
)

const ExecutorsProjectionSchemaVersion = "compound.executors_projection.v0.1"
const ExecutorsAuthorityRef = "compound://executors/projection"

// ExecutorsProjection is the read-only executor board source. It groups
// Workbench and lease facts under Compound-owned executor profile rows; health
// remains source-labeled until a real harness probe feeds this surface.
type ExecutorsProjection struct {
	SchemaVersion string         `json:"schema_version"`
	AuthorityRef  string         `json:"authority_ref"`
	Status        string         `json:"status"`
	GeneratedAt   string         `json:"generated_at"`
	Counts        ExecutorCounts `json:"counts"`
	Executors     []ExecutorRow  `json:"executors"`
	SourceRefs    []string       `json:"source_refs"`
	NextCommand   []string       `json:"next_command"`
	Integrity     []string       `json:"integrity,omitempty"`
}

type ExecutorCounts struct {
	Total        int `json:"total"`
	Open         int `json:"open"`
	Running      int `json:"running"`
	Ready        int `json:"ready"`
	NeedsMe      int `json:"needs_me"`
	Assigned     int `json:"assigned"`
	Leases       int `json:"leases"`
	ProfileGuide int `json:"profile_guide"`
}

type ExecutorRow struct {
	ID             string             `json:"id"`
	Label          string             `json:"label"`
	Status         string             `json:"status"`
	StatusSource   string             `json:"status_source"`
	Tone           string             `json:"tone,omitempty"`
	Aliases        []string           `json:"aliases,omitempty"`
	BestFor        []string           `json:"best_for,omitempty"`
	CanUseTools    []string           `json:"can_use_tools,omitempty"`
	CostLimits     []string           `json:"cost_limits,omitempty"`
	AssignedCount  int                `json:"assigned_count"`
	RunningCount   int                `json:"running_count"`
	ReadyCount     int                `json:"ready_count"`
	NeedsMeCount   int                `json:"needs_me_count"`
	LeaseCount     int                `json:"lease_count"`
	CurrentWork    []ExecutorWorkRef  `json:"current_work,omitempty"`
	Leases         []ExecutorLeaseRef `json:"leases,omitempty"`
	LastSuccess    *ExecutorWorkRef   `json:"last_success,omitempty"`
	LastFailure    *ExecutorWorkRef   `json:"last_failure,omitempty"`
	NextAssigned   *ExecutorWorkRef   `json:"next_assigned,omitempty"`
	ProjectionNote string             `json:"projection_note,omitempty"`
}

type ExecutorWorkRef struct {
	WorkID           string `json:"work_id"`
	State            string `json:"state"`
	Owner            string `json:"owner,omitempty"`
	ClaimedBy        string `json:"claimed_by,omitempty"`
	ClaimedAt        string `json:"claimed_at,omitempty"`
	Title            string `json:"title"`
	NextAction       string `json:"next_action,omitempty"`
	MoveType         string `json:"move_type,omitempty"`
	ReceiptID        string `json:"receipt_id,omitempty"`
	TransitionID     string `json:"transition_id,omitempty"`
	CanonicalSurface string `json:"canonical_surface,omitempty"`
}

type ExecutorLeaseRef struct {
	WorkID    string `json:"work_id"`
	By        string `json:"by"`
	ClaimedAt string `json:"claimed_at"`
}

type executorProfile struct {
	ID             string
	Label          string
	Aliases        []string
	Status         string
	StatusSource   string
	Tone           string
	BestFor        []string
	CanUseTools    []string
	CostLimits     []string
	ProjectionNote string
}

func (s *Store) ExecutorsProjection(now time.Time) (ExecutorsProjection, error) {
	if now.IsZero() {
		now = time.Now()
	}
	workbench, err := s.Workbench(now, WorkbenchOptions{RecentLimit: 8})
	if err != nil {
		return ExecutorsProjection{}, err
	}
	leases, err := s.ListLeases()
	if err != nil {
		return ExecutorsProjection{}, err
	}

	projection := ExecutorsProjection{
		SchemaVersion: ExecutorsProjectionSchemaVersion,
		AuthorityRef:  ExecutorsAuthorityRef,
		Status:        "ok",
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		SourceRefs:    []string{"compound.workbench", "compound.work.leases", "compound.executor.profile_guide"},
		NextCommand:   []string{"compound", "executors", "--json"},
		Integrity:     append([]string{}, workbench.Integrity...),
	}

	rows := map[string]*ExecutorRow{}
	for _, profile := range executorProfiles() {
		row := ExecutorRow{
			ID:             profile.ID,
			Label:          profile.Label,
			Status:         profile.Status,
			StatusSource:   profile.StatusSource,
			Tone:           profile.Tone,
			Aliases:        append([]string{}, profile.Aliases...),
			BestFor:        append([]string{}, profile.BestFor...),
			CanUseTools:    append([]string{}, profile.CanUseTools...),
			CostLimits:     append([]string{}, profile.CostLimits...),
			ProjectionNote: profile.ProjectionNote,
		}
		rows[row.ID] = &row
		projection.Counts.ProfileGuide++
	}

	runningStates := map[string]bool{"claimed": true, "running": true, "in_progress": true, "verifying": true}
	readyStates := map[string]bool{"ready": true, "admitted": true, "proposed": true}
	needsMeStates := map[string]bool{"held": true, "blocked": true}
	for _, item := range workbench.Open {
		row := rows[executorIDForActor(executorActor(item))]
		if row == nil {
			row = rows["unassigned"]
		}
		ref := executorWorkRef(item)
		row.AssignedCount++
		row.CurrentWork = append(row.CurrentWork, ref)
		projection.Counts.Assigned++
		switch {
		case runningStates[item.State]:
			row.RunningCount++
			projection.Counts.Running++
		case readyStates[item.State]:
			row.ReadyCount++
			projection.Counts.Ready++
		case needsMeStates[item.State]:
			row.NeedsMeCount++
			projection.Counts.NeedsMe++
		}
	}

	for _, lease := range leases {
		row := rows[executorIDForActor(lease.By)]
		if row == nil {
			row = rows["unassigned"]
		}
		row.LeaseCount++
		row.Leases = append(row.Leases, ExecutorLeaseRef{
			WorkID:    lease.WorkID,
			By:        lease.By,
			ClaimedAt: lease.ClaimAt,
		})
		projection.Counts.Leases++
	}

	for _, item := range workbench.Recent {
		row := rows[executorIDForActor(executorActor(item))]
		if row == nil {
			row = rows["unassigned"]
		}
		ref := executorWorkRef(item)
		switch item.State {
		case "completed":
			if row.LastSuccess == nil {
				row.LastSuccess = &ref
			}
		case "failed":
			if row.LastFailure == nil {
				row.LastFailure = &ref
			}
		}
	}

	for _, row := range rows {
		sort.SliceStable(row.CurrentWork, func(i, j int) bool {
			leftRank := executorStateRank(row.CurrentWork[i].State)
			rightRank := executorStateRank(row.CurrentWork[j].State)
			if leftRank != rightRank {
				return leftRank > rightRank
			}
			if row.CurrentWork[i].ClaimedAt != row.CurrentWork[j].ClaimedAt {
				return row.CurrentWork[i].ClaimedAt > row.CurrentWork[j].ClaimedAt
			}
			return row.CurrentWork[i].WorkID < row.CurrentWork[j].WorkID
		})
		sort.SliceStable(row.Leases, func(i, j int) bool {
			if row.Leases[i].ClaimedAt != row.Leases[j].ClaimedAt {
				return row.Leases[i].ClaimedAt > row.Leases[j].ClaimedAt
			}
			return row.Leases[i].WorkID < row.Leases[j].WorkID
		})
		for _, work := range row.CurrentWork {
			if readyStates[work.State] {
				next := work
				row.NextAssigned = &next
				break
			}
		}
		projection.Executors = append(projection.Executors, *row)
	}
	sort.SliceStable(projection.Executors, func(i, j int) bool {
		return executorProfileRank(projection.Executors[i].ID) < executorProfileRank(projection.Executors[j].ID)
	})

	projection.Counts.Total = len(projection.Executors)
	projection.Counts.Open = workbench.Counts.Open
	return projection, nil
}

func executorProfiles() []executorProfile {
	return []executorProfile{
		{
			ID:           "codex",
			Label:        "Codex",
			Aliases:      []string{"codex"},
			Status:       "available",
			StatusSource: "profile_guide",
			Tone:         "green",
			BestFor:      []string{"code edits", "tests", "local proof", "repo changes"},
			CanUseTools:  []string{"terminal", "files", "browser checks when needed"},
			CostLimits:   []string{"subscription lane", "proof before claim"},
		},
		{
			ID:           "claude_code",
			Label:        "Claude Code",
			Aliases:      []string{"claude", "claude_code", "claude-code"},
			Status:       "manual",
			StatusSource: "profile_guide",
			Tone:         "blue",
			BestFor:      []string{"architecture review", "larger refactors", "prose-heavy synthesis"},
			CanUseTools:  []string{"native Claude Code tool lane"},
			CostLimits:   []string{"subscription lane", "assign intentionally"},
		},
		{
			ID:             "local_lm_studio",
			Label:          "Local LM Studio",
			Aliases:        []string{"lm-studio", "local", "local_model"},
			Status:         "not_checked",
			StatusSource:   "profile_guide",
			Tone:           "gray",
			BestFor:        []string{"cheap private extraction when a model is loaded"},
			CanUseTools:    []string{"model API only"},
			CostLimits:     []string{"local watts", "context limits"},
			ProjectionNote: "availability requires a future model-health probe",
		},
		{
			ID:             "switchback_api",
			Label:          "Switchback/API",
			Aliases:        []string{"switchback", "api", "route_gateway"},
			Status:         "route_evidence_only",
			StatusSource:   "profile_guide",
			Tone:           "violet",
			BestFor:        []string{"provider routing", "cheap extraction", "route evidence"},
			CanUseTools:    []string{"OpenAI-compatible gateway"},
			CostLimits:     []string{"check registry before unattended use"},
			ProjectionNote: "route evidence is not task-success proof",
		},
		{
			ID:           "scripts",
			Label:        "Scripts",
			Aliases:      []string{"script", "scripts", "compound", "compound reviews"},
			Status:       "available",
			StatusSource: "profile_guide",
			Tone:         "green",
			BestFor:      []string{"deterministic preflights", "smoke tests", "projections"},
			CanUseTools:  []string{"local commands only"},
			CostLimits:   []string{"no model spend"},
		},
		{
			ID:           "unassigned",
			Label:        "Unassigned",
			Aliases:      []string{"", "unassigned"},
			Status:       "waiting",
			StatusSource: "workbench",
			Tone:         "orange",
			BestFor:      []string{"ready work that still needs an executor choice"},
			CanUseTools:  []string{"none until claimed"},
			CostLimits:   []string{"assign manually or Continue when safe"},
		},
	}
}

func executorActor(item WorkbenchItem) string {
	return firstNonEmptyExecutorString(item.ClaimedBy, item.Owner)
}

func executorIDForActor(actor string) string {
	normalized := normalizeExecutorAlias(actor)
	for _, profile := range executorProfiles() {
		for _, alias := range profile.Aliases {
			alias = normalizeExecutorAlias(alias)
			if alias == "" && normalized == "" {
				return profile.ID
			}
			if alias != "" && strings.Contains(normalized, alias) {
				return profile.ID
			}
		}
	}
	return "unassigned"
}

func executorWorkRef(item WorkbenchItem) ExecutorWorkRef {
	return ExecutorWorkRef{
		WorkID:           item.WorkID,
		State:            item.State,
		Owner:            item.Owner,
		ClaimedBy:        item.ClaimedBy,
		ClaimedAt:        item.ClaimedAt,
		Title:            executorWorkTitle(item),
		NextAction:       item.NextAction,
		MoveType:         item.MoveType,
		ReceiptID:        item.ReceiptID,
		TransitionID:     item.TransitionID,
		CanonicalSurface: item.CanonicalSurface,
	}
}

func executorWorkTitle(item WorkbenchItem) string {
	if strings.HasPrefix(item.CanonicalSurface, "packet:") {
		return strings.ReplaceAll(strings.TrimPrefix(item.CanonicalSurface, "packet:"), "-", " ")
	}
	if item.ActionContext != nil && strings.TrimSpace(item.ActionContext.Title) != "" {
		return item.ActionContext.Title
	}
	return firstNonEmptyExecutorString(item.SuccessPredicate, item.DesiredOutcome, item.WorkID)
}

func executorStateRank(state string) int {
	switch state {
	case "running", "in_progress", "verifying", "claimed":
		return 50
	case "ready", "admitted":
		return 40
	case "proposed":
		return 30
	case "held", "blocked":
		return 20
	default:
		return 10
	}
}

func executorProfileRank(id string) int {
	switch id {
	case "codex":
		return 10
	case "claude_code":
		return 20
	case "local_lm_studio":
		return 30
	case "switchback_api":
		return 40
	case "scripts":
		return 50
	case "unassigned":
		return 60
	default:
		return 90
	}
}

func normalizeExecutorAlias(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

func firstNonEmptyExecutorString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
