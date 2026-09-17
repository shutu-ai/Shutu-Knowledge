//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

func configureOwnedProcess(_ *exec.Cmd) error { return nil }

func terminateOwnedProcessTree(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	pid := strconv.Itoa(command.Process.Pid)
	treeErr := exec.Command("taskkill.exe", "/PID", pid, "/T", "/F").Run()
	if treeErr == nil {
		return nil
	}
	if command.ProcessState != nil {
		return nil
	}
	if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errors.Join(treeErr, err)
	}
	return treeErr
}
