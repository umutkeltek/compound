package gitrepository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexLockLivenessForeignMountWarnings(t *testing.T) {
	for _, name := range []string{"unrelated", "multiple unrelated", "same device", "target beneath mount", "unknown warning", "malformed device", "trailing warning", "holder with warning", "second target beneath mount", "missing target"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "index")
			if err := os.WriteFile(target, nil, 0600); err != nil {
				t.Fatal(err)
			}
			id, err := indexLockIdentity(target, false)
			if err != nil {
				t.Fatal(err)
			}
			other := id.Device ^ 0x40000000
			warning := func(mount string, dev uint64) string {
				return fmt.Sprintf("lsof: WARNING: can't stat() smbfs file system %s\n      Output information may be incomplete.\n      assuming \"dev=%x\" from mount table\n", mount, dev)
			}
			mount := filepath.Join(t.TempDir(), "unavailable")
			diagnostic := warning(mount, other)
			status, output, wantIdle := 1, "", false
			switch name {
			case "unrelated":
				wantIdle = true
			case "multiple unrelated":
				diagnostic += warning(mount+"-second", other+1)
				wantIdle = true
			case "same device":
				diagnostic = warning(mount, id.Device)
			case "target beneath mount":
				diagnostic = warning(dir, other)
			case "unknown warning":
				diagnostic = "lsof: unrelated-looking warning\n"
			case "malformed device":
				diagnostic = strings.ReplaceAll(diagnostic, fmt.Sprintf("dev=%x", other), "dev=unknown")
			case "trailing warning":
				diagnostic += "lsof: other process inspection incomplete\n"
			case "holder with warning":
				status, output = 0, "printf 'p123\\n'\n"
			case "missing target":
				target += "-missing"
			}
			script := "#!/bin/sh\n/bin/cat >&2 <<'COMPOUND_WARNING'\n" + diagnostic + "COMPOUND_WARNING\n" + output + fmt.Sprintf("exit %d\n", status)
			if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			targets := []string{target}
			if name == "second target beneath mount" {
				if err := os.MkdirAll(mount, 0700); err != nil {
					t.Fatal(err)
				}
				second := filepath.Join(mount, "index.lock")
				if err := os.WriteFile(second, nil, 0600); err != nil {
					t.Fatal(err)
				}
				targets = append(targets, second)
			}
			err = LSOFIndexLockLiveness(context.Background(), targets...)
			if (err == nil) != wantIdle {
				t.Fatalf("idle=%v error=%v", wantIdle, err)
			}
		})
	}
}
