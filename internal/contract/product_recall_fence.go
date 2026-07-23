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

var supportedInfraOSProductContractSchemaVersions = map[string]bool{
	// infra-os' current project/release contract generator and inspected
	// production contracts all use schema_version 1.
	"1": true,
}

// productRecallFence is the single product-isolation seam consumed by flat
// memory, folded positions, accepted decisions, and grouped probes. It scopes
// product-associated claims while leaving genuinely global claims available.
// An empty Entity on a required fence is deliberate fail-closed state.
type productRecallFence struct {
	Required bool
	Status   string
	Identity string
	Entity   string
	known    map[string][]string
	ids      map[string][]string
	Revision string
}

type workspaceProductEvidence struct {
	PrimaryTokens []string
	Tokens        []string
	Identifiers   []string
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
	authorityCandidates := map[string]bool{}
	addToken := func(target map[string]bool, token string) {
		key := productIdentityKey(token)
		if len(fence.known[key]) > 0 {
			target[key] = true
		}
	}
	addWorkspaceClaimToken := func(target map[string]bool, token string) {
		addToken(target, token)
		// Path evidence may be owned by repo:/workspace: entities rather than
		// product: entities. Only this corroboration path interprets the final
		// namespace segment as a product alias.
		key := productFieldKey(token)
		if len(fence.known[key]) > 0 {
			target[key] = true
		}
	}
	addIdentifier := func(target map[string]bool, identifier string) {
		for _, identity := range fence.ids[productIdentifierKey(identifier)] {
			target[identity] = true
		}
	}

	evidence, contractStatus := nearestProductProjectEvidence(in.Cwd)
	if contractStatus != "valid" {
		fence.Status = "contract-" + contractStatus
		fence.Revision = productRecallFenceRevision(fence)
		return fence
	}
	for _, token := range evidence.Tokens {
		addToken(authorityCandidates, token)
	}
	for _, identifier := range evidence.Identifiers {
		addIdentifier(authorityCandidates, identifier)
	}

	// Promoted path claims may corroborate or disambiguate a valid physical
	// contract, but can never establish product identity on their own.
	claimCandidates := map[string]bool{}
	for _, claim := range claims {
		if !workspaceIdentityClaimMatches(claim, in.Cwd) {
			continue
		}
		addWorkspaceClaimToken(claimCandidates, claim.Entity)
	}

	candidates := authorityCandidates
	if len(authorityCandidates) > 1 && len(claimCandidates) > 0 {
		intersection := map[string]bool{}
		for identity := range authorityCandidates {
			if claimCandidates[identity] {
				intersection[identity] = true
			}
		}
		if len(intersection) == 1 && len(claimCandidates) == 1 {
			candidates = intersection
		}
	}
	if len(authorityCandidates) == 1 && len(claimCandidates) > 0 {
		for identity := range claimCandidates {
			if !authorityCandidates[identity] {
				candidates = map[string]bool{}
				fence.Status = "ambiguous"
				fence.Revision = productRecallFenceRevision(fence)
				return fence
			}
		}
	}

	switch len(candidates) {
	case 1:
		fence.Status = "resolved"
		for identity := range candidates {
			fence.Identity = identity
			fence.Entity = "product:" + identity
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
	products := map[string]string{}
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
		products[entity] = key
	}
	out := make(map[string][]string, len(byKey))
	for key, entities := range byKey {
		for entity := range entities {
			out[key] = append(out[key], entity)
		}
		sort.Strings(out[key])
	}
	byIdentifier := map[string]map[string]bool{}
	addIdentifier := func(identity, value string) {
		key := productIdentifierKey(value)
		if key == "" {
			return
		}
		if byIdentifier[key] == nil {
			byIdentifier[key] = map[string]bool{}
		}
		byIdentifier[key][identity] = true
	}
	for _, claim := range claims {
		identity := products[claim.Entity]
		if identity == "" {
			continue
		}
		switch productFieldKey(claim.Predicate) {
		case "surfaces", "releasebundleid", "releaseappstoreconnectappid":
			for _, value := range strings.FieldsFunc(claim.Object, func(r rune) bool {
				return r == ',' || r == ';' || r == '|'
			}) {
				addIdentifier(identity, value)
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

func productIdentityKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(value, "product:") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "product:"))
	}
	return compactProductIdentity(value)
}

func productFieldKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if i := strings.LastIndex(value, ":"); i >= 0 && i+1 < len(value) {
		value = value[i+1:]
	}
	return compactProductIdentity(value)
}

func compactProductIdentity(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func nearestProductProjectEvidence(cwd string) (workspaceProductEvidence, string) {
	dir, err := canonicalWorkspacePath(cwd)
	if err != nil {
		return workspaceProductEvidence{}, "invalid"
	}
	for {
		projectData, projectPresent, projectErr := readOptionalProductContract(filepath.Join(dir, "project.yaml"))
		releaseData, releasePresent, releaseErr := readOptionalProductContract(filepath.Join(dir, "release.yaml"))
		if projectErr != nil || releaseErr != nil {
			return workspaceProductEvidence{}, "invalid"
		}
		if projectPresent || releasePresent {
			var evidence workspaceProductEvidence
			if projectPresent {
				projectEvidence, ok := productProjectEvidence(projectData)
				if !ok {
					return workspaceProductEvidence{}, "invalid"
				}
				evidence.PrimaryTokens = append(evidence.PrimaryTokens, projectEvidence.PrimaryTokens...)
				evidence.Tokens = append(evidence.Tokens, projectEvidence.Tokens...)
				evidence.Identifiers = append(evidence.Identifiers, projectEvidence.Identifiers...)
			}
			if releasePresent {
				releaseEvidence, ok := productReleaseEvidence(releaseData)
				if !ok {
					return workspaceProductEvidence{}, "invalid"
				}
				evidence.PrimaryTokens = append(evidence.PrimaryTokens, releaseEvidence.PrimaryTokens...)
				evidence.Tokens = append(evidence.Tokens, releaseEvidence.Tokens...)
				evidence.Identifiers = append(evidence.Identifiers, releaseEvidence.Identifiers...)
			}
			evidence.PrimaryTokens = compactProbeStrings(evidence.PrimaryTokens)
			evidence.Tokens = compactProbeStrings(evidence.Tokens)
			evidence.Identifiers = compactProbeStrings(evidence.Identifiers)
			if !oneProductContractIdentity(evidence.PrimaryTokens) || (len(evidence.Tokens) == 0 && len(evidence.Identifiers) == 0) {
				return workspaceProductEvidence{}, "invalid"
			}
			return evidence, "valid"
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return workspaceProductEvidence{}, "absent"
		}
		dir = parent
	}
}

func readOptionalProductContract(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, true, nil
	}
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func canonicalWorkspacePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", os.ErrInvalid
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	physical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(physical), nil
}

func productProjectEvidence(data []byte) (workspaceProductEvidence, bool) {
	root, ok := validProductContractRoot(data)
	if !ok {
		return workspaceProductEvidence{}, false
	}
	var evidence workspaceProductEvidence
	project := yamlMappingValue(root, "project")
	if project == nil || project.Kind != yaml.MappingNode {
		return workspaceProductEvidence{}, false
	}
	slug, ok := yamlStringValue(yamlMappingValue(project, "slug"))
	if !ok || genericProductIdentifier(slug) {
		return workspaceProductEvidence{}, false
	}
	evidence.Tokens = append(evidence.Tokens, slug)
	evidence.PrimaryTokens = append(evidence.PrimaryTokens, slug)
	if nameNode := yamlMappingValue(project, "name"); nameNode != nil {
		name, valid := yamlStringValue(nameNode)
		if !valid {
			return workspaceProductEvidence{}, false
		}
		if !genericProductIdentifier(name) {
			evidence.Tokens = append(evidence.Tokens, name)
		}
	}
	if aliasesNode := yamlMappingValue(project, "aliases"); aliasesNode != nil {
		aliases, valid := yamlProductAliasValues(aliasesNode)
		if !valid {
			return workspaceProductEvidence{}, false
		}
		for _, alias := range aliases {
			if !genericProductIdentifier(alias) {
				evidence.Tokens = append(evidence.Tokens, alias)
			}
		}
	}
	if source := yamlMappingValue(root, "source"); source != nil {
		if source.Kind != yaml.MappingNode {
			return workspaceProductEvidence{}, false
		}
		if authority := yamlMappingValue(source, "authority"); authority != nil {
			if authority.Kind != yaml.MappingNode {
				return workspaceProductEvidence{}, false
			}
			if repoNode := yamlMappingValue(authority, "repo"); repoNode != nil {
				repo, valid := yamlStringValue(repoNode)
				if !valid {
					return workspaceProductEvidence{}, false
				}
				evidence.Identifiers = append(evidence.Identifiers, repo)
			}
		}
	}
	if !appendReleaseIdentityEvidence(&evidence, yamlMappingValue(root, "release_identity")) {
		return workspaceProductEvidence{}, false
	}
	return evidence, true
}

func productReleaseEvidence(data []byte) (workspaceProductEvidence, bool) {
	root, ok := validProductContractRoot(data)
	if !ok {
		return workspaceProductEvidence{}, false
	}
	project, ok := yamlStringValue(yamlMappingValue(root, "project"))
	if !ok || genericProductIdentifier(project) {
		return workspaceProductEvidence{}, false
	}
	evidence := workspaceProductEvidence{
		PrimaryTokens: []string{project},
		Tokens:        []string{project},
	}
	if !appendReleaseIdentityEvidence(&evidence, yamlMappingValue(root, "release_identity")) {
		return workspaceProductEvidence{}, false
	}
	return evidence, true
}

func oneProductContractIdentity(tokens []string) bool {
	identities := map[string]bool{}
	for _, token := range tokens {
		if identity := productIdentityKey(token); identity != "" {
			identities[identity] = true
		}
	}
	return len(identities) == 1
}

func validProductContractRoot(data []byte) (*yaml.Node, bool) {
	var document yaml.Node
	if yaml.Unmarshal(data, &document) != nil || len(document.Content) != 1 {
		return nil, false
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode || !yamlMappingKeysUnique(root) {
		return nil, false
	}
	version := yamlMappingValue(root, "schema_version")
	if version == nil || version.Kind != yaml.ScalarNode || !supportedInfraOSProductContractSchemaVersions[strings.TrimSpace(version.Value)] {
		return nil, false
	}
	switch version.Tag {
	case "", "!!int", "!!str":
	default:
		return nil, false
	}
	return root, true
}

func yamlMappingKeysUnique(node *yaml.Node) bool {
	if node == nil {
		return true
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode {
				return false
			}
			normalized := strings.ToLower(strings.TrimSpace(key.Value))
			if normalized == "" || normalized == "<<" || seen[normalized] {
				return false
			}
			seen[normalized] = true
			if !yamlMappingKeysUnique(node.Content[i+1]) {
				return false
			}
		}
		return len(node.Content)%2 == 0
	}
	for _, child := range node.Content {
		if !yamlMappingKeysUnique(child) {
			return false
		}
	}
	return true
}

func appendReleaseIdentityEvidence(evidence *workspaceProductEvidence, identity *yaml.Node) bool {
	if evidence == nil {
		return false
	}
	if identity == nil {
		return true
	}
	if identity.Kind != yaml.MappingNode {
		return false
	}
	for _, key := range []string{"bundle_id", "app_store_connect_app_id"} {
		node := yamlMappingValue(identity, key)
		if node == nil {
			continue
		}
		value, ok := yamlIdentityScalarValue(node)
		if !ok {
			return false
		}
		evidence.Identifiers = append(evidence.Identifiers, value)
	}
	return true
}

func yamlStringValue(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", false
	}
	if node.Tag != "" && node.Tag != "!!str" {
		return "", false
	}
	return strings.TrimSpace(node.Value), true
}

func yamlIdentityScalarValue(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", false
	}
	switch node.Tag {
	case "", "!!str", "!!int":
	default:
		return "", false
	}
	return strings.TrimSpace(node.Value), true
}

func yamlProductAliasValues(node *yaml.Node) ([]string, bool) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, false
	}
	var out []string
	for _, child := range node.Content {
		if value, ok := yamlStringValue(child); ok {
			if value != "" {
				out = append(out, value)
			}
			continue
		}
		// yaml.v3 parses an unquoted list item such as
		// "Example Product: Mobile Client" as a one-pair mapping. Current
		// legacy contracts contain that spelling, so preserve its
		// intended alias without weakening duplicate/schema validation.
		if child.Kind == yaml.MappingNode && len(child.Content) == 2 {
			left, leftOK := yamlStringValue(child.Content[0])
			right, rightOK := yamlStringValue(child.Content[1])
			if leftOK && rightOK && left != "" && right != "" {
				out = append(out, left+": "+right)
				continue
			}
		}
		return nil, false
	}
	return out, true
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
	if genericProductIdentifier(value) {
		return ""
	}
	for _, prefix := range []string{"bundle:", "appstore:", "git_remote:"} {
		value = strings.TrimPrefix(value, prefix)
	}
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "git@")
	value = strings.ReplaceAll(value, ":", "/")
	value = strings.Trim(strings.TrimSpace(value), "/")
	if genericProductIdentifier(value) {
		return ""
	}
	return value
}

func genericProductIdentifier(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "-", "unknown", "none", "null", "n/a", "na", "tbd", "todo", "not_configured", "not-configured", "unconfigured":
		return true
	default:
		return false
	}
}

func workspaceIdentityClaimMatches(claim mem.Claim, cwd string) bool {
	switch productFieldKey(claim.Predicate) {
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
	var err error
	root, err = canonicalWorkspacePath(root)
	if err != nil {
		return false
	}
	cwd, err = canonicalWorkspacePath(cwd)
	if err != nil {
		return false
	}
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
	associated, unknown := productEntitiesForClaim(fence, claim)
	if len(associated) == 0 && !unknown {
		return true
	}
	return !unknown && fence.Identity != "" && len(associated) == 1 && associated[fence.Identity]
}

func productEntitiesForClaim(fence productRecallFence, claim mem.Claim) (map[string]bool, bool) {
	associated := map[string]bool{}
	unknown := false
	// Association is structural, never inferred from arbitrary prose. A global
	// law or decision may cite a product as evidence without belonging to that
	// product; importers that mean product scope must emit an entity, topic ref,
	// qualifier, or product-ref predicate.
	add := func(value string, unknownWhenUnresolved bool) {
		sawToken := false
		for _, token := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ';' || r == '|'
		}) {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			sawToken = true
			identity := productIdentityKey(token)
			if len(fence.known[identity]) > 0 {
				associated[identity] = true
			} else if unknownWhenUnresolved {
				unknown = true
			}
		}
		if unknownWhenUnresolved && !sawToken {
			unknown = true
		}
	}
	add(claim.Entity, strings.HasPrefix(strings.ToLower(strings.TrimSpace(claim.Entity)), "product:"))
	if identity := productFieldKey(claim.Entity); len(fence.known[identity]) > 0 {
		associated[identity] = true
	}
	for _, topicRef := range claim.TopicRefs {
		add(topicRef, strings.HasPrefix(strings.ToLower(strings.TrimSpace(topicRef)), "product:"))
	}
	for key, value := range claim.Qualifiers {
		switch productFieldKey(key) {
		case "product", "productid", "productref", "scopeproduct":
			add(value, true)
		case "domain", "domains", "topic", "scope", "scopes":
			add(value, strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "product:"))
		}
	}
	switch productFieldKey(claim.Predicate) {
	case "product", "productid", "productref", "scopeproduct":
		add(claim.Object, true)
	}
	return associated, unknown
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
		strings.TrimSpace(fence.Identity),
		strings.TrimSpace(fence.Entity),
	}, "\x00")
	digest := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(digest[:])
}
