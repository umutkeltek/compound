package mem

import "strings"

const clusterSep = "\x1f"

// ClusterKey is the pure topic identity used by memory anti-echo logic.
func ClusterKey(entity, topic, claimType string) string {
	return strings.Join([]string{
		NormalizeClaimValue(entity),
		NormalizeClaimValue(topic),
		NormalizeClaimValue(claimType),
	}, clusterSep)
}

// Topic is the candidate-side topic axis: domain qualifier first, predicate fallback.
func (c Candidate) Topic() string {
	return topicFromQualifiers(c.Predicate, c.Qualifiers)
}

// ClusterKey resolves the entity through the registry when there is one unambiguous slug.
func (s *Store) ClusterKey(entity, topic, claimType string) (string, error) {
	resolved := entity
	if r, err := s.ResolveEntity(entity); err != nil {
		return "", err
	} else if r.Status == "resolved" {
		resolved = r.Slug
	}
	return ClusterKey(resolved, topic, claimType), nil
}

func (s *Store) candidateClusterKey(c Candidate) (string, error) {
	return s.ClusterKey(decisionClusterEntity(c.Entity, c.ClaimType, c.Qualifiers, ""), c.Topic(), c.ClaimType)
}

func (s *Store) claimClusterKey(c Claim) (string, error) {
	return s.ClusterKey(decisionClusterEntity(c.Entity, c.ClaimType, c.Qualifiers, c.ID), c.Topic(), c.ClaimType)
}

func sameClusterKey(a, b string) bool {
	return a != "" && a == b
}

// NormalizeClaimValue is used for identity comparison, not display.
func NormalizeClaimValue(v string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(v))), " ")
}

func SameClaimQualifiers(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, av := range a {
		if NormalizeClaimValue(b[key]) != NormalizeClaimValue(av) {
			return false
		}
	}
	return true
}

func SameClaimObject(a, b string) bool {
	return NormalizeClaimValue(a) == NormalizeClaimValue(b)
}

func topicFromQualifiers(predicate string, qualifiers map[string]string) string {
	if d := qualifiers["domain"]; d != "" {
		return d
	}
	if d := qualifiers["domains"]; d != "" {
		return d
	}
	return predicate
}

func decisionClusterEntity(entity, claimType string, qualifiers map[string]string, fallbackID string) string {
	if NormalizeClaimValue(claimType) != "decision" {
		return entity
	}
	if strings.HasPrefix(NormalizeClaimValue(entity), "decision:") {
		return entity
	}
	if id := qualifiers["decision_id"]; id != "" {
		return decisionEntityKey(id)
	}
	if isMeaningfulClaimID(fallbackID) {
		return decisionEntityKey(fallbackID)
	}
	return entity
}

func decisionEntityKey(id string) string {
	if strings.HasPrefix(NormalizeClaimValue(id), "decision:") {
		return id
	}
	return "decision:" + id
}
