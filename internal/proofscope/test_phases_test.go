package proofscope

import (
	"reflect"
	"testing"
)

func TestTestPackagePhasesKeepExactWorkSeparateAndAllPackages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		packages []string
		want     []TestPackagePhase
	}{
		{"empty", nil, nil},
		{"isolated-only", []string{"compound/internal/home", "compound/internal/release", "compound/internal/work"},
			[]TestPackagePhase{{Name: "work", Packages: []string{"compound/internal/work"}}, {Name: "release", Packages: []string{"compound/internal/release"}}, {Name: "home", Packages: []string{"compound/internal/home"}}}},
		{"work-only", []string{"compound/internal/work"},
			[]TestPackagePhase{{Name: "work", Packages: []string{"compound/internal/work"}}}},
		{"exact-path-only", []string{"compound/internal/work/child", "compound/internal/workbench", "compound/internal/work"},
			[]TestPackagePhase{{Name: "general", Packages: []string{"compound/internal/work/child", "compound/internal/workbench"}}, {Name: "work", Packages: []string{"compound/internal/work"}}}},
		{"mixed", []string{"compound/internal/home", "compound/scripts/testbattery", "compound/internal/work", "compound/cmd/compound", "compound/internal/release"},
			[]TestPackagePhase{{Name: "general", Packages: []string{"compound/cmd/compound", "compound/scripts/testbattery"}}, {Name: "work", Packages: []string{"compound/internal/work"}}, {Name: "release", Packages: []string{"compound/internal/release"}}, {Name: "home", Packages: []string{"compound/internal/home"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := append([]string(nil), tt.packages...)
			if got := TestPackagePhases(tt.packages); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("phases=%v; want %v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.packages, original) {
				t.Fatal("phase selection mutated the source inventory")
			}
		})
	}
}
