package release

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// macOS ties a privacy grant given to an ad-hoc signed program to that one
// build, so the installer can sign what it publishes with one fixed identity.
// These tests run the real installer. The signing ones use a throwaway
// self-signed identity in a keychain inside the test's temp folder, never a
// certificate of the person running the tests.

const fixtureMachOSource = `package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var stamp = "unset"

func main() {
	if strings.Contains(filepath.Base(os.Args[0]), "breadth") {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		m := regexp.MustCompile(` + "`" + `"request_id":"([^"]*)"` + "`" + `).FindStringSubmatch(line)
		if m == nil {
			os.Exit(73)
		}
		fmt.Printf("{\"ok\":true,\"schema_version\":1,\"request_id\":\"%s\",\"name\":\"compound-breadth\",\"breadth\":true,\"ready\":false}\n", m[1])
		return
	}
	if repo := os.Getenv("FIXTURE_REPO"); repo != "" {
		if f, err := os.OpenFile(filepath.Join(repo, "cli-calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
			f.Close()
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("fixture", stamp)
	}
}
`

// buildFixtureMachO builds a small real executable that answers the installer
// the way the synthetic CLI and breadth scripts do. A different stamp gives a
// different build, as a rebuild of the real program does.
func buildFixtureMachO(t *testing.T, stamp string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixturecli\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(fixtureMachOSource), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fixture-cli")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-ldflags", "-X main.stamp="+stamp, "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off", "CGO_ENABLED=0")
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture executable: %v\n%s", err, body)
	}
	return out
}

type throwawayIdentity struct {
	sha1, keychain, passwordFile string
}

func runTool(t *testing.T, stdin []byte, name string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// newThrowawayIdentity makes a self-signed code-signing identity in its own
// keychain under the test's temp folder. The keychain is unlocked by a
// password file and its key is open to codesign, so signing raises no prompt.
func newThrowawayIdentity(t *testing.T) throwawayIdentity {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("code signing needs macOS codesign and security; this host has neither")
	}
	openssl := "/usr/bin/openssl" // its PKCS#12 defaults are the ones security import reads
	for _, tool := range []string{"codesign", "security", openssl} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("code signing test needs %s: %v", tool, err)
		}
	}
	dir := t.TempDir()
	config := filepath.Join(dir, "cert.cnf")
	if err := os.WriteFile(config, []byte("[req]\ndistinguished_name = dn\nx509_extensions = ext\nprompt = no\n[dn]\nCN = compound installer test signing\n[ext]\nbasicConstraints = critical,CA:false\nkeyUsage = critical,digitalSignature\nextendedKeyUsage = critical,codeSigning\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, cert, p12 := filepath.Join(dir, "key.pem"), filepath.Join(dir, "cert.pem"), filepath.Join(dir, "id.p12")
	runTool(t, nil, openssl, "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", key, "-out", cert, "-days", "2", "-config", config)
	password := strings.TrimSpace(string(runTool(t, nil, openssl, "rand", "-hex", "24")))
	id := throwawayIdentity{keychain: filepath.Join(dir, "signing.keychain-db"), passwordFile: filepath.Join(dir, "keychain.pw")}
	if err := os.WriteFile(id.passwordFile, []byte(password), 0o600); err != nil {
		t.Fatal(err)
	}
	runTool(t, nil, openssl, "pkcs12", "-export", "-out", p12, "-inkey", key, "-in", cert, "-passout", "file:"+id.passwordFile)
	fingerprint := string(runTool(t, nil, openssl, "x509", "-in", cert, "-noout", "-fingerprint", "-sha1"))
	id.sha1 = strings.ReplaceAll(strings.TrimSpace(fingerprint[strings.Index(fingerprint, "=")+1:]), ":", "")

	searchList := func() string { return string(runTool(t, nil, "security", "list-keychains", "-d", "user")) }
	before := searchList()
	runTool(t, nil, "security", "create-keychain", "-p", password, id.keychain)
	t.Cleanup(func() { _ = exec.Command("security", "delete-keychain", id.keychain).Run() })
	if after := searchList(); after != before {
		// Some macOS versions add a new keychain to the user's search list; put
		// the exact earlier list back so the test leaves the machine as it was.
		var paths []string
		for _, quoted := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(before, -1) {
			paths = append(paths, quoted[1])
		}
		runTool(t, nil, "security", append([]string{"list-keychains", "-d", "user", "-s"}, paths...)...)
		t.Logf("create-keychain changed the user keychain search list; restored it")
	}
	runTool(t, nil, "security", "set-keychain-settings", id.keychain)
	runTool(t, nil, "security", "import", p12, "-k", id.keychain, "-P", password, "-T", "/usr/bin/codesign")
	runTool(t, nil, "security", "set-key-partition-list", "-S", "apple-tool:,apple:,codesign:", "-s", "-k", password, id.keychain)
	// Locked again, so the installer has to unlock it from the password file.
	runTool(t, nil, "security", "lock-keychain", id.keychain)
	return id
}

// designatedRequirement is what macOS checks a privacy grant against.
func designatedRequirement(t *testing.T, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "codesign", "-d", "-r-", path).Output()
	if err != nil {
		t.Fatalf("codesign -d -r- %s: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

func cdhash(t *testing.T, path string) string {
	t.Helper()
	out := string(runTool(t, nil, "codesign", "-dvvv", path))
	m := regexp.MustCompile(`(?m)^CDHash=(\S+)$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no CDHash for %s:\n%s", path, out)
	}
	return m[1]
}

func writePrivate(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func leftoverStages(t *testing.T, f sourceInstallerFixture) []string {
	t.Helper()
	stages, err := filepath.Glob(filepath.Join(f.bin, ".compound-source-stage.*"))
	if err != nil {
		t.Fatal(err)
	}
	return stages
}

// A signed install publishes programs whose designated requirement names the
// identity, and a rebuild keeps that same requirement: the property that
// keeps a privacy grant. Read from the settings file under the Compound home
// and from the environment.
func TestSourceInstallerSignsPublishedProgramsWithFixedIdentity(t *testing.T) {
	id := newThrowawayIdentity(t)
	first, second := buildFixtureMachO(t, "first"), buildFixtureMachO(t, "second")
	for _, source := range []string{"settings-file", "environment"} {
		t.Run(source, func(t *testing.T) {
			f := sourceInstallFixture(t)
			var env []string
			if source == "settings-file" {
				writePrivate(t, filepath.Join(f.home, "config", "install-signing.conf"), "# private signing settings\nCOMPOUND_INSTALL_SIGN_IDENTITY="+id.sha1+"\nCOMPOUND_INSTALL_SIGN_KEYCHAIN="+id.keychain+"\nCOMPOUND_INSTALL_SIGN_KEYCHAIN_PASSWORD_FILE="+id.passwordFile+"\n")
			} else {
				env = []string{"COMPOUND_INSTALL_SIGN_IDENTITY=" + id.sha1, "COMPOUND_INSTALL_SIGN_KEYCHAIN=" + id.keychain, "COMPOUND_INSTALL_SIGN_KEYCHAIN_PASSWORD_FILE=" + id.passwordFile}
			}
			requirements := map[string]string{}
			hashes := map[string]string{}
			for round, build := range []string{first, second} {
				if round == 1 {
					// Locked again between installs, as after a restart.
					runTool(t, nil, "security", "lock-keychain", id.keychain)
				}
				out, err := f.runEnv("", append(env, "FIXTURE_MACHO="+build)...)
				if err != nil {
					t.Fatalf("signed install %d failed: %v\n%s", round+1, err, out)
				}
				if !strings.Contains(string(out), "designated requirement: identity-based") {
					t.Fatalf("summary does not report the requirement kind:\n%s", out)
				}
				for name, identifier := range map[string]string{"compound": "compound", "compound-breadth-engine": "compound-breadth-engine"} {
					path := filepath.Join(f.bin, name)
					req := designatedRequirement(t, path)
					// The requirement language quotes an identifier only when it needs to.
					named := strings.Contains(req, `identifier "`+identifier+`" `) || strings.Contains(req, "identifier "+identifier+" ")
					if strings.Contains(req, "cdhash") || !named || !strings.Contains(strings.ToLower(req), strings.ToLower(`H"`+id.sha1+`"`)) {
						t.Fatalf("%s is not signed with the fixed identity: %q", name, req)
					}
					runTool(t, nil, "codesign", "--verify", "--strict", path)
					if prior, ok := requirements[name]; ok && prior != req {
						t.Fatalf("%s requirement changed across a rebuild: %q -> %q", name, prior, req)
					}
					requirements[name] = req
					hash := cdhash(t, path)
					if prior, ok := hashes[name]; ok && prior == hash {
						t.Fatalf("%s: second install published the same build (%s); the rebuild proof proves nothing", name, hash)
					}
					hashes[name] = hash
				}
				if got := strings.TrimSpace(string(runTool(t, nil, filepath.Join(f.bin, "compound"), "version"))); got != "fixture "+[]string{"first", "second"}[round] {
					t.Fatalf("signed program does not run as built: %q", got)
				}
			}
			if stages := leftoverStages(t, f); len(stages) != 0 {
				t.Fatalf("signed install left stages: %v", stages)
			}
		})
	}
}

// Signing that is asked for and fails must stop the install before any
// activation, leave earlier files as they were, and leave no stage behind, so
// the next install starts clean.
func TestSourceInstallerSigningFailureActivatesNothing(t *testing.T) {
	for _, tc := range []struct {
		name, codesign, security, want string
		extra                          []string
	}{
		{name: "codesign_fails", codesign: "exit 1", want: "codesign of compound failed"},
		{name: "codesign_hangs", codesign: "exec sleep 30", want: "codesign of compound did not finish within 2s", extra: []string{"COMPOUND_INSTALL_STEP_TIMEOUT=2"}},
		{name: "requirement_stays_cdhash", codesign: `case " $* " in *' -d '*) echo 'designated => cdhash H"0000000000000000000000000000000000000000"' ;; esac; exit 0`, want: "cdhash-based"},
		{name: "unlock_fails", codesign: "exit 0", security: "echo 'wrong passphrase' >&2; exit 51", want: "unlocking the signing keychain failed", extra: []string{"WITH_KEYCHAIN=1"}},
	} {
		for _, prior := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/fresh", true: "/upgrade"}[prior], func(t *testing.T) {
				f := sourceInstallFixture(t)
				before := map[string][]byte{}
				if prior {
					for _, path := range f.targets {
						if filepath.Base(path) == "os" {
							continue // a fresh alias is enough here; the alias rules have their own tests
						}
						body := []byte("previous " + filepath.Base(path) + "\n")
						if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(path, body, 0o711); err != nil {
							t.Fatal(err)
						}
						before[path] = body
					}
				}
				security := tc.security
				if security == "" {
					security = "exit 0"
				}
				for name, body := range map[string]string{"codesign": tc.codesign, "security": security} {
					if err := os.WriteFile(filepath.Join(f.fakeBin, name), []byte("#!/bin/sh\nprintf '%s %s\\n' \"$(basename \"$0\")\" \"$*\" >>\"$FIXTURE_REPO/sign-calls\"\n"+body+"\n"), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				env := []string{"COMPOUND_INSTALL_SIGN_IDENTITY=0123456789abcdef0123456789abcdef01234567"}
				for _, e := range tc.extra {
					if e == "WITH_KEYCHAIN=1" {
						keychain, password := filepath.Join(f.repo, "fixture.keychain-db"), filepath.Join(f.repo, "fixture.pw")
						writePrivate(t, keychain, "not a real keychain\n")
						writePrivate(t, password, "fixture password\n")
						env = append(env, "COMPOUND_INSTALL_SIGN_KEYCHAIN="+keychain, "COMPOUND_INSTALL_SIGN_KEYCHAIN_PASSWORD_FILE="+password)
						continue
					}
					env = append(env, e)
				}
				started := time.Now()
				out, err := f.runEnv("", env...)
				if err == nil {
					t.Fatalf("install succeeded although signing failed:\n%s", out)
				}
				if elapsed := time.Since(started); elapsed > 25*time.Second {
					t.Fatalf("a hung signing step held the install for %s", elapsed)
				}
				want := tc.want
				if runtime.GOOS != "darwin" {
					want = "needs macOS codesign"
				}
				if !strings.Contains(string(out), want) || !strings.Contains(string(out), "nothing was published") && runtime.GOOS == "darwin" {
					t.Fatalf("missing clear refusal %q:\n%s", want, out)
				}
				if strings.Contains(string(out), "activation") {
					t.Fatalf("failed signing reached activation:\n%s", out)
				}
				if calls, _ := os.ReadFile(filepath.Join(f.repo, "cli-calls")); strings.Contains(string(calls), "home init") {
					t.Fatalf("installer activated after failed signing:\n%s", calls)
				}
				for _, path := range f.targets {
					body, readErr := os.ReadFile(path)
					if _, had := before[path]; !had {
						if _, err := os.Lstat(path); !os.IsNotExist(err) {
							t.Errorf("failed signing published %s: %v", path, err)
						}
						continue
					}
					if readErr != nil || !bytes.Equal(body, before[path]) {
						t.Errorf("failed signing changed %s: %v", path, readErr)
					}
				}
				if stages := leftoverStages(t, f); len(stages) != 0 {
					t.Fatalf("failed signing left a stage that blocks the next install: %v", stages)
				}
				if out, err := f.run(""); err != nil {
					t.Fatalf("next install (signing unset) did not start clean: %v\n%s", err, out)
				}
			})
		}
	}
}

// With signing unset (no environment, no settings file, or the ad-hoc
// identity "-") the installer runs no signing tool, publishes the built bytes
// unchanged, and prints no signature line: the install it did before.
func TestSourceInstallerUnsetSigningKeepsTheAdHocInstall(t *testing.T) {
	for _, mode := range []string{"unset", "environment-adhoc", "settings-file-adhoc"} {
		t.Run(mode, func(t *testing.T) {
			f := sourceInstallFixture(t)
			for _, name := range []string{"codesign", "security"} {
				if err := os.WriteFile(filepath.Join(f.fakeBin, name), []byte("#!/bin/sh\nprintf '%s %s\\n' \"$(basename \"$0\")\" \"$*\" >>\"$FIXTURE_REPO/sign-calls\"\nexit 0\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var env []string
			switch mode {
			case "environment-adhoc":
				env = []string{"COMPOUND_INSTALL_SIGN_IDENTITY=-"}
			case "settings-file-adhoc":
				writePrivate(t, filepath.Join(f.home, "config", "install-signing.conf"), "COMPOUND_INSTALL_SIGN_IDENTITY=-\n")
			}
			out, err := f.runEnv("", env...)
			if err != nil {
				t.Fatalf("install: %v\n%s", err, out)
			}
			if calls, err := os.ReadFile(filepath.Join(f.repo, "sign-calls")); !os.IsNotExist(err) {
				t.Fatalf("unset signing ran a signing tool: %v\n%s", err, calls)
			}
			if strings.Contains(string(out), "signature") {
				t.Fatalf("unset signing changed the summary:\n%s", out)
			}
			for name, built := range map[string]string{"compound": "synthetic-cli", "compound-breadth-engine": "synthetic-breadth"} {
				want, err := os.ReadFile(filepath.Join(f.repo, built))
				if err != nil {
					t.Fatal(err)
				}
				if got, err := os.ReadFile(filepath.Join(f.bin, name)); err != nil || !bytes.Equal(got, want) {
					t.Fatalf("%s is not the built bytes: %v", name, err)
				}
			}
		})
	}
}

// The verifier is built into the stage and run from there (never `go run`, see
// the go shim), under the step time limit; a verifier that hangs fails the
// install quickly, before activation, and leaves no stage behind.
func TestSourceInstallerVerifierRunsFromStageUnderTimeLimit(t *testing.T) {
	f := sourceInstallFixture(t)
	out, err := f.run("")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	calls, err := os.ReadFile(filepath.Join(f.repo, "cli-calls"))
	if err != nil {
		t.Fatal(err)
	}
	bin, err := filepath.EvalSymlinks(f.bin) // the installer resolves its folder with pwd -P
	if err != nil {
		t.Fatal(err)
	}
	ranFrom := regexp.MustCompile(`(?m)^verify (\S+) --binary (\S+) --build-sha`).FindStringSubmatch(string(calls))
	if ranFrom == nil || filepath.Base(filepath.Dir(ranFrom[1])) != "tools" || !strings.HasPrefix(filepath.Base(filepath.Dir(filepath.Dir(ranFrom[1]))), ".compound-source-stage.") || filepath.Dir(filepath.Dir(filepath.Dir(ranFrom[1]))) != bin {
		t.Fatalf("verifier did not run from the stage:\n%s", calls)
	}
	if !strings.Contains(string(out), "install verification ok") {
		t.Fatalf("verifier result missing:\n%s", out)
	}

	f = sourceInstallFixture(t)
	started := time.Now()
	out, err = f.runEnv("verify_hang", "COMPOUND_INSTALL_STEP_TIMEOUT=2")
	if err == nil || !strings.Contains(string(out), "install verification did not finish within 2s") {
		t.Fatalf("hung verifier was not stopped: %v\n%s", err, out)
	}
	if elapsed := time.Since(started); elapsed > 25*time.Second {
		t.Fatalf("hung verifier held the install for %s", elapsed)
	}
	if strings.Contains(string(out), "activation") {
		t.Fatalf("hung verifier reached activation:\n%s", out)
	}
	if stages := leftoverStages(t, f); len(stages) != 0 {
		t.Fatalf("hung verifier left a stage: %v", stages)
	}
	if out, err := f.run(""); err != nil {
		t.Fatalf("next install did not start clean: %v\n%s", err, out)
	}
}

// An installer killed before activation (here while building the verifier)
// leaves a build-only stage. That stage holds nothing earlier, so the next
// install removes it and proceeds; a stage killed later still refuses (see
// TestSourceInstallerPreservesInterruptedPreimage).
func TestSourceInstallerClearsInterruptedBuildOnlyStage(t *testing.T) {
	f := sourceInstallFixture(t)
	if err := os.WriteFile(filepath.Join(f.bin, "compound"), []byte("previous binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := f.run("kill_build")
	if err == nil {
		t.Fatalf("killed installer succeeded:\n%s", out)
	}
	stages := leftoverStages(t, f)
	if len(stages) != 1 {
		t.Fatalf("killed build should leave exactly one stage, got %v", stages)
	}
	if phase, err := os.ReadFile(filepath.Join(stages[0], "phase")); err != nil || string(phase) != "build\n" {
		t.Fatalf("stage phase = %q, %v", phase, err)
	}
	if body, err := os.ReadFile(filepath.Join(f.bin, "compound")); err != nil || string(body) != "previous binary\n" {
		t.Fatalf("killed build changed the installed binary: %v %q", err, body)
	}
	out, err = f.run("")
	if err != nil || !strings.Contains(string(out), "removed an interrupted build-only stage") {
		t.Fatalf("build-only stage still blocks the install: %v\n%s", err, out)
	}
	if stages := leftoverStages(t, f); len(stages) != 0 {
		t.Fatalf("stages left after a clean install: %v", stages)
	}
}
