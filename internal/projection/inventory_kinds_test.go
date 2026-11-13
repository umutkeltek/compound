package projection

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryKindsEmbeddedSeedIsValid(t *testing.T) {
	set, err := LoadInventoryKinds("")
	if err != nil {
		t.Fatalf("LoadInventoryKinds: %v", err)
	}
	want := map[string][]string{
		"account": {"identity_ok", "resources", "cost_month_to_date", "credit_left", "credit_ends", "sign_in_expires"},
		"host":    {"cpu_pct", "memory_pct", "disk_pct", "guests_running", "cost_month"},
		"guest":   {"status", "cpu_pct", "memory_pct", "disk_pct"},
		"service": {"status", "health"},
		"domain":  {"plan", "renews_on", "has_search_property"},
		"site":    {"verified", "last_data_day"},
		"app":     {"status"},
		"plan":    {"balance", "window_used_pct", "resets_at", "ends_on", "cost_month"},
		// v0.2 graph kinds
		"product":          {},
		"repo":             {"has_remote", "last_commit"},
		"deployment":       {"live", "last_deploy"},
		"container":        {"status"},
		"hostname":         {"traced"},
		"datastore":        {"backed_up"},
		"external_service": {},
		"store_app":        {"status"},
		"mailbox":          {},
		"alias":            {},
		"identity":         {"sign_in_works", "sign_in_ends", "second_factor_on_file", "recovery_on_file"},
		"secret_store":     {"store_kind"},
		"tunnel":           {},
		"backup_target":    {"last_backup", "off_host"},
	}
	if len(want) != inventorySeedKindCount || len(set.Kinds) != len(want) {
		t.Fatalf("kinds = %v, want %d seed kinds", set.KindIDs(), len(want))
	}
	for id, facts := range want {
		k, ok := set.Kind(id)
		if !ok {
			t.Fatalf("seed kind %q missing", id)
		}
		if len(k.Facts) != len(facts) {
			t.Fatalf("kind %q facts = %+v, want %v", id, k.Facts, facts)
		}
		for _, name := range facts {
			if _, ok := k.Fact(name); !ok {
				t.Fatalf("kind %q missing fact %q", id, name)
			}
		}
	}
	host, _ := set.Kind("host")
	disk, _ := host.Fact("disk_pct")
	if disk.WarnAt == nil || *disk.WarnAt != 80 || disk.WarnWhen != "above" {
		t.Fatalf("host disk_pct threshold = %+v, want above 80", disk)
	}
	guest, _ := set.Kind("guest")
	mem, _ := guest.Fact("memory_pct")
	if mem.WarnAt == nil || *mem.WarnAt != 95 {
		t.Fatalf("guest memory_pct threshold = %+v, want 95", mem)
	}
	if set.OverlayError != "" || set.OverlayPath != "" {
		t.Fatalf("no home must mean no overlay: %+v", set)
	}
	// Boolean warning rules on the four facts the contract names.
	for kind, fact := range map[string]string{"repo": "has_remote", "identity": "sign_in_works", "backup_target": "off_host", "datastore": "backed_up"} {
		k, _ := set.Kind(kind)
		f, _ := k.Fact(fact)
		if f.WarnWhen != "is_false" || f.WarnAt != nil {
			t.Fatalf("%s.%s rule = %+v, want is_false", kind, fact, f)
		}
	}
	// The seed relations and what each means for the walks.
	meanings := map[string]string{
		"runs_on": "depends", "deployed_on": "depends", "uses": "depends", "calls": "depends", "sold_through": "depends",
		"routes_to": "route", "points_to": "route", "serves": "route", "forwards_to": "route",
		"signs_in_with": "identity", "secret_kept_in": "identity", "recovers_through": "identity", "reached_through": "identity",
		"backs_up_to": "backup", "code_in": "other", "built_by": "other", "owned_by": "other", "search_property": "other",
	}
	if len(set.Relations) != len(meanings) {
		t.Fatalf("relations = %+v, want %d", set.Relations, len(meanings))
	}
	for id, meaning := range meanings {
		r, ok := set.Relation(id)
		if !ok || r.Meaning != meaning || r.Title == "" || r.InverseTitle == "" {
			t.Fatalf("relation %s = %+v (present %v), want meaning %s", id, r, ok, meaning)
		}
	}
	if r, ok := set.Relation("parent"); !ok || r.ID != "runs_on" {
		t.Fatalf("parent must count as runs_on: %+v %v", r, ok)
	}
}

// inventorySeedKindCount is the number of kinds the embedded file serves.
const inventorySeedKindCount = 22

func TestInventoryKindsRelationValidationAndOverlay(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"unknown meaning", "relations:\n- {id: feeds, title: feeds, inverse_title: fed by, meaning: sometimes}\n", `unknown meaning "sometimes"`},
		{"duplicate", "relations:\n- {id: runs_on, title: a, inverse_title: b, meaning: depends}\n- {id: runs_on, title: a, inverse_title: b, meaning: depends}\n", "listed twice"},
		{"no inverse", "relations:\n- {id: runs_on, title: a, meaning: depends}\n", "needs title and inverse_title"},
		{"parent reserved", "relations:\n- {id: runs_on, title: a, inverse_title: b, meaning: depends}\n- {id: parent, title: a, inverse_title: b, meaning: depends}\n", "reserved"},
		{"no runs_on", "relations:\n- {id: uses, title: a, inverse_title: b, meaning: depends}\n", "runs_on is missing"},
		{"bad id", "relations:\n- {id: Runs-On, title: a, inverse_title: b, meaning: depends}\n", `id "Runs-On"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := ParseInventoryKinds([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := ValidateInventoryRelations(file.Relations); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := ParseInventoryKinds([]byte("schema_version: compound.system_inventory_kinds.v0.1\nrelations:\n- {id: x, title: x, inverse_title: y, meaning: other}\n")); err == nil {
		t.Fatal("relations in a v0.1 kinds file must be refused")
	}
	if _, err := ParseInventoryKinds([]byte("relations:\n- {id: x, title: x, inverse_title: y, meaning: other, colour: red}\n")); err == nil {
		t.Fatal("unknown relation field must be refused")
	}
	if err := ValidateInventoryKinds([]InventoryKind{{ID: "a", Title: "A", Facts: []InventoryFactSpec{{Name: "x", Title: "X", Group: "use", WarnWhen: "is_false", WarnAt: new(float64)}}}}); err == nil || !strings.Contains(err.Error(), "is_false takes no warn_at") {
		t.Fatalf("is_false with warn_at err = %v", err)
	}

	home := t.TempDir()
	writeProjectionFile(t, filepath.Join(home, "config", "inventory-kinds.yaml"),
		"relations:\n- {id: mirrors, title: mirrors, inverse_title: mirrored by, meaning: backup}\n- {id: uses, title: relies on, meaning: \"\"}\n")
	set, err := LoadInventoryKinds(home)
	if err != nil || set.OverlayError != "" {
		t.Fatalf("overlay relations: %v %q", err, set.OverlayError)
	}
	if r, ok := set.Relation("mirrors"); !ok || r.Meaning != "backup" {
		t.Fatalf("overlay relation not added: %+v", set.Relations)
	}
	if r, _ := set.Relation("uses"); r.Title != "relies on" || r.Meaning != "depends" || r.InverseTitle != "used by" {
		t.Fatalf("overlay must change only the fields it sets: %+v", r)
	}
	broken := t.TempDir()
	writeProjectionFile(t, filepath.Join(broken, "config", "inventory-kinds.yaml"),
		"relations:\n- {id: uses, meaning: often}\n")
	set, _ = LoadInventoryKinds(broken)
	if set.OverlayError == "" || len(set.Relations) != 18 {
		t.Fatalf("a broken relation overlay must serve the embedded relations: %q %d", set.OverlayError, len(set.Relations))
	}
	if got := set.NearestRelation("run_on"); got != "runs_on" {
		t.Fatalf("nearest relation = %q", got)
	}
}

func TestInventoryKindsValidationRejectsBadFiles(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"duplicate kind", "kinds:\n- {id: a, title: A}\n- {id: a, title: A2}\n", `kind "a" is listed twice`},
		{"duplicate fact", "kinds:\n- id: a\n  title: A\n  facts:\n  - {name: x, title: X, group: use}\n  - {name: x, title: X, group: use}\n", `lists fact "x" twice`},
		{"unknown group", "kinds:\n- id: a\n  title: A\n  facts:\n  - {name: x, title: X, group: money}\n", `unknown group "money"`},
		{"bad id", "kinds:\n- {id: Bad-Id, title: A}\n", `id "Bad-Id"`},
		{"bad fact name", "kinds:\n- id: a\n  title: A\n  facts:\n  - {name: 9x, title: X, group: use}\n", `name "9x"`},
		{"warn without direction", "kinds:\n- id: a\n  title: A\n  facts:\n  - {name: x, title: X, group: use, warn_at: 3}\n", "warn_when must be above or below"},
		{"direction without warn", "kinds:\n- id: a\n  title: A\n  facts:\n  - {name: x, title: X, group: use, warn_when: above}\n", "warn_when needs warn_at"},
		{"missing title", "kinds:\n- {id: a}\n", `kind "a" has no title`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := ParseInventoryKinds([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = ValidateInventoryKinds(file.Kinds)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := ParseInventoryKinds([]byte("kinds:\n- {id: a, title: A, colour: red}\n")); err == nil {
		t.Fatal("unknown field must be rejected")
	}
	if _, err := ParseInventoryKinds([]byte("schema_version: other.v9\nkinds: []\n")); err == nil {
		t.Fatal("wrong schema_version must be rejected")
	}
}

func TestInventoryKindsOverlayAddsKindAndFactAndChangesThreshold(t *testing.T) {
	home := t.TempDir()
	writeProjectionFile(t, filepath.Join(home, "config", "inventory-kinds.yaml"), `schema_version: compound.system_inventory_kinds.v0.1
kinds:
  - id: bucket
    title: Storage bucket
    question: How much is stored?
    facts:
      - {name: stored_gb, title: Stored, unit: gb, group: use, warn_at: 500, warn_when: above}
  - id: host
    facts:
      - {name: disk_pct, warn_at: 70}
      - {name: load_avg, title: Load average, unit: "", group: use}
`)
	set, err := LoadInventoryKinds(home)
	if err != nil {
		t.Fatalf("LoadInventoryKinds: %v", err)
	}
	if set.OverlayError != "" {
		t.Fatalf("overlay error = %q", set.OverlayError)
	}
	if set.OverlayPath == "" {
		t.Fatal("overlay path not recorded")
	}
	bucket, ok := set.Kind("bucket")
	if !ok || bucket.Title != "Storage bucket" {
		t.Fatalf("overlay kind missing: %v", set.KindIDs())
	}
	host, _ := set.Kind("host")
	disk, _ := host.Fact("disk_pct")
	if disk.WarnAt == nil || *disk.WarnAt != 70 || disk.WarnWhen != "above" || disk.Title != "Disk used" || disk.Group != InventoryGroupUse {
		t.Fatalf("merged disk_pct = %+v, want threshold 70 with embedded title/group kept", disk)
	}
	if _, ok := host.Fact("load_avg"); !ok {
		t.Fatalf("overlay fact not added: %+v", host.Facts)
	}
	if _, ok := host.Fact("cost_month"); !ok {
		t.Fatal("embedded facts must survive the merge")
	}
	// The embedded seed is not mutated by a merge.
	fresh, _ := LoadInventoryKinds("")
	freshHost, _ := fresh.Kind("host")
	freshDisk, _ := freshHost.Fact("disk_pct")
	if *freshDisk.WarnAt != 80 || len(freshHost.Facts) != 5 {
		t.Fatalf("embedded host changed by merge: %+v", freshHost)
	}
}

func TestInventoryKindsBrokenOverlayServesEmbedded(t *testing.T) {
	for name, body := range map[string]string{
		"unparseable":  "kinds: [ {id: \n",
		"invalid kind": "kinds:\n- {id: host, facts: [{name: disk_pct, group: money}]}\n",
		"wrong schema": "schema_version: compound.other.v9\nkinds: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeProjectionFile(t, filepath.Join(home, "config", "inventory-kinds.yaml"), body)
			set, err := LoadInventoryKinds(home)
			if err != nil {
				t.Fatalf("a broken overlay must not fail the load: %v", err)
			}
			if set.OverlayError == "" {
				t.Fatal("overlay error not reported")
			}
			if len(set.Kinds) != inventorySeedKindCount {
				t.Fatalf("embedded kinds not served: %v", set.KindIDs())
			}
			host, _ := set.Kind("host")
			disk, _ := host.Fact("disk_pct")
			if disk.Group != InventoryGroupUse || *disk.WarnAt != 80 {
				t.Fatalf("embedded host fact replaced by broken overlay: %+v", disk)
			}
		})
	}
}
