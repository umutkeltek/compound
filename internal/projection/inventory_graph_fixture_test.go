package projection

// A synthetic v0.2 observation shaped like a small real estate of systems:
// two products (one spelled two ways), a deployment on a container on a guest
// on a host, a shared database, a hostname, a domain, an account with its
// sign-in identity, secret store and recovery mailbox, a repository, a backup
// target, an outside service, a cycle and one unknown product reference.
// Every name is made up; hosts use .example.

import (
	"encoding/json"
	"testing"
	"time"
)

const (
	gfAccountID = "aws:account:7e3b5a2d4c6e8f0a1b3c5d7e9f2a4c6e8b0d1f3a5c7e9b2d4f6a8c0e2k8m4"
	gfHost      = "examplehv:node:n1"
	gfGuest     = "examplehv:vm:101"
	gfWebCt     = "docker:container:web"
	gfDBCt      = "docker:container:db"
	gfDB        = "pg:datastore:main"
	gfDeploy    = "deploy:site:web"
	gfHostname  = "hostname:app.shop.example"
	gfDomain    = "domain:shop.example"
	gfIdentity  = "identity:ops-login"
	gfVault     = "vault:store:ops"
	gfMailbox   = "mailbox:ops@example.com"
	gfRepo      = "repo:local/shop"
	gfBackup    = "backup:target:nas"
	gfPay       = "external:service:pay"
	gfStoreApp  = "store_app:apple/1001"
	gfShop      = "product:shop-app"
	gfNotes     = "product:notes-app"
	gfNotesAlt  = "product:notes_app"
	gfGhost     = "product:ghost-app"
)

func gfOK(v any) InventoryObservedFact {
	return InventoryObservedFact{Value: v, State: "ok"}
}

func gfLink(rel, to, confidence string) InventoryLink {
	return InventoryLink{Rel: rel, To: to, Confidence: confidence, Evidence: "fixture"}
}

// graphObservation is the fixture as a struct, so a test can edit it.
func graphObservation(at time.Time) InventoryObservation {
	stamp := at.UTC().Format(time.RFC3339)
	src := func(id, provider string) InventorySource {
		return InventorySource{ID: id, Provider: provider, State: "ok", ObservedAt: stamp}
	}
	res := func(id, kind, provider, label, source string, links ...InventoryLink) InventoryObservedResource {
		return InventoryObservedResource{ID: id, Kind: kind, Provider: provider, Label: label, Source: source,
			Places: []string{}, Links: links, Facts: map[string]InventoryObservedFact{}}
	}
	guest := res(gfGuest, "guest", "examplehv", "app-vm", "lab")
	guest.Parent = gfHost
	db := res(gfDB, "datastore", "pg", "main-db", "lab", gfLink("runs_on", gfDBCt, ""), gfLink("backs_up_to", gfBackup, "declared"))
	deploy := res(gfDeploy, "deployment", "sitehost", "web-deploy", "repos",
		gfLink("deployed_on", gfWebCt, "inferred"), gfLink("uses", gfDB, ""))
	deploy.Facts["live"] = gfOK("not_known")
	hostname := res(gfHostname, "hostname", "dnsco", "app.shop.example", "dns",
		gfLink("routes_to", gfWebCt, ""), gfLink("owned_by", gfDomain, ""))
	hostname.Facts["traced"] = gfOK("not_known")
	host := res(gfHost, "host", "examplehv", "lab-node", "lab", gfLink("owned_by", gfAccountID, "declared"))
	identity := res(gfIdentity, "identity", "logins", "ops login", "logins",
		gfLink("secret_kept_in", gfVault, ""), gfLink("recovers_through", gfMailbox, "declared"))
	identity.Facts["sign_in_works"] = gfOK(true)
	identity.Facts["second_factor_on_file"] = gfOK(false)
	vault := res(gfVault, "secret_store", "logins", "ops vault", "logins")
	vault.Facts["store_kind"] = gfOK("vault")
	repo := res(gfRepo, "repo", "git", "shop", "repos")
	repo.Facts["has_remote"] = gfOK(false)
	backup := res(gfBackup, "backup_target", "examplehv", "nas share", "lab")
	backup.Facts["off_host"] = gfOK(false)
	return InventoryObservation{
		SchemaVersion: InventoryObservationSchemaVersion,
		ObservedAt:    stamp,
		Collector:     InventoryCollector{Name: "example-collector", Version: "0.0.2"},
		Sources:       []InventorySource{src("repos", "repo_scan"), src("lab", "examplehv"), src("dns", "dnsco"), src("logins", "declared")},
		Resources: []InventoryObservedResource{
			res(gfShop, "product", "compound", "Shop app", "repos",
				gfLink("deployed_on", gfDeploy, ""), gfLink("uses", gfPay, "declared"),
				gfLink("code_in", gfRepo, ""), gfLink("sold_through", gfStoreApp, "")),
			res(gfNotes, "product", "compound", "Notes app", "repos", gfLink("uses", gfDB, "")),
			res(gfNotesAlt, "product", "compound", "Notes app (old spelling)", "repos", gfLink("uses", gfPay, "inferred")),
			deploy,
			res(gfWebCt, "container", "docker", "web-ct", "lab", gfLink("runs_on", gfGuest, ""), gfLink("calls", gfDBCt, "")),
			res(gfDBCt, "container", "docker", "db-ct", "lab", gfLink("runs_on", gfGuest, ""), gfLink("calls", gfWebCt, "")),
			guest, host, db, hostname,
			res(gfDomain, "domain", "dnsco", "shop.example", "dns", gfLink("owned_by", gfAccountID, "")),
			res(gfAccountID, "account", "aws", "Example account", "logins", gfLink("signs_in_with", gfIdentity, "")),
			identity, vault,
			res(gfMailbox, "mailbox", "mailco", "ops@example.com", "logins"),
			repo, backup,
			res(gfPay, "external_service", "payco", "Pay service", "repos", gfLink("owned_by", gfGhost, "inferred")),
			res(gfStoreApp, "store_app", "apple", "Shop app listing", "repos"),
		},
	}
}

func graphFixture(t *testing.T, at time.Time, edit func(*InventoryObservation)) []byte {
	t.Helper()
	obs := graphObservation(at)
	if edit != nil {
		edit(&obs)
	}
	data, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// gfResolver stands in for Compound's product identity owner (the CLI passes
// the real one): it knows two products, the second under two spellings.
func gfResolver(ref string) (string, bool) {
	switch ref {
	case gfShop:
		return gfShop, true
	case gfNotes, gfNotesAlt:
		return gfNotes, true
	}
	return "", false
}

func graphImported(t *testing.T, at time.Time) string {
	t.Helper()
	home := t.TempDir()
	if _, err := RefreshInventory(home, graphFixture(t, at, nil), InventoryRefreshOptions{}, at); err != nil {
		t.Fatalf("refresh graph fixture: %v", err)
	}
	return home
}

func findGraphResource(obs InventoryObservation, id string) *InventoryObservedResource {
	for i := range obs.Resources {
		if obs.Resources[i].ID == id {
			return &obs.Resources[i]
		}
	}
	return nil
}
