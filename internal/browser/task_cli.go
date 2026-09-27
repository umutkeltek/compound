package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/policy"
)

const portableBrowserUsage = "usage: compound browser settings [--json] [--home DIR] [--enable|--disable --work work_ID]\n" +
	"       compound browser route [--json] [--home DIR] [--profile ID]\n" +
	"       compound browser goal --work work_ID --url HTTPS_URL --goal TEXT --expect-text TEXT [--provider-budget-usd USD] [--max-steps 1..6] [--profile ID] [--json] [--home DIR] (budget required for Jev)"

func runPortableBrowser(args []string, out io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	verb := args[0]
	if verb != "settings" && verb != "route" && verb != "goal" {
		return false, nil
	}
	values := map[string]string{}
	switches := map[string]bool{}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json", "--enable", "--disable":
			if switches[arg] {
				return true, fmt.Errorf("duplicate flag %s", arg)
			}
			switches[arg] = true
		case "--home", "--work", "--profile", "--url", "--goal", "--expect-text", "--max-steps", "--provider-budget-usd":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") || values[arg] != "" {
				return true, fmt.Errorf("%s requires one value", arg)
			}
			i++
			values[arg] = args[i]
		default:
			return true, fmt.Errorf("unknown browser %s argument %q", verb, arg)
		}
	}
	if switches["--enable"] && switches["--disable"] {
		return true, fmt.Errorf("choose --enable or --disable")
	}
	if verb != "settings" && (switches["--enable"] || switches["--disable"]) {
		return true, fmt.Errorf("enable and disable are settings options")
	}
	if verb != "goal" && (values["--url"] != "" || values["--goal"] != "" || values["--expect-text"] != "" || values["--max-steps"] != "" || values["--provider-budget-usd"] != "") {
		return true, fmt.Errorf("task options require goal")
	}
	if verb == "route" && values["--work"] != "" {
		return true, fmt.Errorf("route is read-only")
	}
	if verb == "settings" && values["--profile"] != "" {
		return true, fmt.Errorf("profile is a route option")
	}
	homePath, err := home.Resolve(values["--home"])
	if err != nil {
		return true, err
	}
	if verb == "goal" {
		if values["--work"] == "" || values["--url"] == "" || values["--goal"] == "" || values["--expect-text"] == "" {
			return true, fmt.Errorf("goal requires --work, --url, --goal, and --expect-text")
		}
		steps := 3
		if values["--max-steps"] != "" {
			steps, err = strconv.Atoi(values["--max-steps"])
			if err != nil {
				return true, fmt.Errorf("invalid max steps")
			}
		}
		budget := 0.0
		if values["--provider-budget-usd"] != "" {
			budget, err = strconv.ParseFloat(values["--provider-budget-usd"], 64)
			if err != nil || !finitePositive(budget) {
				return true, fmt.Errorf("goal requires positive --provider-budget-usd when supplied")
			}
		}
		cfg, _, err := policy.LoadConfig(homePath)
		if err != nil {
			return true, err
		}
		result, err := RunTask(context.Background(), TaskRequest{Home: homePath, WorkID: values["--work"], URL: values["--url"], Goal: values["--goal"], ExpectText: values["--expect-text"], MaxSteps: steps, MaxProviderUSD: budget, ProfileID: values["--profile"]}, cfg)
		if err != nil {
			if switches["--json"] && result.Status != "" {
				_ = json.NewEncoder(out).Encode(result)
			}
			return true, err
		}
		if switches["--json"] {
			return true, json.NewEncoder(out).Encode(result)
		}
		_, err = fmt.Fprintf(out, "browser backend=%s status=%s verified=%t steps=%d\n", result.Backend, result.Status, result.Verified, result.Steps)
		return true, err
	}
	settings, err := LoadSettings(homePath)
	if err != nil {
		return true, err
	}
	if verb == "route" {
		route := SelectRoute(settings, values["--profile"])
		if switches["--json"] {
			return true, json.NewEncoder(out).Encode(route)
		}
		_, err = fmt.Fprintf(out, "browser backend=%s reason=%s\n", route.Backend, route.Reason)
		return true, err
	}
	if !switches["--enable"] && !switches["--disable"] {
		if values["--work"] != "" {
			return true, fmt.Errorf("--work requires --enable or --disable")
		}
		if switches["--json"] {
			return true, json.NewEncoder(out).Encode(settings)
		}
		_, err = fmt.Fprintf(out, "browser jev_enabled=%t\n", settings.JevEnabled)
		return true, err
	}
	if values["--work"] == "" {
		return true, fmt.Errorf("browser setting mutation requires --work")
	}
	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		return true, err
	}
	result, err := SetJevEnabled(homePath, values["--work"], switches["--enable"], cfg, time.Now().UTC())
	if err != nil {
		return true, err
	}
	if switches["--json"] {
		if err := json.NewEncoder(out).Encode(result); err != nil {
			return true, err
		}
	} else {
		if _, err := fmt.Fprintf(out, "browser jev_enabled=%t changed=%t refused=%t receipt=%s\n", result.Settings.JevEnabled, result.Changed, result.Refused, result.ReceiptID); err != nil {
			return true, err
		}
	}
	if result.Refused {
		return true, fmt.Errorf("browser setting held by policy: %s", result.Reason)
	}
	return true, nil
}
