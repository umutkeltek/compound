package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/release"
)

func cmdRelease(args []string) {
	if len(args) == 0 || hasFlag(args, "--help") || hasFlag(args, "-h") {
		fmt.Fprintln(os.Stderr, "usage: compound release export-public [--repo P] [--target archive.tar.gz] [--prefix NAME] [--json]")
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}
	switch args[0] {
	case "export-public":
		report, err := runReleaseExportPublic(args[1:], time.Now())
		if err != nil {
			fmt.Fprintln(os.Stderr, "release export-public:", err)
			os.Exit(1)
		}
		if hasFlag(args[1:], "--json") {
			printJSON(report)
			return
		}
		fmt.Printf("# release export-public %s\n\n", report.Status)
		fmt.Printf("- target: %s\n", report.TargetPath)
		fmt.Printf("- commit: %s\n", report.Commit)
		fmt.Printf("- files: %d\n", report.Files)
		fmt.Printf("- bytes: %d\n", report.Bytes)
	default:
		fmt.Fprintln(os.Stderr, "usage: compound release export-public [--repo P] [--target archive.tar.gz] [--prefix NAME] [--json]")
		os.Exit(2)
	}
}

func runReleaseExportPublic(args []string, now time.Time) (release.PublicExportReport, error) {
	repo := flagValue(args, "--repo")
	target := flagValue(args, "--target")
	if strings.TrimSpace(target) == "" {
		homePath, err := home.Resolve("")
		if err != nil {
			return release.PublicExportReport{}, err
		}
		target = filepath.Join(homePath, "exports", "public-source", "compound-public-"+now.UTC().Format("20060102T150405Z")+".tar.gz")
	}
	return release.ExportPublic(context.Background(), release.PublicExportOptions{
		RepoPath:   repo,
		TargetPath: target,
		Prefix:     flagValue(args, "--prefix"),
	}, now)
}
