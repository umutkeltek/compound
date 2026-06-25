package engine

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NewConfigured returns the configured rented retrieval engine. The public core
// has no private engine default; set COMPOUND_ENGINE_BIN to use a local CLI
// adapter, otherwise Compound runs with the no-op engine.
func NewConfigured() MemoryEngine {
	bin := strings.TrimSpace(os.Getenv("COMPOUND_ENGINE_BIN"))
	if bin == "" {
		return NewNull()
	}
	return NewCLI(bin)
}

// CLI rents a local retrieval CLI behind MemoryEngine. The CLI is expected to
// expose query/recall/put commands whose output matches the small parser below.
type CLI struct {
	bin string
}

func NewCLI(bin string) *CLI {
	return &CLI{bin: bin}
}

func (c *CLI) Capabilities() Capabilities {
	return Capabilities{Name: c.bin, Vector: true, Lexical: true, Recall: true}
}

var hitRe = regexp.MustCompile(`^\[([\d.]+)\]\s+(\S+)\s+--\s+(.*)$`)

func (c *CLI) Query(q string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 12
	}
	out, err := c.run(90*time.Second, "query", q, "--limit", strconv.Itoa(limit), "--detail", "low")
	if err != nil {
		return nil, err
	}
	var hits []Hit
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if m := hitRe.FindStringSubmatch(sc.Text()); m != nil {
			score, _ := strconv.ParseFloat(m[1], 64)
			snip := m[3]
			if len(snip) > 200 {
				snip = snip[:200]
			}
			hits = append(hits, Hit{Score: score, Slug: m[2], Snippet: snip})
		}
	}
	return hits, nil
}

func (c *CLI) Recall(entity string) ([]Fact, error) {
	out, err := c.run(60*time.Second, "recall", entity)
	if err != nil {
		return nil, err
	}
	var facts []Fact
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		facts = append(facts, Fact{Claim: line})
	}
	return facts, nil
}

func (c *CLI) Put(slug, markdown string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, "put", slug)
	cmd.Stdin = strings.NewReader(markdown)
	return cmd.Run()
}

func (c *CLI) run(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}
