package routeevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const ExperimentPlanSchema = "compound.route_experiment_plan.v0.1"

type conformanceProfile struct {
	ID              string            `json:"id"`
	Harness         string            `json:"harness"`
	Route           string            `json:"route"`
	RequestedModel  string            `json:"requested_model"`
	RequestedEffort string            `json:"requested_effort"`
	ClientProfile   string            `json:"client_profile,omitempty"`
	ModelAliases    map[string]string `json:"model_aliases"`
}
type conformanceFile struct {
	Schema              string             `json:"schema"`
	ConformanceRevision string             `json:"conformance_revision"`
	Profile             conformanceProfile `json:"profile"`
}

type ExperimentLane struct {
	ProfileID           string `json:"profile_id"`
	Harness             string `json:"harness"`
	RoleAlias           string `json:"role_alias"`
	RequestedAlias      string `json:"requested_alias"`
	ExpectedRoute       string `json:"expected_route"`
	RequestedEffort     string `json:"requested_effort"`
	ClientProfile       string `json:"client_profile,omitempty"`
	ConformanceRevision string `json:"conformance_revision"`
	ObservedProvider    string `json:"observed_provider"`
	ObservedModel       string `json:"observed_model"`
	ObservedAccountID   string `json:"observed_account_id"`
	EvidenceStatus      string `json:"evidence_status"`
}

type ExperimentPlan struct {
	SchemaVersion string           `json:"schema_version"`
	ID            string           `json:"id"`
	RoleAlias     string           `json:"role_alias"`
	ProfilesRoot  string           `json:"profiles_root"`
	Lanes         []ExperimentLane `json:"lanes"`
	Independence  string           `json:"independence_requirement"`
	Execution     string           `json:"execution"`
	Verdict       string           `json:"verdict"`
}

func PlanRouteExperiment(profilesRoot, role string, profileIDs []string) (ExperimentPlan, error) {
	role = strings.TrimSpace(strings.ToLower(role))
	switch role {
	case "default", "opus", "sonnet", "haiku", "subagent":
	default:
		return ExperimentPlan{}, fmt.Errorf("unsupported role alias %q", role)
	}
	if len(profileIDs) < 2 {
		return ExperimentPlan{}, fmt.Errorf("at least two --profile values required")
	}
	seen := map[string]bool{}
	lanes := make([]ExperimentLane, 0, len(profileIDs))
	for _, id := range profileIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return ExperimentPlan{}, fmt.Errorf("profile ids must be non-empty and unique: %q", id)
		}
		seen[id] = true
		body, err := os.ReadFile(filepath.Join(profilesRoot, id+".json"))
		if err != nil {
			return ExperimentPlan{}, fmt.Errorf("read profile %s: %w", id, err)
		}
		var cf conformanceFile
		if err := json.Unmarshal(body, &cf); err != nil {
			return ExperimentPlan{}, fmt.Errorf("decode profile %s: %w", id, err)
		}
		if cf.Profile.ID != id || cf.Schema != "switchback/profile-conformance@1" {
			return ExperimentPlan{}, fmt.Errorf("profile %s conformance identity/schema mismatch", id)
		}
		alias := cf.Profile.ModelAliases[role]
		if role == "default" && alias == "" {
			alias = cf.Profile.RequestedModel
		}
		if alias == "" {
			return ExperimentPlan{}, fmt.Errorf("profile %s does not declare role alias %s", id, role)
		}
		lanes = append(lanes, ExperimentLane{ProfileID: id, Harness: cf.Profile.Harness, RoleAlias: role, RequestedAlias: alias, ExpectedRoute: cf.Profile.Route, RequestedEffort: cf.Profile.RequestedEffort, ClientProfile: cf.Profile.ClientProfile, ConformanceRevision: cf.ConformanceRevision, EvidenceStatus: "unknown_until_route_evidence"})
	}
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].ProfileID < lanes[j].ProfileID })
	seed, _ := json.Marshal(struct {
		Role  string
		Lanes []ExperimentLane
	}{role, lanes})
	sum := sha256.Sum256(seed)
	return ExperimentPlan{SchemaVersion: ExperimentPlanSchema, ID: "rexp_" + hex.EncodeToString(sum[:6]), RoleAlias: role, ProfilesRoot: profilesRoot, Lanes: lanes, Independence: "terminal comparison requires distinct observed provider/model or account identities; profile and role labels do not prove independence", Execution: "execute each lane through governed compound work delegate; import Switchback trace evidence before comparison", Verdict: "plan_only_not_execution_evidence"}, nil
}
