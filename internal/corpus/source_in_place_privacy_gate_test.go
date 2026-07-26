// source_in_place_privacy_gate_test.go covers the explicit opt-in
// SourceInPlacePrivacyGate on LaneConfig. The default posture is off so
// documented legacy behavior — BuildSourceManifest emits source_in_place
// items with privacy=private while the published quickstart runs public
// lanes — keeps working unchanged. When the operator flips the gate on,
// the corpus resolver refuses a privacy=private source_in_place item
// unless the lane posture is privacy=private + local/loopback embedder,
// matching the predicate the owned_snapshot path already uses. The
// refusal happens BEFORE readItemBody is called, which is the load-bearing
// claim this evidence packet makes: zero private source_in_place bytes
// ever reach the rented embedder.
//
// Red-first falsifiers below are written against the unpatched semantics
// and the patched semantics in the same file. Each named scenario's
// "before / after" pair is explicit so a future change to the gate
// surface — widening, narrowing, or relocating — fails the falsifier
// that proves the contract it removed.
//
// Note on layering: a lane with privacy=private + non-local embedder is
// already refused by ValidateLaneConfig before our gate ever runs —
// that is a lane-level invariant. The gate's per-item job is the second
// layer, catching private source_in_place items inside a public lane
// posture that the lane validator admitted. Tests below stay inside the
// gate's jurisdiction so each can pin a single layer's contract.
package corpus

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/sourceobs"
)

// seedSourceInPlaceLaneForGate builds a single-item corpus lane over a
// body_policy=source_in_place manifest so each gate test can pin its
// own lane posture and embedder. Returns the lane, the embedder (so
// tests can assert the embedder was never invoked), and the body path
// (so a test can prove the gate ran before any read by removing it).
// itemPrivacy overrides BuildSourceManifest's default privacy=private
// so tests can pin both halves of the contract.
func seedSourceInPlaceLaneForGate(t *testing.T, homePath, body, family string, itemPrivacy, lanePrivacy, embedderProvider, embedderBaseURL string, gateEnabled bool) (LaneConfig, *fakeEmbedder, string) {
	t.Helper()
	bodyPath := filepath.Join(t.TempDir(), "gate-source.md")
	if err := os.WriteFile(bodyPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write gate source: %v", err)
	}
	manifest, err := sourceobs.BuildSourceManifest(family, []string{bodyPath}, time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("build source manifest: %v", err)
	}
	manifest.Items[0].ID = "item_gate"
	manifest.Items[0].SourceKind = "synthetic_dialogue"
	manifest.Items[0].PageSlug = "Source In Place Gate Fixture"
	manifest.Items[0].CorpusGeneration = "gate-gen"
	manifest.Items[0].Privacy = itemPrivacy
	if _, err := sourceobs.RecordSourceManifest(homePath, manifest); err != nil {
		t.Fatalf("record source manifest: %v", err)
	}
	lane := LaneConfig{
		Lane:         "gate-" + family,
		SourceFamily: family,
		Generation:   "gate-gen",
		Privacy:      lanePrivacy,
		Embedder: EmbedderConfig{
			Provider: embedderProvider,
			Model:    "synthetic-embedder",
			Dims:     3,
			Bits:     "f32",
			BaseURL:  embedderBaseURL,
		},
		Chunk:                    ChunkConfig{Unit: "runes", Size: 256, Overlap: 16, Boundary: "structure"},
		SourceInPlacePrivacyGate: gateEnabled,
	}
	embedder := &fakeEmbedder{identity: lane.Embedder.Identity(), dim: lane.Embedder.Dims}
	return lane, embedder, bodyPath
}

// TestSourceInPlacePrivacyGateDefaultOffAllowsLegacyQuickstart pins the
// load-bearing default-off contract: BuildSourceManifest defaults
// source_in_place items to privacy=private, and the published quickstart
// runs public lanes. With the gate off, indexing MUST succeed so the
// documented legacy behavior is preserved bit-for-bit. Any future change
// that flips the default to engaged would break this falsifier and
// therefore the quickstart — exactly what the audit packet forbids.
func TestSourceInPlacePrivacyGateDefaultOffAllowsLegacyQuickstart(t *testing.T) {
	homePath := t.TempDir()
	lane, embedder, _ := seedSourceInPlaceLaneForGate(t, homePath,
		"User: legacy public-lane private-source-in-place text.",
		"gate_default_off",
		"private", // item privacy — BuildSourceManifest default
		"public",  // lane privacy
		"local",   // embedder provider
		"",        // embedder base url
		false,     // gate off — legacy contract
	)
	if SourceInPlacePrivacyGateEnabled(lane) {
		t.Fatalf("default-off contract broken: SourceInPlacePrivacyGateEnabled = true")
	}
	if _, err := Index(context.Background(), homePath, lane, embedder, IndexOptions{Rebuild: true}); err != nil {
		t.Fatalf("default-off index refused: %v", err)
	}
	if embedder.embedded == 0 {
		t.Fatalf("default-off index embedded zero texts — gate is silently narrowing legacy behavior")
	}
}

// TestSourceInPlacePrivacyGateEnabledRefusesPrivateInPublicLane is the
// load-bearing refusal canary: gate on, item privacy=private, lane
// privacy=public — the index MUST refuse and the embedder MUST NOT be
// called. This is the exact shape that would leak private bytes to a
// rented embedder under a public lane.
func TestSourceInPlacePrivacyGateEnabledRefusesPrivateInPublicLane(t *testing.T) {
	homePath := t.TempDir()
	lane, embedder, _ := seedSourceInPlaceLaneForGate(t, homePath,
		"User: private source_in_place should refuse in public lane.",
		"gate_priv_pub",
		"private", // item privacy
		"public",  // lane privacy
		"local",
		"",
		true,
	)
	_, err := Index(context.Background(), homePath, lane, embedder, IndexOptions{Rebuild: true})
	if err == nil {
		t.Fatalf("indexing accepted privacy=private source_in_place in public lane — leak gate closed-circuit canary broken")
	}
	if !strings.Contains(err.Error(), "privacy=private refuses non-private lane") {
		t.Fatalf("refusal error = %q, want privacy=private refuses non-private lane", err.Error())
	}
	if embedder.embedded != 0 {
		t.Fatalf("embedder received %d texts despite refusal — private bytes leaked to rented embedder", embedder.embedded)
	}
}

// TestSourceInPlacePrivacyGateEnabledRefusesPrivateInPublicRemoteLane
// closes the remote-embedder half of the leak: gate on, lane privacy
// stays PUBLIC (so the lane validator admits the lane), but embedder is
// remote. ProviderIsLocal returns false and laneIsPrivateLocal is also
// false (lane is public), so the gate refuses — proving the per-item
// predicate honors the embedder half independent of the lane-privacy
// half. A successful index here would prove the gate ran with a partial
// predicate.
func TestSourceInPlacePrivacyGateEnabledRefusesPrivateInPublicRemoteLane(t *testing.T) {
	homePath := t.TempDir()
	lane, embedder, _ := seedSourceInPlaceLaneForGate(t, homePath,
		"User: private source_in_place must not reach remote embedder even with public lane.",
		"gate_priv_pub_remote",
		"private",
		"public", // lane stays public so lane validator passes
		"openai",
		"https://api.openai.example/v1",
		true,
	)
	_, err := Index(context.Background(), homePath, lane, embedder, IndexOptions{Rebuild: true})
	if err == nil {
		t.Fatalf("indexing accepted privacy=private source_in_place with remote embedder — gate ignored ProviderIsLocal")
	}
	if !strings.Contains(err.Error(), "privacy=private refuses non-private lane") {
		t.Fatalf("refusal error = %q, want privacy=private refuses non-private lane", err.Error())
	}
	if embedder.embedded != 0 {
		t.Fatalf("embedder received %d texts despite refusal — private bytes leaked to remote", embedder.embedded)
	}
}

// TestSourceInPlacePrivacyGateEnabledAllowsPrivateInPrivateLocalLane
// proves the admitted posture: gate on, lane privacy=private, local
// embedder — the only configuration where a private source_in_place
// item is allowed past the gate. Removing this positive case would
// silently make the gate always-on, which the default-off contract
// forbids.
func TestSourceInPlacePrivacyGateEnabledAllowsPrivateInPrivateLocalLane(t *testing.T) {
	homePath := t.TempDir()
	lane, embedder, _ := seedSourceInPlaceLaneForGate(t, homePath,
		"User: admitted private source_in_place local lane.",
		"gate_priv_local",
		"private",
		"private",
		"local",
		"",
		true,
	)
	report, err := Index(context.Background(), homePath, lane, embedder, IndexOptions{Rebuild: true})
	if err != nil {
		t.Fatalf("admitted posture refused: %v", err)
	}
	if report.Items != 1 || report.TotalChunks == 0 {
		t.Fatalf("admitted report = %+v, want 1 item with chunks", report)
	}
	if embedder.embedded == 0 {
		t.Fatalf("admitted embedder embedded zero texts — gate is too strict")
	}
}

// TestSourceInPlacePrivacyGateEnabledAllowsPublicItemInAnyLane keeps
// the public half of the contract visible: gate on, item privacy=public
// must succeed regardless of lane posture. This is the negative-control
// partner to the refusal canary — if it ever flips to refuse, the gate
// widened silently and the operator's opt-in is meaningless. Lane
// privacy is held public in every subcase so the lane validator admits
// the lane; the gate's per-item check is what we want to prove.
func TestSourceInPlacePrivacyGateEnabledAllowsPublicItemInAnyLane(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		baseURL  string
	}{
		{"public_item_local_embedder", "local", ""},
		{"public_item_remote_embedder", "openai", "https://api.openai.example/v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homePath := t.TempDir()
			lane, embedder, _ := seedSourceInPlaceLaneForGate(t, homePath,
				"User: public source_in_place may ride any lane.",
				"gate_pub_any",
				"public", // item privacy
				"public", // lane privacy
				tc.provider,
				tc.baseURL,
				true,
			)
			if _, err := Index(context.Background(), homePath, lane, embedder, IndexOptions{Rebuild: true}); err != nil {
				t.Fatalf("public item refused in %s lane: %v", tc.name, err)
			}
			if embedder.embedded == 0 {
				t.Fatalf("public item embedded zero texts in %s lane", tc.name)
			}
		})
	}
}

// TestSourceInPlacePrivacyGateEnabledRefusesMalformedPrivacy closes
// the validation half of the gate. The owned_snapshot gate already
// refuses a blank/garbage privacy field; the source_in_place gate must
// do the same so a rented projection can never widen a malformed item
// to "public" by accident. Body must not be read on the way to refusal.
// Inputs are chosen so neither case-insensitive match resolves to
// "public" or "private" — only the malformed-default branch can fire.
func TestSourceInPlacePrivacyGateEnabledRefusesMalformedPrivacy(t *testing.T) {
	for _, privacy := range []string{"", "internal", "PRIVATEr"} {
		t.Run("privacy="+privacy, func(t *testing.T) {
			homePath := t.TempDir()
			lane, embedder, _ := seedSourceInPlaceLaneForGate(t, homePath,
				"User: malformed privacy should fail closed.",
				"gate_malformed_"+strings.ReplaceAll(strings.TrimSpace(privacy), " ", "_"),
				privacy, // item privacy — drives the malformed branch
				"public",
				"local",
				"",
				true,
			)
			_, err := Index(context.Background(), homePath, lane, embedder, IndexOptions{Rebuild: true})
			if err == nil {
				t.Fatalf("indexing accepted malformed privacy %q — gate widened instead of fail-closing", privacy)
			}
			if !strings.Contains(err.Error(), "malformed privacy field") {
				t.Fatalf("refusal error = %q, want malformed privacy field", err.Error())
			}
			if embedder.embedded != 0 {
				t.Fatalf("embedder received %d texts on malformed privacy — gate ran after body read", embedder.embedded)
			}
		})
	}
}

// TestSourceInPlacePrivacyGateEnabledBodyReaderNotInvokedForRefused
// is the strongest falsifier the packet makes: the gate MUST refuse
// before readItemBody touches the on-disk source. The canary deletes
// the source file between seeding and indexing — if the gate ran
// after body read, the index would surface a "source_unavailable" /
// "source missing" error rather than the gate's refusal error. A green
// run here proves the gate is structural, ahead of any body read.
func TestSourceInPlacePrivacyGateEnabledBodyReaderNotInvokedForRefused(t *testing.T) {
	homePath := t.TempDir()
	lane, embedder, bodyPath := seedSourceInPlaceLaneForGate(t, homePath,
		"User: gate must run before body read.",
		"gate_body_read_canary",
		"private",
		"public",
		"local",
		"",
		true,
	)
	if err := os.Remove(bodyPath); err != nil {
		t.Fatalf("remove body to prove gate runs first: %v", err)
	}
	_, err := Index(context.Background(), homePath, lane, embedder, IndexOptions{Rebuild: true})
	if err == nil {
		t.Fatalf("indexing succeeded with body missing — gate cannot be ahead of body read")
	}
	if strings.Contains(err.Error(), "source_unavailable") || strings.Contains(err.Error(), "manifest item") {
		t.Fatalf("gate ran AFTER body read: %v", err)
	}
	if !strings.Contains(err.Error(), "privacy=private refuses non-private lane") {
		t.Fatalf("expected gate refusal error, got %v", err)
	}
	if embedder.embedded != 0 {
		t.Fatalf("embedder received %d texts despite gate refusal", embedder.embedded)
	}
}

// TestSourceInPlacePrivacyGateEnabledEmbedderNotCalledForRefused pins
// the second half of the canary: even with the source file present (so
// body read would succeed), the gate refusal must reach the index
// driver without ever invoking the embedder. The fake embedder records
// `embedded` calls; a non-zero count is unambiguous evidence the gate
// is positioned wrong.
func TestSourceInPlacePrivacyGateEnabledEmbedderNotCalledForRefused(t *testing.T) {
	homePath := t.TempDir()
	lane, embedder, _ := seedSourceInPlaceLaneForGate(t, homePath,
		"User: embedder must never see these bytes.",
		"gate_embedder_canary",
		"private",
		"public",
		"local",
		"",
		true,
	)
	_, err := Index(context.Background(), homePath, lane, embedder, IndexOptions{Rebuild: true})
	if err == nil {
		t.Fatalf("indexing accepted private source_in_place in public lane — gate missing entirely")
	}
	if embedder.calls != 0 || embedder.embedded != 0 {
		t.Fatalf("embedder invoked despite refusal: calls=%d embedded=%d", embedder.calls, embedder.embedded)
	}
}
