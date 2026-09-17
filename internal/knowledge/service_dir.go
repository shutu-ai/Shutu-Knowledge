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
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/jobs"
)

// MaxIngestFileBytes bounds one imported file (100 MiB).
const MaxIngestFileBytes = 100 << 20

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

// directorySyncIndex keeps one database snapshot in memory while a directory
// tree is synchronized. Re-querying all documents for every filesystem entry
// turns a large tree into an O(files x documents) disk workload.
type directorySyncIndex struct {
	docs             []Document
	byID             map[string]Document
	directoriesByKey map[string]Document
	filesByPath      map[string]Document
	filesByName      map[string]Document
}

func newDirectorySyncIndex(docs []Document) *directorySyncIndex {
	index := &directorySyncIndex{
		docs:             append([]Document(nil), docs...),
		byID:             make(map[string]Document, len(docs)),
		directoriesByKey: make(map[string]Document),
		filesByPath:      make(map[string]Document),
		filesByName:      make(map[string]Document),
	}
	for _, doc := range docs {
		index.add(doc)
	}
	return index
}

func directoryIndexKey(parentID, value string) string { return parentID + "\x00" + value }

func (i *directorySyncIndex) add(doc Document) {
	i.byID[doc.ID] = doc
	if doc.SourceType == "directory" {
		i.directoriesByKey[directoryIndexKey(doc.ParentDirectoryID, doc.SourcePath)] = doc
		return
	}
	if doc.SourceType == "file" {
		i.filesByPath[directoryIndexKey(doc.ParentDirectoryID, doc.SourcePath)] = doc
		i.filesByName[directoryIndexKey(doc.ParentDirectoryID, doc.FileName)] = doc
	}
}

func (i *directorySyncIndex) remove(doc Document) {
	delete(i.byID, doc.ID)
	if doc.SourceType == "directory" {
		delete(i.directoriesByKey, directoryIndexKey(doc.ParentDirectoryID, doc.SourcePath))
		return
	}
	if doc.SourceType == "file" {
		delete(i.filesByPath, directoryIndexKey(doc.ParentDirectoryID, doc.SourcePath))
		delete(i.filesByName, directoryIndexKey(doc.ParentDirectoryID, doc.FileName))
	}
}

func (i *directorySyncIndex) findDirectory(parentID, sourcePath string) (Document, bool) {
	doc, ok := i.directoriesByKey[directoryIndexKey(parentID, sourcePath)]
	return doc, ok
}

func (i *directorySyncIndex) findFile(parentID, sourcePath, fileName string) (Document, bool) {
	if doc, ok := i.filesByPath[directoryIndexKey(parentID, sourcePath)]; ok {
		return doc, true
	}
	doc, ok := i.filesByName[directoryIndexKey(parentID, fileName)]
	return doc, ok
}

func (i *directorySyncIndex) upsert(doc Document) {
	if old, ok := i.byID[doc.ID]; ok {
		i.remove(old)
	}
	i.add(doc)
}

func (i *directorySyncIndex) hasAncestor(rootID, parentID string) bool {
	for current := parentID; current != ""; {
		if current == rootID {
			return true
		}
		doc, ok := i.byID[current]
		if !ok {
			return false
		}
		current = doc.ParentDirectoryID
	}
	return false
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
	return s.createDirectoryContext(context.Background(), baseID, title, parentDirectoryID, sourcePath)
}

// CreateDirectoryWithContext binds directory creation to the caller.
func (s *Service) CreateDirectoryWithContext(ctx context.Context, baseID, title, parentDirectoryID, sourcePath string) (Document, error) {
	return s.createDirectoryContext(ctx, baseID, title, parentDirectoryID, sourcePath)
}

func (s *Service) createDirectoryContext(ctx context.Context, baseID, title, parentDirectoryID, sourcePath string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	if _, err := s.store.getBaseContext(ctx, baseID); err != nil {
		return Document{}, err
	}
	doc := s.newDocument(baseID, strings.TrimSpace(title), "directory")
	doc.ParentDirectoryID = parentDirectoryID
	doc.SourcePath = sourcePath
	doc.Status = StatusReady
	doc.ChunkCount = 0
	if err := s.store.putDocumentContext(ctx, doc); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// findDirectoryByPath locates a tracked container by absolute source path.
func (s *Service) findDirectoryByPath(baseID, sourcePath string) (Document, error) {
	return s.findDirectoryByPathContext(context.Background(), baseID, sourcePath)
}

func (s *Service) findDirectoryByPathContext(ctx context.Context, baseID, sourcePath string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	doc, err := s.store.findDocumentBySourcePathContext(ctx, baseID, sourcePath)
	if err != nil || doc.SourceType != "directory" {
		if err == nil {
			err = ErrNotFound
		}
		return Document{}, err
	}
	return doc, nil
}

// FindDirectoryByPath returns the stable directory container for a normalized
// source path. Durable directory-import recovery uses this lookup before it
// touches the filesystem, so a committed aggregate marker can short-circuit
// even when the original source has since disappeared.
func (s *Service) FindDirectoryByPath(baseID, sourcePath string) (Document, error) {
	return s.findDirectoryByPathPublicContext(context.Background(), baseID, sourcePath)
}

func (s *Service) FindDirectoryByPathContext(ctx context.Context, baseID, sourcePath string) (Document, error) {
	return s.findDirectoryByPathPublicContext(ctx, baseID, sourcePath)
}

func (s *Service) findDirectoryByPathPublicContext(ctx context.Context, baseID, sourcePath string) (Document, error) {
	path := normalizeDirectoryPath(sourcePath)
	if path == "" {
		return Document{}, ErrNotFound
	}
	return s.findDirectoryByPathContext(ctx, baseID, path)
}

func normalizeDirectoryPath(sourcePath string) string {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return ""
	}
	absolute, err := filepath.Abs(sourcePath)
	if err != nil {
		absolute = sourcePath
	}
	return filepath.Clean(absolute)
}

// scanDirectoryTree recursively returns supported files and ordinary
// directories, sorted by stable relative path. Read errors are retained per
// entry so one unreadable subtree cannot hide the rest of the source tree.
func (s *Service) scanDirectoryTree(ctx context.Context, root string) ([]directoryEntry, error) {
	supported := map[string]bool{}
	for _, ext := range s.parsers.SupportedExtensions() {
		supported[strings.ToLower(ext)] = true
	}
	var entries []directoryEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
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

// prepareDirectoryImport validates a filesystem source and binds it to a
// boundary, so a replay resolves the same source by path.
func (s *Service) prepareDirectoryImport(baseID, rootPath string) (Document, error) {
	return s.prepareDirectoryImportContext(context.Background(), baseID, rootPath)
}

func (s *Service) prepareDirectoryImportContext(ctx context.Context, baseID, rootPath string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return Document{}, fmt.Errorf("path is required")
	}
	if info, err := os.Stat(rootPath); err != nil || !info.IsDir() {
		return Document{}, fmt.Errorf("path is not a readable directory: %s", rootPath)
	}
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	absolute := normalizeDirectoryPath(rootPath)
	container, err := s.findDirectoryByPathContext(ctx, baseID, absolute)
	if err == ErrNotFound {
		container, err = s.createDirectoryContext(ctx, baseID, filepath.Base(absolute), "", absolute)
		if err != nil {
			return Document{}, err
		}
	} else if err != nil {
		return Document{}, err
	}
	return container, nil
}

// RunDirectoryImport executes the directory command synchronously in the
// caller's worker. A persistent operation adapter calls this; the incremental
// sync makes a replay after a crash safe.
func (s *Service) RunDirectoryImport(ctx context.Context, baseID, rootPath string, report func(jobs.ProgressUpdate)) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	container, err := s.prepareDirectoryImportContext(ctx, baseID, rootPath)
	if err != nil {
		return Document{}, err
	}
	if _, err := s.runDirectorySync(ctx, container, report); err != nil {
		return container, err
	}
	return s.store.getDocumentContext(ctx, container.ID)
}

// importChildFile imports one scanned file incrementally:
// unchanged hash -> skip; changed/new -> (re)import; too large -> error.
func (s *Service) importChildFile(ctx context.Context, baseID, containerID string, entry directoryEntry, index *directorySyncIndex) (*Document, error) {
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > MaxIngestFileBytes {
		return nil, fmt.Errorf("file exceeds %d MB limit", MaxIngestFileBytes>>20)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	existing, exists, err := s.findChildFile(baseID, containerID, entry, index)
	if err != nil {
		return nil, err
	}
	if !exists {
		existing, err = s.store.findDocumentByTitleContext(ctx, baseID, entry.relPath)
		exists = err == nil
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	if exists && existing.SourcePath == entry.absPath && existing.ContentHash == hash && existing.Status == StatusReady {
		return nil, nil // unchanged: skip
	}
	if exists {
		if _, err := s.DeleteDocumentWithProgress(ctx, existing.ID, nil); err != nil {
			return nil, err
		}
		index.remove(existing)
	}
	doc := s.newDocument(baseID, entry.relPath, "file")
	doc.ParentDirectoryID = containerID
	doc.SourcePath = entry.absPath
	doc.FileName = entry.fileName
	if err := s.ingest(ctx, &doc, s.baseConfigOrEmpty(baseID), data); err != nil {
		// ingest persists the raw source before parsing. Keep that failed
		// document in the sync index so recordChildFailure updates the same
		// row instead of creating a source-less duplicate placeholder. This
		// preserves a recovery path for a later reindex after a parser fix.
		index.add(doc)
		return &doc, err
	}
	index.add(doc)
	return &doc, nil
}

func (s *Service) prepareDirectoryRescan(directoryID string) (Document, error) {
	return s.prepareDirectoryRescanContext(context.Background(), directoryID)
}

func (s *Service) prepareDirectoryRescanContext(ctx context.Context, directoryID string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	doc, err := s.store.getDocumentContext(ctx, directoryID)
	if err != nil {
		return Document{}, err
	}
	if doc.SourceType != "directory" {
		return Document{}, fmt.Errorf("document is not a directory")
	}
	if strings.TrimSpace(doc.SourcePath) == "" {
		return Document{}, fmt.Errorf("directory has no tracked source path")
	}
	if info, statErr := os.Stat(doc.SourcePath); statErr != nil || !info.IsDir() {
		doc.Status = StatusFailed
		doc.ErrorCode = ErrSourceMissing
		doc.ErrorMessage = fmt.Sprintf("tracked directory is not readable: %s", doc.SourcePath)
		doc.UpdatedAt = now()
		if err := s.store.putDocumentContext(ctx, doc); err != nil {
			return Document{}, err
		}
		return Document{}, fmt.Errorf("%s", doc.ErrorMessage)
	}
	return doc, nil
}

// RunDirectoryRescan executes a persisted rescan command in an Operation
// worker using the remembered source path.
func (s *Service) RunDirectoryRescan(ctx context.Context, directoryID string, report func(jobs.ProgressUpdate)) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	container, err := s.prepareDirectoryRescanContext(ctx, directoryID)
	if err != nil {
		return Document{}, err
	}
	if _, err := s.runDirectorySync(ctx, container, report); err != nil {
		return container, err
	}
	return s.store.getDocumentContext(ctx, container.ID)
}

// RepointSource changes the live path of one top-level file or directory.
// Directory descendants remain until the next rescan, so a mistyped repoint
// cannot immediately delete the old subtree.
func (s *Service) RepointSource(sourceID, path string) (Document, error) {
	return s.RepointSourceWithContext(context.Background(), sourceID, path)
}

// RepointSourceWithContext binds source inspection and update to the caller.
func (s *Service) RepointSourceWithContext(ctx context.Context, sourceID, path string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return Document{}, fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		return Document{}, fmt.Errorf("path must be absolute: %s", path)
	}
	doc, err := s.store.getDocumentContext(ctx, sourceID)
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
	if err := s.store.putDocumentContext(ctx, doc); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// DeleteDirectoryRecursive removes a directory, nested containers, files,
// chunks, and raw copies. It refuses to treat a non-directory as a subtree.
func (s *Service) DeleteDirectoryRecursive(directoryID string) (int, error) {
	return s.DeleteDirectoryRecursiveWithProgress(context.Background(), directoryID, nil)
}

// DeleteDirectoryRecursiveWithProgress is the cancellable directory-delete
// path used by the Web job queue. The callback runs after each document has
// been removed, allowing callers to report real document-level progress.
func (s *Service) DeleteDirectoryRecursiveWithProgress(ctx context.Context, directoryID string, onDeleted func()) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	doc, err := s.store.getDocumentIncludingDeletingContext(ctx, directoryID)
	if err != nil {
		return 0, err
	}
	if doc.SourceType != "directory" {
		return 0, fmt.Errorf("document is not a directory")
	}
	return s.deleteDocumentTree(ctx, directoryID, onDeleted)
}

func (s *Service) runDirectorySync(ctx context.Context, container Document, report func(jobs.ProgressUpdate)) (int, error) {
	if report == nil {
		report = func(jobs.ProgressUpdate) {}
	}
	containerID, baseID, root := container.ID, container.BaseID, container.SourcePath
	container.Status = StatusProcessing
	// The document phase is constrained by the existing schema to parsing or
	// embedding. The job itself carries the more precise "scanning" phase.
	container.Phase = PhaseParsing
	container.Progress = 0
	container.ErrorCode = ""
	container.ErrorMessage = ""
	container.UpdatedAt = now()
	if err := s.store.putDocumentContext(ctx, container); err != nil {
		return 0, err
	}
	report(jobs.ProgressUpdate{Phase: PhaseScanning, Percent: 0})
	entries, scanErr := s.scanDirectoryTree(ctx, root)
	if scanErr != nil {
		if markErr := s.markDirectorySyncFailedContext(ctx, containerID, scanErr); markErr != nil {
			return 0, errors.Join(scanErr, fmt.Errorf("record directory failure: %w", markErr))
		}
		return 0, scanErr
	}
	report(jobs.ProgressUpdate{Phase: PhaseScanning, Percent: 0, Total: len(entries)})
	lastProgress := -1
	lastReportAt := time.Time{}
	progressStep := len(entries) / 100
	if progressStep < 1 {
		progressStep = 1
	}
	emitProgress := func(progress int, file string) error {
		completed := progress >= len(entries)
		now := time.Now()
		if !completed && progress-lastProgress < progressStep && now.Sub(lastReportAt) < 500*time.Millisecond {
			return nil
		}
		lastProgress = progress
		lastReportAt = now
		if err := s.updateDirectorySyncProgressContext(ctx, containerID, progress); err != nil {
			return err
		}
		report(jobs.ProgressUpdate{Phase: PhaseScanning, File: file, Completed: progress, Total: len(entries)})
		return nil
	}
	syncErr := s.syncNestedDirectoryTree(ctx, baseID, containerID, root, entries, emitProgress)
	if syncErr == nil && len(entries) == 0 {
		syncErr = emitProgress(0, "")
	}
	if syncErr != nil {
		if markErr := s.markDirectorySyncFailedContext(ctx, containerID, syncErr); markErr != nil {
			return len(entries), errors.Join(syncErr, fmt.Errorf("record directory failure: %w", markErr))
		}
		return len(entries), syncErr
	}
	return max(len(entries), 1), nil
}

func (s *Service) updateDirectorySyncProgress(id string, progress int) error {
	return s.updateDirectorySyncProgressContext(context.Background(), id, progress)
}

func (s *Service) updateDirectorySyncProgressContext(ctx context.Context, id string, progress int) error {
	doc, err := s.store.getDocumentContext(ctx, id)
	if err != nil {
		return err
	}
	doc.Status = StatusProcessing
	doc.Phase = PhaseParsing
	doc.Progress = progress
	doc.UpdatedAt = now()
	return s.store.putDocumentContext(ctx, doc)
}

func (s *Service) markDirectorySyncFailed(id string, cause error) error {
	return s.markDirectorySyncFailedContext(context.Background(), id, cause)
}

func (s *Service) markDirectorySyncFailedContext(ctx context.Context, id string, cause error) error {
	doc, err := s.store.getDocumentContext(ctx, id)
	if err != nil {
		return err
	}
	doc.Status = StatusFailed
	doc.Phase = ""
	doc.ErrorCode = ErrParseFailed
	doc.ErrorMessage = cause.Error()
	doc.UpdatedAt = now()
	return s.store.putDocumentContext(ctx, doc)
}

func (s *Service) syncNestedDirectoryTree(ctx context.Context, baseID, rootID, root string, entries []directoryEntry, report func(int, string) error) error {
	directoryIDs := map[string]string{rootID: "."}
	docs, err := s.store.listDocumentMetadataTreeContext(ctx, baseID, rootID)
	if err != nil {
		return err
	}
	index := newDirectorySyncIndex(docs)
	kept := map[string]bool{}
	failures := 0
	var firstErr error
	fail := func(parentID string, entry directoryEntry, err error) error {
		failures++
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", entry.relPath, err)
		}
		if id, markerErr := s.recordChildFailureContext(ctx, baseID, parentID, entry, err, index); markerErr != nil {
			return fmt.Errorf("record failed directory item %s: %w", entry.relPath, markerErr)
		} else if id != "" {
			kept[id] = true
		}
		return nil
	}

	for entryIndex, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		parentRel := filepath.ToSlash(filepath.Dir(entry.relPath))
		parentID := rootID
		if parentRel != "." {
			if id := directoryIDs[parentRel]; id != "" {
				parentID = id
			} else {
				if err := fail(rootID, entry, fmt.Errorf("parent directory was not synced")); err != nil {
					return err
				}
				if err := report(entryIndex+1, entry.relPath); err != nil {
					return err
				}
				continue
			}
		}
		if entry.err != nil {
			if err := fail(parentID, entry, entry.err); err != nil {
				return err
			}
			if err := report(entryIndex+1, entry.relPath); err != nil {
				return err
			}
			continue
		}
		if entry.isDir {
			doc, err := s.syncDirectoryContainerContext(ctx, baseID, parentID, entry, index)
			if err != nil {
				if failErr := fail(parentID, entry, err); failErr != nil {
					return failErr
				}
			} else {
				kept[doc.ID] = true
				directoryIDs[entry.relPath] = doc.ID
			}
		} else {
			doc, err := s.importChildFile(ctx, baseID, parentID, entry, index)
			if err != nil {
				if failErr := fail(parentID, entry, err); failErr != nil {
					return failErr
				}
			} else if doc != nil {
				kept[doc.ID] = true
			}
		}
		if err := report(entryIndex+1, entry.relPath); err != nil {
			return err
		}
	}

	for _, doc := range index.docs {
		if doc.ParentDirectoryID == "" || kept[doc.ID] {
			continue
		}
		if !index.hasAncestor(rootID, doc.ParentDirectoryID) {
			continue
		}
		if _, err := s.DeleteDocumentWithProgress(ctx, doc.ID, nil); err != nil && err != ErrNotFound {
			failures++
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", doc.Title, err)
			}
		}
	}
	if failures > 0 {
		return &DirectoryImportError{Count: failures, Path: root, Err: firstErr}
	}
	container, err := s.store.getDocumentContext(ctx, rootID)
	if err != nil {
		return err
	}
	container.Status = StatusReady
	container.Phase = ""
	container.Progress = 100
	container.ErrorCode = ""
	container.ErrorMessage = ""
	container.UpdatedAt = now()
	if err := s.store.putDocumentContext(ctx, container); err != nil {
		return err
	}
	return nil
}

func (s *Service) syncDirectoryContainer(baseID, parentID string, entry directoryEntry, index *directorySyncIndex) (Document, error) {
	return s.syncDirectoryContainerContext(context.Background(), baseID, parentID, entry, index)
}

func (s *Service) syncDirectoryContainerContext(ctx context.Context, baseID, parentID string, entry directoryEntry, index *directorySyncIndex) (Document, error) {
	if doc, ok := index.findDirectory(parentID, entry.absPath); ok {
		doc.Title = entry.fileName
		doc.UpdatedAt = now()
		if err := s.store.putDocumentContext(ctx, doc); err != nil {
			return Document{}, err
		}
		index.upsert(doc)
		return doc, nil
	}
	doc, err := s.createDirectoryContext(ctx, baseID, entry.fileName, parentID, entry.absPath)
	if err == nil {
		index.add(doc)
	}
	return doc, err
}

func (s *Service) findChildFile(_ string, parentID string, entry directoryEntry, index *directorySyncIndex) (Document, bool, error) {
	if doc, ok := index.findFile(parentID, entry.absPath, entry.fileName); ok {
		return doc, true, nil
	}
	return Document{}, false, nil
}

func (s *Service) recordChildFailure(baseID, rootID string, entry directoryEntry, cause error, index *directorySyncIndex) (string, error) {
	return s.recordChildFailureContext(context.Background(), baseID, rootID, entry, cause, index)
}

func (s *Service) recordChildFailureContext(ctx context.Context, baseID, parentID string, entry directoryEntry, cause error, index *directorySyncIndex) (string, error) {
	if entry.isDir {
		return "", nil
	}
	if existing, ok := index.findFile(parentID, entry.absPath, entry.fileName); ok {
		existing.Status = StatusFailed
		existing.ErrorCode = ErrParseFailed
		existing.ErrorMessage = cause.Error()
		existing.UpdatedAt = now()
		if err := s.store.putDocumentContext(ctx, existing); err != nil {
			return "", err
		}
		index.upsert(existing)
		return existing.ID, nil
	}
	failed := s.newDocument(baseID, entry.relPath, "file")
	failed.ParentDirectoryID = parentID
	failed.SourcePath = entry.absPath
	failed.FileName = entry.fileName
	failed.Status = StatusFailed
	failed.ErrorCode = ErrParseFailed
	failed.ErrorMessage = cause.Error()
	if err := s.store.putDocumentContext(ctx, failed); err != nil {
		return "", err
	}
	index.add(failed)
	return failed.ID, nil
}
