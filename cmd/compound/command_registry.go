package main

import (
	"fmt"

	"compound/internal/policy"
)

type compoundCommand struct {
	Admission           policy.FirstPartyCommandAdmission
	SubcommandAdmission map[string]policy.FirstPartyCommandAdmission
	Run                 func([]string)
}

func registeredCommand(
	admission policy.FirstPartyCommandAdmission,
	run func([]string),
) compoundCommand {
	return compoundCommand{Admission: admission, Run: run}
}

func heldCommand(run func([]string)) compoundCommand {
	return registeredCommand(policy.FirstPartyCommandHold, run)
}

func readCommand(run func([]string)) compoundCommand {
	return registeredCommand(policy.FirstPartyCommandRead, run)
}

func governedCommand(run func([]string)) compoundCommand {
	return registeredCommand(policy.FirstPartyCommandGoverned, run)
}

func (command compoundCommand) withSubcommandAdmission(
	subcommand string,
	admission policy.FirstPartyCommandAdmission,
) compoundCommand {
	if command.SubcommandAdmission == nil {
		command.SubcommandAdmission = map[string]policy.FirstPartyCommandAdmission{}
	}
	command.SubcommandAdmission[subcommand] = admission
	return command
}

func cmdVersion([]string) {
	fmt.Println(versionString())
}

func retiredCommand(name string) func([]string) {
	return func([]string) {
		cmdRetiredGen1(name)
	}
}

// compoundCommandRegistry is both executable dispatch and admission metadata.
// A command is therefore impossible to expose without choosing its outer
// posture. "hold" is the safe default for commands whose internal governance
// has not yet earned automatic first-party admission.
func compoundCommandRegistry() map[string]compoundCommand {
	return map[string]compoundCommand{
		"version":            readCommand(cmdVersion),
		"--version":          readCommand(cmdVersion),
		"-v":                 readCommand(cmdVersion),
		"distill":            readCommand(retiredCommand("distill")),
		"scope":              readCommand(retiredCommand("scope")),
		"promote":            readCommand(retiredCommand("promote")),
		"build":              readCommand(retiredCommand("build")),
		"reconcile":          readCommand(retiredCommand("reconcile")),
		"timeline":           readCommand(retiredCommand("timeline")),
		"recall":             readCommand(retiredCommand("recall")),
		"pack":               readCommand(retiredCommand("pack")),
		"ledger":             readCommand(retiredCommand("ledger")),
		"authority":          governedCommand(cmdAuthority),
		"decisions":          heldCommand(cmdDecisions),
		"director":           heldCommand(cmdDirector),
		"jobprobe":           heldCommand(cmdJobProbe),
		"runtime":            heldCommand(cmdRuntime),
		"projection":         heldCommand(cmdProjection),
		"system":             heldCommand(cmdSystem),
		"product":            heldCommand(cmdProduct),
		"source":             heldCommand(cmdSource),
		"console":            heldCommand(cmdConsole),
		"event":              heldCommand(cmdEvent),
		"event-store":        heldCommand(cmdEventStore),
		"writeback":          heldCommand(cmdWriteback),
		"home":               heldCommand(cmdHome),
		"envelope":           heldCommand(cmdEnvelope),
		"schema":             heldCommand(cmdSchema),
		"contract":           heldCommand(cmdContract),
		"orient":             readCommand(cmdOrient),
		"work":               governedCommand(cmdWork),
		"packets":            heldCommand(cmdPackets),
		"workbench":          readCommand(cmdWorkbench),
		"coordination":       readCommand(cmdCoordination),
		"attention":          readCommand(cmdAttention),
		"activity":           heldCommand(cmdActivity),
		"narrate":            readCommand(cmdNarrate),
		"stream":             readCommand(cmdStream),
		"executors":          readCommand(cmdExecutors),
		"proof-check":        governedCommand(cmdProofCheck),
		"doctor":             readCommand(cmdDoctor),
		"retention":          readCommand(cmdRetention),
		"run-attempt":        heldCommand(cmdRunAttempt),
		"experiential-smoke": heldCommand(cmdExperientialSmoke),
		"release":            heldCommand(cmdRelease),
		"reviews":            heldCommand(cmdReviews),
		"finding":            heldCommand(cmdFinding),
		"policy":             heldCommand(cmdPolicy),
		"placement":          readCommand(cmdPlacement),
		"hook": governedCommand(cmdHook).
			withSubcommandAdmission("install", policy.FirstPartyCommandInstall),
		"harness": governedCommand(cmdHarness).
			withSubcommandAdmission("posture-plan", policy.FirstPartyCommandRead),
		"transition":      readCommand(cmdTransition),
		"route":           readCommand(cmdRoute),
		"profiles":        heldCommand(cmdProfiles),
		"memory":          heldCommand(cmdMemory),
		"corpus":          heldCommand(cmdCorpus),
		"synthesis":       heldCommand(cmdSynthesis),
		"knowledge":       heldCommand(cmdKnowledge),
		"sessions":        heldCommand(cmdSessions),
		"distiller":       heldCommand(cmdDistiller),
		"capability":      heldCommand(cmdCapability),
		"bench":           heldCommand(cmdBench),
		"latent-tax":      heldCommand(cmdLatentTax),
		"behavioral-lens": heldCommand(cmdBehavioralLens),
		"executor-lens":   heldCommand(cmdExecutorLens),
		"lens-modulate":   heldCommand(cmdLensModulate),
		"act":             heldCommand(cmdAct),
		"actuator":        heldCommand(cmdActuator),
		"capture":         heldCommand(cmdCapture),
		"intent":          heldCommand(cmdIntent),
		"land-lease":      governedCommand(cmdLandLease),
		"panel":           heldCommand(cmdPanel),
		"local-models":    heldCommand(cmdLocalModels),
		"lanebeliefs":     heldCommand(cmdLaneBeliefs),
		"mcp-transport":   heldCommand(cmdMCPTransport),
		"negatives":       heldCommand(cmdNegatives),
		"architecture":    heldCommand(cmdArchitecture),
	}
}

func firstPartyCommandCatalog() policy.FirstPartyCommandCatalog {
	registry := compoundCommandRegistry()
	commands := make(map[string]policy.FirstPartyCommandAdmission, len(registry))
	for name, command := range registry {
		commands[name] = command.Admission
	}
	catalog := policy.NewFirstPartyCommandCatalog(commands)
	for name, command := range registry {
		if len(command.SubcommandAdmission) > 0 {
			catalog = catalog.WithSubcommands(name, command.SubcommandAdmission)
		}
	}
	return catalog
}
