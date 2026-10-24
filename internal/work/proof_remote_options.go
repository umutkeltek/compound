package work

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const RemoteProofHelp = "  --remote-go-test FILE  sealed canonical general-release route; no local fallback\n  --remote-reconcile ID --work ID --actor ACTOR [--cancel]  reconcile exact persisted request"

type ProofCheckRouteOptions struct {
	RemoteRoute             string
	PublicRedactionActionID string
}

// ParseProofCheckRouteOptions owns compatibility between proof obligations and
// lightweight/recovery modes. Other CLI options remain with their existing owners.
func ParseProofCheckRouteOptions(args []string) (ProofCheckRouteOptions, error) {
	seen := map[string]bool{}
	values := map[string]string{}
	development := false
	for i, arg := range args {
		development = development || arg == "--dev" || strings.HasPrefix(arg, "--dev=")
		if !seen[arg] && i+1 < len(args) {
			values[arg] = args[i+1]
		}
		seen[arg] = true
	}
	options := ProofCheckRouteOptions{
		RemoteRoute:             strings.TrimSpace(values["--remote-go-test"]),
		PublicRedactionActionID: strings.TrimSpace(values["--redact-review"]),
	}
	lightweight := development || seen["--dry-run"] || seen["--ci"] || seen["--ci-console"] ||
		seen["--recover-legacy-retained"] || seen["--timings"]
	if seen["--remote-go-test"] && (options.RemoteRoute == "" || strings.HasPrefix(options.RemoteRoute, "--") || lightweight) {
		return options, fmt.Errorf("--remote-go-test requires exact route file and canonical full proof")
	}
	if seen["--redact-review"] && (options.PublicRedactionActionID == "" || strings.HasPrefix(options.PublicRedactionActionID, "--")) {
		return options, fmt.Errorf("--redact-review requires an exact recorded ActionIntent ID")
	}
	if options.PublicRedactionActionID != "" && lightweight {
		return options, fmt.Errorf("reviewed redaction requires canonical full proof, not a lightweight or recovery mode")
	}
	return options, nil
}

func ProofResourceProfileForCanonicalPlanWithRemote(ctx context.Context, plan CanonicalProofPlan, homePath, routePath string, command RemoteProofCommand) (ProofResourceProfile, error) {
	profile, err := ProofResourceProfileForCanonicalPlan(plan)
	if err != nil || routePath == "" {
		return profile, err
	}
	if err := ValidateRemoteProofDomain(homePath); err != nil {
		return ProofResourceProfile{}, err
	}
	return SealRemoteProofProfile(ctx, profile, routePath, command)
}

func ValidateRemoteProofSteps(ctx context.Context, steps []ProofCheckStep) error {
	if !RemoteProofRequired(ctx) {
		return nil
	}
	for _, step := range steps {
		if step.Name == "go-test" && step.RemoteEvidence != nil {
			return nil
		}
	}
	return fmt.Errorf("remote canonical gate has no terminal receipt; reconcile original reservation before fresh proof")
}

// ProofCheckInvocationIdentity preserves exact native argv as proof authority.
func ProofCheckInvocationIdentity(args []string) (string, error) {
	argv := append([]string{"compound", "proof-check"}, args...)
	body, err := json.Marshal(argv)
	if err != nil {
		return "", err
	}
	return "argv-json:" + string(body), nil
}
