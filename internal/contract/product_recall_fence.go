package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"compound/internal/mem"
	"gopkg.in/yaml.v3"
)

const productRecallFenceAuthorityRef = "compound://contract/product-recall-fence"

// productRecallFence is the single product-isolation seam consumed by flat
// memory, folded positions, accepted decisions, and grouped probes. It scopes
// product-associated claims while leaving genuinely global claims available.
// An empty Entity on a required fence is deliberate fail-closed state.
type productRecallFence struct {
	Required bool
	Status   string
	Entity   string
	known    map[string][]string
	ids      map[string][]string
	Revision string
}

type workspaceProductEvidence struct {
	Tokens      []string
	Identifiers []string
}

func resolveProductRecallFence(in Input, view *mem.CurrentView) productRecallFence {
	if !strings.EqualFold(strings.TrimSpace(in.RepoKind), "product") {
		return productRecallFence{Status: "not-required"}
	}
	fence := productRecallFence{Required: true, Status: "unresolved"}
	if view == nil {
		fence.Revision = productRecallFenceRevision(fence)
		return fence
	}

	claims := view.Claims()
	fence.known, fence.ids = currentProductIdentityIndex(claims)
	candidates := map[string]bool{}
	addToken := func(token string) {
		for _, entity := range resolveKnownProductToken(fence.known, token) {
			candidates[entity] = true
		}
	}
	addIdentifier := func(identifier string) {
		for _, entity := range fence.ids[productIdentifierKey(identifier)] {
			candidates[entity] = true
		}
	}

	if evidence, ok := nearestProductProjectEvidence(in.Cwd); ok {
		for _, token := range evidence.Tokens {
			addToken(token)
		}
		for _, identifier := range evidence.Identifiers {
			addIdentifier(identifier)
		}
	}
	for _, claim := range claims {
		if !workspaceIdentityClaimMatches(claim, in.Cwd) {
			continue
		}
		addToken(claim.Entity)
	}

	switch len(candidates) {
	case 1:
		fence.Status = "resolved"
		for entity := range candidates {
			fence.Entity = entity
		}
	case 0:
		fence.Status = "unresolved"
	default:
		fence.Status = "ambiguous"
	}
	fence.Revision = productRecallFenceRevision(fence)
	return fence
}

func currentProductIdentityIndex(claims []mem.Claim) (map[string][]string, map[string][]string) {
	byKey := map[string]map[string]bool{}
	products := map[string]bool{}
	for _, claim := range claims {
		entity := strings.TrimSpace(claim.Entity)
		// Canonical product identities have one namespace separator. Deeper
		// product:* evidence namespaces (for example product:unscoped:idea)
		// are not products and cannot establish a workspace identity.
		if !strings.HasPrefix(strings.ToLower(entity), "product:") || strings.Count(entity, ":") != 1 {
			continue
		}
		key := productIdentityKey(entity)
		if key == "" {
			continue
		}
		if byKey[key] == nil {
			byKey[key] = map[string]bool{}
		}
		byKey[key][entity] = true
		products[entity] = true
	}
	out := make(map[string][]string, len(byKey))
	for key, entities := range byKey {
		for entity := range entities {
			out[key] = append(out[key], entity)
		}
		sort.Strings(out[key])
	}
	byIdentifier := map[string]map[string]bool{}
	addIdentifier := func(entity, value string) {
		key := productIdentifierKey(value)
		if key == "" {
			return
		}
		if byIdentifier[key] == nil {
			byIdentifier[key] = map[string]bool{}
		}
		byIdentifier[key][entity] = true
	}
	for _, claim := range claims {
		if !products[claim.Entity] {
			continue
		}
		switch productIdentityKey(claim.Predicate) {
		case "surfaces", "releasebundleid", "releaseappstoreconnectappid":
			for _, value := range strings.FieldsFunc(claim.Object, func(r rune) bool {
				return r == ',' || r == ';' || r == '|'
			}) {
				addIdentifier(claim.Entity, value)
			}
		}
	}
	ids := make(map[string][]string, len(byIdentifier))
	for key, entities := range byIdentifier {
		for entity := range entities {
			ids[key] = append(ids[key], entity)
		}
		sort.Strings(ids[key])
	}
	return out, ids
}

func resolveKnownProductToken(known map[string][]string, token string) []string {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	key := productIdentityKey(token)
	if key == "" {
		return nil
	}
	return append([]string(nil), known[key]...)
}

func productIdentityKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if i := strings.LastIndex(value, ":"); i >= 0 && i+1 < len(value) {
		value = value[i+1:]
	}
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func nearestProductProjectEvidence(cwd string) (workspaceProductEvidence, bool) {
	dir := strings.TrimSpace(cwd)
	if dir == "" {
		return workspaceProductEvidence{}, false
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return workspaceProductEvidence{}, false
	}
	dir = filepath.Clean(absolute)
	for {
		path := filepath.Join(dir, "project.yaml")
		data, err := os.ReadFile(path)
		if err == nil {
			evidence := productProjectEvidence(data)
			if releaseData, releaseErr := os.ReadFile(filepath.Join(dir, "release.yaml")); releaseErr == nil {
				releaseEvidence := productReleaseEvidence(releaseData)
				evidence.Tokens = append(evidence.Tokens, releaseEvidence.Tokens...)
				evidence.Identifiers = append(evidence.Identifiers, releaseEvidence.Identifiers...)
			}
			evidence.Tokens = compactProbeStrings(evidence.Tokens)
			evidence.Identifiers = compactProbeStrings(evidence.Identifiers)
			return evidence, len(evidence.Tokens) > 0 || len(evidence.Identifiers) > 0
		}
		if !os.IsNotExist(err) {
			return workspaceProductEvidence{}, false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return workspaceProductEvidence{}, false
		}
		dir = parent
	}
}

func productProjectEvidence(data []byte) workspaceProductEvidence {
	var document yaml.Node
	if yaml.Unmarshal(data, &document) != nil || len(document.Content) != 1 {
		return workspaceProductEvidence{}
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return workspaceProductEvidence{}
	}
	var evidence workspaceProductEvidence
	project := yamlMappingValue(root, "project")
	if project != nil && project.Kind == yaml.MappingNode {
		evidence.Tokens = append(evidence.Tokens,
			yamlScalarValue(yamlMappingValue(project, "slug")),
			yamlScalarValue(yamlMappingValue(project, "name")),
		)
		evidence.Tokens = append(evidence.Tokens, yamlSequenceScalarValues(yamlMappingValue(project, "aliases"))...)
	}
	if source := yamlMappingValue(root, "source"); source != nil {
		if authority := yamlMappingValue(source, "authority"); authority != nil {
			evidence.Identifiers = append(evidence.Identifiers, yamlScalarValue(yamlMappingValue(authority, "repo")))
		}
	}
	appendReleaseIdentityEvidence(&evidence, yamlMappingValue(root, "release_identity"))
	return evidence
}

func productReleaseEvidence(data []byte) workspaceProductEvidence {
	var document yaml.Node
	if yaml.Unmarshal(data, &document) != nil || len(document.Content) != 1 {
		return workspaceProductEvidence{}
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return workspaceProductEvidence{}
	}
	evidence := workspaceProductEvidence{
		Tokens: []string{yamlScalarValue(yamlMappingValue(root, "project"))},
	}
	appendReleaseIdentityEvidence(&evidence, yamlMappingValue(root, "release_identity"))
	return evidence
}

func appendReleaseIdentityEvidence(evidence *workspaceProductEvidence, identity *yaml.Node) {
	if evidence == nil || identity == nil || identity.Kind != yaml.MappingNode {
		return
	}
	for _, key := range []string{"bundle_id", "app_store_connect_app_id"} {
		evidence.Identifiers = append(evidence.Identifiers, yamlScalarValue(yamlMappingValue(identity, key)))
	}
}

func yamlScalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(node.Value)
}

func yamlSequenceScalarValues(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	var out []string
	for _, child := range node.Content {
		if value := yamlScalarValue(child); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func yamlMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if strings.EqualFold(strings.TrimSpace(mapping.Content[i].Value), key) {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func productIdentifierKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range []string{"bundle:", "appstore:", "git_remote:"} {
		value = strings.TrimPrefix(value, prefix)
	}
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "git@")
	value = strings.ReplaceAll(value, ":", "/")
	return strings.Trim(strings.TrimSpace(value), "/")
}

func workspaceIdentityClaimMatches(claim mem.Claim, cwd string) bool {
	switch productIdentityKey(claim.Predicate) {
	case "gitroot", "reporoot", "sourcepath", "sourceroot", "workspace", "workspaceroot", "surfaces":
	default:
		return false
	}
	for _, candidate := range strings.FieldsFunc(claim.Object, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	}) {
		if workspacePathContains(candidate, cwd) {
			return true
		}
	}
	return false
}

func workspacePathContains(root, cwd string) bool {
	root = strings.TrimSpace(root)
	cwd = strings.TrimSpace(cwd)
	if root == "" || cwd == "" || !filepath.IsAbs(root) {
		return false
	}
	root = filepath.Clean(root)
	cwd = filepath.Clean(cwd)
	relative, err := filepath.Rel(root, cwd)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func productRecallFenceAllowsClaim(fence productRecallFence, claim mem.Claim) bool {
	if !fence.Required {
		return true
	}
	associated := productEntitiesForClaim(fence, claim)
	if len(associated) == 0 {
		return true
	}
	return fence.Entity != "" && len(associated) == 1 && associated[fence.Entity]
}

func productEntitiesForClaim(fence productRecallFence, claim mem.Claim) map[string]bool {
	associated := map[string]bool{}
	// Association is structural, never inferred from arbitrary prose. A global
	// law or decision may cite a product as evidence without belonging to that
	// product; importers that mean product scope must emit an entity, topic ref,
	// qualifier, or product-ref predicate.
	add := func(value string) {
		for _, token := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ';' || r == '|'
		}) {
			for _, entity := range resolveKnownProductToken(fence.known, token) {
				associated[entity] = true
			}
		}
	}
	add(claim.Entity)
	for _, topicRef := range claim.TopicRefs {
		add(topicRef)
	}
	for key, value := range claim.Qualifiers {
		switch productIdentityKey(key) {
		case "product", "productid", "productref", "scopeproduct", "domain", "topic":
			add(value)
		}
	}
	switch productIdentityKey(claim.Predicate) {
	case "product", "productid", "productref", "scopeproduct":
		add(claim.Object)
	}
	return associated
}

func seedProductFenceEntity(entities map[string]bool, fence productRecallFence) {
	if fence.Entity != "" {
		entities[memoryKey(fence.Entity)] = true
	}
}

func productRecallFenceExclusionReason(fence productRecallFence) string {
	if fence.Entity == "" {
		return "product-associated context omitted: workspace product identity " + fence.Status
	}
	return "product-associated context omitted: outside resolved workspace product fence"
}

func productRecallFenceRevision(fence productRecallFence) string {
	body := strings.Join([]string{
		productRecallFenceAuthorityRef,
		strings.TrimSpace(fence.Status),
		strings.TrimSpace(fence.Entity),
	}, "\x00")
	digest := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(digest[:])
}
