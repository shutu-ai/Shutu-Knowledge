// Package version holds the build version of shutu-knowledge.
package version

import (
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// Version is the extension's own semantic version. It is independent of the
// Agent version; compatibility is expressed through the Extension v1 contract.
const Version = "0.3.0"

// ExtensionVersion is the numeric identity required by Extension Protocol v1
// manifests. Semantic-version prerelease/build suffixes remain in Version.
func ExtensionVersion() string {
	if boundary := strings.IndexAny(Version, "-+"); boundary >= 0 {
		return Version[:boundary]
	}
	return Version
}

// GitCommit is injected by formal release packaging so a binary can be tied
// to the exact candidate commit that produced it.
var GitCommit string

// Build is the deployed product and storage compatibility identity.
type Build struct {
	Version              string `json:"version"`
	GitCommit            string `json:"gitCommit"`
	StorageFormatVersion int    `json:"storageFormatVersion"`
	StorageReaderVersion int    `json:"storageReaderVersion"`
	StorageWriterVersion int    `json:"storageWriterVersion"`
	MinStorageReader     int    `json:"minStorageReader"`
	MinStorageWriter     int    `json:"minStorageWriter"`
}

// Current returns the identity compiled into this binary.
func Current() Build {
	return Build{
		Version:              Version,
		GitCommit:            GitCommit,
		StorageFormatVersion: storage.CurrentStorageFormatVersion,
		StorageReaderVersion: storage.CurrentStorageReaderVersion,
		StorageWriterVersion: storage.CurrentStorageWriterVersion,
		MinStorageReader:     storage.MinStorageReaderVersion,
		MinStorageWriter:     storage.MinStorageWriterVersion,
	}
}
