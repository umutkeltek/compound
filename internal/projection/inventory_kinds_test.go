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
	}
	if len(set.Kinds) != len(want) {
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
			if len(set.Kinds) != 8 {
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
