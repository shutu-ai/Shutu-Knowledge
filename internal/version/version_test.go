package version

import (
	"strings"

	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

func TestBuildExposesStorageCompatibilityEnvelope(t *testing.T) {
	build := Current()

	if build.Version != Version {
		t.Fatalf("build version = %q, want %q", build.Version, Version)
	}
	if build.StorageFormatVersion != storage.CurrentStorageFormatVersion {
		t.Fatalf("storage format = %d, want %d", build.StorageFormatVersion, storage.CurrentStorageFormatVersion)
	}
	if build.StorageReaderVersion != storage.CurrentStorageReaderVersion {
		t.Fatalf("storage reader = %d, want %d", build.StorageReaderVersion, storage.CurrentStorageReaderVersion)
	}
	if build.StorageWriterVersion != storage.CurrentStorageWriterVersion {
		t.Fatalf("storage writer = %d, want %d", build.StorageWriterVersion, storage.CurrentStorageWriterVersion)
	}
	if build.MinStorageReader != storage.MinStorageReaderVersion {
		t.Fatalf("minimum reader = %d, want %d", build.MinStorageReader, storage.MinStorageReaderVersion)
	}
	if build.MinStorageWriter != storage.MinStorageWriterVersion {
		t.Fatalf("minimum writer = %d, want %d", build.MinStorageWriter, storage.MinStorageWriterVersion)
	}
}

func TestGitCommitIsInjectable(t *testing.T) {
	previous := GitCommit
	t.Cleanup(func() { GitCommit = previous })

	const commit = "0123456789abcdef0123456789abcdef01234567"
	GitCommit = commit

	if build := Current(); build.GitCommit != commit {
		t.Fatalf("git commit = %q, want %q", build.GitCommit, commit)
	}
}

func TestExtensionVersionIsProtocolCompatible(t *testing.T) {
	version := ExtensionVersion()

	if version != "0.3.0" {
		t.Fatalf("extension version = %q, want %q", version, "0.3.0")
	}
	for _, part := range strings.Split(version, ".") {
		if part == "" {
			t.Fatalf("extension version %q has an empty numeric part", version)
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				t.Fatalf("extension version %q contains non-numeric part %q", version, part)
			}
		}
	}
}
