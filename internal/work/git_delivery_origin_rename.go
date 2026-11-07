package work

// Origin-rename migration for a bound git_delivery repository identity.
//
// git_delivery.repository_fingerprint commits to three parts: the object
// format, the lineage roots at the bound expected base, and the canonical
// origin (internal/authority/gitrepository/identity.go). Renaming the hosted
// repository changes only the origin, yet every open Work bound before the
// rename then stops matching and is refused wherever the binding is checked.
//
// The migration is a same-state advance carrying the exact existing
// git_delivery fields plus the origin the Work was bound under. It re-reads
// the identity at the bound base and rebinds only when that earlier origin,
// combined with the object format and lineage roots read now, reproduces the
// bound fingerprint exactly. Because the fingerprint is a commitment, that
// equality proves the format and lineage did not change; a different
// repository at the same path cannot reproduce it and is refused. The check is
// mechanical and the same door undoes it (rename back, name the newer origin),
// so an executor may run it (Law 16). The pre-fingerprint backfill and every
// first binding stay operator-only in preflightGitDeliveryBinding.

import (
	"fmt"
	"strings"

	"compound/internal/authority/gitrepository"
	"compound/internal/envelope"
)

// RepositoryOriginRename is the old and new repository identity of one
// governed origin-rename migration.
type RepositoryOriginRename struct {
	PreviousFingerprint string `json:"previous_fingerprint"`
	PreviousOrigin      string `json:"previous_origin"`
	Fingerprint         string `json:"fingerprint"`
	Origin              string `json:"origin"`
}

func (r RepositoryOriginRename) note() string {
	return fmt.Sprintf("origin renamed: repository fingerprint %s (origin %s) -> %s (origin %s)", r.PreviousFingerprint, r.PreviousOrigin, r.Fingerprint, r.Origin)
}

func (s *Store) preflightOriginRename(w envelope.WorkEnvelope, to string, requested *envelope.GitDeliveryContract, previousOrigin string) (*envelope.GitDeliveryContract, *RepositoryOriginRename, error) {
	if requested == nil {
		return nil, nil, fmt.Errorf("--previous-origin requires the exact existing git_delivery flags (--target-worktree-root, --target-local-ref, --expected-base-sha, and --publication-ref or --local-only)")
	}
	if w.GitDelivery == nil || gitDeliveryRequiresRepositoryIdentityMigration(w.GitDelivery) {
		return nil, nil, fmt.Errorf("work %s has no bound repository fingerprint to migrate; --previous-origin only follows an origin rename of an existing binding", w.ID)
	}
	if terminalStates[w.State] {
		return nil, nil, fmt.Errorf("work is terminal in state %q; cannot migrate git_delivery", w.State)
	}
	if to != w.State {
		return nil, nil, fmt.Errorf("origin migration requires a same-state advance, got %s -> %s", w.State, to)
	}
	candidate := w
	contract := *requested
	candidate.ExpectedBaseSHA = ""
	candidate.GitDelivery = &contract
	if err := s.normalizeGitDeliveryForPlan(&candidate); err != nil {
		return nil, nil, err
	}
	normalized := *candidate.GitDelivery
	bound := *w.GitDelivery
	if !sameGitDeliveryAuthorityExceptFingerprint(bound, normalized) {
		return nil, nil, fmt.Errorf("work %s origin migration must exactly match the existing git_delivery fields; only the repository fingerprint may change", w.ID)
	}
	current, err := gitrepository.ReadIdentity(normalized.TargetWorktreeRoot, normalized.ExpectedBaseSHA)
	if err != nil {
		return nil, nil, fmt.Errorf("git_delivery.repository_fingerprint: %w", err)
	}
	if current.Fingerprint == bound.RepositoryFingerprint {
		return nil, nil, fmt.Errorf("work %s repository fingerprint is unchanged under origin %q; nothing to migrate", w.ID, current.RemoteIdentity)
	}
	previous, err := current.WithOrigin(previousOrigin)
	if err != nil {
		return nil, nil, err
	}
	if previous.Fingerprint != bound.RepositoryFingerprint {
		return nil, nil, fmt.Errorf("work %s origin migration refused: object format %s and lineage roots %s at base %s under previous origin %q do not reproduce bound fingerprint %s; either the lineage differs (a different repository at %s) or %q is not the origin the work was bound under; rebinding to a different repository is not allowed",
			w.ID, current.ObjectFormat, strings.Join(current.LineageRoots, ","), normalized.ExpectedBaseSHA, previous.RemoteIdentity, bound.RepositoryFingerprint, normalized.TargetWorktreeRoot, previous.RemoteIdentity)
	}
	return &normalized, &RepositoryOriginRename{
		PreviousFingerprint: bound.RepositoryFingerprint,
		PreviousOrigin:      previous.RemoteIdentity,
		Fingerprint:         normalized.RepositoryFingerprint,
		Origin:              current.RemoteIdentity,
	}, nil
}

// repositoryFingerprintChangedError is the refusal for a bound fingerprint
// the repository no longer reproduces. It keeps the historical message and
// names the origin-rename migration, so a Work stranded by a renamed origin
// carries its next action instead of a dead end.
type repositoryFingerprintChangedError struct {
	work    envelope.WorkEnvelope
	current gitrepository.Identity
}

func (e *repositoryFingerprintChangedError) Error() string {
	return fmt.Sprintf("git_delivery repository fingerprint %s no longer matches bound fingerprint %s; %s", e.current.Fingerprint, e.work.GitDelivery.RepositoryFingerprint, e.nextAction())
}

func (e *repositoryFingerprintChangedError) nextAction() string {
	return fmt.Sprintf("if only the origin was renamed (now %q), run: %s --previous-origin <origin before the rename>", e.current.RemoteIdentity, gitDeliveryAdvanceCommand(e.work, "<lease holder>"))
}

// gitDeliveryAdvanceCommand renders the same-state advance that restates a
// Work's exact git_delivery fields, the shape both identity migrations take.
func gitDeliveryAdvanceCommand(w envelope.WorkEnvelope, actor string) string {
	contract := w.GitDelivery
	command := fmt.Sprintf("compound work advance %s --to %s --by %s --target-worktree-root %q --target-local-ref %s --expected-base-sha %s",
		w.ID, w.State, actor, contract.TargetWorktreeRoot, contract.TargetLocalRef, contract.ExpectedBaseSHA)
	if contract.PublicationMode == "local_only" {
		return command + " --local-only"
	}
	return command + " --publication-ref " + contract.PublicationRef
}
