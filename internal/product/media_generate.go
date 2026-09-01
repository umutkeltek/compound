package product

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"compound/internal/act"
)

// media_generate.go is the pluggable generator seam: the door that turns a prompt
// into an asset a ContentDraft can carry.
//
// It lives in internal/product rather than a package of its own because what it
// produces is a ContentDraftMedia, which this package already owns. localmodel is
// LLM routing, adapter is read-only foreign normalization, and actuator routes work
// to executions — none of them owns asset generation. If a second consumer appears,
// extract it then.
//
// The seam has one integrity rule that everything downstream depends on: an
// asset's PROVENANCE comes from the enrolled config, never from the generator's own
// output. A generator that could name its own adapter or model could attribute its
// work to another, and every later comparison ("which model actually wins?") would
// be measuring a claim the generator made about itself.

// MaxGeneratedAssetBytes bounds what one generator may hand back. An unbounded read
// is how a single bad render exhausts memory on the machine that runs every lane.
const MaxGeneratedAssetBytes = 64 * 1024 * 1024

// maxGeneratorStdout bounds the generator's control-channel output. The asset
// itself travels as a file path, so this only needs to hold a small JSON object.
const maxGeneratorStdout = 64 * 1024

// generatorAllowedBareNames is empty on purpose: a media generator must be enrolled
// by clean absolute path. Unlike a transport (where "claude" or "codex" is a
// meaningful bare name resolved on PATH), a generator is operator-specific tooling,
// and resolving it through PATH would make which program ran depend on environment.
var generatorAllowedBareNames = map[string]bool{}

// MediaGeneratorConfig is one enrolled generator, read from
// $COMPOUND_HOME/actuator/generators/<name>.json. It mirrors the exec transport
// config deliberately: same shape, same safety rule, one more declared field pair
// (kind + model) because an asset carries provenance a text post does not.
type MediaGeneratorConfig struct {
	Argv     []string `json:"argv"`
	TimeoutS int      `json:"timeout_s"`
	Kind     string   `json:"kind"`
	Model    string   `json:"model"`
}

// MediaGenerateRequest is what the operator asks for.
type MediaGenerateRequest struct {
	Adapter string
	Prompt  string
	AltText string
	Params  map[string]string
}

// generatorOutputRootPerm keeps a render-in-progress unreadable by other local
// users. The root is Compound's, not the generator's, and it is removed per run.
const generatorOutputRootPerm = 0o700

// MediaGeneratorRunner executes an enrolled generator. It is an interface so every
// rule in this file is provable without a GPU, a network, or model weights.
type MediaGeneratorRunner interface {
	Run(cfg MediaGeneratorConfig, stdin []byte) ([]byte, error)
}

// generatorAnswer is the generator's control-channel reply. Note what is NOT read
// from it: adapter, model, and kind. Those are the enrolled config's to declare.
type generatorAnswer struct {
	Path     string `json:"path"`
	MIMEType string `json:"mime_type"`
}

// GenerateContentDraftMedia runs an enrolled generator and returns an asset ready
// to attach to a draft. The returned value is a ContentDraftMediaInput precisely so
// the draft door validates it on the way in — this function has no privileged path
// into a draft.
func GenerateContentDraftMedia(homePath string, req MediaGenerateRequest, runner MediaGeneratorRunner, now time.Time) (ContentDraftMediaInput, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return ContentDraftMediaInput{}, fmt.Errorf("prompt required: an asset with no prompt cannot be reproduced or compared later")
	}
	name := strings.TrimSpace(req.Adapter)
	cfg, err := LoadMediaGeneratorConfig(homePath, name)
	if err != nil {
		return ContentDraftMediaInput{}, err
	}
	if runner == nil {
		runner = ExecMediaGeneratorRunner{}
	}
	// Compound owns the output root. A generator that could name any readable path
	// could have Compound custody a private key or credential file — this is not
	// hypothetical, it was live in 8059782a and confirmed by probe.
	outputRoot, err := os.MkdirTemp("", "compound-generator-")
	if err != nil {
		return ContentDraftMediaInput{}, err
	}
	defer os.RemoveAll(outputRoot)
	if err := os.Chmod(outputRoot, generatorOutputRootPerm); err != nil {
		return ContentDraftMediaInput{}, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(outputRoot)
	if err != nil {
		return ContentDraftMediaInput{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"kind":       cfg.Kind,
		"model":      cfg.Model,
		"prompt":     prompt,
		"params":     req.Params,
		"output_dir": resolvedRoot,
		"issued_at":  now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return ContentDraftMediaInput{}, err
	}
	stdout, err := runner.Run(cfg, payload)
	if err != nil {
		return ContentDraftMediaInput{}, fmt.Errorf("generator %q failed: %w", name, err)
	}
	bytesOut, mime, err := readGeneratorAsset(name, resolvedRoot, stdout)
	if err != nil {
		return ContentDraftMediaInput{}, err
	}
	return ContentDraftMediaInput{
		Kind:     cfg.Kind,
		Bytes:    bytesOut,
		MIMEType: mime,
		AltText:  strings.TrimSpace(req.AltText),
		// Provenance from the ENROLLED CONFIG, never from the generator's answer.
		Adapter: "adapter:" + name,
		Model:   cfg.Model,
		Prompt:  prompt,
		Params:  req.Params,
	}, nil
}

// LoadMediaGeneratorConfig reads and validates one enrolled generator. Everything
// that can be checked before a GPU spends minutes rendering is checked here.
func LoadMediaGeneratorConfig(homePath, name string) (MediaGeneratorConfig, error) {
	homePath = strings.TrimSpace(homePath)
	if homePath == "" {
		return MediaGeneratorConfig{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if name == "" || filepath.Base(name) != name || strings.Contains(name, ".") {
		return MediaGeneratorConfig{}, fmt.Errorf("generator name %q must be a plain name", name)
	}
	path := filepath.Join(homePath, "actuator", "generators", name+".json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return MediaGeneratorConfig{}, fmt.Errorf("generator %q is not enrolled: no config at %s", name, path)
	}
	if err != nil {
		return MediaGeneratorConfig{}, err
	}
	var cfg MediaGeneratorConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return MediaGeneratorConfig{}, fmt.Errorf("generator %q config: %w", name, err)
	}
	cfg.Kind = strings.ToLower(strings.TrimSpace(cfg.Kind))
	if !contentDraftMediaKinds[cfg.Kind] {
		return MediaGeneratorConfig{}, fmt.Errorf("generator %q declares kind %q, which a draft cannot carry (image|video)", name, cfg.Kind)
	}
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" {
		return MediaGeneratorConfig{}, fmt.Errorf("generator %q config: model identity required, or its output cannot be attributed", name)
	}
	// One owner for "may this enrolled config invoke this program?" — shared with
	// the act exec transport rather than copied, because a security rule with two
	// copies is a rule that drifts.
	if err := act.ValidateEnrolledArgv("generator", name, cfg.Argv, generatorAllowedBareNames); err != nil {
		return MediaGeneratorConfig{}, err
	}
	if cfg.TimeoutS <= 0 || cfg.TimeoutS > 3600 {
		return MediaGeneratorConfig{}, fmt.Errorf("generator %q config: timeout_s must be between 1 and 3600", name)
	}
	return cfg, nil
}

// readGeneratorAsset parses the control-channel answer and loads the asset the
// generator wrote. Every failure here is loud: a generator that produced nothing
// usable must not become an empty asset that later publishes as a blank image.
func readGeneratorAsset(name, outputRoot string, stdout []byte) ([]byte, string, error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return nil, "", fmt.Errorf("generator %q produced no output", name)
	}
	var answer generatorAnswer
	if err := json.Unmarshal([]byte(trimmed), &answer); err != nil {
		return nil, "", fmt.Errorf("generator %q output is not a JSON object naming the asset path: %w", name, err)
	}
	assetPath := strings.TrimSpace(answer.Path)
	if assetPath == "" {
		return nil, "", fmt.Errorf("generator %q output names no asset path", name)
	}
	if !filepath.IsAbs(assetPath) || filepath.Clean(assetPath) != assetPath {
		return nil, "", fmt.Errorf("generator %q asset path must be a clean absolute path, got %q", name, answer.Path)
	}
	// Containment, checked on the PATH first: the asset must live under the output
	// root Compound created for this run.
	rel, err := filepath.Rel(outputRoot, assetPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("generator %q asset %q is outside its output root %s: a generator may only return a file it wrote there", name, assetPath, outputRoot)
	}
	// Containment, enforced on the DESCRIPTOR: a path inside the root can still be a
	// symlink pointing out of it. O_NOFOLLOW refuses the link itself, and every
	// subsequent check reads the same descriptor, so the file cannot be swapped
	// between the check and the read.
	f, err := os.OpenFile(assetPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, "", fmt.Errorf("generator %q asset is not readable as a regular file inside its output root: %w", name, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("generator %q asset stat: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("generator %q asset %q is not a regular file", name, assetPath)
	}
	if info.Size() == 0 {
		return nil, "", fmt.Errorf("generator %q produced an empty asset", name)
	}
	if info.Size() > MaxGeneratedAssetBytes {
		return nil, "", fmt.Errorf("generator %q asset is %d bytes, which exceeds the %d byte bound", name, info.Size(), MaxGeneratedAssetBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxGeneratedAssetBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("generator %q asset read: %w", name, err)
	}
	if int64(len(data)) > MaxGeneratedAssetBytes {
		return nil, "", fmt.Errorf("generator %q asset exceeds the %d byte bound", name, MaxGeneratedAssetBytes)
	}
	return data, strings.TrimSpace(answer.MIMEType), nil
}

// ExecMediaGeneratorRunner runs the enrolled program directly, handing it the
// request on stdin and reading its bounded control-channel answer from stdout. It
// never uses a shell: the argv is executed as-is, already validated by the shared
// enrolled-executable rule.
type ExecMediaGeneratorRunner struct{}

func (ExecMediaGeneratorRunner) Run(cfg MediaGeneratorConfig, stdin []byte) ([]byte, error) {
	cmd := exec.Command(cfg.Argv[0], cfg.Argv[1:]...)
	cmd.Stdin = strings.NewReader(string(stdin))
	// The generator's own environment is inherited deliberately: a local renderer
	// legitimately needs CUDA/model-path variables the operator has configured.
	// Separate buffers, not one shared builder: os/exec writes stdout and stderr
	// from different goroutines, so a single unsynchronized strings.Builder is a
	// data race AND lets a chatty generator's stderr logs corrupt the JSON contract
	// on stdout.
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &boundedWriter{w: &stdoutBuf, max: maxGeneratorStdout}
	cmd.Stderr = &boundedWriter{w: &stderrBuf, max: maxGeneratorStdout}
	timer := time.AfterFunc(time.Duration(cfg.TimeoutS)*time.Second, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	defer timer.Stop()
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderrBuf.String()))
	}
	return []byte(stdoutBuf.String()), nil
}

// boundedWriter caps what a generator can push through the control channel.
type boundedWriter struct {
	w   *strings.Builder
	max int
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	if remaining := b.max - b.w.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.w.Write(p)
	}
	// Report the full length: a generator must not see a short write and retry.
	return len(p), nil
}
