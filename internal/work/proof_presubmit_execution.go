package work

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// MaxPresubmitExecutionBudget is an upper ceiling, not an expected wait.
// It covers the existing sequential phase limits plus preparation and other gates.
const MaxPresubmitExecutionBudget = 4 * time.Hour

type PresubmitExecutionRequest struct {
	WorkID  string        `json:"work_id"`
	Actor   string        `json:"actor"`
	Repo    string        `json:"repo"`
	BaseRef string        `json:"base_ref"`
	OutPath string        `json:"out_path"`
	Budget  time.Duration `json:"active_budget_ns"`
	Wait    time.Duration `json:"admission_wait_ns,omitempty"`
	Args    []string      `json:"args"`
}

// IsGovernedPresubmitArgs selects only the explicit fixed-purpose producer.
func IsGovernedPresubmitArgs(args []string) bool {
	if ProofCheckNonCertifyingMode(args) != "--presubmit" {
		return false
	}
	for _, arg := range args {
		switch strings.SplitN(arg, "=", 2)[0] {
		case "--work", "--actor", "--execution-timeout":
			return true
		}
	}
	return false
}

func ParsePresubmitExecutionRequest(args []string) (PresubmitExecutionRequest, error) {
	var r PresubmitExecutionRequest
	values := map[string]string{}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		flag := args[i]
		if seen[flag] {
			return r, fmt.Errorf("governed presubmit flag %s may be provided only once", flag)
		}
		seen[flag] = true
		switch flag {
		case "--presubmit", "--json":
		case "--repo", "--base", "--out", "--work", "--actor", "--execution-timeout", "--wait":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" || strings.HasPrefix(args[i+1], "--") {
				return r, fmt.Errorf("%s requires a non-empty separate value", flag)
			}
			i++
			values[flag] = strings.TrimSpace(args[i])
		default:
			return r, fmt.Errorf("unsupported governed presubmit argument %q", flag)
		}
	}
	if !seen["--presubmit"] || !validProofWorkID(values["--work"]) || values["--actor"] == "" || values["--out"] == "" || !presubmitFullSHA.MatchString(values["--base"]) {
		return r, fmt.Errorf("governed presubmit requires --presubmit --work WORK --actor ACTOR --base FULL_SHA --out FILE --execution-timeout DURATION")
	}
	budget, err := time.ParseDuration(values["--execution-timeout"])
	if err != nil || budget <= 0 || budget > MaxPresubmitExecutionBudget {
		return r, fmt.Errorf("governed presubmit execution timeout must be positive and no greater than %s", MaxPresubmitExecutionBudget)
	}
	var wait time.Duration
	if seen["--wait"] {
		wait, err = time.ParseDuration(values["--wait"])
		if err != nil || wait <= 0 {
			return r, fmt.Errorf("governed presubmit --wait requires a positive bounded duration")
		}
	}
	repo, err := filepath.Abs(values["--repo"])
	if err != nil {
		return r, fmt.Errorf("resolve presubmit repository: %w", err)
	}
	out, err := filepath.Abs(values["--out"])
	if err != nil {
		return r, fmt.Errorf("resolve presubmit output: %w", err)
	}
	r = PresubmitExecutionRequest{WorkID: values["--work"], Actor: values["--actor"], Repo: repo, BaseRef: values["--base"], OutPath: out, Budget: budget, Wait: wait, Args: append([]string(nil), args...)}
	return r, nil
}

func (r PresubmitExecutionRequest) command() string {
	body, _ := json.Marshal(append([]string{"compound", "proof-check"}, r.Args...))
	return "argv-json:" + string(body)
}
