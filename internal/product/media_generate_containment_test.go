package product

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// pathRunner answers with whatever path the test wants, ignoring the output root
// it was handed. It stands in for a compromised or buggy generator.
type pathRunner struct {
	path     string
	rootSeen string
}

func (r *pathRunner) Run(cfg MediaGeneratorConfig, stdin []byte) ([]byte, error) {
	var req map[string]any
	_ = json.Unmarshal(stdin, &req)
	if root, ok := req["output_dir"].(string); ok {
		r.rootSeen = root
	}
	return []byte(`{"path":` + strconv.Quote(r.path) + `,"mime_type":"image/png"}`), nil
}

// TestGeneratorCannotIngestFileOutsideOutputRoot is the red-first falsifier for a
// file-disclosure hole shipped in 8059782a and confirmed by live probe: a generator
// returned a path to a file it never created, and Compound ingested those bytes into
// the artifact store and attached them to a draft.
//
// An enrolled generator is operator-installed, but it is still a separate program
// that may be third-party, compromised, or simply wrong. It must not be able to name
// ~/.ssh/id_ed25519, a credential file, or any other readable host file and have
// Compound custody it.
//
// The boundary: Compound owns a private per-run output root, the generator writes
// inside it, and nothing outside it is accepted.
func TestGeneratorCannotIngestFileOutsideOutputRoot(t *testing.T) {
	homePath := t.TempDir()
	enrollGenerator(t, homePath, "local-sdxl", imageGeneratorConfig())

	outside := filepath.Join(t.TempDir(), "not-an-asset.txt")
	if err := os.WriteFile(outside, []byte("SYNTHETIC-NOT-A-REAL-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &pathRunner{path: outside}
	_, err := GenerateContentDraftMedia(homePath, MediaGenerateRequest{
		Adapter: "local-sdxl",
		Prompt:  "give me a host file",
	}, runner, mediaGenNow)
	if err == nil {
		t.Fatal("a generator must not be able to ingest a file outside the output root Compound gave it")
	}
	if !strings.Contains(err.Error(), "output") {
		t.Fatalf("refusal must explain the containment boundary, got: %v", err)
	}
	if runner.rootSeen == "" {
		t.Fatal("the generator must be TOLD its output root; a boundary it cannot see is one it cannot honour")
	}
}

// TestGeneratorOutputRootIsPrivateAndCleanedUp pins that the root Compound hands out
// is its own, is not world-readable, and does not accumulate on disk across runs.
func TestGeneratorOutputRootIsPrivateAndCleanedUp(t *testing.T) {
	homePath := t.TempDir()
	enrollGenerator(t, homePath, "local-sdxl", imageGeneratorConfig())

	var rootDuringRun string
	var modeDuringRun os.FileMode
	runner := runnerFunc(func(cfg MediaGeneratorConfig, stdin []byte) ([]byte, error) {
		var req map[string]any
		if err := json.Unmarshal(stdin, &req); err != nil {
			return nil, err
		}
		root, _ := req["output_dir"].(string)
		rootDuringRun = root
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		modeDuringRun = info.Mode().Perm()
		asset := filepath.Join(root, "out.png")
		if err := os.WriteFile(asset, []byte("\x89PNG\r\n\x1a\nok"), 0o644); err != nil {
			return nil, err
		}
		return []byte(`{"path":` + strconv.Quote(asset) + `,"mime_type":"image/png"}`), nil
	})

	media, err := GenerateContentDraftMedia(homePath, MediaGenerateRequest{Adapter: "local-sdxl", Prompt: "p"}, runner, mediaGenNow)
	if err != nil {
		t.Fatalf("a generator writing inside its output root must succeed: %v", err)
	}
	if len(media.Bytes) == 0 {
		t.Fatal("asset bytes not returned")
	}
	if modeDuringRun&0o077 != 0 {
		t.Fatalf("output root mode = %v, want owner-only: another local user must not read a render in progress", modeDuringRun)
	}
	if _, err := os.Stat(rootDuringRun); !os.IsNotExist(err) {
		t.Fatalf("output root %q survived the run; a per-run root must not accumulate", rootDuringRun)
	}
}

// TestGeneratorCannotEscapeOutputRootBySymlink closes the same hole one level down:
// a path INSIDE the root that is a symlink pointing OUT of it. Checking the path
// string is not enough; the file must be opened without following links.
func TestGeneratorCannotEscapeOutputRootBySymlink(t *testing.T) {
	homePath := t.TempDir()
	enrollGenerator(t, homePath, "local-sdxl", imageGeneratorConfig())

	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("SYNTHETIC-NOT-A-REAL-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := runnerFunc(func(cfg MediaGeneratorConfig, stdin []byte) ([]byte, error) {
		var req map[string]any
		if err := json.Unmarshal(stdin, &req); err != nil {
			return nil, err
		}
		root, _ := req["output_dir"].(string)
		link := filepath.Join(root, "out.png")
		if err := os.Symlink(outside, link); err != nil {
			return nil, err
		}
		return []byte(`{"path":` + strconv.Quote(link) + `,"mime_type":"image/png"}`), nil
	})

	if _, err := GenerateContentDraftMedia(homePath, MediaGenerateRequest{Adapter: "local-sdxl", Prompt: "p"}, runner, mediaGenNow); err == nil {
		t.Fatal("a symlink inside the output root pointing outside it must be refused")
	}
}

// TestGeneratedMediaCarriesItsParameters pins the reproducibility claim. The seed
// and other params were sent to the generator but never persisted, so an asset could
// not actually be reproduced from its own record — the claim outran the data.
func TestGeneratedMediaCarriesItsParameters(t *testing.T) {
	homePath := t.TempDir()
	enrollGenerator(t, homePath, "local-sdxl", imageGeneratorConfig())
	runner := runnerFunc(func(cfg MediaGeneratorConfig, stdin []byte) ([]byte, error) {
		var req map[string]any
		if err := json.Unmarshal(stdin, &req); err != nil {
			return nil, err
		}
		root, _ := req["output_dir"].(string)
		asset := filepath.Join(root, "out.png")
		if err := os.WriteFile(asset, []byte("\x89PNG\r\n\x1a\nok"), 0o644); err != nil {
			return nil, err
		}
		return []byte(`{"path":` + strconv.Quote(asset) + `}`), nil
	})

	media, err := GenerateContentDraftMedia(homePath, MediaGenerateRequest{
		Adapter: "local-sdxl",
		Prompt:  "p",
		Params:  map[string]string{"seed": "42", "steps": "30"},
	}, runner, mediaGenNow)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if media.Params["seed"] != "42" || media.Params["steps"] != "30" {
		t.Fatalf("generation params not persisted, so the asset cannot be reproduced from its record: %+v", media.Params)
	}

	draft, err := RecordContentDraft(homePath, ContentDraftInput{
		ProductID: "product:prod_synth_a",
		Platform:  "instagram",
		Body:      "b",
		ExpiresAt: mediaGenNow.Add(time.Hour),
		Media:     []ContentDraftMediaInput{media},
	}, mediaGenNow)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if draft.Media[0].Params["seed"] != "42" {
		t.Fatalf("params lost on attach: %+v", draft.Media[0])
	}
}

type runnerFunc func(cfg MediaGeneratorConfig, stdin []byte) ([]byte, error)

func (f runnerFunc) Run(cfg MediaGeneratorConfig, stdin []byte) ([]byte, error) { return f(cfg, stdin) }
