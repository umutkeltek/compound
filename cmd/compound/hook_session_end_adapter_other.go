//go:build !unix

package main

import "os/exec"

func setSessionEndAdapterProcessGroup(_ *exec.Cmd) {}

func killSessionEndAdapterProcessGroup(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
