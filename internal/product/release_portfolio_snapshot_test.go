package product

import (
	"reflect"
	"testing"
	"time"

	"compound/internal/mem"
	"compound/internal/sourceobs"
)

func TestReleaseTruthPortfolioReadsOneFreshSnapshot(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	homePath := t.TempDir()
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"product:alpha", "product:beta", "product:gamma"}
	for _, id := range wantIDs {
		bundle := Bundle{
			SchemaVersion: SchemaVersion,
			Product:       Product{ID: id, Name: id, OwnerEntity: "project:fixture", Surfaces: []Surface{{ID: "web", Kind: "web", Ref: "https://product.invalid"}}, Channels: []string{"direct"}, Positioning: "Synthetic portfolio", PrivacyPosture: "local_first", ReleaseIdentity: ReleaseIdentity{Version: "1.0", Build: "10"}},
			EvidenceRefs:  []string{"fixture:release-portfolio"},
		}
		if _, err := Apply(homePath, store, bundle, "fixture://release-portfolio", now); err != nil {
			t.Fatal(err)
		}
		obs := truthRelease(now, "direct", "2.0", "20", "live", time.Hour)
		obs.ProductID = id
		storeTruthRelease(t, homePath, now, obs)
	}
	scans := 0
	read := func(home string, ids []string, at time.Time) (sourceobs.ReleaseObservationsReport, error) {
		scans++
		if !reflect.DeepEqual(sortedUnique(ids), wantIDs) {
			t.Fatalf("release reader scope=%v want %v", ids, wantIDs)
		}
		// Count the actual inventory reader, not a canned result.
		return sourceobs.LoadReleaseObservationsReport(home, ids, at)
	}
	first, err := buildLoopModel(homePath, LoopOptions{Now: now}, read)
	if err != nil {
		t.Fatal(err)
	}
	if scans != 1 {
		t.Fatalf("release inventory scanned %d times for one portfolio; want 1", scans)
	}
	if len(first.Products) != len(wantIDs) {
		t.Fatalf("products=%d", len(first.Products))
	}
	for _, item := range first.Products {
		if item.ReleaseAwareness == nil || item.ReleaseAwareness.Status != "current" || item.ReleaseAwareness.ObservationCount != 1 || !item.ReleaseAwareness.OutdatedContract {
			t.Fatalf("release snapshot not consumed: %+v", item)
		}
	}
	newer := truthRelease(now, "direct", "3.0", "30", "live", 30*time.Minute)
	newer.ProductID = wantIDs[0]
	storeTruthRelease(t, homePath, now, newer)
	second, err := buildLoopModel(homePath, LoopOptions{Now: now}, read)
	if err != nil {
		t.Fatal(err)
	}
	if scans != 2 {
		t.Fatalf("second portfolio read used stale cache or extra scans: %d", scans)
	}
	if second.Products[0].ReleaseAwareness.ChannelPlatforms[0].CurrentVersion != "3.0" {
		t.Fatalf("new intake absent from next snapshot: %+v", second.Products[0])
	}
	if first.Products[0].ReleaseAwareness.ChannelPlatforms[0].CurrentVersion != "2.0" {
		t.Fatal("new intake mutated earlier snapshot")
	}
	filteredScans := 0
	filtered, err := buildLoopModel(homePath, LoopOptions{Now: now, ProductID: wantIDs[1]}, func(home string, ids []string, at time.Time) (sourceobs.ReleaseObservationsReport, error) {
		filteredScans++
		if !reflect.DeepEqual(ids, []string{wantIDs[1]}) {
			t.Fatalf("filtered scope=%v", ids)
		}
		return sourceobs.LoadReleaseObservationsReport(home, ids, at)
	})
	if err != nil {
		t.Fatal(err)
	}
	if filteredScans != 1 || len(filtered.Products) != 1 || filtered.Products[0].ProductID != wantIDs[1] {
		t.Fatalf("filtered projection=%+v scans=%d", filtered, filteredScans)
	}
}

func TestReleaseTruthEmptyPortfolioDoesNotReadInventory(t *testing.T) {
	calls := 0
	got, err := buildLoopModel(t.TempDir(), LoopOptions{}, func(string, []string, time.Time) (sourceobs.ReleaseObservationsReport, error) {
		calls++
		return sourceobs.ReleaseObservationsReport{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || len(got.Products) != 0 {
		t.Fatalf("empty projection read unrelated inventory: calls=%d products=%d", calls, len(got.Products))
	}
}
