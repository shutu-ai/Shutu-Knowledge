package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProfileIncludesNativeLifecycle(t *testing.T) {
	for _, test := range []struct {
		profile string
		want    bool
	}{
		{profile: "host", want: true},
		{profile: "all", want: true},
		{profile: "identity", want: false},
		{profile: "core", want: false},
		{profile: "agent", want: false},
		{profile: "web", want: false},
	} {
		if got := profileIncludesNativeLifecycle(test.profile); got != test.want {
			t.Fatalf("profileIncludesNativeLifecycle(%q) = %v, want %v", test.profile, got, test.want)
		}
	}
}

func TestGitStateIncludesUntrackedFiles(t *testing.T) {
	repoRoot := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, output)
		}
	}
	runGit("init", "--quiet")
	runGit("config", "user.email", "acceptance-test@example.invalid")
	runGit("config", "user.name", "release acceptance test")
	tracked := filepath.Join(repoRoot, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tracked.txt")
	runGit("commit", "--quiet", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repoRoot, "untracked.txt"), []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	commit, dirty, err := gitStateAt(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(commit) != 40 {
		t.Fatalf("commit = %q, want a 40-character SHA", commit)
	}
	if !dirty {
		t.Fatal("gitStateAt reported a clean worktree with an untracked file")
	}
}
