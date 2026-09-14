//go:build !windows

package runtime

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminateProcessTreeKillsProcessGroupDescendants(t *testing.T) {
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}

	marker := filepath.Join(t.TempDir(), "descendant.pid")
	cmd := exec.Command("/bin/sh", "-c",
		`sleep 30 & child=$!; printf '%s\n' "$child" > "$0"; wait "$child"`, marker)
	if err := configureProcess(cmd); err != nil {
		t.Fatalf("configure process group: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start shell: %v", err)
	}

	var descendantPID int
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, readErr := os.ReadFile(marker)
		if readErr == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil {
				t.Fatalf("parse descendant PID %q: %v", data, parseErr)
			}
			descendantPID = pid
			break
		}
		if !os.IsNotExist(readErr) {
			t.Fatalf("read descendant PID marker: %v", readErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("shell did not record its descendant")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := syscall.Kill(descendantPID, 0); err != nil {
		t.Fatalf("descendant %d was not alive before close: %v", descendantPID, err)
	}

	terminateProcessTree(cmd)
	waitErr := cmd.Wait()
	deadline = time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(descendantPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if err == nil {
			if time.Now().After(deadline) {
				t.Fatalf("descendant %d survived process-group termination", descendantPID)
			}
			time.Sleep(5 * time.Millisecond)
			continue
		}
		t.Fatalf("probe descendant %d: %v", descendantPID, err)
	}
	if waitErr == nil {
		t.Fatal("terminated shell exited cleanly")
	}
}
