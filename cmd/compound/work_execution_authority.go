package main

import (
	"fmt"
	"strings"
	"time"

	"compound/internal/work"
)

const workExecutionAuthorityUsage = "usage: compound work execution-authority --work W --policy-decision POL --action-intent ACT --permission-mode plan [--json]"

// checkWorkExecutionAuthority exposes the existing read-only launch owner for a
// transport's per-account callback. It never opens a writable store or lock.
func checkWorkExecutionAuthority(store *work.Store, args []string, now time.Time) error {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		key := args[i]
		if key == "--json" {
			continue
		}
		if key != "--work" && key != "--policy-decision" && key != "--action-intent" && key != "--permission-mode" {
			return fmt.Errorf("%s", workExecutionAuthorityUsage)
		}
		if _, duplicate := values[key]; duplicate || i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" || strings.HasPrefix(args[i+1], "--") {
			return fmt.Errorf("%s", workExecutionAuthorityUsage)
		}
		i++
		values[key] = args[i]
	}
	if len(values) != 4 || values["--permission-mode"] != "plan" {
		return fmt.Errorf("execution authority requires explicit plan mode and exact work/policy/action")
	}
	return store.ValidateExecutionStartAuthority(values["--work"], values["--policy-decision"], values["--action-intent"], func() time.Time { return now })
}
