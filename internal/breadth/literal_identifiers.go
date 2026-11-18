package breadth

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"compound/internal/mem"
)

// Preserve separators within identifiers. For example, a request for T-310
// must not match T-3100, T_310, or the middle of OLD-T-310.
var identifierToken = regexp.MustCompile(`[\pL\pN]+(?:[-_][\pL\pN]+)*`)

func lookupIdentifiers(query string) []string {
	if strings.IndexFunc(query, unicode.IsDigit) < 0 {
		return nil
	}
	var ids []string
	for _, token := range identifierToken.FindAllString(strings.ToLower(query), -1) {
		letters, digits := 0, 0
		for _, ch := range token {
			if unicode.IsLetter(ch) {
				letters++
			}
			if unicode.IsDigit(ch) {
				digits++
			}
		}
		// A short number and a calendar year are common descriptive terms. A
		// long uninterrupted number or a mixed serial carries a literal identity.
		if (digits >= 6 && digits == len([]rune(token))) || (letters > 0 && digits >= 2) {
			ids = append(ids, token)
		}
	}
	return unique(ids)
}

func identifierMatches(query string, docs []doc) map[string]int {
	ids := lookupIdentifiers(query)
	if len(ids) == 0 {
		return nil
	}
	var matches map[string]int
	for _, d := range docs {
		text := strings.ToLower(d.claim.Text)
		entity := strings.ToLower(d.claim.Entity)
		score := 0
		for _, id := range ids {
			if !containsIdentifier(text, id) {
				continue
			}
			// More requested identifiers win first. Within that group an entity
			// named by the identifier outranks records which only mention it.
			score += len(ids) + 1
			if containsIdentifier(entity, id) {
				score++
			}
		}
		if score > 0 {
			if matches == nil {
				matches = make(map[string]int)
			}
			matches[d.claim.ClaimID] = score
		}
	}
	return matches
}

func containsIdentifier(text, id string) bool {
	if !strings.Contains(text, id) {
		return false
	}
	for _, token := range identifierToken.FindAllString(text, -1) {
		if token == id {
			return true
		}
	}
	return false
}

func preferIdentifierMatches(hits []mem.Hit, matches map[string]int, k int) []mem.Hit {
	sort.SliceStable(hits, func(i, j int) bool {
		return matches[hits[i].ClaimID] > matches[hits[j].ClaimID]
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	// Alias expansion merges by score after this layer. Keep the ordering of
	// literal hits ahead of ordinary hit scores during that merge as well.
	var maxScore float64
	for _, hit := range hits {
		if hit.Score > maxScore {
			maxScore = hit.Score
		}
	}
	for i := range hits {
		if matches[hits[i].ClaimID] == 0 {
			break
		}
		hits[i].Score = maxScore + float64(len(hits)-i)
	}
	return hits
}
