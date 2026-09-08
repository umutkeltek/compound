package work

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
)

func validateConsumerGoReport(c CanonicalConsumer, body []byte) error {
	if !bytes.HasSuffix(body, []byte("\n")) {
		return fmt.Errorf("Go report has an incomplete final record")
	}
	type packageState struct {
		done  bool
		tests map[string]string
	}
	packages := map[string]*packageState{}
	observed := map[string]string{}
	allowed, _ := consumerUniqueStrings(c.AllowedSkips, false)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), CanonicalConsumerReportLimit)
	for scanner.Scan() {
		line := scanner.Bytes()
		if err := consumerUniqueJSON(line); err != nil {
			return fmt.Errorf("Go event: %w", err)
		}
		var event struct{ Action, Package, Test string }
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("Go event: %w", err)
		}
		if event.Package == "" {
			return fmt.Errorf("Go event requires package identity")
		}
		p := packages[event.Package]
		if event.Test == "" {
			if event.Action == "start" {
				if p != nil {
					return fmt.Errorf("duplicate Go package start")
				}
				packages[event.Package] = &packageState{tests: map[string]string{}}
				continue
			}
			if p == nil || p.done {
				return fmt.Errorf("out-of-order Go package event")
			}
			switch event.Action {
			case "output":
			case "pass":
				for _, state := range p.tests {
					if state != "pass" && state != "skip" {
						return fmt.Errorf("Go package passed before tests finished")
					}
				}
				p.done = true
			default:
				return fmt.Errorf("Go package did not pass: %s", event.Action)
			}
			continue
		}
		if p == nil || p.done {
			return fmt.Errorf("Go test outside running package")
		}
		state := p.tests[event.Test]
		id := event.Package + "::" + event.Test
		switch event.Action {
		case "run":
			if state != "" {
				return fmt.Errorf("duplicate Go test %s", id)
			}
			p.tests[event.Test] = "run"
		case "pause":
			if state != "run" {
				return fmt.Errorf("out-of-order Go pause")
			}
			p.tests[event.Test] = "pause"
		case "cont":
			if state != "pause" {
				return fmt.Errorf("out-of-order Go continue")
			}
			p.tests[event.Test] = "run"
		case "output":
			if state != "run" && state != "pause" {
				return fmt.Errorf("Go output outside running test")
			}
		case "pass", "skip":
			if state != "run" {
				return fmt.Errorf("out-of-order Go test result for %s", id)
			}
			if event.Action == "skip" && !allowed[id] {
				return fmt.Errorf("unexpected skipped Go test %s", id)
			}
			p.tests[event.Test] = event.Action
			observed[id] = event.Action
		default:
			return fmt.Errorf("Go test did not pass: %s %s", id, event.Action)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(packages) == 0 || len(observed) == 0 {
		return fmt.Errorf("vacuous Go report")
	}
	for _, p := range packages {
		if !p.done {
			return fmt.Errorf("Go report missing package pass")
		}
	}
	for _, id := range c.RequiredTests {
		if observed[id] != "pass" {
			return fmt.Errorf("required Go test %s did not pass", id)
		}
	}
	return nil
}

type consumerXcodeNode struct {
	NodeType       string              `json:"nodeType"`
	NodeIdentifier string              `json:"nodeIdentifier"`
	Result         string              `json:"result"`
	Children       []consumerXcodeNode `json:"children"`
	TestNodes      []consumerXcodeNode `json:"testNodes"`
}

func validateConsumerXcodeReport(c CanonicalConsumer, summaryBody, testsBody []byte) error {
	if len(testsBody) == 0 {
		return fmt.Errorf("Xcode test tree required; summary alone is not proof")
	}
	for _, body := range [][]byte{summaryBody, testsBody} {
		if err := consumerUniqueJSON(body); err != nil {
			return fmt.Errorf("Xcode native report: %w", err)
		}
	}
	var summary struct {
		Result   string            `json:"result"`
		Total    *int              `json:"totalTestCount"`
		Passed   *int              `json:"passedTests"`
		Failed   *int              `json:"failedTests"`
		Skipped  *int              `json:"skippedTests"`
		Expected *int              `json:"expectedFailures"`
		Failures []json.RawMessage `json:"testFailures"`
	}
	if err := json.Unmarshal(summaryBody, &summary); err != nil {
		return err
	}
	if summary.Result != "Passed" || summary.Total == nil || summary.Passed == nil || summary.Failed == nil || summary.Skipped == nil || summary.Expected == nil || summary.Failures == nil {
		return fmt.Errorf("Xcode summary lacks complete passing counts")
	}
	if *summary.Total < 1 || *summary.Passed < 1 || *summary.Skipped < 0 || *summary.Failed != 0 || *summary.Expected != 0 || len(summary.Failures) != 0 || *summary.Passed+*summary.Skipped != *summary.Total {
		return fmt.Errorf("Xcode summary is failed, vacuous, or inconsistent")
	}
	var tree struct {
		TestNodes []consumerXcodeNode `json:"testNodes"`
	}
	if err := json.Unmarshal(testsBody, &tree); err != nil {
		return err
	}
	observed := map[string]string{}
	allowed, _ := consumerUniqueStrings(c.AllowedSkips, false)
	passed, skipped := 0, 0
	var visit func(consumerXcodeNode) error
	visit = func(n consumerXcodeNode) error {
		if n.NodeType == "Test Case" {
			if n.NodeIdentifier == "" || observed[n.NodeIdentifier] != "" || len(n.TestNodes) != 0 {
				return fmt.Errorf("invalid or duplicate Xcode test leaf")
			}
			// Native skipped cases carry a flat Failure Message child (for
			// example a physical-device-only reason). It is diagnostic, not a
			// second test. Do not let it conceal nested cases or result fields.
			for _, diagnostic := range n.Children {
				if n.Result != "Skipped" || diagnostic.NodeType != "Failure Message" ||
					diagnostic.NodeIdentifier != "" || diagnostic.Result != "" ||
					len(diagnostic.Children)+len(diagnostic.TestNodes) != 0 {
					return fmt.Errorf("unsupported or nested Xcode test diagnostic")
				}
			}
			switch n.Result {
			case "Passed":
				passed++
			case "Skipped":
				if !allowed[n.NodeIdentifier] {
					return fmt.Errorf("unexpected skipped Xcode test %s", n.NodeIdentifier)
				}
				skipped++
			default:
				return fmt.Errorf("Xcode test %s did not pass: %s", n.NodeIdentifier, n.Result)
			}
			observed[n.NodeIdentifier] = n.Result
			return nil
		}
		if n.Result == "Failed" {
			return fmt.Errorf("failed Xcode test group")
		}
		for _, nodes := range [][]consumerXcodeNode{n.Children, n.TestNodes} {
			for _, child := range nodes {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, node := range tree.TestNodes {
		if err := visit(node); err != nil {
			return err
		}
	}
	if passed != *summary.Passed || skipped != *summary.Skipped || len(observed) != *summary.Total {
		return fmt.Errorf("Xcode summary and test tree disagree")
	}
	for _, id := range c.RequiredTests {
		if observed[id] != "Passed" {
			return fmt.Errorf("required Xcode test %s did not pass", id)
		}
	}
	return nil
}
