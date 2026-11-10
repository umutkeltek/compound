package main

import (
	"fmt"
	"time"

	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/retrievalparity"
)

var materializeRuntimeForOrient = contract.MaterializeRuntime
var materializeBodiesForOrient = func(homePath string, pack envelope.ContextPack, cwd, task string, bodyBudget int) (envelope.ContextPack, error) {
	if bodyBudget <= 0 {
		return pack, nil
	}
	resolver, err := newOrientBodyResolver(homePath, pack, cwd)
	if err != nil {
		return envelope.ContextPack{}, err
	}
	candidates, err := resolver.Resolve(task)
	if err != nil {
		return envelope.ContextPack{}, err
	}
	if resolver.Degraded() {
		// Surface a single generic config-unavailable candidate so the materializer
		// accounts for it in the omitted ledger rather than the resolver silently
		// dropping the optional body source. The text is empty, the status is
		// unresolvable, the materializer maps it to body_pack.omitted.reason=unresolvable.
		candidates = append(candidates, contract.BodyCandidate{
			Ref:    contract.OrientBodyConfigUnavailableRef,
			Source: "corpus:configuration",
			Rank:   0,
			Status: contract.BodyStatusUnresolvable,
		})
	}
	return contract.ApplyBodyPack(pack, contract.MaterializeBodyPack(candidates, pack, bodyBudget)), nil
}

// orientMaterializationResult is the result of materializeOrientContextPackDetailed.
// EngineDependency traces the exact rented breadth dependency the materializer
// used (or "not_attempted" if the engine is disabled), BodyPackUsedTokens is the
// body_pack.estimated_used_tokens of the returned pack (0 when no body pack),
// Degraded reports whether the optional configured body source emitted any
// degradation candidate during this materialization.
type orientMaterializationResult struct {
	Pack               envelope.ContextPack
	EngineDependency   contract.EngineDependencyTrace
	BodyPackUsedTokens int
	Degraded           bool
}

// materializeOrientContextPackDetailed is the one cmd-layer orientation path
// used by both `compound orient` and the parity replay. It performs the
// shared runtime materialization, optional body materialization, sealing, and
// atomic persistence. Parity replay consumes the result directly; orient uses
// the compatibility wrapper materializeOrientContextPack that returns just the
// pack.
func materializeOrientContextPackDetailed(
	homePath string,
	in contract.Input,
	bodyBudget int,
	now time.Time,
) (orientMaterializationResult, error) {
	restoreEngine, err := configureMemoryEngineFromHome(homePath, "")
	if err != nil {
		return orientMaterializationResult{}, contract.OrientationInitializationError("configure memory reader", err)
	}
	defer restoreEngine()

	orientationPolicy, _, err := contract.LoadPolicy(homePath)
	if err != nil {
		return orientMaterializationResult{}, contract.OrientationInitializationError("load orientation policy", err)
	}
	retrievalStarted := time.Now()
	runtime, materializeErr := materializeRuntimeForOrient(in, orientationPolicy, homePath, now)
	pack := runtime.ContextPack
	retrievalparity.Record(homePath, retrievalparity.NewOrientRow(in.Task, parityOrientInput(in), pack.ActiveMemory, time.Since(retrievalStarted), materializeErr == nil, time.Now()))
	if materializeErr != nil {
		return orientMaterializationResult{}, contract.OrientationInitializationError("materialize runtime context", materializeErr)
	}
	ec := runtime.EffectiveContract
	if pack.IDs.ContractID != ec.ID {
		return orientMaterializationResult{}, fmt.Errorf("context pack contract_id %q does not match effective contract %q", pack.IDs.ContractID, ec.ID)
	}
	degraded := false
	if bodyBudget > 0 {
		pack, err = materializeBodiesForOrient(homePath, pack, in.Cwd, in.Task, bodyBudget)
		if err != nil {
			return orientMaterializationResult{}, contract.OrientationInitializationError("materialize context bodies", err)
		}
		// After materialization, scan the returned pack's body_pack omission ledger
		// for any of the resolver's generic degradation refs. This is how the CLI
		// learns whether the resolver degraded without coupling the parity
		// evaluator to the resolver's ref shapes.
		degraded = contract.OrientBodyDegraded(pack)
	}
	runtime.EffectiveContract = ec
	runtime.ContextPack = pack
	runtime, err = contract.SealRuntimeMaterialization(runtime)
	if err != nil {
		return orientMaterializationResult{}, err
	}
	ec = runtime.EffectiveContract
	pack = runtime.ContextPack
	if _, err := contract.RecordRuntimeMaterialization(homePath, runtime); err != nil {
		return orientMaterializationResult{}, contract.OrientationInitializationError("persist runtime context", err)
	}
	var usedTokens int
	if pack.BodyPack != nil {
		usedTokens = pack.BodyPack.EstimatedUsedTokens
	}
	return orientMaterializationResult{
		Pack:               pack,
		EngineDependency:   runtime.EngineDependencyTrace,
		BodyPackUsedTokens: usedTokens,
		Degraded:           degraded,
	}, nil
}

// materializeOrientContextPack is the one cmd-layer orientation path used by
// `compound orient` and the MCP transport. It materializes the shared
// EffectiveContract/ContextPack runtime pair, optionally adds task-scoped
// bodies to the pack, then atomically persists that exact pair. It is the
// compatibility wrapper around materializeOrientContextPackDetailed; the
// detailed variant carries EngineDependency/Degraded for the parity replay
// evidence path.
func materializeOrientContextPack(homePath string, in contract.Input, bodyBudget int, now time.Time) (envelope.ContextPack, error) {
	result, err := materializeOrientContextPackDetailed(homePath, in, bodyBudget, now)
	if err != nil {
		return envelope.ContextPack{}, err
	}
	return result.Pack, nil
}
