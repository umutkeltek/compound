package main

import (
	"fmt"
	"os"

	"compound/internal/breadth"
)

func main() {
	embedder := os.Getenv("COMPOUND_BREADTH_EMBEDDER")
	if err := breadth.RunProtocol(os.Stdin, os.Stdout, embedder); err != nil {
		fmt.Fprintln(os.Stderr, "compound-breadth-engine:", err)
		os.Exit(1)
	}
}
