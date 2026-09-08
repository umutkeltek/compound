package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const consumerGoReportFixture = `{"Action":"start","Package":"example.invalid/product/integration"}
{"Action":"run","Package":"example.invalid/product/integration","Test":"TestBuyerPath"}
{"Action":"pass","Package":"example.invalid/product/integration","Test":"TestBuyerPath"}
{"Action":"pass","Package":"example.invalid/product/integration"}
`

func consumerRunnerFixture(t *testing.T, script string, change func(map[string]any)) (string, proofCheckStep) {
	t.Helper()
	contract := consumerContractFixture("go_consumers_v1")
	c := contract["consumers"].([]any)[0].(map[string]any)
	c["command"] = []string{"sh", "consumer.sh"}
	c["required_tools"] = []string{"sh"}
	if change != nil {
		change(c)
	}
	body := consumerContractJSON(t, contract)
	repo := initCanonicalProofRepo(t, map[string]string{"go.mod": "module example.invalid/product\n", "compound-proof.json": body, "consumer.sh": script})
	sum := sha256.Sum256([]byte(body))
	return repo, proofCheckStep{Name: "consumer:buyer-path", Command: []string{"sh", "consumer.sh"}, Dir: ".", ProofRef: "consumer:buyer-path:sha256:" + hex.EncodeToString(sum[:])}
}

func nativeReportScript(report string) string {
	return "set -eu\nif [ -n \"${COMPOUND_PROOF_REPORT:-}\" ]; then\nprintf '%s' '" + strings.ReplaceAll(report, "'", "'\\''") + "' > \"$COMPOUND_PROOF_REPORT\"\nfi\n"
}

func TestCanonicalConsumerRunnerRetainsNativeEvidence(t *testing.T) {
	repo, step := consumerRunnerFixture(t, nativeReportScript(consumerGoReportFixture), nil)
	result := runProofCheckStepContext(t.Context(), repo, step, "HEAD", "HEAD")
	if result.Status != "ok" {
		t.Fatalf("native consumer failed: %+v", result)
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded["consumer_evidence"]) == 0 {
		t.Fatal("successful native evidence was discarded before canonical report custody")
	}
}

func TestCanonicalConsumerRunnerRejectsVacuousReports(t *testing.T) {
	cases := map[string]string{
		"missing":          "exit 0\n",
		"empty":            nativeReportScript(""),
		"malformed":        nativeReportScript("not JSON"),
		"missing required": nativeReportScript(strings.ReplaceAll(consumerGoReportFixture, "TestBuyerPath", "TestSomethingElse")),
		"required skipped": nativeReportScript(strings.Replace(consumerGoReportFixture, `"Action":"pass","Package":"example.invalid/product/integration","Test"`, `"Action":"skip","Package":"example.invalid/product/integration","Test"`, 1)),
		"duplicate":        nativeReportScript(consumerGoReportFixture + consumerGoReportFixture),
		"incomplete":       nativeReportScript(strings.Join(strings.Split(consumerGoReportFixture, "\n")[:3], "\n") + "\n"),
		"reordered":        nativeReportScript(strings.Join([]string{strings.Split(consumerGoReportFixture, "\n")[0], strings.Split(consumerGoReportFixture, "\n")[2], strings.Split(consumerGoReportFixture, "\n")[1], strings.Split(consumerGoReportFixture, "\n")[3]}, "\n") + "\n"),
		"duplicate key":    nativeReportScript(strings.Replace(consumerGoReportFixture, `"Action":"run"`, `"Action":"skip","Action":"run"`, 1)),
		"symlink":          "if [ -n \"${COMPOUND_PROOF_REPORT:-}\" ]; then ln -s consumer.sh \"$COMPOUND_PROOF_REPORT\"; fi\n",
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			repo, step := consumerRunnerFixture(t, script, nil)
			result := runProofCheckStepContext(t.Context(), repo, step, "HEAD", "HEAD")
			if result.Status != "failed" {
				t.Fatalf("%s native report accepted: %+v", name, result)
			}
		})
	}
}

func TestCanonicalConsumerRunnerScrubsAmbientEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "synthetic-secret")
	t.Setenv("GOFLAGS", "-skip=all")
	t.Setenv("COMPOUND_PROOF_REPORT", filepath.Join(t.TempDir(), "caller-report"))
	script := "test -z \"${DATABASE_URL+x}\" && test -z \"${GOFLAGS+x}\" && test \"$GOPROXY\" = off && test \"$GOENV\" = off || exit 41\n" + nativeReportScript(consumerGoReportFixture)
	repo, step := consumerRunnerFixture(t, script, nil)
	result := runProofCheckStepContext(t.Context(), repo, step, "HEAD", "HEAD")
	if result.Status != "ok" {
		t.Fatalf("ambient environment reached required consumer: %+v", result)
	}
	if _, err := os.Stat(os.Getenv("COMPOUND_PROOF_REPORT")); !os.IsNotExist(err) {
		t.Fatalf("caller report path was used: %v", err)
	}
}

func TestCanonicalConsumerRunnerRejectsMissingPrerequisiteBeforeLaunch(t *testing.T) {
	t.Setenv("PG_BIN_DIR", "")
	repo, step := consumerRunnerFixture(t, "touch launched\n", func(c map[string]any) { c["required_environment"] = []string{"PG_BIN_DIR"} })
	result := runProofCheckStepContext(t.Context(), repo, step, "HEAD", "HEAD")
	if result.Status != "failed" {
		t.Fatalf("missing prerequisite admitted: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(repo, "launched")); !os.IsNotExist(err) {
		t.Fatalf("consumer launched before prerequisite validation: %v", err)
	}
}

func TestCanonicalConsumerRunnerRejectsContractDriftBeforeLaunch(t *testing.T) {
	repo, step := consumerRunnerFixture(t, "touch launched\n", nil)
	step.ProofRef = "consumer:buyer-path:sha256:" + strings.Repeat("0", 64)
	result := runProofCheckStepContext(t.Context(), repo, step, "HEAD", "HEAD")
	if result.Status != "failed" {
		t.Fatalf("unbound contract admitted: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(repo, "launched")); !os.IsNotExist(err) {
		t.Fatalf("consumer launched before contract validation: %v", err)
	}
}
