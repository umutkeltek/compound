package proofscope

import "sort"

// TestPackagePhase groups selected packages around the existing shared IO owners.
type TestPackagePhase struct {
	Name     string
	Packages []string
}

// TestPackagePhases accepts a source-owned full inventory or affected subset.
// Work has substantial serial IO, Release exercises machine-wide sync barriers,
// and Home has scale assertions. Each runs alone, after general packages.
// Selection and coverage are kept.
func TestPackagePhases(packages []string) []TestPackagePhase {
	var general, work, release, home []string
	for _, pkg := range packages {
		switch pkg {
		case "compound/internal/work":
			work = append(work, pkg)
		case "compound/internal/release":
			release = append(release, pkg)
		case "compound/internal/home":
			home = append(home, pkg)
		default:
			general = append(general, pkg)
		}
	}
	sort.Strings(general)
	var phases []TestPackagePhase
	for _, phase := range []TestPackagePhase{
		{Name: "general", Packages: general},
		{Name: "work", Packages: work},
		{Name: "release", Packages: release},
		{Name: "home", Packages: home},
	} {
		if len(phase.Packages) > 0 {
			phases = append(phases, phase)
		}
	}
	return phases
}
