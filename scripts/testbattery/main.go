// Command testbattery runs the complete Go inventory with release and home isolated.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

type commandRunner func(context.Context, []string, io.Writer, io.Writer) error

func runBattery(ctx context.Context, run commandRunner, stdout, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var inventory bytes.Buffer
	err := run(ctx, []string{"list", "./..."}, &inventory, stderr)
	if err != nil || ctx.Err() != nil {
		return fmt.Errorf("inventory: %w", errors.Join(err, ctx.Err()))
	}
	parallel, isolated, err := partitionPackages(inventory.String())
	if err != nil {
		return err
	}
	phases := [][]string{parallel}
	for _, pkg := range isolated {
		phases = append(phases, []string{pkg})
	}
	var failures []error
	for i, packages := range phases {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		fmt.Fprintf(stdout, "testbattery: phase %d/%d packages=%d\n", i+1, len(phases), len(packages))
		args := append([]string{"test", "-count=1", "-timeout=40m"}, packages...)
		err := run(ctx, args, stdout, stderr)
		if err != nil {
			failures = append(failures, fmt.Errorf("phase %d: %w", i+1, err))
		}
		if cancelErr := ctx.Err(); cancelErr != nil {
			return errors.Join(append(failures, cancelErr)...)
		}
		if err != nil {
			var exitErr *exec.ExitError
			// An ordinary test failure must not suppress the other phase. A
			// signal, start failure or wait failure must stop later launches.
			if !errors.As(err, &exitErr) || exitErr.ExitCode() < 0 {
				return errors.Join(failures...)
			}
		}
	}
	return errors.Join(failures...)
}

// This is the current module's inventory, not caller-supplied patterns or flags.
var packagePath = regexp.MustCompile(`^compound(/[A-Za-z0-9_-][A-Za-z0-9_.-]*)*$`)

func partitionPackages(inventory string) ([]string, []string, error) {
	const homePackage = "compound/internal/home"
	const releasePackage = "compound/internal/release"
	var parallel, isolated []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(inventory, "\n"), "\n") {
		pkg := strings.TrimSuffix(line, "\r")
		if !packagePath.MatchString(pkg) || seen[pkg] {
			return nil, nil, fmt.Errorf("invalid or duplicate package inventory entry %q", pkg)
		}
		seen[pkg] = true
		if pkg != homePackage && pkg != releasePackage {
			parallel = append(parallel, pkg)
		}
	}
	if !seen[homePackage] || !seen[releasePackage] || len(parallel) == 0 {
		return nil, nil, errors.New("inventory requires release, home, and at least one general package")
	}
	// Release exercises real machine-wide sync barriers. Concurrent package writes
	// can exhaust its checkpoint deadlines before the assertion is reached.
	// Keep those durability checks intact and run each shared-resource owner alone.
	for _, pkg := range []string{releasePackage, homePackage} {
		isolated = append(isolated, pkg)
	}
	sort.Strings(parallel)
	return parallel, isolated, nil
}

func runGo(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Inherit the canonical proof gate's process group and execution barrier.
	// The proof runner owns cancellation and joining the whole group. Do not
	// add CommandContext, Setpgid, a child killer or a separate timeout here.
	cmd := exec.Command("go", args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

func main() {
	mode := "test"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("testbattery", flag.ContinueOnError)
	sourceHead := flags.String("source-head", "", "require this sealed commit and clean source before and after the gate")
	if flags.Parse(args) != nil || flags.NArg() != 0 ||
		(mode != "test" && mode != "vet" && mode != "build") ||
		(*sourceHead != "" && !sourceCommit.MatchString(*sourceHead)) {
		fmt.Fprintln(os.Stderr, "usage: testbattery [test|vet|build] [--source-head SHA] (all source packages; no selectors)")
		os.Exit(2)
	}
	// Only prevent subsequent launches after the owner's signal; children
	// remain in its group. Standalone interruption likewise uses group signals.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := runBoundSourceGate(ctx, mode, *sourceHead, os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "testbattery:", err)
		os.Exit(1)
	}
}
