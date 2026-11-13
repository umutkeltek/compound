package product

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StoreAppsMapRel is the operator's private one-to-one map from store app id
// to product, relative to the Compound home. It is read when present and
// never written.
const StoreAppsMapRel = "private/asc-products.json"

// Identity sources, strongest first.
const (
	storeIDReleaseIdentity = "release_identity"
	storeIDPrivateMap      = "private_map"
	storeIDLedgerRef       = "ledger_reference"
	storeIDRegistry        = "registry_identifier"
)

// storeAppClaim is one source tying a store app id to a product group. group
// is nil when the source names a product Compound does not know.
type storeAppClaim struct {
	group   *growthGroup
	product string
	source  string
}

// storeAppIdentity collects every claim that ties a store app id to a product
// group: the release identity on the product contract, the private map, a
// ledger row citing "asc:app:<id>", and an all-digit registry identifier.
// resolve may add a group (a ledger product id is a product); lookup never
// does (the private map may name a product nobody registered).
func storeAppIdentity(homePath string, signals []Signal, groups []*growthGroup, resolve, lookup func(string) *growthGroup) (map[string][]storeAppClaim, error) {
	out := map[string][]storeAppClaim{}
	add := func(id string, c storeAppClaim) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		for _, have := range out[id] {
			if have.group == c.group && have.source == c.source && have.product == c.product {
				return
			}
		}
		out[id] = append(out[id], c)
	}
	for _, g := range groups {
		for _, id := range g.releaseAppIDs {
			add(id, storeAppClaim{group: g, source: storeIDReleaseIdentity})
		}
	}
	mapped, err := loadStoreAppsMap(homePath)
	if err != nil {
		return nil, err
	}
	for _, m := range mapped {
		add(m.appID, storeAppClaim{group: lookup(m.product), product: m.product, source: storeIDPrivateMap})
	}
	for _, s := range signals {
		for _, ref := range s.EvidenceRefs {
			m := growthASCAppRefRE.FindStringSubmatch(strings.TrimSpace(ref))
			if m == nil {
				continue
			}
			if g := resolve(s.ProductID); g != nil {
				add(m[1], storeAppClaim{group: g, source: storeIDLedgerRef})
			}
		}
	}
	for _, g := range groups {
		for _, token := range growthSortedKeys(g.tokens) {
			if token != "" && strings.Trim(token, "0123456789") == "" {
				add(token, storeAppClaim{group: g, source: storeIDRegistry})
			}
		}
	}
	return out, nil
}

// storeAppGroups maps each app id to the set of known groups that claim it.
// Readers use an id only when the set has exactly one group.
func storeAppGroups(identity map[string][]storeAppClaim) map[string]map[*growthGroup]bool {
	out := map[string]map[*growthGroup]bool{}
	for id, claims := range identity {
		for _, c := range claims {
			if c.group == nil {
				continue
			}
			if out[id] == nil {
				out[id] = map[*growthGroup]bool{}
			}
			out[id][c.group] = true
		}
	}
	return out
}

// linkedAppIDs lists, in order, the app ids that map to exactly this group.
func linkedAppIDs(identity map[string][]storeAppClaim, g *growthGroup) []string {
	var out []string
	for id, set := range storeAppGroups(identity) {
		if len(set) == 1 && set[g] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

type storeAppsMapEntry struct{ appID, product string }

// loadStoreAppsMap reads the private app map. It accepts {"apps": [...]} or a
// bare list; each entry names the app id as asc_app_id, app_id or
// apple_identifier (string or number) and the product as product_id or
// product. A missing file is normal; entries without both are skipped.
func loadStoreAppsMap(homePath string) ([]storeAppsMapEntry, error) {
	raw, err := os.ReadFile(filepath.Join(homePath, filepath.FromSlash(StoreAppsMapRel)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []map[string]json.RawMessage
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, fmt.Errorf("%s: %w", StoreAppsMapRel, err)
		}
	} else {
		var doc struct {
			Apps []map[string]json.RawMessage `json:"apps"`
		}
		if err := json.Unmarshal(trimmed, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", StoreAppsMapRel, err)
		}
		list = doc.Apps
	}
	var out []storeAppsMapEntry
	for _, e := range list {
		appID := jsonScalar(e, "asc_app_id", "app_id", "apple_identifier")
		product := jsonScalar(e, "product_id", "product")
		if appID != "" && product != "" {
			out = append(out, storeAppsMapEntry{appID: appID, product: product})
		}
	}
	return out, nil
}

// jsonScalar returns the first of keys that holds a string or a number, as
// text.
func jsonScalar(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		var n json.Number
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if dec.Decode(&n) == nil && n.String() != "" {
			return n.String()
		}
	}
	return ""
}

// StoreAppLink is one store app id and the product it belongs to.
type StoreAppLink struct {
	AppID string `json:"app_id"`
	// Status is linked (exactly one product), no_product (no source names
	// a known product) or conflict (two or more products claim it).
	Status    string   `json:"status"`
	ProductID string   `json:"product_id"`
	Sources   []string `json:"sources"`
	Claims    []string `json:"claims"`
	SeenIn    []string `json:"seen_in"`
}

// StoreReportIdentity says which product each store app id belongs to.
type StoreReportIdentity struct {
	Apps               []StoreAppLink `json:"apps"`
	Linked             int            `json:"linked"`
	AppsWithoutProduct []string       `json:"apps_without_product"`
	Conflicts          []string       `json:"conflicts"`
	ProductsWithoutApp []string       `json:"products_without_app"`
}
