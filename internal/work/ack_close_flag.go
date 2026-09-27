package work

import (
	"fmt"
	"strings"

	"compound/internal/runattempt"
)

// ParseAcknowledgeAbandonedFlagPairs scans args linearly for
// --acknowledge-abandoned ratt_X --reason R pairs and returns one
// caller-owned terminalization reservation per pair. The reason is required
// and is later stamped on the receipt as `abandon_ack:<id>:<reason>` so the
// operator-visible audit carries the explicit acknowledgement (see
// buildRunAttemptTerminalizationPlans). Refused if --acknowledge-abandoned
// is present without a following ratt_ id, or if the --reason paired with
// it is empty. The CLI does not consult RunAttempt status here -- the
// close-time gate is the only authority on whether the acknowledgement is
// legal for the specific attempt status; this parser only guarantees the
// pair is well-formed at the door.
//
// stealReason is unused; the steal --reason (if any) is independent of
// acknowledgement reasons and is stamped separately by the close-time gate
// via CloseInput.StealReason. The parameter is preserved verbatim so the
// call site signature does not change.
func ParseAcknowledgeAbandonedFlagPairs(args []string, stealReason string) ([]RunAttemptTerminalizationRequest, error) {
	var out []RunAttemptTerminalizationRequest
	for i := 0; i < len(args); i++ {
		if args[i] != "--acknowledge-abandoned" {
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("--acknowledge-abandoned requires a following ratt_ id")
		}
		id := strings.TrimSpace(args[i+1])
		if id == "" {
			return nil, fmt.Errorf("--acknowledge-abandoned ratt_ id must be non-empty")
		}
		i++
		// Scan ahead for the nearest --reason R whose R is non-empty and
		// whose R is not itself a flag (so the value can carry colons).
		reason := ""
		for j := i + 1; j+1 < len(args); j++ {
			if args[j] == "--reason" {
				if strings.HasPrefix(args[j+1], "--") {
					return nil, fmt.Errorf("--acknowledge-abandoned %s: --reason value must not start with --", id)
				}
				reason = strings.TrimSpace(args[j+1])
				break
			}
		}
		if reason == "" {
			return nil, fmt.Errorf("--acknowledge-abandoned %s requires a non-empty --reason", id)
		}
		out = append(out, RunAttemptTerminalizationRequest{
			RunAttemptID: id,
			Owner:        runattempt.TerminalizationOwnerWorkAcknowledgeAbandoned,
			TargetStatus: runattempt.StatusAbandoned,
			Reason:       reason,
		})
	}
	_ = stealReason
	return out, nil
}
