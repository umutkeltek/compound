package product

import "strings"

// Profile sources.
const (
	GrowthProfileDeclared = "declared"
	GrowthProfileInferred = "inferred"
	GrowthProfileUnknown  = "unknown"
)

// GrowthProfileValue is one profile dimension of one product and where its
// values came from.
type GrowthProfileValue struct {
	Values []string `json:"values"`
	Source string   `json:"source"`
}

// GrowthDeferral is an owner's "not now" for an area or a group.
type GrowthDeferral struct {
	Area   string `json:"area"`
	Reason string `json:"reason"`
	Until  string `json:"until"`
}

// GrowthProfile is the product profile the projection judges areas against.
type GrowthProfile struct {
	Kinds         GrowthProfileValue `json:"kinds"`
	Audiences     GrowthProfileValue `json:"audiences"`
	RevenueModels GrowthProfileValue `json:"revenue_models"`
	Objectives    GrowthProfileValue `json:"objectives"`
	Stage         GrowthProfileValue `json:"stage"`
	Deferred      []GrowthDeferral   `json:"deferred"`
	Unknown       []string           `json:"unknown"`
}

// dim returns the profile dimension by its catalog name.
func (p GrowthProfile) dim(name string) GrowthProfileValue {
	switch name {
	case "kinds":
		return p.Kinds
	case "audiences":
		return p.Audiences
	case "revenue_models":
		return p.RevenueModels
	case "objectives":
		return p.Objectives
	case "stage":
		return p.Stage
	}
	return GrowthProfileValue{Values: []string{}, Source: GrowthProfileUnknown}
}

// growthProfileDimensionsOut is the dimension order of the profile output,
// including the free-text stage.
var growthProfileDimensionsOut = []string{"kinds", "audiences", "revenue_models", "objectives", "stage"}

// profile builds the product's profile: declared dimensions first, then a
// conservative inference from the data on file, else unknown.
func (c growthCtx) profile(g *growthGroup) GrowthProfile {
	declared := func(values []string) GrowthProfileValue {
		return GrowthProfileValue{Values: append([]string{}, compact(values)...), Source: GrowthProfileDeclared}
	}
	inferred := func(values []string) GrowthProfileValue {
		if len(values) == 0 {
			return GrowthProfileValue{Values: []string{}, Source: GrowthProfileUnknown}
		}
		return GrowthProfileValue{Values: values, Source: GrowthProfileInferred}
	}
	p := GrowthProfile{Deferred: []GrowthDeferral{}, Unknown: []string{}}
	if g.profileDeclared["kinds"] {
		p.Kinds = declared(g.profile.Kinds)
	} else {
		p.Kinds = inferred(c.inferKinds(g))
	}
	if g.profileDeclared["audiences"] {
		p.Audiences = declared(g.profile.Audiences)
	} else {
		p.Audiences = inferred(nil) // audiences are never inferred
	}
	if g.profileDeclared["revenue_models"] {
		p.RevenueModels = declared(g.profile.RevenueModels)
	} else {
		var models []string
		if g.subscriptionIDs || g.hasMetric("*subscription*") {
			models = append(models, "subscription")
		}
		p.RevenueModels = inferred(models)
	}
	if g.profileDeclared["objectives"] {
		p.Objectives = declared(g.profile.Objectives)
	} else {
		var objectives []string
		if g.hasProceeds() || (g.essenceRec != nil && g.essenceRec.Goal.IncomeTargetUSDMonth > 0) {
			objectives = append(objectives, "revenue")
		}
		p.Objectives = inferred(objectives)
	}
	if g.essenceRec != nil && strings.TrimSpace(g.essenceRec.Stage) != "" && g.essenceRec.Stage != EssenceUnknown {
		p.Stage = GrowthProfileValue{Values: []string{g.essenceRec.Stage}, Source: GrowthProfileDeclared}
	} else {
		p.Stage = GrowthProfileValue{Values: []string{}, Source: GrowthProfileUnknown}
	}
	for _, d := range g.profile.Deferred {
		p.Deferred = append(p.Deferred, GrowthDeferral{Area: d.Area, Reason: d.Reason, Until: d.Until})
	}
	for _, name := range growthProfileDimensionsOut {
		if p.dim(name).Source == GrowthProfileUnknown {
			p.Unknown = append(p.Unknown, name)
		}
	}
	return p
}

// inferKinds reads the product kind from the data on file. Several kinds may
// be true at once.
func (c growthCtx) inferKinds(g *growthGroup) []string {
	var kinds []string
	if g.hasMetric("app_store_*", "app.*", growthAppleAds) || strings.Contains(g.surfaces, "app") {
		kinds = append(kinds, "ios_app")
	}
	if g.hasMetric("gsc_*", "ga4_*") || strings.Contains(g.surfaces, "web") || strings.Contains(g.surfaces, "site") {
		kinds = append(kinds, "website")
	}
	return kinds
}

// hasMetric reports whether any periodic or range metric matches a glob.
func (g *growthGroup) hasMetric(globs ...string) bool {
	for metric := range g.daily {
		if growthMatchAny(globs, metric) {
			return true
		}
	}
	for metric := range g.ranges {
		if growthMatchAny(globs, metric) {
			return true
		}
	}
	return false
}

// hasProceeds reports proceeds above zero on any date.
func (g *growthGroup) hasProceeds() bool {
	metric := growthKpiMetric["proceeds_usd"]
	for _, v := range g.daily[metric] {
		if v > 0 {
			return true
		}
	}
	for _, pv := range g.ranges[metric] {
		if pv.v > 0 {
			return true
		}
	}
	return false
}

// profileDimensions lists the catalog's allowed profile values.
func (c *growthAreaCatalog) profileDimensions() map[string][]string {
	out := map[string][]string{}
	for _, dim := range growthProfileDimensions {
		out[dim] = append([]string{}, c.Profile[dim]...)
	}
	return out
}

// firstKind is the app's single kind field: its first kind, or unknown.
func (p GrowthProfile) firstKind() string {
	if len(p.Kinds.Values) > 0 {
		return p.Kinds.Values[0]
	}
	return "unknown"
}

// isStoreApp reports whether store-only funnel stages can apply: the kind is
// unknown or includes an iPhone app.
func (p GrowthProfile) isStoreApp() bool {
	return len(p.Kinds.Values) == 0 || containsString(p.Kinds.Values, "ios_app")
}
