package work

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/envelope"
)

// fakeAppResolver stands in for the identity owner: a surface of
// product:alpha names one app, "both" names two, anything else names none.
func fakeAppResolver(keys AppScopeKeys) AppScopeAnswer {
	switch keys.Surface {
	case "product:alpha":
		return AppScopeAnswer{Scope: envelope.Scope{ProductID: "product:alpha"}, Key: "surface_product", Reason: "surface names product:alpha"}
	case "both":
		return AppScopeAnswer{Ambiguous: true, Candidates: []string{"product:alpha", "product:beta"}, Reason: "two apps match"}
	}
	return AppScopeAnswer{Reason: "no recorded key names an app"}
}

func snapshotWorkHome(t *testing.T, s *Store) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.Walk(s.home, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		out[path] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func setWorkState(t *testing.T, s *Store, w envelope.WorkEnvelope, state string) {
	t.Helper()
	w.State = state
	if err := s.writeJSON(s.workPath(w.ID), &w); err != nil {
		t.Fatal(err)
	}
}

// TestBindScopeBindsOnlyOpenUnclaimedWorkAndDryRunWritesNothing pins the
// backfill: the dry run reads and writes nothing; apply records the app only
// where exactly one app matches, on open work that holds no claim, through a
// same-state lifecycle event that attaches an ActionIntent and an allowed
// PolicyDecision and names the deciding key. Closed, in-flight, ambiguous and
// unresolved records stay byte-identical.
func TestBindScopeBindsOnlyOpenUnclaimedWorkAndDryRunWritesNothing(t *testing.T) {
	s := newStore(t)
	n := 0
	create := func(surface string) envelope.WorkEnvelope {
		n++
		w, err := s.Create(CreateInput{Owner: "codex", SuccessPredicate: fmt.Sprintf("bind %s #%d", surface, n), CanonicalSurface: surface}, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	one, two, none := create("product:alpha"), create("both"), create("/nowhere/in/particular")
	claimed, closed := create("product:alpha"), create("product:alpha")
	setWorkState(t, s, claimed, "claimed")
	setWorkState(t, s, closed, "completed")

	before := snapshotWorkHome(t, s)
	plan, err := s.PlanScopeBinding(fakeAppResolver, "operator", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	after := snapshotWorkHome(t, s)
	if len(after) != len(before) {
		t.Fatalf("dry run wrote files: %d -> %d", len(before), len(after))
	}
	for path, b := range before {
		if !bytes.Equal(after[path], b) {
			t.Fatalf("dry run changed %s", path)
		}
	}
	if plan.Open != 4 || plan.Counts[ScopeBindWouldBind] != 1 || plan.Counts[ScopeBindAmbiguous] != 1 ||
		plan.Counts[ScopeBindNone] != 1 || plan.Counts[ScopeBindInFlight] != 1 || plan.Homes["product:alpha"] != 1 {
		t.Fatalf("plan counts = open %d %v homes %v", plan.Open, plan.Counts, plan.Homes)
	}
	for _, item := range plan.Items {
		if item.WorkID == closed.ID {
			t.Fatalf("closed work entered the plan: %+v", item)
		}
		if item.WorkID == one.ID && (item.PolicyOutcome == "" || item.Key != "surface_product") {
			t.Fatalf("would-bind item = %+v, want the policy outcome and deciding key", item)
		}
	}

	applied, err := s.ApplyScopeBinding(plan, "operator", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Counts[ScopeBindBound] != 1 || applied.Counts[ScopeBindRefused] != 0 {
		t.Fatalf("apply counts = %v (%+v)", applied.Counts, applied.Items)
	}
	got, err := s.Get(one.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope.ProductID != "product:alpha" || got.State != one.State {
		t.Fatalf("bound work = scope %+v state %s", got.Scope, got.State)
	}
	if strings.Join(got.ActionIntentIDs, ",") != strings.Join(one.ActionIntentIDs, ",") || got.PolicyDecisionID != one.PolicyDecisionID {
		t.Fatalf("bind changed the launch generation: %v %s", got.ActionIntentIDs, got.PolicyDecisionID)
	}
	events, err := s.eventsFor(one.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.From != last.To || last.By != "operator" || !strings.Contains(last.Note, "key=surface_product") || len(last.Attached) != 2 {
		t.Fatalf("bind event = %+v, want a same-state event naming the key and attaching intent and decision", last)
	}
	var ai envelope.ActionIntent
	var pd envelope.PolicyDecision
	if err := s.readEventJSON(last.Attached[0], &ai); err != nil || ai.Capability != "work.bind_scope" {
		t.Fatalf("attached action intent %s = %+v (%v)", last.Attached[0], ai, err)
	}
	if err := s.readEventJSON(last.Attached[1], &pd); err != nil || pd.ActionIntentID != ai.ID {
		t.Fatalf("attached policy decision %s = %+v (%v)", last.Attached[1], pd, err)
	}
	for _, w := range []envelope.WorkEnvelope{two, none, claimed, closed} {
		path := s.workPath(w.ID)
		now, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(now, before[path]) {
			t.Fatalf("work %s (%s) changed but must stay as it was", w.ID, w.State)
		}
	}

	again, err := s.PlanScopeBinding(fakeAppResolver, "operator", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if again.Counts[ScopeBindAlreadyRecorded] != 1 || again.Counts[ScopeBindWouldBind] != 0 {
		t.Fatalf("second plan counts = %v, want the bound item left as recorded", again.Counts)
	}
}

// TestBindScopeRefusesARecordChangedSincePlanned: apply rechecks the record's
// hash under the lifecycle lock, so a record changed after the dry run is
// refused rather than bound from stale keys.
func TestBindScopeRefusesARecordChangedSincePlanned(t *testing.T) {
	s := newStore(t)
	w, err := s.Create(CreateInput{Owner: "codex", SuccessPredicate: "bind me", CanonicalSurface: "product:alpha"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanScopeBinding(fakeAppResolver, "operator", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	w.DesiredOutcome = "changed after the plan"
	if err := s.writeJSON(s.workPath(w.ID), &w); err != nil {
		t.Fatal(err)
	}
	applied, err := s.ApplyScopeBinding(plan, "operator", fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Counts[ScopeBindRefused] != 1 {
		t.Fatalf("apply counts = %v, want the changed record refused", applied.Counts)
	}
	got, _ := s.Get(w.ID)
	if got.Scope != (envelope.Scope{}) {
		t.Fatalf("changed record was bound: %+v", got.Scope)
	}
}
