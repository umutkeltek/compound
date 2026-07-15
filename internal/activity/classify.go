package activity

import (
	"sort"
	"strings"
)

// categoryRule maps a bundle-id prefix to a category. Longest matching prefix wins, so a specific id
// (com.apple.dt.Xcode) beats a broad vendor prefix (com.apple.). Generic bundle ids only — no
// personal or private-tool identifiers (this file ships in the public core).
type categoryRule struct {
	prefix   string
	category Category
}

// bundleCategoryRules is the deterministic, closed-taxonomy classifier. It is intentionally small and
// generic; an unmapped id resolves to CategoryUnknown at confidence 0 (never guessed).
var bundleCategoryRules = []categoryRule{
	// coding
	{"com.apple.dt.Xcode", CategoryCoding},
	{"com.microsoft.VSCode", CategoryCoding},
	{"com.googlecode.iterm2", CategoryCoding},
	{"com.apple.Terminal", CategoryCoding},
	{"dev.warp.Warp", CategoryCoding},
	{"com.jetbrains.", CategoryCoding},
	{"com.sublimetext.", CategoryCoding},
	// browsing
	{"com.apple.Safari", CategoryBrowsing},
	{"com.google.Chrome", CategoryBrowsing},
	{"org.mozilla.firefox", CategoryBrowsing},
	{"com.microsoft.edgemac", CategoryBrowsing},
	{"company.thebrowser.Browser", CategoryBrowsing},
	{"com.brave.Browser", CategoryBrowsing},
	// design
	{"com.figma.Desktop", CategoryDesign},
	{"com.bohemiancoding.sketch3", CategoryDesign},
	{"com.adobe.", CategoryDesign},
	{"com.pixelmatorteam.", CategoryDesign},
	// comms
	{"com.apple.mail", CategoryComms},
	{"com.tinyspeck.slackmacgap", CategoryComms},
	{"com.hnc.Discord", CategoryComms},
	{"ru.keepcoder.Telegram", CategoryComms},
	{"com.apple.iChat", CategoryComms},
	{"us.zoom.xos", CategoryComms},
	// store-ops
	{"com.apple.iTunesConnect", CategoryStoreOps},
	{"com.apple.AppStoreConnect", CategoryStoreOps},
	{"com.apple.itunesconnect.", CategoryStoreOps},
	// docs
	{"com.apple.iWork.Pages", CategoryDocs},
	{"com.apple.iWork.Numbers", CategoryDocs},
	{"com.apple.iWork.Keynote", CategoryDocs},
	{"com.apple.iWork.", CategoryDocs},
	{"com.microsoft.Word", CategoryDocs},
	{"com.microsoft.Excel", CategoryDocs},
	{"com.microsoft.Powerpoint", CategoryDocs},
	{"md.obsidian", CategoryDocs},
	{"notion.id", CategoryDocs},
	{"com.apple.Notes", CategoryDocs},
	{"com.apple.Preview", CategoryDocs},
	// media
	{"com.apple.Music", CategoryMedia},
	{"com.spotify.client", CategoryMedia},
	{"com.apple.TV", CategoryMedia},
	{"com.apple.QuickTimePlayerX", CategoryMedia},
	// system
	{"com.apple.finder", CategorySystem},
	{"com.apple.systempreferences", CategorySystem},
	{"com.apple.ActivityMonitor", CategorySystem},
	{"com.apple.controlcenter", CategorySystem},
	{"com.apple.loginwindow", CategorySystem},
}

// sortedRules is bundleCategoryRules sorted by descending prefix length so the FIRST match is the
// longest (most specific) match. Built once at init; the classifier is pure and deterministic.
var sortedRules = buildSortedRules()

func buildSortedRules() []categoryRule {
	rules := make([]categoryRule, len(bundleCategoryRules))
	copy(rules, bundleCategoryRules)
	sort.SliceStable(rules, func(i, j int) bool {
		return len(rules[i].prefix) > len(rules[j].prefix)
	})
	return rules
}

// Classify maps a bundle id to a category via longest-prefix match. The second return is confidence:
// 1.0 for a mapped id, 0.0 for an unmapped one (CategoryUnknown). No ML, no guessing.
func Classify(bundleID string) (Category, float64) {
	id := strings.TrimSpace(bundleID)
	if id == "" {
		return CategoryUnknown, 0
	}
	for _, r := range sortedRules {
		if strings.HasPrefix(id, r.prefix) {
			return r.category, 1.0
		}
	}
	return CategoryUnknown, 0
}
