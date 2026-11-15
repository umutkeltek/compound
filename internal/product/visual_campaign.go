package product

// Campaigns are work artifacts, not another work queue or product registry. The
// compiler is deliberately pure: a rented executor chooses scenes and renders;
// Compound checks exact inputs/outputs and imports observations through Signal.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const VisualCampaignSchema = "compound.visual_campaign.v0.1"

type VisualCampaign struct {
	SchemaVersion string              `json:"schema_version"`
	ID            string              `json:"id"`
	ProductID     string              `json:"product_id"`
	CreatedAt     string              `json:"created_at"`
	Context       CampaignContext     `json:"context"`
	Direction     CampaignDirection   `json:"direction"`
	Locales       []CampaignLocale    `json:"locales"`
	Targets       []CampaignTarget    `json:"targets"`
	References    []CampaignReference `json:"references"`
	Scenes        []CampaignScene     `json:"scenes"`
	Assets        []CampaignAsset     `json:"assets"`
	Experiment    CampaignExperiment  `json:"experiment"`
}

type CampaignContext struct {
	Audience     string   `json:"audience"`
	Problem      string   `json:"problem"`
	Value        string   `json:"value"`
	EvidenceRefs []string `json:"evidence_refs"`
	Constraints  []string `json:"constraints"`
}

type CampaignDirection struct {
	Concept    string   `json:"concept"`
	Palette    string   `json:"palette"`
	Lighting   string   `json:"lighting"`
	Typography string   `json:"typography"`
	Layout     string   `json:"layout"`
	Avoid      []string `json:"avoid"`
}

type CampaignLocale struct {
	Tag       string `json:"tag"`
	Direction string `json:"direction"`
	Font      string `json:"font"`
}

// Requirements are supplied from dated platform evidence, never guessed from
// an app name or a permanent hardcoded phone-size table.
type CampaignTarget struct {
	ID              string `json:"id"`
	Surface         string `json:"surface"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	Format          string `json:"format"`
	AllowAlpha      bool   `json:"allow_alpha"`
	MaxBytes        int64  `json:"max_bytes"`
	RulesRef        string `json:"rules_ref"`
	RulesCheckedAt  string `json:"rules_checked_at"`
	RulesValidUntil string `json:"rules_valid_until"`
}

type CampaignFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type CampaignReference struct {
	ID        string       `json:"id"`
	Role      string       `json:"role"` // style, product, or capture
	Locale    string       `json:"locale,omitempty"`
	RightsRef string       `json:"rights_ref"`
	File      CampaignFile `json:"file"`
}

type CampaignCopy struct {
	Headline   string `json:"headline"`
	Caption    string `json:"caption"`
	CaptureRef string `json:"capture_ref"`
}

type CampaignScene struct {
	ID           string                  `json:"id"`
	Purpose      string                  `json:"purpose"`
	Setting      string                  `json:"setting"`
	Action       string                  `json:"action"`
	Composition  string                  `json:"composition"`
	ReferenceIDs []string                `json:"reference_ids"`
	Copy         map[string]CampaignCopy `json:"copy"`
}

type CampaignAsset struct {
	Scene          string              `json:"scene"`
	Locale         string              `json:"locale"`
	Target         string              `json:"target"`
	File           CampaignFile        `json:"file"`
	PromptID       string              `json:"prompt_id"`
	Model          string              `json:"model"`
	RenderReceipt  string              `json:"render_receipt"`
	AttachedSHA256 []string            `json:"attached_sha256"`
	Review         CampaignAssetReview `json:"review"`
}

// These are explicit evidence-backed attestations, not machine-computed taste
// scores. The checker reports who supplied them and binds them to exact bytes.
type CampaignAssetReview struct {
	AssetSHA256     string   `json:"asset_sha256"`
	Reviewer        string   `json:"reviewer"`
	EvidenceRefs    []string `json:"evidence_refs"`
	Coherence       bool     `json:"coherence"`
	CopyAndLayout   bool     `json:"copy_and_layout"`
	CaptureFidelity bool     `json:"capture_fidelity"`
	PlatformRules   bool     `json:"platform_rules"`
	NativeDetail    bool     `json:"native_detail"`
}

type CampaignExperiment struct {
	ID               string  `json:"id"`
	HypothesisID     string  `json:"hypothesis_id"`
	Metric           string  `json:"metric"`
	ControlSetSHA256 string  `json:"control_set_sha256"`
	StartsAt         string  `json:"starts_at"`
	EndsAt           string  `json:"ends_at"`
	MinPerArm        int64   `json:"min_per_arm"`
	MinAbsoluteLift  float64 `json:"min_absolute_lift"`
	TreatmentShare   float64 `json:"treatment_share"`
}

type CampaignPrompt struct {
	ID          string              `json:"id"`
	Scene       string              `json:"scene"`
	Locale      string              `json:"locale"`
	Target      string              `json:"target"`
	ImagePrompt string              `json:"image_prompt"`
	Attachments []CampaignReference `json:"attachments"`
	Composition CampaignCopy        `json:"composition"`
	LocaleRules CampaignLocale      `json:"locale_rules"`
	Export      CampaignTarget      `json:"export"`
}

type CampaignPromptPacket struct {
	BriefSHA256 string           `json:"brief_sha256"`
	Requests    []CampaignPrompt `json:"requests"`
	NextAction  string           `json:"next_action"`
}

var campaignHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var campaignLocalePattern = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

func campaignDigest(v any) string {
	b, _ := json.Marshal(v) // all callers use validated, finite JSON values
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func loadCampaignJSON(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one JSON document")
	}
	return nil
}

func LoadVisualCampaign(path string) (VisualCampaign, error) {
	var c VisualCampaign
	err := loadCampaignJSON(path, &c)
	return c, err
}

func campaignFilePath(base string, f CampaignFile) string {
	if filepath.IsAbs(f.Path) {
		return f.Path
	}
	return filepath.Join(base, f.Path)
}

func campaignNonempty(values ...string) bool {
	for _, s := range values {
		if strings.TrimSpace(s) == "" {
			return false
		}
	}
	return true
}

func campaignEvidence(refs []string) bool { return len(refs) > 0 && campaignNonempty(refs...) }

func validateCampaignBrief(c VisualCampaign, now time.Time) error {
	if c.SchemaVersion != VisualCampaignSchema || !campaignNonempty(c.ID, c.ProductID) {
		return fmt.Errorf("campaign schema, id and product_id required")
	}
	if !campaignNonempty(c.Context.Audience, c.Context.Problem, c.Context.Value) || !campaignEvidence(c.Context.EvidenceRefs) {
		return fmt.Errorf("verified audience, problem, value and evidence_refs required")
	}
	d := c.Direction
	if !campaignNonempty(d.Concept, d.Palette, d.Lighting, d.Typography, d.Layout) {
		return fmt.Errorf("shared concept, palette, lighting, typography and layout required")
	}
	created, err := time.Parse(time.RFC3339, c.CreatedAt)
	if err != nil || created.After(now) {
		return fmt.Errorf("invalid campaign created_at")
	}
	e := c.Experiment
	start, e1 := time.Parse(time.RFC3339, e.StartsAt)
	end, e2 := time.Parse(time.RFC3339, e.EndsAt)
	if !campaignNonempty(e.ID, e.HypothesisID, e.Metric) || !campaignHashPattern.MatchString(e.ControlSetSHA256) || e1 != nil || e2 != nil || !start.Before(end) || created.After(start) || e.MinPerArm < 100 || e.MinAbsoluteLift < 0 || e.MinAbsoluteLift >= 1 || e.TreatmentShare <= 0 || e.TreatmentShare >= 1 {
		return fmt.Errorf("predeclared experiment identity, control hash, fixed window, allocation and thresholds required (min_per_arm >= 100)")
	}
	if len(c.Locales) == 0 || len(c.Targets) == 0 || len(c.Scenes) == 0 {
		return fmt.Errorf("locales, targets and scenes required")
	}
	locales, targets, scenes, refs := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]CampaignReference{}
	for _, l := range c.Locales {
		if !campaignLocalePattern.MatchString(l.Tag) || locales[l.Tag] || (l.Direction != "ltr" && l.Direction != "rtl") || !campaignNonempty(l.Font) {
			return fmt.Errorf("invalid/duplicate locale %q or missing font/direction", l.Tag)
		}
		locales[l.Tag] = true
	}
	for _, t := range c.Targets {
		checked, a := time.Parse(time.RFC3339, t.RulesCheckedAt)
		until, b := time.Parse(time.RFC3339, t.RulesValidUntil)
		if !campaignNonempty(t.ID, t.Surface, t.RulesRef) || targets[t.ID] || t.Width <= 0 || t.Height <= 0 || t.Width > 20000 || t.Height > 20000 || (t.Format != "png" && t.Format != "jpeg") || t.MaxBytes <= 0 || a != nil || b != nil || checked.After(now) || !until.After(now) || !until.After(checked) {
			return fmt.Errorf("invalid or stale target requirements %q", t.ID)
		}
		targets[t.ID] = true
	}
	for _, r := range c.References {
		if !campaignNonempty(r.ID, r.RightsRef, r.File.Path) || refs[r.ID].ID != "" || !campaignHashPattern.MatchString(r.File.SHA256) || (r.Role != "style" && r.Role != "product" && r.Role != "capture") || (r.Role == "capture" && !locales[r.Locale]) {
			return fmt.Errorf("invalid/duplicate reference %q or missing capture locale/rights/hash", r.ID)
		}
		refs[r.ID] = r
	}
	for _, s := range c.Scenes {
		if !campaignNonempty(s.ID, s.Purpose, s.Setting, s.Action, s.Composition) || scenes[s.ID] || len(s.ReferenceIDs) == 0 {
			return fmt.Errorf("invalid/duplicate scene %q or missing image references", s.ID)
		}
		scenes[s.ID] = true
		seen := map[string]bool{}
		for _, id := range s.ReferenceIDs {
			if refs[id].ID == "" || seen[id] || refs[id].Role == "capture" {
				return fmt.Errorf("scene %s reference %s must be a unique style/product image; captures belong to locale copy", s.ID, id)
			}
			seen[id] = true
		}
		if len(s.Copy) != len(locales) {
			return fmt.Errorf("scene %s copy must match every requested locale", s.ID)
		}
		for locale := range locales {
			copy, ok := s.Copy[locale]
			r := refs[copy.CaptureRef]
			if !ok || !campaignNonempty(copy.Headline, copy.Caption) || r.Role != "capture" || r.Locale != locale {
				return fmt.Errorf("scene %s requires copy and actual %s capture", s.ID, locale)
			}
		}
	}
	return nil
}

func CompileCampaignPrompts(c VisualCampaign, base string, now time.Time) (CampaignPromptPacket, error) {
	var out CampaignPromptPacket
	if err := validateCampaignBrief(c, now); err != nil {
		return out, err
	}
	refs := map[string]CampaignReference{}
	for _, r := range c.References {
		if _, err := inspectCampaignImage(base, r.File, 64<<20); err != nil {
			return out, fmt.Errorf("reference %s: %w", r.ID, err)
		}
		refs[r.ID] = r
	}
	brief := c
	brief.Assets = nil
	out.BriefSHA256 = campaignDigest(brief)
	shared, _ := json.Marshal(struct {
		Context   CampaignContext
		Direction CampaignDirection
	}{c.Context, c.Direction})
	for _, s := range c.Scenes {
		for _, l := range c.Locales {
			for _, t := range c.Targets {
				p := CampaignPrompt{Scene: s.ID, Locale: l.Tag, Target: t.ID, Composition: s.Copy[l.Tag], LocaleRules: l, Export: t}
				for _, id := range append(append([]string{}, s.ReferenceIDs...), p.Composition.CaptureRef) {
					p.Attachments = append(p.Attachments, refs[id])
				}
				p.ImagePrompt = fmt.Sprintf("Shared campaign contract (keep consistent across this set): %s\nScene purpose: %s\nSetting: %s\nAction: %s\nComposition: %s\nCreate language-light artwork with space for the specified layout. Do not bake in typography or invent app UI. Use the attached style/product images for their declared roles. The attached %s app capture is the truthful UI source; composite its original pixels after generation. Render enough native detail for %dx%d. Final copy, font, direction, capture and export are in the composition packet; they must be applied and reviewed after rendering.", shared, s.Purpose, s.Setting, s.Action, s.Composition, l.Tag, t.Width, t.Height)
				p.ID = campaignDigest(struct {
					Brief   string
					Request CampaignPrompt
				}{out.BriefSHA256, p})
				out.Requests = append(out.Requests, p)
			}
		}
	}
	out.NextAction = "Execute requests using authorized image tools with every attachment; composite localized copy and original capture; record actual model, render receipt and byte-bound review; run campaign check. Reuse artwork across locales only after verifying layout and cultural fit."
	return out, nil
}
