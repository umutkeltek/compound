package main

import (
	"strings"
	"testing"
	"time"
)

func TestHomeRestoreResumeRequiresExplicitStage(t *testing.T) {
	_, err := restoreHomeArchive("unused-archive", "unused-target", []string{"--resume-stage"}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "resume stage required") {
		t.Fatalf("missing stage: %v", err)
	}
}
