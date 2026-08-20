package home

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	HomeKindGlobal = "global"
	HomeKindLocal  = "local"
	homeKindTest   = "test"
)

const manifestInitStagingPrefix = ".compound-manifest-init-"

var (
	initManifestFileWrite             = func(file *os.File, body []byte) (int, error) { return file.Write(body) }
	initManifestFileSync              = func(file *os.File) error { return file.Sync() }
	initManifestFileClose             = func(file *os.File) error { return file.Close() }
	initManifestBeforePublish         = func(*os.Root, string, []byte) error { return nil }
	manifestInitResidueOwnedByCurrent = ownedByCurrent
)

const (
	manifestRecoveryGuidance = "before migration, use a Compound version that supports this home to run `compound home backup --target <archive>` and `compound home verify <archive>`; preserve the original home and rehearse how to roll back with that supporting version using `compound home restore <archive> --target <new-absent-home-path>`; this binary will not rewrite manifest.yaml automatically"
	archiveRecoveryGuidance  = "preserve the archive unchanged; use a Compound version that supports its archive envelope to run `compound home verify <archive>` and restore only to a new absent target with `compound home restore <archive> --target <new-absent-home-path>`; this binary will not rewrite the archive automatically"
)

const (
	compatibilitySurfaceHomeManifest    = "home_manifest"
	compatibilitySurfaceArchiveEnvelope = "archive_envelope"
)

// CompatibilityError is the typed recovery contract shared by Init, archive
// verification, restore, and the CLI mutation preflight. Callers can classify
// the failing surface and render exact recovery guidance without string
// matching an implementation error.
type CompatibilityError struct {
	surface  string
	guidance string
	cause    error
}

func (e *CompatibilityError) Error() string {
	if e == nil {
		return "compatibility failure"
	}
	return fmt.Sprintf("%v; recovery: %s", e.cause, e.guidance)
}

func (e *CompatibilityError) Unwrap() error { return e.cause }

func (e *CompatibilityError) CompatibilitySurface() string {
	if e == nil {
		return ""
	}
	return e.surface
}

func (e *CompatibilityError) RecoveryGuidance() string {
	if e == nil {
		return ""
	}
	return e.guidance
}

func asCompatibilityError(err error, surface, guidance string) error {
	if err == nil {
		return nil
	}
	var compatibilityErr *CompatibilityError
	if errors.As(err, &compatibilityErr) {
		return err
	}
	return &CompatibilityError{surface: surface, guidance: guidance, cause: err}
}

func asManifestCompatibilityError(err error) error {
	return asCompatibilityError(err, compatibilitySurfaceHomeManifest, manifestRecoveryGuidance)
}

func asArchiveCompatibilityError(err error) error {
	return asCompatibilityError(err, compatibilitySurfaceArchiveEnvelope, archiveRecoveryGuidance)
}

func isManifestCompatibilityError(err error) bool {
	var compatibilityErr *CompatibilityError
	return errors.As(err, &compatibilityErr) && compatibilityErr.CompatibilitySurface() == compatibilitySurfaceHomeManifest
}

// RequireMutationCompatible proves the selected home has an identity this
// binary understands before any command classified as mutating, held,
// governed, or installing is dispatched. It never creates or rewrites state.
func RequireMutationCompatible(homePath string) error {
	if strings.TrimSpace(homePath) == "" {
		return asManifestCompatibilityError(fmt.Errorf("home path is required before mutation"))
	}
	if _, err := LoadManifest(homePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return asManifestCompatibilityError(fmt.Errorf("home manifest is missing at %s", filepath.Join(homePath, ManifestName)))
		}
		return err
	}
	return nil
}

func withManifestRecoveryGuidance(err error) error {
	return err
}

func validateHomeKind(kind string) error {
	if strings.TrimSpace(kind) == "" {
		return fmt.Errorf("home kind is required")
	}
	return nil
}

func validateHomeManifest(manifest Manifest) error {
	if manifest.SchemaVersion == "" {
		return fmt.Errorf("home manifest schema_version is required")
	}
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported home schema %q; supported schema is %q", manifest.SchemaVersion, SchemaVersion)
	}
	if manifest.Kind == "" {
		return fmt.Errorf("home manifest kind is required")
	}
	if manifest.Kind != ManifestKind {
		return fmt.Errorf("home manifest kind must be %q, got %q", ManifestKind, manifest.Kind)
	}
	if strings.TrimSpace(manifest.HomeKind) == "" {
		return fmt.Errorf("home manifest home_kind is required")
	}
	return validateHomeKind(manifest.HomeKind)
}

func decodeHomeManifest(raw []byte) (Manifest, error) {
	var manifest Manifest
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, asManifestCompatibilityError(fmt.Errorf("parse home manifest: %w", err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return Manifest{}, asManifestCompatibilityError(fmt.Errorf("parse home manifest: expected exactly one YAML document"))
	} else if !errors.Is(err, io.EOF) {
		return Manifest{}, asManifestCompatibilityError(fmt.Errorf("parse home manifest after first YAML document: %w", err))
	}
	// v0.1 homes written before the kind discriminator existed are an explicit
	// compatibility form. They stay byte-preserved and readable; only the in-
	// memory view is normalized so current consumers see the fixed identity.
	if manifest.Kind == "" && manifest.SchemaVersion == SchemaVersion && strings.TrimSpace(manifest.HomeKind) != "" {
		manifest.Kind = ManifestKind
		manifest.LegacyIdentity = true
	}
	if err := validateHomeManifest(manifest); err != nil {
		return manifest, asManifestCompatibilityError(err)
	}
	return manifest, nil
}

func preflightInitManifest(homePath string) ([]byte, bool, error) {
	raw, err := readManifestNoFollowBounded(filepath.Join(homePath, ManifestName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if _, err := decodeHomeManifest(raw); err != nil {
		return nil, true, err
	}
	return raw, true, nil
}

func readRootedManifest(root *os.Root, displayPath string) ([]byte, error) {
	return readRootedManifestNamed(root, ManifestName, displayPath)
}

func readRootedManifestNamed(root *os.Root, name, displayPath string) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("manifest must be a regular non-symlink file: %s", displayPath)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, manifestMaxBytes+1))
	if err != nil {
		return nil, err
	}
	after, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if opened.Mode()&os.ModeSymlink != 0 || !opened.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, fmt.Errorf("manifest identity changed while reading: %s", displayPath)
	}
	if int64(len(raw)) > manifestMaxBytes {
		return nil, fmt.Errorf("manifest %s exceeds %d byte limit", displayPath, manifestMaxBytes)
	}
	return raw, nil
}

func validateRootedInitManifest(root *os.Root, homePath, expectedKind string, expectedRaw []byte) (Manifest, error) {
	display := filepath.Join(homePath, ManifestName)
	raw, err := readRootedManifest(root, display)
	if err != nil {
		return Manifest{}, err
	}
	manifest, err := decodeHomeManifest(raw)
	if err != nil {
		return manifest, err
	}
	if expectedRaw != nil && !bytes.Equal(expectedRaw, raw) {
		return manifest, fmt.Errorf("home manifest changed before rooted validation: %s", display)
	}
	if manifest.HomeKind != expectedKind {
		return manifest, asManifestCompatibilityError(fmt.Errorf("existing home kind %q does not match requested kind %q; manifest will not be rewritten", manifest.HomeKind, expectedKind))
	}
	return manifest, nil
}

func requireEmptyRootForFreshManifest(root *os.Root, homePath string) error {
	if err := recoverOwnedInitManifestStaging(root, homePath); err != nil {
		return err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("existing populated path has no manifest; refusing to initialize or rewrite it: %s", homePath)
	}
	return nil
}

func recoverOwnedInitManifestStaging(root *os.Root, homePath string) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !isManifestInitStagingName(name) {
			continue
		}
		display := filepath.Join(homePath, name)
		if err := removeRootedRegularFileDurableValidatedAt(
			root,
			name,
			display,
			"home init manifest staging residue",
			nil,
			func(info os.FileInfo) error {
				if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return fmt.Errorf("home init manifest staging residue must be a regular non-symlink file: %s", display)
				}
				if !manifestInitResidueOwnedByCurrent(info) {
					return fmt.Errorf("home init manifest staging residue must be current-user-owned: %s", display)
				}
				return nil
			},
		); err != nil {
			return fmt.Errorf("recover interrupted home init manifest staging: %w", err)
		}
	}
	return nil
}

func writeRootedInitManifest(root *os.Root, manifestBytes []byte, homePath string) error {
	tempName, file, tempInfo, err := createRootedInitManifestStaging(root)
	if err != nil {
		return err
	}
	published := false
	cleanup := func(cause error) error {
		_ = file.Close()
		if published {
			return cause
		}
		return errors.Join(cause, removeOwnedInitManifestStaging(root, tempName, tempInfo, homePath))
	}
	if n, err := initManifestFileWrite(file, manifestBytes); err != nil {
		return cleanup(err)
	} else if n != len(manifestBytes) {
		return cleanup(io.ErrShortWrite)
	}
	if err := file.Chmod(0o644); err != nil {
		return cleanup(err)
	}
	if err := initManifestFileSync(file); err != nil {
		return cleanup(err)
	}
	if err := initManifestFileClose(file); err != nil {
		return cleanup(err)
	}
	staged, err := readRootedManifestNamed(root, tempName, filepath.Join(homePath, tempName))
	if err != nil {
		return cleanup(err)
	}
	if !bytes.Equal(staged, manifestBytes) {
		return cleanup(fmt.Errorf("staged home manifest changed before publication: %s", filepath.Join(homePath, tempName)))
	}
	stagedInfo, err := root.Lstat(tempName)
	if err != nil {
		return cleanup(err)
	}
	if stagedInfo.Mode()&os.ModeSymlink != 0 || !stagedInfo.Mode().IsRegular() || stagedInfo.Mode().Perm() != 0o644 || !ownedByCurrent(stagedInfo) || !os.SameFile(tempInfo, stagedInfo) {
		return cleanup(fmt.Errorf("staged home manifest identity, ownership, or mode changed before publication: %s", filepath.Join(homePath, tempName)))
	}
	if err := initManifestBeforePublish(root, tempName, manifestBytes); err != nil {
		return cleanup(err)
	}
	publication, err := beginRootedFileCASWithRequiredMode(root, ManifestName, tempName, nil, nil, manifestBytes, stagedInfo, 0o644)
	if err != nil {
		if publication != nil && publication.State() != RootedFileCASNotPublished {
			published = true
			return fmt.Errorf("home manifest publication is ambiguous; preserve the home for recovery: %w", err)
		}
		return cleanup(fmt.Errorf("publish home manifest without replacement: %w", err))
	}
	published = true
	if err := publication.Commit(); err != nil {
		return fmt.Errorf("home manifest was published but publication finalization is ambiguous; preserve the home for recovery: %w", err)
	}
	return nil
}

func createRootedInitManifestStaging(root *os.Root) (string, *os.File, os.FileInfo, error) {
	var nonce [16]byte
	for range 100 {
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", nil, nil, err
		}
		name := manifestInitStagingPrefix + hex.EncodeToString(nonce[:])
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, nil, err
		}
		opened, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return "", nil, nil, err
		}
		leaf, err := root.Lstat(name)
		if err != nil {
			_ = file.Close()
			return "", nil, nil, err
		}
		if leaf.Mode()&os.ModeSymlink != 0 || !leaf.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(opened, leaf) {
			_ = file.Close()
			return "", nil, nil, fmt.Errorf("manifest staging file must be a stable regular file: %s", name)
		}
		return name, file, opened, nil
	}
	return "", nil, nil, fmt.Errorf("allocate home manifest staging file")
}

func removeOwnedInitManifestStaging(root *os.Root, name string, expected os.FileInfo, homePath string) error {
	_, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	display := filepath.Join(homePath, name)
	return removeRootedRegularFileDurableValidatedAt(
		root,
		name,
		display,
		"home init manifest staging cleanup",
		nil,
		func(info os.FileInfo) error {
			if expected == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !os.SameFile(expected, info) || !ownedByCurrent(info) {
				return fmt.Errorf("refusing to remove changed home manifest staging file: %s", display)
			}
			return nil
		},
	)
}

func isManifestInitStagingName(name string) bool {
	suffix, ok := strings.CutPrefix(name, manifestInitStagingPrefix)
	if !ok || len(suffix) != 32 {
		return false
	}
	for _, ch := range suffix {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}
