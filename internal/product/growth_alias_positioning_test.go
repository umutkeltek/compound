package product

import (
	"strings"
	"testing"
	"time"

	"compound/internal/mem"
)

// growthAliasFixture registers one product under two spellings: the canonical
// member carries the product's registry record, the alias member carries a
// scratch contract seed. It mirrors the shape a renamed product leaves in the
// ledger: two entity spellings of one product, each holding part of its
// record.
func growthAliasFixture(t *testing.T, home, canonicalID, aliasID, canonicalPositioning, aliasPositioning string, canonicalRows int) {
	t.Helper()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	// The alias member: a scratch contract seed, imported first.
	if _, err := Apply(home, store, registryBundle(aliasID, "Alias Name"), "project.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	// The canonical member: the product's own registry record, imported later.
	b := registryBundle(canonicalID, "Canonical Name")
	b.Product.Positioning = canonicalPositioning
	if _, err := Apply(home, store, b, "canonical.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	// One alias declares the other as an alias, so the two join into one group.
	ents, err := store.Entities()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Slug == canonicalID {
			e.Aliases = append(e.Aliases, strings.TrimPrefix(aliasID, "product:"))
			if _, err := store.RegisterEntity(e, tProduct); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The canonical member is the one the signal ledger names most, so the
	// identity owner picks it.
	var rows []growthRow
	for i := 0; i < canonicalRows; i++ {
		rows = append(rows, growthRow{product: canonicalID, metric: growthSalesFirstDownloads, date: growthDay(i), value: 1})
	}
	writeGrowthSignals(t, home, rows...)
}

// A product's contract facts are read from the member the identity owner
// names. When the canonical member holds the product's record and an alias
// holds a generated contract seed, the canonical record is what the reader
// sees, however the two spellings sort.
func TestGrowthPositioningReadsTheCanonicalMemberAcrossAliases(t *testing.T) {
	home := t.TempDir()
	growthAliasFixture(t, home,
		"product:zebra-app", "product:zebra_app",
		"Zebra App: record one take, save two videos on the device.",
		"Zebra App product source contract (scratch/scratch).",
		9)

	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:zebra-app")
	got := growthAudit(t, app, "strategy_positioning")
	if got.Status != GrowthMeasured || got.Basis != GrowthBasisRecord {
		t.Fatalf("positioning audit = %+v", got)
	}
	if !strings.Contains(got.Summary, "record one take, save two videos") {
		t.Fatalf("canonical positioning not read: %q", got.Summary)
	}
	if strings.Contains(got.Summary, "source contract") {
		t.Fatalf("alias contract seed read over the canonical record: %q", got.Summary)
	}
}

// The other spelling order must give the same answer: the fix cannot depend
// on which alias sorts first.
func TestGrowthPositioningIsIndependentOfAliasSpelling(t *testing.T) {
	home := t.TempDir()
	// The canonical member sorts last, the alias first.
	growthAliasFixture(t, home,
		"product:zebra-app", "product:aardvark-app",
		"Zebra App: record one take, save two videos on the device.",
		"Zebra App product source contract (scratch/scratch).",
		9)

	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:zebra-app")
	got := growthAudit(t, app, "strategy_positioning")
	if !strings.Contains(got.Summary, "record one take, save two videos") {
		t.Fatalf("canonical positioning not read: %q", got.Summary)
	}
	if strings.Contains(got.Summary, "source contract") {
		t.Fatalf("alias contract seed read over the canonical record: %q", got.Summary)
	}
}

// The canonical member's record stands even when it was recorded BEFORE the
// alias member's. Identity authority is not a recency contest.
func TestGrowthPositioningCanonicalBeatsAnOlderAliasRecord(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	// The canonical member is imported first, so it holds the older seq.
	b := registryBundle("product:zebra-app", "Canonical Name")
	b.Product.Positioning = "Zebra App: record one take, save two videos on the device."
	if _, err := Apply(home, store, b, "canonical.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(home, store, registryBundle("product:zebra_app", "Alias Name"), "project.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	ents, err := store.Entities()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Slug == "product:zebra-app" {
			e.Aliases = append(e.Aliases, "zebra_app")
			if _, err := store.RegisterEntity(e, tProduct); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeGrowthSignals(t, home, growthDaily("product:zebra-app", growthSalesFirstDownloads, 8, 0, 1)...)

	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:zebra-app")
	got := growthAudit(t, app, "strategy_positioning")
	if !strings.Contains(got.Summary, "record one take, save two videos") {
		t.Fatalf("canonical positioning not read: %q", got.Summary)
	}
}

// When no member records a positioning, the reader says nothing is on file. It
// must not fall back to a neighbour product or invent an answer.
func TestGrowthPositioningStaysBlindWhenNoMemberRecordsIt(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	b := registryBundle("product:quiet-app", "Quiet App")
	// "unknown" is the recorded answer for a field nobody answered, so the
	// reader must treat it as missing evidence, not as a positioning.
	b.Product.Positioning = EssenceUnknown
	if _, err := Apply(home, store, b, "quiet.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	writeGrowthSignals(t, home, growthDaily("product:quiet-app", growthSalesFirstDownloads, 5, 0, 1)...)

	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:quiet-app")
	got := growthAudit(t, app, "strategy_positioning")
	if got.Status != GrowthBlind || got.Basis != GrowthBasisNone {
		t.Fatalf("positioning audit = %+v, want blind", got)
	}
	if !strings.Contains(got.Summary, "Nothing on file") {
		t.Fatalf("blind positioning summary = %q", got.Summary)
	}
}

// When the canonical member records no positioning of its own but an alias
// does, the alias record is used: the canonical's silence is not a veto.
func TestGrowthPositioningFallsBackToAnAliasWhenCanonicalHasNone(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	// The alias member records the positioning; the canonical member does not.
	alias := registryBundle("product:zebra_app", "Alias Name")
	alias.Product.Positioning = "Zebra App: the alias member's record."
	if _, err := Apply(home, store, alias, "project.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	canon := registryBundle("product:zebra-app", "Canonical Name")
	canon.Product.Positioning = EssenceUnknown
	if _, err := Apply(home, store, canon, "canonical.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	ents, err := store.Entities()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Slug == "product:zebra-app" {
			e.Aliases = append(e.Aliases, "zebra_app")
			if _, err := store.RegisterEntity(e, tProduct); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeGrowthSignals(t, home, growthDaily("product:zebra-app", growthSalesFirstDownloads, 5, 0, 1)...)

	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:zebra-app")
	got := growthAudit(t, app, "strategy_positioning")
	if got.Status != GrowthMeasured || !strings.Contains(got.Summary, "the alias member's record") {
		t.Fatalf("positioning audit = %+v", got)
	}
}

// A product with no alias member at all reads its own record: the fix must
// not disturb the single-member case.
func TestGrowthPositioningSingleMemberUnchanged(t *testing.T) {
	home := t.TempDir()
	applyGrowthBundle(t, home, "product:solo-app", func(p *Product) {
		p.Positioning = "Solo App: one clear sentence."
	})
	writeGrowthSignals(t, home, growthDaily("product:solo-app", growthSalesFirstDownloads, 5, 0, 1)...)

	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:solo-app")
	got := growthAudit(t, app, "strategy_positioning")
	if got.Status != GrowthMeasured || !strings.Contains(got.Summary, "one clear sentence") {
		t.Fatalf("positioning audit = %+v", got)
	}
}

// The canonical member's record stands for every contract fact, not only
// positioning. A product's essence read from the canonical member must not be
// replaced by an alias's.
func TestGrowthEssenceReadsTheCanonicalMemberAcrossAliases(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	alias := registryBundle("product:zebra_app", "Alias Name")
	alias.Product.Essence = Essence{Audience: "Alias audience", Stage: "beta"}
	if _, err := Apply(home, store, alias, "project.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	canon := registryBundle("product:zebra-app", "Canonical Name")
	canon.Product.Essence = Essence{Audience: "Canonical audience", Stage: "live"}
	if _, err := Apply(home, store, canon, "canonical.yaml", tProduct); err != nil {
		t.Fatal(err)
	}
	ents, err := store.Entities()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Slug == "product:zebra-app" {
			e.Aliases = append(e.Aliases, "zebra_app")
			if _, err := store.RegisterEntity(e, tProduct); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeGrowthSignals(t, home, growthDaily("product:zebra-app", growthSalesFirstDownloads, 5, 0, 1)...)

	app := growthApp(t, buildGrowthT(t, home, growthOpts()), "product:zebra-app")
	if app.Essence == nil || app.Essence.Audience != "Canonical audience" {
		t.Fatalf("essence = %+v, want the canonical member's", app.Essence)
	}
}

var _ = time.Now
