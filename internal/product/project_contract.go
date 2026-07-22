package product

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type projectContract struct {
	Project struct {
		Slug    string `yaml:"slug"`
		Name    string `yaml:"name"`
		Class   string `yaml:"class"`
		Status  string `yaml:"status"`
		Owner   string `yaml:"owner"`
		Notes   string `yaml:"notes"`
		Aliases []any  `yaml:"aliases"`
	} `yaml:"project"`
	Source struct {
		Authority struct {
			Path string `yaml:"path"`
			Repo string `yaml:"repo"`
		} `yaml:"authority"`
	} `yaml:"source"`
	Runtime struct {
		Authority    string   `yaml:"authority"`
		ServiceNames []string `yaml:"service_names"`
		RuntimePaths []string `yaml:"runtime_paths"`
		URLs         []string `yaml:"urls"`
		Healthchecks []string `yaml:"healthchecks"`
		Primary      string   `yaml:"primary"`
	} `yaml:"runtime"`
	Data struct {
		Classification  string   `yaml:"classification"`
		LocalPaths      []string `yaml:"local_paths"`
		Databases       []string `yaml:"databases"`
		ExternalSources []string `yaml:"external_sources"`
		Backend         string   `yaml:"backend"`
		CloudResources  string   `yaml:"cloud_resources"`
	} `yaml:"data"`
	ReleaseIdentity releaseIdentity `yaml:"release_identity"`
}

type releaseContract struct {
	Project         string          `yaml:"project"`
	ReleaseStatus   string          `yaml:"release_status"`
	ReleaseIdentity releaseIdentity `yaml:"release_identity"`
}

type releaseIdentity struct {
	BundleID             string   `yaml:"bundle_id"`
	AppStoreConnectAppID string   `yaml:"app_store_connect_app_id"`
	SubscriptionIDs      []string `yaml:"subscription_product_ids"`
	Version              string   `yaml:"version"`
	Build                any      `yaml:"build"`
}

// LoadProjectContractBundle adapts infra-os-style project/release contracts into
// the existing Product registry bundle. It is source-in-place intake only:
// analytics and causality still enter through ProductSignal.
func LoadProjectContractBundle(projectPath, releasePath string) (Bundle, error) {
	pc, err := loadProjectContract(projectPath)
	if err != nil {
		return Bundle{}, err
	}
	var rc releaseContract
	if strings.TrimSpace(releasePath) != "" {
		rc, err = loadReleaseContract(releasePath)
		if err != nil {
			return Bundle{}, err
		}
	}
	return ProjectContractBundle(pc, rc, filepath.Base(projectPath), filepath.Base(releasePath))
}

func loadProjectContract(path string) (projectContract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return projectContract{}, fmt.Errorf("read project contract %s: %w", path, err)
	}
	var pc projectContract
	if err := yaml.Unmarshal(data, &pc); err != nil {
		return projectContract{}, fmt.Errorf("parse project contract %s: %w", path, err)
	}
	return pc, nil
}

func loadReleaseContract(path string) (releaseContract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return releaseContract{}, fmt.Errorf("read release contract %s: %w", path, err)
	}
	var rc releaseContract
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return releaseContract{}, fmt.Errorf("parse release contract %s: %w", path, err)
	}
	return rc, nil
}

func ProjectContractBundle(pc projectContract, rc releaseContract, projectRef, releaseRef string) (Bundle, error) {
	slug := strings.TrimSpace(pc.Project.Slug)
	if slug == "" {
		slug = strings.TrimSpace(rc.Project)
	}
	if slug == "" {
		return Bundle{}, fmt.Errorf("project contract project.slug required")
	}
	if rc.Project != "" && pc.Project.Slug != "" && strings.TrimSpace(rc.Project) != strings.TrimSpace(pc.Project.Slug) {
		return Bundle{}, fmt.Errorf("release project %q does not match project slug %q", rc.Project, pc.Project.Slug)
	}
	name := firstNonEmpty(pc.Project.Name, slug)
	identity := mergeReleaseIdentity(pc.ReleaseIdentity, rc.ReleaseIdentity)
	productID := "product:" + normalizeIDPart(slug)
	evidenceRefs := compact([]string{"project-contract:" + projectRef, optionalRef("release-contract:", releaseRef)})

	bundle := Bundle{
		SchemaVersion: SchemaVersion,
		Product: Product{
			ID:              productID,
			Name:            name,
			OwnerEntity:     ownerEntity(pc.Project.Owner, slug),
			Surfaces:        projectSurfaces(pc, identity),
			Channels:        projectChannels(pc, identity),
			Positioning:     projectPositioning(pc, rc, name),
			PrivacyPosture:  projectPrivacy(pc),
			ReleaseIdentity: exportedReleaseIdentity(identity),
			TasteProfile: TasteProfile{
				Standards: []string{"source-in-place product contract"},
				RedLines:  []string{"do not infer product signal or causality from project contract"},
			},
		},
		EvidenceRefs: evidenceRefs,
	}
	if err := Validate(bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func exportedReleaseIdentity(identity releaseIdentity) ReleaseIdentity {
	build := ""
	if identity.Build != nil {
		build = strings.TrimSpace(fmt.Sprint(identity.Build))
	}
	return ReleaseIdentity{
		BundleID:             strings.TrimSpace(identity.BundleID),
		AppStoreConnectAppID: strings.TrimSpace(identity.AppStoreConnectAppID),
		SubscriptionIDs:      compact(identity.SubscriptionIDs),
		Version:              strings.TrimSpace(identity.Version),
		Build:                build,
	}
}

func mergeReleaseIdentity(project, release releaseIdentity) releaseIdentity {
	out := project
	if strings.TrimSpace(release.BundleID) != "" {
		out.BundleID = release.BundleID
	}
	if strings.TrimSpace(release.AppStoreConnectAppID) != "" {
		out.AppStoreConnectAppID = release.AppStoreConnectAppID
	}
	if len(release.SubscriptionIDs) > 0 {
		out.SubscriptionIDs = append([]string(nil), release.SubscriptionIDs...)
	}
	if strings.TrimSpace(release.Version) != "" {
		out.Version = release.Version
	}
	if release.Build != nil {
		out.Build = release.Build
	}
	return out
}

func projectSurfaces(pc projectContract, id releaseIdentity) []Surface {
	var surfaces []Surface
	add := func(surfaceID, kind, ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		surfaces = append(surfaces, Surface{ID: surfaceID, Kind: kind, Ref: ref})
	}
	add("source-repo", "repo", pc.Source.Authority.Repo)
	if pc.Source.Authority.Repo == "" {
		add("source-path", "repo", pc.Source.Authority.Path)
	}
	add("bundle", "app_bundle", optionalRef("bundle:", id.BundleID))
	add("app-store", "app_store", optionalRef("appstore:", id.AppStoreConnectAppID))
	for i, url := range pc.Runtime.URLs {
		add(fmt.Sprintf("url-%d", i+1), "web", url)
	}
	for i, url := range pc.Runtime.Healthchecks {
		add(fmt.Sprintf("healthcheck-%d", i+1), "healthcheck", url)
	}
	if len(surfaces) == 0 {
		add("project", "project", pc.Project.Slug)
	}
	return surfaces
}

func projectChannels(pc projectContract, id releaseIdentity) []string {
	var channels []string
	if strings.TrimSpace(id.AppStoreConnectAppID) != "" || strings.TrimSpace(id.BundleID) != "" {
		channels = append(channels, "app_store")
	}
	if len(pc.Runtime.URLs) > 0 {
		channels = append(channels, "web")
	}
	if len(pc.Runtime.Healthchecks) > 0 || len(pc.Runtime.ServiceNames) > 0 {
		channels = append(channels, "api")
	}
	if strings.Contains(strings.ToLower(pc.Runtime.Primary), "ios") {
		channels = append(channels, "ios")
	}
	if strings.TrimSpace(pc.Source.Authority.Repo) != "" || strings.TrimSpace(pc.Source.Authority.Path) != "" {
		channels = append(channels, "repo")
	}
	if len(channels) == 0 {
		channels = append(channels, "source_contract")
	}
	return compact(channels)
}

func projectPositioning(pc projectContract, rc releaseContract, name string) string {
	if strings.TrimSpace(pc.Project.Notes) != "" {
		return strings.TrimSpace(pc.Project.Notes)
	}
	if strings.TrimSpace(rc.ReleaseStatus) != "" {
		return fmt.Sprintf("%s release status: %s.", name, strings.TrimSpace(rc.ReleaseStatus))
	}
	return fmt.Sprintf("%s product source contract (%s/%s).", name, firstNonEmpty(pc.Project.Class, "unknown_class"), firstNonEmpty(pc.Project.Status, "unknown_status"))
}

func projectPrivacy(pc projectContract) string {
	if strings.TrimSpace(pc.Data.Classification) != "" {
		return strings.TrimSpace(pc.Data.Classification)
	}
	if strings.TrimSpace(pc.Data.Backend) == "none" || strings.Contains(strings.ToLower(strings.Join(pc.Data.LocalPaths, " ")), "local") {
		return "local_first"
	}
	return "unspecified_project_contract"
}

func ownerEntity(owner, slug string) string {
	owner = normalizeIDPart(owner)
	if owner != "" {
		return "owner:" + owner
	}
	return "project:" + normalizeIDPart(slug)
}

func optionalRef(prefix, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return prefix + value
}
