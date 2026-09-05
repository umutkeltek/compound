// Command testbattery runs the complete Go inventory with home isolated.
package main

import (
	"bytes"
	"context"
	"errors"
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
	var failures []error
	for i, packages := range [][]string{parallel, isolated} {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		fmt.Fprintf(stdout, "testbattery: phase %d/2 packages=%d\n", i+1, len(packages))
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
	var parallel, isolated []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(inventory, "\n"), "\n") {
		pkg := strings.TrimSuffix(line, "\r")
		if !packagePath.MatchString(pkg) || seen[pkg] {
			return nil, nil, fmt.Errorf("invalid or duplicate package inventory entry %q", pkg)
		}
		seen[pkg] = true
		if pkg == homePackage {
			isolated = append(isolated, pkg)
		} else {
			parallel = append(parallel, pkg)
		}
	}
	if len(isolated) != 1 || len(parallel) == 0 {
		return nil, nil, errors.New("inventory requires home and at least one other package")
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
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: testbattery (runs every Go package; no selectors)")
		os.Exit(2)
	}
	// Only prevent subsequent launches after the owner's signal; children
	// remain in its group. Standalone interruption likewise uses group signals.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := runBattery(ctx, runGo, os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "testbattery:", err)
		os.Exit(1)
	}
}
