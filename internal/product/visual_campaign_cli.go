package product

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"
)

func CampaignUsage() string {
	return "campaign prompts|check <campaign.json> | campaign measure <campaign.json> <observation.json> [--signal] | " + campaignViewUsage
}

// RunCampaignCLI is read-only. JSON results are work artifacts; preparation does
// not create a second campaign authority in COMPOUND_HOME or publish anything.
func RunCampaignCLI(args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, CampaignUsage())
		return 0
	}
	fail := func(err error) int { fmt.Fprintln(stderr, "product campaign:", err); return 1 }
	if len(args) < 2 {
		fmt.Fprintln(stderr, CampaignUsage())
		return 2
	}
	var result any
	code := 0
	switch args[0] {
	case "prompts", "check":
		if len(args) != 2 {
			fmt.Fprintln(stderr, CampaignUsage())
			return 2
		}
		c, err := LoadVisualCampaign(args[1])
		if err != nil {
			return fail(err)
		}
		if args[0] == "prompts" {
			result, err = CompileCampaignPrompts(c, filepath.Dir(args[1]), now)
		} else {
			var check CampaignCheck
			check, err = CheckVisualCampaign(c, filepath.Dir(args[1]), now)
			result = check
			if !check.Ready {
				code = 1
			}
		}
		if err != nil {
			return fail(err)
		}
	case "measure":
		if len(args) != 3 && !(len(args) == 4 && args[3] == "--signal") {
			fmt.Fprintln(stderr, CampaignUsage())
			return 2
		}
		measurement, err := LoadCampaignMeasurement(args[1], args[2], now)
		if err != nil {
			return fail(err)
		}
		result = measurement
		if len(args) == 4 {
			result = measurement.Signal
		}
	default:
		fmt.Fprintln(stderr, CampaignUsage())
		return 2
	}
	e := json.NewEncoder(stdout)
	e.SetIndent("", "  ")
	if err := e.Encode(result); err != nil {
		return fail(err)
	}
	return code
}
