package work

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// VerifierVerdictUsage is the `compound work verifier-verdict` usage line.
const VerifierVerdictUsage = "usage: compound work verifier-verdict <work_id> --tip <full-sha> [--verdicts DIR] [--attach --actor A] [--json]\n" +
	"  resolves the post-land verifier verdict covering the landed tip; exit 0 covered, 1 refused (red culprit or not verified yet), 2 usage\n" +
	"  --attach stores the covering verifier-verdict:<run_id>:<sha> ref on the work (reads $COMPOUND_HOME/state/verify only; not with --verdicts)"

// VerifierVerdictResult is the JSON body of `compound work verifier-verdict`.
type VerifierVerdictResult struct {
	WorkID   string             `json:"work_id"`
	Decision VerifierVerdictDecision `json:"decision"`
	Attached bool               `json:"attached"`
}

// RunVerifierVerdictCLI resolves (and optionally attaches) the verifier verdict for one
// work's landed tip. args starts at the work id.
func RunVerifierVerdictCLI(s *Store, args []string, stdout, stderr io.Writer) int {
	var workID, tip, verdicts, actor string
	var attach, jsonOut bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, bool) {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") || strings.TrimSpace(args[i+1]) == "" {
				return "", false
			}
			i++
			return strings.TrimSpace(args[i]), true
		}
		var ok = true
		switch arg {
		case "--tip":
			tip, ok = value()
		case "--verdicts":
			verdicts, ok = value()
		case "--actor":
			actor, ok = value()
		case "--attach":
			attach = true
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(arg, "-") || workID != "" {
				ok = false
			} else {
				workID = strings.TrimSpace(arg)
			}
		}
		if !ok {
			fmt.Fprintf(stderr, "error: bad argument %q\n%s\n", arg, VerifierVerdictUsage)
			return 2
		}
	}
	switch {
	case workID == "" || tip == "":
		fmt.Fprintln(stderr, VerifierVerdictUsage)
		return 2
	case attach && actor == "":
		fmt.Fprintf(stderr, "error: --attach requires --actor\n%s\n", VerifierVerdictUsage)
		return 2
	case attach && verdicts != "":
		fmt.Fprintf(stderr, "error: --attach reads only $COMPOUND_HOME/state/verify; drop --verdicts\n%s\n", VerifierVerdictUsage)
		return 2
	}
	decision, err := s.ResolveVerifierVerdict(workID, tip, verdicts)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	result := VerifierVerdictResult{WorkID: workID, Decision: decision}
	if attach && decision.Status == VerifierVerdictCovered {
		w, err := s.Get(workID)
		if err == nil {
			_, err = s.Advance(w.ID, AdvanceInput{To: w.State, Actor: actor, VerificationID: decision.Ref, Note: "attached covering verifier verdict"}, time.Now())
		}
		if err != nil {
			fmt.Fprintln(stderr, "error: attach verifier verdict:", err)
			return 2
		}
		result.Attached = true
	}
	if jsonOut {
		body, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(stdout, string(body))
	} else {
		fmt.Fprintf(stdout, "verifier-verdict %s status=%s tip=%s ref=%s attached=%v\n", workID, decision.Status, decision.Tip, firstNonEmpty(decision.Ref, "-"), result.Attached)
		fmt.Fprintf(stdout, "reason: %s\n", decision.Reason)
		if decision.Status == VerifierVerdictCovered && !attach {
			fmt.Fprintf(stdout, "attach: compound work verifier-verdict %s --tip %s --attach --actor <actor>\n", workID, tip)
		}
	}
	if decision.Status != VerifierVerdictCovered {
		return 1
	}
	return 0
}
