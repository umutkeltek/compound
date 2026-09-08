package work

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"reflect"
	"regexp"
	"strings"
)

const CanonicalConsumerContractPath = "compound-proof.json"
const CanonicalConsumerReportLimit = 16 << 20
const CanonicalConsumerEvidenceBudget = 32 << 20

// CanonicalConsumer is a repository-owned invocation, never a caller replacement.
// RequiredEnvironment accepts tool/cache directories only, not arbitrary secret-bearing inputs.
type CanonicalConsumer struct {
	ID                  string   `json:"id"`
	Command             []string `json:"command"`
	Dir                 string   `json:"dir"`
	TimeoutSeconds      int      `json:"timeout_seconds"`
	RequiredTools       []string `json:"required_tools"`
	RequiredEnvironment []string `json:"required_environment,omitempty"`
	ReportKind          string   `json:"report_kind"`
	RequiredTests       []string `json:"required_tests"`
	AllowedSkips        []string `json:"allowed_skips,omitempty"`
}

type canonicalConsumerContract struct {
	SchemaVersion string              `json:"schema_version"`
	Profile       string              `json:"profile"`
	Consumers     []CanonicalConsumer `json:"consumers"`
}

// CanonicalConsumerEvidence is nested inside the existing content-addressed proof report.
// Byte slices preserve exact contract bytes (including whitespace) through JSON custody.
// Report is native Go NDJSON or xcresulttool summary JSON; Tests is the xcresult test tree.
type CanonicalConsumerEvidence struct {
	Contract []byte `json:"contract"`
	Report   []byte `json:"report"`
	Tests    []byte `json:"tests,omitempty"`
}

func (e *CanonicalConsumerEvidence) size() int {
	if e == nil {
		return 0
	}
	return len(e.Contract) + len(e.Report) + len(e.Tests)
}

// RetainCanonicalConsumerEvidence bounds the whole report, not only each native
// view. An over-limit consumer fails and its oversized bytes are not retained;
// earlier bounded evidence and all reserved terminal gate identities survive.
func RetainCanonicalConsumerEvidence(prior []ProofCheckStep, next ProofCheckStep) ProofCheckStep {
	if next.ConsumerEvidence == nil {
		return next
	}
	remaining := CanonicalConsumerEvidenceBudget
	for _, step := range prior {
		remaining -= step.ConsumerEvidence.size()
	}
	if next.ConsumerEvidence.size() <= remaining {
		return next
	}
	reason := fmt.Errorf("aggregate native evidence exceeds %d-byte budget", CanonicalConsumerEvidenceBudget)
	next.ConsumerEvidence = nil
	next.Status, next.ExitCode = "failed", 1
	next.OutputTail += "\n" + reason.Error()
	if len(next.OutputTail) > 4000 {
		next.OutputTail = next.OutputTail[len(next.OutputTail)-4000:]
	}
	next.FailureFacts = append(next.FailureFacts, ProofFailureFacts(next.Name, []byte(reason.Error()), reason)...)
	return next
}

var consumerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var consumerToolPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

func parseCanonicalConsumerContract(body []byte) (canonicalConsumerContract, error) {
	var c canonicalConsumerContract
	if len(body) == 0 || len(body) > 64<<10 {
		return c, fmt.Errorf("consumer contract must contain 1..65536 bytes")
	}
	if err := consumerUniqueJSON(body); err != nil {
		return c, fmt.Errorf("consumer contract: %w", err)
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("consumer contract: %w", err)
	}
	if c.SchemaVersion != "compound.proof_consumers.v1" || (c.Profile != "go_consumers_v1" && c.Profile != "xcode_consumers_v1") {
		return c, fmt.Errorf("unsupported consumer contract schema/profile")
	}
	if len(c.Consumers) == 0 || len(c.Consumers) > 32 {
		return c, fmt.Errorf("consumer contract requires 1..32 consumers")
	}
	ids := map[string]bool{}
	for _, s := range c.Consumers {
		if !consumerIDPattern.MatchString(s.ID) || ids[s.ID] {
			return c, fmt.Errorf("invalid or duplicate consumer id %q", s.ID)
		}
		ids[s.ID] = true
		if s.Dir == "" || path.IsAbs(s.Dir) || path.Clean(s.Dir) != s.Dir || s.Dir == ".." || strings.HasPrefix(s.Dir, "../") || strings.ContainsAny(s.Dir, "\\\x00\r\n") {
			return c, fmt.Errorf("consumer %s requires a clean relative dir", s.ID)
		}
		if len(s.Command) == 0 || !consumerToolPattern.MatchString(s.Command[0]) {
			return c, fmt.Errorf("consumer %s requires a named executable and command", s.ID)
		}
		for _, arg := range s.Command {
			if strings.TrimSpace(arg) == "" || strings.ContainsRune(arg, '\x00') {
				return c, fmt.Errorf("consumer %s has an empty/invalid command argument", s.ID)
			}
		}
		if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 3600 {
			return c, fmt.Errorf("consumer %s timeout_seconds must be 1..3600", s.ID)
		}
		tools, err := consumerUniqueStrings(s.RequiredTools, true)
		if err != nil || !tools[s.Command[0]] {
			return c, fmt.Errorf("consumer %s required_tools must include its command", s.ID)
		}
		for tool := range tools {
			if !consumerToolPattern.MatchString(tool) {
				return c, fmt.Errorf("consumer %s has invalid tool %q", s.ID, tool)
			}
		}
		env, err := consumerUniqueStrings(s.RequiredEnvironment, false)
		if err != nil {
			return c, err
		}
		for name := range env {
			switch name {
			case "PG_BIN_DIR", "GOCACHE", "GOMODCACHE", "DEVELOPER_DIR":
			default:
				return c, fmt.Errorf("consumer %s environment %q is not a tool/cache directory", s.ID, name)
			}
		}
		required, err := consumerUniqueStrings(s.RequiredTests, true)
		if err != nil {
			return c, fmt.Errorf("consumer %s required_tests: %w", s.ID, err)
		}
		skips, err := consumerUniqueStrings(s.AllowedSkips, false)
		if err != nil {
			return c, fmt.Errorf("consumer %s allowed_skips: %w", s.ID, err)
		}
		for id := range skips {
			if required[id] {
				return c, fmt.Errorf("required test %s cannot be an allowed skip", id)
			}
		}
		wantKind := "go_test_json"
		if c.Profile == "xcode_consumers_v1" {
			wantKind = "xcresult"
		}
		if s.ReportKind != wantKind {
			return c, fmt.Errorf("consumer %s report_kind must be %s", s.ID, wantKind)
		}
		for id := range required {
			if wantKind == "go_test_json" && (strings.Count(id, "::") != 1 || strings.HasPrefix(id, "::") || strings.HasSuffix(id, "::")) {
				return c, fmt.Errorf("Go required test must be exact package::test identifier")
			}
		}
	}
	return c, nil
}

func consumerUniqueStrings(values []string, required bool) (map[string]bool, error) {
	if required && len(values) == 0 {
		return nil, fmt.Errorf("nonempty list required")
	}
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") || out[value] {
			return nil, fmt.Errorf("invalid or duplicate value %q", value)
		}
		out[value] = true
	}
	return out, nil
}

func canonicalConsumerStep(c CanonicalConsumer, body []byte) ProofCheckStep {
	sum := sha256.Sum256(body)
	return ProofCheckStep{Name: "consumer:" + c.ID, Command: append([]string(nil), c.Command...), Dir: c.Dir, ProofRef: "consumer:" + c.ID + ":sha256:" + hex.EncodeToString(sum[:])}
}

// CanonicalConsumerForStep checks the complete gate identity against the exact sealed body.
func CanonicalConsumerForStep(body []byte, step ProofCheckStep) (CanonicalConsumer, error) {
	contract, err := parseCanonicalConsumerContract(body)
	if err != nil {
		return CanonicalConsumer{}, err
	}
	for _, consumer := range contract.Consumers {
		want := canonicalConsumerStep(consumer, body)
		if want.Name != step.Name {
			continue
		}
		if want.ProofRef != step.ProofRef || want.Dir != step.Dir || step.SkipIfMissing != "" || !reflect.DeepEqual(want.Command, step.Command) {
			return CanonicalConsumer{}, fmt.Errorf("consumer gate does not match sealed contract")
		}
		return consumer, nil
	}
	return CanonicalConsumer{}, fmt.Errorf("consumer %s absent from sealed contract", step.Name)
}

// ReadCanonicalConsumer reads only the admitted Git object's regular root blob.
func ReadCanonicalConsumer(repo, head string, step ProofCheckStep) (CanonicalConsumer, []byte, error) {
	markers, err := canonicalProofRootMarkers(repo, head, []string{CanonicalConsumerContractPath})
	if err != nil {
		return CanonicalConsumer{}, nil, err
	}
	if !markers[CanonicalConsumerContractPath] {
		return CanonicalConsumer{}, nil, fmt.Errorf("tracked consumer contract required")
	}
	body, err := canonicalProofGitBlob(repo, head, CanonicalConsumerContractPath)
	if err != nil {
		return CanonicalConsumer{}, nil, err
	}
	c, err := CanonicalConsumerForStep(body, step)
	return c, body, err
}

// ValidateCanonicalConsumerEvidence is shared by the native runner and owned report finalizer.
// No temporary path or success sentence participates in certification.
func ValidateCanonicalConsumerEvidence(step ProofCheckStep) error {
	e := step.ConsumerEvidence
	if e == nil {
		return fmt.Errorf("consumer native evidence required")
	}
	c, err := CanonicalConsumerForStep(e.Contract, step)
	if err != nil {
		return err
	}
	if len(e.Report) == 0 || len(e.Report) > CanonicalConsumerReportLimit || len(e.Tests) > CanonicalConsumerReportLimit {
		return fmt.Errorf("consumer report absent or oversized")
	}
	switch c.ReportKind {
	case "go_test_json":
		if len(e.Tests) != 0 {
			return fmt.Errorf("Go report cannot carry an Xcode test tree")
		}
		return validateConsumerGoReport(c, e.Report)
	case "xcresult":
		return validateConsumerXcodeReport(c, e.Report, e.Tests)
	default:
		return fmt.Errorf("unsupported native report")
	}
}

// JSON's default last-key-wins decoder is not suitable for a deciding proof.
func consumerUniqueJSON(body []byte) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("JSON nesting exceeds 64")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || keys[key] {
					return fmt.Errorf("duplicate or invalid JSON key %q", key)
				}
				keys[key] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON value or invalid ending")
	}
	return nil
}
