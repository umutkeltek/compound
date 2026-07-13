// Package retrievalparity owns retrieval-traffic evidence and parity artifact
// schemas. It is a data-layer leaf: production retrieval remains in its existing
// consumers, while this package records and compares their result references.
package retrievalparity

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/home"
)

const (
	JournalSchemaVersion = "compound.retrieval_traffic.v0.1"
	ClassifierVersion    = "consumer-token-count.v0"
	JournalRelPath       = "events/retrieval-traffic.ndjson"

	ConsumerOrient = "orient"
	ConsumerRecall = "recall"
	ConsumerQuery  = "query"
	ConsumerSearch = "search"
)

// Row is one production retrieval observation. Query is intentionally present
// only in this COMPOUND_HOME journal shape; parity reports use QuerySHA256 and
// never embed Row.
type Row struct {
	SchemaVersion     string   `json:"schema_version"`
	ClassifierVersion string   `json:"classifier_version"`
	Consumer          string   `json:"consumer"`
	QueryClass        string   `json:"query_class"`
	QuerySHA256       string   `json:"query_sha256"`
	Query             string   `json:"query"`
	ResultRefs        []string `json:"result_refs"`
	ResultCount       int      `json:"result_count"`
	LatencyMS         float64  `json:"latency_ms"`
	// EngineAvailable is consumer-relative: a successful designed fallback is
	// available even when an optional rented sidecar did not answer.
	EngineAvailable bool   `json:"engine_available"`
	Timestamp       string `json:"timestamp"`
}

// NewRow builds the normalized journal-only representation for one retrieval.
func NewRow(consumer, query string, resultRefs []string, latency time.Duration, engineAvailable bool, now time.Time) Row {
	if latency < 0 {
		latency = 0
	}
	refs := uniqueSorted(resultRefs)
	return Row{
		SchemaVersion:     JournalSchemaVersion,
		ClassifierVersion: ClassifierVersion,
		Consumer:          strings.TrimSpace(consumer),
		QueryClass:        ClassifyQuery(consumer, query),
		QuerySHA256:       HashQuery(query),
		Query:             query,
		ResultRefs:        refs,
		ResultCount:       len(refs),
		LatencyMS:         float64(latency) / float64(time.Millisecond),
		EngineAvailable:   engineAvailable,
		Timestamp:         now.UTC().Format(time.RFC3339Nano),
	}
}

// ClassifyQuery is deliberately content-free: consumer plus a coarse token-count
// bucket. Changing the buckets requires a classifier-version change.
func ClassifyQuery(consumer, query string) string {
	bucket := "0"
	switch n := len(strings.Fields(query)); {
	case n == 1:
		bucket = "1"
	case n >= 2 && n <= 4:
		bucket = "2-4"
	case n >= 5 && n <= 12:
		bucket = "5-12"
	case n >= 13:
		bucket = "13+"
	}
	return strings.TrimSpace(consumer) + "/tokens:" + bucket
}

// HashQuery returns the privacy-safe query reference used outside the journal.
func HashQuery(query string) string {
	sum := sha256.Sum256([]byte(query))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Record is a best-effort tap. A journal failure emits exactly one warning and
// cannot change the retrieval consumer's return value, exit code, or stdout.
func Record(homePath string, row Row) {
	if err := appendRow(homePath, row); err != nil {
		fmt.Fprintf(os.Stderr, "retrieval journal warning: %v\n", err)
	}
}

func appendRow(homePath string, row Row) error {
	if strings.TrimSpace(homePath) == "" {
		return fmt.Errorf("COMPOUND_HOME required")
	}
	if _, err := home.LoadManifest(homePath); err != nil {
		return fmt.Errorf("invalid COMPOUND_HOME: %w", err)
	}
	row = normalizeRow(row)
	if err := validateRow(row); err != nil {
		return err
	}
	path := filepath.Join(homePath, filepath.FromSlash(JournalRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}

func normalizeRow(row Row) Row {
	row.Consumer = strings.TrimSpace(row.Consumer)
	if row.SchemaVersion == "" {
		row.SchemaVersion = JournalSchemaVersion
	}
	if row.ClassifierVersion == "" {
		row.ClassifierVersion = ClassifierVersion
	}
	if row.QueryClass == "" {
		row.QueryClass = ClassifyQuery(row.Consumer, row.Query)
	}
	if row.QuerySHA256 == "" {
		row.QuerySHA256 = HashQuery(row.Query)
	}
	row.ResultRefs = uniqueSorted(row.ResultRefs)
	row.ResultCount = len(row.ResultRefs)
	return row
}

func validateRow(row Row) error {
	if row.SchemaVersion != JournalSchemaVersion {
		return fmt.Errorf("unsupported retrieval journal schema %q", row.SchemaVersion)
	}
	if row.ClassifierVersion != ClassifierVersion {
		return fmt.Errorf("unsupported query classifier %q", row.ClassifierVersion)
	}
	if !validConsumer(row.Consumer) {
		return fmt.Errorf("unsupported retrieval consumer %q", row.Consumer)
	}
	if row.QueryClass != ClassifyQuery(row.Consumer, row.Query) {
		return fmt.Errorf("query_class does not match classifier")
	}
	if row.QuerySHA256 != HashQuery(row.Query) {
		return fmt.Errorf("query_sha256 does not match query")
	}
	if row.LatencyMS < 0 {
		return fmt.Errorf("latency_ms must be non-negative")
	}
	if _, err := time.Parse(time.RFC3339Nano, row.Timestamp); err != nil {
		return fmt.Errorf("timestamp must be RFC3339Nano")
	}
	return nil
}

// LoadJournal reads the strict journal surface used by tests and consumers.
func LoadJournal(homePath string) ([]Row, error) {
	rows, _, err := loadJournal(homePath, false)
	return rows, err
}

func loadJournal(homePath string, tolerateMalformed bool) ([]Row, []string, error) {
	path := filepath.Join(homePath, filepath.FromSlash(JournalRelPath))
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return []Row{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	rows := []Row{}
	gaps := []string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		var row Row
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			if tolerateMalformed {
				gaps = append(gaps, fmt.Sprintf("malformed_journal_row:%d", lineNo))
				continue
			}
			return nil, nil, fmt.Errorf("retrieval journal row %d: %w", lineNo, err)
		}
		if err := validateRow(row); err != nil {
			if tolerateMalformed {
				gaps = append(gaps, fmt.Sprintf("invalid_journal_row:%d", lineNo))
				continue
			}
			return nil, nil, fmt.Errorf("retrieval journal row %d: %w", lineNo, err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return rows, uniqueSorted(gaps), nil
}

func validConsumer(consumer string) bool {
	switch consumer {
	case ConsumerOrient, ConsumerRecall, ConsumerQuery, ConsumerSearch:
		return true
	default:
		return false
	}
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
