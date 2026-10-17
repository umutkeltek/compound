package work

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPrivateSourcePublishCLISeparatesReadOnlyPlanningFromExactApply(t *testing.T) {
	for _, tc := range []struct {
		args            []string
		valid, writable bool
	}{
		{[]string{"--work", "work_fixture", "--actor", "codex:fixture", "--repo", "/synthetic/source", "--review-action", "act_fixture", "--json"}, true, false},
		{[]string{"--apply", "--request", "request.json", "--json"}, true, true},
		{[]string{"--request", "request.json"}, false, false},
		{[]string{"--apply"}, false, false},
		{[]string{"--apply", "--request", "request.json", "--repo", "/override"}, false, false},
		{[]string{"--apply", "--request", "one.json", "--request", "two.json"}, false, false},
		{[]string{"--apply", "--request", "request.json", "--force"}, false, false},
		{[]string{"--apply", "--request", "request.json", "--", "push"}, false, false},
	} {
		_, err := parsePrivateSourcePublishCLI(tc.args)
		if (err == nil) != tc.valid || PrivateSourcePublishCLIRequiresWritableStore(tc.args) != tc.writable {
			t.Fatalf("publication parsing/admission drift: %q %v", tc.args, err)
		}
		if !tc.valid {
			var out, errs bytes.Buffer
			if code := RunPrivateSourcePublishCLI(nil, tc.args, &out, &errs); code != 2 || out.Len() != 0 {
				t.Fatalf("invalid command reached store: code=%d", code)
			}
		}
	}
}

func TestPrivateSourcePublishCLIProducesConcretePlanWithoutPublishing(t *testing.T) {
	p := newPrivatePublisherFixture(t, false)
	var out, errs bytes.Buffer
	code := RunPrivateSourcePublishCLI(p.f.store, []string{"--work", p.f.workID, "--actor", proofActorAlpha, "--repo", p.f.repo, "--review-action", p.f.action.ID, "--json"}, &out, &errs)
	if code != 0 || p.pushes != 0 {
		t.Fatalf("planning mutated destination: %d %s", code, errs.String())
	}
	var request PrivateSourcePublishRequest
	if err := json.Unmarshal(out.Bytes(), &request); err != nil || request.Action.ID == "" || request.ExpectedRemoteSHA != p.tip || !strings.HasPrefix(request.ProofRef, "proof-check:pfrs_") {
		t.Fatalf("invalid concrete plan: %+v %v", request, err)
	}
	if _, err := p.f.store.Get(p.f.workID); err != nil {
		t.Fatal(err)
	}
}
