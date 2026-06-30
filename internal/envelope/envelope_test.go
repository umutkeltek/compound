package envelope

import "testing"

func hdr(sv, id string) Header {
	return Header{
		SchemaVersion: sv,
		ID:            id,
		CreatedAt:     "2026-06-28T00:00:00Z",
		Producer:      Producer{Kind: "cli", Name: "compound"},
		Authority:     Authority{SourceClass: "runtime", Status: "current"},
		Privacy:       "private",
		Risk:          "tier0",
		EvidenceRefs:  []string{},
		Uncertainty:   Uncertainty{Level: "known"},
	}
}

func withWork(h Header, wid string) Header {
	h.IDs.WorkID = wid
	return h
}

func TestAllKindsValidateAndRoundTrip(t *testing.T) {
	envs := []Envelope{
		&EffectiveContract{Header: hdr(SVEffectiveContract, "contract_1"), Harness: "claude_code"},
		&ContextRequest{Header: hdr(SVContextRequest, "ctxreq_1"), Harness: "claude_code", RequestedMode: "execute"},
		&ContextPack{
			Header: hdr(SVContextPack, "ctx_1"),
			Included: []IncludedRef{{
				Ref:         "claim_1",
				SourceClass: "evidence",
				Status:      "current",
				Confidence:  0.9,
			}},
			Excluded: []ExcludedRef{},
		},
		&WorkEnvelope{Header: hdr(SVWork, "work_1"), Owner: "compound", State: "ready", SuccessPredicate: "a state delta exists"},
		&ActionIntent{
			Header:        withWork(hdr(SVActionIntent, "act_1"), "work_1"),
			Capability:    "filesystem.write",
			Action:        "write_file",
			MutationClass: "local_reversible",
			Reversibility: "easy",
		},
		&PolicyDecision{Header: hdr(SVPolicyDecision, "pol_1"), ActionIntentID: "act_1", Outcome: "allow"},
		&HookEvent{Header: hdr(SVHookEvent, "hook_1"), Harness: "claude_code", HookName: "SessionStart", Status: "ok"},
		&TraceEnvelope{
			Header: hdr(SVTraceEnvelope, "trace_1"),
			Source: TraceSource{
				System:     "os-event",
				Stream:     "control-events",
				SourceID:   "evt_1",
				SourceKind: "snapshot.generated",
			},
			MutationClass:   "read",
			ToolsModelsUsed: []string{},
			FilesTouched:    []string{},
			Outputs:         []string{},
			Cost:            ReceiptCost{ModelCalls: []string{}},
		},
		&Receipt{Header: hdr(SVReceipt, "rec_1"), Outcome: "completed", MoveType: "memory_delta", Memory: ReceiptMemory{Promoted: []string{"claim_1"}}},
		&Transition{Header: withWork(hdr(SVTransition, "tr_1"), "work_1")},
	}
	if len(envs) != len(registry) {
		t.Fatalf("covered %d kinds, registry has %d; kind is untested", len(envs), len(registry))
	}
	for _, e := range envs {
		if err := e.Validate(); err != nil {
			t.Errorf("%s: should be valid: %v", e.Kind(), err)
		}
		if err := RoundTrip(e); err != nil {
			t.Errorf("%s: round-trip: %v", e.Kind(), err)
		}
	}
}

func TestValidateBytesDetectsAndRejects(t *testing.T) {
	good := `{"schema_version":"compound.work.v0.1","id":"work_x","created_at":"2026-06-28T00:00:00Z","authority":{"source_class":"runtime"},"privacy":"private","risk":"tier1","uncertainty":{"level":"known"},"owner":"compound","state":"ready","success_predicate":"x"}`
	if kind, err := ValidateBytes([]byte(good)); err != nil || kind != "work" {
		t.Fatalf("good work: kind=%q err=%v", kind, err)
	}
	cases := map[string]string{
		"bad state enum":      `{"schema_version":"compound.work.v0.1","id":"work_x","created_at":"t","authority":{"source_class":"runtime"},"privacy":"private","risk":"tier1","uncertainty":{"level":"known"},"owner":"c","state":"frobnicate","success_predicate":"x"}`,
		"bad id prefix":       `{"schema_version":"compound.work.v0.1","id":"nope","created_at":"t","authority":{"source_class":"runtime"},"privacy":"private","risk":"tier1","uncertainty":{"level":"known"},"owner":"c","state":"ready","success_predicate":"x"}`,
		"missing required":    `{"schema_version":"compound.work.v0.1","id":"work_x","created_at":"t","authority":{"source_class":"runtime"},"privacy":"private","risk":"tier1","uncertainty":{"level":"known"},"state":"ready"}`,
		"bad source_class":    `{"schema_version":"compound.work.v0.1","id":"work_x","created_at":"t","authority":{"source_class":"made_up"},"privacy":"private","risk":"tier1","uncertainty":{"level":"known"},"owner":"c","state":"ready","success_predicate":"x"}`,
		"unknown schema":      `{"schema_version":"compound.nope.v9"}`,
		"action no work_id":   `{"schema_version":"compound.action_intent.v0.1","id":"act_x","created_at":"t","authority":{"source_class":"runtime"},"privacy":"private","risk":"tier2","uncertainty":{"level":"known"},"capability":"git.commit","mutation_class":"local_reversible","reversibility":"easy"}`,
		"obligations missing": `{"schema_version":"compound.policy_decision.v0.1","id":"pol_x","created_at":"t","authority":{"source_class":"runtime"},"privacy":"private","risk":"tier1","uncertainty":{"level":"known"},"action_intent_id":"act_x","outcome":"allow_with_obligations","obligations":[]}`,
	}
	for name, js := range cases {
		if _, err := ValidateBytes([]byte(js)); err == nil {
			t.Errorf("%s: expected rejection, got none", name)
		}
	}
}
