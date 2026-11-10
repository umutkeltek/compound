package capgraph

import (
	"strings"
	"testing"
)

func TestSetConnectionSurfaceCredentialBindsInPlace(t *testing.T) {
	home := t.TempDir()
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddConnectionSurface(ConnectionSurface{NodeID: "tool:db", Endpoint: "postgres://db.example"}); err != nil {
		t.Fatal(err)
	}

	// Attach a handle to the existing surface.
	h := CredentialHandle{Ref: "home-secret://db/app", Custodian: "compound", EnvName: "DB_PASSWORD"}
	changed, err := store.SetConnectionSurfaceCredential("tool:db", h)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected the handle to be added")
	}
	surfaces, err := store.ConnectionSurfaces()
	if err != nil {
		t.Fatal(err)
	}
	var got *ConnectionSurface
	for i := range surfaces {
		if surfaces[i].NodeID == "tool:db" {
			got = &surfaces[i]
		}
	}
	if got == nil {
		t.Fatal("surface vanished after bind")
	}
	if got.CredentialHandle != h {
		t.Fatalf("bound handle = %+v, want %+v", got.CredentialHandle, h)
	}
	if got.Endpoint != "postgres://db.example" {
		t.Fatalf("bind clobbered the surface endpoint: %q", got.Endpoint)
	}

	// Idempotent: same handle → no-op.
	changed, err = store.SetConnectionSurfaceCredential("tool:db", h)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("identical re-bind must be a no-op")
	}
}

func TestSetConnectionSurfaceCredentialFailsClosed(t *testing.T) {
	home := t.TempDir()
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	// Unknown surface.
	if _, err := store.SetConnectionSurfaceCredential("tool:nope", CredentialHandle{Ref: "home-secret://s/f", Custodian: "compound", EnvName: "K"}); err == nil {
		t.Fatal("binding an unknown surface must fail closed")
	}
	// Value-shaped handle rejected at the write door.
	if _, err := store.AddConnectionSurface(ConnectionSurface{NodeID: "tool:leaky"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetConnectionSurfaceCredential("tool:leaky", CredentialHandle{Ref: "home-secret://sk-" + strings.Repeat("a", 20), Custodian: "compound", EnvName: "K"}); err == nil {
		t.Fatal("value-shaped handle must be refused at bind")
	}
}
