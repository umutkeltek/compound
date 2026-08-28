package hook

import (
	"strings"

	"compound/internal/work"
)

// ConsumptionForClose reports which injected claim refs a transcript actually consumed, for the
// governed `compound work close` path, and whether a used-list was DECLARED.
//
// WHY THIS EXISTS. Consumption was only ever detected on the SessionEnd hook path, because that is
// the only path that owns a transcript (see detectSessionConsumption). `compound work close` — the
// ceremony that closes the work that actually matters — carried no transcript, so it could not
// declare consumption BY CONSTRUCTION. Its only honesty signal was a hand-typed `--used` flag that
// nothing routinely passes, so every governed close landed used_declared=false and every injected
// ref it was served stayed UNKNOWN forever.
//
// The visible symptom is an injection meter whose served count climbs while its consumed count
// stays pinned near zero — not because refs are being ignored, but because the closing ceremony is
// structurally mute. Generation runs on the governed path; consumption could only ever be recorded
// on the ungoverned one. This function is the missing edge between them.
//
// It is a DELIBERATELY THIN wrapper over detectSessionConsumption. Hook-close and governed-close
// must score identically — a ref counted CONSUMED by one and UNKNOWN by the other would make the
// usage projection unreadable — and the only way to guarantee that is to share one detector rather
// than maintain two that agree today and drift tomorrow. Do not reimplement the matching here; if
// the matching rules change, they change once, in consumption.go, for both callers.
//
// Fail-open, exactly like the hook path: an empty, absent, or unreadable transcript returns
// (nil, false) — honestly UNKNOWN, never a false "ignored", and never an error that blocks a close.
// A readable transcript that references nothing returns (nil, true): an honest all-ignored
// declaration, which is a real and useful signal.
func ConsumptionForClose(store *work.Store, homePath, workID, transcriptPath string) (consumed []string, declared bool) {
	if store == nil {
		return nil, false
	}
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(transcriptPath) == "" {
		return nil, false
	}
	return detectSessionConsumption(
		Deps{Store: store, HomePath: homePath},
		Fixture{WorkID: workID, TranscriptPath: transcriptPath},
	)
}
