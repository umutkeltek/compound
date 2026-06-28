package policy

import (
	"testing"
	"time"

	"compound/internal/envelope"
)

var fixedNow = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

// mkAction builds a valid ActionIntent for a given class/reversibility/path/blast.
func mkAction(class, reversibility, path, blast string) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            "act_test01",
			CreatedAt:     fixedNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound"},
			IDs:           envelope.IDs{WorkID: "work_test"},
			Authority:     envelope.Authority{SourceClass: "runtime"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "claude_code",
		Capability:    "filesystem.write",
		Action:        "write_file",
		Target:        envelope.ActionTarget{Kind: "file", Path: path},
		MutationClass: class,
		Reversibility: reversibility,
		BlastRadius:   blast,
	}
}

// The three documented P4 proof cases.
func TestProofCases(t *testing.T) {
	cfg := DefaultConfig()
	cases := []struct {
		name, class, rev, path, blast, want string
	}{
		{"local reversible write -> allow_with_obligations", "local_reversible", "easy", "docs/x.md", "repo", "allow_with_obligations"},
		{"forbidden path -> deny", "local_reversible", "easy", "config/secret.key", "repo", "deny"},
		{"public -> hold", "public", "manual", "site/index.html", "public", "hold"},
		{"spend (financial) -> hold", "financial", "manual", "billing/charge", "account", "hold"},
		{"destructive -> hold", "destructive", "hard", "db/table", "repo", "hold"},
		{"security -> hold", "security", "manual", "auth/policy", "repo", "hold"},
		{"read -> allow", "read", "easy", "docs/x.md", "repo", "allow"},
	}
	for _, c := range cases {
		pd := Evaluate(mkAction(c.class, c.rev, c.path, c.blast), cfg, fixedNow)
		if pd.Outcome != c.want {
			t.Errorf("%s: got %q, want %q (reasons: %v)", c.name, pd.Outcome, c.want, pd.Reasons)
		}
		if err := pd.Validate(); err != nil {
			t.Errorf("%s: emitted decision invalid: %v", c.name, err)
		}
		if err := envelope.RoundTrip(&pd); err != nil {
			t.Errorf("%s: round-trip: %v", c.name, err)
		}
		if pd.Outcome == "allow_with_obligations" && len(pd.Obligations) == 0 {
			t.Errorf("%s: allow_with_obligations must carry obligations", c.name)
		}
	}
}

// A "reversible" class with irreversible reversibility escalates to hold (D3 fail-closed).
func TestReversibilityEscalation(t *testing.T) {
	pd := Evaluate(mkAction("local_reversible", "irreversible", "docs/x.md", "repo"), DefaultConfig(), fixedNow)
	if pd.Outcome != "hold" {
		t.Errorf("irreversible local write should escalate to hold, got %q", pd.Outcome)
	}
}

// A wide blast radius escalates a soft class to hold.
func TestBlastRadiusEscalation(t *testing.T) {
	pd := Evaluate(mkAction("local_reversible", "easy", "docs/x.md", "production"), DefaultConfig(), fixedNow)
	if pd.Outcome != "hold" {
		t.Errorf("production blast radius should escalate to hold, got %q", pd.Outcome)
	}
}

// An unknown mutation class fails closed.
func TestUnknownClassFailsClosed(t *testing.T) {
	cfg := DefaultConfig()
	ai := mkAction("local_reversible", "easy", "docs/x.md", "repo")
	ai.MutationClass = "experimental" // not in MutationClasses... still: policy must not silently allow
	pd := Evaluate(ai, cfg, fixedNow)
	if pd.Outcome != "hold" {
		t.Errorf("unknown class should fail closed to hold, got %q", pd.Outcome)
	}
}

// A standing grant allows an otherwise-held action; an expired grant does not.
func TestStandingGrant(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StandingGrants = []StandingGrant{{ID: "grant_deploy", Capability: "deploy.run", MutationClass: "public", ExpiresAt: "2027-01-01T00:00:00Z"}}
	ai := mkAction("public", "manual", "site/index.html", "public")
	ai.Capability = "deploy.run"
	pd := Evaluate(ai, cfg, fixedNow)
	if pd.Outcome != "allow" || pd.StandingGrantID != "grant_deploy" {
		t.Errorf("matching grant should allow with grant id, got outcome=%q grant=%q", pd.Outcome, pd.StandingGrantID)
	}
	// Expired grant -> falls through to baseline (hold).
	cfg.StandingGrants[0].ExpiresAt = "2020-01-01T00:00:00Z"
	if pd := Evaluate(ai, cfg, fixedNow); pd.Outcome != "hold" {
		t.Errorf("expired grant should fall to baseline hold, got %q", pd.Outcome)
	}
}

// Forbidden path beats a standing grant (hard safety wins).
func TestForbiddenBeatsGrant(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StandingGrants = []StandingGrant{{ID: "grant_all", MutationClass: "local_reversible"}}
	ai := mkAction("local_reversible", "easy", "deploy/.env", "repo")
	if pd := Evaluate(ai, cfg, fixedNow); pd.Outcome != "deny" {
		t.Errorf("forbidden path must deny even with a grant, got %q", pd.Outcome)
	}
}

func TestGate(t *testing.T) {
	mk := func(o string) envelope.PolicyDecision { return envelope.PolicyDecision{Outcome: o} }
	if err := Gate(mk("allow")); err != nil {
		t.Errorf("allow should pass the gate: %v", err)
	}
	if err := Gate(mk("allow_with_obligations")); err != nil {
		t.Errorf("allow_with_obligations should pass the gate: %v", err)
	}
	if err := Gate(mk("hold")); err == nil {
		t.Error("hold should be blocked by the gate")
	}
	if err := Gate(mk("deny")); err == nil {
		t.Error("deny should be blocked by the gate")
	}
}

func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		pat, path string
		match     bool
	}{
		{"**/*.key", "secret.key", true},
		{"**/*.key", "a/b/secret.key", true},
		{"**/.env", ".env", true},
		{"**/.env", "config/.env", true},
		{"**/.env", "config/env", false},
		{"**/.git/**", "repo/.git/config", true},
		{"**/*secret*", "my-secrets.txt", true},
		{"**/*.key", "key.txt", false},
	}
	for _, c := range cases {
		_, ok := matchForbidden([]string{c.pat}, c.path)
		if ok != c.match {
			t.Errorf("matchForbidden(%q, %q) = %v, want %v", c.pat, c.path, ok, c.match)
		}
	}
}
