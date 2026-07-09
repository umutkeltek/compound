package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"compound/internal/sourceobs"
)

type Chunk struct {
	ID        string `json:"id"`
	ItemID    string `json:"item_id"`
	Ordinal   int    `json:"ordinal"`
	Section   string `json:"section,omitempty"`
	Prefix    string `json:"prefix"`
	Text      string `json:"text"`
	EmbedText string `json:"embed_text"`
	ChunkHash string `json:"chunk_hash"`
}

func ChunkItem(item sourceobs.SourceManifestItem, body string, cfg ChunkConfig) ([]Chunk, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	units := splitStructureUnits(body)
	base := packUnits(units, cfg.Size)
	if len(base) == 0 && strings.TrimSpace(body) != "" {
		base = []chunkDraft{{text: trimToRuneBoundary(body), section: ""}}
	}
	var chunks []Chunk
	for i, draft := range base {
		text := draft.text
		if i > 0 && cfg.Overlap > 0 {
			overlap := tailRunes(base[i-1].text, cfg.Overlap)
			if overlap != "" {
				text = overlap + "\n\n" + text
				if runeLen(text) > cfg.Size {
					text = tailRunes(text, cfg.Size)
				}
			}
		}
		prefix := buildPrefix(item, draft.section)
		embedText := prefix + " " + text
		sum := sha256.Sum256([]byte(embedText))
		chunks = append(chunks, Chunk{
			ID:        fmt.Sprintf("%s#%d", item.ID, i),
			ItemID:    item.ID,
			Ordinal:   i,
			Section:   draft.section,
			Prefix:    prefix,
			Text:      text,
			EmbedText: embedText,
			ChunkHash: "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	return chunks, nil
}

type structureUnit struct {
	text    string
	section string
}

type chunkDraft struct {
	text    string
	section string
}

func splitStructureUnits(body string) []structureUnit {
	parts := strings.Split(body, "\n\n")
	var out []structureUnit
	headingPath := ""
	turn := 0
	for _, part := range parts {
		text := strings.TrimSpace(part)
		if text == "" {
			continue
		}
		section := headingPath
		if heading := markdownHeading(text); heading != "" {
			headingPath = heading
			section = headingPath
		}
		if isTurn(text) {
			section = fmt.Sprintf("turn:%d", turn)
			turn++
		}
		out = append(out, structureUnit{text: text, section: section})
	}
	return out
}

func markdownHeading(text string) string {
	first := strings.TrimSpace(strings.Split(text, "\n")[0])
	if !strings.HasPrefix(first, "#") {
		return ""
	}
	level := 0
	for _, r := range first {
		if r != '#' {
			break
		}
		level++
	}
	if level == 0 || level > 6 || len(first) <= level || first[level] != ' ' {
		return ""
	}
	return truncateRunes(strings.TrimSpace(first[level:]), 80)
}

func isTurn(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(lower, "user:") ||
		strings.HasPrefix(lower, "assistant:") ||
		strings.HasPrefix(lower, "system:") ||
		strings.HasPrefix(lower, "turn ")
}

func packUnits(units []structureUnit, size int) []chunkDraft {
	var out []chunkDraft
	var current chunkDraft
	flush := func() {
		if strings.TrimSpace(current.text) == "" {
			return
		}
		out = append(out, current)
		current = chunkDraft{}
	}
	for _, unit := range units {
		pieces := hardSplit(unit.text, size)
		for _, piece := range pieces {
			if strings.TrimSpace(piece) == "" {
				continue
			}
			if current.text == "" {
				current = chunkDraft{text: piece, section: unit.section}
				continue
			}
			joined := current.text + "\n\n" + piece
			if runeLen(joined) <= size {
				current.text = joined
				if current.section == "" {
					current.section = unit.section
				}
				continue
			}
			flush()
			current = chunkDraft{text: piece, section: unit.section}
		}
	}
	flush()
	return out
}

func hardSplit(text string, size int) []string {
	if runeLen(text) <= size {
		return []string{text}
	}
	runes := []rune(text)
	var out []string
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, strings.TrimSpace(string(runes[start:end])))
	}
	return out
}

func buildPrefix(item sourceobs.SourceManifestItem, section string) string {
	sourceKind := truncateRunes(strings.TrimSpace(item.SourceKind), 40)
	title := strings.TrimSpace(item.PageSlug)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(item.SourceRef), filepath.Ext(item.SourceRef))
	}
	title = truncateRunes(title, 120)
	section = truncateRunes(strings.TrimSpace(section), 80)
	entities := prefixEntities(item.CompoundEntityRefs)
	fields := []string{sourceKind, title, section, entities}
	prefix := prefixFromFields(fields)
	if runeLen(prefix) <= 240 {
		return prefix
	}
	for _, idx := range []int{3, 2, 1} {
		if fields[idx] == "" {
			continue
		}
		fields[idx] = "…"
		prefix = prefixFromFields(fields)
		if runeLen(prefix) <= 240 {
			return prefix
		}
		fields[idx] = ""
		prefix = prefixFromFields(fields)
		if runeLen(prefix) <= 240 {
			return prefix
		}
	}
	return truncateRunes(prefix, 240)
}

func prefixEntities(values []string) string {
	seen := map[string]bool{}
	var entities []string
	for _, value := range values {
		value = truncateRunes(strings.TrimSpace(value), 40)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		entities = append(entities, value)
	}
	sort.Strings(entities)
	if len(entities) > 5 {
		entities = entities[:5]
	}
	return strings.Join(entities, ", ")
}

func prefixFromFields(fields []string) string {
	var parts []string
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			parts = append(parts, field)
		}
	}
	if len(parts) == 0 {
		return "[]"
	}
	return "[" + strings.Join(parts, " · ") + "]"
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || runeLen(value) <= limit {
		return value
	}
	runes := []rune(value)
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func tailRunes(value string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return string(runes[len(runes)-n:])
}

func runeLen(value string) int {
	return utf8.RuneCountInString(value)
}

func trimToRuneBoundary(value string) string {
	if utf8.ValidString(value) {
		return value
	}
	return string([]rune(value))
}
