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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"compound/internal/candidates"
	"gopkg.in/yaml.v3"
)

// ManifestName is the home's identity file; its presence marks a real home.
const ManifestName = "manifest.yaml"

// SchemaVersion of the home manifest.
const SchemaVersion = "compound.home.v0.1"

const (
	HomeConfigSchemaVersion = "compound.home_config.v0.1"
	BackupFreshnessMaxAge   = 48 * time.Hour
)

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
		"inbox/knowledge-extractions",
		"ledger",
		"observed/tool-output", // rebuildable, content-addressed externalized tool-output bodies (C3); NEVER under ledger
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

// LoadManifest reads the identity record without mutating the home.
func LoadManifest(homePath string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(homePath, ManifestName))
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// HomeConfig is the optional COMPOUND_HOME/config/home.yaml operator config.
// Absence is normal for fresh installs; doctor checks that depend on it fail open.
type HomeConfig struct {
	SchemaVersion string `yaml:"schema_version,omitempty"`
	BackupDir     string `yaml:"backup_dir,omitempty"`
}

func HomeConfigPath(homePath string) string {
	return filepath.Join(homePath, "config", "home.yaml")
}

func LoadHomeConfig(homePath string) (HomeConfig, string, error) {
	path := HomeConfigPath(homePath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return HomeConfig{}, path, nil
	}
	if err != nil {
		return HomeConfig{}, path, err
	}
	var cfg HomeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return HomeConfig{}, path, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.SchemaVersion != "" && cfg.SchemaVersion != HomeConfigSchemaVersion {
		return HomeConfig{}, path, fmt.Errorf("unsupported home config schema %q", cfg.SchemaVersion)
	}
	cfg.SchemaVersion = HomeConfigSchemaVersion
	return cfg, path, nil
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

// SetupCheck is one fresh-machine setup finding.
type SetupCheck struct {
	Name     string
	OK       bool
	Required bool
	Detail   string
	Fix      string
}

// SetupReport is the home-owned setup preflight. It belongs to home doctor so
// setup does not become a parallel doctor surface.
type SetupReport struct {
	InstallDir        string
	RequiredGoVersion string
	Checks            []SetupCheck
	OK                bool
}

// Report is the doctor result.
type Report struct {
	Home           string
	ManifestOK     bool
	Manifest       Manifest
	Dirs           []CheckItem
	DirsOK         int
	InsideGitRepo  bool        // a global home inside a git repo is a "generated-state-in-source" smell
	Hygiene        []CheckItem // operational hygiene: terminal work swept, leases live-only
	HygieneOK      bool
	BackupWarnings []CheckItem
	Setup          SetupReport
	SetupOK        bool
	OK             bool
}

// Doctor inspects a home: manifest present + parseable, every habitat dir present,
// whether the home sits inside a git repo (generated state must not be tracked),
// and operational hygiene (no terminal work hot, no stale leases — the sweep in
// `home archive-history` is the remedy). OK is gated on manifest + dirs only; the
// git and hygiene checks are reported, not fatal (a workspace-local home is
// legitimately inside a repo but must be gitignored, and the archive sweep must
// still be allowed to run on a hygiene-dirty home to clean it).
type DoctorOptions struct {
	Now time.Time
}

func Doctor(homePath string) Report {
	return DoctorWithOptions(homePath, DoctorOptions{})
}

func DoctorWithOptions(homePath string, opts DoctorOptions) Report {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
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
	r.Hygiene, r.HygieneOK = hygieneChecks(homePath)
	r.BackupWarnings = backupFreshnessWarnings(homePath, now)
	r.Setup = DoctorSetup(SetupOptions{})
	r.SetupOK = r.Setup.OK
	r.OK = r.ManifestOK && r.DirsOK == len(Dirs) && r.SetupOK
	return r
}

func backupFreshnessWarnings(homePath string, now time.Time) []CheckItem {
	cfg, _, err := LoadHomeConfig(homePath)
	if err != nil {
		return []CheckItem{{Name: "backup.config", OK: false}}
	}
	if strings.TrimSpace(cfg.BackupDir) == "" {
		return nil
	}
	dir, err := resolveConfiguredBackupDir(homePath, cfg.BackupDir)
	if err != nil {
		return []CheckItem{{Name: "backup.config", OK: false}}
	}
	newest, ok, err := newestBackupArchive(dir)
	if err != nil || !ok {
		return []CheckItem{{Name: "backup.missing", OK: false}}
	}
	if now.Sub(newest) > BackupFreshnessMaxAge {
		return []CheckItem{{Name: "backup.stale", OK: false}}
	}
	return nil
}

func resolveConfiguredBackupDir(homePath, dir string) (string, error) {
	dir = strings.TrimSpace(expandUser(dir))
	if dir == "" {
		return "", fmt.Errorf("backup dir required")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(homePath, dir)
	}
	return filepath.Abs(dir)
}

func newestBackupArchive(dir string) (time.Time, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, false, err
	}
	var newest time.Time
	for _, entry := range entries {
		if entry.IsDir() || !isHomeBackupArchiveName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return time.Time{}, false, err
		}
		if newest.IsZero() || info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, !newest.IsZero(), nil
}

func isHomeBackupArchiveName(name string) bool {
	return strings.HasSuffix(name, ".tar.gz") &&
		(strings.HasPrefix(name, "home-") || strings.HasPrefix(name, "compound-home-"))
}

// SetupOptions lets tests inject host/tool state without mutating the process.
type SetupOptions struct {
	RepoRoot        string
	HomeDir         string
	InstallDir      string
	Path            string
	GoVersionOutput string
	LookPath        func(string) (string, error)
	GoVersion       func(env []string) (string, error)
}

func DoctorSetup(opts SetupOptions) SetupReport {
	installDir := strings.TrimSpace(opts.InstallDir)
	if installDir == "" {
		installDir = defaultInstallDir(opts.HomeDir)
	}
	installDir, _ = filepath.Abs(installDir)
	pathValue := opts.Path
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	repoRoot := strings.TrimSpace(opts.RepoRoot)
	if repoRoot == "" {
		repoRoot = findAncestorFile("go.mod")
	}
	requiredGo := requiredGoVersion(repoRoot)
	report := SetupReport{InstallDir: installDir, RequiredGoVersion: requiredGo, OK: true}
	add := func(check SetupCheck) {
		report.Checks = append(report.Checks, check)
		if check.Required && !check.OK {
			report.OK = false
		}
	}

	goPath, goErr := lookPath("go")
	goOK := goErr == nil
	goDetail := "go not found"
	if goOK {
		goDetail = goPath
		if requiredGo != "" {
			versionOut := strings.TrimSpace(opts.GoVersionOutput)
			if versionOut == "" {
				versionOut, _ = runGoVersion(opts.GoVersion)
			}
			if versionOut == "" {
				goOK = false
				goDetail = "could not read go version"
			} else if !goVersionAtLeast(versionOut, requiredGo) {
				goOK = false
				goDetail = versionOut + " < required go" + requiredGo
			} else {
				goDetail = versionOut
			}
		}
	}
	add(SetupCheck{
		Name:     "setup.go_toolchain",
		OK:       goOK,
		Required: true,
		Detail:   goDetail,
		Fix:      "install Go " + firstNonEmpty(requiredGo, "from go.dev/dl") + " or newer, then rerun `make install`",
	})

	add(SetupCheck{
		Name:     "setup.install_dir_on_path",
		OK:       pathContainsDir(pathValue, installDir),
		Required: true,
		Detail:   "install_dir=" + installDir,
		Fix:      `export PATH="` + installDir + `:$PATH"`,
	})

	compoundPath, compoundErr := lookPath("compound")
	add(SetupCheck{
		Name:     "setup.compound_on_path",
		OK:       compoundErr == nil,
		Required: true,
		Detail:   firstNonEmpty(compoundPath, "compound not found"),
		Fix:      "make install",
	})

	bunPath, bunErr := lookPath("bun")
	add(SetupCheck{
		Name:     "setup.bun_optional_console",
		OK:       bunErr == nil,
		Required: false,
		Detail:   firstNonEmpty(bunPath, "bun not found; console is optional"),
		Fix:      "install Bun from https://bun.sh when using the optional console",
	})
	return report
}

func runGoVersion(fn func(env []string) (string, error)) (string, error) {
	env := append(os.Environ(), "GOTOOLCHAIN=local")
	if fn != nil {
		out, err := fn(env)
		return strings.TrimSpace(out), err
	}
	cmd := exec.Command("go", "version")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func defaultInstallDir(homeDir string) string {
	if gobin := strings.TrimSpace(os.Getenv("GOBIN")); gobin != "" {
		return gobin
	}
	if homeDir == "" {
		if h, err := os.UserHomeDir(); err == nil {
			homeDir = h
		}
	}
	if homeDir == "" {
		return filepath.Join(".", "bin")
	}
	return filepath.Join(homeDir, ".local", "bin")
}

func pathContainsDir(pathValue, dir string) bool {
	want, err := filepath.Abs(dir)
	if err != nil {
		want = filepath.Clean(dir)
	}
	for _, part := range filepath.SplitList(pathValue) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		got, err := filepath.Abs(part)
		if err != nil {
			got = filepath.Clean(part)
		}
		if got == want {
			return true
		}
	}
	return false
}

func findAncestorFile(name string) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func requiredGoVersion(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" {
			return fields[1]
		}
	}
	return ""
}

func goVersionAtLeast(output, required string) bool {
	current := parseGoVersion(output)
	req := parseGoVersion(required)
	if len(current) == 0 || len(req) == 0 {
		return false
	}
	for i := 0; i < 3; i++ {
		if current[i] > req[i] {
			return true
		}
		if current[i] < req[i] {
			return false
		}
	}
	return true
}

func parseGoVersion(s string) []int {
	for _, field := range strings.Fields(s) {
		if strings.HasPrefix(field, "go") && len(field) > 2 && field[2] >= '0' && field[2] <= '9' {
			s = field
			break
		}
	}
	s = strings.TrimPrefix(s, "go")
	parts := strings.Split(s, ".")
	out := []int{0, 0, 0}
	found := false
	for i := 0; i < len(parts) && i < 3; i++ {
		part := strings.TrimFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' })
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		out[i] = n
		found = true
	}
	if !found {
		return nil
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
