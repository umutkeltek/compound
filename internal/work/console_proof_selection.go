package work

import (
	"context"
	"fmt"
	"strings"
)

// Console gate selection for the canonical Compound battery.
//
// The console gates (the console type check and every console smoke) are
// opt-in. The local console is not the operator's dashboard, so a battery that
// proves a change outside console/ does not run them (operator decision,
// 2026-11-03). They run when the proved range touches console/, when
// --with-console asks for them, or under --ci-console, which is the console
// subset by definition. The gate definitions and their tests stay; only the
// default selection changed.

// ProofCheckWithConsoleFlag asks a canonical or dry-run proof-check to run the
// console gates even when console/ did not change in the proved range.
const ProofCheckWithConsoleFlag = "--with-console"

// Stable selection reasons.
const (
	ConsoleProofReasonUnchanged = "console_unchanged"
	ConsoleProofReasonNoRange   = "no_base_range"
	ConsoleProofReasonChanged   = "console_changed"
	ConsoleProofReasonRequested = "requested"
)

// ConsoleProofSelection says whether one battery runs the console gates, and why.
type ConsoleProofSelection struct {
	Include bool
	Reason  string
}

// IsConsoleProofGate is the single membership rule: a console gate is a step
// rooted in console/. CIConsoleGateNames pins the same set by name.
func IsConsoleProofGate(step ProofCheckStep) bool {
	return step.Dir == "console"
}

// WithoutConsoleProofGates keeps every non-console step in its original order.
func WithoutConsoleProofGates(steps []ProofCheckStep) []ProofCheckStep {
	out := make([]ProofCheckStep, 0, len(steps))
	for _, step := range steps {
		if !IsConsoleProofGate(step) {
			out = append(out, step)
		}
	}
	return out
}

// ResolveConsoleProofSelection decides from the request and the committed range
// alone. The same (repo, base, head, request) always gives the same answer, so
// a sealed profile and its later re-derivation agree. Renames count on both
// sides: moving a file out of console/ can break console imports.
func ResolveConsoleProofSelection(ctx context.Context, repo, baseRef, headRef string, requested bool) (ConsoleProofSelection, error) {
	if requested {
		return ConsoleProofSelection{Include: true, Reason: ConsoleProofReasonRequested}, nil
	}
	baseRef = strings.TrimSpace(baseRef)
	if baseRef == "" {
		return ConsoleProofSelection{Reason: ConsoleProofReasonNoRange}, nil
	}
	headRef = strings.TrimSpace(headRef)
	if headRef == "" {
		headRef = "HEAD"
	}
	paths, err := canonicalProofGitNULListContext(ctx, repo, "diff", "--no-renames", "--name-only", "-z", baseRef+"..."+headRef, "--", "console")
	if err != nil {
		return ConsoleProofSelection{}, fmt.Errorf("console gate selection for %s...%s: %w", baseRef, headRef, err)
	}
	for _, path := range paths {
		if path == "console" || strings.HasPrefix(path, "console/") {
			return ConsoleProofSelection{Include: true, Reason: ConsoleProofReasonChanged}, nil
		}
	}
	return ConsoleProofSelection{Reason: ConsoleProofReasonUnchanged}, nil
}

// ConsoleSelectedProofInventory narrows the full inventory under one selection.
// The full inventory stays the source every membership guard reads; this is the
// list one run reserves and executes.
func ConsoleSelectedProofInventory(full CanonicalProofInventory, selection ConsoleProofSelection) CanonicalProofInventory {
	return func(baseRef string, headRefs ...string) ([]ProofCheckStep, error) {
		steps, err := full(baseRef, headRefs...)
		if err != nil || selection.Include {
			return steps, err
		}
		return WithoutConsoleProofGates(steps), nil
	}
}

// ConsoleAwareProofInventory resolves the selection from the range the
// inventory is asked for. A run that already holds a sealed plan never calls it.
func ConsoleAwareProofInventory(ctx context.Context, repo string, requested bool, full CanonicalProofInventory) CanonicalProofInventory {
	return func(baseRef string, headRefs ...string) ([]ProofCheckStep, error) {
		headRef := ""
		if len(headRefs) > 0 {
			headRef = headRefs[0]
		}
		selection, err := ResolveConsoleProofSelection(ctx, repo, baseRef, headRef, requested)
		if err != nil {
			return nil, err
		}
		return ConsoleSelectedProofInventory(full, selection)(baseRef, headRefs...)
	}
}

type consoleProofRequestKey struct{}

// ContextWithConsoleProofRequest carries --with-console from argv to the
// non-certifying runner without widening every runner signature.
func ContextWithConsoleProofRequest(ctx context.Context, requested bool) context.Context {
	if !requested {
		return ctx
	}
	return context.WithValue(ctx, consoleProofRequestKey{}, true)
}

// ConsoleProofRequested reports whether the context carries --with-console.
func ConsoleProofRequested(ctx context.Context) bool {
	requested, _ := ctx.Value(consoleProofRequestKey{}).(bool)
	return requested
}
