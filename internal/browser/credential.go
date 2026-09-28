package browser

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/policy"
)

const (
	jevCredentialSurface = "tool:browser-jev-typesafe"
	jevCredentialRef     = "home-secret://typesafe/jev-api-key"
)

func jevCredentialHandle() capgraph.CredentialHandle {
	return capgraph.CredentialHandle{Ref: jevCredentialRef, Custodian: "compound-home", EnvName: "TYPESAFE_API_KEY"}
}

type CredentialChange struct {
	Stored       bool   `json:"stored"`
	Refused      bool   `json:"refused,omitempty"`
	Reason       string `json:"reason,omitempty"`
	ActionID     string `json:"action_intent_id,omitempty"`
	DecisionID   string `json:"policy_decision_id,omitempty"`
	ReceiptID    string `json:"receipt_id,omitempty"`
	TransitionID string `json:"transition_id,omitempty"`
}

// StoreTypeSafeCredential is the closed, stdin-fed enrollment door for Jev's
// single provider slot. Neither the key nor a key-derived digest enters an
// envelope, registry, receipt, or error message.
func StoreTypeSafeCredential(home, workID string, key []byte, cfg policy.Config, now time.Time) (change CredentialChange, err error) {
	return setTypeSafeCredential(home, workID, key, cfg, now, false)
}

// RotateTypeSafeCredential replaces only the exact registered Jev slot through
// the same security-class policy gate; no credential bytes enter audit state.
func RotateTypeSafeCredential(home, workID string, key []byte, cfg policy.Config, now time.Time) (CredentialChange, error) {
	return setTypeSafeCredential(home, workID, key, cfg, now, true)
}

func setTypeSafeCredential(home, workID string, key []byte, cfg policy.Config, now time.Time, replace bool) (change CredentialChange, err error) {
	if !strings.HasPrefix(workID, "work_") || len(key) == 0 || len(key) > 4096 || strings.ContainsAny(string(key), "\r\n\x00") {
		return CredentialChange{}, errors.New("credential enrollment requires work_id and one bounded nonempty key")
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	operation := "enroll"
	if replace {
		operation = "rotate"
	}
	digest := sha256.Sum256([]byte(workID + "\x00" + jevCredentialSurface + "\x00" + operation + "\x00" + stamp))
	intent := envelope.ActionIntent{
		Header: envelope.Header{SchemaVersion: envelope.SVActionIntent, ID: "act_" + hex.EncodeToString(digest[:8]), CreatedAt: stamp,
			Producer: envelope.Producer{Kind: "cli", Name: "compound"}, IDs: envelope.IDs{WorkID: workID},
			Authority: envelope.Authority{SourceClass: "runtime", Status: "current"}, Privacy: "private", Risk: "tier1",
			EvidenceRefs: []string{}, Uncertainty: envelope.Uncertainty{Level: "known"}},
		Actor: "compound.browser", Capability: "browser.credential.store", Action: "enroll_typesafe_key",
		Target:        envelope.ActionTarget{Kind: "credential_slot", ID: jevCredentialSurface},
		MutationClass: "security", Reversibility: "hard", BlastRadius: "local",
		Preview:      "enroll Jev provider credential in Compound custody",
		RollbackPlan: "operator removes or replaces the exact Compound credential slot through a governed credential action",
	}
	if replace {
		intent.Action = "rotate_typesafe_key"
		intent.Preview = "replace the Jev provider credential in Compound custody"
	}
	if err := intent.Validate(); err != nil {
		return CredentialChange{}, err
	}
	decision := policy.Evaluate(intent, cfg, now)
	if err := decision.Validate(); err != nil {
		return CredentialChange{}, err
	}
	store, err := eventstore.Open(home)
	if err != nil {
		return CredentialChange{}, err
	}
	if err := recordEvent(store, intent.ID, &intent); err != nil {
		return CredentialChange{}, err
	}
	if err := recordEvent(store, decision.ID, &decision); err != nil {
		return CredentialChange{}, err
	}
	if err := policy.Gate(decision); err != nil {
		receiptID, transitionID, recordErr := recordStep(store, intent, "blocked", "Jev credential enrollment held by policy")
		if recordErr != nil {
			return CredentialChange{}, recordErr
		}
		return CredentialChange{Refused: true, Reason: err.Error(), ActionID: intent.ID, DecisionID: decision.ID, ReceiptID: receiptID, TransitionID: transitionID}, nil
	}
	defer func() {
		if err == nil || change.ReceiptID != "" {
			return
		}
		receiptID, transitionID, recordErr := recordStep(store, intent, "failed", "Jev credential enrollment stopped without activating the slot")
		if recordErr != nil {
			err = errors.Join(err, recordErr)
			return
		}
		change.ActionID, change.DecisionID = intent.ID, decision.ID
		change.ReceiptID, change.TransitionID = receiptID, transitionID
	}()
	path := filepath.Join(home, "secrets", "typesafe", "jev-api-key")
	graph, err := capgraph.Open(home)
	if err != nil {
		return CredentialChange{}, err
	}
	surfaces, err := graph.ConnectionSurfaces()
	if err != nil {
		return CredentialChange{}, err
	}
	registered := false
	for _, surface := range surfaces {
		if surface.NodeID == jevCredentialSurface {
			if surface.CredentialHandle != jevCredentialHandle() || surface.AuthMode != "api_key" || surface.AssertingOwner != "compound.browser" {
				return CredentialChange{}, errors.New("Jev credential surface already exists with an unexpected binding")
			}
			registered = true
		}
	}
	if replace && !registered {
		return CredentialChange{}, errors.New("credential rotation requires the exact registered Jev surface")
	}
	if err := secureCredentialParents(home); err != nil {
		return CredentialChange{}, err
	}
	info, statErr := os.Lstat(path)
	existing := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return CredentialChange{}, statErr
	}
	if existing {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return CredentialChange{}, errors.New("existing credential slot is not a regular mode-0600 file")
		}
		stored, readErr := os.ReadFile(path)
		if readErr != nil {
			return CredentialChange{}, errors.New("credential slot could not be read")
		}
		if subtle.ConstantTimeCompare(stored, key) != 1 && !replace {
			return CredentialChange{}, errors.New("credential slot already contains another value; enrollment does not replace secrets")
		}
		if replace && subtle.ConstantTimeCompare(stored, key) != 1 {
			if err := replaceCredentialFile(path, key); err != nil {
				return CredentialChange{}, err
			}
			existing = false // A real state delta requires a completed receipt.
		}
	} else {
		if replace {
			return CredentialChange{}, errors.New("credential rotation requires an existing secret slot")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return CredentialChange{}, err
		}
		if err := secureCredentialParents(home); err != nil {
			return CredentialChange{}, err
		}
		file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			return CredentialChange{}, openErr
		}
		written, writeErr := file.Write(key)
		if writeErr == nil && written != len(key) {
			writeErr = errors.New("short credential write")
		}
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			_ = os.Remove(path)
			return CredentialChange{}, fmt.Errorf("credential enrollment failed: %w", writeErr)
		}
	}
	if !registered {
		if err := capgraph.AddGoverned(home, capgraph.Node{ID: jevCredentialSurface, Kind: "tool", Name: "Jev TypeSafe provider", Triggers: []string{"browser", "jev"}}, now); err != nil {
			return CredentialChange{}, err
		}
		_, err = capgraph.AddConnectionSurfaceGoverned(home, capgraph.ConnectionSurface{
			NodeID: jevCredentialSurface, AuthMode: "api_key", AssertingOwner: "compound.browser",
			CredentialHandle: jevCredentialHandle(),
		}, now)
		if err != nil {
			return CredentialChange{}, err
		}
	}
	if existing && registered {
		receiptID, transitionID, recordErr := recordStep(store, intent, "no_op", "Jev credential slot already enrolled")
		return CredentialChange{Stored: recordErr == nil, ActionID: intent.ID, DecisionID: decision.ID, ReceiptID: receiptID, TransitionID: transitionID}, recordErr
	}
	receiptID, transitionID, err := recordCredentialStored(store, intent, now)
	if err != nil {
		// The exact slot stays recoverable on retry if audit publication fails.
		return CredentialChange{Stored: true, ActionID: intent.ID, DecisionID: decision.ID, ReceiptID: receiptID}, err
	}
	return CredentialChange{Stored: true, ActionID: intent.ID, DecisionID: decision.ID, ReceiptID: receiptID, TransitionID: transitionID}, nil
}

func replaceCredentialFile(path string, key []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".jev-api-key-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if n, err := file.Write(key); err != nil || n != len(key) {
		_ = file.Close()
		return errors.New("credential replacement write failed")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func secureCredentialParents(home string) error {
	for _, path := range []string{filepath.Join(home, "secrets"), filepath.Join(home, "secrets", "typesafe")} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return errors.New("credential custody directories must be private real directories")
		}
	}
	return nil
}

func recordCredentialStored(store *eventstore.Store, intent envelope.ActionIntent, now time.Time) (string, string, error) {
	digest := sha256.Sum256([]byte(intent.ID + "\x00stored"))
	summary := "Jev TypeSafe credential slot enrolled in Compound custody"
	if intent.Action == "rotate_typesafe_key" {
		summary = "Jev TypeSafe credential slot replaced in Compound custody"
	}
	receipt := envelope.Receipt{
		Header: intent.Header, Outcome: "completed", MoveType: "control_plane_repair",
		StateDelta: envelope.StateDelta{Changed: true, Summary: summary},
		Cost:       envelope.ReceiptCost{ModelCalls: []string{}},
		Skills:     envelope.ReceiptSkills{Used: []string{"browser.credential.store"}, Missed: []string{}, Candidate: []string{}, UsedDeclared: true},
		Memory:     envelope.ReceiptMemory{Candidates: []string{}, Promoted: []string{}, Parked: []string{}},
		Next:       envelope.ReceiptNext{SuggestedWork: []string{}},
		ProofRefs:  []string{"check:jev-credential-slot-mode-0600"},
	}
	receipt.SchemaVersion, receipt.ID, receipt.CreatedAt = envelope.SVReceipt, "rec_"+hex.EncodeToString(digest[:8]), now.UTC().Format(time.RFC3339Nano)
	if err := receipt.Validate(); err != nil {
		return "", "", err
	}
	transition := envelope.Transition{
		Header: intent.Header, BeforeRefs: []string{}, ActionRefs: []string{intent.ID}, AfterRefs: []string{receipt.ID},
		StateDelta:  summary,
		Projections: envelope.Projections{Memory: []string{}, Skill: []string{}, Product: []string{}, Cost: []string{}, Risk: []string{}, Taste: []string{}, Routing: []string{}, HumanAttention: []string{}},
		SecondOrder: envelope.SecondOrder{FutureBehaviorChanged: true},
	}
	transition.SchemaVersion, transition.ID, transition.CreatedAt = envelope.SVTransition, "tr_"+hex.EncodeToString(digest[:8]), now.UTC().Format(time.RFC3339Nano)
	if err := transition.Validate(); err != nil {
		return "", "", err
	}
	if err := recordEvent(store, receipt.ID, &receipt); err != nil {
		return "", "", err
	}
	if err := recordEvent(store, transition.ID, &transition); err != nil {
		return receipt.ID, "", err
	}
	return receipt.ID, transition.ID, nil
}
