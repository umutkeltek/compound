package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

func TestRequireHomeCompatibilityUsesExplicitHomeAroundSubcommand(t *testing.T) {
	ambientHome := filepath.Join(t.TempDir(), "ambient")
	if err := home.Init(ambientHome, "ambient-profile", time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	selectedHome := filepath.Join(t.TempDir(), "selected")
	if err := os.Mkdir(selectedHome, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte("schema_version: " + home.SchemaVersion + "\nkind: compound_server\nhome_kind: selected-profile\n")
	if err := os.WriteFile(filepath.Join(selectedHome, home.ManifestName), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMPOUND_HOME", ambientHome)

	catalog := NewFirstPartyCommandCatalog(map[string]FirstPartyCommandAdmission{
		"home": FirstPartyCommandGoverned,
	}).WithSubcommands("home", map[string]FirstPartyCommandAdmission{
		"private-custody": FirstPartyCommandGoverned,
	})

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "home before subcommand", args: []string{"--home", selectedHome, "private-custody", "repair"}},
		{name: "home after subcommand", args: []string{"private-custody", "repair", "--home", selectedHome}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := catalog.RequireHomeCompatibility("home", tc.args)
			if err == nil || !strings.Contains(err.Error(), "manifest kind") {
				t.Fatalf("RequireHomeCompatibility(%q) error = %v, want selected-home manifest-kind refusal", tc.args, err)
			}
		})
	}
}
