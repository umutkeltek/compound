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

// GBrain rents gbrain via its CLI. gbrain is an adopted upstream engine; nothing
// in the owned spine touches it except this adapter. Swapping engines = a new
// MemoryEngine implementation, no spine change.
type GBrain struct{ bin string }

func NewGBrain() *GBrain {
	bin := os.Getenv("COMPOUND_GBRAIN_BIN")
	if bin == "" {
		bin = "gbrain"
	}
	return &GBrain{bin: bin}
}

func (g *GBrain) Capabilities() Capabilities {
	return Capabilities{Name: "gbrain", Vector: true, Lexical: true, Recall: true}
}

var hitRe = regexp.MustCompile(`^\[([\d.]+)\]\s+(\S+)\s+--\s+(.*)$`)

func (g *GBrain) Query(q string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 12
	}
	out, err := g.run(90*time.Second, "query", q, "--limit", strconv.Itoa(limit), "--detail", "low")
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

func (g *GBrain) Recall(entity string) ([]Fact, error) {
	out, err := g.run(60*time.Second, "recall", entity)
	if err != nil {
		return nil, err
	}
	var facts []Fact
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		t := strings.TrimSpace(sc.Text())
		if t == "" || strings.HasPrefix(strings.ToLower(t), "no matching facts") {
			continue
		}
		facts = append(facts, Fact{Claim: t})
	}
	return facts, nil
}

func (g *GBrain) Put(slug, markdown string) error {
	cmd := exec.Command(g.bin, "put", slug)
	cmd.Stdin = strings.NewReader(markdown)
	return cmd.Run()
}

func (g *GBrain) run(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, g.bin, args...)
	cmd.Stdout = &out
	err := cmd.Run()
	return out.Bytes(), err
}
