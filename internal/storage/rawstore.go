package storage

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// RawFileStore persists original source bytes: "import means copy". Files
// live under <root>/<baseId>/<docId><ext> or directory-relative subtrees;
// reindex re-reads and re-parses them instead of trusting persisted text.
type RawFileStore struct {
	root string
}

// Publish hooks are test-only fault-injection points for real process-kill
// drills. Production never installs them.
var (
	testRawBeforePublish func()
	testRawAfterPublish  func()

	testQuarantineBeforeMove func(sourceRel, destinationRel string)
	testQuarantineAfterMove  func(sourceRel, destinationRel string)
)

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

// WriteVersion stores one immutable published source version. Re-indexing
// writes a new version instead of replacing bytes already named by an old
// generation citation. Callers still publish metadata by transaction.
func (s *RawFileStore) WriteVersion(baseID, docID string, sourceVersion int64, ext string, data []byte) (string, error) {
	if err := validateSegment(baseID); err != nil {
		return "", err
	}
	if err := validateSegment(docID); err != nil {
		return "", err
	}
	if sourceVersion <= 0 {
		return "", fmt.Errorf("invalid raw source version %d", sourceVersion)
	}
	rel := fmt.Sprintf("%s/.generations/%s/v%020d%s", baseID, docID, sourceVersion, sanitizeExt(ext))
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

	// Raw bytes are immutable inputs for recovery and reindexing. Stage in the
	// destination directory and publish with rename so a crash exposes either
	// the complete previous file or the complete new file, never a truncation.
	temp, err := os.CreateTemp(filepath.Dir(full), "."+filepath.Base(full)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create raw temp file: %w", err)
	}
	tempName := temp.Name()
	published := false
	defer func() {
		_ = temp.Close()
		if !published {
			_ = os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write raw file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync raw file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close raw file: %w", err)
	}
	if testRawBeforePublish != nil {
		testRawBeforePublish()
	}
	if err := os.Rename(tempName, full); err != nil {
		return fmt.Errorf("publish raw file: %w", err)
	}
	published = true
	if testRawAfterPublish != nil {
		testRawAfterPublish()
	}
	return nil
}

// Read returns the stored bytes, or nil when absent.
func (s *RawFileStore) Read(relativePath string) ([]byte, error) {
	return s.ReadContext(context.Background(), relativePath)
}

// ReadContext reads one stored source while honoring cancellation before the
// filesystem operation. The OS read itself remains bounded by the immutable
// file size established at ingest time.
func (s *RawFileStore) ReadContext(ctx context.Context, relativePath string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

// Size reports one active raw file without reading its bytes.
func (s *RawFileStore) Size(relativePath string) (int64, error) {
	full, err := s.pathOf(relativePath)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return 0, err
	}
	if info.IsDir() {
		return 0, fmt.Errorf("raw path is a directory: %s", relativePath)
	}
	return info.Size(), nil
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

// WalkAll visits every stored base-relative path without materializing the
// complete file list. Quarantine contents are excluded from the active scan.
func (s *RawFileStore) WalkAll(ctx context.Context, visit func(string) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if visit == nil {
		return fmt.Errorf("raw file visitor is required")
	}
	return filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if relSlash == QuarantineDir || strings.HasPrefix(relSlash+"/", QuarantineDir+"/") {
			return nil
		}
		return visit(relSlash)
	})
}

// CountAll counts active raw files without retaining their paths.
func (s *RawFileStore) CountAll(ctx context.Context) (int, error) {
	count := 0
	err := s.WalkAll(ctx, func(string) error {
		count++
		return nil
	})
	return count, err
}

// ListAll returns every stored base-relative path (orphan reconciliation).
// New maintenance code should prefer WalkAll to keep memory bounded.
func (s *RawFileStore) ListAll() ([]string, error) {
	var out []string
	err := s.WalkAll(context.Background(), func(rel string) error {
		out = append(out, rel)
		return nil
	})
	return out, err
}

// QuarantineDir is the reserved recovery area. Files here remain on disk for
// inspection or retention-bound purge, but are never active source bytes.
const QuarantineDir = "quarantine"

// Quarantine moves a raw source out of the active tree. It returns the
// quarantine-relative path and original byte size so maintenance results can
// account for recovered disk space without trusting unbounded listings.
func (s *RawFileStore) Quarantine(relativePath string) (string, int64, error) {
	source, err := s.pathOf(relativePath)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", 0, err
	}
	if info.IsDir() {
		return "", 0, fmt.Errorf("cannot quarantine directory: %s", relativePath)
	}
	cleanRelative := filepath.ToSlash(relativePath)
	destinationRelative := QuarantineDir + "/" + cleanRelative
	for attempt := 0; ; attempt++ {
		candidate := destinationRelative
		if attempt > 0 {
			candidate = fmt.Sprintf("%s.%d", destinationRelative, attempt)
		}
		destination, err := s.pathOf(candidate)
		if err != nil {
			return "", 0, err
		}
		if _, statErr := os.Stat(destination); statErr == nil {
			continue
		} else if !os.IsNotExist(statErr) {
			return "", 0, statErr
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return "", 0, fmt.Errorf("create quarantine dir: %w", err)
		}
		if testQuarantineBeforeMove != nil {
			testQuarantineBeforeMove(cleanRelative, candidate)
		}
		if err := os.Rename(source, destination); err != nil {
			return "", 0, fmt.Errorf("quarantine raw file: %w", err)
		}
		if testQuarantineAfterMove != nil {
			testQuarantineAfterMove(cleanRelative, candidate)
		}
		return candidate, info.Size(), nil
	}
}

// QuarantineStats reports the retained recovery area. It is O(quarantine
// size), never the active raw-store size.
func (s *RawFileStore) QuarantineStats() (int, int64, error) {
	root := filepath.Join(s.root, QuarantineDir)
	var count int
	var bytes int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		count++
		bytes += info.Size()
		return nil
	})
	return count, bytes, err
}

// PurgeQuarantine permanently removes retained recovery copies. Callers must
// expose this as an explicit operation; reconciliation defaults to quarantine.
func (s *RawFileStore) PurgeQuarantine() error {
	if err := os.RemoveAll(filepath.Join(s.root, QuarantineDir)); err != nil {
		return fmt.Errorf("purge raw quarantine: %w", err)
	}
	return nil
}

// PurgeExpiredQuarantine removes only recovery copies older than the cutoff.
// This bounds retained disk without forcing every scan into an immediate purge.
func (s *RawFileStore) PurgeExpiredQuarantine(cutoff time.Time) (int, int64, error) {
	root := filepath.Join(s.root, QuarantineDir)
	var count int
	var bytes int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.ModTime().Before(cutoff) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		count++
		bytes += info.Size()
		return removeEmptyParentDirs(root, filepath.Dir(path))
	})
	return count, bytes, err
}

func removeEmptyParentDirs(root, current string) error {
	for {
		cleanCurrent := filepath.Clean(current)
		cleanRoot := filepath.Clean(root)
		if cleanCurrent == cleanRoot || !strings.HasPrefix(cleanCurrent, cleanRoot+string(os.PathSeparator)) {
			return nil
		}
		entries, err := os.ReadDir(cleanCurrent)
		if err != nil || len(entries) != 0 {
			return err
		}
		if err := os.Remove(cleanCurrent); err != nil {
			return err
		}
		current = filepath.Dir(cleanCurrent)
	}
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
