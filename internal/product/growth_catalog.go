package product

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// GrowthCatalogSchemaVersion is the version of the growth area catalog files.
const GrowthCatalogSchemaVersion = "compound.product_growth_catalog.v0.1"

// The growth catalog is data, not code: one core file with the groups and the
// profile values, then one file per group with its areas. Files load in name
// order, so the core file sorts first.
//
//go:embed growth_catalog/*.yaml
var growthCatalogFS embed.FS

// growthBranches are the product-pipeline branches an area may route to. A
// test checks this list against the branch files of the pipeline bundle.
var growthBranches = map[string]bool{
	"research": true, "strategy": true, "experiment": true, "ux": true, "ui": true,
	"visuals": true, "spec": true, "delivery": true, "qa": true, "release": true,
	"distribution": true, "activation": true, "retention": true, "measurement": true, "learning": true,
}

// growthCustomEvaluators are the evidence evaluators the engine knows by key.
var growthCustomEvaluators = map[string]bool{
	"feedback": true, "funnel": true, "experiments": true, "organic": true,
	"search_ads": true, "web_traffic": true, "pricing": true, "unit_economics": true,
}

// growthProfileDimensions are the profile dimensions an area may restrict on,
// in contract order. Stage is free text and never restricts an area.
var growthProfileDimensions = []string{"kinds", "audiences", "revenue_models", "objectives"}

var (
	growthIDRE    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	growthSkillRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// GrowthAreaGroup is one catalog group: a broad question and the pipeline
// branch its areas route to by default. It is unrelated to the product group
// that merges spelling variants of one product.
type GrowthAreaGroup struct {
	ID       string `json:"id" yaml:"id"`
	Title    string `json:"title" yaml:"title"`
	Question string `json:"question" yaml:"question"`
	Branch   string `json:"branch" yaml:"branch"`
}

// growthEvidenceDef says what counts as evidence for an area or a form.
type growthEvidenceDef struct {
	Metrics    []string `yaml:"metrics"`
	Capped     []string `yaml:"capped"`
	Companion  []string `yaml:"companion"`
	Source     string   `yaml:"source"`
	Record     []string `yaml:"record"`
	Custom     string   `yaml:"custom"`
	Paywall    bool     `yaml:"paywall"`
	CapPartial bool     `yaml:"cap_partial"`
}

func (e growthEvidenceDef) empty() bool {
	return len(e.Metrics) == 0 && len(e.Capped) == 0 && len(e.Companion) == 0 && e.Source == "" &&
		len(e.Record) == 0 && e.Custom == "" && !e.Paywall && !e.CapPartial
}

// growthFormDef is the form an area takes for one product kind. Unset fields
// inherit from the area's base row.
type growthFormDef struct {
	Title    string             `yaml:"title"`
	Question string             `yaml:"question"`
	Measure  string             `yaml:"measure"`
	Evidence *growthEvidenceDef `yaml:"evidence"`
	Skills   []string           `yaml:"skills"`
}

// growthAreaDef is one catalog row.
type growthAreaDef struct {
	ID       string                   `yaml:"id"`
	Group    string                   `yaml:"group"`
	Title    string                   `yaml:"title"`
	Question string                   `yaml:"question"`
	Measure  string                   `yaml:"measure"`
	Applies  map[string][]string      `yaml:"applies"`
	Needs    []string                 `yaml:"needs"`
	Branch   string                   `yaml:"branch"`
	Skills   []string                 `yaml:"skills"`
	Evidence growthEvidenceDef        `yaml:"evidence"`
	Kinds    map[string]growthFormDef `yaml:"kinds"`
}

type growthCatalogFile struct {
	SchemaVersion string              `yaml:"schema_version"`
	Groups        []GrowthAreaGroup   `yaml:"groups"`
	Profile       map[string][]string `yaml:"profile"`
	Areas         []growthAreaDef     `yaml:"areas"`
}

// growthAreaCatalog is the loaded, validated catalog.
type growthAreaCatalog struct {
	SchemaVersion string
	Groups        []GrowthAreaGroup
	Profile       map[string][]string
	Areas         []growthAreaDef
	byID          map[string]int
	groupIdx      map[string]int
	Overlay       string
	OverlayError  string
}

// area returns the catalog row for id.
func (c *growthAreaCatalog) area(id string) (growthAreaDef, bool) {
	i, ok := c.byID[id]
	if !ok {
		return growthAreaDef{}, false
	}
	return c.Areas[i], true
}

// branch is the area's branch, falling back to its group's.
func (c *growthAreaCatalog) branch(a growthAreaDef) string {
	if a.Branch != "" {
		return a.Branch
	}
	if i, ok := c.groupIdx[a.Group]; ok {
		return c.Groups[i].Branch
	}
	return ""
}

var (
	embeddedGrowthCatalogOnce sync.Once
	embeddedGrowthCatalog     *growthAreaCatalog
	embeddedGrowthCatalogErr  error
)

// loadEmbeddedGrowthCatalog parses and validates the shipped catalog once.
func loadEmbeddedGrowthCatalog() (*growthAreaCatalog, error) {
	embeddedGrowthCatalogOnce.Do(func() {
		files, err := readGrowthCatalogFS(growthCatalogFS)
		if err != nil {
			embeddedGrowthCatalogErr = err
			return
		}
		embeddedGrowthCatalog, embeddedGrowthCatalogErr = buildGrowthCatalog(files)
	})
	if embeddedGrowthCatalogErr != nil {
		return nil, embeddedGrowthCatalogErr
	}
	return embeddedGrowthCatalog.clone(), nil
}

// GrowthCatalogOverlayPath is the operator's optional catalog overlay,
// relative to the Compound home.
const GrowthCatalogOverlayPath = "config/product-growth-catalog.yaml"

// loadGrowthCatalog is the embedded catalog with the home's overlay merged in.
// A missing overlay is normal. A broken overlay never breaks the projection:
// the embedded catalog is served and the error is reported on the catalog.
func loadGrowthCatalog(homePath string) (*growthAreaCatalog, error) {
	base, err := loadEmbeddedGrowthCatalog()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(homePath) == "" {
		return base, nil
	}
	raw, err := os.ReadFile(filepath.Join(homePath, filepath.FromSlash(GrowthCatalogOverlayPath)))
	if errors.Is(err, fs.ErrNotExist) {
		return base, nil
	}
	if err == nil {
		var overlay growthCatalogFile
		if overlay, err = parseGrowthCatalogFile(raw); err == nil {
			var merged *growthAreaCatalog
			if merged, err = base.withOverlay(overlay); err == nil {
				merged.Overlay = GrowthCatalogOverlayPath
				return merged, nil
			}
		}
	}
	base.OverlayError = GrowthCatalogOverlayPath + ": " + err.Error()
	return base, nil
}

// withOverlay merges an overlay file by id and validates the result. An
// overlay area with an existing id overrides the fields it sets and appends
// its skills; a new id adds an area. Groups and profile values merge the same
// way.
func (c *growthAreaCatalog) withOverlay(o growthCatalogFile) (*growthAreaCatalog, error) {
	if o.SchemaVersion != "" && o.SchemaVersion != c.SchemaVersion {
		return nil, fmt.Errorf("schema_version = %q, want %q", o.SchemaVersion, c.SchemaVersion)
	}
	out := c.clone()
	for _, g := range o.Groups {
		i, ok := out.groupIdx[g.ID]
		if !ok {
			out.Groups = append(out.Groups, g)
			out.groupIdx[g.ID] = len(out.Groups) - 1
			continue
		}
		cur := &out.Groups[i]
		if g.Title != "" {
			cur.Title = g.Title
		}
		if g.Question != "" {
			cur.Question = g.Question
		}
		if g.Branch != "" {
			cur.Branch = g.Branch
		}
	}
	for dim, values := range o.Profile {
		out.Profile[dim] = appendUnique(out.Profile[dim], values...)
	}
	for _, a := range o.Areas {
		i, ok := out.byID[a.ID]
		if !ok {
			out.Areas = append(out.Areas, a.clone())
			out.byID[a.ID] = len(out.Areas) - 1
			continue
		}
		out.Areas[i] = out.Areas[i].overlaid(a)
	}
	if err := out.finish(); err != nil {
		return nil, err
	}
	return out, nil
}

// overlaid applies one overlay row to an existing row.
func (a growthAreaDef) overlaid(o growthAreaDef) growthAreaDef {
	out := a.clone()
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&out.Group, o.Group}, {&out.Title, o.Title}, {&out.Question, o.Question}, {&out.Measure, o.Measure}, {&out.Branch, o.Branch},
	} {
		if f.src != "" {
			*f.dst = f.src
		}
	}
	if o.Applies != nil {
		out.Applies = o.clone().Applies
	}
	if o.Needs != nil {
		out.Needs = append([]string{}, o.Needs...)
	}
	if !o.Evidence.empty() {
		out.Evidence = o.Evidence.clone()
	}
	out.Skills = appendUnique(out.Skills, o.Skills...)
	for kind, f := range out.Kinds {
		if f.Skills != nil {
			f.Skills = appendUnique(f.Skills, o.Skills...)
			out.Kinds[kind] = f
		}
	}
	for kind, f := range o.clone().Kinds {
		cur, ok := out.Kinds[kind]
		if !ok {
			out.Kinds[kind] = f
			continue
		}
		if f.Title != "" {
			cur.Title = f.Title
		}
		if f.Question != "" {
			cur.Question = f.Question
		}
		if f.Measure != "" {
			cur.Measure = f.Measure
		}
		if f.Evidence != nil {
			cur.Evidence = f.Evidence
		}
		if f.Skills != nil {
			base := cur.Skills
			if base == nil {
				base = append([]string{}, out.Skills...)
			}
			cur.Skills = appendUnique(base, f.Skills...)
		}
		out.Kinds[kind] = cur
	}
	return out
}

func readGrowthCatalogFS(fsys fs.FS) ([]growthCatalogFile, error) {
	names, err := fs.Glob(fsys, "growth_catalog/*.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var files []growthCatalogFile
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		f, err := parseGrowthCatalogFile(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		files = append(files, f)
	}
	return files, nil
}

func parseGrowthCatalogFile(raw []byte) (growthCatalogFile, error) {
	var f growthCatalogFile
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return growthCatalogFile{}, err
	}
	return f, nil
}

// buildGrowthCatalog folds catalog files in order and validates the result.
// Exactly one file declares the schema version and the profile; any file may
// add groups and areas.
func buildGrowthCatalog(files []growthCatalogFile) (*growthAreaCatalog, error) {
	c := &growthAreaCatalog{Profile: map[string][]string{}}
	for _, f := range files {
		if f.SchemaVersion != "" {
			if c.SchemaVersion != "" && c.SchemaVersion != f.SchemaVersion {
				return nil, fmt.Errorf("catalog files disagree on schema_version: %q and %q", c.SchemaVersion, f.SchemaVersion)
			}
			c.SchemaVersion = f.SchemaVersion
		}
		c.Groups = append(c.Groups, f.Groups...)
		for dim, values := range f.Profile {
			c.Profile[dim] = appendUnique(c.Profile[dim], values...)
		}
		c.Areas = append(c.Areas, f.Areas...)
	}
	if err := c.finish(); err != nil {
		return nil, err
	}
	return c, nil
}

// finish orders areas by group, numbers them and validates the catalog.
func (c *growthAreaCatalog) finish() error {
	if c.SchemaVersion != GrowthCatalogSchemaVersion {
		return fmt.Errorf("catalog schema_version = %q, want %q", c.SchemaVersion, GrowthCatalogSchemaVersion)
	}
	c.groupIdx = map[string]int{}
	for i, g := range c.Groups {
		if !growthIDRE.MatchString(g.ID) {
			return fmt.Errorf("group id %q must match %s", g.ID, growthIDRE)
		}
		if _, dup := c.groupIdx[g.ID]; dup {
			return fmt.Errorf("group %s is declared twice", g.ID)
		}
		if strings.TrimSpace(g.Title) == "" || strings.TrimSpace(g.Question) == "" {
			return fmt.Errorf("group %s needs a title and a question", g.ID)
		}
		if !growthBranches[g.Branch] {
			return fmt.Errorf("group %s branch %q is not a product-pipeline branch", g.ID, g.Branch)
		}
		c.groupIdx[g.ID] = i
	}
	if len(c.Groups) == 0 {
		return fmt.Errorf("catalog declares no groups")
	}
	for _, dim := range growthProfileDimensions {
		if len(c.Profile[dim]) == 0 {
			return fmt.Errorf("catalog profile declares no %s values", dim)
		}
	}
	for dim, values := range c.Profile {
		if !containsString(growthProfileDimensions, dim) {
			return fmt.Errorf("catalog profile dimension %q is not one of %s", dim, strings.Join(growthProfileDimensions, ", "))
		}
		for _, v := range values {
			if !growthIDRE.MatchString(v) {
				return fmt.Errorf("profile %s value %q must match %s", dim, v, growthIDRE)
			}
		}
	}
	sort.SliceStable(c.Areas, func(i, j int) bool {
		return c.groupIdx[c.Areas[i].Group] < c.groupIdx[c.Areas[j].Group]
	})
	c.byID = map[string]int{}
	for i, a := range c.Areas {
		if err := c.validateArea(a); err != nil {
			return err
		}
		if _, dup := c.byID[a.ID]; dup {
			return fmt.Errorf("area %s is declared twice", a.ID)
		}
		c.byID[a.ID] = i
	}
	for _, a := range c.Areas {
		for _, n := range a.Needs {
			if _, ok := c.byID[n]; !ok {
				return fmt.Errorf("area %s needs unknown area %q", a.ID, n)
			}
			if n == a.ID {
				return fmt.Errorf("area %s needs itself", a.ID)
			}
		}
	}
	return c.checkAcyclic()
}

func (c *growthAreaCatalog) validateArea(a growthAreaDef) error {
	if !growthIDRE.MatchString(a.ID) {
		return fmt.Errorf("area id %q must match %s", a.ID, growthIDRE)
	}
	if _, ok := c.groupIdx[a.Group]; !ok {
		return fmt.Errorf("area %s names unknown group %q", a.ID, a.Group)
	}
	if !strings.HasPrefix(a.ID, a.Group+"_") {
		return fmt.Errorf("area %s must start with its group id %q", a.ID, a.Group+"_")
	}
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Question) == "" || strings.TrimSpace(a.Measure) == "" {
		return fmt.Errorf("area %s needs a title, a question and a measure", a.ID)
	}
	if a.Branch != "" && !growthBranches[a.Branch] {
		return fmt.Errorf("area %s branch %q is not a product-pipeline branch", a.ID, a.Branch)
	}
	for dim, allowed := range a.Applies {
		declared, ok := c.Profile[dim]
		if !ok {
			return fmt.Errorf("area %s applies on undeclared profile dimension %q", a.ID, dim)
		}
		if len(allowed) == 0 {
			return fmt.Errorf("area %s applies.%s lists no values", a.ID, dim)
		}
		for _, v := range allowed {
			if !containsString(declared, v) {
				return fmt.Errorf("area %s applies.%s value %q is not a declared profile value", a.ID, dim, v)
			}
		}
	}
	if err := validateGrowthEvidence(a.ID, a.Evidence); err != nil {
		return err
	}
	if err := validateGrowthSkills(a.ID, a.Skills); err != nil {
		return err
	}
	for kind, form := range a.Kinds {
		if !containsString(c.Profile["kinds"], kind) {
			return fmt.Errorf("area %s has a form for undeclared kind %q", a.ID, kind)
		}
		if form.Evidence != nil {
			if err := validateGrowthEvidence(a.ID+"."+kind, *form.Evidence); err != nil {
				return err
			}
		}
		if err := validateGrowthSkills(a.ID+"."+kind, form.Skills); err != nil {
			return err
		}
	}
	return nil
}

func validateGrowthEvidence(where string, e growthEvidenceDef) error {
	if e.Custom != "" && !growthCustomEvaluators[e.Custom] {
		return fmt.Errorf("area %s names unknown custom evaluator %q", where, e.Custom)
	}
	for _, p := range e.Record {
		if strings.TrimSpace(p) == "" || strings.ContainsAny(p, " \t") {
			return fmt.Errorf("area %s record predicate %q is not a predicate", where, p)
		}
	}
	for _, list := range [][]string{e.Metrics, e.Capped, e.Companion} {
		for _, m := range list {
			if strings.TrimSpace(m) == "" {
				return fmt.Errorf("area %s has an empty metric glob", where)
			}
		}
	}
	return nil
}

func validateGrowthSkills(where string, skills []string) error {
	for _, s := range skills {
		if !growthSkillRE.MatchString(s) {
			return fmt.Errorf("area %s skill %q must match %s", where, s, growthSkillRE)
		}
	}
	return nil
}

// checkAcyclic rejects a needs cycle; it names one area on the cycle.
func (c *growthAreaCatalog) checkAcyclic() error {
	const (
		white = iota
		grey
		black
	)
	color := make([]int, len(c.Areas))
	var visit func(i int) error
	visit = func(i int) error {
		color[i] = grey
		for _, n := range c.Areas[i].Needs {
			j := c.byID[n]
			switch color[j] {
			case grey:
				return fmt.Errorf("area needs form a cycle through %s and %s", c.Areas[i].ID, n)
			case white:
				if err := visit(j); err != nil {
					return err
				}
			}
		}
		color[i] = black
		return nil
	}
	for i := range c.Areas {
		if color[i] == white {
			if err := visit(i); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *growthAreaCatalog) clone() *growthAreaCatalog {
	out := &growthAreaCatalog{
		SchemaVersion: c.SchemaVersion,
		Groups:        append([]GrowthAreaGroup{}, c.Groups...),
		Profile:       map[string][]string{},
		Areas:         make([]growthAreaDef, len(c.Areas)),
		byID:          map[string]int{},
		groupIdx:      map[string]int{},
		Overlay:       c.Overlay,
		OverlayError:  c.OverlayError,
	}
	for k, v := range c.Profile {
		out.Profile[k] = append([]string{}, v...)
	}
	for i, a := range c.Areas {
		out.Areas[i] = a.clone()
	}
	for k, v := range c.byID {
		out.byID[k] = v
	}
	for k, v := range c.groupIdx {
		out.groupIdx[k] = v
	}
	return out
}

func (a growthAreaDef) clone() growthAreaDef {
	out := a
	out.Needs = append([]string{}, a.Needs...)
	out.Skills = append([]string{}, a.Skills...)
	out.Evidence = a.Evidence.clone()
	out.Applies = map[string][]string{}
	for k, v := range a.Applies {
		out.Applies[k] = append([]string{}, v...)
	}
	out.Kinds = map[string]growthFormDef{}
	for k, f := range a.Kinds {
		nf := f
		if f.Evidence != nil {
			ev := f.Evidence.clone()
			nf.Evidence = &ev
		}
		if f.Skills != nil {
			nf.Skills = append([]string{}, f.Skills...)
		}
		out.Kinds[k] = nf
	}
	return out
}

func (e growthEvidenceDef) clone() growthEvidenceDef {
	out := e
	out.Metrics = append([]string{}, e.Metrics...)
	out.Capped = append([]string{}, e.Capped...)
	out.Companion = append([]string{}, e.Companion...)
	out.Record = append([]string{}, e.Record...)
	return out
}

// growthCustomInputs names what a custom evaluator reads, for the inputs list.
var growthCustomInputs = map[string][]string{
	"feedback":    {"open source issues", "store reviews"},
	"funnel":      {"funnel stages"},
	"experiments": {"hypotheses", "reviews"},
	"organic":     {"content post records"},
	"search_ads":  {"ads rows"},
}

// inputs lists what an evidence spec reads, in a stable order.
func (e growthEvidenceDef) inputs() []string {
	out := []string{}
	out = appendUnique(out, e.Metrics...)
	out = appendUnique(out, e.Capped...)
	out = appendUnique(out, e.Companion...)
	out = appendUnique(out, e.Record...)
	out = appendUnique(out, growthCustomInputs[e.Custom]...)
	return out
}

// growthAreaSpec is one area resolved to one form: the base row, or the form
// for one product kind. The evaluators read only this.
type growthAreaSpec struct {
	ID         string
	Kind       string // "" for the base row
	Title      string
	Question   string
	Inputs     []string
	Skills     []string
	patterns   []string // metric globs that count as full evidence
	capped     []string // metric globs that only ever justify "partial"
	companion  []string // all of these globs must match something, else partial
	source     string   // human name of the source to connect
	measure    string   // noun phrase for "Nothing measures X today"
	record     []string // product-contract fact predicates that answer the question
	paywall    bool     // hypothesis/review text mentioning paywall counts as partial
	capPartial bool     // never better than partial
	custom     string   // custom evaluator key
	hasSpec    bool     // any evidence is declared at all
}

// spec resolves the area for one kind; an empty kind or a kind without a form
// is the base row.
func (a growthAreaDef) spec(kind string) growthAreaSpec {
	s := growthAreaSpec{ID: a.ID, Title: a.Title, Question: a.Question, measure: a.Measure, Skills: append([]string{}, a.Skills...)}
	ev := a.Evidence
	if f, ok := a.Kinds[kind]; ok && kind != "" {
		s.Kind = kind
		if f.Title != "" {
			s.Title = f.Title
		}
		if f.Question != "" {
			s.Question = f.Question
		}
		if f.Measure != "" {
			s.measure = f.Measure
		}
		if f.Evidence != nil {
			ev = *f.Evidence
		}
		if f.Skills != nil {
			s.Skills = append([]string{}, f.Skills...)
		}
	}
	s.patterns, s.capped, s.companion = ev.Metrics, ev.Capped, ev.Companion
	s.source, s.record, s.custom = ev.Source, ev.Record, ev.Custom
	s.paywall, s.capPartial = ev.Paywall, ev.CapPartial
	s.Inputs = ev.inputs()
	s.hasSpec = !ev.empty()
	return s
}

// kindsSorted lists the kinds an area has forms for, in profile order.
func (c *growthAreaCatalog) kindsSorted(a growthAreaDef) []string {
	var out []string
	for _, k := range c.Profile["kinds"] {
		if _, ok := a.Kinds[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

// publicAreas renders the catalog rows for the projection.
func (c *growthAreaCatalog) publicAreas() []GrowthArea {
	out := make([]GrowthArea, 0, len(c.Areas))
	for i, a := range c.Areas {
		base := a.spec("")
		row := GrowthArea{
			ID: a.ID, Number: i + 1, Group: a.Group, Title: a.Title, Question: a.Question,
			Inputs: base.Inputs, Applies: map[string][]string{}, Needs: append([]string{}, a.Needs...),
			Branch: c.branch(a), Skills: append([]string{}, a.Skills...), Kinds: map[string]GrowthAreaForm{},
		}
		for dim, v := range a.Applies {
			row.Applies[dim] = append([]string{}, v...)
		}
		for _, k := range c.kindsSorted(a) {
			s := a.spec(k)
			row.Kinds[k] = GrowthAreaForm{Title: s.Title, Question: s.Question, Inputs: s.Inputs, Skills: s.Skills}
		}
		out = append(out, row)
	}
	return out
}

func appendUnique(list []string, values ...string) []string {
	for _, v := range values {
		if !containsString(list, v) {
			list = append(list, v)
		}
	}
	return list
}

// GrowthCatalogInfo says which catalog judged the products.
type GrowthCatalogInfo struct {
	SchemaVersion string `json:"schema_version"`
	Source        string `json:"source"`
	Overlay       string `json:"overlay"`
	OverlayError  string `json:"overlay_error"`
	Groups        int    `json:"groups"`
	Areas         int    `json:"areas"`
}

func (c *growthAreaCatalog) info() GrowthCatalogInfo {
	return GrowthCatalogInfo{
		SchemaVersion: c.SchemaVersion, Source: "embedded", Overlay: c.Overlay, OverlayError: c.OverlayError,
		Groups: len(c.Groups), Areas: len(c.Areas),
	}
}
