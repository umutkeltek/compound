//go:build darwin

package schedule

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLaunchctlPrintIgnoresNestedFields(t *testing.T) {
	text := "gui/501/com.example.job = {\n\tactive count = 0\n\tstate = not running\n\n\tprogram = /bin/sh\n\tinherited environment = {\n\t\tstate = nested\n\t}\n\truns = 10\n\tlast exit code = 78: EX_CONFIG\n\tspawn type = daemon (3)\n\tevent triggers = {\n\t\tx = {\n\t\t\tstate = active\n\t\t}\n\t}\n}\n"
	p := parseLaunchctlPrint(text)
	if p.State != "not running" || p.LastExit != "78: EX_CONFIG" {
		t.Fatalf("got %+v", p)
	}
}

func TestLaunchdFailureDetail(t *testing.T) {
	cases := []struct {
		name string
		in   launchdProcessState
		fail bool
	}{
		{"last run succeeded", launchdProcessState{State: "not running", LastExit: "0"}, false},
		{"never ran", launchdProcessState{State: "not running", LastExit: "(never exited)"}, false},
		{"running after an earlier crash proves nothing", launchdProcessState{State: "running", LastExit: "1"}, false},
		{"not loaded has no exit", launchdProcessState{}, false},
		{"plain failure", launchdProcessState{State: "not running", LastExit: "1"}, true},
		{"named failure", launchdProcessState{State: "spawn scheduled", LastExit: "78: EX_CONFIG"}, true},
		{"killed by signal", launchdProcessState{State: "not running", LastExit: "-9"}, true},
	}
	for _, c := range cases {
		if got := launchdFailureDetail(c.in) != ""; got != c.fail {
			t.Errorf("%s: failing=%v, want %v", c.name, got, c.fail)
		}
	}
}

func TestLaunchdLogFolderIssue(t *testing.T) {
	present := func(p string) bool { return p == "/logs/present" }
	e := ScheduleEntry{StdOutPath: "/logs/present/out.log", StdErrPath: "/logs/gone/err.log"}
	issue := launchdLogFolderIssue(e, present)
	if issue == nil || issue.Code != issueLogFolderMissing || !strings.Contains(issue.Detail, "/logs/gone") {
		t.Fatalf("got %+v", issue)
	}
	if got := launchdLogFolderIssue(ScheduleEntry{StdOutPath: "/logs/present/out.log"}, present); got != nil {
		t.Fatalf("present folder reported: %+v", got)
	}
	if got := launchdLogFolderIssue(ScheduleEntry{}, present); got != nil {
		t.Fatalf("job without logs reported: %+v", got)
	}
}

func TestHermesEntriesFromJSON(t *testing.T) {
	raw := []byte(`{"jobs":[
	 {"id":"a1","name":"healthy","enabled":true,"last_status":"ok","script":"a.sh"},
	 {"id":"b2","name":"broken","enabled":true,"last_status":"error","failure_streak":4,"last_error":"Script timed out\nsecond line"},
	 {"id":"c3","name":"paused","enabled":false,"last_status":"error"},
	 {"id":"d4","name":"undelivered","last_status":"delivery_failed","last_delivery_error":"flood control"}
	]}`)
	got, err := hermesEntriesFromJSON(raw, "/h/cron/jobs.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d entries", len(got))
	}
	if len(got[0].ProgramArgs) != 1 || got[0].ProgramArgs[0] != "/h/scripts/a.sh" {
		t.Errorf("script must resolve under the Hermes scripts folder: %+v", got[0].ProgramArgs)
	}
	if len(got[0].Issues) != 0 || got[0].Disabled {
		t.Errorf("healthy job: %+v", got[0])
	}
	if len(got[1].Issues) != 1 || !strings.Contains(got[1].Issues[0].Detail, "4 in a row: Script timed out") || strings.Contains(got[1].Issues[0].Detail, "second line") {
		t.Errorf("broken job: %+v", got[1].Issues)
	}
	if !got[2].Disabled || len(got[2].Issues) != 0 {
		t.Errorf("paused job must not count as failing: %+v", got[2])
	}
	if got[3].Disabled || len(got[3].Issues) != 1 || !strings.Contains(got[3].Issues[0].Detail, "flood control") {
		t.Errorf("job without an enabled field defaults on: %+v", got[3])
	}
	if _, err := hermesEntriesFromJSON([]byte(`{`), "/jobs.json"); err == nil {
		t.Error("broken file must be an error, not an empty projection")
	}
}

const btmFixture = `========================
 Records for UID -2 : FFFFEEEE
========================
 #1:
                 Name: OtherUser
                 Type: app (0x2)
          Disposition: [enabled, allowed, notified] (0xb)
                  URL: file:///Applications/OtherUser.app/
========================
 Records for UID 501 : AAAA
========================
 #1:
                 Name: Present App
                 Type: app (0x2)
          Disposition: [enabled, allowed, notified] (0xb)
                  URL: file:///Applications/Present%20App.app/
 #2:
                 Name: Removed App
                 Type: app (0x2)
          Disposition: [enabled, allowed, notified] (0xb)
                  URL: file:///Users/someone/.Trash/Removed%20App.app/
 #3:
                 Name: Switched Off
                 Type: app (0x2)
          Disposition: [disabled, allowed, notified] (0xa)
                  URL: file:///Applications/Off.app/
 #4:
                 Name: bash
                 Type: legacy agent (0x8)
          Disposition: [enabled, allowed, notified] (0xb)
                  URL: (null)
`

func TestLoginItemEntriesFromBTM(t *testing.T) {
	exists := func(p string) bool { return p == "/Applications/Present App.app" }
	got := loginItemEntriesFromBTM(btmFixture, 501, exists)
	if len(got) != 2 {
		t.Fatalf("want only this user's enabled apps, got %+v", got)
	}
	if got[0].Label != "Present App" || len(got[0].Issues) != 0 || got[0].Path != "/Applications/Present App.app" {
		t.Errorf("present item: %+v", got[0])
	}
	if got[1].Label != "Removed App" || len(got[1].Issues) != 1 || got[1].Issues[0].Code != issueTargetMissing {
		t.Errorf("removed item: %+v", got[1])
	}
}

func TestDockerEntriesFromPS(t *testing.T) {
	raw := []byte(`{"Names":"open","State":"running","Ports":"0.0.0.0:5432->5432/tcp, [::]:5432->5432/tcp","Image":"pg"}
{"Names":"local","State":"running","Ports":"127.0.0.1:9000->9000/tcp","Image":"app"}
{"Names":"stopped","State":"exited","Ports":"","Image":"old"}
`)
	got, err := dockerEntriesFromPS(raw)
	if err != nil || len(got) != 3 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if len(got[0].Issues) != 1 || got[0].Issues[0].Code != issueNetworkExposed {
		t.Errorf("exposed container: %+v", got[0])
	}
	if len(got[1].Issues) != 0 || got[1].Disabled {
		t.Errorf("loopback container: %+v", got[1])
	}
	if !got[2].Disabled {
		t.Errorf("stopped container must not be active: %+v", got[2])
	}
}

func TestRosterFindings(t *testing.T) {
	roster := &backgroundRoster{Schema: backgroundRosterSchema, Items: []backgroundRosterItem{
		{Source: "user", Label: "owned", Owner: "a", Expected: "on"},
		{Source: "user", Label: "gone", Owner: "a", Expected: "on"},
		{Source: "user", Label: "rare-loaded", Owner: "a", Expected: "on-demand"},
		{Source: "user", Label: "rare-running", Owner: "a", Expected: "on-demand"},
		{Source: "hermes", Label: "retired", Owner: "a", Expected: "off"},
		{Source: "docker", Label: "dev-db", Owner: "a", Expected: "any"},
		{Source: "docker", Label: "dev-db-stopped", Owner: "a", Expected: "any"},
	}}
	loaded := func(process string) *ScheduleRuntimeState {
		return &ScheduleRuntimeState{EffectiveEnablement: "enabled", LoadedState: "loaded", ProcessState: process}
	}
	entries := []ScheduleEntry{
		{Source: scheduleSourceUser, Label: "owned", Runtime: loaded("running")},
		{Source: scheduleSourceUser, Label: "rare-loaded", Runtime: loaded("not running")},
		{Source: scheduleSourceUser, Label: "rare-running", Runtime: loaded("running")},
		{Source: scheduleSourceUser, Label: "stranger", Runtime: loaded("running")},
		{Source: scheduleSourceUser, Label: "stranger-off", Runtime: &ScheduleRuntimeState{EffectiveEnablement: "disabled", LoadedState: "absent"}},
		{Source: scheduleSourceHermes, Label: "retired"},
		{Source: scheduleSourceDocker, Label: "dev-db"},
		{Source: scheduleSourceDocker, Label: "dev-db-stopped", Disabled: true},
	}
	codes := map[string]string{}
	for _, f := range rosterFindings(entries, roster) {
		codes[f.Label] = f.Code
	}
	want := map[string]string{"gone": issueMissing, "rare-running": issueUnexpectedActive, "stranger": issueUnknown, "retired": issueUnexpectedActive}
	if len(codes) != len(want) {
		t.Fatalf("got %v, want %v", codes, want)
	}
	for k, v := range want {
		if codes[k] != v {
			t.Errorf("%s: got %q, want %q", k, codes[k], v)
		}
	}
	if got := rosterFindings(entries, nil); got != nil {
		t.Errorf("no roster must mean no roster findings: %+v", got)
	}
}

func TestLoadBackgroundRoster(t *testing.T) {
	dir := t.TempDir()
	if r, err := loadBackgroundRoster(filepath.Join(dir, "absent.json")); r != nil || err != nil {
		t.Fatalf("absent roster: %+v %v", r, err)
	}
	write := func(body string) string {
		p := filepath.Join(dir, "r.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := loadBackgroundRoster(write(`{"schema":"other","items":[]}`)); err == nil {
		t.Error("wrong schema must fail")
	}
	if _, err := loadBackgroundRoster(write(`{"schema":"` + backgroundRosterSchema + `","items":[{"source":"user","label":"x","expected":"maybe"}]}`)); err == nil {
		t.Error("unknown expected state must fail")
	}
	r, err := loadBackgroundRoster(write(`{"schema":"` + backgroundRosterSchema + `","items":[{"source":"user","label":"x","owner":"o","purpose":"p","expected":"on"}]}`))
	if err != nil || r == nil || len(r.Items) != 1 {
		t.Fatalf("valid roster: %+v %v", r, err)
	}
}

func TestProgramExists(t *testing.T) {
	if err := programExists("sh"); err != nil {
		t.Errorf("bare sh: %v", err)
	}
	if err := programExists("no-such-command-here"); err == nil {
		t.Error("unknown bare command must fail")
	}
	if err := programExists(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Error("missing path must fail")
	}
}

func TestFindingLocation(t *testing.T) {
	if got := findingLocation(scheduleDoctorFinding{Source: scheduleSourceHermes, Label: "job", Path: "/jobs.json#1"}); got != "hermes:job" {
		t.Errorf("hermes: %q", got)
	}
	if got := findingLocation(scheduleDoctorFinding{Source: scheduleSourceUser, Label: "l", Path: "/p.plist"}); got != "user:/p.plist" {
		t.Errorf("launchd: %q", got)
	}
}
