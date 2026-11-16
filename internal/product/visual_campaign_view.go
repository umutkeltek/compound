package product

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"compound/internal/work"
)

const campaignViewAssetLimit = 20 << 20
const campaignViewManifestLimit = 2 << 20
const campaignViewUsage = "campaign view --work <work_id> [--campaign <index> --asset <index>] [--json]"

var campaignViewWorkID = regexp.MustCompile(`^work_[A-Za-z0-9][A-Za-z0-9._:-]*$`)

type CampaignWorkView struct {
	SchemaVersion string         `json:"schema_version"`
	WorkID        string         `json:"work_id"`
	Campaigns     []CampaignView `json:"campaigns"`
}

// CampaignView contains no filesystem locator. Indices resolve against the
// requested WorkEnvelope's attachments again for every image request.
type CampaignView struct {
	Index          int                 `json:"index"`
	WorkID         string              `json:"work_id"`
	Status         string              `json:"status"`
	Issue          string              `json:"issue,omitempty"`
	ID             string              `json:"id,omitempty"`
	ProductID      string              `json:"product_id,omitempty"`
	CreatedAt      string              `json:"created_at,omitempty"`
	CampaignSHA256 string              `json:"campaign_sha256,omitempty"`
	Context        CampaignContext     `json:"context"`
	Direction      CampaignDirection   `json:"direction"`
	Locales        []CampaignLocale    `json:"locales"`
	Targets        []CampaignTarget    `json:"targets"`
	Scenes         []CampaignScene     `json:"scenes"`
	Assets         []CampaignAssetView `json:"assets"`
	Check          *CampaignCheck      `json:"check,omitempty"`
	CheckError     string              `json:"check_error,omitempty"`
}

type CampaignAssetView struct {
	Index         int                 `json:"index"`
	Scene         string              `json:"scene"`
	Locale        string              `json:"locale"`
	Target        string              `json:"target"`
	SHA256        string              `json:"sha256"`
	Available     bool                `json:"available"`
	Issue         string              `json:"issue,omitempty"`
	Width         int                 `json:"width,omitempty"`
	Height        int                 `json:"height,omitempty"`
	MIMEType      string              `json:"mime_type,omitempty"`
	Model         string              `json:"model,omitempty"`
	RenderReceipt string              `json:"render_receipt,omitempty"`
	Review        CampaignAssetReview `json:"review"`
}

func attachedCampaignPaths(homePath, workID string) ([]string, error) {
	if !campaignViewWorkID.MatchString(workID) {
		return nil, fmt.Errorf("a valid work_id is required")
	}
	store, err := work.OpenReadOnly(homePath)
	if err != nil {
		return nil, fmt.Errorf("work store unavailable")
	}
	w, err := store.Get(workID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("work %s was not found", workID)
		}
		return nil, fmt.Errorf("work %s is unreadable", workID)
	}
	if w.ID != workID {
		return nil, fmt.Errorf("work record identity does not match requested work")
	}
	paths := []string{}
	for _, artifact := range w.ArtifactIDs {
		if strings.HasPrefix(artifact, "visual-campaign:") {
			paths = append(paths, strings.TrimPrefix(artifact, "visual-campaign:"))
		}
	}
	if len(paths) > 32 {
		return nil, fmt.Errorf("work has more than 32 campaign attachments; narrow the work artifacts before viewing")
	}
	return paths, nil
}

// Read through a pinned root descriptor. Every component must be clean and
// non-symlink; Root also enforces containment during traversal and races.
func readCampaignViewFile(root *os.Root, base, name string, limit int64) ([]byte, error) {
	if filepath.IsAbs(name) {
		var err error
		name, err = filepath.Rel(base, name)
		if err != nil {
			return nil, fmt.Errorf("file must be inside the campaign directory")
		}
	}
	if name == "" || name == "." || name != filepath.Clean(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("file must use a clean path inside the campaign directory")
	}
	parts := strings.Split(name, string(filepath.Separator))
	for i := range parts {
		st, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("file is missing")
			}
			return nil, fmt.Errorf("file is unreadable within the campaign directory")
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlinks are not permitted in campaign file paths")
		}
	}
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > limit {
		return nil, fmt.Errorf("file must be nonempty, regular and at most %d bytes", limit)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("file is unreadable within the campaign directory")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("file identity changed while opening")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, fmt.Errorf("file read failed or exceeded the size limit")
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(opened, after) {
		return nil, fmt.Errorf("file identity changed while reading")
	}
	return b, nil
}

func openCampaignView(path string) (VisualCampaign, *os.Root, error) {
	var c VisualCampaign
	if !filepath.IsAbs(path) || path != filepath.Clean(path) {
		return c, nil, fmt.Errorf("campaign attachment must name a clean absolute manifest path")
	}
	base := filepath.Dir(path)
	root, err := os.OpenRoot(base)
	if err != nil {
		return c, nil, fmt.Errorf("attached campaign directory is unreadable")
	}
	b, err := readCampaignViewFile(root, base, filepath.Base(path), campaignViewManifestLimit)
	if err == nil {
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if err = d.Decode(&c); err == nil {
			var extra any
			if d.Decode(&extra) != io.EOF {
				err = fmt.Errorf("expected one campaign JSON document")
			}
		} else {
			err = fmt.Errorf("attached campaign JSON is malformed or contains unknown fields")
		}
	}
	if err == nil && (c.SchemaVersion != VisualCampaignSchema || !campaignNonempty(c.ID, c.ProductID) || len(c.Assets) > 256 || len(c.References) > 128 || len(c.Scenes) > 64 || len(c.Locales) > 64 || len(c.Targets) > 32 || len(c.Scenes)*len(c.Locales)*len(c.Targets) > 256) {
		err = fmt.Errorf("campaign schema, identity or collection bounds are invalid")
	}
	if err != nil {
		root.Close()
		return c, nil, err
	}
	return c, root, nil
}

func campaignViewImage(root *os.Root, base string, file CampaignFile, limit int64) ([]byte, campaignImage, error) {
	if limit > campaignViewAssetLimit {
		limit = campaignViewAssetLimit
	}
	b, err := readCampaignViewFile(root, base, file.Path, limit)
	if err != nil {
		return nil, campaignImage{}, err
	}
	im, err := inspectCampaignImageBytes(b, file)
	if err != nil {
		return nil, im, err
	}
	if im.Format != "png" && im.Format != "jpeg" {
		return nil, im, fmt.Errorf("only PNG and JPEG images can be viewed")
	}
	return b, im, nil
}

func ReadCampaignWorkView(homePath, workID string, now time.Time) (CampaignWorkView, error) {
	out := CampaignWorkView{SchemaVersion: "compound.visual_campaign_view.v0.1", WorkID: workID, Campaigns: []CampaignView{}}
	paths, err := attachedCampaignPaths(homePath, workID)
	if err != nil {
		return out, err
	}
	for index, path := range paths {
		view := CampaignView{Index: index, WorkID: workID, Status: "unavailable", Assets: []CampaignAssetView{}}
		c, root, err := openCampaignView(path)
		if err != nil {
			view.Issue = err.Error()
			out.Campaigns = append(out.Campaigns, view)
			continue
		}
		base := filepath.Dir(path)
		view.ID, view.ProductID, view.CreatedAt = c.ID, c.ProductID, c.CreatedAt
		view.Context, view.Direction, view.Locales, view.Targets, view.Scenes = c.Context, c.Direction, c.Locales, c.Targets, c.Scenes
		view.CampaignSHA256 = campaignDigest(c)
		for assetIndex, asset := range c.Assets {
			a := CampaignAssetView{Index: assetIndex, Scene: asset.Scene, Locale: asset.Locale, Target: asset.Target, SHA256: asset.File.SHA256, Model: asset.Model, RenderReceipt: asset.RenderReceipt, Review: asset.Review}
			_, im, imageErr := campaignViewImage(root, base, asset.File, campaignViewAssetLimit)
			if imageErr != nil {
				a.Issue = imageErr.Error()
			} else {
				a.Available, a.Width, a.Height, a.MIMEType = true, im.Width, im.Height, "image/"+im.Format
			}
			view.Assets = append(view.Assets, a)
		}
		check, checkErr := checkVisualCampaign(c, now, func(file CampaignFile, limit int64) (campaignImage, error) {
			_, im, imageErr := campaignViewImage(root, base, file, limit)
			return im, imageErr
		})
		root.Close()
		view.Status = "draft"
		if checkErr != nil {
			view.CheckError = checkErr.Error()
		} else {
			view.Check = &check
			if check.Ready {
				view.Status = "checked"
			}
		}
		out.Campaigns = append(out.Campaigns, view)
	}
	return out, nil
}

func ReadCampaignWorkAsset(homePath, workID string, campaignIndex, assetIndex int) ([]byte, string, error) {
	paths, err := attachedCampaignPaths(homePath, workID)
	if err != nil {
		return nil, "", err
	}
	if campaignIndex < 0 || campaignIndex >= len(paths) {
		return nil, "", fmt.Errorf("campaign index is not attached to this work")
	}
	c, root, err := openCampaignView(paths[campaignIndex])
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	if assetIndex < 0 || assetIndex >= len(c.Assets) {
		return nil, "", fmt.Errorf("asset index is not present in the attached campaign")
	}
	b, im, err := campaignViewImage(root, filepath.Dir(paths[campaignIndex]), c.Assets[assetIndex].File, campaignViewAssetLimit)
	return b, "image/" + im.Format, err
}

func RunCampaignViewCLI(args []string, homePath string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(stdout, campaignViewUsage)
		return 0
	}
	fs := flag.NewFlagSet("campaign view", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workID := fs.String("work", "", "work owning the campaign attachment")
	campaign := fs.Int("campaign", 0, "attached campaign index")
	asset := fs.Int("asset", -1, "asset index; emits raw verified image bytes")
	fs.Bool("json", false, "metadata is always JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *workID == "" {
		fmt.Fprintln(stderr, campaignViewUsage)
		return 2
	}
	fail := func(err error) int { fmt.Fprintln(stderr, "product campaign view:", err); return 1 }
	assetRequested := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "asset" {
			assetRequested = true
		}
	})
	if assetRequested {
		b, _, err := ReadCampaignWorkAsset(homePath, *workID, *campaign, *asset)
		if err != nil {
			return fail(err)
		}
		if _, err = stdout.Write(b); err != nil {
			return fail(fmt.Errorf("write image: %w", err))
		}
		return 0
	}
	out, err := ReadCampaignWorkView(homePath, *workID, now)
	if err != nil {
		return fail(err)
	}
	if err = json.NewEncoder(stdout).Encode(out); err != nil {
		return fail(err)
	}
	return 0
}
