package observation

import (
	"sort"
	"strings"
	"time"
)

const (
	SourceConnectionsProjectionSchemaVersion = "compound.source_connections_projection.v0.1"
	SourceConnectionsAuthorityRef            = "compound://sources/connections"
)

type SourceConnectionsProjection struct {
	SchemaVersion string                  `json:"schema_version"`
	AuthorityRef  string                  `json:"authority_ref"`
	Status        string                  `json:"status"`
	GeneratedAt   string                  `json:"generated_at"`
	Counts        SourceConnectionsCounts `json:"counts"`
	Connections   []SourceConnection      `json:"connections"`
	NextCommand   []string                `json:"next_command"`
}

type SourceConnectionsCounts struct {
	Total          int `json:"total"`
	Connected      int `json:"connected"`
	NeedsInput     int `json:"needs_input"`
	NeedsReview    int `json:"needs_review"`
	Stale          int `json:"stale"`
	NotConnected   int `json:"not_connected"`
	ActionRequired int `json:"action_required"`
	PrivateSetup   int `json:"private_setup"`
}

type SourceConnection struct {
	ID                 string                   `json:"id"`
	Label              string                   `json:"label"`
	Status             string                   `json:"status"`
	SourceKinds        []string                 `json:"source_kinds,omitempty"`
	SourceKind         string                   `json:"source_kind,omitempty"`
	SourceRef          string                   `json:"source_ref,omitempty"`
	LastSeen           string                   `json:"last_seen,omitempty"`
	Produces           []string                 `json:"produces,omitempty"`
	NextSafeAction     string                   `json:"next_safe_action"`
	PrivateSetupNeeded bool                     `json:"private_setup_needed,omitempty"`
	RowCount           int                      `json:"row_count"`
	LatestCardCount    int                      `json:"latest_card_count"`
	LatestCards        []SourceIntakeActionCard `json:"latest_cards,omitempty"`
}

type sourceConnectionDef struct {
	ID                 string
	Label              string
	SourceKinds        []string
	DefaultStatus      string
	Produces           []string
	NextSafeAction     string
	PrivateSetupNeeded bool
}

func LoadSourceConnectionsProjection(homePath string, now time.Time) (SourceConnectionsProjection, error) {
	if now.IsZero() {
		now = time.Now()
	}
	intake, err := LoadSourceIntakeProjection(homePath, now, 0)
	if err != nil {
		return SourceConnectionsProjection{}, err
	}

	defs := sourceConnectionDefs()
	byID := map[string]*SourceConnection{}
	kindToID := map[string]string{}
	defaultStatusByID := map[string]string{}
	for _, def := range defs {
		conn := &SourceConnection{
			ID:                 def.ID,
			Label:              def.Label,
			SourceKinds:        append([]string{}, def.SourceKinds...),
			Produces:           append([]string{}, def.Produces...),
			NextSafeAction:     def.NextSafeAction,
			PrivateSetupNeeded: def.PrivateSetupNeeded,
		}
		byID[def.ID] = conn
		defaultStatusByID[def.ID] = def.DefaultStatus
		for _, kind := range def.SourceKinds {
			kindToID[strings.TrimSpace(kind)] = def.ID
		}
	}

	for _, row := range intake.Rows {
		id := kindToID[strings.TrimSpace(row.SourceKind)]
		if id == "" {
			id = "source_" + slugPart(row.SourceKind)
			if _, ok := byID[id]; !ok {
				byID[id] = &SourceConnection{
					ID:             id,
					Label:          titleFromSlug(row.SourceKind),
					Status:         "observed",
					SourceKinds:    []string{row.SourceKind},
					Produces:       producesForSourceRow(row),
					NextSafeAction: "Review latest intake row and route it through the existing source intake command.",
				}
			}
		}
		conn := byID[id]
		conn.RowCount++
		conn.SourceKind = firstNonEmpty(conn.SourceKind, row.SourceKind)
		conn.SourceRef = firstNonEmpty(conn.SourceRef, row.SourceRef)
		conn.LastSeen = newestTimestamp(conn.LastSeen, firstNonEmpty(row.FreshnessAt, row.SourceCapturedAt, row.GeneratedAt))
		conn.Produces = compact(append(conn.Produces, producesForSourceRow(row)...))
		conn.PrivateSetupNeeded = conn.PrivateSetupNeeded || row.Privacy == "private" || row.Status == RouteBlockedPrivate
		if len(row.ActionCards) > 0 {
			conn.LatestCards = append(conn.LatestCards, row.ActionCards...)
			conn.LatestCardCount += len(row.ActionCards)
			if conn.NextSafeAction == "" || sourceConnectionStatusRank(conn.Status) < sourceConnectionStatusRank("needs_review") {
				conn.NextSafeAction = row.ActionCards[0].Title
			}
		}
		conn.Status = strongerSourceConnectionStatus(conn.Status, sourceConnectionRowStatus(conn.ID, row, now))
		if conn.NextSafeAction == "" {
			conn.NextSafeAction = sourceConnectionNextAction(conn.Status)
		}
	}

	out := SourceConnectionsProjection{
		SchemaVersion: SourceConnectionsProjectionSchemaVersion,
		AuthorityRef:  SourceConnectionsAuthorityRef,
		Status:        "ok",
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		NextCommand:   []string{"compound", "source", "connections", "--json"},
	}
	for _, conn := range byID {
		conn.Produces = compact(conn.Produces)
		if conn.Status == "" {
			conn.Status = firstNonEmpty(defaultStatusByID[conn.ID], "not_connected")
		}
		if conn.NextSafeAction == "" {
			conn.NextSafeAction = sourceConnectionNextAction(conn.Status)
		}
		sort.SliceStable(conn.LatestCards, func(i, j int) bool {
			if conn.LatestCards[i].Severity != conn.LatestCards[j].Severity {
				return sourceSeverityRank(conn.LatestCards[i].Severity) > sourceSeverityRank(conn.LatestCards[j].Severity)
			}
			return conn.LatestCards[i].ID < conn.LatestCards[j].ID
		})
		if len(conn.LatestCards) > 3 {
			conn.LatestCards = conn.LatestCards[:3]
		}
		out.Connections = append(out.Connections, *conn)
	}
	sort.SliceStable(out.Connections, func(i, j int) bool {
		if sourceConnectionStatusRank(out.Connections[i].Status) != sourceConnectionStatusRank(out.Connections[j].Status) {
			return sourceConnectionStatusRank(out.Connections[i].Status) > sourceConnectionStatusRank(out.Connections[j].Status)
		}
		if out.Connections[i].LastSeen != out.Connections[j].LastSeen {
			return out.Connections[i].LastSeen > out.Connections[j].LastSeen
		}
		return out.Connections[i].ID < out.Connections[j].ID
	})
	out.Counts.Total = len(out.Connections)
	for _, conn := range out.Connections {
		switch conn.Status {
		case "connected", "observed":
			out.Counts.Connected++
		case "blocked_private_config", "needs_private_input", "ready_preflight":
			out.Counts.NeedsInput++
		case "needs_review", "needs_mapping":
			out.Counts.NeedsReview++
		case "stale":
			out.Counts.Stale++
		case "not_connected":
			out.Counts.NotConnected++
		}
		if conn.LatestCardCount > 0 {
			out.Counts.ActionRequired++
		}
		if conn.PrivateSetupNeeded {
			out.Counts.PrivateSetup++
		}
	}
	return out, nil
}

func sourceConnectionDefs() []sourceConnectionDef {
	return []sourceConnectionDef{
		{
			ID:                 "asc_daily_signals",
			Label:              "ASC daily signals",
			SourceKinds:        []string{"app_store_connect"},
			DefaultStatus:      "blocked_private_config",
			Produces:           []string{"product signal", "product loop evidence"},
			NextSafeAction:     "Run the read-only ASC preflight with private product mapping config.",
			PrivateSetupNeeded: true,
		},
		{
			ID:             "google_search_console",
			Label:          "Google Search Console",
			SourceKinds:    []string{"google_search_console"},
			DefaultStatus:  "not_connected",
			Produces:       []string{"product signal", "search signal"},
			NextSafeAction: "Observe a bounded GSC export through source intake.",
		},
		{
			ID:                 "chatgpt_export",
			Label:              "ChatGPT export",
			SourceKinds:        []string{"chatgpt", "chatgpt_export", "conversation_export"},
			DefaultStatus:      "ready_preflight",
			Produces:           []string{"knowledge evidence", "memory candidate"},
			NextSafeAction:     "Run the conversation import preflight on a bounded private export.",
			PrivateSetupNeeded: true,
		},
		{
			ID:             "github_stars",
			Label:          "GitHub stars",
			SourceKinds:    []string{"github_stars", "github_starred_repos"},
			DefaultStatus:  "not_connected",
			Produces:       []string{"tool candidate", "research reference"},
			NextSafeAction: "Connect a read-only stars export before synthesis.",
		},
		{
			ID:             "x_bookmarks",
			Label:          "X bookmarks",
			SourceKinds:    []string{"x_bookmarks", "twitter_bookmarks"},
			DefaultStatus:  "not_connected",
			Produces:       []string{"reference", "design taste example", "product idea"},
			NextSafeAction: "Import a bounded bookmarks export as source material, not memory.",
		},
		{
			ID:             "postiz",
			Label:          "Postiz",
			SourceKinds:    []string{"postiz", "postiz_metrics"},
			DefaultStatus:  "not_connected",
			Produces:       []string{"distribution signal", "social metric"},
			NextSafeAction: "Connect read-only post/channel metrics before any publishing flow.",
		},
		{
			ID:             "codex_sessions",
			Label:          "Codex sessions",
			SourceKinds:    []string{"codex", "codex_session", "codex_sessions"},
			DefaultStatus:  "not_connected",
			Produces:       []string{"session evidence", "skill candidate"},
			NextSafeAction: "Run a small session-mining dry run before broad import.",
		},
		{
			ID:             "claude_sessions",
			Label:          "Claude Code sessions",
			SourceKinds:    []string{"claude", "claude_code", "claude_sessions"},
			DefaultStatus:  "not_connected",
			Produces:       []string{"session evidence", "skill candidate"},
			NextSafeAction: "Run a small session-mining dry run before broad import.",
		},
	}
}

func sourceConnectionRowStatus(connectionID string, row SourceIntakeRow, now time.Time) string {
	if row.Status == RouteBlockedPrivate {
		if connectionID == "asc_daily_signals" {
			return "blocked_private_config"
		}
		return "needs_private_input"
	}
	if row.ActionRequired {
		return "needs_review"
	}
	if row.Status == RouteUnsupportedKind {
		return "needs_mapping"
	}
	if sourceIntakeRowIsStale(row, now) {
		return "stale"
	}
	switch row.Status {
	case "applied", "review_recorded":
		return "connected"
	case "skipped":
		return "needs_review"
	case "planned":
		return "observed"
	default:
		return firstNonEmpty(row.Status, "observed")
	}
}

func strongerSourceConnectionStatus(a, b string) string {
	if sourceConnectionStatusRank(b) > sourceConnectionStatusRank(a) {
		return b
	}
	return a
}

func sourceConnectionStatusRank(status string) int {
	switch status {
	case "blocked_private_config", "needs_private_input":
		return 90
	case "ready_preflight":
		return 80
	case "needs_review", "needs_mapping":
		return 70
	case "stale":
		return 60
	case "connected":
		return 40
	case "observed":
		return 30
	case "not_connected":
		return 10
	default:
		return 20
	}
}

func sourceSeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func producesForSourceRow(row SourceIntakeRow) []string {
	switch row.Route {
	case RouteProductSignal:
		return []string{"product signal", "product loop evidence"}
	case RouteKnowledgeExport:
		return []string{"knowledge evidence", "memory candidate"}
	case RouteSourceObservation:
		return []string{"source observation", "candidate evidence"}
	case RouteRouteEvidence:
		return []string{"route evidence"}
	case RouteTraceEnvelope:
		return []string{"event trace", "workflow evidence"}
	case RouteCandidateOnly:
		return []string{"review candidate"}
	default:
		if row.SubjectKind != "" {
			return []string{row.SubjectKind + " evidence"}
		}
		return nil
	}
}

func sourceConnectionNextAction(status string) string {
	switch status {
	case "blocked_private_config", "needs_private_input":
		return "Supply private setup through runtime config or keep the source visibly blocked."
	case "ready_preflight":
		return "Run the read-only preflight before importing source material."
	case "needs_review":
		return "Resolve the latest source action card."
	case "needs_mapping":
		return "Map the source to an existing owner before import."
	case "stale":
		return "Refresh the source or keep the stale state visible with reason."
	case "connected", "observed":
		return "Review the latest source rows and continue through existing cards."
	default:
		return "Connect a bounded read-only export before automation."
	}
}

func newestTimestamp(a, b string) string {
	if strings.TrimSpace(a) == "" {
		return strings.TrimSpace(b)
	}
	if strings.TrimSpace(b) == "" {
		return strings.TrimSpace(a)
	}
	at, aerr := time.Parse(time.RFC3339, a)
	bt, berr := time.Parse(time.RFC3339, b)
	if aerr == nil && berr == nil {
		if bt.After(at) {
			return b
		}
		return a
	}
	if b > a {
		return b
	}
	return a
}

func titleFromSlug(value string) string {
	parts := strings.Fields(strings.ReplaceAll(strings.TrimSpace(value), "_", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	if len(parts) == 0 {
		return "Unknown source"
	}
	return strings.Join(parts, " ")
}

func slugPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "unknown"
	}
	return out
}
