package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"compound/internal/authority"
	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/intent"
	"compound/internal/retrievalparity"
)

const (
	intentUsage       = "usage: compound intent <plan|review|apply> ..."
	intentPlanUsage   = "usage: compound intent plan --source <label> [--text <utterance>] [--task <task>] (--from-proposal <proposal.json>|--model-stdin) [--harness <h>] [--cwd <dir>] [--repo-kind <k>] [--mode <m>] [--topic <slug>] [--body-budget <tokens>] [--by <actor>] [--owner <owner>] [--canonical-surface <uri>] [--json]"
	intentReviewUsage = "usage: compound intent review <create-plan.json|ambiguity-park.json> [--json]"
	intentApplyUsage  = "usage: compound intent apply <create-plan.json> --by <reviewer> [--json]"
)

var materializeRuntimeForOrient = contract.MaterializeRuntime

// materializeOrientContextPack is the one cmd-layer orientation path used by
// both `compound orient` and expressive intent. It compiles and records the
// EffectiveContract, assembles the task-scoped ContextPack through the existing
// recall owner, optionally materializes bodies, then records that exact pack.
func materializeOrientContextPack(homePath string, in contract.Input, bodyBudget int, now time.Time) (envelope.ContextPack, error) {
	restoreEngine, err := configureMemoryEngineFromHome(homePath, "")
	if err != nil {
		return envelope.ContextPack{}, err
	}
	defer restoreEngine()

	orientationPolicy, _, err := contract.LoadPolicy(homePath)
	if err != nil {
		return envelope.ContextPack{}, err
	}
	retrievalStarted := time.Now()
	runtime, materializeErr := materializeRuntimeForOrient(in, orientationPolicy, homePath, now)
	pack := runtime.ContextPack
	retrievalparity.Record(homePath, retrievalparity.NewOrientRow(in.Task, parityOrientInput(in), pack.ActiveMemory, time.Since(retrievalStarted), materializeErr == nil, time.Now()))
	if materializeErr != nil {
		return envelope.ContextPack{}, materializeErr
	}
	ec := runtime.EffectiveContract
	entrypointSource, entrypointRevision, found, err := authority.InstalledHarnessEntrypointBinding(homePath, in.Harness)
	if err != nil {
		return envelope.ContextPack{}, err
	}
	if found {
		pack.SourceRevisions = bindOrientSourceRevision(pack.SourceRevisions, entrypointSource, entrypointRevision)
		ec.SourceRevisions = bindOrientSourceRevision(ec.SourceRevisions, entrypointSource, entrypointRevision)
	}
	if _, err := contract.RecordEffectiveContract(homePath, ec); err != nil {
		return envelope.ContextPack{}, err
	}
	if pack.IDs.ContractID != ec.ID {
		return envelope.ContextPack{}, fmt.Errorf("context pack contract_id %q does not match effective contract %q", pack.IDs.ContractID, ec.ID)
	}
	if bodyBudget > 0 {
		resolver := newOrientBodyResolver(homePath, pack, in.Cwd)
		candidates, err := resolver.Resolve(in.Task)
		if err != nil {
			return envelope.ContextPack{}, err
		}
		pack = contract.ApplyBodyPack(pack, contract.MaterializeBodyPack(candidates, pack, bodyBudget))
	}
	if _, err := contract.RecordContextPack(homePath, pack); err != nil {
		return envelope.ContextPack{}, err
	}
	return pack, nil
}

func bindOrientSourceRevision(revisions []envelope.SourceRev, source, revision string) []envelope.SourceRev {
	bound := append([]envelope.SourceRev(nil), revisions...)
	for i := range bound {
		if bound[i].Source == source {
			bound[i].Rev = revision
			return bound
		}
	}
	return append(bound, envelope.SourceRev{Source: source, Rev: revision})
}

func cmdIntent(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, intentUsage)
		os.Exit(2)
	}
	if hasUsageFlag(args) && len(args) == 1 {
		fmt.Println(intentUsage)
		return
	}
	switch args[0] {
	case "plan":
		cmdIntentPlan(args[1:])
	case "review":
		cmdIntentReview(args[1:])
	case "apply":
		cmdIntentApply(args[1:])
	default:
		fmt.Fprintln(os.Stderr, intentUsage)
		os.Exit(2)
	}
}

func cmdIntentPlan(args []string) {
	valueFlags := map[string]bool{
		"--source": true, "--text": true, "--task": true, "--from-proposal": true,
		"--harness": true, "--cwd": true, "--repo-kind": true, "--mode": true,
		"--topic": true, "--body-budget": true, "--by": true, "--actor": true,
		"--owner": true, "--canonical-surface": true,
	}
	boolFlags := map[string]bool{"--model-stdin": true, "--json": true}
	if hasUsageFlag(args) {
		fmt.Println(intentPlanUsage)
		return
	}
	if err := validateKnownFlags(args, valueFlags, boolFlags); err != nil {
		intentUsageError(err, intentPlanUsage)
	}
	source := strings.TrimSpace(valueFlag(args, "--source"))
	proposalPath := strings.TrimSpace(valueFlag(args, "--from-proposal"))
	modelStdin := hasFlag(args, "--model-stdin")
	if source == "" {
		intentUsageError(fmt.Errorf("--source is required"), intentPlanUsage)
	}
	if (proposalPath == "") == !modelStdin {
		intentUsageError(fmt.Errorf("choose exactly one of --from-proposal or --model-stdin"), intentPlanUsage)
	}

	var utterance []byte
	if hasFlag(args, "--text") {
		utterance = []byte(valueFlag(args, "--text"))
	} else {
		if modelStdin {
			intentUsageError(fmt.Errorf("--text is required when proposal JSON comes from --model-stdin"), intentPlanUsage)
		}
		var err error
		utterance, err = io.ReadAll(os.Stdin)
		die(err)
	}
	if len(utterance) == 0 {
		intentUsageError(fmt.Errorf("utterance bytes are empty"), intentPlanUsage)
	}

	var proposalBytes []byte
	var err error
	if modelStdin {
		proposalBytes, err = io.ReadAll(os.Stdin)
	} else {
		proposalBytes, err = os.ReadFile(proposalPath)
	}
	die(err)
	proposal, err := intent.DecodeProposal(proposalBytes)
	die(err)

	homePath, err := home.Resolve("")
	die(err)
	in, err := parseOrientArgsWithHome(args, homePath)
	die(err)
	in.Harness = firstNonEmpty(in.Harness, "codex")
	in.Task = firstNonEmpty(in.Task, string(utterance))
	in.Mode = firstNonEmpty(in.Mode, "plan")
	bodyBudget, err := parseBodyBudget(args)
	if err != nil {
		intentUsageError(err, intentPlanUsage)
	}
	now := time.Now()
	pack, err := materializeOrientContextPack(homePath, in, bodyBudget, now)
	die(err)

	recalledRefs := append([]string{}, pack.ActiveDecisions...)
	recalledRefs = append(recalledRefs, pack.ActiveMemory...)
	report, err := intent.Plan(homePath, intent.PlanInput{
		Source:           source,
		Utterance:        utterance,
		ContextPackID:    pack.ID,
		RecalledRefs:     recalledRefs,
		Actor:            firstNonEmpty(valueFlag(args, "--by"), valueFlag(args, "--actor"), in.Harness),
		Owner:            firstNonEmpty(valueFlag(args, "--owner"), "compound-core"),
		CanonicalSurface: valueFlag(args, "--canonical-surface"),
		Proposal:         proposal,
	}, now)
	die(err)
	if hasFlag(args, "--json") {
		printJSON(report)
		return
	}
	id := report.CaptureCID
	if report.Plan != nil {
		id = report.Plan.ID
	} else if report.Park != nil {
		id = report.Park.ID
	}
	fmt.Printf("plan %s status=%s capture=%s recall=%drefs\n", id, report.Status, report.CaptureCID, len(recalledRefs))
	if report.PlanPath != "" {
		fmt.Printf("plan_path: %s\n", report.PlanPath)
	}
	if report.ParkPath != "" {
		fmt.Printf("park_path: %s\n", report.ParkPath)
	}
}

func cmdIntentReview(args []string) {
	if hasUsageFlag(args) {
		fmt.Println(intentReviewUsage)
		return
	}
	if err := validateKnownFlags(args, nil, map[string]bool{"--json": true}); err != nil {
		intentUsageError(err, intentReviewUsage)
	}
	path := firstIntentPositional(args)
	if path == "" {
		intentUsageError(fmt.Errorf("plan or park path is required"), intentReviewUsage)
	}
	report, err := intent.Review(path)
	die(err)
	if hasFlag(args, "--json") {
		printJSON(report)
		return
	}
	if report.Plan != nil {
		fmt.Printf("intent review plan=%s work=%s status=%s\n", report.Plan.ID, report.Plan.WorkID, report.Plan.Status)
		return
	}
	fmt.Printf("intent review park=%s status=%s refs=%s\n", report.Park.ID, report.Park.Status, strings.Join(report.Park.AmbiguousRefs, ","))
	for _, question := range report.Park.NamedQuestions {
		fmt.Printf("question: %s\n", question)
	}
}

func cmdIntentApply(args []string) {
	if hasUsageFlag(args) {
		fmt.Println(intentApplyUsage)
		return
	}
	if err := validateKnownFlags(args, map[string]bool{"--by": true}, map[string]bool{"--json": true}); err != nil {
		intentUsageError(err, intentApplyUsage)
	}
	path := firstIntentPositional(args)
	reviewer := strings.TrimSpace(valueFlag(args, "--by"))
	if path == "" || reviewer == "" {
		intentUsageError(fmt.Errorf("create-plan path and --by reviewer are required"), intentApplyUsage)
	}
	homePath, err := home.Resolve("")
	die(err)
	report, err := intent.Apply(homePath, path, reviewer, time.Now())
	die(err)
	if hasFlag(args, "--json") {
		printJSON(report)
		return
	}
	fmt.Printf("intent apply plan=%s status=%s work=%s state=%s\n", report.PlanID, report.Status, report.WorkID, report.WorkState)
}

func firstIntentPositional(args []string) string {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--by":
			i++
		case "--json":
		default:
			if !strings.HasPrefix(args[i], "-") {
				return args[i]
			}
		}
	}
	return ""
}

func intentUsageError(err error, usage string) {
	fmt.Fprintln(os.Stderr, "error:", err)
	fmt.Fprintln(os.Stderr, usage)
	os.Exit(2)
}
