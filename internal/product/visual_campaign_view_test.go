package product

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func campaignViewWork(t *testing.T, artifacts []string) (string, string) {
	t.Helper()
	homePath, id := t.TempDir(), "work_campaign_fixture_20300220"
	dir := filepath.Join(homePath, "work", "work")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"id": id, "artifact_ids": artifacts})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return homePath, id
}

func campaignViewManifest(t *testing.T, base string, c VisualCampaign) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "campaign.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCampaignWorkViewProjectsRealCheckedAssetsWithoutFileLocators(t *testing.T) {
	c, base, now := campaignFixture(t)
	path := campaignViewManifest(t, base, c)
	homePath, id := campaignViewWork(t, []string{"unrelated:artifact", "visual-campaign:" + path})
	view, err := ReadCampaignWorkView(homePath, id, now)
	if err != nil || len(view.Campaigns) != 1 {
		t.Fatalf("view: %+v %v", view, err)
	}
	v := view.Campaigns[0]
	if v.WorkID != id || v.Index != 0 || v.Status != "checked" || v.Check == nil || !v.Check.Ready || v.CampaignSHA256 != campaignDigest(c) {
		t.Fatalf("check identity/status: %+v", v)
	}
	if len(v.Assets) != 2 || !v.Assets[0].Available || v.Assets[0].MIMEType != "image/png" || v.Assets[0].Width != 16 {
		t.Fatalf("assets: %+v", v.Assets)
	}
	b, _ := json.Marshal(view)
	if bytes.Contains(b, []byte(base)) || bytes.Contains(b, []byte("source.png")) || bytes.Contains(b, []byte(`"path"`)) {
		t.Fatalf("metadata leaked a file locator: %s", b)
	}
	asset, mime, err := ReadCampaignWorkAsset(homePath, id, 0, 0)
	want, _ := os.ReadFile(filepath.Join(base, c.Assets[0].File.Path))
	if err != nil || mime != "image/png" || !bytes.Equal(asset, want) {
		t.Fatalf("asset: %s %v", mime, err)
	}
	entries, err := os.ReadDir(homePath)
	if err != nil || len(entries) != 1 || entries[0].Name() != "work" {
		t.Fatalf("read projection initialized runtime state: %v %v", entries, err)
	}
}

func TestCampaignWorkViewKeepsDraftAndCheckFailureVisible(t *testing.T) {
	for _, mode := range []string{"missing outputs", "missing experiment", "missing source image"} {
		t.Run(mode, func(t *testing.T) {
			c, base, now := campaignFixture(t)
			switch mode {
			case "missing outputs":
				c.Assets = nil
			case "missing experiment":
				c.Experiment = CampaignExperiment{}
			case "missing source image":
				if err := os.Remove(filepath.Join(base, "source.png")); err != nil {
					t.Fatal(err)
				}
			}
			path := campaignViewManifest(t, base, c)
			homePath, id := campaignViewWork(t, []string{"visual-campaign:" + path})
			view, err := ReadCampaignWorkView(homePath, id, now)
			if err != nil || len(view.Campaigns) != 1 {
				t.Fatalf("view: %+v %v", view, err)
			}
			v := view.Campaigns[0]
			if v.Status != "draft" || v.ID != c.ID || v.Issue != "" {
				t.Fatalf("draft lost its brief: %+v", v)
			}
			if mode == "missing outputs" {
				if v.Check == nil || v.Check.Ready || len(v.Check.Issues) != 2 || v.Check.ExpectedAssets != 2 {
					t.Fatalf("missing export debt hidden: %+v", v)
				}
			} else if v.CheckError == "" {
				t.Fatalf("exact check failure hidden: %+v", v)
			}
		})
	}
}

func TestCampaignWorkAssetRefusesUntrustedFiles(t *testing.T) {
	cases := map[string]func(*testing.T, *VisualCampaign, string){
		"relative traversal": func(t *testing.T, c *VisualCampaign, base string) { c.Assets[0].File.Path = "../outside.png" },
		"absolute outside": func(t *testing.T, c *VisualCampaign, base string) {
			c.Assets[0].File.Path = filepath.Join(t.TempDir(), "outside.png")
		},
		"hash mismatch": func(t *testing.T, c *VisualCampaign, base string) { c.Assets[0].File.SHA256 = strings.Repeat("0", 64) },
		"oversize": func(t *testing.T, c *VisualCampaign, base string) {
			f, err := os.Create(filepath.Join(base, "oversize.png"))
			if err != nil {
				t.Fatal(err)
			}
			if err = f.Truncate(campaignViewAssetLimit + 1); err != nil {
				t.Fatal(err)
			}
			f.Close()
			c.Assets[0].File.Path = "oversize.png"
		},
		"symlink file inside": func(t *testing.T, c *VisualCampaign, base string) {
			if err := os.Symlink("source.png", filepath.Join(base, "link.png")); err != nil {
				t.Fatal(err)
			}
			c.Assets[0].File.Path = "link.png"
		},
		"symlink directory inside": func(t *testing.T, c *VisualCampaign, base string) {
			if err := os.Symlink(".", filepath.Join(base, "link")); err != nil {
				t.Fatal(err)
			}
			c.Assets[0].File.Path = "link/source.png"
		},
		"symlink directory outside": func(t *testing.T, c *VisualCampaign, base string) {
			outside := t.TempDir()
			b, _ := os.ReadFile(filepath.Join(base, "source.png"))
			campaignTestFile(t, outside, "source.png", b)
			if err := os.Symlink(outside, filepath.Join(base, "link")); err != nil {
				t.Fatal(err)
			}
			c.Assets[0].File.Path = "link/source.png"
		},
		"non image exact hash": func(t *testing.T, c *VisualCampaign, base string) {
			c.Assets[0].File = campaignTestFile(t, base, "text.png", []byte("not an image"))
		},
		"truncated image exact hash": func(t *testing.T, c *VisualCampaign, base string) {
			b, _ := os.ReadFile(filepath.Join(base, "source.png"))
			c.Assets[0].File = campaignTestFile(t, base, "truncated.png", b[:len(b)/2])
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c, base, now := campaignFixture(t)
			mutate(t, &c, base)
			path := campaignViewManifest(t, base, c)
			homePath, id := campaignViewWork(t, []string{"visual-campaign:" + path})
			asset, _, err := ReadCampaignWorkAsset(homePath, id, 0, 0)
			if err == nil || len(asset) != 0 {
				t.Fatalf("unsafe asset returned bytes: %d %v", len(asset), err)
			}
			view, err := ReadCampaignWorkView(homePath, id, now)
			if err != nil || len(view.Campaigns) != 1 || view.Campaigns[0].Assets[0].Available || view.Campaigns[0].Assets[0].Issue == "" {
				t.Fatalf("unsafe asset not explicit in metadata: %+v %v", view, err)
			}
			b, _ := json.Marshal(view)
			if bytes.Contains(b, []byte(base)) {
				t.Fatalf("failure exposed local path: %s", b)
			}
		})
	}
}

func TestCampaignWorkViewDistinguishesEmptyUnattachedAndUnreadable(t *testing.T) {
	c, base, now := campaignFixture(t)
	manifest := campaignViewManifest(t, base, c)
	homePath, id := campaignViewWork(t, []string{"other:" + manifest})
	v, err := ReadCampaignWorkView(homePath, id, now)
	if err != nil || v.Campaigns == nil || len(v.Campaigns) != 0 {
		t.Fatalf("no attached campaigns: %+v %v", v, err)
	}
	if b, _, err := ReadCampaignWorkAsset(homePath, id, 0, 0); err == nil || len(b) != 0 {
		t.Fatal("unattached manifest supplied an asset")
	}
	missingHome := filepath.Join(t.TempDir(), "absent")
	if _, err := ReadCampaignWorkView(missingHome, id, now); err == nil {
		t.Fatal("missing work became empty campaigns")
	}
	if _, err := os.Stat(missingHome); !os.IsNotExist(err) {
		t.Fatal("read created missing home")
	}
	if err := os.WriteFile(filepath.Join(homePath, "work", "work", id+".json"), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCampaignWorkView(homePath, id, now); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("corrupt work became empty campaigns: %v", err)
	}
	for _, bad := range []string{"work_../../secret", "work_%2fsecret", ""} {
		if _, err := ReadCampaignWorkView(homePath, bad, now); err == nil {
			t.Fatalf("bad work id accepted: %q", bad)
		}
	}
	for _, content := range []string{`{"schema_version":"wrong","id":"x","product_id":"product:fixture"}`, `{"id":"x","unknown":true}`, `{broken`} {
		if err := os.WriteFile(manifest, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		homePath, id := campaignViewWork(t, []string{"visual-campaign:" + manifest})
		v, err := ReadCampaignWorkView(homePath, id, now)
		if err != nil || len(v.Campaigns) != 1 || v.Campaigns[0].Status != "unavailable" || v.Campaigns[0].Issue == "" {
			t.Fatalf("malformed attachment became empty campaigns: %+v %v", v, err)
		}
		if b, _, err := ReadCampaignWorkAsset(homePath, id, 0, 0); err == nil || len(b) != 0 {
			t.Fatal("malformed manifest supplied asset bytes")
		}
	}
}

func TestCampaignWorkViewCLIIndicesAndWriteFreeFailure(t *testing.T) {
	c, base, now := campaignFixture(t)
	homePath, id := campaignViewWork(t, []string{"visual-campaign:" + campaignViewManifest(t, base, c)})
	for _, args := range [][]string{{"--work", id, "--asset", "-1"}, {"--work", id, "--campaign", "1", "--asset", "0"}, {"--work", id, "--asset", "9"}, {"--work", id, "--manifest", "anything"}} {
		var stdout, stderr bytes.Buffer
		if code := RunCampaignViewCLI(args, homePath, &stdout, &stderr, now); code == 0 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("bad args supplied output: %v code=%d stdout=%d stderr=%s", args, code, stdout.Len(), stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := RunCampaignViewCLI([]string{"--work", id, "--json"}, homePath, &stdout, &stderr, now); code != 0 || !json.Valid(stdout.Bytes()) {
		t.Fatalf("metadata CLI: %d %s", code, stderr.String())
	}
	stdout.Reset()
	if code := RunCampaignViewCLI([]string{"--work", id, "--campaign", "0", "--asset", "0"}, homePath, &stdout, &stderr, now); code != 0 || !bytes.HasPrefix(stdout.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("asset CLI: %d %s", code, stderr.String())
	}
}
