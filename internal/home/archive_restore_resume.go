package home

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ResumeRestoreArchive finishes a streamed restore from an explicit private sibling
// stage. Prior extraction is not trusted: the original archive is verified again,
// every staged body and the exact inventory are checked before publication, and
// the pinned stage is preserved on every failure.
func ResumeRestoreArchive(archivePath, stagingPath, targetHome string, now time.Time) (RestoreReport, error) {
	if strings.TrimSpace(stagingPath) == "" {
		return RestoreReport{}, fmt.Errorf("resume stage required")
	}
	return restoreArchiveWithStage(archivePath, targetHome, stagingPath, now, archiveStreamTarLimits())
}
func openExistingRestoreStaging(parent *os.Root, parentPath, stagingPath, targetName string) (string, *os.Root, os.FileInfo, error) {
	stage, err := filepath.Abs(expandUser(stagingPath))
	if err != nil {
		return "", nil, nil, err
	}
	name := filepath.Base(stage)
	if filepath.Dir(stage) != parentPath || name == targetName || !strings.HasPrefix(name, restoreStagingPrefix) || name == restoreStagingPrefix {
		return "", nil, nil, fmt.Errorf("resume stage must name a private restore staging sibling of the new target")
	}
	before, err := parent.Lstat(name)
	if err != nil {
		return "", nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() || before.Mode().Perm() != 0700 || !ownedByCurrent(before) {
		return "", nil, nil, fmt.Errorf("resume stage must be a current-user-owned non-symlink directory with mode 0700")
	}
	root, err := restoreOpenStaging(parent, name)
	if err != nil {
		return "", nil, nil, err
	}
	hookErr := restoreBeforeStagingValidation(stage)
	if err := errors.Join(hookErr, verifyRestoreStaging(parent, root, name, before, stage)); err != nil {
		return "", nil, nil, errors.Join(err, root.Close())
	}
	if err := rejectLiveRestoreStage(stage, before); err != nil {
		return "", nil, nil, errors.Join(err, root.Close())
	}
	return name, root, before, nil
}

// rejectLiveRestoreStage refuses the current runtime home and all its descendants,
// including paths that spell that home through a filesystem alias.
func rejectLiveRestoreStage(stage string, stageInfo os.FileInfo) error {
	live, err := Resolve("")
	if err != nil {
		return err
	}
	liveInfo, err := os.Stat(live)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if os.SameFile(stageInfo, liveInfo) {
		return fmt.Errorf("resume stage must not be the live Compound home")
	}
	liveReal, err := filepath.EvalSymlinks(live)
	if err != nil {
		return err
	}
	stageReal, err := filepath.EvalSymlinks(stage)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(liveReal, stageReal)
	if err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return fmt.Errorf("resume stage must not be inside the live Compound home")
	}
	return nil
}
