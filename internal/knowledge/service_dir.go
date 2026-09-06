package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxIngestFileBytes bounds one file read from disk (reference: 22 MB).
const MaxIngestFileBytes = 22 << 20

// maxDirectoryDepth bounds recursive scans and prevents a filesystem cycle
// from exhausting a background worker.
const maxDirectoryDepth = 32

// directoryEntry is one scanned file pending import.
type directoryEntry struct {
	absPath  string
	relPath  string
	fileName string
	size     int64
	isDir    bool
	err      error
}

// DirectoryImportError is a bounded summary of per-entry failures. Individual
// document rows retain the detailed failure for their own user-visible row.
type DirectoryImportError struct {
	Count int
	Path  string
	Err   error
}

func (e *DirectoryImportError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("directory import finished with %d failed item(s)", e.Count)
	}
	return fmt.Sprintf("directory import finished with %d failed item(s): %v", e.Count, e.Err)
}

// CreateDirectory registers a directory container (no content).
func (s *Service) CreateDirectory(baseID, title, parentDirectoryID, sourcePath string) (Document, error) {
	if _, err := s.store.getBase(baseID); err != nil {
		return Document{}, err
	}
	doc := s.newDocument(baseID, strings.TrimSpace(title), "directory")
	doc.ParentDirectoryID = parentDirectoryID
	doc.SourcePath = sourcePath
	doc.Status = StatusReady
	doc.ChunkCount = 0
	if err := s.store.putDocument(doc); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// findDirectoryByPath locates a tracked container by absolute source path.
func (s *Service) findDirectoryByPath(baseID, sourcePath string) (Document, error) {
	docs, err := s.store.listDocuments(baseID)
	if err != nil {
		return Document{}, err
	}
	for _, doc := range docs {
		if doc.SourceType == "directory" && doc.SourcePath == sourcePath {
			return doc, nil
		}
	}
	return Document{}, ErrNotFound
}

// scanDirectoryTree recursively returns supported files and ordinary
// directories, sorted by stable relative path. Read errors are retained per
// entry so one unreadable subtree cannot hide the rest of the source tree.
func (s *Service) scanDirectoryTree(root string) ([]directoryEntry, error) {
	supported := map[string]bool{}
	for _, ext := range s.parsers.SupportedExtensions() {
		supported[strings.ToLower(ext)] = true
	}
	var entries []directoryEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			entry := directoryEntry{absPath: path, isDir: true, err: walkErr}
			if d != nil && !d.IsDir() {
				entry.isDir = false
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil {
				entry.relPath = filepath.ToSlash(rel)
			}
			entries = append(entries, entry)
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		entry := directoryEntry{
			absPath: path, relPath: filepath.ToSlash(rel), fileName: d.Name(), isDir: d.IsDir(),
		}
		if strings.Count(entry.relPath, "/") >= maxDirectoryDepth {
			entry.err = fmt.Errorf("directory depth exceeds %d", maxDirectoryDepth)
			entries = append(entries, entry)
			return fs.SkipDir
		}
		if d.IsDir() {
			entries = append(entries, entry)
			return nil
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if !supported[ext] {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			entry.err = infoErr
			entries = append(entries, entry)
			return nil
		}
		entry.size = info.Size()
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].relPath < entries[j].relPath })
	return entries, nil
}

// scanDirectory walks the tree and returns supported files sorted by path.
func (s *Service) scanDirectory(root string) ([]directoryEntry, error) {
	supported := map[string]bool{}
	for _, ext := range s.parsers.SupportedExtensions() {
		supported[ext] = true
	}
	var entries []directoryEntry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, per-file errors surface elsewhere
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.TrimPrefix(filepath.Ext(p), ".")
		if !supported[ext] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = filepath.Base(p)
		}
		entries = append(entries, directoryEntry{
			absPath:  p,
			relPath:  filepath.ToSlash(rel),
			fileName: d.Name(),
			size:     info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].relPath < entries[j].relPath })
	return entries, nil
}

// ImportDirectoryTree imports (or incrementally rescans) a directory into a
// stable container: new files import, changed files rebuild, unchanged skip,
// missing files are removed. Runs as a background job with progress/cancel.
func (s *Service) ImportDirectoryTree(ctx context.Context, baseID, rootPath string) (string, error) {
	if s.jobMgr == nil {
		return "", fmt.Errorf("job manager is not running")
	}
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return "", fmt.Errorf("path is required")
	}
	if info, err := os.Stat(rootPath); err != nil || !info.IsDir() {
		return "", fmt.Errorf("path is not a readable directory: %s", rootPath)
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		absolute = rootPath
	}
	entries, err := s.scanDirectoryTree(absolute)
	if err != nil {
		return "", err
	}
	container, err := s.findDirectoryByPath(baseID, absolute)
	if err == ErrNotFound {
		container, err = s.CreateDirectory(baseID, filepath.Base(absolute), "", absolute)
		if err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	return s.submitDirectorySync("import_directory", container, entries)
}

// importChildFile imports one scanned file incrementally:
// unchanged hash -> skip; changed/new -> (re)import; too large -> error.
func (s *Service) importChildFile(ctx context.Context, baseID, containerID string, entry directoryEntry) (*Document, error) {
	if entry.size > MaxIngestFileBytes {
		return nil, fmt.Errorf("file exceeds %d MB limit", MaxIngestFileBytes>>20)
	}
	file, err := os.Open(entry.absPath)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxIngestFileBytes+1))
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	if len(data) > MaxIngestFileBytes {
		return nil, fmt.Errorf("file exceeds %d MB limit", MaxIngestFileBytes>>20)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	existing, exists, err := s.findChildFile(baseID, containerID, entry)
	if err != nil {
		return nil, err
	}
	if !exists {
		existing, err = s.store.findDocumentByTitle(baseID, entry.relPath)
		exists = err == nil
	}
	if exists && existing.SourcePath == entry.absPath && existing.ContentHash == hash && existing.Status == StatusReady {
		return nil, nil // unchanged: skip
	}
	if exists {
		if err := s.DeleteDocument(existing.ID); err != nil {
			return nil, err
		}
	}
	doc := s.newDocument(baseID, entry.relPath, "file")
	doc.ParentDirectoryID = containerID
	doc.SourcePath = entry.absPath
	doc.FileName = entry.fileName
	if err := s.ingest(ctx, &doc, s.baseConfigOrEmpty(baseID), data); err != nil {
		return nil, err
	}
	return &doc, nil
}

// RescanDirectory refreshes a tracked directory using its remembered path.
// Missing/unreadable roots keep their existing subtree and fail visibly
// instead of wiping content that can no longer be verified on disk.
func (s *Service) RescanDirectory(directoryID string) (string, error) {
	doc, err := s.store.getDocument(directoryID)
	if err != nil {
		return "", err
	}
	if doc.SourceType != "directory" {
		return "", fmt.Errorf("document is not a directory")
	}
	if strings.TrimSpace(doc.SourcePath) == "" {
		return "", fmt.Errorf("directory has no tracked source path")
	}
	if info, statErr := os.Stat(doc.SourcePath); statErr != nil || !info.IsDir() {
		doc.Status = StatusFailed
		doc.ErrorCode = ErrSourceMissing
		doc.ErrorMessage = fmt.Sprintf("tracked directory is not readable: %s", doc.SourcePath)
		doc.UpdatedAt = now()
		if err := s.store.putDocument(doc); err != nil {
			return "", err
		}
		return "", fmt.Errorf("%s", doc.ErrorMessage)
	}
	entries, err := s.scanDirectoryTree(doc.SourcePath)
	if err != nil {
		return "", err
	}
	return s.submitDirectorySync("rescan_directory", doc, entries)
}

// RepointSource changes the live path of one top-level file or directory.
// Directory descendants remain until the next rescan, so a mistyped repoint
// cannot immediately delete the old subtree.
func (s *Service) RepointSource(sourceID, path string) (Document, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Document{}, fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		return Document{}, fmt.Errorf("path must be absolute: %s", path)
	}
	doc, err := s.store.getDocument(sourceID)
	if err != nil {
		return Document{}, err
	}
	if doc.ParentDirectoryID != "" {
		return Document{}, fmt.Errorf("only a top-level source can be repointed")
	}
	if doc.SourceType != "file" && doc.SourceType != "directory" {
		return Document{}, fmt.Errorf("only a file or directory source can be repointed")
	}
	info, err := os.Stat(path)
	if err != nil {
		return Document{}, fmt.Errorf("path not found: %s", path)
	}
	switch doc.SourceType {
	case "directory":
		if !info.IsDir() {
			return Document{}, fmt.Errorf("a directory source must be repointed to a directory")
		}
	case "file":
		if !info.Mode().IsRegular() {
			return Document{}, fmt.Errorf("a file source must be repointed to a file")
		}
		fileName := filepath.Base(path)
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
		supported := false
		for _, item := range s.parsers.SupportedExtensions() {
			if item == ext {
				supported = true
				break
			}
		}
		if !supported {
			return Document{}, fmt.Errorf("unsupported document format: %s", ext)
		}
		doc.FileName = fileName
		doc.MimeType = ""
	}
	doc.SourcePath = filepath.Clean(path)
	doc.UpdatedAt = now()
	if err := s.store.putDocument(doc); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// DeleteDirectoryRecursive removes a directory, nested containers, files,
// chunks, and raw copies. It refuses to treat a non-directory as a subtree.
func (s *Service) DeleteDirectoryRecursive(directoryID string) (int, error) {
	doc, err := s.store.getDocument(directoryID)
	if err != nil {
		return 0, err
	}
	if doc.SourceType != "directory" {
		return 0, fmt.Errorf("document is not a directory")
	}
	docs, err := s.store.listDocuments(doc.BaseID)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, child := range docs {
		if child.ParentDirectoryID != directoryID {
			continue
		}
		var childErr error
		if child.SourceType == "directory" {
			var childRemoved int
			childRemoved, childErr = s.DeleteDirectoryRecursive(child.ID)
			removed += childRemoved
		} else {
			childErr = s.DeleteDocument(child.ID)
			removed++
		}
		if childErr != nil && !errors.Is(childErr, ErrNotFound) {
			return removed, childErr
		}
	}
	if err := s.DeleteDocument(doc.ID); err != nil && err != ErrNotFound {
		return removed, err
	}
	return removed + 1, nil
}

func (s *Service) submitDirectorySync(kind string, container Document, entries []directoryEntry) (string, error) {
	if s.jobMgr == nil {
		return "", fmt.Errorf("job manager is not running")
	}
	containerID, baseID, root := container.ID, container.BaseID, container.SourcePath
	return s.jobMgr.Submit(kind, baseID, len(entries), func(jobCtx context.Context, report func(int)) error {
		return s.syncNestedDirectoryTree(jobCtx, baseID, containerID, root, entries, report)
	})
}

func (s *Service) syncNestedDirectoryTree(ctx context.Context, baseID, rootID, root string, entries []directoryEntry, report func(int)) error {
	byRel := map[string]directoryEntry{}
	directoryIDs := map[string]string{rootID: "."}
	for _, entry := range entries {
		byRel[entry.relPath] = entry
	}
	_, err := s.store.listDocuments(baseID)
	if err != nil {
		return err
	}
	kept := map[string]bool{}
	failures := 0
	var firstErr error
	fail := func(entry directoryEntry, err error) {
		failures++
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", entry.relPath, err)
		}
		if id := s.recordChildFailure(baseID, rootID, entry, err); id != "" {
			kept[id] = true
		}
	}

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.err != nil {
			fail(entry, entry.err)
			report(processedCount(entries, entry.relPath))
			continue
		}
		parentRel := filepath.ToSlash(filepath.Dir(entry.relPath))
		parentID := rootID
		if parentRel != "." {
			if id := directoryIDs[parentRel]; id != "" {
				parentID = id
			} else {
				fail(entry, fmt.Errorf("parent directory was not synced"))
				report(processedCount(entries, entry.relPath))
				continue
			}
		}
		if entry.isDir {
			doc, err := s.syncDirectoryContainer(baseID, parentID, entry)
			if err != nil {
				fail(entry, err)
			} else {
				kept[doc.ID] = true
				directoryIDs[entry.relPath] = doc.ID
			}
		} else {
			doc, err := s.importChildFile(ctx, baseID, parentID, entry)
			if err != nil {
				fail(entry, err)
			} else if doc != nil {
				kept[doc.ID] = true
			}
		}
		report(processedCount(entries, entry.relPath))
	}

	descendants, err := s.store.listDocuments(baseID)
	if err != nil {
		return err
	}
	for _, doc := range descendants {
		if doc.ParentDirectoryID == "" || kept[doc.ID] {
			continue
		}
		if !s.hasAncestor(rootID, doc.ParentDirectoryID, descendants) {
			continue
		}
		if err := s.DeleteDocument(doc.ID); err != nil && err != ErrNotFound {
			failures++
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", doc.Title, err)
			}
		}
	}
	if failures > 0 {
		return &DirectoryImportError{Count: failures, Path: root, Err: firstErr}
	}
	if container, err := s.store.getDocument(rootID); err == nil {
		container.Status = StatusReady
		container.ErrorCode = ""
		container.ErrorMessage = ""
		container.UpdatedAt = now()
		_ = s.store.putDocument(container)
	}
	return nil
}

func (s *Service) syncDirectoryContainer(baseID, parentID string, entry directoryEntry) (Document, error) {
	docs, err := s.store.listDocuments(baseID)
	if err != nil {
		return Document{}, err
	}
	for _, doc := range docs {
		if doc.SourceType == "directory" && doc.ParentDirectoryID == parentID && doc.SourcePath == entry.absPath {
			doc.Title = entry.fileName
			doc.UpdatedAt = now()
			return doc, s.store.putDocument(doc)
		}
	}
	return s.CreateDirectory(baseID, entry.fileName, parentID, entry.absPath)
}

func (s *Service) findChildFile(baseID, parentID string, entry directoryEntry) (Document, bool, error) {
	docs, err := s.store.listDocuments(baseID)
	if err != nil {
		return Document{}, false, err
	}
	for _, doc := range docs {
		if doc.SourceType != "file" || doc.ParentDirectoryID != parentID {
			continue
		}
		if doc.SourcePath == entry.absPath || doc.FileName == entry.fileName {
			return doc, true, nil
		}
	}
	return Document{}, false, nil
}

func (s *Service) recordChildFailure(baseID, rootID string, entry directoryEntry, cause error) string {
	if entry.isDir {
		return ""
	}
	docs, err := s.store.listDocuments(baseID)
	if err != nil {
		return ""
	}
	parentID := rootID
	var existing *Document
	for index, doc := range docs {
		if doc.SourceType == "file" && (doc.SourcePath == entry.absPath || doc.FileName == entry.fileName) {
			candidate := docs[index]
			existing = &candidate
			if doc.SourcePath == entry.absPath {
				break
			}
		}
	}
	if existing != nil {
		existing.Status = StatusFailed
		existing.ErrorCode = ErrParseFailed
		existing.ErrorMessage = cause.Error()
		existing.UpdatedAt = now()
		_ = s.store.putDocument(*existing)
		return existing.ID
	}
	failed := s.newDocument(baseID, entry.relPath, "file")
	failed.ParentDirectoryID = parentID
	failed.SourcePath = entry.absPath
	failed.FileName = entry.fileName
	failed.Status = StatusFailed
	failed.ErrorCode = ErrParseFailed
	failed.ErrorMessage = cause.Error()
	_ = s.store.putDocument(failed)
	return failed.ID
}

func (s *Service) hasAncestor(rootID, parentID string, docs []Document) bool {
	byID := map[string]Document{}
	for _, doc := range docs {
		byID[doc.ID] = doc
	}
	for current := parentID; current != ""; {
		if current == rootID {
			return true
		}
		parent, ok := byID[current]
		if !ok || parent.ParentDirectoryID == "" {
			return false
		}
		current = parent.ParentDirectoryID
	}
	return false
}

func processedCount(entries []directoryEntry, relPath string) int {
	count := 0
	for _, entry := range entries {
		if entry.relPath == "." || entry.relPath <= relPath {
			count++
		}
	}
	return count
}
