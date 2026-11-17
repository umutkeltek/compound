package main

import (
	"compound/internal/home"
	"fmt"
	"time"
)

func restoreHomeArchive(archivePath, targetPath string, args []string, now time.Time) (home.RestoreReport, error) {
	if hasFlag(args, "--resume-stage") {
		return home.ResumeRestoreArchive(archivePath, valueFlag(args, "--resume-stage"), targetPath, now)
	}
	return home.RestoreArchive(archivePath, targetPath, now)
}

func printHomeRestoreResumeEvidence(r home.RestoreReport) {
	if r.RevalidatedStage != "" {
		fmt.Printf("- revalidated_stage: %s\n- archive_sha256: %s\n- stage_identity: %s\n- stage_owner_uid: %d\n", r.RevalidatedStage, r.ArchiveSHA256, r.StageIdentity, r.StageOwnerUID)
	}
}
