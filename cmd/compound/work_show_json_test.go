package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"compound/internal/home"
	"compound/internal/work"
)

func TestWorkShowJSONIsOneObjectWithLifecycle(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work: %v", err)
	}
	created, err := store.Create(work.CreateInput{Owner: "compound-core", SuccessPredicate: "show emits one JSON object"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}

	out := captureStdout(t, func() {
		cmdWork([]string{"show", created.ID, "--json"})
	})
	decoder := json.NewDecoder(strings.NewReader(out))
	var shown map[string]json.RawMessage
	if err := decoder.Decode(&shown); err != nil {
		t.Fatalf("decode work show: %v\n%s", err, out)
	}
	if _, ok := shown["lifecycle"]; !ok {
		t.Fatalf("work show JSON omits lifecycle:\n%s", out)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("work show JSON has trailing data: %v\n%s", err, out)
	}
}
