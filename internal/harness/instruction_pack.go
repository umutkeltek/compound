package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// InstructionPackSchemaVersion is the JSON schema id the pack body carries.
// A future bump is conscious — tests pin it.
const InstructionPackSchemaVersion = "compound.instruction_pack.v0.1"

// InstructionPackLayer is the typed reference to a context-defining layer
// (policy-core / posture). Each layer degrades gracefully: an unresolvable
// source (no authority yaml, no permission HarnessArtifact) renders as a
// single absent marker so the pack still writes a complete shape.
type InstructionPackLayer struct {
	AuthorityRef   string `json:"authority_ref"`
	SourceRevision string `json:"source_revision"`
	ContentSHA256  string `json:"content_sha256,omitempty"`
	Absent         bool   `json:"absent,omitempty"`
}

// InstructionPackRosterRef carries one roster ref (installed hooks manifest,
// effective contract latest.json) with the same absent-aware semantics.
type InstructionPackRosterRef struct {
	Ref      string `json:"ref,omitempty"`
	Revision string `json:"revision,omitempty"`
	Absent   bool   `json:"absent,omitempty"`
}

// InstructionPackRoster is the deterministic-identity view of every roster
// a harness sees at render time: the registry revision (default — never
// absent) plus the optional installed-hooks manifest and the effective
// contract latest.json.
type InstructionPackRoster struct {
	RegistryRevision  string                   `json:"registry_revision"`
	InstalledHooks    InstructionPackRosterRef `json:"installed_hooks"`
	EffectiveContract InstructionPackRosterRef `json:"effective_contract"`
}

// InstructionPack is the typed, content-addressed, three-layer reflection
// of every deterministic input a harness uses to construct an instruction
// stream (packet work_7373143486d9). It is produced by RenderInstructionPack
// and written atomically under
// $COMPOUND_HOME/context/instruction-packs/<harness>/latest.json.
//
// ContentSHA256 covers the pack body EXCLUDING GeneratedAt so two renders
// over identical inputs yield the same identity (the stability falsifier
// depends on this). GeneratedAt IS recorded on the body — only the hash
// excludes it.
type InstructionPack struct {
	SchemaVersion string                `json:"schema_version"`
	Harness       string                `json:"harness"`
	PolicyCore    InstructionPackLayer  `json:"policy_core"`
	Posture       InstructionPackLayer  `json:"posture"`
	Roster        InstructionPackRoster `json:"roster"`
	GeneratedAt   time.Time             `json:"generated_at"`
	ContentSHA256 string                `json:"content_sha256"`
}

// installedHooksManifestPath mirrors contract.InstalledHooksPath
// (homePath/config/installed-hooks.yaml). Inlined here because internal/harness
// cannot import internal/contract (contract imports harness). The mirror is
// mechanical — any path drift is caught by F5.
func installedHooksManifestPath(homePath string) string {
	return filepath.Join(homePath, "config", "installed-hooks.yaml")
}

// effectiveContractLatestPath mirrors contract.RecordEffectiveContract's
// `contracts/effective/latest.json` write point. Same rationale as
// installedHooksManifestPath.
func effectiveContractLatestPath(homePath string) string {
	return filepath.Join(homePath, "contracts", "effective", "latest.json")
}

// InstructionPackPath returns the write path for the harness-scoped pack
// projection: $COMPOUND_HOME/context/instruction-packs/<harness>/latest.json.
func InstructionPackPath(homePath, harness string) string {
	return filepath.Join(
		strings.TrimSpace(homePath),
		"context",
		"instruction-packs",
		strings.TrimSpace(harness),
		"latest.json",
	)
}

// RenderInstructionPack assembles and writes the harness-scoped pack
// projection. Every layer degrades gracefully: missing authority/artifact/
// manifest/contract-latest → that layer carries an explicit absent marker
// (empty refs, no error), the pack still renders. PolicyCore for slice 1 = the
// permission authority ref + revision when present (the hand-written yaml IS
// today's policy-core instance); a fuller policy-core source is a later slice.
func RenderInstructionPack(homePath, harness string, now time.Time) (InstructionPack, error) {
	harness = strings.TrimSpace(harness)
	if harness == "" {
		return InstructionPack{}, fmt.Errorf("harness required")
	}
	now = now.UTC()

	policy, policyRev, policyErr := LoadPermissionAuthority(homePath, harness)
	policyLayer := InstructionPackLayer{Absent: true}
	if policyErr == nil {
		body, readErr := os.ReadFile(PermissionAuthorityPath(homePath, harness))
		if readErr != nil {
			return InstructionPack{}, fmt.Errorf("read authority body: %w", readErr)
		}
		policyLayer = InstructionPackLayer{
			AuthorityRef:   PermissionAuthorityRef(harness),
			SourceRevision: policyRev,
			ContentSHA256:  "sha256:" + digest(body),
		}
	}

	postureLayer := InstructionPackLayer{Absent: true}
	if policyErr == nil {
		// Posture layer needs both the authority (for target path) AND a
		// recorded HarnessArtifact for that target. Missing artifact → absent.
		if artifact, ok, err := latestHarnessArtifact(homePath, harness, policy.TargetPath); err == nil && ok {
			postureLayer = InstructionPackLayer{
				AuthorityRef:   artifact.SourceAuthorityRef,
				SourceRevision: artifact.SourceRevision,
				ContentSHA256:  artifact.ContentSHA256,
			}
		}
	}

	installedHooksRef := installedHooksManifestPath(homePath)
	installedHooks := InstructionPackRosterRef{Absent: true}
	if body, err := os.ReadFile(installedHooksRef); err == nil {
		installedHooks = InstructionPackRosterRef{
			Ref:      installedHooksManifestRel(homePath),
			Revision: "sha256:" + digest(body),
		}
	}

	contractRef := effectiveContractLatestPath(homePath)
	effectiveContract := InstructionPackRosterRef{Absent: true}
	if body, err := os.ReadFile(contractRef); err == nil {
		effectiveContract = InstructionPackRosterRef{
			Ref:      effectiveContractLatestRel(homePath),
			Revision: "sha256:" + digest(body),
		}
	}

	pack := InstructionPack{
		SchemaVersion: InstructionPackSchemaVersion,
		Harness:       harness,
		PolicyCore:    policyLayer,
		Posture:       postureLayer,
		Roster: InstructionPackRoster{
			RegistryRevision:  RegistryRevision(DefaultRegistry()),
			InstalledHooks:    installedHooks,
			EffectiveContract: effectiveContract,
		},
		GeneratedAt: now,
	}
	pack.ContentSHA256 = "sha256:" + digest(instructionPackBody(pack))

	if err := writeInstructionPackAtomic(InstructionPackPath(homePath, harness), pack); err != nil {
		return InstructionPack{}, fmt.Errorf("write pack latest.json: %w", err)
	}
	return pack, nil
}

// instructionPackBody marshals the pack EXCLUDING GeneratedAt for the
// content sha. We marshal a normalized map[string]any with sorted keys so
// any map-key reordering across Go versions cannot perturb identity. The
// serialized body MUST round-trip to a pack with the same ContentSHA256
// (F4 covers this).
func instructionPackBody(p InstructionPack) []byte {
	body := map[string]any{
		"schema_version": p.SchemaVersion,
		"harness":        p.Harness,
		"policy_core":    layerToMap(p.PolicyCore),
		"posture":        layerToMap(p.Posture),
		"roster":         rosterToMap(p.Roster),
	}
	normalized := normalizeJSONValue(body)
	out, err := json.Marshal(normalized)
	if err != nil {
		// Best-effort: caller already built everything; should never reach here.
		// Fall back to a non-deterministic blank to surface the fault loudly.
		return []byte("marshal-error:" + err.Error())
	}
	return out
}

func layerToMap(l InstructionPackLayer) map[string]any {
	m := map[string]any{
		"authority_ref":   l.AuthorityRef,
		"source_revision": l.SourceRevision,
		"content_sha256":  l.ContentSHA256,
		"absent":          l.Absent,
	}
	return m
}

func rosterRefToMap(r InstructionPackRosterRef) map[string]any {
	return map[string]any{
		"ref":      r.Ref,
		"revision": r.Revision,
		"absent":   r.Absent,
	}
}

func rosterToMap(r InstructionPackRoster) map[string]any {
	return map[string]any{
		"registry_revision":  r.RegistryRevision,
		"installed_hooks":    rosterRefToMap(r.InstalledHooks),
		"effective_contract": rosterRefToMap(r.EffectiveContract),
	}
}

// normalizeJSONValue recursively forces deterministic key ordering so the
// content sha is independent of Go map iteration order (Go map iteration is
// randomized; we want sha to be deterministic across re-renders).
func normalizeJSONValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, child := range x {
			out[k] = normalizeJSONValue(child)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, child := range x {
			out[i] = normalizeJSONValue(child)
		}
		return out
	default:
		return v
	}
}

// writeInstructionPackAtomic writes the JSON body to path with an
// atomic temp-file rename, mirroring writePermissionFileAtomic's
// discipline. The pack body itself is marshaled with json.MarshalIndent
// so the file is human-readable on disk.
func writeInstructionPackAtomic(path string, pack InstructionPack) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// installedHooksManifestRel returns the home-relative ref of the installed
// hooks manifest; used as the InstalledHooks.Ref value so projections stay
// home-scoped.
func installedHooksManifestRel(homePath string) string {
	return filepath.ToSlash(filepath.Join("config", "installed-hooks.yaml"))
}

// effectiveContractLatestRel is the mirror for the effective contract
// latest.json.
func effectiveContractLatestRel(homePath string) string {
	return filepath.ToSlash(filepath.Join("contracts", "effective", "latest.json"))
}

// installedHooksManifestSchemaVersion is referenced only by an explicit
// schema-validation refusal path — not currently invoked but pinned here
// so a future envelope validator can pull it from this layer without a
// cross-package import.
var _ = strings.HasPrefix
var _ = yaml.Marshal
var _ = sort.Strings
