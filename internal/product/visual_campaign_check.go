package product

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"time"
)

type campaignImage struct {
	Width, Height int
	Format        string
	Bytes         int64
	Alpha         bool
}

func inspectCampaignImage(base string, file CampaignFile, limit int64) (campaignImage, error) {
	var out campaignImage
	if !campaignHashPattern.MatchString(file.SHA256) || file.Path == "" {
		return out, fmt.Errorf("file path and SHA256 required")
	}
	f, err := os.Open(campaignFilePath(base, file))
	if err != nil {
		return out, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return out, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit || st.Size() <= 0 {
		return out, fmt.Errorf("image must be a nonempty regular file within %d bytes", limit)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return out, err
	}
	if int64(len(b)) > limit {
		return out, fmt.Errorf("image grew past size limit")
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != file.SHA256 {
		return out, fmt.Errorf("image bytes changed: SHA256 mismatch")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return out, fmt.Errorf("decode image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
		return out, fmt.Errorf("image exceeds 40 megapixel decode budget")
	}
	// Decode pixels as well: a valid header is not evidence of a complete file.
	if _, _, err = image.Decode(bytes.NewReader(b)); err != nil {
		return out, fmt.Errorf("decode image pixels: %w", err)
	}
	out = campaignImage{Width: cfg.Width, Height: cfg.Height, Format: format, Bytes: int64(len(b))}
	if format == "png" {
		// Inspect channel/transparency metadata, even if all alpha values are opaque.
		out.Alpha = len(b) > 25 && (b[25] == 4 || b[25] == 6)
		for offset := 8; offset+12 <= len(b); {
			n := int64(binary.BigEndian.Uint32(b[offset : offset+4]))
			if n > int64(len(b)-offset-12) {
				return out, fmt.Errorf("invalid PNG chunk")
			}
			if string(b[offset+4:offset+8]) == "tRNS" {
				out.Alpha = true
			}
			offset += int(n) + 12
		}
	}
	return out, nil
}

type CampaignCheck struct {
	CampaignSHA256 string   `json:"campaign_sha256"`
	BriefSHA256    string   `json:"brief_sha256"`
	Ready          bool     `json:"ready"`
	ExpectedAssets int      `json:"expected_assets"`
	VerifiedAssets int      `json:"verified_assets"`
	Issues         []string `json:"issues"`
	ReviewBasis    string   `json:"review_basis"`
	NextAction     string   `json:"next_action"`
}

func CheckVisualCampaign(c VisualCampaign, base string, now time.Time) (CampaignCheck, error) {
	out := CampaignCheck{CampaignSHA256: campaignDigest(c), Issues: []string{}, ReviewBasis: "Dimensions, file integrity and attachment identity are checked mechanically. Taste, translation, UI truth, native detail and platform-specific content rules require the named evidence-backed reviewer attestations; they are not inferred from resolution."}
	packet, err := CompileCampaignPrompts(c, base, now)
	if err != nil {
		return out, err
	}
	out.BriefSHA256, out.ExpectedAssets = packet.BriefSHA256, len(packet.Requests)
	requests := map[string]CampaignPrompt{}
	for _, p := range packet.Requests {
		requests[campaignAssetKey(p.Scene, p.Locale, p.Target)] = p
	}
	seen := map[string]bool{}
	for _, a := range c.Assets {
		key := campaignAssetKey(a.Scene, a.Locale, a.Target)
		p, ok := requests[key]
		if !ok || seen[key] {
			return out, fmt.Errorf("unknown or duplicate asset %s", key)
		}
		seen[key] = true
		before := len(out.Issues)
		issue := func(s string) { out.Issues = append(out.Issues, key+": "+s) }
		if a.PromptID != p.ID {
			issue("stale prompt identity; re-render/review after brief changes")
		}
		limit := p.Export.MaxBytes
		if limit > 64<<20 {
			limit = 64 << 20
		}
		im, e := inspectCampaignImage(base, a.File, limit)
		if e != nil {
			issue(e.Error())
		} else if im.Width != p.Export.Width || im.Height != p.Export.Height || im.Format != p.Export.Format || (!p.Export.AllowAlpha && im.Alpha) {
			issue(fmt.Sprintf("export mismatch: got %dx%d %s alpha=%t", im.Width, im.Height, im.Format, im.Alpha))
		}
		attached := map[string]bool{}
		for _, hash := range a.AttachedSHA256 {
			attached[hash] = true
		}
		for _, r := range p.Attachments {
			if !attached[r.File.SHA256] {
				issue("missing actual render attachment " + r.ID)
			}
		}
		if !campaignNonempty(a.Model, a.RenderReceipt) {
			issue("actual model and render receipt required (unknown must not be invented)")
		}
		r := a.Review
		if r.AssetSHA256 != a.File.SHA256 || !campaignNonempty(r.Reviewer) || !campaignEvidence(r.EvidenceRefs) || !r.Coherence || !r.CopyAndLayout || !r.CaptureFidelity || !r.PlatformRules || !r.NativeDetail {
			issue("exact-asset review incomplete: coherence, locale/layout, capture, platform and native detail must all pass")
		}
		if len(out.Issues) == before {
			out.VerifiedAssets++
		}
	}
	for _, p := range packet.Requests {
		key := campaignAssetKey(p.Scene, p.Locale, p.Target)
		if !seen[key] {
			out.Issues = append(out.Issues, key+": export missing")
		}
	}
	out.Ready = len(out.Issues) == 0
	out.NextAction = "Repair only failed exports and recheck; preserve rejected renders as work evidence."
	if out.Ready {
		out.NextAction = "Freeze campaign JSON and exact assets as work artifacts. Bind this campaign hash to platform experiment assignment before exposure; execute release through existing authority; collect results after the declared window."
	}
	return out, nil
}

func campaignAssetKey(scene, locale, target string) string {
	return scene + "/" + locale + "/" + target
}
