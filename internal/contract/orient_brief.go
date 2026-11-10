package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"compound/internal/envelope"
)

// `compound orient --brief` serves the same materialized ContextPack as plain
// text an agent can read in one glance. The full pack measured 57-77 KB on
// 2026-11-10 and agents used about 3% of its items (pack hit rate, 2026-11-03),
// so the brief leads with the checkout's own facts and keeps four short
// sections. The pack is still materialized and recorded; the brief only
// changes what is printed.

// OrientBriefMaxBytes is the hard cap on the whole brief, footer included.
const OrientBriefMaxBytes = 6000

// RepoBriefsRelPath is the optional per-checkout fact projection under
// COMPOUND_HOME. Any private tool may write it; Compound only reads it.
const RepoBriefsRelPath = "state/repo-briefs.json"

const (
	orientBriefMemoryLimit     = 5
	orientBriefOfferLimit      = 5
	orientBriefFactLineLimit   = 12
	orientBriefLineMaxBytes    = 300
	orientBriefTitleMaxBytes   = 120
	orientBriefCommandMax      = 1200
	orientBriefStaleAfter      = 36 * time.Hour
	repoBriefsMaxFileBytes     = 4 << 20
	orientBriefTruncatedLine   = "(brief cut at the size cap; the full pack has the rest)"
	orientBriefControlPlane    = "control-plane"
	orientBriefNoFactsSuffix   = "no repo facts recorded in " + RepoBriefsRelPath
	orientBriefTaskPlaceholder = "<same task>"
)

// RepoBrief is one checkout's facts in the projection: the repository root it
// covers, a display title, and short fact lines, most important first.
type RepoBrief struct {
	RepoRoot string   `json:"repo_root"`
	Title    string   `json:"title"`
	Lines    []string `json:"lines"`
}

// RepoBriefs is the projection file. GeneratedAt lets the brief say how old
// the facts are; the projection is evidence, not authority.
type RepoBriefs struct {
	SchemaVersion string      `json:"schema_version,omitempty"`
	GeneratedAt   string      `json:"generated_at,omitempty"`
	Briefs        []RepoBrief `json:"briefs"`
}

// OrientBriefInput is everything the brief renders from. Pack is the pack
// orient already materialized and recorded for the same Input.
type OrientBriefInput struct {
	HomePath    string
	Input       Input
	Pack        envelope.ContextPack
	FullCommand string
	Now         time.Time
}

// RenderOrientBrief returns the plain-text brief, never longer than
// OrientBriefMaxBytes. Order: repo facts, task-matched memory, capability
// offers, then one line naming the command for the full pack.
func RenderOrientBrief(in OrientBriefInput) string {
	var body []string
	body = append(body, orientBriefFacts(in)...)
	body = append(body, "")
	body = append(body, orientBriefMemory(in.Input, in.Pack)...)
	body = append(body, "")
	body = append(body, orientBriefOffers(in.Pack)...)
	body = append(body, "")

	footer := orientBriefFooter(in)
	budget := OrientBriefMaxBytes - len(footer) - 1
	var b strings.Builder
	for _, line := range body {
		if b.Len()+len(line)+1 > budget-len(orientBriefTruncatedLine)-1 {
			b.WriteString(orientBriefTruncatedLine + "\n")
			break
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(footer + "\n")
	return b.String()
}

func orientBriefFacts(in OrientBriefInput) []string {
	kind := strings.TrimSpace(in.Input.RepoKind)
	if kind == "" {
		kind = defaultRepoKind
	}
	repoName := filepath.Base(filepath.Clean(strings.TrimSpace(in.Input.Cwd)))
	briefs, err := LoadRepoBriefs(in.HomePath)
	if err != nil {
		return []string{
			briefLine(repoName) + " (" + kind + " repo)",
			briefLine("Repo facts unavailable: " + err.Error()),
		}
	}
	match, ok := MatchRepoBrief(briefs, in.Input.Cwd)
	if !ok {
		return []string{briefLine(repoName) + " (" + kind + " repo): " + orientBriefNoFactsSuffix}
	}
	title := clipBytes(sanitizeBriefText(match.Title), orientBriefTitleMaxBytes)
	if title == "" {
		title = briefLine(repoName)
	}
	lines := []string{title + " (" + kind + " repo)"}
	for i, line := range match.Lines {
		if i == orientBriefFactLineLimit {
			lines = append(lines, fmt.Sprintf("- (+%d more fact lines in %s)", len(match.Lines)-i, RepoBriefsRelPath))
			break
		}
		if text := briefLine(line); text != "" {
			lines = append(lines, "- "+text)
		}
	}
	lines = append(lines, briefLine(repoBriefsAge(briefs.GeneratedAt, in.Now)))
	return lines
}

func repoBriefsAge(generatedAt string, now time.Time) string {
	at, err := time.Parse(time.RFC3339, strings.TrimSpace(generatedAt))
	if err != nil {
		return "(facts from " + RepoBriefsRelPath + ", generation time unknown)"
	}
	age := now.Sub(at)
	stamp := at.UTC().Format("2006-01-02 15:04 UTC")
	if age > orientBriefStaleAfter {
		return fmt.Sprintf("(facts from %s as of %s: STALE, %d days old)", RepoBriefsRelPath, stamp, int(age.Hours()/24))
	}
	return "(facts from " + RepoBriefsRelPath + " as of " + stamp + ")"
}

// LoadRepoBriefs reads the optional projection. A missing file is not an
// error and returns no briefs; an unreadable or malformed one is an error the
// brief prints, so absence and breakage never look the same.
func LoadRepoBriefs(homePath string) (RepoBriefs, error) {
	if strings.TrimSpace(homePath) == "" {
		return RepoBriefs{}, nil
	}
	path := filepath.Join(homePath, filepath.FromSlash(RepoBriefsRelPath))
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return RepoBriefs{}, nil
	}
	if err != nil {
		return RepoBriefs{}, fmt.Errorf("%s: %w", RepoBriefsRelPath, err)
	}
	if !info.Mode().IsRegular() {
		return RepoBriefs{}, fmt.Errorf("%s is not a regular file", RepoBriefsRelPath)
	}
	if info.Size() > repoBriefsMaxFileBytes {
		return RepoBriefs{}, fmt.Errorf("%s is %d bytes, above the %d-byte read limit", RepoBriefsRelPath, info.Size(), repoBriefsMaxFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return RepoBriefs{}, fmt.Errorf("%s: %w", RepoBriefsRelPath, err)
	}
	var briefs RepoBriefs
	if err := json.Unmarshal(data, &briefs); err != nil {
		return RepoBriefs{}, fmt.Errorf("%s is not valid JSON: %w", RepoBriefsRelPath, err)
	}
	return briefs, nil
}

// MatchRepoBrief returns the brief whose repo_root contains cwd. When roots
// nest, the deepest root wins. Paths are compared after symlink resolution.
func MatchRepoBrief(briefs RepoBriefs, cwd string) (RepoBrief, bool) {
	var best RepoBrief
	bestLen := -1
	for _, brief := range briefs.Briefs {
		root := strings.TrimSpace(brief.RepoRoot)
		if !workspacePathContains(root, cwd) {
			continue
		}
		canonical, err := canonicalWorkspacePath(root)
		if err != nil {
			continue
		}
		if len(canonical) > bestLen {
			best, bestLen = brief, len(canonical)
		}
	}
	return best, bestLen >= 0
}

// orientBriefMemory picks up to five task-matched memory lines from the pack:
// folded positions first, then already-tried-and-failed negatives. Outside the
// control-plane repo only product-scoped positions and failure patterns are
// kept: Compound's global positions and routing remorse describe Compound's
// own state, and they stay in the full pack.
func orientBriefMemory(in Input, pack envelope.ContextPack) []string {
	compoundRepo := strings.EqualFold(strings.TrimSpace(in.RepoKind), orientBriefControlPlane)
	var lines []string
	omitted := 0
	for _, pos := range pack.ActivePositions {
		if !compoundRepo && !productScopedEntity(pos.Entity) {
			omitted++
			continue
		}
		lines = append(lines, briefLine(pos.Entity+": "+pos.Stance))
	}
	for _, neg := range pack.Negatives {
		if !compoundRepo && neg.Source != "failure_pattern" {
			omitted++
			continue
		}
		text := "Tried before and failed: " + neg.ApproachKey
		if why := strings.TrimSpace(neg.WhyItFailed); why != "" {
			text += " (" + why + ")"
		}
		lines = append(lines, briefLine(text+" ["+neg.Ref+"]"))
	}
	shown := lines
	if len(shown) > orientBriefMemoryLimit {
		shown = shown[:orientBriefMemoryLimit]
	}
	out := []string{"Memory (task-matched):"}
	if len(shown) == 0 {
		out[0] = "Memory: nothing task-matched for this repo."
	}
	for _, line := range shown {
		out = append(out, "- "+line)
	}
	if rest := len(lines) - len(shown) + omitted; rest > 0 {
		out = append(out, fmt.Sprintf("(+%d more memory lines in the full pack)", rest))
	}
	return out
}

func productScopedEntity(entity string) bool {
	entity = strings.ToLower(strings.TrimSpace(entity))
	return strings.HasPrefix(entity, "product:") && len(entity) > len("product:")
}

// orientBriefOffers lists the top offers with the one thing to use: the
// procedure file to read, or the skill/tool/server name.
func orientBriefOffers(pack envelope.ContextPack) []string {
	if len(pack.CapabilityOffers) == 0 {
		return []string{"Capabilities: none offered for this task."}
	}
	out := []string{"Capabilities (offers, not execution):"}
	for i, offer := range pack.CapabilityOffers {
		if i == orientBriefOfferLimit {
			break
		}
		name := strings.TrimSpace(offer.Name)
		use := capabilityOfferUse(offer)
		if name == "" {
			name = use
		}
		out = append(out, "- "+briefLine(name+": "+use))
	}
	return out
}

func capabilityOfferUse(offer envelope.CapabilityOffer) string {
	for _, req := range offer.Required {
		if req.Kind == "read" && strings.TrimSpace(req.Ref) != "" {
			return "read " + strings.TrimSpace(req.Ref)
		}
	}
	ref := strings.TrimPrefix(strings.TrimSpace(offer.Ref), "capability://")
	kind, id, ok := strings.Cut(ref, ":")
	if !ok || id == "" {
		return strings.TrimSpace(offer.Ref)
	}
	return kind + " " + id
}

func orientBriefFooter(in OrientBriefInput) string {
	command := clipBytes(sanitizeBriefText(in.FullCommand), orientBriefCommandMax)
	if command == "" {
		command = "compound orient without --brief"
	}
	line := "Full pack: " + command
	if id := strings.TrimSpace(in.Pack.ID); id != "" {
		line += "   (this brief's pack: " + id + ")"
	}
	return line
}

// OrientFullPackCommand renders the orient command for the full pack: the
// same arguments with --brief removed. Values of value flags are kept even
// when they read "--brief". A task too long for one line becomes a
// placeholder so the footer stays inside the cap.
func OrientFullPackCommand(args []string) string {
	valueFlags := map[string]bool{"--harness": true, "--cwd": true, "--task": true, "--repo-kind": true, "--mode": true, "--topic": true, "--body-budget": true}
	words := []string{"compound", "orient"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--brief" {
			continue
		}
		words = append(words, shellWord(arg))
		if valueFlags[arg] && i+1 < len(args) {
			i++
			words = append(words, shellWord(args[i]))
		}
	}
	command := strings.Join(words, " ")
	if len(command) <= orientBriefCommandMax {
		return command
	}
	for i := 0; i+1 < len(words); i++ {
		if words[i] == "--task" {
			words[i+1] = shellWord(orientBriefTaskPlaceholder)
		}
	}
	return clipBytes(strings.Join(words, " "), orientBriefCommandMax)
}

func shellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"$&;|<>()*?![]{}~#\\`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// briefLine makes one display line: control characters become spaces, so a
// projection line cannot start a fake section, and the line is clipped.
func briefLine(s string) string {
	return clipBytes(sanitizeBriefText(s), orientBriefLineMaxBytes)
}

func sanitizeBriefText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func clipBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const ellipsis = "..."
	cut := max - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}
