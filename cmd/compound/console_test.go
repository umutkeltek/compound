package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestConsoleLifecycleDryRunsExposeDistinctDevBuildAndServePlans(t *testing.T) {
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"_next:dev":"next dev","_next:build":"next build","_next:serve":"next start"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	homePath := filepath.Join(root, ".compound")

	dev := runConsoleCommand(t, homePath, "dev", "--operator", "--console-dir", consoleDir, "--dry-run", "--json")
	build := runConsoleCommand(t, homePath, "build", "--operator", "--console-dir", consoleDir, "--dry-run", "--json")
	serve := runConsoleCommand(t, homePath, "serve", "--operator", "--console-dir", consoleDir, "--dry-run", "--json")

	if dev.Lifecycle != "dev" {
		t.Fatalf("dev lifecycle = %q, want dev", dev.Lifecycle)
	}
	if got, want := dev.Command, []string{"bun", "run", "_next:dev"}; !sameStringSlice(got, want) {
		t.Fatalf("dev command = %v, want %v", got, want)
	}
	if got := dev.Env["COMPOUND_CONSOLE_BUILD_KIND"]; got != "development" {
		t.Fatalf("dev build kind = %q, want development", got)
	}

	if build.Lifecycle != "build" {
		t.Fatalf("build lifecycle = %q, want build", build.Lifecycle)
	}
	if got, want := build.Command, []string{"bun", "run", "_next:build"}; !sameStringSlice(got, want) {
		t.Fatalf("build command = %v, want %v", got, want)
	}
	if got := build.Env["COMPOUND_CONSOLE_BUILD_KIND"]; got != "production" {
		t.Fatalf("build kind = %q, want production", got)
	}

	if serve.Lifecycle != "serve" {
		t.Fatalf("serve lifecycle = %q, want serve", serve.Lifecycle)
	}
	if got, want := serve.Command, []string{"bun", "run", "_next:serve"}; !sameStringSlice(got, want) {
		t.Fatalf("serve command = %v, want %v", got, want)
	}
	if got := serve.Env["COMPOUND_CONSOLE_BUILD_KIND"]; got != "production" {
		t.Fatalf("serve build kind = %q, want production", got)
	}
}

func TestConsoleBuildReservesCanonicalPortBeforeBun(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:3777")
	if err == nil {
		defer listener.Close()
	}

	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"_next:build":"next build"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "bun-ran")
	fakeBun := "#!/bin/sh\n: > \"$COMPOUND_CONSOLE_BUN_MARKER\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "bun"), []byte(fakeBun), 0o755); err != nil {
		t.Fatal(err)
	}

	_, stderr, runErr := invokeConsoleCommand(t, []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"COMPOUND_CONSOLE_BUN_MARKER=" + marker,
	}, "build", "--operator", "--console-dir", consoleDir)
	if runErr == nil {
		t.Fatal("console build succeeded with canonical port already owned")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("production build ran before port preflight; marker stat error = %v", err)
	}
	if !strings.Contains(stderr, "port 3777") || !strings.Contains(stderr, "status") {
		t.Fatalf("port preflight error must identify the canonical port and status action:\n%s", stderr)
	}
}

func TestConsoleLifecycleLockRejectsConcurrentArtifactOwner(t *testing.T) {
	consoleDir := filepath.Join(t.TempDir(), "console")
	first, err := acquireConsoleLifecycleLock(consoleDir)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseConsoleLifecycleLock(first)

	if _, err := acquireConsoleLifecycleLock(consoleDir); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("second lifecycle lock error = %v, want already active", err)
	}

	releaseConsoleLifecycleLock(first)
	first = nil
	third, err := acquireConsoleLifecycleLock(consoleDir)
	if err != nil {
		t.Fatalf("lock was not released with its descriptor: %v", err)
	}
	releaseConsoleLifecycleLock(third)
}

func TestConsoleServeRefusesMissingProductionBuildBeforeBun(t *testing.T) {
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"_next:serve":"next start"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "bun-ran")
	fakeBun := "#!/bin/sh\n: > \"$COMPOUND_CONSOLE_BUN_MARKER\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "bun"), []byte(fakeBun), 0o755); err != nil {
		t.Fatal(err)
	}

	_, stderr, runErr := invokeConsoleCommand(t, []string{
		"PATH=" + binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"COMPOUND_CONSOLE_BUN_MARKER=" + marker,
	}, "serve", "--operator", "--console-dir", consoleDir)
	if runErr == nil {
		t.Fatal("console serve succeeded without a production BUILD_ID")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Bun ran before production build validation; marker stat error = %v", err)
	}
	if !strings.Contains(stderr, "console build") || !strings.Contains(stderr, "BUILD_ID") {
		t.Fatalf("missing-build error must name the build action and artifact:\n%s", stderr)
	}
}

func TestConsoleLifecycleCanRequireAnExplicitHome(t *testing.T) {
	consoleDir := filepath.Join(t.TempDir(), "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"_next:dev":"next dev"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := invokeConsoleCommand(
		t,
		[]string{"COMPOUND_HOME="},
		"dev", "--operator", "--require-explicit-home", "--console-dir", consoleDir, "--dry-run",
	)
	if err == nil {
		t.Fatal("package lifecycle silently pinned the implicit canonical home")
	}
	if !strings.Contains(stderr, "explicit COMPOUND_HOME") {
		t.Fatalf("implicit-home refusal must explain the pin requirement:\n%s", stderr)
	}
}

func TestConsoleRuntimeStatusDistinguishesHealthyUnhealthyAndForeign(t *testing.T) {
	homePath := filepath.Join(t.TempDir(), ".compound")
	expectedHash := consoleHomeShortHash(homePath)

	for _, tc := range []struct {
		name      string
		root      string
		badAsset  string
		rootDelay time.Duration
		wantState string
		wantKind  string
	}{
		{
			name:      "healthy canonical console",
			root:      `<html><body><span data-instance-home-hash="` + expectedHash + `" data-instance-explicit="1" data-instance-build-kind="production"></span><link href="/_next/static/css/app.css"><script src="/_next/static/chunks/app.js"></script></body></html>`,
			wantState: "healthy",
			wantKind:  "production",
		},
		{
			name:      "cold canonical console within projection budget",
			root:      `<html><body><span data-instance-home-hash="` + expectedHash + `" data-instance-explicit="1" data-instance-build-kind="production"></span><script src="/_next/static/chunks/app.js"></script></body></html>`,
			rootDelay: 5200 * time.Millisecond,
			wantState: "healthy",
			wantKind:  "production",
		},
		{
			name:      "canonical console with broken assets",
			root:      `<html><body><span data-instance-home-hash="` + expectedHash + `" data-instance-explicit="1" data-instance-build-kind="production"></span><script src="/_next/static/chunks/good.js"></script><script src="/_next/static/chunks/broken.js"></script></body></html>`,
			badAsset:  "/_next/static/chunks/broken.js",
			wantState: "unhealthy",
			wantKind:  "production",
		},
		{
			name:      "foreign listener",
			root:      `<html><body>another service</body></html>`,
			wantState: "occupied_foreign",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/_next/static/") {
					if r.URL.Path == tc.badAsset {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					w.WriteHeader(http.StatusOK)
					return
				}
				if tc.rootDelay > 0 {
					time.Sleep(tc.rootDelay)
				}
				_, _ = fmt.Fprint(w, tc.root)
			}))
			defer server.Close()

			status := inspectConsoleRuntimeAt(strings.TrimPrefix(server.URL, "http://"), homePath, nil)
			if status.State != tc.wantState {
				t.Fatalf("state = %q, want %q (detail=%q)", status.State, tc.wantState, status.Detail)
			}
			if status.BuildKind != tc.wantKind {
				t.Fatalf("build kind = %q, want %q", status.BuildKind, tc.wantKind)
			}
			if tc.wantState == "healthy" && status.AssetsChecked == 0 {
				t.Fatal("healthy status did not verify any referenced Next asset")
			}
		})
	}
}

func TestConsoleStaticAssetsReadsOnlyHTMLAttributes(t *testing.T) {
	html := `<link href="/_next/static/css/app.css"><script src="/_next/static/chunks/app.js"></script>` +
		`<script>self.__next_f.push(["/_next/static/chunks/ghost.js\\\""])</script>`
	got := consoleStaticAssets(html)
	want := []string{"/_next/static/css/app.css", "/_next/static/chunks/app.js"}
	if !sameStringSlice(got, want) {
		t.Fatalf("static assets = %v, want rendered src/href attributes %v", got, want)
	}
}

func TestConsoleSupervisorForwardsTerminationToChildGroup(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "terminated")
	started := filepath.Join(root, "started")
	script := filepath.Join(root, "child.sh")
	body := "#!/bin/sh\ntrap 'printf terminated > \"$CONSOLE_TERM_MARKER\"; exit 0' TERM INT\nprintf started > \"$CONSOLE_STARTED_MARKER\"\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := consoleLaunchPlan{
		ConsoleDir: root,
		Env: map[string]string{
			"CONSOLE_TERM_MARKER":    marker,
			"CONSOLE_STARTED_MARKER": started,
		},
	}
	signals := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- runConsoleSupervisedCommand(plan, []string{script}, signals)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervised child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	signals <- syscall.SIGTERM
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("supervisor returned after forwarded TERM: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not reap the terminated child")
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "terminated" {
		t.Fatalf("child did not receive forwarded termination: body=%q err=%v", body, err)
	}
}

func TestConsoleSupervisorKillsChildGroupAfterTerminationGrace(t *testing.T) {
	root := t.TempDir()
	pidPath := filepath.Join(root, "child.pid")
	script := filepath.Join(root, "ignores-term.sh")
	body := "#!/bin/sh\ntrap '' TERM INT HUP\nprintf '%s' \"$$\" > \"$CONSOLE_PID_PATH\"\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := consoleLaunchPlan{
		ConsoleDir: root,
		Env: map[string]string{
			"CONSOLE_PID_PATH": pidPath,
		},
	}
	signals := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- runConsoleSupervisedCommand(plan, []string{script}, signals)
	}()

	var childPID int
	deadline := time.Now().Add(3 * time.Second)
	for {
		body, err := os.ReadFile(pidPath)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(body)))
			if err != nil {
				t.Fatalf("parse supervised child pid: %v", err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervised child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = syscall.Kill(-childPID, syscall.SIGKILL) }()

	signals <- syscall.SIGTERM
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("supervisor returned after forced shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor waited forever for a child that ignored termination")
	}
	if err := syscall.Kill(-childPID, 0); err != syscall.ESRCH {
		t.Fatalf("supervised child process group still exists after forced shutdown: %v", err)
	}
}

func TestConsoleStatusReportsCanonicalHealth(t *testing.T) {
	stdout, stderr, err := invokeConsoleCommand(t, nil, "status", "--json")
	if err != nil {
		t.Fatalf("compound console status --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	var status struct {
		SchemaVersion string `json:"schema_version"`
		State         string `json:"state"`
		Address       string `json:"address"`
		Port          int    `json:"port"`
	}
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("parse console status: %v\n%s", err, stdout)
	}
	if status.SchemaVersion != "compound.console_status.v0.2" {
		t.Fatalf("status schema = %q", status.SchemaVersion)
	}
	if status.Address != "127.0.0.1:3777" || status.Port != 3777 {
		t.Fatalf("status endpoint = %s (port %d), want canonical 127.0.0.1:3777", status.Address, status.Port)
	}
	if status.State != "healthy" && status.State != "unhealthy" && status.State != "occupied_foreign" && status.State != "stopped" {
		t.Fatalf("status state = %q", status.State)
	}
}

func TestConsoleNextBuildDirectoriesAreLifecycleIsolated(t *testing.T) {
	consoleDir, err := filepath.Abs(filepath.Join("..", "..", "console"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind string
		want string
	}{
		{kind: "development", want: ".next/development"},
		{kind: "production", want: ".next/production"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			cmd := exec.Command("bun", "-e", `import config from "./next.config.ts"; console.log(config.distDir ?? "")`)
			cmd.Dir = consoleDir
			cmd.Env = append(os.Environ(), "COMPOUND_CONSOLE_BUILD_KIND="+tc.kind)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("load next config: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Fatalf("distDir = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConsolePackageScriptsPinLifecycleBuildKinds(t *testing.T) {
	packagePath, err := filepath.Abs(filepath.Join("..", "..", "console", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(body, &pkg); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		script string
		owner  string
		kind   string
	}{
		{script: "dev", owner: "console dev", kind: "development"},
		{script: "build", owner: "console build", kind: "production"},
		{script: "serve", owner: "console serve", kind: "production"},
	} {
		if got := pkg.Scripts[tc.script]; !strings.Contains(got, tc.owner) {
			t.Fatalf("%s script = %q, want CLI owner %q", tc.script, got, tc.owner)
		}
		if got := pkg.Scripts[tc.script]; !strings.Contains(got, "--require-explicit-home") {
			t.Fatalf("%s script = %q, want explicit-home guard", tc.script, got)
		}
		raw := pkg.Scripts["_next:"+tc.script]
		want := "COMPOUND_CONSOLE_BUILD_KIND=" + tc.kind
		if !strings.Contains(raw, want) {
			t.Fatalf("_next:%s script = %q, want explicit %q", tc.script, raw, want)
		}
	}
}

type consoleLifecyclePlanView struct {
	Lifecycle string            `json:"lifecycle"`
	Command   []string          `json:"command"`
	Env       map[string]string `json:"env"`
}

func runConsoleCommand(t *testing.T, homePath string, args ...string) consoleLifecyclePlanView {
	t.Helper()
	stdout, stderr, err := invokeConsoleCommand(t, []string{"COMPOUND_HOME=" + homePath}, args...)
	if err != nil {
		t.Fatalf("compound console %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout, stderr)
	}
	var plan consoleLifecyclePlanView
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatalf("parse console launch plan: %v\n%s", err, stdout)
	}
	return plan
}

func invokeConsoleCommand(t *testing.T, extraEnv []string, args ...string) (string, string, error) {
	t.Helper()
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestConsoleCommandHelper$")
	cmd.Env = append(os.Environ(),
		"COMPOUND_CONSOLE_TEST_HELPER=1",
		"COMPOUND_CONSOLE_TEST_ARGS="+string(encodedArgs),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestConsoleCommandHelper(t *testing.T) {
	if os.Getenv("COMPOUND_CONSOLE_TEST_HELPER") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("COMPOUND_CONSOLE_TEST_ARGS")), &args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cmdConsole(args)
	os.Exit(0)
}

func TestConsoleCompoundBinPrefersStablePathForTemporaryExecutable(t *testing.T) {
	stable := filepath.Join(t.TempDir(), "bin", "compound")
	got := resolveConsoleCompoundBin(filepath.Join(string(filepath.Separator), "tmp", "compound-dashboard"), func(name string) (string, error) {
		if name != "compound" {
			t.Fatalf("lookup name = %q", name)
		}
		return stable, nil
	})
	if got != stable {
		t.Fatalf("compound bin = %q, want %q", got, stable)
	}
}

func TestConsoleCompoundBinKeepsStableExecutable(t *testing.T) {
	current := filepath.Join(string(filepath.Separator), "usr", "local", "bin", "compound")
	got := resolveConsoleCompoundBin(current, func(name string) (string, error) {
		t.Fatalf("stable executable should not need PATH lookup")
		return "", os.ErrNotExist
	})
	if got != current {
		t.Fatalf("compound bin = %q, want %q", got, current)
	}
}

func TestConsoleOperatorDryRunWiresLocalMutationEnv(t *testing.T) {
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"dev":"next dev -p 3777"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	homePath := filepath.Join(root, ".compound")
	t.Setenv("COMPOUND_HOME", homePath)

	out := captureStdout(t, func() {
		cmdConsole([]string{"--operator", "--console-dir", consoleDir, "--dry-run", "--json"})
	})
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("console dry-run missing JSON:\n%s", out)
	}
	var plan consoleLaunchPlan
	if err := json.Unmarshal([]byte(out[start:]), &plan); err != nil {
		t.Fatalf("parse console launch plan: %v\n%s", err, out)
	}
	if plan.Mode != "operator" || !plan.DryRun || !plan.MutationsEnabled || plan.PrivateFallbacks {
		t.Fatalf("operator launch flags = %+v", plan)
	}
	if got, want := plan.Command, []string{"bun", "run", "_next:dev"}; !sameStringSlice(got, want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
	if plan.ConsoleDir != consoleDir {
		t.Fatalf("console_dir = %q, want %q", plan.ConsoleDir, consoleDir)
	}
	if plan.Env["COMPOUND_HOME"] != homePath ||
		plan.Env["COMPOUND_CONSOLE_MUTATIONS"] != "1" ||
		strings.TrimSpace(plan.Env["COMPOUND_BIN"]) == "" ||
		plan.Env["COMPOUND_CONSOLE_PRIVATE_FALLBACKS"] != "" {
		t.Fatalf("env = %+v", plan.Env)
	}
}

func TestConsoleOperatorDryRunSurfacesPinnedHomeIdentity(t *testing.T) {
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"dev":"next dev -p 3777"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	homePath := filepath.Join(root, ".compound")
	t.Setenv("COMPOUND_HOME", homePath)

	// Dry-run WITHOUT --json prints the human launch line: it must name the exact
	// home this instance is pinned to so an operator driving several ports can
	// tell which instance a click lands on. A regression that drops the home from
	// the line reintroduces the "which instance am I on?" blind spot the
	// 2026-07-11 breach exploited.
	out := captureStdout(t, func() {
		cmdConsole([]string{"--operator", "--console-dir", consoleDir, "--dry-run"})
	})
	wantHash := consoleHomeShortHash(homePath)
	for _, want := range []string{"home ", ".compound", "#" + wantHash, homePath} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run launch line missing %q:\n%s", want, out)
		}
	}
	if len(wantHash) != 7 {
		t.Fatalf("home short hash = %q, want 7 hex chars", wantHash)
	}
}

func TestConsoleLaunchPlanPinsResolvedHome(t *testing.T) {
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"dev":"next dev -p 3777"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	scratchA := filepath.Join(root, "scratch-a", ".compound")
	t.Setenv("COMPOUND_HOME", scratchA)

	plan, err := buildConsoleLaunchPlan(consoleDir, false, true)
	if err != nil {
		t.Fatal(err)
	}
	// The child process is pinned to the started home by construction: the launch
	// env carries the resolved home, and consoleLaunchEnv injects exactly that —
	// even overriding a DIFFERENT COMPOUND_HOME that happened to be in the parent
	// environment. This is what stops a clone from inheriting/writing another home.
	if plan.Env["COMPOUND_HOME"] != scratchA {
		t.Fatalf("plan home = %q, want pinned %q", plan.Env["COMPOUND_HOME"], scratchA)
	}
	if !filepath.IsAbs(plan.Env["COMPOUND_HOME"]) {
		t.Fatalf("pinned home must be absolute, got %q", plan.Env["COMPOUND_HOME"])
	}
	t.Setenv("COMPOUND_HOME", filepath.Join(root, "scratch-b", ".compound"))
	env := consoleLaunchEnv(plan.Env)
	var got string
	for _, entry := range env {
		if strings.HasPrefix(entry, "COMPOUND_HOME=") {
			got = strings.TrimPrefix(entry, "COMPOUND_HOME=")
		}
	}
	if got != scratchA {
		t.Fatalf("launch env COMPOUND_HOME = %q, want %q (parent env must not leak a different home)", got, scratchA)
	}
}

func TestConsoleOperatorDryRunCanEnablePrivateFallbacks(t *testing.T) {
	root := t.TempDir()
	consoleDir := filepath.Join(root, "console")
	if err := os.MkdirAll(consoleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleDir, "package.json"), []byte(`{"scripts":{"dev":"next dev -p 3777"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPOUND_HOME", filepath.Join(root, ".compound"))

	plan, err := buildConsoleLaunchPlan(consoleDir, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.PrivateFallbacks || plan.Env["COMPOUND_CONSOLE_PRIVATE_FALLBACKS"] != "1" {
		t.Fatalf("private fallback env missing: %+v", plan)
	}
}
