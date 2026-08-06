package main

import (
	"fmt"

	browsercli "compound/internal/browser"
	gamecli "compound/internal/game/cli"
	"compound/internal/policy"
	schedulecli "compound/internal/schedule"
	terminalcli "compound/internal/terminal/cli"
)

type compoundCommand struct {
	Admission               policy.FirstPartyCommandAdmission
	SubcommandAdmission     map[string]policy.FirstPartyCommandAdmission
	SubcommandFlagAdmission map[string]map[string]policy.FirstPartyCommandAdmission
	Run                     func([]string)
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

func (command compoundCommand) withSubcommandFlagAdmission(
	subcommand string,
	flag string,
	admission policy.FirstPartyCommandAdmission,
) compoundCommand {
	if command.SubcommandFlagAdmission == nil {
		command.SubcommandFlagAdmission = map[string]map[string]policy.FirstPartyCommandAdmission{}
	}
	if command.SubcommandFlagAdmission[subcommand] == nil {
		command.SubcommandFlagAdmission[subcommand] = map[string]policy.FirstPartyCommandAdmission{}
	}
	command.SubcommandFlagAdmission[subcommand][flag] = admission
	return command
}

// cmdVersion prints the human version string, or — with --json — the structured version report
// install verification parses. The structured form exists because a version STRING is
// precomputable: comparing one is exactly the probe a stub answers correctly (see
// internal/arch/installverify.go).
func cmdVersion(args []string) {
	if !hasFlag(args, "--json") {
		fmt.Println(versionString())
		return
	}
	printJSON(versionReport(valueFlag(args, "--challenge")))
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
		"version":   readCommand(cmdVersion),
		"--version": readCommand(cmdVersion),
		"-v":        readCommand(cmdVersion),
		"distill":   readCommand(retiredCommand("distill")),
		"scope":     readCommand(retiredCommand("scope")),
		"promote":   readCommand(retiredCommand("promote")),
		"build":     readCommand(retiredCommand("build")),
		"reconcile": readCommand(retiredCommand("reconcile")),
		"timeline":  readCommand(retiredCommand("timeline")),
		"recall":    readCommand(retiredCommand("recall")),
		"pack":      readCommand(retiredCommand("pack")),
		"ledger":    readCommand(retiredCommand("ledger")),
		"authority": governedCommand(cmdAuthority),
		"decisions": heldCommand(cmdDecisions),
		"director":  heldCommand(cmdDirector),
		"jobprobe":  heldCommand(cmdJobProbe),
		"runtime": heldCommand(cmdRuntime).
			withSubcommandAdmission("doctor", policy.FirstPartyCommandGoverned),
		"projection":  heldCommand(cmdProjection),
		"system":      heldCommand(cmdSystem),
		"product":     heldCommand(cmdProduct),
		"source":      heldCommand(cmdSource),
		"console":     heldCommand(cmdConsole),
		"event":       heldCommand(cmdEvent),
		"event-store": heldCommand(cmdEventStore),
		"writeback":   heldCommand(cmdWriteback),
		"home": heldCommand(cmdHome).
			withSubcommandAdmission("doctor", policy.FirstPartyCommandRead).
			withSubcommandAdmission("where", policy.FirstPartyCommandRead).
			withSubcommandAdmission("verify", policy.FirstPartyCommandRead).
			withSubcommandAdmission("init", policy.FirstPartyCommandGoverned),
		"store": heldCommand(cmdStore).
			withSubcommandAdmission("doctor", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("status", policy.FirstPartyCommandRead).
			withSubcommandAdmission("init", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("migrate", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("sync", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("embed", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("recall", policy.FirstPartyCommandRead),
		"envelope": heldCommand(cmdEnvelope),
		"schema":   heldCommand(cmdSchema),
		"contract": heldCommand(cmdContract).
			withSubcommandAdmission("hooks-authority", policy.FirstPartyCommandRead).
			withSubcommandAdmission("compile", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("import-hooks", policy.FirstPartyCommandGoverned),
		"orient":    governedCommand(cmdOrient),
		"work":      governedCommand(cmdWork),
		"packets":   heldCommand(cmdPackets),
		"workbench": readCommand(cmdWorkbench),
		"coordination": readCommand(cmdCoordination).
			withSubcommandAdmission("measure", policy.FirstPartyCommandGoverned),
		"attention": readCommand(cmdAttention),
		"activity":  heldCommand(cmdActivity),
		"terminal": readCommand(terminalcli.Run).
			withSubcommandAdmission("doctor", policy.FirstPartyCommandRead).
			withSubcommandAdmission("wire", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("unwire", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("hooks", policy.FirstPartyCommandRead).
			withSubcommandAdmission("workspace", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("feed", policy.FirstPartyCommandGoverned),
		"narrate":   readCommand(cmdNarrate),
		"stream":    readCommand(cmdStream),
		"executors": readCommand(cmdExecutors),
		"presence": readCommand(cmdPresence).
			withSubcommandAdmission("announce", policy.FirstPartyCommandGoverned),
		"proof-check":        governedCommand(cmdProofCheck),
		"doctor":             readCommand(cmdDoctor),
		"retention":          readCommand(cmdRetention),
		"run-attempt":        heldCommand(cmdRunAttempt).withSubcommandAdmission("list", policy.FirstPartyCommandRead),
		"experiential-smoke": heldCommand(cmdExperientialSmoke),
		"release":            heldCommand(cmdRelease),
		"reviews":            heldCommand(cmdReviews),
		"finding":            heldCommand(cmdFinding),
		"policy":             heldCommand(cmdPolicy),
		"placement":          readCommand(cmdPlacement),
		"hook": governedCommand(cmdHook).
			withSubcommandAdmission("install", policy.FirstPartyCommandInstall).
			withSubcommandFlagAdmission("install", "--check", policy.FirstPartyCommandRead),
		"harness": governedCommand(cmdHarness).
			withSubcommandAdmission("posture-plan", policy.FirstPartyCommandRead),
		"transition": readCommand(cmdTransition),
		"route": governedCommand(cmdRoute).
			withSubcommandAdmission("evidence-authority", policy.FirstPartyCommandRead).
			withSubcommandAdmission("execution-profile", policy.FirstPartyCommandRead).
			withSubcommandAdmission("experiment-plan", policy.FirstPartyCommandRead).
			withSubcommandAdmission("lane-projection", policy.FirstPartyCommandRead).
			withSubcommandAdmission("model-capabilities", policy.FirstPartyCommandRead),
		"profiles": heldCommand(cmdProfiles).
			withSubcommandAdmission("check", policy.FirstPartyCommandRead).
			withSubcommandAdmission("generate", policy.FirstPartyCommandGoverned),
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
		"architecture": heldCommand(cmdArchitecture).
			withSubcommandAdmission("doctor", policy.FirstPartyCommandRead),
		"schedule": readCommand(schedulecli.Run).
			withSubcommandAdmission("list", policy.FirstPartyCommandRead).
			withSubcommandAdmission("show", policy.FirstPartyCommandRead).
			withSubcommandAdmission("doctor", policy.FirstPartyCommandRead),
		"browser": readCommand(browsercli.Run).
			withSubcommandAdmission("list", policy.FirstPartyCommandRead).
			withSubcommandAdmission("show", policy.FirstPartyCommandRead).
			withSubcommandAdmission("doctor", policy.FirstPartyCommandRead).
			withSubcommandFlagAdmission("doctor", "--fix", policy.FirstPartyCommandGoverned),
		"game": readCommand(gamecli.Run).
			withSubcommandAdmission("doctor", policy.FirstPartyCommandRead).
			withSubcommandAdmission("observe", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("genres", policy.FirstPartyCommandRead).
			withSubcommandAdmission("companies", policy.FirstPartyCommandRead).
			withSubcommandAdmission("trending", policy.FirstPartyCommandRead).
			withSubcommandAdmission("scout", policy.FirstPartyCommandRead).
			withSubcommandAdmission("draft", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("unity", policy.FirstPartyCommandGoverned).
			withSubcommandAdmission("release", policy.FirstPartyCommandGoverned),
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
		for subcommand, flagAdmissions := range command.SubcommandFlagAdmission {
			for flag, admission := range flagAdmissions {
				catalog = catalog.WithSubcommandFlagAdmission(name, subcommand, flag, admission)
			}
		}
	}
	return catalog
}
