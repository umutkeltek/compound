package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductCampaignReadOnlyDispatchDoesNotInitializeHome(t *testing.T) {
	binary := compoundTestBinary(t)
	homePath := filepath.Join(t.TempDir(), "absent-home")
	cmd := exec.Command(binary, "product", "campaign", "--help")
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "campaign measure") {
		t.Fatalf("help: %v %s", err, out)
	}
	cmd = exec.Command(binary, "product", "campaign", "check", filepath.Join(t.TempDir(), "missing.json"))
	cmd.Env = append(os.Environ(), "COMPOUND_HOME="+homePath)
	if err = cmd.Run(); err == nil {
		t.Fatal("missing campaign must fail")
	}
	if _, err = os.Stat(homePath); !os.IsNotExist(err) {
		t.Fatalf("read-only campaign initialized home: %v", err)
	}
}
