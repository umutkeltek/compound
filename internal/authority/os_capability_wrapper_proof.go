package authority

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"compound/internal/capgraph"
	"compound/internal/routeevidence"
)

const (
	osCapabilityResolutionSchema    = "compound.capability_resolution.v0.1"
	osCapabilityProbeTask           = "implement a bounded Go authority proof"
	osCapabilitySecondaryProbeTask  = "review an authentication diff for security vulnerabilities without editing files"
	maxOSCapabilityOffers           = 12
	maxOSCapabilityRouteLanes       = 16
	maxOSCapabilityShimBytes        = 16 << 10
	maxOSCapabilityEntrypointBytes  = 2 << 20
	maxOSCapabilityProjectionBytes  = 256 << 10
	maxOSCapabilityCommandBytes     = 32 << 20
	maxOSCapabilityBinaryBytes      = 256 << 20
	osCapabilityShimDigestRef       = "os_capability_shim_sha256:"
	osCapabilityEntrypointDigestRef = "os_capability_entrypoint_sha256:"
	osCapabilityProjectionDigestRef = "os_capability_proxy_sha256:"
)

var (
	osCapabilityShimSourcePattern = regexp.MustCompile(`(?m)^exec\s+"\$\{HOME\}/\.bun/bin/bun"\s+"\$\{HOME\}([^"]+/exec-lane/os\.ts)"\s+"\$@"\s*$`)
	osCapabilityBuildPattern      = regexp.MustCompile(`\(build ([a-f0-9]{7,40})\)`)
	osCapabilityDispatchPattern   = regexp.MustCompile(`\bcmd\s*===\s*["']capability["']`)
	osCapabilityRuntimeIDPattern  = regexp.MustCompile(`^mrcsrc_[a-f0-9]{12}$`)
	errOSCapabilityOutputLimit    = errors.New("command output exceeds proof size limit")
)

type osCapabilitySourceSnapshot struct {
	shimPath       string
	entrypointPath string
	projectionPath string
	shim           []byte
	entrypoint     []byte
	projection     []byte
}

type osCapabilityBinarySnapshot struct {
	path   string
	digest string
}

type osCapabilityExpectedSourceDigests struct {
	shim       string
	entrypoint string
	projection string
}

type osCapabilityCommandResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

type osCapabilityResolutionProof struct {
	raw                 map[string]any
	canonical           []byte
	capabilityClass     string
	classifierRevision  string
	graphRevision       string
	routeSourceID       string
	routeSourceRevision string
}

type osCapabilityOutputBudget struct {
	mu        sync.Mutex
	remaining int64
	exceeded  bool
	cancel    context.CancelFunc
}

type osCapabilityBoundedBuffer struct {
	budget *osCapabilityOutputBudget
	buffer bytes.Buffer
}

type osCapabilityProbe struct {
	task            string
	capabilityClass string
}

func osAbsorptionOSCapabilityProof(homePath string, row OSAbsorptionMatrixRow) osAbsorptionProofResult {
	for _, required := range []string{
		routeevidence.CapabilityTaskClassifierAuthorityRef,
		capgraph.CapabilityGraphAuthorityRef,
		routeevidence.ModelCapabilitiesAuthorityRef,
	} {
		if !hasExactString(row.EvidenceRefs, required) {
			return osAbsorptionProofResult{reason: "os capability wrapper requires classifier, capability graph, and model-router authority evidence refs"}
		}
	}
	expectedSources, err := expectedOSCapabilitySourceDigests(row.EvidenceRefs)
	if err != nil {
		return osAbsorptionProofResult{reason: "os capability expected source binding: " + err.Error()}
	}
	source, osPath, err := resolveInstalledOSCapabilitySource()
	if err != nil {
		return osAbsorptionProofResult{reason: "os capability source binding: " + err.Error()}
	}
	if err := source.matchesExpected(expectedSources); err != nil {
		return osAbsorptionProofResult{reason: "os capability expected source binding: " + err.Error()}
	}
	compoundBinary, err := resolveOSCapabilityBinary()
	if err != nil {
		return osAbsorptionProofResult{reason: "resolve installed compound binary: " + err.Error()}
	}

	version, err := runOSCapabilityCommand(compoundBinary.path, []string{"version"}, map[string]string{"COMPOUND_HOME": homePath})
	if err != nil || version.exitCode != 0 {
		return osAbsorptionProofResult{reason: commandFailure("compound version", version, err)}
	}
	compoundBuild, err := parseOSCapabilityBuild(version.stdout)
	if err != nil {
		return osAbsorptionProofResult{reason: "installed compound binary has no build SHA"}
	}

	probes := []osCapabilityProbe{
		{task: osCapabilityProbeTask, capabilityClass: "build-implement"},
		{task: osCapabilitySecondaryProbeTask, capabilityClass: "judge-critique"},
	}
	var authorityProof osCapabilityResolutionProof
	for index, probe := range probes {
		direct, err := runOSCapabilityCommand(compoundBinary.path, []string{"capability", "resolve", "--json", probe.task}, map[string]string{"COMPOUND_HOME": homePath})
		if err != nil || direct.exitCode != 0 {
			return osAbsorptionProofResult{reason: commandFailure("compound capability resolve", direct, err)}
		}
		directProof, err := parseOSCapabilityResolution(direct.stdout, probe.task, probe.capabilityClass)
		if err != nil {
			return osAbsorptionProofResult{reason: "compound capability resolve: " + err.Error()}
		}

		wrapper, err := runOSCapabilityCommand(osPath, []string{"capability", probe.task, "--json"}, map[string]string{
			"COMPOUND_HOME":   homePath,
			"OS_COMPOUND_CLI": compoundBinary.path,
			"COMPOUND_CLI":    "",
		})
		if err != nil || wrapper.exitCode != 0 {
			return osAbsorptionProofResult{reason: commandFailure("os capability", wrapper, err)}
		}
		wrapperProof, err := parseOSCapabilityResolution(wrapper.stdout, probe.task, probe.capabilityClass)
		if err != nil {
			return osAbsorptionProofResult{reason: "os capability output: " + err.Error()}
		}
		if !bytes.Equal(directProof.canonical, wrapperProof.canonical) {
			return osAbsorptionProofResult{reason: "capability surface parity failed between Compound resolution and os capability projection"}
		}
		if index == 0 {
			authorityProof = directProof
		} else if !sameOSCapabilityAuthorityBindings(authorityProof, directProof) {
			return osAbsorptionProofResult{reason: "capability authority bindings changed across probe tasks"}
		} else if authorityProof.capabilityClass == directProof.capabilityClass || bytes.Equal(authorityProof.canonical, directProof.canonical) {
			return osAbsorptionProofResult{reason: "capability probe tasks did not exercise distinct resolutions"}
		}

		packetRoot := filepath.Join(os.TempDir(), "compound-os-capability-proof-"+digest12(homePath+string(source.projection)+probe.task))
		if _, err := os.Stat(packetRoot); err == nil {
			return osAbsorptionProofResult{reason: "disabled Compound packet probe target already exists"}
		} else if !os.IsNotExist(err) {
			return osAbsorptionProofResult{reason: "inspect disabled Compound packet probe target: " + err.Error()}
		}
		disabled, err := runOSCapabilityCommand(osPath, []string{"capability", probe.task, "--json", "--write-packet"}, map[string]string{
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
		if err := validateDisabledOSCapabilityOutput(disabled.stdout, probe.task); err != nil {
			return osAbsorptionProofResult{reason: "disabled Compound output: " + err.Error()}
		}
		if _, err := os.Stat(packetRoot); err == nil {
			return osAbsorptionProofResult{reason: "disabled Compound wrote an os capability packet"}
		} else if !os.IsNotExist(err) {
			return osAbsorptionProofResult{reason: "inspect disabled Compound packet result: " + err.Error()}
		}
	}
	postVersion, err := runOSCapabilityCommand(compoundBinary.path, []string{"version"}, map[string]string{"COMPOUND_HOME": homePath})
	if err != nil || postVersion.exitCode != 0 {
		return osAbsorptionProofResult{reason: commandFailure("compound version postcheck", postVersion, err)}
	}
	postBuild, err := parseOSCapabilityBuild(postVersion.stdout)
	if err != nil || postBuild != compoundBuild {
		return osAbsorptionProofResult{reason: "compound build changed during proof"}
	}
	if err := verifyOSCapabilitySourceSnapshot(source); err != nil {
		return osAbsorptionProofResult{reason: "os capability source changed during proof: " + err.Error()}
	}
	if err := verifyOSCapabilityBinarySnapshot(compoundBinary); err != nil {
		return osAbsorptionProofResult{reason: "compound binary changed during proof: " + err.Error()}
	}

	proofRef := fmt.Sprintf(
		"os_capability_wrapper_authority:shim_sha256:%s;entrypoint_sha256:%s;projection_sha256:%s;compound_sha256:%s;compound_build:%s;probes:%d;classifier=%s@%s;capability=%s@%s;router=%s@%s/%s",
		digestBytes(source.shim),
		digestBytes(source.entrypoint),
		digestBytes(source.projection),
		compoundBinary.digest,
		compoundBuild,
		len(probes),
		routeevidence.CapabilityTaskClassifierAuthorityRef,
		authorityProof.classifierRevision,
		capgraph.CapabilityGraphAuthorityRef,
		authorityProof.graphRevision,
		routeevidence.ModelCapabilitiesAuthorityRef,
		authorityProof.routeSourceID,
		authorityProof.routeSourceRevision,
	)
	return osAbsorptionProofResult{ok: true, proofRef: proofRef}
}

func resolveInstalledOSCapabilitySource() (osCapabilitySourceSnapshot, string, error) {
	osPath, err := exec.LookPath("os")
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	osPath, err = filepath.Abs(osPath)
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	shim, err := readRegularBounded(osPath, maxOSCapabilityShimBytes)
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	relSourceText, err := parseCanonicalOSCapabilityShim(shim)
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	homePath, err := os.UserHomeDir()
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	relSource := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(relSourceText, "/")))
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
	const proxyImport = `import { runCapabilityProxy } from "./os-capability-proxy";`
	const proxyDispatch = `else if (cmd === "capability") exit = await runCapabilityProxy(rest);`
	if strings.Count(entryText, proxyImport) != 1 ||
		strings.Count(entryText, proxyDispatch) != 1 ||
		len(osCapabilityDispatchPattern.FindAllStringIndex(entryText, -1)) != 1 ||
		strings.Count(entryText, "runCapabilityProxy(") != 1 {
		return osCapabilitySourceSnapshot{}, "", fmt.Errorf("production dispatch does not reach the Compound-only capability proxy")
	}
	for _, forbidden := range []string{
		`./classify-capability`,
		`../scripts/resolve_capability`,
		`async function runCapability(`,
		`function writeCapabilityContextPacket(`,
		`function collectLoopTriggerFires(`,
		`function appendLoopTriggerFireReceipts(`,
		`TOOL_HINTS`,
		`OS_CAPABILITY_FIRES_PATH`,
	} {
		if strings.Contains(entryText, forbidden) {
			return osCapabilitySourceSnapshot{}, "", fmt.Errorf("dormant independent resolver source %q remains", forbidden)
		}
	}
	projectionPath := filepath.Join(filepath.Dir(entrypointPath), "os-capability-proxy.ts")
	projection, err := readRegularBounded(projectionPath, maxOSCapabilityProjectionBytes)
	if err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	if err := validateOSCapabilityProjectionSource(projection); err != nil {
		return osCapabilitySourceSnapshot{}, "", err
	}
	return osCapabilitySourceSnapshot{
		shimPath:       osPath,
		entrypointPath: entrypointPath,
		projectionPath: projectionPath,
		shim:           shim,
		entrypoint:     entrypoint,
		projection:     projection,
	}, osPath, nil
}

func parseCanonicalOSCapabilityShim(source []byte) (string, error) {
	lines := strings.Split(strings.ReplaceAll(string(source), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "#!/bin/sh" {
		return "", fmt.Errorf("installed os command is not the canonical HOME-relative shim")
	}
	var relSource string
	for _, raw := range lines[1:] {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		match := osCapabilityShimSourcePattern.FindStringSubmatch(line)
		if len(match) != 2 || relSource != "" {
			return "", fmt.Errorf("installed os command contains non-canonical shim logic")
		}
		relSource = match[1]
	}
	if relSource == "" {
		return "", fmt.Errorf("installed os command is not the canonical HOME-relative shim")
	}
	return relSource, nil
}

func validateOSCapabilityProjectionSource(source []byte) error {
	text := string(source)
	requiredCounts := map[string]int{
		`export async function runCapabilityProxy`: 1,
		`"capability", "resolve", "--json"`:        1,
		`spawnSync(`:                               1,
		`result.status !== 0`:                      1,
	}
	for required, count := range requiredCounts {
		if strings.Count(text, required) != count {
			return fmt.Errorf("capability projection missing required boundary %q", required)
		}
	}
	if !strings.Contains(text, "validateResolution") {
		return fmt.Errorf("capability projection missing required boundary %q", "validateResolution")
	}
	lower := strings.ToLower(text)
	for _, forbidden := range []string{
		"skill-manifest", "skills-inventory", "skills-selection-board", "capability-graph.json",
		"capability-resolver", "model-capabilities.source", "routingdefaults", "free-pool", "loop-map",
		"resolvecapabilityintent", "searchskillcorpus", "searchcapabilitygraph", "searchmodellane", "collectlooptriggerfires",
		"appendlooptriggerfirereceipts", "tool_hints", "os_capability_fires_path",
		"readfile", "readdir", "createreadstream", "opensync", "bun.file", "fetch(", "require(", "import(",
		"execsync", "execfilesync", "execfile(", "spawn(", "fork(",
	} {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("capability projection reads retired source %q", forbidden)
		}
	}
	return nil
}

func verifyOSCapabilitySourceSnapshot(snapshot osCapabilitySourceSnapshot) error {
	checks := []struct {
		path     string
		maxBytes int64
		want     []byte
	}{
		{path: snapshot.shimPath, maxBytes: maxOSCapabilityShimBytes, want: snapshot.shim},
		{path: snapshot.entrypointPath, maxBytes: maxOSCapabilityEntrypointBytes, want: snapshot.entrypoint},
		{path: snapshot.projectionPath, maxBytes: maxOSCapabilityProjectionBytes, want: snapshot.projection},
	}
	for _, check := range checks {
		got, err := readRegularBounded(check.path, check.maxBytes)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, check.want) {
			return fmt.Errorf("%s no longer matches the validated snapshot", check.path)
		}
	}
	return nil
}

func resolveOSCapabilityBinary() (osCapabilityBinarySnapshot, error) {
	path, err := exec.LookPath("compound")
	if err != nil {
		return osCapabilityBinarySnapshot{}, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return osCapabilityBinarySnapshot{}, err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return osCapabilityBinarySnapshot{}, err
	}
	digest, err := digestRegularFileBounded(path, maxOSCapabilityBinaryBytes)
	if err != nil {
		return osCapabilityBinarySnapshot{}, err
	}
	return osCapabilityBinarySnapshot{path: path, digest: digest}, nil
}

func verifyOSCapabilityBinarySnapshot(snapshot osCapabilityBinarySnapshot) error {
	digest, err := digestRegularFileBounded(snapshot.path, maxOSCapabilityBinaryBytes)
	if err != nil {
		return err
	}
	if digest != snapshot.digest {
		return fmt.Errorf("%s no longer matches the validated binary", snapshot.path)
	}
	return nil
}

func digestRegularFileBounded(path string, maxBytes int64) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("%s is group/world writable", path)
	}
	if info.Size() > maxBytes {
		return "", fmt.Errorf("%s exceeds proof size limit", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", err
	}
	if written > maxBytes {
		return "", fmt.Errorf("%s exceeds proof size limit", path)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func parseOSCapabilityBuild(body []byte) (string, error) {
	match := osCapabilityBuildPattern.FindSubmatch(body)
	if len(match) != 2 {
		return "", fmt.Errorf("build SHA missing")
	}
	return string(match[1]), nil
}

func sameOSCapabilityAuthorityBindings(left, right osCapabilityResolutionProof) bool {
	return left.classifierRevision == right.classifierRevision &&
		left.graphRevision == right.graphRevision &&
		left.routeSourceID == right.routeSourceID &&
		left.routeSourceRevision == right.routeSourceRevision
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
	return runOSCapabilityCommandWithLimits(path, args, overrides, 20*time.Second, maxOSCapabilityCommandBytes)
}

func runOSCapabilityCommandWithLimits(path string, args []string, overrides map[string]string, timeout time.Duration, maxOutputBytes int64) (osCapabilityCommandResult, error) {
	if timeout <= 0 || maxOutputBytes < 0 {
		return osCapabilityCommandResult{}, fmt.Errorf("invalid command proof limits")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = mergedCommandEnv(overrides)
	budget := &osCapabilityOutputBudget{remaining: maxOutputBytes, cancel: cancel}
	stdout := &osCapabilityBoundedBuffer{budget: budget}
	stderr := &osCapabilityBoundedBuffer{budget: budget}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	result := osCapabilityCommandResult{stdout: stdout.Bytes(), stderr: stderr.Bytes(), exitCode: 0}
	if budget.Exceeded() {
		return result, errOSCapabilityOutputLimit
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

func (buffer *osCapabilityBoundedBuffer) Write(body []byte) (int, error) {
	buffer.budget.mu.Lock()
	keep := int64(len(body))
	if keep > buffer.budget.remaining {
		keep = buffer.budget.remaining
	}
	if keep > 0 {
		_, _ = buffer.buffer.Write(body[:int(keep)])
		buffer.budget.remaining -= keep
	}
	cancel := false
	if keep < int64(len(body)) && !buffer.budget.exceeded {
		buffer.budget.exceeded = true
		cancel = true
	}
	buffer.budget.mu.Unlock()
	if cancel {
		buffer.budget.cancel()
	}
	return len(body), nil
}

func (buffer *osCapabilityBoundedBuffer) Bytes() []byte {
	buffer.budget.mu.Lock()
	defer buffer.budget.mu.Unlock()
	return append([]byte(nil), buffer.buffer.Bytes()...)
}

func (budget *osCapabilityOutputBudget) Exceeded() bool {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.exceeded
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

func parseOSCapabilityResolution(body []byte, expectedTask, expectedClass string) (osCapabilityResolutionProof, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return osCapabilityResolutionProof{}, err
	}
	if stringField(raw, "schema_version") != osCapabilityResolutionSchema || stringField(raw, "status") != "ok" || stringField(raw, "task") != expectedTask {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid resolution header")
	}
	authorityRefs, ok := stringSlice(raw["authority_refs"])
	if !ok {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid authority refs")
	}
	for _, required := range []string{
		routeevidence.CapabilityTaskClassifierAuthorityRef,
		capgraph.CapabilityGraphAuthorityRef,
		routeevidence.ModelCapabilitiesAuthorityRef,
	} {
		if !hasExactString(authorityRefs, required) {
			return osCapabilityResolutionProof{}, fmt.Errorf("invalid authority refs")
		}
	}
	classification, ok := raw["classification"].(map[string]any)
	if !ok ||
		stringField(classification, "schema_version") != routeevidence.CapabilityTaskClassifierSchemaVersion ||
		stringField(classification, "authority_ref") != routeevidence.CapabilityTaskClassifierAuthorityRef ||
		!validSHA256Revision(stringField(classification, "authority_revision")) ||
		stringField(classification, "rule_id") == "" ||
		stringField(classification, "capability_class") != expectedClass {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid classifier authority")
	}
	classifierRevision := stringField(classification, "authority_revision")
	capabilityClass := stringField(classification, "capability_class")
	surface, ok := raw["capability_surface"].(map[string]any)
	if !ok ||
		stringField(surface, "schema_version") != capgraph.SurfaceSchemaVersion ||
		stringField(surface, "authority_ref") != capgraph.CapabilityGraphAuthorityRef ||
		!validGraphAuthorityRevision(stringField(surface, "authority_revision")) ||
		stringField(surface, "status") != "ok" ||
		stringField(surface, "task") != expectedTask {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid capability surface authority")
	}
	graphRevision := stringField(surface, "authority_revision")
	offers, ok := surface["offers"].([]any)
	if !ok || len(offers) > maxOSCapabilityOffers {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid bounded capability offers")
	}
	route, ok := raw["route_plan"].(map[string]any)
	if !ok ||
		stringField(route, "schema_version") != routeevidence.CapabilityRoutePlanSchemaVersion ||
		stringField(route, "status") != "planned" ||
		stringField(route, "authority_ref") != routeevidence.ModelCapabilitiesAuthorityRef ||
		stringField(route, "capability_class") != capabilityClass ||
		stringField(route, "created_at") == "" {
		return osCapabilityResolutionProof{}, fmt.Errorf("invalid route plan authority")
	}
	sourceID := stringField(route, "source_authority_id")
	sourceRevision := stringField(route, "source_revision")
	if !osCapabilityRuntimeIDPattern.MatchString(sourceID) || !validSHA256Revision(sourceRevision) {
		return osCapabilityResolutionProof{}, fmt.Errorf("route source authority binding missing")
	}
	chain, ok := route["chain"].([]any)
	if !ok || len(chain) == 0 || len(chain) > maxOSCapabilityRouteLanes {
		return osCapabilityResolutionProof{}, fmt.Errorf("route plan has no eligible chain")
	}
	delete(route, "created_at")
	canonical, err := json.Marshal(raw)
	if err != nil {
		return osCapabilityResolutionProof{}, err
	}
	return osCapabilityResolutionProof{
		raw:                 raw,
		canonical:           canonical,
		capabilityClass:     capabilityClass,
		classifierRevision:  classifierRevision,
		graphRevision:       graphRevision,
		routeSourceID:       sourceID,
		routeSourceRevision: sourceRevision,
	}, nil
}

func expectedOSCapabilitySourceDigests(evidenceRefs []string) (osCapabilityExpectedSourceDigests, error) {
	shim, err := exactOSCapabilityDigestEvidence(evidenceRefs, osCapabilityShimDigestRef)
	if err != nil {
		return osCapabilityExpectedSourceDigests{}, err
	}
	entrypoint, err := exactOSCapabilityDigestEvidence(evidenceRefs, osCapabilityEntrypointDigestRef)
	if err != nil {
		return osCapabilityExpectedSourceDigests{}, err
	}
	projection, err := exactOSCapabilityDigestEvidence(evidenceRefs, osCapabilityProjectionDigestRef)
	if err != nil {
		return osCapabilityExpectedSourceDigests{}, err
	}
	return osCapabilityExpectedSourceDigests{shim: shim, entrypoint: entrypoint, projection: projection}, nil
}

func exactOSCapabilityDigestEvidence(evidenceRefs []string, prefix string) (string, error) {
	value := ""
	for _, ref := range evidenceRefs {
		ref = strings.TrimSpace(ref)
		if !strings.HasPrefix(ref, prefix) {
			continue
		}
		candidate := strings.TrimPrefix(ref, prefix)
		if len(candidate) != 64 || !isOSAbsorptionHex(candidate) {
			return "", fmt.Errorf("invalid %s evidence ref", prefix)
		}
		if value != "" && value != candidate {
			return "", fmt.Errorf("conflicting %s evidence refs", prefix)
		}
		value = candidate
	}
	if value == "" {
		return "", fmt.Errorf("missing %s evidence ref", prefix)
	}
	return value, nil
}

func (snapshot osCapabilitySourceSnapshot) matchesExpected(expected osCapabilityExpectedSourceDigests) error {
	checks := []struct {
		name string
		got  string
		want string
	}{
		{name: "shim", got: digestBytes(snapshot.shim), want: expected.shim},
		{name: "entrypoint", got: digestBytes(snapshot.entrypoint), want: expected.entrypoint},
		{name: "projection", got: digestBytes(snapshot.projection), want: expected.projection},
	}
	for _, check := range checks {
		if check.got != check.want {
			return fmt.Errorf("installed %s digest %s does not match reviewed %s", check.name, check.got, check.want)
		}
	}
	return nil
}

func validateDisabledOSCapabilityOutput(body []byte, expectedTask string) error {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	if stringField(raw, "status") != "unavailable" || stringField(raw, "task") != expectedTask {
		return fmt.Errorf("must report unavailable for the probe task")
	}
	authorityRefs, ok := stringSlice(raw["authority_refs"])
	if !ok {
		return fmt.Errorf("invalid authority refs")
	}
	for _, required := range []string{
		routeevidence.CapabilityTaskClassifierAuthorityRef,
		capgraph.CapabilityGraphAuthorityRef,
		routeevidence.ModelCapabilitiesAuthorityRef,
	} {
		if !hasExactString(authorityRefs, required) {
			return fmt.Errorf("invalid authority refs")
		}
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

func validGraphAuthorityRevision(value string) bool {
	if len(value) != len("sha256:")+12 || !strings.HasPrefix(value, "sha256:") {
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
