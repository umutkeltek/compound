package mem

import (
	"regexp"
	"strings"
)

const gitObjectIDPattern = "[0-9a-f]{64}|[0-9a-f]{40}"

var gitSnapshotLookup = regexp.MustCompile(
	"(?i)\\b(?:git|repo(?:sitory)?|source)\\s+snapshot(?:\\s+(?:at|gives?|names?|records?))?" +
		"\\s+(?:head|commit)\\s+[`'\"]?(" + gitObjectIDPattern + ")(?:$|[\\s`'\".,?!;:)])",
)
var fullGitObjectID = regexp.MustCompile(`(?i)\b(?:` + gitObjectIDPattern + `)\b`)

// Only direct requests for one fully spelled snapshot head use the literal
// gate. Comparisons, abbreviated IDs and other references use breadth recall.
func gitSnapshotHeadFromQuery(query string) string {
	match := gitSnapshotLookup.FindStringSubmatch(query)
	if match == nil || len(fullGitObjectID.FindAllStringIndex(query, 2)) != 1 {
		return ""
	}
	return strings.ToLower(match[1])
}

func matchesGitSnapshotHead(claim Claim, head string) bool {
	return strings.HasPrefix(claim.Entity, "repo:") && claim.Predicate == "head" && strings.EqualFold(claim.Object, head)
}
