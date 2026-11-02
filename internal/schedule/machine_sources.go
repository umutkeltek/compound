//go:build darwin

package schedule

// Background work on this machine starts from more places than launchd and
// crontab. This file reads the others (Hermes jobs, login items, Docker), the
// real launchd exit state, and an optional local roster that records who owns
// each item. Everything here stays read-only: it reports, it never changes state.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	scheduleSourceHermes    scheduleSource = "hermes"
	scheduleSourceLoginItem scheduleSource = "login-item"
	scheduleSourceDocker    scheduleSource = "docker"

	backgroundRosterSchema = "compound/background-roster.v0.1"

	issueFailing          = "failing"
	issueTargetMissing    = "target-missing"
	issueLogFolderMissing = "log-folder-missing"
	issueNetworkExposed   = "network-exposed"
	issueUnknown          = "unknown"
	issueUnexpectedActive = "unexpected-running"
	issueMissing          = "missing"
)

// scheduleProblemCodes are findings that need a decision. Every other code is
// information, summarized as a count unless the operator asks for --all.
var scheduleProblemCodes = map[string]bool{
	issueFailing:          true,
	issueTargetMissing:    true,
	issueLogFolderMissing: true,
	issueNetworkExposed:   true,
	issueUnknown:          true,
	issueUnexpectedActive: true,
	issueMissing:          true,
	"missing-binary":      true,
	"duplicate-label":     true,
	"source-read-error":   true,
}

// scheduleIssue is one problem a collector found while reading an entry.
type scheduleIssue struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type launchdProcessState struct {
	State    string
	PID      string
	LastExit string
}

var launchdTopLevelField = regexp.MustCompile(`^\t(state|pid|last exit code) = (.*)$`)

// parseLaunchctlPrint reads only the service's own fields. Nested blocks repeat
// names such as "state" with deeper indentation and must not win.
func parseLaunchctlPrint(text string) launchdProcessState {
	var p launchdProcessState
	for _, line := range strings.Split(text, "\n") {
		m := launchdTopLevelField.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch m[1] {
		case "state":
			if p.State == "" {
				p.State = m[2]
			}
		case "pid":
			if p.PID == "" {
				p.PID = m[2]
			}
		case "last exit code":
			if p.LastExit == "" {
				p.LastExit = m[2]
			}
		}
	}
	return p
}

// launchdFailureDetail is empty when the job is running or its last run succeeded.
// A running service keeps the exit code of its previous life, which proves nothing.
func launchdFailureDetail(p launchdProcessState) string {
	if p.State == "running" || p.LastExit == "" || strings.HasPrefix(p.LastExit, "(") {
		return ""
	}
	code := strings.TrimSpace(strings.SplitN(p.LastExit, ":", 2)[0])
	if code == "0" {
		return ""
	}
	return fmt.Sprintf("last exit code %s (state: %s)", p.LastExit, p.State)
}

// launchdLogFolderIssue names a log folder that is gone. launchd will not start a
// job whose StandardOutPath or StandardErrorPath sits in a missing folder, and the
// only symptom is a nonzero exit with nothing written anywhere.
func launchdLogFolderIssue(e ScheduleEntry, exists func(string) bool) *scheduleIssue {
	for _, p := range []string{e.StdOutPath, e.StdErrPath} {
		if p == "" {
			continue
		}
		dir := filepath.Dir(expandEnvPath(p, e.Env))
		if !exists(dir) {
			return &scheduleIssue{Code: issueLogFolderMissing, Detail: "log folder is missing, so launchd cannot start this job: " + dir}
		}
	}
	return nil
}

// programExists resolves a bare command name the way launchd does. A bare "sh" is
// fine; only a named path that is gone means the job cannot start.
func programExists(program string) error {
	if strings.Contains(program, "/") {
		_, err := os.Stat(program)
		return err
	}
	for _, dir := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		if _, err := os.Stat(filepath.Join(dir, program)); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%q is not on launchd's default PATH", program)
}

// findingLocation names an item so a person can find it: launchd and crontab items
// by file, the rest by the name they show in their own tool.
func findingLocation(f scheduleDoctorFinding) string {
	switch f.Source {
	case scheduleSourceHermes, scheduleSourceDocker, scheduleSourceLoginItem:
		return string(f.Source) + ":" + f.Label
	}
	if f.Path != "" {
		return string(f.Source) + ":" + f.Path
	}
	return string(f.Source) + ":" + f.Label
}

func folderExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

type hermesJob struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Enabled           *bool  `json:"enabled"`
	ScheduleDisplay   string `json:"schedule_display"`
	Script            string `json:"script"`
	LastStatus        string `json:"last_status"`
	LastError         string `json:"last_error"`
	LastDeliveryError string `json:"last_delivery_error"`
	FailureStreak     int    `json:"failure_streak"`
}

// hermesEntriesFromJSON projects the Hermes job file. Error text can quote command
// output, so only the first line is kept and values never reach the projection.
func hermesEntriesFromJSON(raw []byte, path string) ([]ScheduleEntry, error) {
	var file struct {
		Jobs []hermesJob `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse Hermes jobs: %w", err)
	}
	entries := make([]ScheduleEntry, 0, len(file.Jobs))
	for _, j := range file.Jobs {
		enabled := j.Enabled == nil || *j.Enabled
		e := ScheduleEntry{Label: j.Name, Path: path + "#" + j.ID, Source: scheduleSourceHermes, Disabled: !enabled, DeclaredDisabledState: "declared"}
		if j.Script != "" {
			script := j.Script
			if !filepath.IsAbs(script) {
				script = filepath.Join(filepath.Dir(filepath.Dir(path)), "scripts", script)
			}
			e.ProgramArgs = []string{script}
		}
		if enabled && j.LastStatus != "" && j.LastStatus != "ok" {
			why := j.LastError
			if why == "" {
				why = j.LastDeliveryError
			}
			detail := "last status " + j.LastStatus
			if j.FailureStreak > 0 {
				detail += ", " + strconv.Itoa(j.FailureStreak) + " in a row"
			}
			if first := firstLine(why); first != "" {
				detail += ": " + first
			}
			e.Issues = append(e.Issues, scheduleIssue{Code: issueFailing, Detail: detail})
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

var (
	btmRecordsHeader = regexp.MustCompile(`Records for UID (-?\d+)`)
	btmField         = regexp.MustCompile(`^\s*(Name|Type|Disposition|URL):\s*(.*)$`)
)

// loginItemEntriesFromBTM reads `sfltool dumpbtm` for one user. Only app records
// are login items; launchd jobs also appear there and are already covered.
func loginItemEntriesFromBTM(text string, uid int, exists func(string) bool) []ScheduleEntry {
	var entries []ScheduleEntry
	var name, typ, disposition, target string
	current := false
	flush := func() {
		if current && name != "" && strings.HasPrefix(typ, "app") && strings.HasPrefix(disposition, "[enabled") {
			e := ScheduleEntry{Label: name, Path: target, Source: scheduleSourceLoginItem, DeclaredDisabledState: "declared"}
			if target != "" && !exists(target) {
				e.Issues = append(e.Issues, scheduleIssue{Code: issueTargetMissing, Detail: "login item points at a path that no longer exists: " + target})
			}
			entries = append(entries, e)
		}
		name, typ, disposition, target = "", "", "", ""
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := btmRecordsHeader.FindStringSubmatch(line); m != nil {
			flush()
			n, err := strconv.Atoi(m[1])
			current = err == nil && n == uid
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			flush()
			continue
		}
		m := btmField.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch m[1] {
		case "Name":
			name = m[2]
		case "Type":
			typ = m[2]
		case "Disposition":
			disposition = m[2]
		case "URL":
			target = fileURLPath(m[2])
		}
	}
	flush()
	return entries
}

func fileURLPath(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// dockerEntriesFromPS reads `docker ps -a --format '{{json .}}'`, one object per line.
func dockerEntriesFromPS(raw []byte) ([]ScheduleEntry, error) {
	var entries []ScheduleEntry
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var c struct {
			Names string `json:"Names"`
			State string `json:"State"`
			Ports string `json:"Ports"`
			Image string `json:"Image"`
		}
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("parse docker ps: %w", err)
		}
		e := ScheduleEntry{Label: c.Names, Path: "docker:" + c.Image, Source: scheduleSourceDocker, Disabled: c.State != "running", DeclaredDisabledState: "declared"}
		if c.State == "running" && (strings.Contains(c.Ports, "0.0.0.0:") || strings.Contains(c.Ports, "[::]:")) {
			e.Issues = append(e.Issues, scheduleIssue{Code: issueNetworkExposed, Detail: "published on all network interfaces: " + c.Ports})
		}
		entries = append(entries, e)
	}
	return entries, sc.Err()
}

func runBounded(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func collectHermesJobs() ([]ScheduleEntry, error) {
	path := filepath.Join(homeUserHome(), ".hermes", "cron", "jobs.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return hermesEntriesFromJSON(raw, path)
}

func collectLoginItems() ([]ScheduleEntry, error) {
	// macOS shows an admin password dialog for this read, and the read waits for an answer.
	// Callers reach it only on an explicit request; allow time to answer, then give up.
	out, err := runBounded(2*time.Minute, "/usr/bin/sfltool", "dumpbtm")
	if err != nil {
		return nil, fmt.Errorf("sfltool dumpbtm, which needs the macOS admin password dialog answered: %w", err)
	}
	return loginItemEntriesFromBTM(string(out), os.Getuid(), func(p string) bool {
		_, statErr := os.Stat(p)
		return statErr == nil
	}), nil
}

// collectDockerContainers reports nothing when Docker is absent or stopped: a stopped
// daemon runs no containers, and that is not a fault in this surface.
func collectDockerContainers() ([]ScheduleEntry, error) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		for _, p := range []string{"/usr/local/bin/docker", "/opt/homebrew/bin/docker"} {
			if _, statErr := os.Stat(p); statErr == nil {
				docker = p
				break
			}
		}
	}
	if docker == "" {
		return nil, nil
	}
	out, err := runBounded(10*time.Second, docker, "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, nil
	}
	return dockerEntriesFromPS(out)
}

// backgroundRosterItem records why one background item exists and whether it should run.
type backgroundRosterItem struct {
	Source   string `json:"source"`
	Label    string `json:"label"`
	Owner    string `json:"owner"`
	Purpose  string `json:"purpose"`
	Expected string `json:"expected"` // on, on-demand, off, or any (owned, no opinion on running)
}

type backgroundRoster struct {
	Schema string                 `json:"schema"`
	Items  []backgroundRosterItem `json:"items"`
}

func backgroundRosterPath() string {
	home := strings.TrimSpace(os.Getenv("COMPOUND_HOME"))
	if home == "" {
		home = filepath.Join(homeUserHome(), ".compound")
	}
	return filepath.Join(home, "config", "background-roster.json")
}

// loadBackgroundRoster returns nil when no roster exists yet: the roster is opt-in,
// and without one the doctor cannot say an item is unknown.
func loadBackgroundRoster(path string) (*backgroundRoster, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r backgroundRoster
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse background roster: %w", err)
	}
	if r.Schema != backgroundRosterSchema {
		return nil, fmt.Errorf("background roster schema %q, want %q", r.Schema, backgroundRosterSchema)
	}
	for _, it := range r.Items {
		switch it.Expected {
		case "on", "on-demand", "off", "any":
		default:
			return nil, fmt.Errorf("background roster %s:%s: expected %q must be on, on-demand, off or any", it.Source, it.Label, it.Expected)
		}
	}
	return &r, nil
}

// entryIsActive says whether an entry will run without anyone starting it.
func entryIsActive(e ScheduleEntry) bool {
	if e.Disabled || e.DisabledByExt {
		return false
	}
	if e.Runtime != nil {
		return e.Runtime.EffectiveEnablement != "disabled" && e.Runtime.LoadedState != "absent"
	}
	return true
}

// entryIsRunningNow is narrower: for on-demand launchd items, being loaded is normal.
func entryIsRunningNow(e ScheduleEntry) bool {
	if e.Runtime != nil {
		return e.Runtime.ProcessState == "running"
	}
	return entryIsActive(e)
}

func rosterKey(source, label string) string { return source + "\x00" + label }

// rosterFindings compares what is on the machine with what the roster says should be.
func rosterFindings(entries []ScheduleEntry, roster *backgroundRoster, unread map[string]bool) []scheduleDoctorFinding {
	if roster == nil {
		return nil
	}
	items := make(map[string]backgroundRosterItem, len(roster.Items))
	for _, it := range roster.Items {
		items[rosterKey(it.Source, it.Label)] = it
	}
	var findings []scheduleDoctorFinding
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		key := rosterKey(string(e.Source), e.Label)
		seen[key] = entryIsActive(e)
		it, listed := items[key]
		switch {
		case !listed:
			if entryIsActive(e) {
				findings = append(findings, scheduleDoctorFinding{Label: e.Label, Path: e.Path, Source: e.Source, Code: issueUnknown, Detail: "active but not in the roster, so nobody is recorded as owning it"})
			}
		case (it.Expected == "on-demand" || it.Expected == "off") && entryIsRunningNow(e):
			findings = append(findings, scheduleDoctorFinding{Label: e.Label, Path: e.Path, Source: e.Source, Code: issueUnexpectedActive, Detail: fmt.Sprintf("roster expects %s, owner %s", it.Expected, it.Owner)})
		}
	}
	for _, it := range roster.Items {
		// A source not read this run cannot show that an item is gone.
		if it.Expected == "on" && !unread[it.Source] && !seen[rosterKey(it.Source, it.Label)] {
			findings = append(findings, scheduleDoctorFinding{Label: it.Label, Source: scheduleSource(it.Source), Code: issueMissing, Detail: fmt.Sprintf("roster expects it on, owner %s, but it is not active", it.Owner)})
		}
	}
	return findings
}
