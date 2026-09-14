package storage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

const (
	rawKillRootEnv       = "STORAGE_RAW_KILL_ROOT"
	rawKillPayloadEnv    = "STORAGE_RAW_KILL_PAYLOAD"
	rawKillMarkerEnv     = "STORAGE_RAW_KILL_MARKER"
	rawKillBreakpointEnv = "STORAGE_RAW_KILL_BREAKPOINT"

	quarantineKillRootEnv       = "STORAGE_QUARANTINE_KILL_ROOT"
	quarantineKillSourceEnv     = "STORAGE_QUARANTINE_KILL_SOURCE"
	quarantineKillMarkerEnv     = "STORAGE_QUARANTINE_KILL_MARKER"
	quarantineKillBreakpointEnv = "STORAGE_QUARANTINE_KILL_BREAKPOINT"
)

func forceKillProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		out, err := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("taskkill %d: %v: %s", cmd.Process.Pid, err, out)
		}
		return nil
	}
	return cmd.Process.Kill()
}

func TestMain(m *testing.M) {
	switch {
	case os.Getenv(rawKillRootEnv) != "":
		os.Exit(runRawPublishKillChild(
			os.Getenv(rawKillRootEnv),
			os.Getenv(rawKillPayloadEnv),
			os.Getenv(rawKillMarkerEnv),
			os.Getenv(rawKillBreakpointEnv),
		))
	case os.Getenv(quarantineKillRootEnv) != "":
		os.Exit(runQuarantineMoveKillChild(
			os.Getenv(quarantineKillRootEnv),
			os.Getenv(quarantineKillSourceEnv),
			os.Getenv(quarantineKillMarkerEnv),
			os.Getenv(quarantineKillBreakpointEnv),
		))
	}
	os.Exit(m.Run())
}

func runRawPublishKillChild(root, payloadPath, markerDir, breakpoint string) int {
	store, err := NewRawFileStore(root)
	if err != nil {
		return 2
	}
	data, err := os.ReadFile(payloadPath)
	if err != nil {
		return 2
	}
	marker := filepath.Join(markerDir, "reached")
	hook := func() {
		if err := os.WriteFile(marker, []byte(breakpoint), 0o600); err != nil {
			return
		}
		// The parent kills the process at this exact instruction boundary.
		for {
			time.Sleep(time.Hour)
		}
	}
	switch breakpoint {
	case "before":
		testRawBeforePublish = hook
	case "after":
		testRawAfterPublish = hook
	default:
		return 2
	}
	if _, err := store.WriteVersion("kill-base", "kill-doc", 1, ".txt", data); err != nil {
		return 2
	}
	return 0
}

func runQuarantineMoveKillChild(root, sourceRel, markerDir, breakpoint string) int {
	store, err := NewRawFileStore(root)
	if err != nil {
		return 2
	}
	marker := filepath.Join(markerDir, "reached")
	hook := func(_, _ string) {
		if err := os.WriteFile(marker, []byte(breakpoint), 0o600); err != nil {
			return
		}
		// The parent kills the process at this exact move boundary.
		for {
			time.Sleep(time.Hour)
		}
	}
	switch breakpoint {
	case "before":
		testQuarantineBeforeMove = hook
	case "after":
		testQuarantineAfterMove = hook
	default:
		return 2
	}
	if _, _, err := store.Quarantine(sourceRel); err != nil {
		return 2
	}
	return 0
}

func TestRawPublishProcessKillConverges(t *testing.T) {
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	runBreakpoint := func(breakpoint string) (root string, markerDir string) {
		t.Helper()
		home := t.TempDir()
		root = filepath.Join(home, "raw")
		markerDir = filepath.Join(home, "marker")
		if err := os.MkdirAll(markerDir, 0o700); err != nil {
			t.Fatal(err)
		}
		store, err := NewRawFileStore(root)
		if err != nil {
			t.Fatal(err)
		}
		oldPath, err := store.Write("kill-base", "kill-doc", ".txt", []byte("old published bytes"))
		if err != nil {
			t.Fatal(err)
		}
		if oldPath != "kill-base/kill-doc.txt" {
			t.Fatalf("old raw path = %s", oldPath)
		}
		payload := filepath.Join(home, "payload")
		want := []byte("new complete publication")
		if err := os.WriteFile(payload, want, 0o600); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(testBinary)
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("%s=%s", rawKillRootEnv, root),
			fmt.Sprintf("%s=%s", rawKillPayloadEnv, payload),
			fmt.Sprintf("%s=%s", rawKillMarkerEnv, markerDir),
			fmt.Sprintf("%s=%s", rawKillBreakpointEnv, breakpoint),
		)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(markerDir, "reached")
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				_ = forceKillProcess(cmd)
				_ = cmd.Wait()
				t.Fatalf("child did not reach %s rename breakpoint", breakpoint)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := forceKillProcess(cmd); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err == nil {
			t.Fatal("killed child exited successfully")
		}
		return root, markerDir
	}

	// Pre-rename kill: the old publication remains authoritative and the
	// synced temp is orphaned. Quarantine/purge converges the orphan bytes.
	currentRel := "kill-base/kill-doc.txt"
	versionRel := "kill-base/.generations/kill-doc/v00000000000000000001.txt"
	root, markerDir := runBreakpoint("before")
	store, err := NewRawFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Read(currentRel)
	if err != nil || string(current) != "old published bytes" {
		t.Fatalf("pre-rename current bytes = %q %v", current, err)
	}
	temps, err := filepath.Glob(filepath.Join(root, "kill-base", ".generations", "kill-doc", ".v00000000000000000001.txt.tmp-*"))
	if err != nil || len(temps) != 1 {
		entries, _ := os.ReadDir(filepath.Join(root, "kill-base"))
		t.Fatalf("pre-rename temp residue: %v %v entries=%v", temps, err, entries)
	}
	tempData, err := os.ReadFile(temps[0])
	if err != nil || string(tempData) != "new complete publication" {
		t.Fatalf("orphan temp bytes = %q %v", tempData, err)
	}
	tempRel, err := filepath.Rel(store.Root(), temps[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, size, err := store.Quarantine(tempRel); err != nil || size != int64(len("new complete publication")) {
		t.Fatalf("quarantine orphan temp: size=%d %v", size, err)
	}
	if err := store.PurgeQuarantine(); err != nil {
		t.Fatal(err)
	}
	tree, err := store.ListAll()
	if err != nil || len(tree) != 1 || tree[0] != currentRel {
		t.Fatalf("raw tree after pre-rename cleanup: %v %v", tree, err)
	}

	// Post-rename kill: the complete new publication is visible atomically and
	// no staging temporary leaked.
	root, markerDir = runBreakpoint("after")
	store, err = NewRawFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	current, err = store.Read(versionRel)
	if err != nil || string(current) != "new complete publication" {
		t.Fatalf("post-rename current bytes = %q %v", current, err)
	}
	temps, err = filepath.Glob(filepath.Join(root, "kill-base", ".generations", "kill-doc", ".v00000000000000000001.txt.tmp-*"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("post-rename temp residue: %v %v", temps, err)
	}
	tree, err = store.ListAll()
	if err != nil || len(tree) != 2 {
		t.Fatalf("raw tree after post-rename kill: %v %v", tree, err)
	}
	current, err = store.Read(currentRel)
	if err != nil || string(current) != "old published bytes" {
		t.Fatalf("old version changed by post-rename kill: %q %v", current, err)
	}
	_ = markerDir
}

func TestQuarantineMoveProcessKillConverges(t *testing.T) {
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sourceRel := "kill-base/kill-doc.txt"
	destinationRel := QuarantineDir + "/" + sourceRel
	want := []byte("active raw bytes at quarantine boundary")

	runBreakpoint := func(breakpoint string) string {
		t.Helper()
		home := t.TempDir()
		root := filepath.Join(home, "raw")
		markerDir := filepath.Join(home, "marker")
		if err := os.MkdirAll(markerDir, 0o700); err != nil {
			t.Fatal(err)
		}
		store, err := NewRawFileStore(root)
		if err != nil {
			t.Fatal(err)
		}
		if path, err := store.Write("kill-base", "kill-doc", ".txt", want); err != nil || path != sourceRel {
			t.Fatalf("seed active raw file: path=%s err=%v", path, err)
		}

		cmd := exec.Command(testBinary)
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("%s=%s", quarantineKillRootEnv, root),
			fmt.Sprintf("%s=%s", quarantineKillSourceEnv, sourceRel),
			fmt.Sprintf("%s=%s", quarantineKillMarkerEnv, markerDir),
			fmt.Sprintf("%s=%s", quarantineKillBreakpointEnv, breakpoint),
		)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(markerDir, "reached")
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				_ = forceKillProcess(cmd)
				_ = cmd.Wait()
				t.Fatalf("child did not reach %s quarantine breakpoint", breakpoint)
				return ""
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := forceKillProcess(cmd); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err == nil {
			t.Fatal("killed quarantine child exited successfully")
		}
		return root
	}

	// Pre-move death leaves the active source authoritative and the reserved
	// quarantine area unchanged. A normal retry then converges explicitly.
	root := runBreakpoint("before")
	store, err := NewRawFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Read(sourceRel)
	if err != nil || string(active) != string(want) {
		t.Fatalf("pre-move active bytes = %q %v", active, err)
	}
	if quarantined, readErr := store.Read(destinationRel); quarantined != nil || readErr != nil {
		t.Fatalf("pre-move quarantine destination = %q %v", quarantined, readErr)
	}
	count, bytes, err := store.QuarantineStats()
	if err != nil || count != 0 || bytes != 0 {
		t.Fatalf("pre-move quarantine stats = (%d,%d) %v", count, bytes, err)
	}
	quarantined, size, err := store.Quarantine(sourceRel)
	if err != nil || quarantined != destinationRel || size != int64(len(want)) {
		t.Fatalf("pre-move retry = (%s,%d) %v", quarantined, size, err)
	}
	if active, err = store.Read(sourceRel); active != nil || err != nil {
		t.Fatalf("pre-move retry source = %q %v", active, err)
	}
	count, bytes, err = store.QuarantineStats()
	if err != nil || count != 1 || bytes != int64(len(want)) {
		t.Fatalf("pre-move retained stats = (%d,%d) %v", count, bytes, err)
	}
	if err := store.PurgeQuarantine(); err != nil {
		t.Fatal(err)
	}
	count, bytes, err = store.QuarantineStats()
	if err != nil || count != 0 || bytes != 0 {
		t.Fatalf("pre-move purge stats = (%d,%d) %v", count, bytes, err)
	}

	// Post-move death leaves the complete source visible only in quarantine.
	root = runBreakpoint("after")
	store, err = NewRawFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	active, err = store.Read(sourceRel)
	if active != nil || err != nil {
		t.Fatalf("post-move active bytes = %q %v", active, err)
	}
	quarantinedBytes, err := store.Read(destinationRel)
	if err != nil || string(quarantinedBytes) != string(want) {
		t.Fatalf("post-move quarantined bytes = %q %v", quarantinedBytes, err)
	}
	tree, err := store.ListAll()
	if err != nil || len(tree) != 0 {
		t.Fatalf("post-move active tree = %v %v", tree, err)
	}
	count, bytes, err = store.QuarantineStats()
	if err != nil || count != 1 || bytes != int64(len(want)) {
		t.Fatalf("post-move quarantine stats = (%d,%d) %v", count, bytes, err)
	}
	if err := store.PurgeQuarantine(); err != nil {
		t.Fatal(err)
	}
	count, bytes, err = store.QuarantineStats()
	if err != nil || count != 0 || bytes != 0 {
		t.Fatalf("post-move purge stats = (%d,%d) %v", count, bytes, err)
	}
	tree, err = store.ListAll()
	if err != nil || len(tree) != 0 {
		t.Fatalf("post-move purged tree = %v %v", tree, err)
	}
}
