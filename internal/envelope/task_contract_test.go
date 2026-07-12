package envelope

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLegacyEnvelopesRemainValidWithoutTaskContract(t *testing.T) {
	contract := EffectiveContract{
		Header:             hdr(SVEffectiveContract, "contract_legacy_task"),
		Harness:            "codex",
		ActiveInstructions: []string{},
		ActiveHooks:        []string{},
		ActiveDecisions:    []string{},
		Excluded:           []ExcludedRef{},
		SourceRevisions:    []SourceRev{},
		Unknowns:           []string{},
		Conflicts:          []string{},
	}
	pack := ContextPack{
		Header:             hdr(SVContextPack, "ctx_legacy_task"),
		SummaryMD:          "legacy context",
		Included:           []IncludedRef{},
		Excluded:           []ExcludedRef{},
		ActiveInstructions: []string{},
		ActiveHooks:        []string{},
		ActiveDecisions:    []string{},
		ActiveMemory:       []string{},
		SourceRevisions:    []SourceRev{},
		RequiredChecks:     []string{},
		Avoid:              []string{},
	}
	for name, value := range map[string]Envelope{"contract": &contract, "pack": &pack} {
		if err := value.Validate(); err != nil {
			t.Fatalf("legacy %s invalid: %v", name, err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "task_contract") {
			t.Fatalf("legacy %s unexpectedly serialized empty task_contract: %s", name, data)
		}
	}
}

func TestTaskContractRejectsInvalidModeAndProofMedium(t *testing.T) {
	err := (TaskContract{Mode: "delegate", ProofMedium: "report"}).Validate()
	if err == nil || !strings.Contains(err.Error(), "mode") || !strings.Contains(err.Error(), "proof_medium") {
		t.Fatalf("invalid task contract error = %v", err)
	}
}

func TestEffectiveContractRejectsConflictingTaskModes(t *testing.T) {
	contract := EffectiveContract{
		Header:       hdr(SVEffectiveContract, "contract_conflicting_task_mode"),
		Harness:      "codex",
		TaskMode:     "execute",
		TaskContract: TaskContract{Mode: "review"},
	}
	if err := contract.Validate(); err == nil || !strings.Contains(err.Error(), "task_mode") {
		t.Fatalf("conflicting task modes error = %v", err)
	}
}
