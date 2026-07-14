package envelope

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestReceiptEffectiveChanged(t *testing.T) {
	base := func(moveType, outcome string, changed bool) Receipt {
		return Receipt{
			Header:     hdr(SVReceipt, "rec_effective"),
			Outcome:    outcome,
			MoveType:   moveType,
			StateDelta: StateDelta{Changed: changed, Summary: "summary", FilesChanged: []string{}, Artifacts: []string{}, Verification: []string{}},
			Cost:       ReceiptCost{ModelCalls: []string{}},
			Skills:     ReceiptSkills{Used: []string{}, Missed: []string{}, Candidate: []string{}},
			Memory:     ReceiptMemory{Candidates: []string{}, Promoted: []string{}, Parked: []string{}},
			Next:       ReceiptNext{SuggestedWork: []string{}},
		}
	}

	tests := []struct {
		name string
		rec  Receipt
		want bool
	}{
		{name: "explicit changed", rec: base("audit_only", "completed", true), want: true},
		{name: "completed audit unchanged", rec: base("audit_only", "completed", false), want: false},
		{name: "failed proof unchanged", rec: func() Receipt {
			rec := base("control_plane_repair", "failed", false)
			rec.ProofRefs = []string{"proof:failed"}
			return rec
		}(), want: false},
		{name: "control repair proof", rec: func() Receipt {
			rec := base("control_plane_repair", "completed", false)
			rec.ProofRefs = []string{"proof:repair"}
			return rec
		}(), want: true},
		{name: "rule proof", rec: func() Receipt {
			rec := base("rule_delta", "completed", false)
			rec.ProofRefs = []string{"guard:repeat-failure"}
			return rec
		}(), want: true},
		{name: "memory proof", rec: func() Receipt {
			rec := base("memory_delta", "completed", false)
			rec.Memory.Promoted = []string{"claim_1"}
			return rec
		}(), want: true},
		{name: "outward proof", rec: func() Receipt {
			rec := base("outward_delta", "completed", false)
			rec.ProofRefs = []string{"artifact:handoff"}
			return rec
		}(), want: true},
		{name: "outward proofless unchanged", rec: base("outward_delta", "completed", false), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rec.EffectiveChanged(); got != tt.want {
				t.Fatalf("EffectiveChanged() = %v, want %v", got, tt.want)
			}
		})
	}
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
			Target:        ActionTarget{Kind: "file", Path: "docs/architecture/runtime.md"},
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

func TestWorkEnvelopeGitDeliveryRoundTripsAndLegacyMissingFieldLoads(t *testing.T) {
	const baseSHA = "0123456789abcdef0123456789abcdef01234567"
	const repositoryFingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	repoRoot := filepath.Join(string(filepath.Separator), "repo", "product")
	w := WorkEnvelope{
		Header:           hdr(SVWork, "work_expected_base"),
		Owner:            "compound",
		State:            "ready",
		SuccessPredicate: "candidate lands from its frozen base",
		GitDelivery: &GitDeliveryContract{
			TargetWorktreeRoot:    repoRoot,
			RepositoryFingerprint: repositoryFingerprint,
			TargetLocalRef:        "refs/heads/main",
			ExpectedBaseSHA:       baseSHA,
			PublicationMode:       "required",
			PublicationRef:        "refs/remotes/origin/main",
		},
	}
	if err := RoundTrip(&w); err != nil {
		t.Fatalf("git-delivery work round-trip: %v", err)
	}

	legacyJSON := `{"schema_version":"compound.work.v0.1","id":"work_legacy_base","created_at":"2026-06-28T00:00:00Z","producer":{"kind":"cli","name":"compound"},"authority":{"source_class":"runtime","status":"current"},"privacy":"private","risk":"tier1","evidence_refs":[],"uncertainty":{"level":"known"},"owner":"compound","state":"ready","success_predicate":"legacy remains valid","non_goals":[],"run_ids":[],"artifact_ids":[],"verification_ids":[],"memory_candidate_ids":[]}`
	var legacy WorkEnvelope
	if err := json.Unmarshal([]byte(legacyJSON), &legacy); err != nil {
		t.Fatalf("decode legacy work: %v", err)
	}
	if legacy.GitDelivery != nil {
		t.Fatalf("legacy git_delivery = %+v, want nil", legacy.GitDelivery)
	}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy work without git_delivery must remain valid: %v", err)
	}

	invalid := w
	invalid.ID = "work_invalid_base"
	invalid.GitDelivery = &GitDeliveryContract{
		TargetWorktreeRoot:    repoRoot,
		RepositoryFingerprint: repositoryFingerprint,
		TargetLocalRef:        "refs/heads/main",
		ExpectedBaseSHA:       "ABCDEF",
		PublicationMode:       "required",
		PublicationRef:        "refs/remotes/origin/main",
	}
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "expected_base_sha") {
		t.Fatalf("invalid expected_base_sha should fail validation, got %v", err)
	}

	invalidFingerprint := w
	invalidFingerprint.ID = "work_invalid_repository_fingerprint"
	invalidFingerprint.GitDelivery = &GitDeliveryContract{
		TargetWorktreeRoot:    repoRoot,
		RepositoryFingerprint: "ABCDEF",
		TargetLocalRef:        "refs/heads/main",
		ExpectedBaseSHA:       baseSHA,
		PublicationMode:       "required",
		PublicationRef:        "refs/remotes/origin/main",
	}
	if err := invalidFingerprint.Validate(); err == nil || !strings.Contains(err.Error(), "repository_fingerprint") {
		t.Fatalf("missing or invalid repository fingerprint should fail validation, got %v", err)
	}

	partial := w
	partial.ID = "work_partial_delivery"
	partial.GitDelivery = &GitDeliveryContract{
		TargetWorktreeRoot:    repoRoot,
		RepositoryFingerprint: repositoryFingerprint,
		TargetLocalRef:        "refs/heads/main",
		ExpectedBaseSHA:       baseSHA,
		PublicationMode:       "required",
	}
	if err := partial.Validate(); err == nil || !strings.Contains(err.Error(), "publication_ref") {
		t.Fatalf("publication-required contract without remote-tracking ref should fail, got %v", err)
	}

	localOnly := w
	localOnly.ID = "work_local_only_delivery"
	localOnly.GitDelivery = &GitDeliveryContract{
		TargetWorktreeRoot:    repoRoot,
		RepositoryFingerprint: repositoryFingerprint,
		TargetLocalRef:        "refs/heads/main",
		ExpectedBaseSHA:       baseSHA,
		PublicationMode:       "local_only",
	}
	if err := localOnly.Validate(); err != nil {
		t.Fatalf("explicit local-only contract should validate: %v", err)
	}
}

func TestWorkEnvelopePreFingerprintGitDeliveryIsReadableAndShapeStable(t *testing.T) {
	const legacyJSON = `{"schema_version":"compound.work.v0.1","id":"work_pre_fingerprint","created_at":"2026-07-13T22:00:00Z","producer":{"kind":"cli","name":"compound","version":""},"ids":{},"scope":{},"authority":{"source_class":"runtime","status":"current"},"privacy":"private","risk":"tier1","evidence_refs":[],"uncertainty":{"level":"known"},"owner":"codex","git_delivery":{"target_worktree_root":"/repo/product","target_local_ref":"refs/heads/main","expected_base_sha":"0123456789abcdef0123456789abcdef01234567","publication_mode":"local_only"},"state":"running","success_predicate":"legacy delivery remains readable without changing its hash","non_goals":[],"run_ids":[],"artifact_ids":[],"verification_ids":[],"memory_candidate_ids":[]}`

	var legacy WorkEnvelope
	if err := json.Unmarshal([]byte(legacyJSON), &legacy); err != nil {
		t.Fatalf("decode pre-fingerprint work: %v", err)
	}
	if legacy.GitDelivery == nil || legacy.GitDelivery.RepositoryFingerprint != "" {
		t.Fatalf("pre-fingerprint git_delivery = %+v", legacy.GitDelivery)
	}
	if err := legacy.Validate(); err != nil {
		t.Errorf("pre-fingerprint work must remain readable: %v", err)
	}
	remarshaled, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(remarshaled) != legacyJSON {
		t.Fatalf("pre-fingerprint work shape changed during re-marshal:\n got: %s\nwant: %s", remarshaled, legacyJSON)
	}
	if strings.Contains(string(remarshaled), "repository_fingerprint") {
		t.Fatalf("legacy re-marshal invented repository_fingerprint: %s", remarshaled)
	}
}

func TestWorkEnvelopeGitDeliveryPublicationRefMatchesWholeBranch(t *testing.T) {
	const baseSHA = "0123456789abcdef0123456789abcdef01234567"
	const repositoryFingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	repoRoot := filepath.Join(string(filepath.Separator), "repo", "product")
	tests := []struct {
		name           string
		localRef       string
		publicationRef string
		wantErr        bool
	}{
		{
			name:           "wrong nested publication branch",
			localRef:       "refs/heads/main",
			publicationRef: "refs/remotes/origin/release/main",
			wantErr:        true,
		},
		{
			name:           "nested target branch",
			localRef:       "refs/heads/release/main",
			publicationRef: "refs/remotes/origin/release/main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := WorkEnvelope{
				Header:           hdr(SVWork, "work_publication_branch"),
				Owner:            "compound",
				State:            "ready",
				SuccessPredicate: "candidate reaches the exact publication branch",
				GitDelivery: &GitDeliveryContract{
					TargetWorktreeRoot:    repoRoot,
					RepositoryFingerprint: repositoryFingerprint,
					TargetLocalRef:        tt.localRef,
					ExpectedBaseSHA:       baseSHA,
					PublicationMode:       "required",
					PublicationRef:        tt.publicationRef,
				},
			}
			err := w.Validate()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "publication_ref") {
					t.Fatalf("wrong publication branch should fail validation, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("matching nested publication branch should validate: %v", err)
			}
		})
	}
}

func TestActionIntentTargetRequiredForMutatingActions(t *testing.T) {
	base := ActionIntent{
		Header:        withWork(hdr(SVActionIntent, "act_target"), "work_1"),
		Capability:    "filesystem.write",
		Action:        "write_file",
		MutationClass: "local_reversible",
		Reversibility: "easy",
	}
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "target.kind") || !strings.Contains(err.Error(), "target.id or target.path") {
		t.Fatalf("mutating action without target should fail target identity, got %v", err)
	}
	withPath := base
	withPath.Target = ActionTarget{Kind: "file", Path: "docs/architecture/runtime.md"}
	if err := withPath.Validate(); err != nil {
		t.Fatalf("mutating action with path target should pass: %v", err)
	}
	withID := base
	withID.ID = "act_target2"
	withID.Target = ActionTarget{Kind: "work", ID: "work_1"}
	if err := withID.Validate(); err != nil {
		t.Fatalf("mutating action with id target should pass: %v", err)
	}
	read := base
	read.ID = "act_read"
	read.MutationClass = "read"
	read.Action = "inspect"
	read.Target = ActionTarget{}
	if err := read.Validate(); err != nil {
		t.Fatalf("read action without target should still pass: %v", err)
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
