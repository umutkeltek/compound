//go:build darwin

package work

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"compound/internal/authority/gitrepository"
)

func TestHomeFixtureRestrictedRunnerExecutesThenReuses(t *testing.T) {
	f := newHomeProofFixture(t)
	_, file, _, _ := runtime.Caller(0)
	helper, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../scripts/testbattery/home_fixture.py"))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string][]byte{
		"go.mod":                              []byte("module compound\n\ngo 1.23\n"),
		"SCHEMA.md":                           []byte("synthetic schema fixture\n"),
		"scripts/testbattery/home_fixture.py": helper,
		"internal/home/home_test.go": []byte(`package home
import("errors";"fmt";"os";"os/exec";"syscall";"testing")
func TestFixture(t *testing.T){
 for _,fd:=range []int{3,4,5}{
 marker:=make([]byte,64);n,e:=syscall.Pread(fd,marker,0)
 if e!=nil||n<0||string(marker[:n])!="known-liveness-descriptor"{t.Fatalf("proof barrier descriptor %d lost: %v",fd,e)}
 }
 if os.Getenv("FIXTURE_CHILD")=="1"{return}
 executable,e:=os.Executable();if e!=nil{t.Fatal(e)}
 child:=exec.Command(executable,"-test.run=^TestFixture$");child.Env=append(os.Environ(),"FIXTURE_CHILD=1")
 if output,e:=child.CombinedOutput();e!=nil{t.Fatalf("descendant lost proof barrier: %v %s",e,output)}

 if os.Getenv("COMPOUND_HOME")!=""||os.Getenv("COMPOUND_TEST_SECRET")!=""{t.Fatal("ambient environment leaked")}
 if _,e:=os.ReadFile("../../SCHEMA.md");e!=nil{t.Fatal(e)}
 if e:=os.WriteFile("../../SCHEMA.md",[]byte("bad"),0600);!errors.Is(e,syscall.EPERM)&&!errors.Is(e,syscall.EACCES){t.Fatalf("input write: %v",e)}
 if e:=os.WriteFile(t.TempDir()+"/positive",[]byte("ok"),0600);e!=nil{t.Fatal(e)}
 fmt.Println("FRESH_HOME_EXECUTION")
}
`),
	}
	for name, body := range fixtures {
		path := filepath.Join(f.repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		runProofSourceGit(t, f.repo, "add", name)
	}
	runProofSourceGit(t, f.repo, "commit", "-m", "synthetic restricted home fixture")
	f.source, err = gitrepository.CaptureCleanSnapshot(f.repo, f.source.BaseSHA)
	if err != nil {
		t.Fatal(err)
	}
	temporary := newIsolatedHomeFixtureTestRoot(t)
	cacheRoot, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(cacheRoot, "fixture-cache")
	originalCache, originalNow := homeFixtureCacheBase, homeFixtureNow
	homeFixtureCacheBase = func() (string, error) { return cache, nil }
	homeFixtureNow = func() time.Time { return fixedNow.Add(20 * time.Second) }
	defer func() { homeFixtureCacheBase, homeFixtureNow = originalCache, originalNow }()
	ctx := ContextWithHomeFixtureReuse(t.Context(), f.store.home, f.workID, &f.source)
	barrierPath := filepath.Join(t.TempDir(), "liveness")
	if err := os.WriteFile(barrierPath, []byte("known-liveness-descriptor"), 0600); err != nil {
		t.Fatal(err)
	}
	barrier, err := os.Open(barrierPath)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Close()
	command := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.ExtraFiles = []*os.File{barrier, barrier, barrier}
		return cmd
	}
	t.Setenv("COMPOUND_TEST_SECRET", "must-not-enter-fixture")
	first, evidence, err := RunHomeFixtureGate(ctx, f.repo, homeProofTestGate(f.source), os.Environ(), command)
	if err != nil {
		t.Fatalf("fresh restricted phase: %v\n%s", err, first)
	}
	if evidence == nil || evidence.OriginVerificationRef != "" || !strings.Contains(string(first), "FRESH_HOME_EXECUTION") {
		t.Fatalf("fresh evidence=%+v output=%s", evidence, first)
	}
	completed, err := f.complete(t, f.repo, evidence, true, fixedNow.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ctx = ContextWithHomeFixtureReuse(t.Context(), f.store.home, f.workID, &f.source, f.repo)
	second, reused, err := RunHomeFixtureGate(ctx, f.repo, homeProofTestGate(f.source), os.Environ(), command)
	if err != nil {
		t.Fatalf("second phase: %v\n%s", err, second)
	}
	if reused == nil || reused.OriginVerificationRef != completed.VerificationRef || strings.Contains(string(second), "FRESH_HOME_EXECUTION") {
		t.Fatalf("identical fixture was not reused: first=%+v evidence=%+v output=%s", evidence, reused, second)
	}
	if !strings.Contains(string(second), "payload retired") {
		t.Fatalf("target gate did not retire fixture: %s", second)
	}
	if _, err := os.Stat(filepath.Join(evidence.Inputs.StageDirectory, "home.test")); !os.IsNotExist(err) {
		t.Fatalf("target fixture payload remains: %v", err)
	}
	if reused.InputSHA256 != evidence.InputSHA256 {
		t.Fatal("fixture input identity changed across identical preparation")
	}
	// Model the helper's successful full execution with ancestor drift. The
	// adapter must retain this fresh result without certifying it or replaying Go.
	freshExecutions, fallbackExecutions := 0, 0
	driftCommand := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "go" {
			fallbackExecutions++
		}
		cmd := command(ctx, name, args...)
		if name == "/usr/bin/python3" && len(args) > 2 && args[2] == "execute" {
			freshExecutions++
			wrapped := append([]string{"-c", `"$@"; result=$?; if [ "$result" -eq 0 ]; then exit 76; fi; exit "$result"`, "fresh-drift"}, name)
			wrapped = append(wrapped, args...)
			cmd = exec.CommandContext(ctx, "/bin/sh", wrapped...)
			cmd.ExtraFiles = []*os.File{barrier, barrier, barrier}
		}
		return cmd
	}
	third, uncertified, err := RunHomeFixtureGate(ctx, f.repo, homeProofTestGate(f.source), os.Environ(), driftCommand)
	if err != nil || uncertified != nil || freshExecutions != 1 || fallbackExecutions != 0 || !strings.Contains(string(third), "FRESH_HOME_EXECUTION") {
		t.Fatalf("fresh drift replayed or certified: fresh=%d fallback=%d evidence=%+v err=%v output=%s", freshExecutions, fallbackExecutions, uncertified, err, third)
	}

}

func TestHomeFixtureHelperFilesystemSafety(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	helper := filepath.Join(filepath.Dir(file), "../../scripts/testbattery/home_fixture_test.py")
	cmd := exec.CommandContext(t.Context(), "/usr/bin/python3", "-I", helper)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture safety: %v\n%s", err, output)
	}
}

// Keep test churn below a stable sibling of the production cache. Creating
// children directly in UserCacheDir changes an ancestor sealed by the real gate.
func newIsolatedHomeFixtureTestRoot(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(cache, "compound-proof-fixture-tests")
	if err := os.Mkdir(base, 0700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	info, err := os.Lstat(base)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || stat.Uid != uint32(os.Geteuid()) {
		t.Fatal("test fixture parent is not a private owned directory")
	}
	temporary, err := os.MkdirTemp(base, "run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(temporary); err != nil {
			t.Error(err)
		}
	})
	return temporary
}

func TestHomeFixtureTestCachePreservesProductionAncestors(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "Library", "Caches")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Run("initialize", func(t *testing.T) { newIsolatedHomeFixtureTestRoot(t) })
	before, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("next-run", func(t *testing.T) { newIsolatedHomeFixtureTestRoot(t) })
	after, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	previous, current := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) ||
		previous.Ino != current.Ino || previous.Ctimespec != current.Ctimespec {
		t.Fatal("test fixture creation or cleanup changed a sealed production ancestor")
	}
}
