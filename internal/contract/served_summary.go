package contract

import (
	"regexp"
	"strings"

	"compound/internal/capgraph"
	"compound/internal/envelope"
)

// SummaryMD is the one part of a ContextPack a session-start harness actually reads
// (the hook hands it to the model as additional context; the pack JSON stays on disk).
// Measured 2026-11-03 over 76 sessions: the summary said only "N capability offer(s)
// surfaced", so offer names never reached the model and served offers had zero uses, and
// hit-rate matching had to guess the pack by time. These helpers put the top offers and the
// sealed pack id into the summary itself.

// servedSummaryOfferLimit is how many ranked offers the summary names. The rest stay in
// the pack's capability_offers drilldown.
const servedSummaryOfferLimit = 5

// servedSummaryOfferNameMax bounds one offer name so a long instruction-rule title cannot
// grow the summary.
const servedSummaryOfferNameMax = 80

// servedSummaryOffers renders the top ranked offers, one short line each: name and ref.
func servedSummaryOffers(hits []capgraph.Surfaced) string {
	if len(hits) == 0 {
		return ""
	}
	if len(hits) > servedSummaryOfferLimit {
		hits = hits[:servedSummaryOfferLimit]
	}
	var b strings.Builder
	b.WriteString("\nTop capability offers (offers, not execution):")
	for _, hit := range hits {
		name := strings.Join(strings.Fields(firstNonEmpty(hit.Node.Name, hit.Node.ID)), " ")
		if len(name) > servedSummaryOfferNameMax {
			name = strings.TrimSpace(name[:servedSummaryOfferNameMax-3]) + "..."
		}
		b.WriteString("\n- " + name + " — capability://" + hit.Node.ID)
	}
	b.WriteString("\n")
	return b.String()
}

// withoutHookEventPlaceholderTask clears a task that is only a hook event name. A live
// session start has no prompt yet, so the hook passes its event name ("SessionStart") as
// the task; ranking against it served facts and offers that merely mention sessions
// (2026-11-03: notebook-session tools as the top offers, and Compound facts whose text
// says "SessionStart", in every session start). An event name carries no task words.
func withoutHookEventPlaceholderTask(in Input) Input {
	task := strings.TrimSpace(in.Task)
	for _, name := range envelope.HookNames {
		if strings.EqualFold(task, name) {
			in.Task = ""
			return in
		}
	}
	return in
}

// The sealed pack id is a digest of the pack body, so the summary cannot carry it while it
// is hashed. SealRuntimeMaterialization hashes the summary WITHOUT the marker and then
// stamps the marker, so resealing a sealed pack reproduces the same id (the load path
// re-seals and compares).
const servedPackIDPrefix = " Context pack: "

var servedPackIDMarker = regexp.MustCompile(` Context pack: ctx_[0-9a-f]{16}\.`)

func withoutServedPackID(summary string) string {
	return servedPackIDMarker.ReplaceAllString(summary, "")
}

func withServedPackID(summary, packID string) string {
	if strings.TrimSpace(packID) == "" {
		return summary
	}
	return summary + servedPackIDPrefix + packID + "."
}

// tierZeroLawPrefix leads the Tier-0 constitution line in a full pack summary and in
// the minimal session start, so both surfaces print the same line.
const tierZeroLawPrefix = "Tier-0 constitution: "

// TierZeroLawLine is the one-line Tier-0 constitution summary for cwd, or "" when no
// constitution is found above cwd. The minimal session start serves it without
// building a ContextPack: it is one file read, not a retrieval.
func TierZeroLawLine(cwd string) string {
	laws := universalPrinciplesLawLine(cwd)
	if laws == "" {
		return ""
	}
	return tierZeroLawPrefix + laws
}
