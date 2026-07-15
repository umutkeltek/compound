package activity

import "testing"

func TestClassifyLongestPrefixDeterministic(t *testing.T) {
	cases := []struct {
		bundle   string
		wantCat  Category
		wantConf float64
	}{
		{"com.apple.dt.Xcode", CategoryCoding, 1.0},
		{"com.google.Chrome", CategoryBrowsing, 1.0},
		{"com.apple.Safari", CategoryBrowsing, 1.0},
		{"com.figma.Desktop", CategoryDesign, 1.0},
		{"com.apple.mail", CategoryComms, 1.0},
		{"com.tinyspeck.slackmacgap", CategoryComms, 1.0},
		{"com.apple.iWork.Pages", CategoryDocs, 1.0},
		{"com.apple.AppStoreConnect", CategoryStoreOps, 1.0},
		{"com.apple.finder", CategorySystem, 1.0},
		{"com.jetbrains.goland", CategoryCoding, 1.0}, // vendor-prefix rule
		{"com.unmapped.whatever", CategoryUnknown, 0.0},
		{"", CategoryUnknown, 0.0},
	}
	for _, tc := range cases {
		gotCat, gotConf := Classify(tc.bundle)
		if gotCat != tc.wantCat || gotConf != tc.wantConf {
			t.Errorf("Classify(%q) = (%s, %.1f), want (%s, %.1f)", tc.bundle, gotCat, gotConf, tc.wantCat, tc.wantConf)
		}
	}
}

// TestClassifyPrefersSpecificOverVendor proves the longest-prefix rule: an iWork-specific id must not
// be shadowed by the broad "com.apple." family that a naive first-match ordering could hit.
func TestClassifyPrefersSpecificOverVendor(t *testing.T) {
	// com.apple.iWork.Keynote must classify docs even though com.apple.finder (system) shares the
	// com.apple. vendor space — because the classifier matches by prefix specificity, not vendor.
	if cat, _ := Classify("com.apple.iWork.Keynote"); cat != CategoryDocs {
		t.Fatalf("iWork.Keynote = %s, want docs (longest-prefix must win)", cat)
	}
}
