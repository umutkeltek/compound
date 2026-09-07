package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"compound/internal/authority/gitrepository"
)

var proofCheckCeremonyOwnedIgnoredInputs = []string{
	"docs_private/leak-tokens.txt",
	"docs_private/source-leak-tokens.txt",
	"docs_private/plan",
	"console/node_modules",
	"docs_private/profiles",
}

func runProofCheckSealedSnapshotContext(
	ctx context.Context,
	repoPath string,
	dryRun bool,
	source gitrepository.Snapshot,
	ciOnly bool,
	ciConsole bool,
	now time.Time,
) (proofCheckReport, error) {
	originalRepo, err := resolveProofCheckRepo(repoPath)
	if err != nil {
		return proofCheckReport{}, err
	}
	ctx = context.WithValue(ctx, proofCheckCacheOriginKey{}, originalRepo)
	sealedRepo, cleanup, err := prepareProofCheckSealedWorkspace(ctx, originalRepo, source)
	if err != nil {
		return proofCheckReport{}, err
	}

	report, runErr := runProofCheckSnapshotContext(
		ctx,
		sealedRepo,
		dryRun,
		source,
		ciOnly,
		ciConsole,
		now,
	)
	if cleanupErr := cleanup(); cleanupErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("remove sealed proof workspace: %w", cleanupErr))
	}
	report.Repo = originalRepo
	return report, runErr
}

func prepareProofCheckSealedWorkspace(
	ctx context.Context,
	repo string,
	source gitrepository.Snapshot,
) (string, func() error, error) {
	tmpRoot, err := os.MkdirTemp("", "compound-proofcheck-sealed-*")
	if err != nil {
		return "", nil, fmt.Errorf("create sealed proof workspace: %w", err)
	}
	cleanup := func() error {
		return os.RemoveAll(tmpRoot)
	}
	sealedRepo := filepath.Join(tmpRoot, "repo")
	if output, cloneErr := proofCheckCommandContext(
		ctx,
		"git",
		"clone",
		"--quiet",
		"--shared",
		"--no-checkout",
		"--",
		repo,
		sealedRepo,
	).CombinedOutput(); cloneErr != nil {
		cleanupErr := cleanup()
		return "", nil, fmt.Errorf(
			"clone sealed proof workspace: %w",
			errors.Join(
				fmt.Errorf("%w: %s", cloneErr, strings.TrimSpace(string(output))),
				cleanupErr,
			),
		)
	}
	if output, checkoutErr := proofCheckCommandContext(
		ctx,
		"git",
		"-C",
		sealedRepo,
		"-c",
		"advice.detachedHead=false",
		"checkout",
		"--quiet",
		"--detach",
		"--force",
		source.HeadSHA,
	).CombinedOutput(); checkoutErr != nil {
		cleanupErr := cleanup()
		return "", nil, fmt.Errorf(
			"checkout sealed proof head %s: %w",
			source.HeadSHA,
			errors.Join(
				fmt.Errorf("%w: %s", checkoutErr, strings.TrimSpace(string(output))),
				cleanupErr,
			),
		)
	}
	if err := copyProofCheckCeremonyInputs(ctx, repo, sealedRepo); err != nil {
		cleanupErr := cleanup()
		return "", nil, errors.Join(err, cleanupErr)
	}
	return sealedRepo, cleanup, nil
}

func copyProofCheckCeremonyInputs(ctx context.Context, sourceRepo, sealedRepo string) error {
	for _, relativePath := range proofCheckCeremonyOwnedIgnoredInputs {
		sourcePath := filepath.Join(sourceRepo, filepath.FromSlash(relativePath))
		if _, err := os.Lstat(sourcePath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect proof ceremony input %s: %w", relativePath, err)
		}
		if err := validateProofCheckCeremonyInputSnapshot(sourcePath); err != nil {
			return fmt.Errorf("validate proof ceremony input %s: %w", relativePath, err)
		}
		destinationPath := filepath.Join(sealedRepo, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
			return fmt.Errorf("prepare proof ceremony input %s: %w", relativePath, err)
		}
		args := []string{"-R"}
		if runtime.GOOS == "darwin" {
			args = []string{"-Rc"}
		}
		args = append(args, sourcePath, destinationPath)
		if output, err := proofCheckCommandContext(ctx, "cp", args...).CombinedOutput(); err != nil {
			return fmt.Errorf(
				"copy proof ceremony input %s: %w: %s",
				relativePath,
				err,
				strings.TrimSpace(string(output)),
			)
		}
		if err := validateProofCheckCeremonyInputSnapshot(destinationPath); err != nil {
			return fmt.Errorf("validate copied proof ceremony input %s: %w", relativePath, err)
		}
	}
	return nil
}

func validateProofCheckCeremonyInputSnapshot(root string) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("top-level ceremony input is a symlink")
	}
	if !rootInfo.IsDir() {
		if !rootInfo.Mode().IsRegular() {
			return fmt.Errorf("top-level ceremony input is not a regular file or directory")
		}
		return nil
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve ceremony input root: %w", err)
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if info.IsDir() || info.Mode().IsRegular() {
				return nil
			}
			return fmt.Errorf("%s is not a regular file, directory, or rooted symlink", path)
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if filepath.IsAbs(target) {
			return fmt.Errorf("%s is an absolute symlink", path)
		}
		resolvedTarget, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve symlink %s: %w", path, err)
		}
		relativeTarget, err := filepath.Rel(resolvedRoot, resolvedTarget)
		if err != nil {
			return fmt.Errorf("relativize symlink %s: %w", path, err)
		}
		if filepath.IsAbs(relativeTarget) ||
			relativeTarget == ".." ||
			strings.HasPrefix(relativeTarget, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("%s is a symlink escaping ceremony input root", path)
		}
		return nil
	})
}
