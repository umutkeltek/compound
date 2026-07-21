package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T1 (packet 2026-07-21-zip27-actuator-cli.md): `actuator budget-set --max-actions ten`
// must error naming the flag and the bad value, and must NOT write the budget file.
// At base, actuatorInt silently returned the default (0 == unlimited) on parse failure.
func TestActuatorBudgetSetRejectsUnparseableMaxActions(t *testing.T) {
	homePath := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runActuator([]string{
		"budget-set", "--channel", "test-channel", "--window-hours", "24",
		"--max-actions", "ten", "--home", homePath,
	}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("want non-zero exit for unparseable --max-actions, got 0; stdout=%q", stdout.String())
	}
	errText := stderr.String()
	if !strings.Contains(errText, "--max-actions") || !strings.Contains(errText, "ten") {
		t.Fatalf("error should mention --max-actions and the bad value %q; got %q", "ten", errText)
	}
	budgetPath := filepath.Join(homePath, "actuator", "budget", "default.budget.json")
	if _, err := os.Stat(budgetPath); !os.IsNotExist(err) {
		t.Fatalf("budget file must NOT be written on parse failure (stat err=%v)", err)
	}
}

// actuatorFloat gets the same falsifier as actuatorInt (T1's sibling): a
// non-empty, non-parseable --max-spend-usd must error, not silently default.
func TestActuatorBudgetSetRejectsUnparseableMaxSpendUSD(t *testing.T) {
	homePath := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runActuator([]string{
		"budget-set", "--channel", "test-channel", "--window-hours", "24",
		"--max-spend-usd", "lots", "--home", homePath,
	}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("want non-zero exit for unparseable --max-spend-usd, got 0; stdout=%q", stdout.String())
	}
	errText := stderr.String()
	if !strings.Contains(errText, "--max-spend-usd") || !strings.Contains(errText, "lots") {
		t.Fatalf("error should mention --max-spend-usd and the bad value %q; got %q", "lots", errText)
	}
	budgetPath := filepath.Join(homePath, "actuator", "budget", "default.budget.json")
	if _, err := os.Stat(budgetPath); !os.IsNotExist(err) {
		t.Fatalf("budget file must NOT be written on parse failure (stat err=%v)", err)
	}
}

// T2: a typo'd flag on `actuator run` must be rejected naming the unknown flag,
// exactly as work_cli.go's validateKnownFlags pattern already does for work
// subcommands. At base, actuator subcommands called validateKnownFlags zero times.
func TestActuatorRunRejectsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runActuator([]string{
		"run", "--work", "w1", "--harness", "noop", "--max-actons", "3",
	}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("want exit 2 for unknown flag, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--max-actons") {
		t.Fatalf("error should name the unknown flag --max-actons; got %q", stderr.String())
	}
}

// Decision 2 applies to every actuator subcommand, not just `run` — enumerate the
// full dispatch table and prove each one rejects an unrecognized flag by name.
func TestActuatorSubcommandsRejectUnknownFlag(t *testing.T) {
	homePath := t.TempDir()
	cases := []struct {
		name string
		args []string
	}{
		{"plan", []string{"plan", "--work", "w1", "--bogus-flag", "x", "--home", homePath}},
		{"budget", []string{"budget", "--bogus-flag", "x", "--home", homePath}},
		{"budget-set", []string{"budget-set", "--channel", "c", "--window-hours", "24", "--bogus-flag", "x", "--home", homePath}},
		{"switchback", []string{"switchback", "--profile", "p.yaml", "--work", "w1", "--bogus-flag", "x", "--home", homePath}},
		{"run", []string{"run", "--work", "w1", "--harness", "noop", "--bogus-flag", "x", "--home", homePath}},
		{"post", []string{"post", "--work", "w1", "--content", "hi", "--bogus-flag", "x", "--home", homePath}},
		{"observe", []string{"observe", "--bogus-flag", "x", "--home", homePath}},
		{"held", []string{"held", "--bogus-flag", "x", "--home", homePath}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runActuator(tc.args, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("%s: want exit 2 for unknown flag, got %d; stderr=%q", tc.name, code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "--bogus-flag") {
				t.Fatalf("%s: error should name --bogus-flag; got %q", tc.name, stderr.String())
			}
		})
	}
}

// T3: valid numeric + known flags still work end-to-end — one happy path per
// changed subcommand, proving decisions 1+2 add zero behavior change for valid
// input (existing green paths stay green).
func TestActuatorSubcommandsAcceptKnownFlagsEndToEnd(t *testing.T) {
	t.Run("plan", func(t *testing.T) {
		homePath := t.TempDir()
		var stdout, stderr bytes.Buffer
		code := runActuator([]string{
			"plan", "--work", "w1", "--class", "local_reversible", "--reversibility", "easy",
			"--channel", "test", "--product", "p1", "--home", homePath, "--json",
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("plan: want exit 0, got %d; stderr=%q", code, stderr.String())
		}
	})

	t.Run("budget", func(t *testing.T) {
		homePath := t.TempDir()
		var stdout, stderr bytes.Buffer
		code := runActuator([]string{"budget", "--product", "p1", "--home", homePath, "--json"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("budget: want exit 0, got %d; stderr=%q", code, stderr.String())
		}
	})

	t.Run("budget-set", func(t *testing.T) {
		homePath := t.TempDir()
		var stdout, stderr bytes.Buffer
		code := runActuator([]string{
			"budget-set", "--channel", "test-channel", "--window-hours", "24",
			"--max-actions", "5", "--max-spend-usd", "1.5", "--max-tokens", "100",
			"--product", "p1", "--home", homePath,
		}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("budget-set: want exit 0, got %d; stderr=%q", code, stderr.String())
		}
		budgetPath := filepath.Join(homePath, "actuator", "budget", "p1.budget.json")
		data, err := os.ReadFile(budgetPath)
		if err != nil {
			t.Fatalf("budget-set: expected budget file written: %v", err)
		}
		if !strings.Contains(string(data), `"max_actions": 5`) {
			t.Fatalf("budget-set: expected max_actions=5 in %s", data)
		}
	})

	t.Run("observe", func(t *testing.T) {
		homePath := t.TempDir()
		var stdout, stderr bytes.Buffer
		code := runActuator([]string{"observe", "--product", "p1", "--home", homePath}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("observe: want exit 0, got %d; stderr=%q", code, stderr.String())
		}
	})

	t.Run("held", func(t *testing.T) {
		homePath := t.TempDir()
		var stdout, stderr bytes.Buffer
		code := runActuator([]string{"held", "--product", "p1", "--home", homePath}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("held: want exit 0, got %d; stderr=%q", code, stderr.String())
		}
	})

	// run/post/switchback reach into internal/actuator + internal/act domain logic
	// this packet does not touch, so these assert only what changed here: known
	// flags must never be rejected as unknown, and passthrough argv after `--`
	// must never be flag-validated at all.
	t.Run("run_passthrough_args_not_validated", func(t *testing.T) {
		homePath := t.TempDir()
		var stdout, stderr bytes.Buffer
		runActuator([]string{
			"run", "--work", "w1", "--harness", "noop-does-not-exist",
			"--home", homePath, "--", "echo", "--not-a-real-flag",
		}, &stdout, &stderr)
		if strings.Contains(stderr.String(), "unknown flag") {
			t.Fatalf("run: known flags + passthrough argv after -- must not be flag-rejected; stderr=%q", stderr.String())
		}
	})

	t.Run("post_known_flags_not_rejected", func(t *testing.T) {
		homePath := t.TempDir()
		var stdout, stderr bytes.Buffer
		runActuator([]string{
			"post", "--work", "w1", "--content", "hello", "--product", "p1",
			"--channel", "distribution", "--surface", "generic", "--metric", "m1",
			"--expected-effect", "up", "--window", "7d", "--home", homePath,
		}, &stdout, &stderr)
		if strings.Contains(stderr.String(), "unknown flag") {
			t.Fatalf("post: known flags must not be rejected; stderr=%q", stderr.String())
		}
	})

	t.Run("switchback_known_flags_not_rejected", func(t *testing.T) {
		homePath := t.TempDir()
		profilePath := filepath.Join(t.TempDir(), "profile.yaml")
		if err := os.WriteFile(profilePath, []byte("id: test_profile\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		runActuator([]string{
			"switchback", "--profile", profilePath, "--work", "w1", "--home", homePath,
		}, &stdout, &stderr)
		if strings.Contains(stderr.String(), "unknown flag") {
			t.Fatalf("switchback: known flags must not be rejected; stderr=%q", stderr.String())
		}
	})
}
