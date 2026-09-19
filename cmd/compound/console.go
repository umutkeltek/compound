package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"compound/internal/console"
	"compound/internal/home"
)

const consoleStatusSchemaVersion = "compound.console_status.v0.2"

// internal/console is the single authority for the launch-plan schema, env
// names, lifecycle names, and capability gate. These aliases keep the cmd-
// level CLI tests, flags, and conditionals readable without duplicating any
// approval logic in this file.
const (
	consoleLifecycleDev   = console.LifecycleDev
	consoleLifecycleBuild = console.LifecycleBuild
	consoleLifecycleServe = console.LifecycleServe
)

const (
	consoleCapabilityEnv       = console.CapabilityEnv
	consoleLegacySessionEnv    = console.LegacySessionEnv
	consoleMutationsEnv        = console.MutationsEnv
	consolePrivateFallbacksEnv = console.PrivateFallbacksEnv
)

const consoleListenAddress = console.ListenAddress

const consoleShutdownGrace = 2 * time.Second

// consoleLaunchPlan is the cmd-side alias of internal/console.LaunchPlan so
// existing call sites keep their short names and tests stay unchanged.
type consoleLaunchPlan = console.LaunchPlan

type consoleRuntimeStatus struct {
	SchemaVersion string `json:"schema_version"`
	CheckedAt     string `json:"checked_at"`
	State         string `json:"state"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	OwnerProcess  string `json:"owner_process,omitempty"`
	OwnerPID      string `json:"owner_pid,omitempty"`
	ExpectedHome  string `json:"expected_home_hash,omitempty"`
	ObservedHome  string `json:"observed_home_hash,omitempty"`
	BuildKind     string `json:"build_kind,omitempty"`
	HTTPStatus    int    `json:"http_status,omitempty"`
	AssetsChecked int    `json:"assets_checked,omitempty"`
	FailedAsset   string `json:"failed_asset,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

func cmdConsole(args []string) {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprintln(os.Stderr, consoleUsage())
		os.Exit(2)
	}
	if args[0] == "status" {
		cmdConsoleStatus(args[1:])
		return
	}
	lifecycle := consoleLifecycleDev
	if args[0] == consoleLifecycleDev || args[0] == consoleLifecycleBuild || args[0] == consoleLifecycleServe {
		lifecycle = args[0]
		args = args[1:]
	}
	operator := false
	mutations := false
	privateFallbacks := false
	dryRun := false
	jsonOut := false
	requireExplicitHome := false
	consoleDirFlag := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--operator":
			operator = true
		case "--mutations":
			mutations = true
		case "--private-fallbacks":
			privateFallbacks = true
		case "--dry-run":
			dryRun = true
		case "--json":
			jsonOut = true
		case "--require-explicit-home":
			requireExplicitHome = true
		case "--console-dir":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, consoleUsage())
				os.Exit(2)
			}
			i++
			consoleDirFlag = args[i]
		default:
			fmt.Fprintf(os.Stderr, "unknown console option %q\n", args[i])
			os.Exit(2)
		}
	}
	if !operator {
		fmt.Fprintln(os.Stderr, consoleUsage())
		os.Exit(2)
	}
	if requireExplicitHome && strings.TrimSpace(os.Getenv("COMPOUND_HOME")) == "" {
		fmt.Fprintln(os.Stderr, "error: this lifecycle entrypoint requires an explicit COMPOUND_HOME; use compound console dev --operator when you intend the canonical default")
		os.Exit(1)
	}
	plan, err := buildConsoleLifecycleLaunchPlan(lifecycle, consoleDirFlag, mutations, privateFallbacks, dryRun)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if jsonOut {
		printJSON(plan)
		if dryRun {
			return
		}
	}
	pinLabel := consoleInstancePinLabel(plan.Env["COMPOUND_HOME"])
	if dryRun {
		fmt.Printf("console operator %s dry-run: home %s; cd %s && %s\n", plan.Lifecycle, pinLabel, plan.ConsoleDir, consoleCommandSummary(plan))
		return
	}
	if _, err := exec.LookPath(plan.Command[0]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s not found; install Bun or run from a shell with Bun on PATH\n", plan.Command[0])
		os.Exit(1)
	}
	// Pin identity: print the exact home this instance is bound to so an operator
	// running several ports can always tell which instance they launched. Mirrors
	// the console's always-visible InstanceBadge (same home + short hash).
	fmt.Printf("console operator: local console on port 3777 — home %s\n", pinLabel)
	lock, err := acquireConsoleLifecycleLock(plan.ConsoleDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	defer releaseConsoleLifecycleLock(lock)

	if plan.Lifecycle == consoleLifecycleServe {
		if err := validateConsoleProductionBuild(plan.ConsoleDir); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	reservation, err := reserveConsolePort(consoleListenAddress)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if plan.Lifecycle == consoleLifecycleBuild {
		defer reservation.Close()
		if err := executeConsoleCommand(plan, plan.Command); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if err := reservation.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "error: release console port 3777 reservation:", err)
		os.Exit(1)
	}
	if err := executeConsoleSupervisedCommand(plan, plan.Command); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdConsoleStatus(args []string) {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintln(os.Stderr, consoleUsage())
			os.Exit(2)
		}
	}
	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: resolve COMPOUND_HOME:", err)
		os.Exit(1)
	}
	status := inspectConsoleRuntimeAt(consoleListenAddress, homePath, nil)
	if jsonOut {
		printJSON(status)
		return
	}
	if status.State == "healthy" && status.OwnerProcess != "" {
		fmt.Printf("console healthy at %s — %s build; owner %s pid %s\n", status.Address, status.BuildKind, status.OwnerProcess, status.OwnerPID)
		return
	}
	if status.Detail != "" {
		fmt.Printf("console %s at %s — %s\n", status.State, status.Address, status.Detail)
		return
	}
	fmt.Printf("console %s at %s\n", status.State, status.Address)
}

func inspectConsoleRuntimeAt(address, expectedHome string, client *http.Client) consoleRuntimeStatus {
	_, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	status := consoleRuntimeStatus{
		SchemaVersion: consoleStatusSchemaVersion,
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
		State:         "unhealthy",
		Address:       address,
		Port:          port,
		ExpectedHome:  consoleHomeShortHash(expectedHome),
	}
	listener, err := net.Listen("tcp", address)
	if err == nil {
		status.State = "stopped"
		_ = listener.Close()
		return status
	}
	status.OwnerPID, status.OwnerProcess = consolePortOwner(port)
	if client == nil {
		// RootLayout waits on the real attention/policy projections. A cold
		// canonical process can legitimately take tens of seconds on its first
		// render, so health must cover the longest current projection door.
		client = &http.Client{Timeout: 60 * time.Second}
	}
	rootURL := "http://" + address + "/"
	response, err := client.Get(rootURL)
	if err != nil {
		status.Detail = "listener accepted TCP but the console HTTP probe failed"
		return status
	}
	defer response.Body.Close()
	status.HTTPStatus = response.StatusCode
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		status.Detail = "console root response could not be read"
		return status
	}
	html := string(body)
	status.ObservedHome = consoleHTMLAttribute(html, "data-instance-home-hash")
	status.BuildKind = consoleHTMLAttribute(html, "data-instance-build-kind")
	explicit := consoleHTMLAttribute(html, "data-instance-explicit")
	if status.ObservedHome == "" || status.ObservedHome != status.ExpectedHome || explicit != "1" {
		status.State = "occupied_foreign"
		status.Detail = "port owner is not the explicitly pinned canonical Compound console"
		return status
	}
	if response.StatusCode != http.StatusOK {
		status.Detail = fmt.Sprintf("console root returned HTTP %d", response.StatusCode)
		return status
	}
	if status.BuildKind != "development" && status.BuildKind != "production" {
		status.Detail = "console identity is missing its lifecycle build kind"
		return status
	}
	assetPaths := consoleStaticAssets(html)
	if len(assetPaths) == 0 {
		status.Detail = "console root referenced no Next static asset"
		return status
	}
	for _, assetPath := range assetPaths {
		assetResponse, err := client.Get("http://" + address + assetPath)
		if err != nil {
			status.FailedAsset = assetPath
			status.Detail = "console static asset probe failed"
			return status
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(assetResponse.Body, 4<<20))
		_ = assetResponse.Body.Close()
		if assetResponse.StatusCode < 200 || assetResponse.StatusCode >= 300 {
			status.FailedAsset = assetPath
			status.Detail = fmt.Sprintf("console static asset returned HTTP %d", assetResponse.StatusCode)
			return status
		}
		status.AssetsChecked++
	}
	status.State = "healthy"
	status.Detail = ""
	return status
}

func consoleHTMLAttribute(html, name string) string {
	marker := name + `="`
	start := strings.Index(html, marker)
	if start < 0 {
		return ""
	}
	value := html[start+len(marker):]
	end := strings.IndexByte(value, '"')
	if end < 0 {
		return ""
	}
	return value[:end]
}

func consoleStaticAssets(html string) []string {
	const limit = 256
	seen := make(map[string]struct{})
	assets := make([]string, 0, 32)
	remaining := html
	for len(assets) < limit {
		src := strings.Index(remaining, `src="`)
		href := strings.Index(remaining, `href="`)
		start := src
		marker := `src="`
		if start < 0 || (href >= 0 && href < start) {
			start = href
			marker = `href="`
		}
		if start < 0 {
			break
		}
		rest := remaining[start+len(marker):]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			break
		}
		value := rest[:end]
		if strings.HasPrefix(value, "/_next/static/") {
			if _, ok := seen[value]; !ok {
				seen[value] = struct{}{}
				assets = append(assets, value)
			}
		}
		remaining = rest[end+1:]
	}
	return assets
}

func consolePortOwner(port int) (string, string) {
	out, err := exec.Command("lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-Fpc").Output()
	if err != nil {
		return "", ""
	}
	var pid, process string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			if pid == "" {
				pid = line[1:]
			}
		case 'c':
			if process == "" {
				process = line[1:]
			}
		}
	}
	return pid, process
}

func reserveConsolePort(address string) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("console port 3777 is already owned; run compound console status --json: %w", err)
	}
	return listener, nil
}

func consoleUsage() string {
	return "usage: compound console [dev|build|serve] --operator [--require-explicit-home] [--console-dir DIR] [--mutations] [--private-fallbacks] [--dry-run] [--json] | compound console status [--json]"
}

func consoleCommandSummary(plan consoleLaunchPlan) string {
	return strings.Join(plan.Command, " ")
}

func executeConsoleCommand(plan consoleLaunchPlan, command []string) error {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = plan.ConsoleDir
	cmd.Env = consoleLaunchEnv(plan)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func executeConsoleSupervisedCommand(plan consoleLaunchPlan, command []string) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	return runConsoleSupervisedCommand(plan, command, signals)
}

func runConsoleSupervisedCommand(plan consoleLaunchPlan, command []string, signals <-chan os.Signal) error {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = plan.ConsoleDir
	cmd.Env = consoleLaunchEnv(plan)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	pgid := cmd.Process.Pid
	leaderDone := false
	waitForGroupExit := func(timeout time.Duration) (bool, error) {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		poll := time.NewTicker(10 * time.Millisecond)
		defer poll.Stop()
		waitCh := waited
		if leaderDone {
			waitCh = nil
		}
		for {
			alive, err := console.ProcessGroupAlive(pgid)
			if err != nil {
				return false, err
			}
			if leaderDone && !alive {
				return true, nil
			}
			select {
			case <-waitCh:
				leaderDone = true
				waitCh = nil
			case <-poll.C:
			case <-timer.C:
				return false, nil
			}
		}
	}
	forceGroupExit := func() error {
		if err := signalConsoleProcessGroup(pgid, syscall.SIGKILL); err != nil {
			_ = cmd.Process.Kill()
			return fmt.Errorf("force console process group shutdown: %w", err)
		}
		stopped, err := waitForGroupExit(consoleShutdownGrace)
		if err != nil {
			return fmt.Errorf("verify forced console process group shutdown: %w", err)
		}
		if !stopped {
			_ = cmd.Process.Kill()
			return fmt.Errorf("console process group %d still exists after forced shutdown", pgid)
		}
		return nil
	}
	select {
	case err := <-waited:
		leaderDone = true
		alive, groupErr := console.ProcessGroupAlive(pgid)
		if groupErr != nil {
			return fmt.Errorf("inspect console process group after leader exit: %w", groupErr)
		}
		if alive {
			if cleanupErr := forceGroupExit(); cleanupErr != nil {
				return cleanupErr
			}
		}
		return err
	case received := <-signals:
		sig, ok := received.(syscall.Signal)
		if !ok {
			sig = syscall.SIGTERM
		}
		if err := signalConsoleProcessGroup(pgid, sig); err != nil {
			_ = cmd.Process.Kill()
			_, _ = waitForGroupExit(consoleShutdownGrace)
			return fmt.Errorf("forward console termination: %w", err)
		}
		stopped, err := waitForGroupExit(consoleShutdownGrace)
		if err != nil {
			_ = forceGroupExit()
			return fmt.Errorf("verify console process group shutdown: %w", err)
		}
		if stopped {
			return nil
		}
		if err := forceGroupExit(); err != nil {
			return fmt.Errorf("force console shutdown after %s: %w", consoleShutdownGrace, err)
		}
		return nil
	}
}

func signalConsoleProcessGroup(pgid int, sig syscall.Signal) error {
	err := syscall.Kill(-pgid, sig)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

func acquireConsoleLifecycleLock(consoleDir string) (*os.File, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("resolve console lifecycle lock directory: %w", err)
	}
	lockDir := filepath.Join(cacheDir, "compound", "locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return nil, fmt.Errorf("create console lifecycle lock directory: %w", err)
	}
	endpointHash := sha256.Sum256([]byte(consoleListenAddress))
	lockName := "console-" + hex.EncodeToString(endpointHash[:8]) + ".lock"
	lock, err := os.OpenFile(filepath.Join(lockDir, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open console lifecycle lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("console lifecycle is already active for %s (requested from %s); run compound console status --json: %w", consoleListenAddress, consoleDir, err)
	}
	return lock, nil
}

func releaseConsoleLifecycleLock(lock *os.File) {
	if lock == nil {
		return
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	_ = lock.Close()
}

func validateConsoleProductionBuild(consoleDir string) error {
	return console.ValidateProductionBuild(consoleDir)
}

func buildConsoleLaunchPlan(consoleDirFlag string, privateFallbacks, dryRun bool) (consoleLaunchPlan, error) {
	return console.BuildLaunchPlan(consoleDirFlag, privateFallbacks, dryRun)
}

func buildConsoleLifecycleLaunchPlan(lifecycle, consoleDirFlag string, mutations, privateFallbacks, dryRun bool) (consoleLaunchPlan, error) {
	return console.BuildLifecycleLaunchPlan(lifecycle, consoleDirFlag, mutations, privateFallbacks, dryRun)
}

func validateConsoleMutationLaunchEnvironment(mutations bool) error {
	return console.ValidateMutationLaunchEnvironment(mutations)
}

func resolveConsoleCompoundBin(currentExe string, lookPath func(string) (string, error)) string {
	return console.ResolveCompoundBin(currentExe, lookPath)
}

func consoleExecutableIsTemporary(path string) bool {
	return console.ExecutableIsTemporary(path)
}

func resolveConsoleDir(explicit string) (string, error) {
	return console.ResolveDir(explicit)
}

func validateConsoleDir(dir string) (string, error) {
	return console.ValidateDir(dir)
}

// consoleHomeShortHash fingerprints an absolute home path. It mirrors the
// console's instance-pin.ts (sha256 of the absolute home, first 7 hex chars) so
// the terminal launch line and the browser InstanceBadge show the SAME hash for
// the same instance — an operator can match them by eye across ports.
func consoleHomeShortHash(homePath string) string {
	return console.HomeShortHash(homePath)
}

// consoleInstancePinLabel renders the human pin identity: "<leaf> #<hash> (<abs>)".
func consoleInstancePinLabel(homePath string) string {
	return console.InstancePinLabel(homePath)
}

func consoleLaunchEnv(plan consoleLaunchPlan) []string {
	return console.LaunchEnv(plan)
}
