//go:build windows

package runtime

import (
	"os/exec"
	"strconv"
)

// configureProcess keeps the hook explicit so the supervisor can add Windows
// Job Object setup later without changing the protocol code.
func configureProcess(_ *exec.Cmd) error { return nil }

// terminateProcessTree is required on Windows because TerminateProcess only
// stops the direct child. taskkill's /T flag also terminates descendants such
// as npm, OCR, or converter subprocesses spawned by a runtime adapter.
func terminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	_ = exec.Command("taskkill.exe", "/PID", pid, "/T", "/F").Run()
	_ = cmd.Process.Kill()
}
