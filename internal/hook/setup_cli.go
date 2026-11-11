package hook

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"compound/internal/contract"
	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/home"
	"compound/internal/hookplan"
)

// setup_cli.go owns the user-facing setup commands: `compound init`, `compound hook install`,
// `compound hook uninstall` and `compound receipt last`. cmd/compound only dispatches here.
// Every write goes through hookplan's no-clobber install/uninstall transaction and is published
// to the installed-hook authority under the home's hook lock, the same path `hook install`
// has always used.

const (
	HookInstallUsage = "usage: compound hook install [--lane-file lane.yaml | --harness claude-code|claude-zai|claude-minimax|claude-qwen|codex|opencode|oh-my-pi|all] [--roster minimal|full] [--scope global|project] [--config-dir DIR] [--base DIR] [--binary PATH] [--check] [--json]\n" +
		"  --scope project wires the current project (its .claude local settings file) unless --base names another folder."
	HookUninstallUsage = "usage: compound hook uninstall [--harness claude-code|claude-zai|claude-minimax|claude-qwen|codex|opencode|oh-my-pi|all] [--scope global|project] [--config-dir DIR] [--base DIR] [--check] [--json]\n" +
		"  removes only Compound's hook entries (or, for OpenCode and oh-my-pi, Compound's own hook file); every other setting and hook stays as it was."
	InitUsage = "usage: compound init [--harness claude-code|codex|opencode|oh-my-pi] [--check] [--base DIR] [--binary PATH]\n" +
		"  sets Compound up for this user: creates the Compound home if it is missing, wires the hooks of every\n" +
		"  supported harness found here (Claude Code, Codex, OpenCode, oh-my-pi), and checks that `compound` is on PATH.\n" +
		"  --check only reports; it changes nothing and exits 1 when something still needs `compound init`."
	ReceiptUsage = "usage: compound receipt last [--json] [--home PATH]\n" +
		"  shows the newest receipt: what the last session (or closed work item) did, and where its transcript is."
)

type setupOptions struct {
	scope, base, binary string
	check, json         bool
}

func setupFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *setupOptions) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {}
	o := &setupOptions{}
	flags.StringVar(&o.scope, "scope", "global", "")
	flags.StringVar(&o.base, "base", "", "")
	flags.StringVar(&o.binary, "binary", "", "")
	flags.BoolVar(&o.check, "check", false, "")
	flags.BoolVar(&o.json, "json", false, "")
	return flags, o
}

// parseSetupArgs stops the command on help (exit 0, usage on stdout) or on any unknown flag or
// stray argument (exit 2), so a typo never silently selects a default posture.
func parseSetupArgs(flags *flag.FlagSet, args []string, stdout, stderr io.Writer, usage string) (int, bool) {
	err := flags.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprintln(stdout, usage)
		return 0, true
	case err != nil:
		fmt.Fprintln(stderr, usage)
		return 2, true
	case flags.NArg() > 0:
		fmt.Fprintf(stderr, "error: unexpected argument %q\n%s\n", flags.Arg(0), usage)
		return 2, true
	}
	return 0, false
}

func (o *setupOptions) resolve() error {
	if o.scope != "global" && o.scope != "project" {
		return fmt.Errorf("--scope must be global or project")
	}
	var err error
	if strings.TrimSpace(o.base) == "" {
		if o.base, err = hookplan.DefaultSetupBase(o.scope); err != nil {
			return err
		}
	}
	if strings.TrimSpace(o.binary) == "" {
		o.binary, err = os.Executable()
	}
	return err
}

// withHookAuthority runs one native-config change. A check writes nothing and needs no home;
// a real change runs under the home's hook lock and publishes what it wrote.
func withHookAuthority(o *setupOptions, change func(publish func(hookplan.HookInstallReport) error) (hookplan.HookInstallReport, error)) (hookplan.HookInstallReport, error) {
	if o.check {
		return change(nil)
	}
	homePath, err := home.Resolve("")
	if err != nil {
		return hookplan.HookInstallReport{}, err
	}
	publish := func(report hookplan.HookInstallReport) error {
		_, err := contract.ImportHookInstallReport(homePath, o.base, report, time.Now())
		return err
	}
	var report hookplan.HookInstallReport
	err = home.WithAutonomyAdmissionLock(homePath, func() error {
		var changeErr error
		report, changeErr = change(publish)
		return changeErr
	})
	return report, err
}

func installHooks(o *setupOptions, req hookplan.InstallRequest) (hookplan.HookInstallReport, error) {
	return withHookAuthority(o, func(publish func(hookplan.HookInstallReport) error) (hookplan.HookInstallReport, error) {
		if len(req.Harnesses) == 1 && req.Harnesses[0] == "oh-my-pi" {
			return hookplan.InstallOhMyPiShimWithConfigDir(o.base, req.ConfigDir, o.check)
		}
		if len(req.Harnesses) == 1 && req.Harnesses[0] == "opencode" {
			return hookplan.InstallOpenCodePlugin(o.base, o.check)
		}
		return hookplan.InstallHooksWithRoster(o.base, o.binary, o.scope, req.Harnesses, req.ConfigDir, req.AllowedEvents, o.check, publish)
	})
}

// RunInstallCommand is `compound hook install`: it reconciles each compiled default posture
// without clobbering unrelated settings. Interactive Codex and Claude Code observe actions
// without publishing native permission decisions; isolated Claude-family profiles keep explicit
// mutation enforcement. `--lane-file` derives the harness and hook roster from a lane manifest.
func RunInstallCommand(args []string, stdout, stderr io.Writer) int {
	flags, o := setupFlagSet("hook install", stderr)
	laneFile := flags.String("lane-file", "", "")
	harnessFlag := flags.String("harness", "", "")
	roster := flags.String("roster", "", "")
	configDir := flags.String("config-dir", "", "")
	if code, stop := parseSetupArgs(flags, args, stdout, stderr, HookInstallUsage); stop {
		return code
	}
	if err := o.resolve(); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	req, err := hookplan.ResolveInstallRequest(strings.TrimSpace(*laneFile), strings.TrimSpace(*harnessFlag), strings.TrimSpace(*roster), strings.TrimSpace(*configDir))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	report, err := installHooks(o, req)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	head := map[bool]string{false: "installed", true: "check"}[o.check]
	printHookReport(stdout, o.json, "hook "+head, report)
	if !o.check && !o.json {
		fmt.Fprintln(stdout, "\ninteractive Codex/Claude PreToolUse is observation-only; isolated executor profiles use explicit enforcement, or --enforce-governed (blocks only inside governed runs) where the lane was watch-only.")
	}
	return 0
}

// RunUninstallCommand is `compound hook uninstall`, the exact reverse of install: it removes
// Compound's own hook entries and records them as removed in the installed-hook authority.
func RunUninstallCommand(args []string, stdout, stderr io.Writer) int {
	flags, o := setupFlagSet("hook uninstall", stderr)
	harnessFlag := flags.String("harness", "", "")
	configDir := flags.String("config-dir", "", "")
	if code, stop := parseSetupArgs(flags, args, stdout, stderr, HookUninstallUsage); stop {
		return code
	}
	if err := o.resolve(); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	var harnesses []string
	switch h := strings.TrimSpace(*harnessFlag); h {
	case "", "all":
	case "claude-code", "claude-zai", "claude-minimax", "claude-qwen", "codex", "opencode", "oh-my-pi":
		harnesses = []string{h}
	default:
		fmt.Fprintf(stderr, "error: --harness must be claude-code, claude-zai, claude-minimax, claude-qwen, codex, opencode, oh-my-pi, or all\n%s\n", HookUninstallUsage)
		return 2
	}
	report, err := withHookAuthority(o, func(publish func(hookplan.HookInstallReport) error) (hookplan.HookInstallReport, error) {
		return hookplan.UninstallHooks(o.base, o.binary, o.scope, harnesses, strings.TrimSpace(*configDir), o.check, publish)
	})
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	printHookReport(stdout, o.json, "hook uninstall", report)
	return 0
}

func printHookReport(stdout io.Writer, asJSON bool, head string, report hookplan.HookInstallReport) {
	if asJSON {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Fprintln(stdout, string(data))
		return
	}
	fmt.Fprintf(stdout, "%s (scope=%s):\n", head, report.Scope)
	for _, e := range report.Entries {
		if line := fmt.Sprintf("- %s: %s -> %s", e.Harness, e.Action, e.ConfigPath); head == "hook uninstall" {
			fmt.Fprintln(stdout, line)
		} else {
			fmt.Fprintf(stdout, "%s [%s]\n", line, e.Matcher)
		}
	}
}

// RunInitCommand is `compound init`, the one command a new user runs after `make install`.
func RunInitCommand(args []string, stdout, stderr io.Writer) int {
	flags, o := setupFlagSet("init", stderr)
	only := flags.String("harness", "", "")
	if code, stop := parseSetupArgs(flags, args, stdout, stderr, InitUsage); stop {
		return code
	}
	if *only != "" && !slices.Contains(hookplan.SetupHarnessNames(), *only) {
		fmt.Fprintf(stderr, "error: --harness must be one of %s\n%s\n", strings.Join(hookplan.SetupHarnessNames(), ", "), InitUsage)
		return 2
	}
	if err := o.resolve(); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	needsInit := false

	homePath, err := home.Resolve("")
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	homeState := "ready"
	if _, err := home.LoadManifest(homePath); errors.Is(err, fs.ErrNotExist) && o.check {
		homeState, needsInit = "missing; `compound init` creates it", true
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := home.Init(homePath, home.HomeKindGlobal, time.Now()); err != nil {
			fmt.Fprintf(stderr, "error: create the Compound home at %s: %v\n", homePath, err)
			return 1
		}
		homeState = "created"
	} else if err != nil {
		fmt.Fprintf(stderr, "error: the Compound home at %s is not usable: %v\n", homePath, err)
		return 1
	}
	if !needsInit { // the home exists now: refuse one this binary cannot safely write
		if err := home.RequireMutationCompatible(homePath); err != nil {
			fmt.Fprintf(stderr, "error: the Compound home at %s is not usable: %v\n", homePath, err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "Compound home: %s (%s)\n", homePath, homeState)

	var found []hookplan.SetupHarness
	var wire []string
	for _, h := range hookplan.DetectSetupHarnesses(o.base, exec.LookPath) {
		if *only == "" || h.Name == *only {
			found = append(found, h)
			if h.Present && h.Wireable {
				wire = append(wire, h.Name)
			}
		}
	}
	entries := map[string]hookplan.HookInstallEntry{}
	if len(wire) > 0 {
		report, err := withHookAuthority(o, func(publish func(hookplan.HookInstallReport) error) (hookplan.HookInstallReport, error) {
			return hookplan.InstallSetupHarnesses(o.base, o.binary, wire, o.check, publish)
		})
		if err != nil {
			fmt.Fprintln(stderr, "error: wire hooks:", err)
			return 1
		}
		for _, e := range report.Entries {
			entries[e.Harness] = e
		}
	}
	for _, h := range found {
		switch e := entries[h.Name]; {
		case !h.Present:
			fmt.Fprintf(stdout, "%s: not found\n", h.Label)
		case !h.Wireable:
			fmt.Fprintf(stdout, "%s: found; Compound observes it, init does not wire it (see docs/harness-wiring.md)\n", h.Label)
		default:
			state := map[string]string{"installed": "wired", "updated": "wired (updated)", "ok": "already wired", "absent": "not wired", "drift": "needs an update"}[e.Action]
			if o.check && e.Action == "ok" {
				state = "wired"
			}
			needsInit = needsInit || (o.check && e.Action != "ok")
			fmt.Fprintf(stdout, "%s: %s (%s)\n", h.Label, state, e.ConfigPath)
		}
	}
	if advice := pathAdvice(filepath.Dir(o.binary), os.Getenv("PATH"), os.Getenv("SHELL"), os.Getenv("HOME"), runtime.GOOS); advice != "" {
		fmt.Fprintln(stdout, advice)
	}

	switch {
	case len(wire) == 0:
		fmt.Fprintln(stdout, "\nNo supported harness found. Install Claude Code, Codex, OpenCode or oh-my-pi, open it once, then run `compound init` again.")
	case o.check && needsInit:
		fmt.Fprintln(stdout, "\nRun `compound init` to finish.")
	case !o.check:
		next := wire[0] // the harness this run wired, else the first one already wired
		for _, name := range wire {
			if a := entries[name].Action; a == "installed" || a == "updated" {
				next = name
				break
			}
		}
		fmt.Fprintf(stdout, "\nNext: start %s in any project. Compound gives it context when the session starts\n", hookplan.SetupHarnessLabel(next))
		if bindsEvent(entries[next], "SessionEnd") {
			fmt.Fprintln(stdout, "and writes a receipt when it ends.")
		} else {
			fmt.Fprintln(stdout, "and records its tool calls; it has no session-end event, so it writes no session receipt.")
		}
		fmt.Fprintf(stdout, "Check: compound init --check   Last session: compound receipt last   Undo: compound hook uninstall --harness %s\n", next)
	}
	if needsInit {
		return 1
	}
	return 0
}

// bindsEvent reports whether an install entry wired the named native event.
func bindsEvent(entry hookplan.HookInstallEntry, event string) bool {
	for _, b := range entry.Bindings {
		if b.Event == event {
			return true
		}
	}
	return false
}

// pathAdvice tells the user how to put dir on PATH for their shell. It never edits a profile.
// It returns "" when dir is on PATH and a profile already names it.
func pathAdvice(dir, pathEnv, shell, userHome, goos string) string {
	dir = filepath.Clean(dir)
	onPath := false
	for _, entry := range filepath.SplitList(pathEnv) {
		onPath = onPath || (entry != "" && filepath.Clean(entry) == dir)
	}
	shown := dir
	if rel, err := filepath.Rel(userHome, dir); userHome != "" && err == nil && rel == "." {
		shown = "$HOME"
	} else if userHome != "" && err == nil && !strings.HasPrefix(rel, "..") {
		shown = "$HOME/" + filepath.ToSlash(rel)
	}
	line, profile, candidates := `export PATH="`+shown+`:$PATH"`, "~/.profile", []string{".profile"}
	switch filepath.Base(shell) {
	case "zsh":
		profile, candidates = "~/.zshrc", []string{".zshrc", ".zprofile", ".zshenv"}
	case "bash":
		profile, candidates = "~/.bashrc", []string{".bashrc", ".bash_profile", ".profile"}
		if goos == "darwin" {
			profile = "~/.bash_profile"
		}
	case "fish":
		line, profile, candidates = "fish_add_path "+shown, "~/.config/fish/config.fish", []string{".config/fish/config.fish"}
	}
	named := false
	for _, name := range candidates {
		body, _ := os.ReadFile(filepath.Join(userHome, name))
		text := string(body)
		named = named || strings.Contains(text, dir) || strings.Contains(text, shown) ||
			strings.Contains(text, strings.Replace(shown, "$HOME/", "~/", 1)) || strings.Contains(text, strings.Replace(shown, "$HOME/", "${HOME}/", 1))
	}
	switch {
	case onPath && named:
		return ""
	case onPath:
		return fmt.Sprintf("PATH: ok in this terminal. If a new terminal cannot find `compound`, add this line to %s:\n  %s", profile, line)
	}
	return fmt.Sprintf("PATH: %s is not on your PATH. Add this line to %s, then open a new terminal:\n  %s", dir, profile, line)
}

// RunReceiptCommand is `compound receipt last`: the newest receipt in the home's event store.
func RunReceiptCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "last" {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			fmt.Fprintln(stdout, ReceiptUsage)
			return 0
		}
		fmt.Fprintln(stderr, ReceiptUsage)
		return 2
	}
	flags := flag.NewFlagSet("receipt last", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {}
	asJSON := flags.Bool("json", false, "")
	homeFlag := flags.String("home", "", "")
	if code, stop := parseSetupArgs(flags, args[1:], stdout, stderr, ReceiptUsage); stop {
		return code
	}
	homePath, err := home.Resolve(*homeFlag)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	newest, raw, err := newestReceipt(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if newest == nil {
		fmt.Fprintln(stdout, "no receipt yet: Compound writes one when a harness session ends or a work item closes.")
		return 1
	}
	if *asJSON {
		fmt.Fprintln(stdout, string(raw))
		return 0
	}
	r := newest
	fmt.Fprintf(stdout, "receipt %s  %s\n", r.ID, r.CreatedAt)
	if r.IDs.SessionID != "" {
		fmt.Fprintf(stdout, "session: %s\n", r.IDs.SessionID)
	}
	if r.IDs.WorkID != "" {
		fmt.Fprintf(stdout, "work: %s\n", r.IDs.WorkID)
	}
	if r.Scope.Surface != "" {
		fmt.Fprintf(stdout, "where: %s\n", r.Scope.Surface)
	}
	fmt.Fprintf(stdout, "outcome: %s (%s)\n", r.Outcome, r.MoveType)
	if n := len(r.StateDelta.FilesChanged); n > 0 {
		shown := r.StateDelta.FilesChanged[:min(n, 5)]
		fmt.Fprintf(stdout, "files changed: %d (%s)\n", n, strings.Join(shown, ", "))
	} else {
		fmt.Fprintln(stdout, "files changed: none")
	}
	for _, ref := range r.ProofRefs {
		if path, ok := strings.CutPrefix(ref, "transcript:"); ok {
			fmt.Fprintf(stdout, "transcript: %s\n", path)
		}
	}
	fmt.Fprintf(stdout, "full record: compound event show %s\n", r.ID)
	return 0
}

// newestReceipt reads the receipt family's newest segments (the newest receipt is always in the
// newest one) and returns the receipt with the latest created_at.
func newestReceipt(homePath string) (*envelope.Receipt, []byte, error) {
	store, err := eventstore.Open(homePath)
	if err != nil {
		return nil, nil, err
	}
	entries, err := store.Scan(eventstore.ScanOptions{Prefixes: []string{"rec_"}, RegularOnly: true, RecentSegments: 2})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil // a home with no event store yet has no receipt
	} else if err != nil {
		return nil, nil, err
	}
	var newest *envelope.Receipt
	var raw []byte
	for _, entry := range entries {
		record, err := entry.Read()
		if err != nil {
			continue
		}
		var r envelope.Receipt
		if json.Unmarshal(record.Bytes, &r) != nil {
			continue
		}
		if newest == nil || r.CreatedAt > newest.CreatedAt || (r.CreatedAt == newest.CreatedAt && r.ID > newest.ID) {
			newest, raw = &r, record.Bytes
		}
	}
	return newest, raw, nil
}
