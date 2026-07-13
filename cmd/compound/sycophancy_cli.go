package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"compound/internal/home"
	"compound/internal/sycophancy"
)

func sycophancyUsage() string {
	return "usage: compound sycophancy <probe --transcripts FILE [--home P] [--record] [--json] | asymmetry --records FILE [--json]>\n" +
		"  probe      score recorded per-lane probe transcripts (paired-stance shift); --record writes routeevidence.SycophancyScore per lane tuple\n" +
		"  asymmetry  project per-lane operator- vs peer-agreement from derived judgment records (read-only)\n" +
		"transcripts/records are read from files; this command makes NO provider calls — real lanes are probed out of band and their endorsements captured to a transcript."
}

// cmdSycophancy is the operator/orchestrator door onto the sycophancy measurement organ.
// It is deliberately file-fed: a real lane is probed out of band (with whatever provider
// calls it makes) and its supportive/critical endorsements captured to a transcript;
// this command only scores the captured numbers, so it can point at a real lane later
// without any provider call in its own path.
func cmdSycophancy(args []string) {
	if err := runSycophancy(os.Stdout, args); err != nil {
		if err == errSycophancyUsage {
			fmt.Fprintln(os.Stderr, sycophancyUsage())
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "sycophancy:", err)
		os.Exit(1)
	}
}

var errSycophancyUsage = fmt.Errorf("usage")

func runSycophancy(out io.Writer, args []string) error {
	if len(args) == 0 || hasUsageFlag(args) {
		return errSycophancyUsage
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "probe":
		return runSycophancyProbe(out, rest)
	case "asymmetry":
		return runSycophancyAsymmetry(out, rest)
	default:
		return errSycophancyUsage
	}
}

func runSycophancyProbe(out io.Writer, args []string) error {
	transcripts := ""
	homeFlag := ""
	record := false
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--transcripts":
			if i+1 >= len(args) {
				return fmt.Errorf("--transcripts requires a file path")
			}
			transcripts = args[i+1]
			i++
		case "--home":
			if i+1 >= len(args) {
				return fmt.Errorf("--home requires a path")
			}
			homeFlag = args[i+1]
			i++
		case "--record":
			record = true
		case "--json":
			jsonOut = true
		default:
			return errSycophancyUsage
		}
	}
	if transcripts == "" {
		return fmt.Errorf("--transcripts FILE is required")
	}
	bundle, err := sycophancy.LoadTranscriptBundle(transcripts)
	if err != nil {
		return err
	}
	var homePath string
	if record {
		homePath, err = home.Resolve(homeFlag)
		if err != nil {
			return err
		}
	}
	type probeRow struct {
		Result   sycophancy.LaneProbeResult `json:"result"`
		ScoreID  string                     `json:"score_id,omitempty"`
		Recorded bool                       `json:"recorded"`
	}
	rows := make([]probeRow, 0, len(bundle.Transcripts))
	for _, transcript := range bundle.Transcripts {
		result, err := sycophancy.ScoreTranscript(transcript)
		if err != nil {
			return err
		}
		row := probeRow{Result: result}
		if record {
			score, err := sycophancy.RecordScore(homePath, result, time.Now().UTC(), []string{"compound://sycophancy/transcript"})
			if err != nil {
				return err
			}
			row.ScoreID = score.ID
			row.Recorded = true
		}
		rows = append(rows, row)
	}
	if jsonOut {
		printJSON(rows)
		return nil
	}
	fmt.Fprintln(out, "# sycophancy probe")
	for _, row := range rows {
		fmt.Fprintf(out, "- lane=%s mean_shift=%.3f max_shift=%.3f probes=%d",
			row.Result.LaneNodeID, row.Result.MeanShift, row.Result.MaxShift, row.Result.ProbeCount)
		if row.Recorded {
			fmt.Fprintf(out, " recorded=%s", row.ScoreID)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "note: stance shift is EVIDENCE per lane tuple, not a routing gate; a max-stance-shift threshold is a separate later decision.")
	return nil
}

func runSycophancyAsymmetry(out io.Writer, args []string) error {
	records := ""
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--records":
			if i+1 >= len(args) {
				return fmt.Errorf("--records requires a file path")
			}
			records = args[i+1]
			i++
		case "--json":
			jsonOut = true
		default:
			return errSycophancyUsage
		}
	}
	if records == "" {
		return fmt.Errorf("--records FILE is required")
	}
	file, err := sycophancy.LoadJudgmentRecords(records)
	if err != nil {
		return err
	}
	rows := sycophancy.ProjectAgreementAsymmetry(file.Records)
	if jsonOut {
		printJSON(rows)
		return nil
	}
	fmt.Fprintln(out, "# sycophancy agreement-asymmetry")
	for _, row := range rows {
		fmt.Fprintf(out, "- lane=%s operator=%.3f peer=%.3f asymmetry=%.3f (op_samples=%d peer_cmp=%d)\n",
			row.LaneNodeID, row.OperatorAgreementRate, row.PeerAgreementRate, row.Asymmetry, row.OperatorSamples, row.PeerComparisons)
	}
	fmt.Fprintln(out, "note: high asymmetry = a lane agreeing with the operator far above its agreement with peer lanes (tracking the operator, not truth).")
	return nil
}
