// Package authority is Layer 2: what's TRUE about your world — which project runs
// on which host/cloud, its repo, database, domain. It is NOT a new object model:
// it imports infra-os into the SAME Memory primitives (Entity + promoted Claim),
// so authority facts get positions, supersession on migration, and recall for
// free. The Memory machinery, pointed at infrastructure instead of conversations.
package authority

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/umutkeltek/compound/internal/memory"
)

// DefaultRegistry is infra-os's richest project→authority source.
func DefaultRegistry() string {
	return filepath.Join(os.Getenv("HOME"), "Projects/operating-system/infra-os/project-registry.yaml")
}

type registryFile struct {
	Production map[string]projectRec `yaml:"production"`
}

type projectRec struct {
	Class           string `yaml:"class"`
	SourceAuthority struct {
		Host string `yaml:"host"`
		Repo string `yaml:"repo"`
	} `yaml:"source_authority"`
	Runtime struct {
		Host      string `yaml:"host"`
		Authority string `yaml:"authority"`
	} `yaml:"runtime"`
	DataAuthority struct {
		Database        string `yaml:"database"`
		PrimaryDatabase string `yaml:"primary_database"`
	} `yaml:"data_authority"`
	AWS struct {
		Profile string `yaml:"profile"`
	} `yaml:"aws"`
	URLs struct {
		App string `yaml:"app"`
	} `yaml:"urls"`
}

// Import parses an infra-os project registry into Entities + promoted authority
// Claims. Claim IDs are deterministic (auth_<slug>_<predicate>) so re-import
// updates current truth in place (a migration overwrites the old value).
func Import(path string) (entities []memory.Entity, claims []memory.Claim, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var rf registryFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	src := "infra-os:" + filepath.Base(path)

	for slug, p := range rf.Production {
		etype := memory.EntProject
		if strings.Contains(p.Class, "product") {
			etype = memory.EntProduct
		}
		entities = append(entities, memory.Entity{
			Slug: slug, Type: etype, VisibilityDefault: memory.VisWorld, CreatedAt: now,
		})

		add := func(pred, obj string) {
			obj = strings.TrimSpace(obj)
			if obj == "" {
				return
			}
			claims = append(claims, memory.Claim{
				ID:             fmt.Sprintf("auth_%s_%s", slug, pred),
				SubjectEntity:  slug,
				Predicate:      pred,
				Object:         obj,
				ClaimType:      memory.ClaimTypeStatus,
				Confidence:     1.0,
				Visibility:     memory.VisWorld,
				Status:         memory.StatusPromoted, // declared infra truth, not a candidate
				PromotionBasis: memory.PromoRuntimeProof,
				CreatedBy:      "authority-import",
				ResolverVer:    src,
				Qualifiers:     map[string]any{"domain": pred},
			})
		}

		runtime := firstNonEmpty(p.Runtime.Host, p.Runtime.Authority)
		db := firstNonEmpty(p.DataAuthority.Database, p.DataAuthority.PrimaryDatabase)
		add("source_repo", p.SourceAuthority.Repo)
		add("source_host", p.SourceAuthority.Host)
		add("deployed_on", runtime)
		add("database", db)
		add("cloud_profile", p.AWS.Profile)
		add("domain", p.URLs.App)
	}
	return entities, claims, nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
