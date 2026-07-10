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

// StandingGrant is a pre-authorization envelope (D3: humans own irreversible
// decisions by SETTING THE ENVELOPE, not by being paged each time). A matching
// unexpired grant allows only while the action stays inside its scope.
type StandingGrant struct {
	ID                   string   `yaml:"id" json:"id"`
	Capability           string   `yaml:"capability" json:"capability"`
	MutationClass        string   `yaml:"mutation_class" json:"mutation_class"`
	Reversibility        string   `yaml:"reversibility,omitempty" json:"reversibility,omitempty"`
	ReversibilityCeiling string   `yaml:"reversibility_ceiling,omitempty" json:"reversibility_ceiling,omitempty"`
	BlastRadius          string   `yaml:"blast_radius,omitempty" json:"blast_radius,omitempty"`
	TargetPath           string   `yaml:"target_path,omitempty" json:"target_path,omitempty"`
	MaxUSD               *float64 `yaml:"max_usd,omitempty" json:"max_usd,omitempty"`
	MaxTokens            *int     `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	ExpiresAt            string   `yaml:"expires_at" json:"expires_at"` // RFC3339; empty = no expiry
}

// Config is the declarative policy. Shipped as a leak-free built-in default; overridable
// from COMPOUND_HOME/config/policy.yaml. Direct edits while autonomous admission is
// available are unsupported: engage the locked global autonomy halt first, edit and
// validate the projection, then clear the halt through the command.
type Config struct {
	ForbiddenPaths               []string             `yaml:"forbidden_paths" json:"forbidden_paths"`
	ClassOutcome                 map[string]ClassRule `yaml:"class_outcome" json:"class_outcome"`
	HumanReversibilities         []string             `yaml:"human_reversibilities" json:"human_reversibilities"`
	WideBlastRadii               []string             `yaml:"wide_blast_radii" json:"wide_blast_radii"`
	StandingGrants               []StandingGrant      `yaml:"standing_grants" json:"standing_grants"`
	PlacementGuardMode           string               `yaml:"placement_guard_mode,omitempty" json:"placement_guard_mode,omitempty"`
	OperatorAutonomyLevel        string               `yaml:"operator_autonomy_level,omitempty" json:"operator_autonomy_level,omitempty"`
	OperatorAutonomyAuthorityRef string               `yaml:"operator_autonomy_authority_ref,omitempty" json:"operator_autonomy_authority_ref,omitempty"`
	PolicyEvidenceRefs           []string             `yaml:"policy_evidence_refs,omitempty" json:"policy_evidence_refs,omitempty"`
}

// DefaultConfig satisfies the three documented proof cases:
// local_reversible -> allow_with_obligations; forbidden path -> deny;
// public/financial/destructive/security -> hold. Unknown class fails closed to hold.
// EvaluationContext carries live observations that can only raise caution.
// The core policy stays declarative; runtime adapters provide current evidence.
type EvaluationContext struct {
	SourceControl *SourceControlContext  `json:"source_control,omitempty"`
	Placement     *PlacementGuardContext `json:"placement,omitempty"`
}

// SourceControlContext is a read-only git status observation for the action target.
type SourceControlContext struct {
	EntityRef    string   `json:"entity_ref,omitempty"`
	Status       string   `json:"status,omitempty"`
	ChangedPaths int      `json:"changed_paths,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

// PlacementGuardContext is owner-placement evidence for an intended mutation.
// It stages observe -> warn -> block without creating another guard path.
type PlacementGuardContext struct {
	Mode         string   `json:"mode,omitempty"`
	Confidence   string   `json:"confidence,omitempty"`
	OwnerRef     string   `json:"owner_ref,omitempty"`
	TargetRef    string   `json:"target_ref,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	OpenWrite    bool     `json:"open_write,omitempty"`
	Override     bool     `json:"override,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
}

// DefaultConfig returns the built-in conservative policy.
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
		PlacementGuardMode:   "observe",
	}
}

// LoadConfig returns the policy from COMPOUND_HOME/config/policy.yaml if present, else
// the built-in default. An empty homePath always falls back to the default.
func LoadConfig(homePath string) (Config, string, error) {
	if homePath != "" {
		p := filepath.Join(homePath, "config", "policy.yaml")
		if data, err := os.ReadFile(p); err == nil {
			c := DefaultConfig()
			if err := yaml.Unmarshal(data, &c); err != nil {
				return Config{}, p, fmt.Errorf("parse %s: %w", p, err)
			}
			applyPlacementGuardEnv(&c)
			if err := ApplyOperatorStateFromHome(&c, homePath); err != nil {
				return Config{}, p, err
			}
			if err := ApplyStandingGrantsFromHome(&c, homePath); err != nil {
				return Config{}, p, err
			}
			return c, p, nil
		}
	}
	c := DefaultConfig()
	applyPlacementGuardEnv(&c)
	if err := ApplyOperatorStateFromHome(&c, homePath); err != nil {
		return Config{}, "built-in", err
	}
	if err := ApplyStandingGrantsFromHome(&c, homePath); err != nil {
		return Config{}, "built-in", err
	}
	return c, "built-in", nil
}

// Evaluate decides an ActionIntent. Precedence: forbidden path (deny) > scoped
// standing grant (allow) > class baseline and risk/blast escalation.
func Evaluate(ai envelope.ActionIntent, cfg Config, now time.Time) envelope.PolicyDecision {
	return EvaluateWithContext(ai, cfg, EvaluationContext{}, now)
}

// EvaluateWithContext applies live runtime observations after the declarative
// baseline. Context can only raise caution or add evidence; it never relaxes a
// decision.
func EvaluateWithContext(ai envelope.ActionIntent, cfg Config, ctx EvaluationContext, now time.Time) envelope.PolicyDecision {
	pd := evaluateBase(ai, cfg, now)
	if ctx.SourceControl != nil {
		pd = applySourceControlContext(pd, ai, *ctx.SourceControl, now)
	}
	if ctx.Placement != nil {
		pd = applyPlacementGuardContext(pd, ai, cfg, *ctx.Placement, now)
	}
	return pd
}

func evaluateBase(ai envelope.ActionIntent, cfg Config, now time.Time) envelope.PolicyDecision {
	// 1. forbidden path -> deny (hard safety; beats even a standing grant).
	if p := ai.Target.Path; p != "" {
		if pat, ok := matchForbidden(cfg.ForbiddenPaths, p); ok {
			return withPolicyEvidence(mkDecision(ai, "deny",
				[]string{fmt.Sprintf("target path %q matches forbidden pattern %q", p, pat)}, nil, "", "", now), cfg)
		}
	}

	outcome, reasons, obligations := baselineOutcome(ai, cfg)

	// 2. standing grant -> allow only inside its explicit envelope.
	if g, missReasons, ok := matchGrant(cfg.StandingGrants, ai, cfg, now); ok {
		return withPolicyEvidence(mkDecision(ai, "allow",
			[]string{fmt.Sprintf("covered by standing grant %s (capability=%q class=%q)", g.ID, g.Capability, g.MutationClass)},
			nil, g.ID, g.ExpiresAt, now), cfg)
	} else if len(missReasons) > 0 {
		reasons = append(reasons, missReasons...)
	}

	return withPolicyEvidence(mkDecision(ai, outcome, reasons, obligations, "", "", now), cfg)
}

func baselineOutcome(ai envelope.ActionIntent, cfg Config) (string, []string, []string) {
	var reasons []string
	outcome := "hold"
	base, known := cfg.ClassOutcome[ai.MutationClass]
	if known {
		outcome = base.Outcome
		reasons = append(reasons, fmt.Sprintf("baseline for mutation_class %q is %s", ai.MutationClass, base.Outcome))
	} else {
		reasons = append(reasons, fmt.Sprintf("mutation_class %q has no rule; failing closed to hold", ai.MutationClass))
	}
	if level := strings.TrimSpace(cfg.OperatorAutonomyLevel); level != "" && ai.MutationClass != "read" {
		source := strings.TrimSpace(cfg.OperatorAutonomyAuthorityRef)
		if source == "" {
			source = "policy config"
		}
		reasons = append(reasons, fmt.Sprintf("operator autonomy ceiling %s from %s applies", level, source))
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

	return outcome, reasons, obligations
}

func applySourceControlContext(pd envelope.PolicyDecision, ai envelope.ActionIntent, sc SourceControlContext, now time.Time) envelope.PolicyDecision {
	if len(sc.EvidenceRefs) > 0 {
		pd.EvidenceRefs = mergeStrings(pd.EvidenceRefs, sc.EvidenceRefs...)
	}
	if ai.MutationClass == "read" || strings.TrimSpace(sc.Status) != "dirty" {
		return pd
	}
	entity := strings.TrimSpace(sc.EntityRef)
	if entity == "" {
		entity = "target repo"
	}
	reason := fmt.Sprintf("source_control_status dirty for %s changed_paths=%d; target mutation requires clean or explicitly reviewed repo state", entity, sc.ChangedPaths)
	pd.Reasons = append(pd.Reasons, reason)
	if severity[pd.Outcome] < severity["hold"] {
		pd.Outcome = "hold"
		pd.Obligations = nil
		pd.StandingGrantID = ""
		pd.ExpiresAt = ""
		pd.ID = idFor("pol_", ai.ID, "hold", "source-control-dirty", ts(now))
	}
	return pd
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

func withPolicyEvidence(pd envelope.PolicyDecision, cfg Config) envelope.PolicyDecision {
	if len(cfg.PolicyEvidenceRefs) > 0 {
		pd.EvidenceRefs = mergeStrings(pd.EvidenceRefs, cfg.PolicyEvidenceRefs...)
	}
	return pd
}

func applyPlacementGuardContext(pd envelope.PolicyDecision, ai envelope.ActionIntent, cfg Config, pg PlacementGuardContext, now time.Time) envelope.PolicyDecision {
	if len(pg.EvidenceRefs) > 0 {
		pd.EvidenceRefs = mergeStrings(pd.EvidenceRefs, pg.EvidenceRefs...)
	}
	if ai.MutationClass == "read" {
		return pd
	}
	mode, unknownMode := placementGuardMode(pg.Mode, cfg.PlacementGuardMode)
	confidence := strings.ToLower(strings.TrimSpace(pg.Confidence))
	reason := placementGuardReason(mode, confidence, pg)
	if unknownMode != "" {
		pd.Reasons = append(pd.Reasons, reason+fmt.Sprintf("; unknown mode %q, fail-open observe", unknownMode))
		return pd
	}
	switch {
	case pg.Override || placementGuardOverrideEnv():
		pd.Reasons = append(pd.Reasons, reason+"; PLACEMENT_OVERRIDE active, fail-open")
		return pd
	case pg.OpenWrite:
		pd.Reasons = append(pd.Reasons, reason+"; OPEN_WRITE declared, placement guard does not block")
		return pd
	case confidence != "block":
		pd.Reasons = append(pd.Reasons, reason+"; confidence below block threshold")
		return pd
	}
	switch mode {
	case "observe":
		pd.Reasons = append(pd.Reasons, reason+"; observe mode records only")
	case "warn":
		pd.Reasons = append(pd.Reasons, reason+"; warn mode requires explicit receipt")
		if severity[pd.Outcome] < severity["allow_with_obligations"] {
			pd.Outcome = "allow_with_obligations"
			pd.ID = idFor("pol_", ai.ID, "warn", "placement-guard", ts(now))
		}
		if severity[pd.Outcome] <= severity["allow_with_obligations"] {
			pd.Obligations = mergeStrings(pd.Obligations, "record placement warning")
		}
	case "block":
		pd.Reasons = append(pd.Reasons, reason+"; block mode holds confidence:block placement")
		if severity[pd.Outcome] < severity["hold"] {
			pd.Outcome = "hold"
			pd.Obligations = nil
			pd.StandingGrantID = ""
			pd.ExpiresAt = ""
			pd.ID = idFor("pol_", ai.ID, "hold", "placement-guard", ts(now))
		}
	}
	return pd
}

func placementGuardMode(values ...string) (string, string) {
	for _, value := range values {
		mode := strings.ToLower(strings.TrimSpace(value))
		if mode == "" {
			continue
		}
		switch mode {
		case "observe", "warn", "block":
			return mode, ""
		default:
			return "observe", mode
		}
	}
	return "observe", ""
}

func placementGuardReason(mode, confidence string, pg PlacementGuardContext) string {
	owner := strings.TrimSpace(pg.OwnerRef)
	if owner == "" {
		owner = "unknown-owner"
	}
	target := strings.TrimSpace(pg.TargetRef)
	if target == "" {
		target = "target"
	}
	if confidence == "" {
		confidence = "unknown"
	}
	reason := fmt.Sprintf("placement_guard mode=%s confidence=%s owner=%s target=%s", mode, confidence, owner, target)
	if detail := strings.TrimSpace(pg.Reason); detail != "" {
		reason += "; " + detail
	}
	return reason
}

func applyPlacementGuardEnv(c *Config) {
	mode := strings.TrimSpace(os.Getenv("COMPOUND_PLACEMENT_GUARD_MODE"))
	if mode == "" {
		mode = strings.TrimSpace(os.Getenv("PLACEMENT_GUARD_MODE"))
	}
	if mode != "" {
		c.PlacementGuardMode = mode
	}
}

func placementGuardOverrideEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PLACEMENT_OVERRIDE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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

func matchGrant(grants []StandingGrant, ai envelope.ActionIntent, cfg Config, now time.Time) (StandingGrant, []string, bool) {
	var misses []string
	for _, g := range grants {
		if g.Capability != "" && g.Capability != ai.Capability {
			continue
		}
		if g.MutationClass != "" && g.MutationClass != ai.MutationClass {
			continue
		}
		if g.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, g.ExpiresAt); err == nil && now.After(t) {
				misses = append(misses, fmt.Sprintf("standing grant %q expired at %s", grantID(g), g.ExpiresAt))
				continue
			}
		}
		reasons := grantEnvelopeMisses(g, ai, cfg)
		if len(reasons) == 0 {
			return g, nil, true
		}
		misses = append(misses, reasons...)
	}
	return StandingGrant{}, misses, false
}

func grantEnvelopeMisses(g StandingGrant, ai envelope.ActionIntent, cfg Config) []string {
	var reasons []string
	id := grantID(g)
	if g.ReversibilityCeiling != "" {
		if !reversibilityWithinCeiling(ai.Reversibility, g.ReversibilityCeiling) {
			reasons = append(reasons, fmt.Sprintf("standing grant %q covers reversibility up to %q, not %q", id, g.ReversibilityCeiling, ai.Reversibility))
		}
	} else if g.Reversibility != "" && g.Reversibility != ai.Reversibility {
		reasons = append(reasons, fmt.Sprintf("standing grant %q covers reversibility %q, not %q", id, g.Reversibility, ai.Reversibility))
	} else if g.Reversibility == "" && ai.MutationClass != "read" && contains(cfg.HumanReversibilities, ai.Reversibility) {
		reasons = append(reasons, fmt.Sprintf("standing grant %q does not cover reversibility %q", id, ai.Reversibility))
	}
	if g.BlastRadius != "" && g.BlastRadius != ai.BlastRadius {
		reasons = append(reasons, fmt.Sprintf("standing grant %q covers blast_radius %q, not %q", id, g.BlastRadius, ai.BlastRadius))
	} else if g.BlastRadius == "" && ai.MutationClass != "read" && contains(cfg.WideBlastRadii, ai.BlastRadius) {
		reasons = append(reasons, fmt.Sprintf("standing grant %q does not cover blast_radius %q", id, ai.BlastRadius))
	}
	if g.TargetPath != "" {
		if ai.Target.Path == "" {
			reasons = append(reasons, fmt.Sprintf("standing grant %q target path empty outside target_path %q", id, g.TargetPath))
		} else if _, ok := matchForbidden([]string{g.TargetPath}, ai.Target.Path); !ok {
			reasons = append(reasons, fmt.Sprintf("standing grant %q target path %q outside target_path %q", id, ai.Target.Path, g.TargetPath))
		}
	}
	if ai.EstimatedCost.USD != nil {
		if g.MaxUSD == nil {
			reasons = append(reasons, fmt.Sprintf("standing grant %q does not set max_usd for estimated_cost.usd %.2f", id, *ai.EstimatedCost.USD))
		} else if *ai.EstimatedCost.USD > *g.MaxUSD {
			reasons = append(reasons, fmt.Sprintf("standing grant %q estimated_cost.usd %.2f exceeds max_usd %.2f", id, *ai.EstimatedCost.USD, *g.MaxUSD))
		}
	}
	if ai.EstimatedCost.Tokens != nil {
		if g.MaxTokens == nil {
			reasons = append(reasons, fmt.Sprintf("standing grant %q does not set max_tokens for estimated_cost.tokens %d", id, *ai.EstimatedCost.Tokens))
		} else if *ai.EstimatedCost.Tokens > *g.MaxTokens {
			reasons = append(reasons, fmt.Sprintf("standing grant %q estimated_cost.tokens %d exceeds max_tokens %d", id, *ai.EstimatedCost.Tokens, *g.MaxTokens))
		}
	}
	return reasons
}

func grantID(g StandingGrant) string {
	if strings.TrimSpace(g.ID) == "" {
		return "(unnamed)"
	}
	return g.ID
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

func mergeStrings(base []string, extra ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(base)+len(extra))
	for _, v := range base {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	for _, v := range extra {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

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
