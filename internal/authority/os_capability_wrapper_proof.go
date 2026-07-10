package authority

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"compound/internal/capgraph"
)

const (
	osCapabilityModelRouterAuthority = "compound://registries/model-router"
	osCapabilityResolutionSchema     = "compound.capability_resolution.v0.1"
	osCapabilityProbeTask            = "implement a bounded Go authority proof"
	maxOSCapabilityShimBytes         = 16 << 10
	maxOSCapabilityEntrypointBytes   = 2 << 20
	maxOSCapabilityProjectionBytes   = 256 << 10
	maxOSCapabilityCommandBytes      = 32 << 20
)

var (
	osCapabilityShimSourcePattern = regexp.MustCompile(`(?m)^exec\s+"\$\{HOME\}/\.bun/bin/bun"\s+"\$\{HOME\}([^"]+/exec-lane/os\.ts)"\s+"\$@"\s*$`)
	osCapabilityBuildPattern      = regexp.MustCompile(`\(build ([a-f0-9]{7,40})\)`)
)

type osCapabilitySourceSnapshot struct {
	shim       []byte
	entrypoint []byte
	projection []byte
}

type osCapabilityCommandResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

type osCapabilityResolutionProof struct {
	raw                 map[string]any
	canonical           []byte
	routeSourceID       string
	routeSourceRevision string
}

func osAbsorptionOSCapabilityProof(homePath string, row OSAbsorptionMatrixRow) osAbsorptionProofResult {
	if !hasExactString(row.EvidenceRefs, capgraph.CapabilityGraphAuthorityRef) || !hasExactString(row.EvidenceRefs, osCapabilityModelRouterAuthority) {
		return osAbsorptionProofResult{reason: "os capability wrapper requires both Compound authority evidence refs"}
	}
	source, osPath, err := resolveInstalledOSCapabilitySource()
	if err != nil {
		return osAbsorptionProofResult{reason: "os capability source binding: " + err.Error()}
	}
	compoundPath, err := exec.LookPath("compound")
	if err != nil {
		return osAbsorptionProofResult{reason: "resolve installed compound binary: " + err.Error()}
	}

	version, err := runOSCapabilityCommand(compoundPath, []string{"version"}, map[string]string{"COMPOUND_HOME": homePath})
	if err != nil || version.exitCode != 0 {
		return osAbsorptionProofResult{reason: commandFailure("compound version", version, err)}
	}
	buildMatch := osCapabilityBuildPattern.FindSubmatch(version.stdout)
	if len(buildMatch) != 2 {
		return osAbsorptionProofResult{reason: "installed compound binary has no build SHA"}
	}
	compoundBuild := string(buildMatch[1])

	direct, err := runOSCapabilityCommand(compoundPath, []string{"capability", "resolve", "--json", osCapabilityProbeTask}, map[string]string{"COMPOUND_HOME": homePath})
	if err != nil || direct.exitCode != 0 {
		return osAbsorptionProofResult{reason: commandFailure("compound capability resolve", direct, err)}
	}
	directProof, err := parseOSCapabilityResolution(direct.stdout)
	if err != nil {
		return osAbsorptionProofResult{reason: "compound capability resolve: " + err.Error()}
	}

	wrapper, err := runOSCapabilityCommand(osPath, []string{"capability", osCapabilityProbeTask, "--json"}, map[string]string{
		"COMPOUND_HOME":   homePath,
		"OS_COMPOUND_CLI": compoundPath,
		"COMPOUND_CLI":    "",
	})
	if err != nil || wrapper.exitCode != 0 {
		return osAbsorptionProofResult{reason: commandFailure("os capability", wrapper, err)}
	}
	wrapperProof, err := parseOSCapabilityResolution(wrapper.stdout)
	if err != nil {
		return osAbsorptionProofResult{reason: "os capability output: " + err.Error()}
	}
	if !bytes.Equal(directProof.canonical, wrapperProof.canonical) {
		return osAbsorptionProofResult{reason: "capability surface parity failed between Compound resolution and os capability projection"}
	}

	packetRoot := filepath.Join(os.TempDir(), "compound-os-capability-proof-"+digest12(homePath+string(source.projection)))
	if _, err := os.Stat(packetRoot); err == nil {
		return osAbsorptionProofResult{reason: "disabled Compound packet probe target already exists"}
	} else if !os.IsNotExist(err) {
		return osAbsorptionProofResult{reason: "inspect disabled Compound packet probe target: " + err.Error()}
	}
	disabled, err := runOSCapabilityCommand(osPath, []string{"capability", osCapabilityProbeTask, "--json", "--write-packet"}, map[string]string{
		"COMPOUND_HOME":            homePath,
		"OS_COMPOUND_CLI":          "off",
		"COMPOUND_CLI":             "off",
		"OS_CAPABILITY_PACKET_DIR": packetRoot,
	})
	if err != nil {
		return osAbsorptionProofResult{reason: commandFailure("os capability with disabled Compound", disabled, err)}
	}
	if disabled.exitCode == 0 {
		return osAbsorptionProofResult{reason: "disabled Compound must make os capability exit nonzero"}
	}
	if err := validateDisabledOSCapabilityOutput(disabled.stdout); err != nil {
		return osAbsorptionProofResult{reason: "disabled Compound output: " + err.Error()}
	}
	if _, err := os.Stat(packetRoot); err == nil {
		return osAbsorptionProofResult{reason: "disabled Compound wrote an os capability packet"}
	} else if !os.IsNotExist(err) {
		return osAbsorptionProofResult{reason: "inspect disabled Compound packet result: " + err.Error()}
	}

	proofRef := fmt.Sprintf(
		"os_capability_wrapper_authority:shim_sha256:%s;entrypoint_sha256:%s;projection_sha256:%s;compound_build:%s;capability=%s;router=%s@%s/%s",
		digestBytes(source.shim),
		digestBytes(source.entrypoint),
		digestBytes(source.projection),
		compoundBuild,
		capgraph.CapabilityGraphAuthorityRef,
		osCapabilityModelRouterAuthority,
		directProof.routeSourceID,
		directProof.routeSourceRevision,
	)
	return osAbsorptionProofResult{ok: true, proofRef: proofRef}
}

func resolveInstalledOSCapabilitySource() (osCapabilitySourceSnapshot, string, error) {
	osPath, err := exec.LookPath("os")
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	shim, err := readRegularBounded(osPath, maxOSCapabilityShimBytes)
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	match := osCapabilityShimSourcePattern.FindSubmatch(shim)
	if len(match) != 2 {
		return osCapabilitySourceSnapshot{}, "", fmt.Errorf("installed os command is not the canonical HOME-relative shim")
	}
	homePath, err := os.UserHomeDir()
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	relSource := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(string(match[1]), "/")))
	expected := filepath.Join("Projects", "operating-system", "umut-os", "exec-lane", "os.ts")
	if relSource != expected {
		return osCapabilitySourceSnapshot{}, "", fmt.Errorf("installed os shim targets unexpected entrypoint")
	}
	entrypointPath := filepath.Join(homePath, relSource)
	entrypoint, err := readRegularBounded(entrypointPath, maxOSCapabilityEntrypointBytes)
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	entryText := string(entrypoint)
	if !strings.Contains(entryText, `import { runCapabilityProxy } from "./os-capability-proxy";`) ||
		!strings.Contains(entryText, `else if (cmd === "capability") exit = await runCapabilityProxy(rest);`) {
		return osCapabilitySourceSnapshot{}, "", fmt.Errorf("production dispatch does not reach the Compound-only capability proxy")
	}
	projectionPath := filepath.Join(filepath.Dir(entrypointPath), "os-capability-proxy.ts")
	projection, err := readRegularBounded(projectionPath, maxOSCapabilityProjectionBytes)
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	if err := validateOSCapabilityProjectionSource(projection); err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	return osCapabilitySourceSnapshot{shim: shim, entrypoint: entrypoint, projection: projection}, osPath, nil
}

func validateOSCapabilityProjectionSource(source []byte) error {
	text := string(source)
	for _, required := range []string{`runCapabilityProxy`, `"capability", "resolve", "--json"`, `validateResolution`, `result.status !== 0`} {
		if !strings.Contains(text, required) {
			return fmt.Errorf("capability projection missing required boundary %q", required)
		}
	}
	lower := strings.ToLower(text)
	for _, forbidden := range []string{
		"skill-manifest", "skills-inventory", "skills-selection-board", "capability-graph.json",
		"capability-resolver", "model-capabilities.source", "routingdefaults", "free-pool", "loop-map",
		"resolvecapabilityintent", "searchskillcorpus", "searchcapabilitygraph", "searchmodellane", "collectlooptriggerfires",
	} {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("capability projection reads retired source %q", forbidden)
		}
	}
	return nil
}

func readRegularBounded(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("%s is group/world writable", path)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%s exceeds proof size limit", path)
	}
	return os.ReadFile(path)
}

func runOSCapabilityCommand(path string, args []string, overrides map[string]string) (osCapabilityCommandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = mergedCommandEnv(overrides)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := osCapabilityCommandResult{stdout: stdout.Bytes(), stderr: stderr.Bytes(), exitCode: 0}
	if stdout.Len() > maxOSCapabilityCommandBytes || stderr.Len() > maxOSCapabilityCommandBytes {
		return result, fmt.Errorf("command output exceeds proof size limit")
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return result, err
	}
	result.exitCode = exitErr.ExitCode()
	return result, nil
}

func mergedCommandEnv(overrides map[string]string) []string {
	blocked := map[string]bool{}
	for key := range overrides {
		blocked[key] = true
	}
	out := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && !blocked[key] {
			out = append(out, item)
		}
	}
	for key, value := range overrides {
		if value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}

func parseOSCapabilityResolution(body []byte) (osCapabilityResolutionProof, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return osCapabilityResolutionProof{}, err
	}
	if stringField(raw, "schema_version") != osCapabilityResolutionSchema || stringField(raw, "status") != "ok" || stringField(raw, "task") != osCapabilityProbeTask {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid resolution header")
	}
	authorityRefs, ok := stringSlice(raw["authority_refs"])
	if !ok || len(authorityRefs) != 2 || authorityRefs[0] != capgraph.CapabilityGraphAuthorityRef || authorityRefs[1] != osCapabilityModelRouterAuthority {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid authority refs")
	}
	surface, ok := raw["capability_surface"].(map[string]any)
	if !ok || stringField(surface, "authority_ref") != capgraph.CapabilityGraphAuthorityRef || stringField(surface, "status") != "ok" || stringField(surface, "task") != osCapabilityProbeTask {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid capability surface authority")
	}
	offers, ok := surface["offers"].([]any)
	if !ok || len(offers) > 12 {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid bounded capability offers")
	}
	route, ok := raw["route_plan"].(map[string]any)
	if !ok || stringField(route, "authority_ref") != osCapabilityModelRouterAuthority || stringField(route, "capability_class") == "" {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid route plan authority")
	}
	sourceID := stringField(route, "source_authority_id")
	sourceRevision := stringField(route, "source_revision")
	if sourceID == "" || !validSHA256Revision(sourceRevision) {
		return osCapabilityResolutionProof{}, fmt.Errorf("route source authority binding missing")
	}
	chain, ok := route["chain"].([]any)
	if !ok || len(chain) == 0 {
		return osCapabilityResolutionProof{}, fmt.Errorf("route plan has no eligible chain")
	}
	delete(route, "created_at")
	canonical, err := json.Marshal(raw)
	if err != nil {
		return osCapabilityResolutionProof{}, err
	}
	return osCapabilityResolutionProof{raw: raw, canonical: canonical, routeSourceID: sourceID, routeSourceRevision: sourceRevision}, nil
}

func validateDisabledOSCapabilityOutput(body []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	if stringField(raw, "status") != "unavailable" || stringField(raw, "task") != osCapabilityProbeTask {
		return fmt.Errorf("must report unavailable for the probe task")
	}
	for _, forbidden := range []string{"context_packet", "deterministic_resolver", "context_candidates", "capability_graph_matches", "model_lane_matches", "loop_trigger_fires", "source_of_truth"} {
		if _, ok := raw[forbidden]; ok {
			return fmt.Errorf("independent fallback field %q present", forbidden)
		}
	}
	return nil
}

func stringField(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return strings.TrimSpace(text)
}

func stringSlice(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, text)
	}
	return out, true
}

func hasExactString(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func validSHA256Revision(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[len("sha256:"):] {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func commandFailure(name string, result osCapabilityCommandResult, err error) string {
	if err != nil {
		return name + ": " + err.Error()
	}
	detail := strings.TrimSpace(string(result.stderr))
	if detail == "" {
		detail = strings.TrimSpace(string(result.stdout))
	}
	if len(detail) > 300 {
		detail = detail[:300]
	}
	return fmt.Sprintf("%s exited %d: %s", name, result.exitCode, detail)
}
