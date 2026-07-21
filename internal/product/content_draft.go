package product

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"compound/internal/candidates"
	"compound/internal/home"
)

// content_draft.go owns the distribution-loop ContentDraft: an observed-class
// candidate that pairs a content-addressed post body with the product, platform,
// voice pack, and deriving evidence it came from — plus an EXPIRY, because stale
// marketing must never post. It lives in the product package because a draft is a
// product-scoped market-control artifact, not a repo summary; persistence and the
// habitat entry are borrowed from the candidates leaf (one home per kind), and the
// body is custodied in the shared content-addressed ArtifactStore (dedup for free).
//
// The door verbs are record / list / park / operationalize / reject. Three
// invariants are load-bearing and each has a red-capable falsifier in the tests:
//   - a rejected draft is RETAINED on disk with state recorded (keep-all-data law);
//     rejection exhaust trains voice later, so the file is never deleted.
//   - an EXPIRED draft REFUSES operationalize with a clear error but stays stored
//     and listable.
//   - nothing self-promotes: record leaves a draft "proposed"; only an explicit
//     verb moves it to parked / operationalized / rejected.

const ContentDraftSchemaVersion = "compound.content_draft.v0.1"

// ContentDraft lifecycle states. Born "proposed"; only a door verb moves it.
const (
	ContentDraftStateProposed        = "proposed"
	ContentDraftStateParked          = "parked"
	ContentDraftStateOperationalized = "operationalized"
	ContentDraftStateRejected        = "rejected"
)

// contentDraftPlatforms is the closed allowlist of distribution targets. The spec
// names them by example ("e.g. x|bluesky|threads"); an observed candidate must not
// carry a junk platform string a downstream transport cannot route, so the set is
// validated, not free-form. Extending distribution to a new platform is one edit
// here — deliberately a decision, not an accident.
var contentDraftPlatforms = map[string]bool{
	"x":       true,
	"bluesky": true,
	"threads": true,
}

// ContentDraftInput is the raw material for a record intake.
type ContentDraftInput struct {
	ProductID    string
	Platform     string
	Body         string // raw post text; stored content-addressed, hashed for BodyRef
	VoicePackRef string
	EvidenceRefs []string
	ExpiresAt    time.Time
}

// ContentDraft is the persisted candidate record under inbox/content-drafts/<id>.json.
type ContentDraft struct {
	SchemaVersion string   `json:"schema_version"`
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	ProductID     string   `json:"product_id"`
	Platform      string   `json:"platform"`
	BodyRef       string   `json:"body_ref"`
	VoicePackRef  string   `json:"voice_pack_ref,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
	State         string   `json:"state"`
	CreatedAt     string   `json:"created_at"`
	ExpiresAt     string   `json:"expires_at"`
	AdjudicatedAt string   `json:"adjudicated_at,omitempty"`
}

// RecordContentDraft intakes a new draft: it custodies the body content-addressed
// (identical bodies dedup to one artifact ref) and mints exactly one observed-class
// candidate in the "proposed" state. It never operationalizes — nothing here
// promotes a draft on its own.
func RecordContentDraft(homePath string, in ContentDraftInput, now time.Time) (ContentDraft, error) {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return ContentDraft{}, fmt.Errorf("COMPOUND_HOME required")
	}
	productID := strings.TrimSpace(in.ProductID)
	if productID == "" {
		return ContentDraft{}, fmt.Errorf("product_id required")
	}
	platform := strings.ToLower(strings.TrimSpace(in.Platform))
	if !contentDraftPlatforms[platform] {
		return ContentDraft{}, fmt.Errorf("platform %q not in allowlist (x|bluesky|threads)", in.Platform)
	}
	if strings.TrimSpace(in.Body) == "" {
		return ContentDraft{}, fmt.Errorf("draft body required")
	}
	if in.ExpiresAt.IsZero() {
		return ContentDraft{}, fmt.Errorf("expiry timestamp required: stale marketing must never post")
	}
	store, err := home.NewArtifactStore(homePath)
	if err != nil {
		return ContentDraft{}, err
	}
	bodyRef, err := store.Put([]byte(in.Body))
	if err != nil {
		return ContentDraft{}, err
	}
	createdAt := now.UTC()
	// Fold the create-nanosecond into the id so two records of the SAME body are
	// distinct drafts (distinct ids) that share one content-addressed body ref.
	id := stableID("cdraft_", productID, platform, bodyRef, strconv.FormatInt(createdAt.UnixNano(), 10))
	draft := ContentDraft{
		SchemaVersion: ContentDraftSchemaVersion,
		ID:            id,
		Kind:          string(candidates.ContentDraft),
		ProductID:     productID,
		Platform:      platform,
		BodyRef:       bodyRef,
		VoicePackRef:  strings.TrimSpace(in.VoicePackRef),
		EvidenceRefs:  compact(in.EvidenceRefs),
		State:         ContentDraftStateProposed,
		CreatedAt:     createdAt.Format(time.RFC3339),
		ExpiresAt:     in.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if err := candidates.WriteCandidate(homePath, candidates.ContentDraft, id, &draft); err != nil {
		return ContentDraft{}, err
	}
	return draft, nil
}

// ReadContentDraft loads one draft by id from its canonical inbox path.
func ReadContentDraft(homePath, id string) (ContentDraft, error) {
	homePath = strings.TrimSpace(homePath)
	id = strings.TrimSpace(id)
	var draft ContentDraft
	if err := candidates.ReadCandidate(homePath, candidates.ContentDraft, id, &draft); err != nil {
		return ContentDraft{}, err
	}
	if draft.ID == "" {
		draft.ID = id
	}
	return draft, nil
}

// ListContentDrafts returns every draft, oldest first, optionally scoped to one
// product. Expired and rejected drafts are INCLUDED — both remain listable by
// design (an expired draft stays stored; a rejected one is retained exhaust).
func ListContentDrafts(homePath, productFilter string) ([]ContentDraft, error) {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return nil, fmt.Errorf("COMPOUND_HOME required")
	}
	dir := filepath.Join(homePath, candidates.InboxDir(candidates.ContentDraft))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	productFilter = strings.TrimSpace(productFilter)
	var drafts []ContentDraft
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		draft, err := ReadContentDraft(homePath, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if productFilter != "" && draft.ProductID != productFilter {
			continue
		}
		drafts = append(drafts, draft)
	}
	sort.Slice(drafts, func(i, j int) bool {
		if drafts[i].CreatedAt == drafts[j].CreatedAt {
			return drafts[i].ID < drafts[j].ID
		}
		return drafts[i].CreatedAt < drafts[j].CreatedAt
	})
	return drafts, nil
}

// ParkContentDraft records the "parked" verdict in place.
func ParkContentDraft(homePath, id string, now time.Time) (ContentDraft, error) {
	return adjudicateContentDraft(homePath, id, ContentDraftStateParked, now, false)
}

// OperationalizeContentDraft records the "operationalized" verdict — but REFUSES,
// without touching the stored record, if the draft has expired.
func OperationalizeContentDraft(homePath, id string, now time.Time) (ContentDraft, error) {
	return adjudicateContentDraft(homePath, id, ContentDraftStateOperationalized, now, true)
}

// RejectContentDraft records the "rejected" verdict in place. The file is NEVER
// deleted (keep-all-data law): rejection exhaust trains voice later.
func RejectContentDraft(homePath, id string, now time.Time) (ContentDraft, error) {
	return adjudicateContentDraft(homePath, id, ContentDraftStateRejected, now, false)
}

// adjudicateContentDraft is the shared read → (expiry gate) → rewrite-in-place path.
// When requireFresh is set it returns an error BEFORE any write if the draft is
// expired, so the stored record and its listability are untouched by a refusal.
func adjudicateContentDraft(homePath, id, newState string, now time.Time, requireFresh bool) (ContentDraft, error) {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return ContentDraft{}, fmt.Errorf("COMPOUND_HOME required")
	}
	draft, err := ReadContentDraft(homePath, id)
	if err != nil {
		return ContentDraft{}, err
	}
	if requireFresh {
		expiresAt, perr := time.Parse(time.RFC3339, draft.ExpiresAt)
		if perr != nil {
			return ContentDraft{}, fmt.Errorf("draft %s has unparseable expiry %q: %w", draft.ID, draft.ExpiresAt, perr)
		}
		if !now.Before(expiresAt) {
			return ContentDraft{}, fmt.Errorf("draft %s expired at %s: stale marketing must never post", draft.ID, draft.ExpiresAt)
		}
	}
	draft.State = newState
	draft.AdjudicatedAt = now.UTC().Format(time.RFC3339)
	if err := candidates.WriteCandidate(homePath, candidates.ContentDraft, draft.ID, &draft); err != nil {
		return ContentDraft{}, err
	}
	return draft, nil
}
