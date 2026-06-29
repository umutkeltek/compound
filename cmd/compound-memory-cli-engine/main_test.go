package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"compound/internal/mem"
)

type fakeMemoryCLI struct {
	output string
	err    error
	calls  int
	args   [][]string
}

func (f *fakeMemoryCLI) run(_ context.Context, _ string, args []string) (string, error) {
	f.calls++
	f.args = append(f.args, append([]string(nil), args...))
	return f.output, f.err
}

func TestMemoryCLIEngineUsesQueryOutputToRankIndexedClaims(t *testing.T) {
	fake := &fakeMemoryCLI{output: "Relevant memory says the API backend language is Go."}
	responses := exchange(t, fake, []mem.Request{
		{SchemaVersion: mem.ProtocolVersion, RequestID: "r1", Op: mem.OpCapabilities},
		{
			SchemaVersion: mem.ProtocolVersion,
			RequestID:     "r2",
			Op:            mem.OpIndex,
			CorpusGen:     7,
			Claims: []mem.ClaimText{
				{ClaimID: "claim_go", Text: "project api backend_language go"},
				{ClaimID: "claim_swift", Text: "project api frontend_language swift"},
			},
		},
		{
			SchemaVersion: mem.ProtocolVersion,
			RequestID:     "r3",
			Op:            mem.OpQuery,
			Text:          "which language should the api backend use",
			Gate:          mem.GateList,
			Allowlist:     []string{"claim_go", "claim_swift"},
			K:             1,
		},
	})
	if got := responses[0].Name; got != "memory-cli" {
		t.Fatalf("capability name = %q", got)
	}
	if responses[1].Indexed != 2 || responses[1].CorpusGen != 7 {
		t.Fatalf("index response = %+v", responses[1])
	}
	if len(responses[2].Hits) != 1 || responses[2].Hits[0].ClaimID != "claim_go" {
		t.Fatalf("query hits = %+v", responses[2].Hits)
	}
	if fake.calls != 1 {
		t.Fatalf("memory CLI calls = %d, want 1", fake.calls)
	}
	joined := strings.Join(fake.args[0], " ")
	if !strings.Contains(joined, "query") || !strings.Contains(joined, "--limit 8") {
		t.Fatalf("memory CLI args should use read-only query template: %v", fake.args[0])
	}
}

func TestMemoryCLIEngineGateListEmptyAllowlistFailsClosed(t *testing.T) {
	fake := &fakeMemoryCLI{output: "Go backend"}
	responses := exchange(t, fake, []mem.Request{
		{
			SchemaVersion: mem.ProtocolVersion,
			RequestID:     "r1",
			Op:            mem.OpIndex,
			CorpusGen:     1,
			Claims:        []mem.ClaimText{{ClaimID: "claim_go", Text: "backend go"}},
		},
		{
			SchemaVersion: mem.ProtocolVersion,
			RequestID:     "r2",
			Op:            mem.OpQuery,
			Text:          "backend",
			Gate:          mem.GateList,
			Allowlist:     nil,
			K:             10,
		},
	})
	if len(responses[1].Hits) != 0 {
		t.Fatalf("empty allowlist must match nothing, got %+v", responses[1].Hits)
	}
	if fake.calls != 0 {
		t.Fatalf("empty allowlist should fail closed before memory CLI call, calls=%d", fake.calls)
	}
}

func TestMemoryCLIEngineReportsUnavailableBackend(t *testing.T) {
	fake := &fakeMemoryCLI{err: errors.New("db unavailable")}
	responses := exchange(t, fake, []mem.Request{
		{
			SchemaVersion: mem.ProtocolVersion,
			RequestID:     "r1",
			Op:            mem.OpIndex,
			CorpusGen:     1,
			Claims:        []mem.ClaimText{{ClaimID: "claim_go", Text: "backend go"}},
		},
		{
			SchemaVersion: mem.ProtocolVersion,
			RequestID:     "r2",
			Op:            mem.OpQuery,
			Text:          "backend",
			Gate:          mem.GateList,
			Allowlist:     []string{"claim_go"},
			K:             10,
		},
	})
	if responses[1].OK {
		t.Fatalf("unavailable backend should not be OK: %+v", responses[1])
	}
	if responses[1].Error == nil || responses[1].Error.Code != mem.CodeEngineUnavailable {
		t.Fatalf("error = %+v, want %s", responses[1].Error, mem.CodeEngineUnavailable)
	}
}

func exchange(t *testing.T, fake *fakeMemoryCLI, requests []mem.Request) []mem.Response {
	t.Helper()
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	for _, req := range requests {
		if err := enc.Encode(req); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := run(&in, &out, fake.run); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&out)
	responses := make([]mem.Response, 0, len(requests))
	for {
		var resp mem.Response
		if err := dec.Decode(&resp); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		responses = append(responses, resp)
	}
	if len(responses) != len(requests) {
		t.Fatalf("responses = %d, want %d; raw=%s", len(responses), len(requests), out.String())
	}
	return responses
}
