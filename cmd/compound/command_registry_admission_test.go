package main

import (
	"testing"

	"compound/internal/policy"
)

func TestFirstPartyCommandAdmissionComesFromCLIRuntimeRegistry(t *testing.T) {
	t.Parallel()

	catalog := firstPartyCommandCatalog()
	request := policy.ToolActionRequest{
		Tool:               "Bash",
		Arguments:          map[string]any{"command": "compound work delegate work_example --harness codex --dry-run"},
		FirstPartyCommands: catalog,
	}

	got := policy.ClassifyToolAction(request)
	if !got.Known || got.Operation != "governed_command" || got.MutationClass != "local_reversible" {
		t.Fatalf("registered work command must enter the governed first-party lane: %+v", got)
	}

	withoutWork := catalog.Without("work")
	request.FirstPartyCommands = withoutWork
	got = policy.ClassifyToolAction(request)
	if got.Known || got.MutationClass != "security" {
		t.Fatalf("removing the CLI registry row must remove admission automatically: %+v", got)
	}
}

func TestFirstPartyCommandAdmissionStillFailsClosedOutsideRegistry(t *testing.T) {
	t.Parallel()

	request := policy.ToolActionRequest{
		Tool:               "Bash",
		Arguments:          map[string]any{"command": "compound definitely-not-a-command --confirm"},
		FirstPartyCommands: firstPartyCommandCatalog(),
	}
	got := policy.ClassifyToolAction(request)
	if got.Known || got.MutationClass != "security" {
		t.Fatalf("unknown first-party command escaped the security floor: %+v", got)
	}
}

func TestFirstPartyCommandCatalogMatchesEveryRuntimeCommand(t *testing.T) {
	t.Parallel()

	catalog := firstPartyCommandCatalog()
	for name, command := range compoundCommandRegistry() {
		got, ok := catalog.Lookup(name)
		if !ok {
			t.Fatalf("runtime command %q has no admission metadata", name)
		}
		if got != command.Admission {
			t.Fatalf("runtime command %q admission = %q, catalog = %q", name, command.Admission, got)
		}
	}
}
