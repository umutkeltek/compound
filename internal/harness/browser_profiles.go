package harness

// browser_profiles.go — browser-profile registry + doctor.
//
// One canonical registry at ~/.compound/registries/browser-profiles.json
// (compound.browser_profile_registry.v0.1) names which persistent profile
// carries which login. Joins by path to social-accounts.json — every
// browser_profile path in social accounts must resolve to exactly one
// profile here. A profile whose dir is absent on disk reads pending_enrollment
// — that is the human enrollment step, not an error.
//
// Door contract: this package owns the registry file. Callers (CLI, codex
// config, mcporter launch args) read it. The seed path joins social accounts
// — never hand-edit; the CLI seeder is the only writer.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BrowserProfileRegistrySchemaVersion tags the on-disk JSON. Bump the
// registry version when the schema changes; bump the door API separately.
const BrowserProfileRegistrySchemaVersion = "compound.browser_profile_registry.v0.1"

// SocialAccountsSchemaVersion is read for compatibility only — the doctor
// uses the version to refuse the join if it is older than expected.
const SocialAccountsSchemaVersion = "compound.social_account_registry.v0.1"

// BrowserProfile is one row in the registry. The id is the row identity;
// user_data_dir is the path the backends point at; backends carry the
// per-backend launch args (Playwright MCP `--user-data-dir`, agent-browser
// `--profile`, hermes CDP user dir, etc.).
type BrowserProfile struct {
	ID          string                       `json:"id"`
	UserDataDir string                       `json:"user_data_dir"`
	Backends    BrowserProfileBackends       `json:"backends"`
	Logins      []string                     `json:"logins"`
	Status      BrowserProfileStatus         `json:"status"`
	UpdatedAt   string                       `json:"updated_at,omitempty"`
}

// BrowserProfileBackends carries per-backend launch args. Each field is
// optional — backends that are not configured simply omit the field.
type BrowserProfileBackends struct {
	PlaywrightMCP *BrowserProfileBackend `json:"playwright_mcp,omitempty"`
	AgentBrowser  *BrowserProfileBackend `json:"agent_browser,omitempty"`
	HermesCDP     *BrowserProfileBackend `json:"hermes_cdp,omitempty"`
	OmpRelay      *BrowserProfileBackend `json:"omp_relay,omitempty"`
}

// BrowserProfileBackend carries the launch arg fragments for one backend.
// Args are joined with spaces when handed to a shell; for typed callers
// (Playwright MCP JSON config) the caller can read fields directly.
type BrowserProfileBackend struct {
	Args []string `json:"args,omitempty"`
	Note string   `json:"note,omitempty"`
}

// BrowserProfileStatus is the enrollment state. Only two values today:
// "enrolled" (the user_data_dir exists on disk and carries cookies) and
// "pending_enrollment" (the dir is absent — the human has not logged in
// yet). A missing dir is never an error.
type BrowserProfileStatus string

const (
	StatusEnrolled         BrowserProfileStatus = "enrolled"
	StatusPendingEnrollment BrowserProfileStatus = "pending_enrollment"
)

// BrowserProfileRegistry is the file shape on disk.
type BrowserProfileRegistry struct {
	SchemaVersion string           `json:"schema_version"`
	UpdatedAt     string           `json:"updated_at"`
	Profiles      []BrowserProfile `json:"profiles"`
}

// SocialAccount is a SUBSET of the social-accounts.json row — only the
// fields the join cares about. The full row lives in
// ~/.compound/registries/social-accounts.json; the doctor reads it
// directly.
type SocialAccount struct {
	ID            string `json:"id"`
	BrowserProfile string `json:"browser_profile"`
}

// socialAccountsFile is the minimum shape we read.
type socialAccountsFile struct {
	SchemaVersion string          `json:"schema_version"`
	Accounts      []SocialAccount `json:"accounts"`
}

// registryPath is the canonical on-disk location under COMPOUND_HOME.
func registryPath(home string) string {
	return filepath.Join(home, "registries", "browser-profiles.json")
}

// socialAccountsPath is the read-only peer registry we join to.
func socialAccountsPath(home string) string {
	return filepath.Join(home, "registries", "social-accounts.json")
}

// LoadBrowserProfileRegistry reads the registry from disk. A missing file
// returns an empty registry with the current schema version (so a doctor
// call on a fresh home still produces a report).
func LoadBrowserProfileRegistry(home string) (BrowserProfileRegistry, error) {
	var reg BrowserProfileRegistry
	b, err := os.ReadFile(registryPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return BrowserProfileRegistry{SchemaVersion: BrowserProfileRegistrySchemaVersion}, nil
		}
		return reg, fmt.Errorf("read browser-profile registry: %w", err)
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		return reg, fmt.Errorf("decode browser-profile registry: %w", err)
	}
	if reg.SchemaVersion == "" {
		reg.SchemaVersion = BrowserProfileRegistrySchemaVersion
	}
	return reg, nil
}

// LoadSocialAccounts reads the peer registry the profile registry joins to.
func LoadSocialAccounts(home string) ([]SocialAccount, error) {
	var file socialAccountsFile
	b, err := os.ReadFile(socialAccountsPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read social-accounts registry: %w", err)
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("decode social-accounts registry: %w", err)
	}
	return file.Accounts, nil
}

// SeedBrowserProfileRegistry joins social-accounts to the profile registry.
// It creates the file if absent; it never deletes profiles; it never writes
// social-accounts. The status of each row reflects the current filesystem
// state (enrolled vs pending_enrollment).
//
// The id is derived from the social account id when present; if a profile
// row already exists for the same user_data_dir, its id is preserved and
// its logins are merged (no duplicates).
func SeedBrowserProfileRegistry(home string, now time.Time) (BrowserProfileRegistry, error) {
	if now.IsZero() {
		now = time.Now()
	}
	accounts, err := LoadSocialAccounts(home)
	if err != nil {
		return BrowserProfileRegistry{}, err
	}
	existing, err := LoadBrowserProfileRegistry(home)
	if err != nil {
		return BrowserProfileRegistry{}, err
	}

	// Index existing profiles by user_data_dir.
	byDir := make(map[string]int, len(existing.Profiles))
	for i, p := range existing.Profiles {
		byDir[p.UserDataDir] = i
	}

	for _, acct := range accounts {
		dir := expandHome(acct.BrowserProfile, home)
		if dir == "" {
			continue
		}
		if idx, ok := byDir[dir]; ok {
			// Merge this login into the existing row.
			if !containsString(existing.Profiles[idx].Logins, acct.ID) {
				existing.Profiles[idx].Logins = append(existing.Profiles[idx].Logins, acct.ID)
			}
			existing.Profiles[idx].Status = deriveStatus(dir)
			continue
		}
		existing.Profiles = append(existing.Profiles, BrowserProfile{
			ID:          acct.ID,
			UserDataDir: dir,
			Backends:    BrowserProfileBackends{},
			Logins:      []string{acct.ID},
			Status:      deriveStatus(dir),
		})
		byDir[dir] = len(existing.Profiles) - 1
	}
	// Re-derive status for every profile (an operator may have created the
	// dir since the last seed).
	for i := range existing.Profiles {
		existing.Profiles[i].Status = deriveStatus(existing.Profiles[i].UserDataDir)
	}
	// Stable order by id.
	sort.SliceStable(existing.Profiles, func(i, j int) bool {
		return existing.Profiles[i].ID < existing.Profiles[j].ID
	})
	existing.SchemaVersion = BrowserProfileRegistrySchemaVersion
	existing.UpdatedAt = now.UTC().Format(time.RFC3339)
	return existing, nil
}

// WriteBrowserProfileRegistry persists the registry through the door. It
// refuses to write if the file's directory does not exist under home (a
// guard against a typo'd home path).
func WriteBrowserProfileRegistry(home string, reg BrowserProfileRegistry) error {
	if strings.TrimSpace(home) == "" {
		return fmt.Errorf("write browser-profile registry: home is empty")
	}
	if reg.SchemaVersion == "" {
		reg.SchemaVersion = BrowserProfileRegistrySchemaVersion
	}
	dir := filepath.Dir(registryPath(home))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	if err := os.WriteFile(registryPath(home), b, 0o644); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return nil
}

// BrowserProfileDoctorReport is the doctor's projection of the registry.
// Counts and lists come from the in-memory join; never a directory listing.
type BrowserProfileDoctorReport struct {
	SchemaVersion     string                  `json:"schema_version"`
	UpdatedAt         string                  `json:"updated_at"`
	ProfileCount      int                     `json:"profile_count"`
	EnrolledCount     int                     `json:"enrolled_count"`
	PendingCount      int                     `json:"pending_count"`
	UnresolvedPaths   []string                `json:"unresolved_paths,omitempty"`
	Profiles          []BrowserProfileSummary `json:"profiles"`
}

// BrowserProfileSummary is one row in the report.
type BrowserProfileSummary struct {
	ID          string               `json:"id"`
	UserDataDir string               `json:"user_data_dir"`
	Status      BrowserProfileStatus `json:"status"`
	Logins      []string             `json:"logins"`
	Backends    []string             `json:"backends"`
}

// DoctorBrowserProfileRegistry returns a doctor report. The report joins
// social-accounts to the registry by path; an account whose path does not
// resolve to a profile is reported in `unresolved_paths` (the operator can
// run the seed command to bring them in).
func DoctorBrowserProfileRegistry(home string) (BrowserProfileDoctorReport, error) {
	rep := BrowserProfileDoctorReport{
		SchemaVersion: BrowserProfileRegistrySchemaVersion,
	}
	reg, err := LoadBrowserProfileRegistry(home)
	if err != nil {
		return rep, err
	}
	accounts, err := LoadSocialAccounts(home)
	if err != nil {
		return rep, err
	}
	byDir := make(map[string]bool, len(reg.Profiles))
	for _, p := range reg.Profiles {
		byDir[p.UserDataDir] = true
	}
	for _, acct := range accounts {
		dir := expandHome(acct.BrowserProfile, home)
		if dir == "" {
			continue
		}
		if !byDir[dir] {
			rep.UnresolvedPaths = append(rep.UnresolvedPaths, dir)
		}
	}
	sort.Strings(rep.UnresolvedPaths)
	rep.ProfileCount = len(reg.Profiles)
	for _, p := range reg.Profiles {
		status := deriveStatus(p.UserDataDir)
		if status != p.Status {
			// Don't mutate the in-memory reg here — doctor is read-only. We
			// surface the derived status in the report so the operator sees
			// the truth even if the file is stale.
			p.Status = status
		}
		sum := BrowserProfileSummary{
			ID:          p.ID,
			UserDataDir: p.UserDataDir,
			Status:      p.Status,
			Logins:      append([]string(nil), p.Logins...),
		}
		if p.Backends.PlaywrightMCP != nil {
			sum.Backends = append(sum.Backends, "playwright_mcp")
		}
		if p.Backends.AgentBrowser != nil {
			sum.Backends = append(sum.Backends, "agent_browser")
		}
		if p.Backends.HermesCDP != nil {
			sum.Backends = append(sum.Backends, "hermes_cdp")
		}
		if p.Backends.OmpRelay != nil {
			sum.Backends = append(sum.Backends, "omp_relay")
		}
		rep.Profiles = append(rep.Profiles, sum)
		if p.Status == StatusEnrolled {
			rep.EnrolledCount++
		} else {
			rep.PendingCount++
		}
	}
	sort.SliceStable(rep.Profiles, func(i, j int) bool {
		return rep.Profiles[i].ID < rep.Profiles[j].ID
	})
	return rep, nil
}

// expandHome resolves "~/" prefix to the home argument. Social accounts
// carry browser_profile as "~/.compound/social-profiles/<account>" — the
// tilde must expand against the COMPOUND_HOME we resolve at runtime, not
// against the user's $HOME.
func expandHome(p, home string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, strings.TrimPrefix(p, "~/"))
	}
	if p == "~" {
		return home
	}
	return p
}

// deriveStatus maps a user_data_dir's filesystem state to the registry row.
// Missing dir is pending_enrollment by design — that is the human enrollment
// step, not an error. A present-but-empty dir is still pending_enrollment
// (no cookies yet).
func deriveStatus(dir string) BrowserProfileStatus {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return StatusPendingEnrollment
	}
	return StatusEnrolled
}

// containsString is the dedupe helper for the seed merge.
func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// FormatBrowserProfileReport renders the doctor report as compact CLI text.
// Used by the CLI dispatcher so cmd/compound stays at a tiny delta.
func FormatBrowserProfileReport(rep BrowserProfileDoctorReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# harness browser-profiles doctor profiles=%d enrolled=%d pending=%d\n",
		rep.ProfileCount, rep.EnrolledCount, rep.PendingCount)
	for _, p := range rep.Profiles {
		fmt.Fprintf(&b, "- %s status=%s dir=%s logins=%v backends=%v\n",
			p.ID, p.Status, p.UserDataDir, p.Logins, p.Backends)
	}
	if len(rep.UnresolvedPaths) > 0 {
		b.WriteString("- unresolved_paths:\n")
		for _, u := range rep.UnresolvedPaths {
			fmt.Fprintf(&b, "  %s\n", u)
		}
	}
	return b.String()
}

// RunBrowserProfileDoctorCLI parses flags, runs the doctor, prints. The
// cmd/compound dispatcher hands off everything to here so the cmd delta
// stays small.
func RunBrowserProfileDoctorCLI(homePath string, asJSON bool) error {
	rep, err := DoctorBrowserProfileRegistry(homePath)
	if err != nil {
		return err
	}
	if asJSON {
		b, err := json.Marshal(rep)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Print(FormatBrowserProfileReport(rep))
	return nil
}
