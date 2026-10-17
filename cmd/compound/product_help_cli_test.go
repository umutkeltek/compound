package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const productHelpChildEnv = "COMPOUND_PRODUCT_HELP_CHILD"

// A subprocess exposes os.Exit and verifies that help needs no usable home.
func TestProductHelpExitsZeroAndDoesNotOpenHome(t *testing.T) {
	if mode := os.Getenv(productHelpChildEnv); mode != "" {
		switch mode {
		case "help-long":
			cmdProduct([]string{"--help"})
		case "help-short":
			cmdProduct([]string{"-h"})
		case "empty":
			cmdProduct(nil)
		case "unknown":
			cmdProduct([]string{"unknown-subcommand"})
		case "malformed":
			cmdProduct([]string{"authority", "--unknown-flag"})
		default:
			t.Fatalf("unknown child mode %q", mode)
		}
		os.Exit(0)
	}
	t.Parallel()
	tests := []struct {
		mode        string
		exit        int
		initialized bool
		stderr      string
	}{
		{mode: "help-long"},
		{mode: "help-short"},
		{mode: "empty", exit: 2, stderr: "usage: compound product"},
		{mode: "unknown", exit: 2, initialized: true, stderr: "usage: compound product"},
		{mode: "malformed", exit: 2, initialized: true, stderr: "usage: compound product authority"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			homePath := filepath.Join(t.TempDir(), "missing-home")
			if tt.initialized {
				homePath = initializedTestHome(t)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestProductHelpExitsZeroAndDoesNotOpenHome$")
			cmd.Env = append(os.Environ(), productHelpChildEnv+"="+tt.mode, "COMPOUND_HOME="+homePath)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			exit := 0
			if err != nil {
				failure, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("run child: %v", err)
				}
				exit = failure.ExitCode()
			}
			if exit != tt.exit {
				t.Fatalf("exit=%d, want %d; stdout=%s; stderr=%s", exit, tt.exit, &stdout, &stderr)
			}
			if tt.exit == 0 {
				if stdout.String() != productUsage()+"\n" || stderr.Len() != 0 {
					t.Fatalf("help streams: stdout=%q stderr=%q", &stdout, &stderr)
				}
			} else if stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.stderr) {
				t.Fatalf("failure streams: stdout=%q stderr=%q", &stdout, &stderr)
			}
			if !tt.initialized {
				if _, err := os.Lstat(homePath); !os.IsNotExist(err) {
					t.Fatalf("unexpected home materialization: %v", err)
				}
			}
		})
	}
}
