package work

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type bunTestIdentity struct {
	File, Classname, Name string
	Suites                []string
}

func parseBunTestIdentity(value string) (bunTestIdentity, error) {
	var fields []json.RawMessage
	d := json.NewDecoder(bytes.NewBufferString(value))
	if err := d.Decode(&fields); err != nil || len(fields) != 4 {
		return bunTestIdentity{}, fmt.Errorf("must be [file,classname,name,[suite...]]")
	}
	var id bunTestIdentity
	for i, target := range []*string{&id.File, &id.Classname, &id.Name} {
		if err := json.Unmarshal(fields[i], target); err != nil {
			return id, fmt.Errorf("field %d must be a string", i)
		}
	}
	if err := json.Unmarshal(fields[3], &id.Suites); err != nil || id.File == "" || id.Name == "" || len(id.Suites) == 0 || len(id.Suites) > 32 {
		return id, fmt.Errorf("requires file, name, and 1..32 suite names")
	}
	for _, suite := range id.Suites {
		if suite == "" {
			return id, fmt.Errorf("suite names must be nonempty")
		}
	}
	canonical, _ := json.Marshal([]any{id.File, id.Classname, id.Name, id.Suites})
	if string(canonical) != value {
		return id, fmt.Errorf("must use canonical compact JSON")
	}
	return id, nil
}

func bunIdentityString(id bunTestIdentity) string {
	body, _ := json.Marshal([]any{id.File, id.Classname, id.Name, id.Suites})
	return string(body)
}

type bunSuiteCount struct {
	name                                             string
	tests, failures, errors, skipped                 int
	wantTests, wantFailures, wantErrors, wantSkipped *int
}

func validateConsumerBunReport(c CanonicalConsumer, body []byte) error {
	d := xml.NewDecoder(bytes.NewReader(body))
	d.Strict = true
	var suites []bunSuiteCount
	seen := map[string]bool{}
	required, _ := consumerUniqueStrings(c.RequiredTests, true)
	allowed, _ := consumerUniqueStrings(c.AllowedSkips, false)
	depth, roots, total, declarations := 0, 0, 0, 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("Bun JUnit XML: %w", err)
		}
		switch node := tok.(type) {
		case xml.ProcInst:
			if node.Target != "xml" || roots != 0 || depth != 0 || declarations > 0 {
				return fmt.Errorf("invalid Bun JUnit XML declaration")
			}
			declarations++
		case xml.Directive, xml.Comment:
			return fmt.Errorf("Bun JUnit directives and comments are not allowed")
		case xml.CharData:
			if strings.TrimSpace(string(node)) != "" {
				return fmt.Errorf("Bun JUnit text outside testcase outcome")
			}
		case xml.StartElement:
			depth++
			if depth > 64 || !bunElementValid(node) || !bunAttrsKnown(node) {
				return fmt.Errorf("Bun JUnit XML exceeds structural limits")
			}
			if depth == 1 {
				roots++
				if roots != 1 || node.Name.Local != "testsuites" {
					return fmt.Errorf("Bun JUnit root must be testsuites")
				}
			} else if node.Name.Local == "testsuites" {
				return fmt.Errorf("nested testsuites is not native Bun JUnit")
			}
			if node.Name.Local != "testsuites" && node.Name.Local != "testsuite" && node.Name.Local != "testcase" {
				return fmt.Errorf("unexpected Bun JUnit element %s", node.Name.Local)
			}
			if node.Name.Local == "testsuite" && (len(suites) == 0 || (depth > 2 && suites[len(suites)-1].name == "")) {
				return fmt.Errorf("Bun testsuite has invalid parent")
			}
			switch node.Name.Local {
			case "testsuites", "testsuite":
				s, err := newBunSuiteCount(node.Attr, node.Name.Local == "testsuite")
				if err != nil {
					return err
				}
				suites = append(suites, s)
			case "testcase":
				if len(suites) < 2 {
					return fmt.Errorf("Bun JUnit testcase must be directly inside testsuite")
				}
				if len(suites)-1 > 32 {
					return fmt.Errorf("Bun testcase suite ancestry exceeds 32")
				}
				id, err := bunIdentityFromTestcase(node.Attr, suites)
				if err != nil {
					return err
				}
				key := bunIdentityString(id)
				if seen[key] {
					return fmt.Errorf("duplicate Bun testcase identity %s", key)
				}
				seen[key] = true
				total++
				for i := range suites {
					suites[i].tests++
				}
				outcome, err := consumeBunTestcase(d)
				if err != nil {
					return err
				}
				depth--
				for i := range suites {
					if outcome == "failure" {
						suites[i].failures++
					}
					if outcome == "error" {
						suites[i].errors++
					}
					if outcome == "skipped" {
						suites[i].skipped++
					}
				}
				if outcome == "failure" || outcome == "error" {
					return fmt.Errorf("Bun testcase failed: %s", key)
				}
				if outcome == "skipped" && !allowed[key] {
					return fmt.Errorf("unexpected skipped Bun testcase: %s", key)
				}
				if required[key] && outcome == "skipped" {
					return fmt.Errorf("required Bun testcase skipped: %s", key)
				}
			}
		case xml.EndElement:
			if node.Name.Local == "testsuite" || node.Name.Local == "testsuites" {
				if len(suites) == 0 {
					return fmt.Errorf("inconsistent Bun JUnit suites")
				}
				s := suites[len(suites)-1]
				suites = suites[:len(suites)-1]
				if !bunCountsMatch(s) || (node.Name.Local == "testsuite" && s.tests == 0) {
					return fmt.Errorf("inconsistent Bun JUnit counts in suite %q", s.name)
				}
			}
			depth--
			if depth < 0 {
				return fmt.Errorf("inconsistent Bun JUnit nesting")
			}
		}
	}
	if roots != 1 || depth != 0 || len(suites) != 0 || total == 0 {
		return fmt.Errorf("Bun JUnit requires one complete nonempty root")
	}
	for id := range required {
		if !seen[id] {
			return fmt.Errorf("required Bun testcase absent: %s", id)
		}
	}
	return nil
}

func newBunSuiteCount(attrs []xml.Attr, requireName bool) (bunSuiteCount, error) {
	s := bunSuiteCount{name: xmlAttr(attrs, "name")}
	if requireName && s.name == "" {
		return s, fmt.Errorf("Bun suite name is required")
	}
	var err error
	s.wantTests, err = xmlRequiredCount(attrs, "tests")
	if err != nil {
		return s, err
	}
	s.wantFailures, err = xmlRequiredCount(attrs, "failures")
	if err != nil {
		return s, err
	}
	s.wantSkipped, err = xmlRequiredCount(attrs, "skipped")
	if err != nil {
		return s, err
	}
	s.wantErrors, err = xmlCount(attrs, "errors")
	return s, err
}

func bunIdentityFromTestcase(attrs []xml.Attr, suites []bunSuiteCount) (bunTestIdentity, error) {
	file, hasFile := xmlAttrOK(attrs, "file")
	class, hasClass := xmlAttrOK(attrs, "classname")
	name, hasName := xmlAttrOK(attrs, "name")
	if !hasFile || !hasClass || !hasName || file == "" || name == "" {
		return bunTestIdentity{}, fmt.Errorf("Bun testcase requires native file, classname, and name attributes")
	}
	id := bunTestIdentity{File: file, Classname: class, Name: name}
	for _, s := range suites[1:] {
		id.Suites = append(id.Suites, s.name)
	}
	return id, nil
}

func consumeBunTestcase(d *xml.Decoder) (string, error) {
	outcome := "passed"
	for {
		tok, err := d.Token()
		if err != nil {
			return "", fmt.Errorf("unfinished Bun testcase: %w", err)
		}
		switch n := tok.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(n)) != "" {
				return "", fmt.Errorf("unexpected Bun testcase text")
			}
		case xml.Comment, xml.Directive, xml.ProcInst:
			return "", fmt.Errorf("unexpected Bun testcase metadata")
		case xml.StartElement:
			if !bunElementValid(n) || !bunAttrsKnown(n) || (n.Name.Local != "failure" && n.Name.Local != "error" && n.Name.Local != "skipped") || outcome != "passed" {
				return "", fmt.Errorf("invalid Bun testcase outcome")
			}
			outcome = n.Name.Local
			if err := consumeBunOutcome(d, n); err != nil {
				return "", err
			}
		case xml.EndElement:
			if n.Name.Local != "testcase" {
				return "", fmt.Errorf("invalid Bun testcase nesting")
			}
			return outcome, nil
		}
	}
}

func consumeBunOutcome(d *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return fmt.Errorf("unfinished Bun testcase outcome: %w", err)
		}
		switch n := tok.(type) {
		case xml.StartElement:
			return fmt.Errorf("nested Bun testcase outcome is not allowed")
		case xml.EndElement:
			if n.Name.Local != start.Name.Local {
				return fmt.Errorf("invalid Bun testcase outcome close")
			}
			return nil
		case xml.Directive, xml.ProcInst:
			return fmt.Errorf("invalid Bun testcase outcome metadata")
		}
	}
}

func bunElementValid(e xml.StartElement) bool {
	if e.Name.Space != "" || len(e.Name.Local) > 256 || len(e.Attr) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, a := range e.Attr {
		if a.Name.Space != "" || a.Name.Local == "" || len(a.Name.Local) > 256 || len(a.Value) > 64<<10 || seen[a.Name.Local] {
			return false
		}
		seen[a.Name.Local] = true
	}
	return true
}

func bunAttrsKnown(e xml.StartElement) bool {
	allowed := map[string]map[string]bool{
		"testsuites": {"name": true, "tests": true, "assertions": true, "failures": true, "errors": true, "skipped": true, "time": true},
		"testsuite":  {"name": true, "file": true, "line": true, "tests": true, "assertions": true, "failures": true, "errors": true, "skipped": true, "time": true, "hostname": true},
		"testcase":   {"name": true, "classname": true, "time": true, "file": true, "line": true, "assertions": true},
		"failure":    {"message": true, "type": true}, "error": {"message": true, "type": true}, "skipped": {"message": true},
	}[e.Name.Local]
	if allowed == nil {
		return false
	}
	for _, attr := range e.Attr {
		if !allowed[attr.Name.Local] {
			return false
		}
	}
	return true
}

func xmlAttrOK(attrs []xml.Attr, name string) (string, bool) {
	for _, a := range attrs {
		if a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}
func xmlAttr(attrs []xml.Attr, name string) string { value, _ := xmlAttrOK(attrs, name); return value }
func xmlRequiredCount(attrs []xml.Attr, name string) (*int, error) {
	value, ok := xmlAttrOK(attrs, name)
	if !ok {
		return nil, fmt.Errorf("Bun JUnit %s count is required", name)
	}
	return parseXMLCount(value, name)
}
func xmlCount(attrs []xml.Attr, name string) (*int, error) {
	value, ok := xmlAttrOK(attrs, name)
	if !ok {
		return nil, nil
	}
	return parseXMLCount(value, name)
}
func parseXMLCount(value, name string) (*int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("invalid Bun JUnit %s count", name)
	}
	return &n, nil
}
func bunCountsMatch(s bunSuiteCount) bool {
	return *s.wantTests == s.tests && *s.wantFailures == s.failures && (s.wantErrors == nil || *s.wantErrors == s.errors) && *s.wantSkipped == s.skipped
}
