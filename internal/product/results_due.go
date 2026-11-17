package product

import (
	"sort"
	"strings"
	"time"

	"compound/internal/mem"
)

// ResultDue is one hypothesis the doctor holds as pending review: it waits
// for a verdict (did the change work?). SignalIDs lists the signals that match
// it (ReviewReady); a hypothesis can be due with none, when its window passed
// and no signal matched its metrics.
type ResultDue struct {
	// ProductID is the identity owner's one name for the app.
	ProductID          string   `json:"product_id"`
	HypothesisID       string   `json:"hypothesis_id"`
	ChangeEvent        string   `json:"change_event,omitempty"`
	ExpectedEffect     string   `json:"expected_effect"`
	ObservationWindow  string   `json:"observation_window,omitempty"`
	CreatedAt          string   `json:"created_at,omitempty"`
	SignalIDs          []string `json:"signal_ids"`
	ReviewPlanCommands []string `json:"review_plan_commands,omitempty"`
}

// ResultsDue lists every hypothesis across all products that the doctor holds
// as pending review, read through the same doctor and product folding that
// Authority uses. It is a pure read: it writes, approves and reviews nothing,
// and it keeps placeholder drafts in the list (ExpectedEffect tells a caller
// whether anyone wrote the draft; see HypothesisDraftExpectedEffectPlaceholderPrefix).
// A nil store reads no claims: product spellings are then not folded into
// the identity owner's one name, and no memory lock file is taken or created.
func ResultsDue(homePath string, store *mem.Store, now time.Time) ([]ResultDue, error) {
	productIDs, err := authorityProductIDs(homePath, store)
	if err != nil {
		return nil, err
	}
	var out []ResultDue
	for _, group := range foldAuthorityProducts(store, productIDs) {
		seen := map[string]int{}
		var rows []ResultDue
		for _, spelling := range group.spellings {
			doctor, err := DoctorWithNow(homePath, spelling, now)
			if err != nil {
				return nil, err
			}
			for _, h := range doctor.PendingReview {
				id := strings.TrimSpace(h.ID)
				if _, ok := seen[id]; ok || id == "" {
					continue
				}
				seen[id] = len(rows)
				rows = append(rows, ResultDue{ProductID: group.id, HypothesisID: id, ChangeEvent: h.ChangeEvent, ExpectedEffect: h.ExpectedEffect, ObservationWindow: h.ObservationWindow, CreatedAt: h.CreatedAt})
			}
			for _, ready := range doctor.ReviewReady {
				if i, ok := seen[strings.TrimSpace(ready.HypothesisID)]; ok {
					rows[i].SignalIDs = append(rows[i].SignalIDs, ready.SignalID)
					rows[i].ReviewPlanCommands = append(rows[i].ReviewPlanCommands, ready.ReviewPlanCommand)
				}
			}
		}
		for _, row := range rows {
			row.SignalIDs = compact(row.SignalIDs)
			row.ReviewPlanCommands = compact(row.ReviewPlanCommands)
			if row.SignalIDs == nil {
				row.SignalIDs = []string{}
			}
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ProductID != out[j].ProductID {
			return out[i].ProductID < out[j].ProductID
		}
		return out[i].HypothesisID < out[j].HypothesisID
	})
	return out, nil
}
