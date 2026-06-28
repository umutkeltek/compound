// Package policy is governance v0: it evaluates an ActionIntent into a PolicyDecision
// in proportion to risk/blast-radius (Law 16; D0 tiers; D3 humans own irreversible).
// The point of the slice is that mutation is GATED, not asked: a mutating adapter must
// hold an allowing PolicyDecision — no model bypasses this by prompt obedience.
// See docs/architecture/contracts/README.md and docs/architecture/transition.md.
package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"compound/internal/envelope"
)

// severity ranks outcomes so escalation only ever raises caution, never lowers it.
var severity = map[string]int{"allow": 0, "allow_with_obligations": 1, "hold": 2, "deny": 3}

// ClassRule is the baseline outcome (and obligations) for a mutation class.
type ClassRule struct {
	Outcome     string   `yaml:"outcome" json:"outcome"`
	Obligations []string `yaml:"obligations" json:"obligations"`
}

// StandingGrant is a pre-authorization envelope (D3: humans own irreversible decisions
// by SETTING THE ENVELOPE, not by being paged each time). A matching unexpired grant allows.
type StandingGrant struct {
	ID            string `yaml:"id" json:"id"`
	Capability    string `yaml:"capability" json:"capability"`
	MutationClass string `yaml:"mutation_class" json:"mutation_class"`
	ExpiresAt     string `yaml:"expires_at" json:"expires_at"` // RFC3339; empty = no expiry
}

// Config is the declarative policy. Shipped as a leak-free built-in default; overridable
// from COMPOUND_HOME/config/policy.yaml.
type Config struct {
	ForbiddenPaths       []string             `yaml:"forbidden_paths" json:"forbidden_paths"`
	ClassOutcome         map[string]ClassRule `yaml:"class_outcome" json:"class_outcome"`
	HumanReversibilities []string             `yaml:"human_reversibilities" json:"human_reversibilities"`
	WideBlastRadii       []string             `yaml:"wide_blast_radii" json:"wide_blast_radii"`
	StandingGrants       []StandingGrant      `yaml:"standing_grants" json:"standing_grants"`
}

// DefaultConfig satisfies the three documented proof cases:
// local_reversible -> allow_with_obligations; forbidden path -> deny;
// public/financial/destructive/security -> hold. Unknown class fails closed to hold.
func DefaultConfig() Config {
	return Config{
		ForbiddenPaths: []string{"**/.git/**", "**/.env", "**/.env.*", "**/*.key", "**/id_rsa*", "**/*secret*"},
		ClassOutcome: map[string]ClassRule{
			"read":                 {Outcome: "allow"},
			"local_reversible":     {Outcome: "allow_with_obligations", Obligations: []string{"emit a Receipt", "record a rollback_plan"}},
			"external_side_effect": {Outcome: "hold"},
			"public":               {Outcome: "hold"},
			"destructive":          {Outcome: "hold"},
			"financial":            {Outcome: "hold"},
			"security":             {Outcome: "hold"},
		},
		HumanReversibilities: []string{"irreversible", "hard", "none"},
		WideBlastRadii:       []string{"production", "public", "account", "fleet", "org", "user-data"},
	}
}

// LoadConfig returns the policy from COMPOUND_HOME/config/policy.yaml if present, else
// the built-in default. An empty homePath always falls back to the default.
func LoadConfig(homePath string) (Config, string, error) {
	if homePath != "" {
		p := filepath.Join(homePath, "config", "policy.yaml")
		if data, err := os.ReadFile(p); err == nil {
			var c Config
			if err := yaml.Unmarshal(data, &c); err != nil {
				return Config{}, p, fmt.Errorf("parse %s: %w", p, err)
			}
			return c, p, nil
		}
	}
	return DefaultConfig(), "built-in", nil
}

// Evaluate decides an ActionIntent. Precedence: forbidden path (deny) > standing grant
// (allow) > class baseline, then risk/blast escalation can only raise caution.
func Evaluate(ai envelope.ActionIntent, cfg Config, now time.Time) envelope.PolicyDecision {
	// 1. forbidden path -> deny (hard safety; beats even a standing grant).
	if p := ai.Target.Path; p != "" {
		if pat, ok := matchForbidden(cfg.ForbiddenPaths, p); ok {
			return mkDecision(ai, "deny",
				[]string{fmt.Sprintf("target path %q matches forbidden pattern %q", p, pat)}, nil, "", "", now)
		}
	}

	// 2. standing grant -> allow (pre-authorized envelope).
	if g, ok := matchGrant(cfg.StandingGrants, ai, now); ok {
		return mkDecision(ai, "allow",
			[]string{fmt.Sprintf("covered by standing grant %s (capability=%q class=%q)", g.ID, g.Capability, g.MutationClass)},
			nil, g.ID, g.ExpiresAt, now)
	}

	// 3. baseline by mutation class; unknown class fails closed to hold.
	var reasons []string
	outcome := "hold"
	base, known := cfg.ClassOutcome[ai.MutationClass]
	if known {
		outcome = base.Outcome
		reasons = append(reasons, fmt.Sprintf("baseline for mutation_class %q is %s", ai.MutationClass, base.Outcome))
	} else {
		reasons = append(reasons, fmt.Sprintf("mutation_class %q has no rule; failing closed to hold", ai.MutationClass))
	}

	// 4. reversibility escalation (D3): a non-read mutation that is operator-owned-reversibility
	//    escalates to at least hold.
	if ai.MutationClass != "read" && contains(cfg.HumanReversibilities, ai.Reversibility) {
		if up := escalate(outcome, "hold"); up != outcome {
			reasons = append(reasons, fmt.Sprintf("reversibility %q is operator-owned; escalated to hold", ai.Reversibility))
			outcome = up
		}
	}

	// 4b. blast-radius escalation: a non-read mutation with a wide blast radius escalates to
	//     at least hold even if its class baseline was softer.
	if ai.MutationClass != "read" && contains(cfg.WideBlastRadii, ai.BlastRadius) {
		if up := escalate(outcome, "hold"); up != outcome {
			reasons = append(reasons, fmt.Sprintf("blast_radius %q is wide; escalated to hold", ai.BlastRadius))
			outcome = up
		}
	}

	// 5. obligations attach only to allow_with_obligations (Validate requires >= 1).
	var obligations []string
	if outcome == "allow_with_obligations" {
		if known {
			obligations = append(obligations, base.Obligations...)
		}
		if len(obligations) == 0 {
			obligations = append(obligations, "emit a Receipt")
		}
	}

	return mkDecision(ai, outcome, reasons, obligations, "", "", now)
}

// Allowed reports whether a decision permits the action to proceed.
func Allowed(pd envelope.PolicyDecision) bool {
	return pd.Outcome == "allow" || pd.Outcome == "allow_with_obligations"
}

// Blocked is the gate error for a non-allowing decision.
type Blocked struct {
	Outcome string
	Reasons []string
}

func (b *Blocked) Error() string {
	if len(b.Reasons) == 0 {
		return "blocked by policy: " + b.Outcome
	}
	return fmt.Sprintf("blocked by policy (%s): %s", b.Outcome, strings.Join(b.Reasons, "; "))
}

// Gate is the enforcement seam an adapter calls before mutating: nil iff the decision
// allows; otherwise a *Blocked error. This is what makes governance enforced, not asked.
func Gate(pd envelope.PolicyDecision) error {
	if Allowed(pd) {
		return nil
	}
	return &Blocked{Outcome: pd.Outcome, Reasons: pd.Reasons}
}

// --- internals ---

func mkDecision(ai envelope.ActionIntent, outcome string, reasons, obligations []string, grantID, expiresAt string, now time.Time) envelope.PolicyDecision {
	return envelope.PolicyDecision{
		Header: envelope.Header{
			SchemaVersion: envelope.SVPolicyDecision,
			ID:            idFor("pol_", ai.ID, outcome, ts(now)),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: ai.IDs.WorkID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       orDefault(ai.Privacy, "private"),
			Risk:          orDefault(ai.Risk, "tier1"),
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		ActionIntentID:  ai.ID,
		Outcome:         outcome,
		Reasons:         orEmpty(reasons),
		Obligations:     orEmpty(obligations),
		StandingGrantID: grantID,
		ExpiresAt:       expiresAt,
	}
}

func matchForbidden(patterns []string, p string) (string, bool) {
	p = filepath.ToSlash(p)
	for _, pat := range patterns {
		re, err := regexp.Compile(globToRegexp(filepath.ToSlash(pat)))
		if err != nil {
			continue
		}
		if re.MatchString(p) {
			return pat, true
		}
	}
	return "", false
}

func matchGrant(grants []StandingGrant, ai envelope.ActionIntent, now time.Time) (StandingGrant, bool) {
	for _, g := range grants {
		if g.Capability != "" && g.Capability != ai.Capability {
			continue
		}
		if g.MutationClass != "" && g.MutationClass != ai.MutationClass {
			continue
		}
		if g.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, g.ExpiresAt); err == nil && now.After(t) {
				continue // expired
			}
		}
		return g, true
	}
	return StandingGrant{}, false
}

// globToRegexp converts a path glob (supporting **, *, ?) to an anchored regexp.
// "**/" matches zero or more leading directories (so "**/*.key" matches "secret.key").
func globToRegexp(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				if i+2 < len(glob) && glob[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 3
				} else {
					b.WriteString(".*")
					i += 2
				}
			} else {
				b.WriteString("[^/]*")
				i++
			}
		case '?':
			b.WriteString("[^/]")
			i++
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	b.WriteString("$")
	return b.String()
}

func escalate(cur, floor string) string {
	if severity[floor] > severity[cur] {
		return floor
	}
	return cur
}

func idFor(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(h[:])[:12]
}

func ts(now time.Time) string { return strconv.FormatInt(now.UTC().UnixNano(), 10) }

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
