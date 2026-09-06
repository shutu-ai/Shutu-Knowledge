package storage

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// RawFileStore persists original source bytes: "import means copy". Files
// live under <root>/<baseId>/<docId><ext> or directory-relative subtrees;
// reindex re-reads and re-parses them instead of trusting persisted text.
type RawFileStore struct {
	root string
}

// NewRawFileStore creates the root directory if needed.
func NewRawFileStore(root string) (*RawFileStore, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create raw dir: %w", err)
	}
	return &RawFileStore{root: root}, nil
}

// Root returns the raw store root directory.
func (s *RawFileStore) Root() string { return s.root }

// pathOf validates a stored relative path and resolves it under the root.
func (s *RawFileStore) pathOf(relativePath string) (string, error) {
	clean := filepath.ToSlash(relativePath)
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || strings.Contains(clean, "\\..") {
		return "", fmt.Errorf("unsafe raw file path: %s", relativePath)
	}
	if path.IsAbs(clean) || filepath.IsAbs(relativePath) || strings.Contains(relativePath, ":") {
		return "", fmt.Errorf("unsafe raw file path: %s", relativePath)
	}
	resolved := filepath.Join(s.root, filepath.FromSlash(clean))
	if !strings.HasPrefix(filepath.Clean(resolved), filepath.Clean(s.root)+string(os.PathSeparator)) {
		return "", fmt.Errorf("unsafe raw file path: %s", relativePath)
	}
	return resolved, nil
}

// Write stores one document's source bytes at <baseId>/<docId><ext>.
func (s *RawFileStore) Write(baseID, docID, ext string, data []byte) (string, error) {
	if err := validateSegment(baseID); err != nil {
		return "", err
	}
	if err := validateSegment(docID); err != nil {
		return "", err
	}
	rel := baseID + "/" + docID + sanitizeExt(ext)
	if _, err := s.pathOf(rel); err != nil {
		return "", err
	}
	return rel, s.writeRel(rel, data)
}

// WriteRel stores bytes at a caller-chosen base-relative path, preserving a
// directory import's on-disk tree.
func (s *RawFileStore) WriteRel(baseID, relativePath string, data []byte) (string, error) {
	if err := validateSegment(baseID); err != nil {
		return "", err
	}
	rel := baseID + "/" + filepath.ToSlash(strings.TrimPrefix(relativePath, "./"))
	if _, err := s.pathOf(rel); err != nil {
		return "", err
	}
	return rel, s.writeRel(rel, data)
}

func (s *RawFileStore) writeRel(relativePath string, data []byte) error {
	full, err := s.pathOf(relativePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return fmt.Errorf("create raw dir: %w", err)
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		return fmt.Errorf("write raw file: %w", err)
	}
	return nil
}

// Read returns the stored bytes, or nil when absent.
func (s *RawFileStore) Read(relativePath string) ([]byte, error) {
	full, err := s.pathOf(relativePath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// Delete removes one raw file (missing = no-op).
func (s *RawFileStore) Delete(relativePath string) error {
	full, err := s.pathOf(relativePath)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeleteBase removes every raw file of a base. The base id is validated, so
// a crafted id can never make the delete escape the root.
func (s *RawFileStore) DeleteBase(baseID string) error {
	if err := validateSegment(baseID); err != nil {
		return err
	}
	full, err := s.pathOf(baseID)
	if err != nil {
		return err
	}
	return os.RemoveAll(full)
}

// ListAll returns every stored base-relative path (orphan reconciliation).
func (s *RawFileStore) ListAll() ([]string, error) {
	var out []string
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

func validateSegment(segment string) error {
	trimmed := strings.TrimSpace(segment)
	if trimmed == "" || trimmed != segment || strings.ContainsAny(segment, "/\\") ||
		segment == "." || segment == ".." || strings.Contains(segment, "..") {
		return fmt.Errorf("invalid raw store segment: %q", segment)
	}
	return nil
}

func sanitizeExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		return ""
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	for _, r := range ext {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_') {
			return ".bin"
		}
	}
	return ext
}
