package policy

import "testing"

func TestLocalVerificationCommandsStayAutomatic(t *testing.T) {
	commands := []string{
		"bash -n Scripts/check.sh",
		"/bin/bash -n Scripts/check.sh",
		"swift --version",
		"swift -version",
		"swift test",
		"swift test --filter PlannerTests",
		"swift build",
		"swift build --configuration release",
		"swift package describe",
		"swift package dump-package",
		"make --version",
		"make -v",
		"make --help",
		"make -h",
		"make test",
		"make build",
		"make app",
		"make verify-direct-release",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			monitored := MonitorToolAction(
				monitorRequest("Bash", map[string]any{"command": command}),
				DefaultConfig(),
				fixedNow,
			)
			if !monitored.Classification.Known || monitored.Classification.Operation != "local_command" {
				t.Fatalf("local verification became unknown or consequential: %q => %+v", command, monitored.Classification)
			}
			if !Allowed(monitored.Decision) {
				t.Fatalf("local verification should remain automatic: %q => %+v", command, monitored.Decision)
			}
		})
	}
}

func TestLocalVerificationClassifierKeepsUnsafeNeighborsHeld(t *testing.T) {
	commands := []string{
		"bash Scripts/check.sh",
		"bash -c 'echo hi'",
		"bash -n -c 'echo hi'",
		"bash -x Scripts/check.sh",
		"bash -n",
		"CI=1 bash -n Scripts/check.sh",
		"BASH_ENV=/synthetic/startup.sh bash -n Scripts/check.sh",
		"env BASH_ENV=/synthetic/startup.sh bash -n Scripts/check.sh",
		"PATH=/synthetic/bin bash -n Scripts/check.sh",
		"LD_PRELOAD=/synthetic/library.so bash -n Scripts/check.sh",
		"DYLD_INSERT_LIBRARIES=/synthetic/library.dylib bash -n Scripts/check.sh",
		"sh -n Scripts/check.sh",
		"swift run Tool",
		"swift Scripts/generate.swift",
		"swift package update",
		"swift package resolve",
		"swift package clean",
		"swift test --disable-sandbox",
		"swift test --package-path ..",
		"swift build --experimental-unknown",
		"swift TEST",
		"swift Build",
		"CI=1 swift test",
		"PATH=/synthetic/bin swift test",
		"make",
		"make TEST",
		"CI=1 make test",
		"MAKEFILES=/synthetic/extra.mk make test",
		"env MAKEFLAGS=-f/synthetic/extra.mk make test",
		"make clean",
		"make release",
		"make install",
		"make publish-release",
		"make notarize",
		"make verify-app-store-release",
		"make build-universal",
		"make -f Other.mk test",
		"make test SHELL=/bin/sh",
		"make test app",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			monitored := MonitorToolAction(
				monitorRequest("Bash", map[string]any{"command": command}),
				DefaultConfig(),
				fixedNow,
			)
			if monitored.Classification.Known && monitored.Classification.Operation == "local_command" {
				t.Fatalf("unsafe or unknown verification entered the routine lane: %q => %+v", command, monitored.Classification)
			}
			if Allowed(monitored.Decision) {
				t.Fatalf("unsafe or unknown verification should remain held: %q => %+v", command, monitored)
			}
		})
	}
}
