package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestBatteryPartitionsEntireInventoryExactlyOnce(t *testing.T) {
	t.Parallel()
	var inventory []string
	for i := 99; i >= 0; i-- {
		inventory = append(inventory, fmt.Sprintf("compound/internal/pkg%02d", i))
	}
	inventory = append(inventory, "compound/internal/home", "compound/scripts/testbattery")
	var calls [][]string
	var stdout, stderr bytes.Buffer
	err := runBattery(t.Context(), func(_ context.Context, args []string, out, errOut io.Writer) error {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "list" {
			fmt.Fprintln(out, strings.Join(inventory, "\n"))
			fmt.Fprintln(errOut, "go: downloading synthetic dependency")
		} else {
			fmt.Fprintf(out, "phase %d stdout\n", len(calls)-1)
			fmt.Fprintf(errOut, "phase %d stderr\n", len(calls)-1)
		}
		return nil
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || !reflect.DeepEqual(calls[0], []string{"list", "./..."}) {
		t.Fatalf("inventory and two phases = %v", calls)
	}
	wantParallel := []string{"test", "-count=1", "-timeout=40m"}
	for i := 0; i < 100; i++ {
		wantParallel = append(wantParallel, fmt.Sprintf("compound/internal/pkg%02d", i))
	}
	wantParallel = append(wantParallel, "compound/scripts/testbattery")
	if !reflect.DeepEqual(calls[1], wantParallel) {
		t.Fatalf("parallel phase lost, repeated, reordered, or filtered packages: %v", calls[1])
	}
	if want := []string{"test", "-count=1", "-timeout=40m", "compound/internal/home"}; !reflect.DeepEqual(calls[2], want) {
		t.Fatalf("isolated phase = %v; want %v", calls[2], want)
	}
	for _, marker := range []string{"phase 1 stdout", "phase 2 stdout"} {
		if !strings.Contains(stdout.String(), marker) {
			t.Errorf("stdout lost %q: %s", marker, &stdout)
		}
	}
	for _, marker := range []string{"go: downloading synthetic dependency", "phase 1 stderr", "phase 2 stderr"} {
		if !strings.Contains(stderr.String(), marker) {
			t.Errorf("stderr lost %q: %s", marker, &stderr)
		}
	}
}

func TestBatteryRefusesInvalidInventoryBeforeTests(t *testing.T) {
	t.Parallel()
	for name, inventory := range map[string]string{
		"empty":                "",
		"missing home":         "compound/internal/work\n",
		"only home":            "compound/internal/home\n",
		"duplicate home":       "compound/internal/home\ncompound/internal/home\ncompound/internal/work\n",
		"duplicate other":      "compound/internal/home\ncompound/internal/work\ncompound/internal/work\n",
		"blank line":           "compound/internal/home\n\ncompound/internal/work\n",
		"extra newline":        "compound/internal/home\ncompound/internal/work\n\n",
		"leading whitespace":   "compound/internal/home\n compound/internal/work\n",
		"trailing whitespace":  "compound/internal/home\ncompound/internal/work \n",
		"diagnostic on stdout": "compound/internal/home\ngo: downloading dependency\n",
		"foreign module":       "compound/internal/home\nother/internal/work\n",
		"pattern":              "compound/internal/home\ncompound/...\n",
		"empty segment":        "compound/internal/home\ncompound//work\n",
		"parent segment":       "compound/internal/home\ncompound/../work\n",
		"option":               "compound/internal/home\n-short\n",
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			err := runBattery(t.Context(), func(_ context.Context, _ []string, out, _ io.Writer) error {
				calls++
				_, err := io.WriteString(out, inventory)
				return err
			}, io.Discard, io.Discard)
			if err == nil || calls != 1 {
				t.Fatalf("invalid inventory: error=%v calls=%d; want refusal before any tests", err, calls)
			}
		})
	}
}

func TestBatteryAggregatesOrdinaryFailuresAndStopsOnInfraOrCancellation(t *testing.T) {
	t.Parallel()
	testFailure := batteryExitError(t, 1)
	startFailure := errors.New("synthetic child start error")
	for _, tt := range []struct {
		name        string
		failures    [3]error
		cancelAfter int
		wantCalls   int
		wantErr     bool
	}{
		{name: "success", wantCalls: 3},
		{name: "first fails home still runs", failures: [3]error{nil, testFailure, nil}, wantCalls: 3, wantErr: true},
		{name: "home fails", failures: [3]error{nil, nil, testFailure}, wantCalls: 3, wantErr: true},
		{name: "both fail", failures: [3]error{nil, testFailure, testFailure}, wantCalls: 3, wantErr: true},
		{name: "list fails despite valid stdout", failures: [3]error{testFailure}, wantCalls: 1, wantErr: true},
		{name: "list start fails", failures: [3]error{startFailure}, wantCalls: 1, wantErr: true},
		{name: "first start fails", failures: [3]error{nil, startFailure}, wantCalls: 2, wantErr: true},
		{name: "home start fails", failures: [3]error{nil, nil, startFailure}, wantCalls: 3, wantErr: true},
		{name: "cancel before list", cancelAfter: -1, wantCalls: 0, wantErr: true},
		{name: "cancel after list", cancelAfter: 1, wantCalls: 1, wantErr: true},
		{name: "cancel before home", cancelAfter: 2, wantCalls: 2, wantErr: true},
		{name: "ordinary failure while cancelled", failures: [3]error{nil, testFailure}, cancelAfter: 2, wantCalls: 2, wantErr: true},
		{name: "cancel after home", cancelAfter: 3, wantCalls: 3, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancelAfter == -1 {
				cancel()
			}
			calls := 0
			err := runBattery(ctx, func(_ context.Context, _ []string, out, _ io.Writer) error {
				calls++
				if calls == 1 {
					fmt.Fprintln(out, "compound/internal/home\ncompound/internal/work")
				}
				if calls == tt.cancelAfter {
					cancel()
				}
				if calls > len(tt.failures) {
					t.Fatal("unexpected extra child launch")
				}
				return tt.failures[calls-1]
			}, io.Discard, io.Discard)
			if (err != nil) != tt.wantErr || calls != tt.wantCalls {
				t.Fatalf("error=%v calls=%d; want error=%t calls=%d", err, calls, tt.wantErr, tt.wantCalls)
			}
			if tt.cancelAfter != 0 && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation not retained: %v", err)
			}
			for _, childErr := range tt.failures {
				if childErr != nil && !errors.Is(err, childErr) {
					t.Errorf("child failure not retained: %v", err)
				}
			}
		})
	}
}

func TestRunGoRefusesCancelledStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var output bytes.Buffer
	if err := runGo(ctx, []string{"version"}, &output, &output); !errors.Is(err, context.Canceled) || output.Len() != 0 {
		t.Fatalf("cancelled child: error=%v output=%q", err, &output)
	}
}

func batteryExitError(t *testing.T, code int) error {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBatteryExitHelper$")
	cmd.Env = append(os.Environ(), "COMPOUND_TEST_BATTERY_EXIT="+strconv.Itoa(code))
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != code {
		t.Fatalf("helper exit: %v; want %d", err, code)
	}
	return err
}

func TestBatteryExitHelper(t *testing.T) {
	if raw := os.Getenv("COMPOUND_TEST_BATTERY_EXIT"); raw != "" {
		code, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
		os.Exit(code)
	}
}
