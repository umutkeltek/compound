// consumption.go — the session-end consumption detector (the Reflector's food supply).
//
// A session injects claims (ContextPack.included[] carries claim://<id>). At session end this
// detector answers, deterministically and conservatively, WHICH of those injected claims the
// session actually referenced, so the close receipt can report them as consumed and the memory-
// temperature Reflector is fed a real signal instead of starving.
//
// Design rules (v1, deliberately narrow):
//   - JOIN PATH (single): the pack is resolved from the CLOSING WORK's context_pack_id. That is
//     exactly the id the close receipt stamps into ids.context_pack_id, which is the id the usage
//     projection (capgraph.BuildCapabilityUsage) joins its consumed/ignored attribution on — so
//     detection and attribution share ONE pack, never two. fx.SessionID is carried on the hook
//     event for session provenance; a session-scan fallback is a documented follow-up, not wired,
//     precisely to keep the single join path.
//   - DETECTOR (exact-match, no fuzz): a claim counts CONSUMED iff its exact id token (the part
//     after claim://) appears in the transcript as a BARE reference — a token occurrence that is
//     NOT part of the scheme-qualified injected form "claim://<id>". The injection renderer always
//     writes the scheme-qualified ref, so scheme-qualified occurrences are injection echo and are
//     excluded; a bare <id> elsewhere is a genuine reference. This delimits the injection block by
//     FORM rather than byte-offset — position-independent yet strictly self-echo-safe (no credit
//     for the id merely appearing in the injection). Token boundaries are enforced so one claim id
//     that is a prefix of another cannot cross-credit. No LLM, no token-overlap heuristics.
//   - UNDER-DETECTION IS SAFE: a missed consumption becomes UNKNOWN, never ignored, because the
//     usage projection only cools injected refs from receipts that DECLARED (used_declared). The
//     detector running at all is the declaration; finding nothing is an honest all-ignored signal.
//   - FAIL-OPEN: a missing/unreadable transcript yields (nil, declared=false) and never errors, so
//     session end is never blocked by the detector.
package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"compound/internal/envelope"
)

// claimScheme is the ref scheme injected ledger claims carry in ContextPack.included[].
const claimScheme = "claim://"

// detectSessionConsumption reports the injected claim refs this session consumed and whether a
// used-list was DECLARED. declared is true iff the transcript was present and readable (the
// detector actually ran); a readable transcript that references nothing is an honest all-ignored
// declaration. An absent/unreadable transcript is fail-open: (nil, false), never an error.
func detectSessionConsumption(deps Deps, fx Fixture) (consumed []string, declared bool) {
	path := strings.TrimSpace(fx.TranscriptPath)
	if path == "" {
		return nil, false
	}
	consumed, declared, err := detectTranscriptConsumption(path, injectedClaimRefs(deps, fx))
	if err != nil {
		return nil, false
	}
	return consumed, declared
}

func detectTranscriptConsumption(path string, injectedRefs []string) (consumed []string, declared bool, err error) {
	refs := dedupeClaimRefs(injectedRefs)
	if len(refs) == 0 {
		refs, err = transcriptInjectedClaimRefs(path)
		if err != nil {
			return nil, false, err
		}
		if len(refs) == 0 {
			return nil, true, nil
		}
	}
	consumed, err = matchConsumedClaimsFromFile(path, refs)
	if err != nil {
		return nil, false, err
	}
	return consumed, true, nil
}

func dedupeClaimRefs(refs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

// injectedClaimRefs resolves the injected claim:// refs for the closing work via its
// context_pack_id — the single join path shared with the usage projection. Any resolution miss
// (no store/home, work not found, no pack, unreadable/foreign pack) yields no refs, so the
// detector degrades to a declared-but-empty (honest all-ignored) signal rather than erroring.
func injectedClaimRefs(deps Deps, fx Fixture) []string {
	if deps.Store == nil || strings.TrimSpace(deps.HomePath) == "" {
		return nil
	}
	w, err := deps.Store.Get(fx.WorkID)
	if err != nil {
		return nil
	}
	packID := strings.TrimSpace(w.IDs.ContextPackID)
	if packID == "" {
		return nil
	}
	pack := loadContextPackFile(deps.HomePath, packID)
	if pack == nil {
		return nil
	}
	seen := map[string]bool{}
	var refs []string
	for _, inc := range pack.Included {
		ref := strings.TrimSpace(inc.Ref)
		if !strings.HasPrefix(ref, claimScheme) || seen[ref] {
			continue
		}
		if strings.TrimSpace(strings.TrimPrefix(ref, claimScheme)) == "" {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	return refs
}

// loadContextPackFile decodes a ContextPack artifact by id. It only needs the included refs, so a
// decode failure (torn/foreign file) returns nil rather than bricking the close.
func loadContextPackFile(homePath, packID string) *envelope.ContextPack {
	path := filepath.Join(homePath, "context", "packs", packID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pack envelope.ContextPack
	if json.Unmarshal(data, &pack) != nil {
		return nil
	}
	return &pack
}

// matchConsumedClaims returns the injected refs whose id token appears in the transcript as a
// bare (non-scheme-qualified) reference. Order follows injectedRefs; duplicates are impossible
// because injectedClaimRefs already deduped.
func matchConsumedClaims(injectedRefs []string, transcript string) []string {
	if len(injectedRefs) == 0 || transcript == "" {
		return nil
	}
	var consumed []string
	for _, ref := range injectedRefs {
		id := strings.TrimPrefix(ref, claimScheme)
		if id == "" {
			continue
		}
		if bareClaimReference(transcript, id) {
			consumed = append(consumed, ref)
		}
	}
	return consumed
}

// bareClaimReference reports whether id occurs in s as a whole token that is NOT the tail of the
// scheme-qualified injected form claim://<id>. Exact substring scan with word boundaries; the
// scheme prefix immediately before an occurrence marks it as injection echo (excluded).
func bareClaimReference(s, id string) bool {
	for from := 0; ; {
		i := strings.Index(s[from:], id)
		if i < 0 {
			return false
		}
		idx := from + i
		schemeStart := idx - len(claimScheme)
		injectionEcho := schemeStart >= 0 && s[schemeStart:idx] == claimScheme
		leftBoundary := idx == 0 || !isTokenByte(s[idx-1])
		rightBoundary := idx+len(id) >= len(s) || !isTokenByte(s[idx+len(id)])
		if !injectionEcho && leftBoundary && rightBoundary {
			return true
		}
		from = idx + 1
	}
}

// isTokenByte reports whether b is part of an identifier token ([A-Za-z0-9_]); token boundaries
// keep one claim id that is a prefix of another (claim_ab vs claim_abc) from cross-crediting.
func isTokenByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '_':
		return true
	default:
		return false
	}
}
