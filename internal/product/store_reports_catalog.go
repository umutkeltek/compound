package product

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// StoreReportCatalogSchemaVersion is the version of the store report catalog
// files.
const StoreReportCatalogSchemaVersion = "compound.store_report_catalog.v0.1"

// StoreReportCatalogOverlayPath is the operator's optional catalog overlay,
// relative to the Compound home. It merges by report id.
const StoreReportCatalogOverlayPath = "config/store-reports.yaml"

// The store report catalog is data, not code: one row per report a store
// publishes, with where its capture lands and what it can feed. Files load in
// name order.
//
//go:embed store_reports/*.yaml
var storeReportCatalogFS embed.FS

// Allowed values. A second store adds a provider value and rows, not code.
var (
	storeReportFamilies   = []string{"sales", "finance", "analytics", "reviews", "ads", "performance"}
	storeReportGrains     = []string{"day", "week", "month", "fiscal_month"}
	storeReportExactness  = []string{"exact", "noised", "thresholded", "opt_in_sample"}
	storeReportDayScopes  = []string{"account", "app"}
	storeReportIDRE       = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	storeReportCellNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// StoreReportPublished says when the store publishes a period, in plain words
// and as a number. A day (or period end) is final once more than LagDays have
// passed after it.
type StoreReportPublished struct {
	Text    string `json:"text" yaml:"text"`
	LagDays int    `json:"lag_days" yaml:"lag_days"`
}

// StoreReportSeriesCapture is a JSONL series a collector keeps: one row per
// app and day. DayScope "account" means one row on any app answers the day
// for every app (the store reports all apps at once, and an app with no
// activity has no row); "app" means each app's own rows answer only its days.
// Cells, when set, name row fields whose metric_evidence must say "observed"
// for the row to count.
type StoreReportSeriesCapture struct {
	Path      string   `json:"path" yaml:"path"`
	AppField  string   `json:"app_field" yaml:"app_field"`
	DayField  string   `json:"day_field" yaml:"day_field"`
	DayScope  string   `json:"day_scope" yaml:"day_scope"`
	DaysIndex string   `json:"days_index,omitempty" yaml:"days_index,omitempty"`
	Cells     []string `json:"cells,omitempty" yaml:"cells,omitempty"`
}

// StoreReportFilesCapture finds raw report files by glob; PeriodPattern has
// one group that captures the period (YYYY-MM-DD for days, YYYY-MM for
// months and Apple fiscal months) from the file name.
type StoreReportFilesCapture struct {
	Globs         []string `json:"globs" yaml:"globs"`
	PeriodPattern string   `json:"period_pattern" yaml:"period_pattern"`
}

// StoreReportCapture is how a report is found on disk. Neither set means the
// report is not captured (written `capture: none` in the catalog).
type StoreReportCapture struct {
	Series *StoreReportSeriesCapture `json:"series,omitempty" yaml:"series,omitempty"`
	Files  *StoreReportFilesCapture  `json:"files,omitempty" yaml:"files,omitempty"`
}

// None reports whether the catalog says nothing captures the report.
func (c StoreReportCapture) None() bool { return c.Series == nil && c.Files == nil }

// UnmarshalYAML accepts the scalar `none` or a mapping with series and/or
// files, and rejects unknown keys.
func (c *StoreReportCapture) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		if strings.TrimSpace(n.Value) != "none" {
			return fmt.Errorf("capture: want none or a mapping with series and/or files, got %q", n.Value)
		}
		*c = StoreReportCapture{}
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("capture: want none or a mapping")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, n.Content[i+1]
		switch key {
		case "series":
			var s StoreReportSeriesCapture
			if err := decodeStrictNode(val, &s); err != nil {
				return fmt.Errorf("capture.series: %w", err)
			}
			c.Series = &s
		case "files":
			var f StoreReportFilesCapture
			if err := decodeStrictNode(val, &f); err != nil {
				return fmt.Errorf("capture.files: %w", err)
			}
			c.Files = &f
		default:
			return fmt.Errorf("capture: unknown key %q", key)
		}
	}
	if c.None() {
		return fmt.Errorf("capture: an empty mapping is not allowed; write none")
	}
	return nil
}

// decodeStrictNode decodes a node and rejects keys the target does not know.
func decodeStrictNode(n *yaml.Node, out any) error {
	raw, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	return dec.Decode(out)
}

// StoreReportFeeds names the growth KPI keys and growth areas a report can
// feed.
type StoreReportFeeds struct {
	Kpis  []string `json:"kpis" yaml:"kpis"`
	Areas []string `json:"areas" yaml:"areas"`
}

// StoreReport is one catalog row.
type StoreReport struct {
	ID             string               `json:"id" yaml:"id"`
	Provider       string               `json:"provider" yaml:"provider"`
	Family         string               `json:"family" yaml:"family"`
	Title          string               `json:"title" yaml:"title"`
	Question       string               `json:"question" yaml:"question"`
	Grain          string               `json:"grain" yaml:"grain"`
	PublishedAfter StoreReportPublished `json:"published_after" yaml:"published_after"`
	KeptFor        string               `json:"kept_for" yaml:"kept_for"`
	Exactness      string               `json:"exactness" yaml:"exactness"`
	ExactnessNote  string               `json:"exactness_note" yaml:"exactness_note"`
	Capture        StoreReportCapture   `json:"capture" yaml:"capture"`
	Feeds          StoreReportFeeds     `json:"feeds" yaml:"feeds"`
}

type storeReportCatalogFile struct {
	SchemaVersion string        `yaml:"schema_version"`
	Reports       []StoreReport `yaml:"reports"`
}

// storeReportOverlayRow is an overlay row: every field optional, so an
// overlay can change one field of an embedded row. Capture and feeds replace
// the embedded value when set.
type storeReportOverlayRow struct {
	ID             string                `yaml:"id"`
	Provider       string                `yaml:"provider"`
	Family         string                `yaml:"family"`
	Title          string                `yaml:"title"`
	Question       string                `yaml:"question"`
	Grain          string                `yaml:"grain"`
	PublishedAfter *StoreReportPublished `yaml:"published_after"`
	KeptFor        string                `yaml:"kept_for"`
	Exactness      string                `yaml:"exactness"`
	ExactnessNote  string                `yaml:"exactness_note"`
	Capture        *StoreReportCapture   `yaml:"capture"`
	Feeds          *StoreReportFeeds     `yaml:"feeds"`
}

type storeReportOverlayFile struct {
	SchemaVersion string                  `yaml:"schema_version"`
	Reports       []storeReportOverlayRow `yaml:"reports"`
}

// storeReportCatalog is the loaded, validated catalog.
type storeReportCatalog struct {
	SchemaVersion string
	Reports       []StoreReport
	byID          map[string]int
	Overlay       string
	OverlayError  string
}

// StoreReportCatalogInfo says which catalog was used.
type StoreReportCatalogInfo struct {
	SchemaVersion string `json:"schema_version"`
	Source        string `json:"source"`
	Overlay       string `json:"overlay"`
	OverlayError  string `json:"overlay_error"`
	Reports       int    `json:"reports"`
	Captured      int    `json:"captured"`
}

func (c *storeReportCatalog) info() StoreReportCatalogInfo {
	captured := 0
	for _, r := range c.Reports {
		if !r.Capture.None() {
			captured++
		}
	}
	return StoreReportCatalogInfo{
		SchemaVersion: c.SchemaVersion, Source: "embedded", Overlay: c.Overlay, OverlayError: c.OverlayError,
		Reports: len(c.Reports), Captured: captured,
	}
}

// report returns the row for id.
func (c *storeReportCatalog) report(id string) (StoreReport, bool) {
	i, ok := c.byID[id]
	if !ok {
		return StoreReport{}, false
	}
	return c.Reports[i], true
}

var (
	embeddedStoreReportCatalogOnce sync.Once
	embeddedStoreReportCatalog     *storeReportCatalog
	embeddedStoreReportCatalogErr  error
)

// loadEmbeddedStoreReportCatalog parses and validates the shipped catalog
// once, against the embedded growth catalog.
func loadEmbeddedStoreReportCatalog() (*storeReportCatalog, error) {
	embeddedStoreReportCatalogOnce.Do(func() {
		growthCat, err := loadEmbeddedGrowthCatalog()
		if err != nil {
			embeddedStoreReportCatalogErr = err
			return
		}
		files, err := readStoreReportCatalogFS(storeReportCatalogFS)
		if err != nil {
			embeddedStoreReportCatalogErr = err
			return
		}
		embeddedStoreReportCatalog, embeddedStoreReportCatalogErr = buildStoreReportCatalog(files, growthCat)
	})
	if embeddedStoreReportCatalogErr != nil {
		return nil, embeddedStoreReportCatalogErr
	}
	return embeddedStoreReportCatalog.clone(), nil
}

// loadStoreReportCatalog is the embedded catalog with the home's overlay
// merged in. A missing overlay is normal. A broken overlay never breaks the
// reader: the embedded catalog is served and the error is reported.
func loadStoreReportCatalog(homePath string) (*storeReportCatalog, error) {
	base, err := loadEmbeddedStoreReportCatalog()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(homePath) == "" {
		return base, nil
	}
	raw, err := os.ReadFile(filepath.Join(homePath, filepath.FromSlash(StoreReportCatalogOverlayPath)))
	if errors.Is(err, fs.ErrNotExist) {
		return base, nil
	}
	if err == nil {
		var growthCat *growthAreaCatalog
		if growthCat, err = loadGrowthCatalog(homePath); err == nil {
			var overlay storeReportOverlayFile
			if overlay, err = parseStoreReportOverlay(raw); err == nil {
				var merged *storeReportCatalog
				if merged, err = base.withOverlay(overlay, growthCat); err == nil {
					merged.Overlay = StoreReportCatalogOverlayPath
					return merged, nil
				}
			}
		}
	}
	base.OverlayError = StoreReportCatalogOverlayPath + ": " + err.Error()
	return base, nil
}

func readStoreReportCatalogFS(fsys fs.FS) ([]storeReportCatalogFile, error) {
	names, err := fs.Glob(fsys, "store_reports/*.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var files []storeReportCatalogFile
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		f, err := parseStoreReportCatalogFile(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		files = append(files, f)
	}
	return files, nil
}

func parseStoreReportCatalogFile(raw []byte) (storeReportCatalogFile, error) {
	var f storeReportCatalogFile
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return storeReportCatalogFile{}, err
	}
	return f, nil
}

func parseStoreReportOverlay(raw []byte) (storeReportOverlayFile, error) {
	var f storeReportOverlayFile
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return storeReportOverlayFile{}, err
	}
	return f, nil
}

// buildStoreReportCatalog folds catalog files in order and validates them.
func buildStoreReportCatalog(files []storeReportCatalogFile, growthCat *growthAreaCatalog) (*storeReportCatalog, error) {
	c := &storeReportCatalog{}
	for _, f := range files {
		if f.SchemaVersion != "" {
			if c.SchemaVersion != "" && c.SchemaVersion != f.SchemaVersion {
				return nil, fmt.Errorf("store report catalog files disagree on schema_version: %q and %q", c.SchemaVersion, f.SchemaVersion)
			}
			c.SchemaVersion = f.SchemaVersion
		}
		c.Reports = append(c.Reports, f.Reports...)
	}
	if err := c.finish(growthCat); err != nil {
		return nil, err
	}
	return c, nil
}

// withOverlay merges overlay rows by id and validates the result. A known id
// overrides the fields the overlay sets; a new id adds a report.
func (c *storeReportCatalog) withOverlay(o storeReportOverlayFile, growthCat *growthAreaCatalog) (*storeReportCatalog, error) {
	if o.SchemaVersion != "" && o.SchemaVersion != c.SchemaVersion {
		return nil, fmt.Errorf("schema_version = %q, want %q", o.SchemaVersion, c.SchemaVersion)
	}
	out := c.clone()
	for _, row := range o.Reports {
		i, ok := out.byID[row.ID]
		if !ok {
			out.Reports = append(out.Reports, StoreReport{ID: row.ID})
			i = len(out.Reports) - 1
			out.byID[row.ID] = i
		}
		cur := &out.Reports[i]
		for _, f := range []struct {
			dst *string
			src string
		}{
			{&cur.Provider, row.Provider}, {&cur.Family, row.Family}, {&cur.Title, row.Title}, {&cur.Question, row.Question},
			{&cur.Grain, row.Grain}, {&cur.KeptFor, row.KeptFor}, {&cur.Exactness, row.Exactness}, {&cur.ExactnessNote, row.ExactnessNote},
		} {
			if f.src != "" {
				*f.dst = f.src
			}
		}
		if row.PublishedAfter != nil {
			cur.PublishedAfter = *row.PublishedAfter
		}
		if row.Capture != nil {
			cur.Capture = row.Capture.clone()
		}
		if row.Feeds != nil {
			cur.Feeds = row.Feeds.clone()
		}
	}
	if err := out.finish(growthCat); err != nil {
		return nil, err
	}
	return out, nil
}

// finish indexes and validates the catalog.
func (c *storeReportCatalog) finish(growthCat *growthAreaCatalog) error {
	if c.SchemaVersion != StoreReportCatalogSchemaVersion {
		return fmt.Errorf("store report catalog schema_version = %q, want %q", c.SchemaVersion, StoreReportCatalogSchemaVersion)
	}
	if len(c.Reports) == 0 {
		return fmt.Errorf("store report catalog has no reports")
	}
	c.byID = map[string]int{}
	for i, r := range c.Reports {
		if _, dup := c.byID[r.ID]; dup {
			return fmt.Errorf("report %q listed twice", r.ID)
		}
		c.byID[r.ID] = i
		if err := validateStoreReport(r, growthCat); err != nil {
			return fmt.Errorf("report %q: %w", r.ID, err)
		}
	}
	return nil
}

func validateStoreReport(r StoreReport, growthCat *growthAreaCatalog) error {
	if !storeReportIDRE.MatchString(r.ID) {
		return fmt.Errorf("id must match %s", storeReportIDRE)
	}
	if !storeReportIDRE.MatchString(r.Provider) {
		return fmt.Errorf("provider %q must match %s", r.Provider, storeReportIDRE)
	}
	for _, f := range []struct{ name, value string }{
		{"title", r.Title}, {"question", r.Question}, {"published_after.text", r.PublishedAfter.Text},
		{"kept_for", r.KeptFor}, {"exactness_note", r.ExactnessNote},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%s is required", f.name)
		}
	}
	for _, f := range []struct {
		name, value string
		allowed     []string
	}{
		{"family", r.Family, storeReportFamilies}, {"grain", r.Grain, storeReportGrains}, {"exactness", r.Exactness, storeReportExactness},
	} {
		if !containsString(f.allowed, f.value) {
			return fmt.Errorf("%s %q must be one of %s", f.name, f.value, strings.Join(f.allowed, ", "))
		}
	}
	if r.PublishedAfter.LagDays < 0 {
		return fmt.Errorf("published_after.lag_days must not be negative")
	}
	if s := r.Capture.Series; s != nil {
		if err := validateStoreReportPath("capture.series.path", s.Path); err != nil {
			return err
		}
		if s.DaysIndex != "" {
			if err := validateStoreReportPath("capture.series.days_index", s.DaysIndex); err != nil {
				return err
			}
		}
		if strings.TrimSpace(s.AppField) == "" || strings.TrimSpace(s.DayField) == "" {
			return fmt.Errorf("capture.series needs app_field and day_field")
		}
		if !containsString(storeReportDayScopes, s.DayScope) {
			return fmt.Errorf("capture.series.day_scope %q must be one of %s", s.DayScope, strings.Join(storeReportDayScopes, ", "))
		}
		if r.Grain != "day" {
			return fmt.Errorf("capture.series needs grain day")
		}
		for _, cell := range s.Cells {
			if !storeReportCellNameRE.MatchString(cell) {
				return fmt.Errorf("capture.series.cells: %q is not a field name", cell)
			}
		}
	}
	if f := r.Capture.Files; f != nil {
		if len(f.Globs) == 0 {
			return fmt.Errorf("capture.files needs at least one glob")
		}
		if r.Grain == "week" {
			return fmt.Errorf("capture.files needs grain day, month or fiscal_month")
		}
		for _, g := range f.Globs {
			if err := validateStoreReportPath("capture.files.globs", g); err != nil {
				return err
			}
			if _, err := path.Match(g, ""); err != nil {
				return fmt.Errorf("capture.files.globs: %q: %w", g, err)
			}
		}
		re, err := regexp.Compile(f.PeriodPattern)
		if err != nil {
			return fmt.Errorf("capture.files.period_pattern: %w", err)
		}
		if re.NumSubexp() != 1 {
			return fmt.Errorf("capture.files.period_pattern must have exactly one group, has %d", re.NumSubexp())
		}
	}
	for _, k := range r.Feeds.Kpis {
		if !containsString(GrowthKpiKeys, k) {
			return fmt.Errorf("feeds.kpis: %q is not a growth KPI key", k)
		}
	}
	for _, a := range r.Feeds.Areas {
		if growthCat == nil {
			break
		}
		if _, ok := growthCat.area(a); !ok {
			return fmt.Errorf("feeds.areas: %q is not a growth area", a)
		}
	}
	return nil
}

// validateStoreReportPath accepts a slash path relative to the Compound home
// that stays inside it.
func validateStoreReportPath(field, p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || strings.Contains(p, `\`) {
		return fmt.Errorf("%s %q must be relative to the Compound home", field, p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "" {
			return fmt.Errorf("%s %q must stay inside the Compound home", field, p)
		}
	}
	return nil
}

func (c *storeReportCatalog) clone() *storeReportCatalog {
	out := &storeReportCatalog{SchemaVersion: c.SchemaVersion, Overlay: c.Overlay, OverlayError: c.OverlayError, byID: map[string]int{}}
	for i, r := range c.Reports {
		out.Reports = append(out.Reports, r.clone())
		out.byID[r.ID] = i
	}
	return out
}

func (r StoreReport) clone() StoreReport {
	out := r
	out.Capture = r.Capture.clone()
	out.Feeds = r.Feeds.clone()
	return out
}

func (c StoreReportCapture) clone() StoreReportCapture {
	var out StoreReportCapture
	if c.Series != nil {
		s := *c.Series
		s.Cells = append([]string(nil), c.Series.Cells...)
		out.Series = &s
	}
	if c.Files != nil {
		f := *c.Files
		f.Globs = append([]string(nil), c.Files.Globs...)
		out.Files = &f
	}
	return out
}

func (f StoreReportFeeds) clone() StoreReportFeeds {
	return StoreReportFeeds{Kpis: append([]string{}, f.Kpis...), Areas: append([]string{}, f.Areas...)}
}
