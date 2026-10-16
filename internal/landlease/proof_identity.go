package landlease

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

type ProofCapacityClass string

const (
	ProofCapacitySystemFull         ProofCapacityClass = "system_full"
	ProofCapacityNativeConsumer     ProofCapacityClass = "native_consumer"
	ProofCoordinationIdentitySchema                    = "compound.proof_coordination.v0.1"
)

// ProofCoordinationIdentity is persisted with both the reservation and lease.
// A missing identity always means the historical singleton; readers must never
// infer a newer route from current configuration.
type ProofCoordinationIdentity struct {
	SchemaVersion         string             `json:"schema_version"`
	Generation            string             `json:"generation"`
	Class                 ProofCapacityClass `json:"class"`
	RepositoryFingerprint string             `json:"repository_fingerprint"`
	RepositoryBarrier     string             `json:"repository_barrier"`
}

func NewProofCoordinationIdentity(generation string, class ProofCapacityClass, repository string) (ProofCoordinationIdentity, error) {
	sum := sha256.Sum256([]byte("proof-repository-barrier:" + repository))
	identity := ProofCoordinationIdentity{
		SchemaVersion: ProofCoordinationIdentitySchema,
		Generation:    generation, Class: class, RepositoryFingerprint: repository,
		RepositoryBarrier: hex.EncodeToString(sum[:]),
	}
	return identity, identity.Validate()
}

func (identity ProofCoordinationIdentity) Validate() error {
	if identity.SchemaVersion != ProofCoordinationIdentitySchema {
		return fmt.Errorf("unsupported proof coordination identity schema %q", identity.SchemaVersion)
	}
	if !proofIdentityDigest(identity.Generation) || !proofIdentityDigest(identity.RepositoryFingerprint) {
		return fmt.Errorf("proof coordination generation and repository must be canonical SHA-256 identities")
	}
	if identity.Class != ProofCapacitySystemFull && identity.Class != ProofCapacityNativeConsumer {
		return fmt.Errorf("unsupported proof capacity class %q", identity.Class)
	}
	sum := sha256.Sum256([]byte("proof-repository-barrier:" + identity.RepositoryFingerprint))
	if identity.RepositoryBarrier != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("proof coordination repository barrier mismatch")
	}
	return nil
}

func proofIdentityDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func cloneProofCoordination(identity *ProofCoordinationIdentity) *ProofCoordinationIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	return &copy
}

func sameProofCoordination(left, right *ProofCoordinationIdentity) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

// proofLeaseIdentity admits explicit routing only within the same class and
// generation. Untyped renewal may preserve an exact holder; an untyped takeover
// must never drop or reinterpret an existing coordinated identity.
func proofLeaseIdentity(current *Lease, holder string, requested *ProofCoordinationIdentity) (*ProofCoordinationIdentity, error) {
	if requested != nil {
		if err := requested.Validate(); err != nil {
			return nil, err
		}
	}
	if current == nil {
		return cloneProofCoordination(requested), nil
	}
	existing := current.ProofCoordination
	if existing == nil {
		if requested != nil {
			return nil, fmt.Errorf("historical lease cannot be reinterpreted as a coordinated resource")
		}
		return nil, nil
	}
	if requested == nil {
		if current.Holder != holder {
			return nil, fmt.Errorf("coordinated proof takeover requires an exact new identity")
		}
		return cloneProofCoordination(existing), nil
	}
	if existing.Generation != requested.Generation || existing.Class != requested.Class {
		return nil, fmt.Errorf("proof lease class or generation changed")
	}
	if current.Holder == holder && !sameProofCoordination(existing, requested) {
		return nil, fmt.Errorf("exact holder cannot change its proof coordination identity")
	}
	return cloneProofCoordination(requested), nil
}

// A Resource expectation is checked under the class flock. Missing identity is
// an exact historical route, never permission to reinterpret coordinated state.
type leaseBinding struct {
	nonblocking      bool
	historicalHolder string
	identity         *ProofCoordinationIdentity
	acquisition      bool
	repositoryRoot   string
}

func (b *leaseBinding) validate(current *Lease) error {
	if b == nil || current == nil {
		return nil
	}
	if b.identity != nil {
		if err := b.identity.Validate(); err != nil {
			return err
		}
	}
	actual := current.ProofCoordination
	if b.historicalHolder != "" && current.Holder != b.historicalHolder && b.identity != nil && actual != nil {
		if b.identity.Generation == actual.Generation && b.identity.Class == actual.Class {
			return nil
		}
	}
	if b.acquisition && b.identity != nil && actual != nil {
		if b.identity.Generation == actual.Generation && b.identity.Class == actual.Class {
			return nil
		}
	}
	if !sameProofCoordination(b.identity, actual) {
		return fmt.Errorf("proof resource identity does not match persisted lease")
	}
	return nil
}
func withLeaseBindingLock(home string, binding *leaseBinding, fn func() error) error {
	return withLockMode(home, binding != nil && binding.nonblocking, func() error {
		path, err := statePath(home)
		if err != nil {
			return err
		}
		current, _, err := readLease(path)
		if err != nil {
			return err
		}
		if err := binding.validate(current); err != nil {
			return err
		}
		return fn()
	})
}
func singleLeaseBinding(bindings []*leaseBinding) *leaseBinding {
	if len(bindings) == 0 {
		return nil
	}
	return bindings[0]
}
func attestExecutionTerminatedBound(home, holder string, binding *leaseBinding) (ExecutionTerminationAttestation, error) {
	var result ExecutionTerminationAttestation
	err := withLeaseBindingLock(home, binding, func() error {
		var err error
		if err := binding.repositoryReleased(); err != nil {
			return err
		}
		result, err = AttestExecutionTerminated(home, holder)
		return err
	})
	return result, err
}
func executionPendingBound(home, holder string, binding *leaseBinding) (bool, error) {
	var pending bool
	err := withLeaseBindingLock(home, binding, func() error {
		var err error
		pending, err = executionBarrierPending(home, holder)
		if err != nil || pending || binding == nil || binding.identity == nil {
			return err
		}
		if binding.repositoryRoot == "" {
			return fmt.Errorf("bound proof repository barrier root required")
		}
		pending, err = executionBarrierPending(binding.repositoryRoot, proofRepositoryHolder(binding.identity))
		return err
	})
	return pending, err
}

func (b *leaseBinding) repositoryReleased() error {
	if b == nil || b.identity == nil {
		return nil
	}
	if b.repositoryRoot == "" {
		return fmt.Errorf("bound proof repository barrier root required")
	}
	pending, err := executionBarrierPending(b.repositoryRoot, proofRepositoryHolder(b.identity))
	if err != nil {
		return err
	}
	if pending {
		return &ExecutionPendingError{Holder: proofRepositoryHolder(b.identity)}
	}
	return nil
}
