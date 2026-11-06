package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestGitIndexLockCLIRejectsMalformedCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"delete"}, {"apply", "--unknown"}, {"apply", "unexpected"}, {"review", "--expires", "not-a-time"}} {
		var out, err bytes.Buffer
		if code := runAuthorityGitIndexLock(t.TempDir(), args, &out, &err); code == 0 {
			t.Fatalf("accepted %v", args)
		}
		if err.Len() == 0 {
			t.Fatalf("missing failure explanation %v", args)
		}
	}
}

func TestGitIndexLockRecoverCLIRoutesToNativeOwner(t *testing.T) {
	var out, diagnostic bytes.Buffer
	args := []string{"recover", "--repo", t.TempDir(), "--work", "work_fixture", "--actor", "codex:test"}
	code := runAuthorityGitIndexLock(t.TempDir(), args, &out, &diagnostic)
	if code != 1 || !strings.Contains(diagnostic.String(), "primary checkout required") {
		t.Fatalf("native recover route missing: code=%d out=%s err=%s", code, out.String(), diagnostic.String())
	}
}
