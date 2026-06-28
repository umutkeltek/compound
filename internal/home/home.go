// Package home owns COMPOUND_HOME — the runtime body. The public repo is source;
// generated runtime state (ledger, work, context, receipts, transitions, indexes,
// projections, inbox candidates, artifacts) lives under the home, NEVER the repo
// source tree. The layout is the guardrail: durable (ledger) vs candidate (inbox)
// vs rebuildable view (projections) vs rented (indexes) vs content-addressed
// (artifacts). See docs/architecture/runtime.md.
package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/candidates"
	"gopkg.in/yaml.v3"
)

// ManifestName is the home's identity file; its presence marks a real home.
const ManifestName = "manifest.yaml"

// SchemaVersion of the home manifest.
const SchemaVersion = "compound.home.v0.1"

// Dirs is the typed habitat — each entry is a subdir compound owns under the home. The candidate
// inbox dirs are NOT hand-listed here: they are derived from internal/candidates (the single
// source of truth), so the home can never create a dir the producers don't write to, or miss one
// they do. Operational sub-states (parked/rejected/contradictions) are added on top.
var Dirs = func() []string {
	d := []string{
		"lock",
		"config", "config/overlays", "config/harnesses", "config/adapters",
		"contracts/effective", "contracts/schemas",
		"events",
		"work", "work/work", "work/runs", "work/leases", "work/checkpoints",
		"context/requests", "context/packs", "context/injections",
		"ledger",
		"registries",
		"artifacts/sha256", "artifacts/refs",
		"indexes",
		"projections",
		"reviews",
		"exports",
		"tmp",
	}
	d = append(d, candidates.AllInboxDirs()...)                             // one canonical dir per candidate kind
	d = append(d, "inbox/parked", "inbox/rejected", "inbox/contradictions") // adjudication sub-states
	return d
}()

// Manifest is the home's identity record.
type Manifest struct {
	SchemaVersion string   `yaml:"schema_version"`
	HomeKind      string   `yaml:"home_kind"` // global | local
	CreatedAt     string   `yaml:"created_at"`
	ActiveModules []string `yaml:"active_modules"`
}

// Resolve returns the COMPOUND_HOME path using strict precedence:
//  1. flagHome (--home)
//  2. $COMPOUND_HOME
//  3. nearest ancestor .compound/manifest.yaml (a workspace-local home)
//  4. ~/.compound (the global default)
func Resolve(flagHome string) (string, error) {
	if s := strings.TrimSpace(flagHome); s != "" {
		return filepath.Abs(s)
	}
	if s := strings.TrimSpace(os.Getenv("COMPOUND_HOME")); s != "" {
		return filepath.Abs(s)
	}
	if p := findAncestorHome(); p != "" {
		return p, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".compound"), nil
}

// LocalPath is <cwd>/.compound — the target for `home init --local`.
func LocalPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".compound"), nil
}

func findAncestorHome() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		cand := filepath.Join(dir, ".compound")
		if fi, err := os.Stat(filepath.Join(cand, ManifestName)); err == nil && !fi.IsDir() {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Init creates the habitat dirs and writes the manifest if absent. Idempotent:
// re-running never destroys existing state, only fills missing dirs.
func Init(homePath, kind string, now time.Time) error {
	if kind == "" {
		kind = "global"
	}
	for _, d := range Dirs {
		if err := os.MkdirAll(filepath.Join(homePath, d), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	mp := filepath.Join(homePath, ManifestName)
	if _, err := os.Stat(mp); errors.Is(err, os.ErrNotExist) {
		m := Manifest{
			SchemaVersion: SchemaVersion,
			HomeKind:      kind,
			CreatedAt:     now.UTC().Format(time.RFC3339),
			ActiveModules: []string{},
		}
		b, err := yaml.Marshal(m)
		if err != nil {
			return err
		}
		if err := os.WriteFile(mp, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// CheckItem is one doctor finding.
type CheckItem struct {
	Name string
	OK   bool
}

// Report is the doctor result.
type Report struct {
	Home          string
	ManifestOK    bool
	Manifest      Manifest
	Dirs          []CheckItem
	DirsOK        int
	InsideGitRepo bool // a global home inside a git repo is a "generated-state-in-source" smell
	OK            bool
}

// Doctor inspects a home: manifest present + parseable, every habitat dir present,
// and whether the home sits inside a git repo (generated state must not be tracked).
// OK is gated on manifest + dirs only; the git check is reported, not fatal (a
// workspace-local home is legitimately inside a repo but must be gitignored).
func Doctor(homePath string) Report {
	r := Report{Home: homePath}
	if b, err := os.ReadFile(filepath.Join(homePath, ManifestName)); err == nil {
		if yaml.Unmarshal(b, &r.Manifest) == nil && r.Manifest.SchemaVersion != "" {
			r.ManifestOK = true
		}
	}
	for _, d := range Dirs {
		fi, err := os.Stat(filepath.Join(homePath, d))
		ok := err == nil && fi.IsDir()
		if ok {
			r.DirsOK++
		}
		r.Dirs = append(r.Dirs, CheckItem{Name: d, OK: ok})
	}
	r.InsideGitRepo = underGitRepo(homePath)
	r.OK = r.ManifestOK && r.DirsOK == len(Dirs)
	return r
}

func underGitRepo(p string) bool {
	dir := p
	for {
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && fi.IsDir() {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
