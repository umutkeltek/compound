package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"compound/internal/console"
)

func consolePairOutput(args []string, stdout *os.File) (io.Writer, func(), error) {
	if len(args) == 0 {
		// A character device alone includes /dev/null. Ask the terminal driver
		// without sending pairing material to a subprocess or its arguments.
		probe := exec.Command("stty", "-g")
		probe.Stdin, probe.Stdout, probe.Stderr = stdout, io.Discard, io.Discard
		if err := probe.Run(); err != nil {
			return nil, nil, errors.New("pairing codes require an interactive terminal; use --out NEW_PRIVATE_FILE for explicit private output")
		}
		return stdout, func() {}, nil
	}
	if len(args) != 2 || args[0] != "--out" || strings.TrimSpace(args[1]) == "" {
		return nil, nil, errors.New("usage: compound console pair [--out NEW_PRIVATE_FILE]")
	}
	output, err := os.OpenFile(args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return output, func() { _ = output.Close() }, nil
}

func cmdConsolePair(args []string) {
	if err := runConsolePair(args, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runConsolePair(args []string, stdout *os.File) error {
	homePath := strings.TrimSpace(os.Getenv("COMPOUND_HOME"))
	if homePath == "" {
		return errors.New("console pair requires the same explicit COMPOUND_HOME as the console")
	}
	homePath, err := filepath.Abs(homePath)
	if err != nil {
		return errors.New("invalid console home")
	}
	output, closeOutput, err := consolePairOutput(args, stdout)
	if err != nil {
		return err
	}
	defer closeOutput()
	code, expires, err := console.IssueOperatorPairingCode(homePath)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "Connect this console at %s; code expires %s\n%s\n", console.OperatorOrigin, expires, code); err != nil {
		return errors.New("write pairing output failed")
	}
	return nil
}
