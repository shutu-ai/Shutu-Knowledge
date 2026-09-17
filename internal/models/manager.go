// Package models owns the local model cache. It downloads model artifacts,
// reports readiness, and removes releases atomically; inference itself stays
// in an isolated optional runtime.
package models

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/httpx"
)

// Kind identifies the runtime that consumes a model.
const (
	KindEmbedding = "embedding"
	KindRerank    = "rerank"
	KindOCR       = "ocr"
)

// Lifecycle values describe the complete model lifecycle, not just whether
// files are present. An artifact directory is never READY without a live
// runtime and a successful inference smoke test.
const (
	LifecycleNotInstalled = "NOT_INSTALLED"
	LifecycleDownloading  = "DOWNLOADING"
	LifecycleVerifying    = "VERIFYING"
	LifecycleInstalled    = "INSTALLED"
	LifecycleRuntimeMiss  = "RUNTIME_MISSING"
	LifecycleLoading      = "LOADING"
	LifecycleReady        = "READY"
	LifecycleFailed       = "FAILED"
)

// Default artifact sets. A custom Hugging Face repository can override them.
var defaultArtifacts = map[string][]string{
	KindEmbedding: {"config.json", "tokenizer.json", "tokenizer_config.json", "special_tokens_map.json", "model.onnx"},
	KindRerank:    {"config.json", "tokenizer.json", "tokenizer_config.json", "special_tokens_map.json", "model.onnx"},
}

// Model is a cached local model directory.
type Model struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Artifacts []string `json:"artifacts"`
	// Status is the artifact/cache status retained for the existing Models UI.
	// It must not be interpreted as runtime readiness.
	Status      string   `json:"status"` // installed | incomplete | not-downloaded
	Lifecycle   string   `json:"lifecycle"`
	Ready       bool     `json:"ready"`
	Runtime     string   `json:"runtimeStatus,omitempty"`
	RuntimePath string   `json:"runtimePath,omitempty"`
	LastError   string   `json:"lastError,omitempty"`
	Remediation string   `json:"remediation,omitempty"`
	SizeBytes   int64    `json:"sizeBytes"`
	Downloaded  int64    `json:"downloadedAt"`
	Missing     []string `json:"missing,omitempty"`
}

// DownloadRequest is one model download.
type DownloadRequest struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Artifacts []string `json:"artifacts,omitempty"`
}

// Manager serializes destructive cache operations and tracks active installs.
type Manager struct {
	root     string
	endpoint string
	client   *http.Client
	now      func() time.Time

	rootMu sync.RWMutex
	opMu   sync.Mutex
	mu     sync.Mutex
	active map[string]bool
}

// NewManager creates a manager under dir.
func NewManager(dir, hfEndpoint string, client *http.Client) *Manager {
	if client == nil {
		client = httpx.NewClient(30 * time.Minute)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(hfEndpoint), "/")
	if endpoint == "" {
		endpoint = "https://huggingface.co"
	}
	return &Manager{
		root: filepath.Clean(dir), endpoint: endpoint, client: client,
		now: time.Now, active: map[string]bool{},
	}
}

// Root returns the resolved cache directory.
func (m *Manager) Root() string {
	m.rootMu.RLock()
	defer m.rootMu.RUnlock()
	return m.root
}

func (m *Manager) currentRoot() string {
	m.rootMu.RLock()
	defer m.rootMu.RUnlock()
	return m.root
}

func (m *Manager) setRoot(root string) {
	m.rootMu.Lock()
	m.root = filepath.Clean(root)
	m.rootMu.Unlock()
}

func (m *Manager) hasActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active) > 0
}

// MigrationPlan describes a validated, non-destructive model-cache move.
type MigrationPlan struct {
	SourceDir string  `json:"sourceDir"`
	TargetDir string  `json:"targetDir"`
	Models    []Model `json:"models"`
	Bytes     int64   `json:"bytes"`
}

// MigrationResult is returned after the copied artifacts have been verified.
type MigrationResult struct {
	SourceDir    string `json:"sourceDir"`
	TargetDir    string `json:"targetDir"`
	ModelCount   int    `json:"modelCount"`
	Bytes        int64  `json:"bytes"`
	SourceRemove bool   `json:"sourceRemoved"`
}

// ModelPage is a bounded view of manifest-backed models. NextOffset is zero
// when HasMore is false; callers can request the next page without loading the
// whole cache into memory.
type ModelPage struct {
	Models     []Model `json:"models"`
	NextOffset int     `json:"nextOffset"`
	HasMore    bool    `json:"hasMore"`
}

const (
	defaultModelPageLimit = 100
	maxModelPageLimit     = 100
	maxModelPageOffset    = 1_000_000
)

var errModelPageComplete = errors.New("model page complete")

// List returns every model directory for legacy callers. New control-plane
// paths should use ListPage so a large cache cannot become an unbounded HTTP
// response or allocation.
func (m *Manager) List() ([]Model, error) {
	return m.ListContext(context.Background())
}

// ListContext returns every manifest-backed model while allowing a maintenance
// operation to stop a large cache walk before the next directory entry.
func (m *Manager) ListContext(ctx context.Context) ([]Model, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root := m.currentRoot()
	var out []Model
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "manifest.json" {
			relative, relErr := filepath.Rel(root, filepath.Dir(path))
			if relErr != nil {
				return relErr
			}
			model, err := m.inspectAt(root, filepath.ToSlash(relative))
			if err != nil {
				return err
			}
			out = append(out, model)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return []Model{}, nil
	}
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Model{}
	}
	return out, nil
}

// CountContext counts manifest-backed models without decoding every manifest
// or retaining the full catalog in memory. It is intended for maintenance
// results that only need a cardinality, not the model picker view.
func (m *Manager) CountContext(ctx context.Context) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root := m.currentRoot()
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "manifest.json" {
			count++
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (m *Manager) inspect(id string) (Model, error) {
	return m.inspectContext(context.Background(), id)
}

func (m *Manager) inspectContext(ctx context.Context, id string) (Model, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Model{}, err
	}
	model, err := m.inspectAt(m.currentRoot(), id)
	if err != nil {
		return Model{}, err
	}
	if err := ctx.Err(); err != nil {
		return Model{}, err
	}
	return model, nil
}

func (m *Manager) inspectAt(root, id string) (Model, error) {
	dir, err := m.modelDirAt(root, id)
	if err != nil {
		return Model{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return Model{}, err
	}
	var model Model
	if err := json.Unmarshal(data, &model); err != nil {
		return Model{}, fmt.Errorf("decode model manifest %s: %w", id, err)
	}
	if model.ID == "" || model.Kind == "" {
		return Model{}, fmt.Errorf("model manifest %s is malformed", id)
	}
	for _, artifact := range model.Artifacts {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(artifact)))
		if err != nil || info.IsDir() || info.Size() == 0 {
			model.Missing = append(model.Missing, artifact)
			continue
		}
		model.SizeBytes += info.Size()
	}
	model.Status = "installed"
	model.Lifecycle = LifecycleInstalled
	model.Runtime = LifecycleRuntimeMiss
	if len(model.Missing) > 0 {
		model.Status = "incomplete"
		model.Lifecycle = LifecycleFailed
		model.LastError = "one or more model artifacts are missing or empty"
	}
	return model, nil
}

// ListPage returns a bounded, deterministic page of local models. WalkDir
// visits directory entries in lexical order; the sentinel stops after the
// first item beyond the requested page.
func (m *Manager) ListPage(limit, offset int) (ModelPage, error) {
	return m.ListPageContext(context.Background(), limit, offset)
}

// ListPageContext returns a bounded model page while honoring cancellation
// during the cache walk and manifest inspection.
func (m *Manager) ListPageContext(ctx context.Context, limit, offset int) (ModelPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = defaultModelPageLimit
	}
	if limit > maxModelPageLimit {
		limit = maxModelPageLimit
	}
	if offset < 0 || offset > maxModelPageOffset {
		return ModelPage{}, fmt.Errorf("model page offset is out of range")
	}
	root := m.currentRoot()
	page := ModelPage{Models: make([]Model, 0, limit)}
	skipped := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "manifest.json" {
			if skipped < offset {
				skipped++
				return nil
			}
			if len(page.Models) >= limit {
				page.HasMore = true
				return errModelPageComplete
			}
			relative, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			model, err := m.inspectAt(root, filepath.ToSlash(relative))
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			page.Models = append(page.Models, model)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return page, nil
	}
	if err != nil && !errors.Is(err, errModelPageComplete) {
		return ModelPage{}, err
	}
	if page.HasMore {
		page.NextOffset = offset + len(page.Models)
	}
	return page, nil
}

// Get returns one manifest-backed model.
func (m *Manager) Get(id string) (Model, error) {
	return m.GetContext(context.Background(), id)
}

// GetContext reads one manifest-backed model while honoring a caller's
// cancellation boundary around the filesystem inspection.
func (m *Manager) GetContext(ctx context.Context, id string) (Model, error) {
	return m.inspectContext(ctx, id)
}

// Download installs the requested Hugging Face repository into a temporary
// directory, then publishes it atomically. Progress is whole-percentage.
func (m *Manager) Download(ctx context.Context, req DownloadRequest, report func(progress int)) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	req.ID = strings.TrimSpace(req.ID)
	req.Kind = strings.TrimSpace(req.Kind)
	if req.ID == "" {
		return fmt.Errorf("model id is required")
	}
	if req.Kind != KindEmbedding && req.Kind != KindRerank && req.Kind != KindOCR {
		return fmt.Errorf("unsupported model kind %q", req.Kind)
	}
	artifacts := req.Artifacts
	if len(artifacts) == 0 {
		artifacts = defaultArtifacts[req.Kind]
	}
	if len(artifacts) == 0 {
		return fmt.Errorf("model artifacts are required")
	}

	m.mu.Lock()
	if m.active[req.ID] {
		m.mu.Unlock()
		return fmt.Errorf("model %s is already downloading", req.ID)
	}
	m.active[req.ID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.active, req.ID)
		m.mu.Unlock()
	}()

	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return err
	}
	dir, err := m.modelDir(req.ID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("model %s already exists", req.ID)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(m.root, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)

	for _, artifact := range artifacts {
		if err := m.downloadArtifact(ctx, req.ID, artifact, temp); err != nil {
			return err
		}
	}
	manifest := Model{ID: req.ID, Kind: req.Kind, Artifacts: artifacts, Downloaded: m.now().UnixMilli()}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(temp, "manifest.json"), data, 0o600); err != nil {
		return err
	}
	_ = os.Remove(dir)
	if err := os.Rename(temp, dir); err != nil {
		return err
	}
	report(100)
	return nil
}

func (m *Manager) downloadArtifact(ctx context.Context, modelID, artifact, dir string) error {
	clean := pathClean(artifact)
	if clean == "" {
		return fmt.Errorf("invalid artifact %q", artifact)
	}
	artifactURL, err := url.Parse(clean)
	if err != nil || artifactURL.IsAbs() {
		return fmt.Errorf("invalid artifact %q", artifact)
	}
	endpoint, err := url.Parse(m.endpoint)
	if err != nil {
		return err
	}
	relative, err := url.Parse(escapePath(modelID) + "/resolve/main/" + escapePath(clean))
	if err != nil {
		return err
	}
	requestURL := endpoint.ResolveReference(relative)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", artifact, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", artifact, resp.StatusCode)
	}
	target := filepath.Join(dir, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("download %s: %w", artifact, err)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return nil
}

// Remove deletes an installed model. Active downloads are never removed.
func (m *Manager) Remove(id string) error {
	return m.RemoveContext(context.Background(), id)
}

// RemoveContext deletes a model tree while honoring cancellation between
// filesystem operations. A canceled or failed delete may leave an incomplete
// tree; a later retry safely removes the remaining entries and the operation
// layer treats an already absent tree as success.
func (m *Manager) RemoveContext(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := m.modelDir(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.active[id] {
		m.mu.Unlock()
		return fmt.Errorf("model %s is downloading", id)
	}
	m.mu.Unlock()
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: model %s", os.ErrNotExist, id)
		}
		return err
	}
	return removeTreeContext(ctx, dir)
}

func removeTreeContext(ctx context.Context, root string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			if err := removeTreeContext(ctx, path); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(root); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// PlanMigration validates a complete move to an empty directory. It never
// creates the target and never changes the active cache.
func (m *Manager) PlanMigration(target string) (MigrationPlan, error) {
	return m.PlanMigrationContext(context.Background(), target)
}

// PlanMigrationContext performs the potentially large validation scan under a
// caller-owned context. The durable plan operation uses this boundary so a
// cancelled request does not leave a worker walking an abandoned cache.
func (m *Manager) PlanMigrationContext(ctx context.Context, target string) (MigrationPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return MigrationPlan{}, err
	}
	source := m.currentRoot()
	cleanTarget, err := migrationTarget(source, target)
	if err != nil {
		return MigrationPlan{}, err
	}
	if m.hasActive() {
		return MigrationPlan{}, fmt.Errorf("model download is active")
	}
	models, err := m.ListContext(ctx)
	if err != nil {
		return MigrationPlan{}, err
	}
	var total int64
	for _, model := range models {
		if err := ctx.Err(); err != nil {
			return MigrationPlan{}, err
		}
		modelDir, err := m.modelDirAt(source, model.ID)
		if err != nil {
			return MigrationPlan{}, err
		}
		size, err := treeSizeContext(ctx, modelDir)
		if err != nil {
			return MigrationPlan{}, err
		}
		total += size
	}
	return MigrationPlan{SourceDir: source, TargetDir: cleanTarget, Models: models, Bytes: total}, nil
}

func treeSize(root string) (int64, error) {
	return treeSizeContext(context.Background(), root)
}

func treeSizeContext(ctx context.Context, root string) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// Migrate copies every manifest-owned model to an empty target, verifies each
// copied file with SHA-256, optionally removes the source models, and only
// then activates the target. Source removal is always explicit.
func (m *Manager) Migrate(target string, removeSource bool) (MigrationResult, error) {
	return m.MigrateContext(context.Background(), target, removeSource, nil)
}

// MigrateContext is the replayable migration boundary used by Durable
// Operations. The target is fully copied and verified before beforeCommit is
// called. Callers can persist the new authoritative configuration in that
// callback; if it fails, the target is removed and the source remains active.
// Once the callback succeeds, source removal (when requested) and activation
// complete the migration without another externally observable decision.
func (m *Manager) MigrateContext(ctx context.Context, target string, removeSource bool, beforeCommit func() error) (MigrationResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return MigrationResult{}, err
	}
	source := m.currentRoot()
	cleanTarget, err := migrationTarget(source, target)
	if err != nil {
		return MigrationResult{}, err
	}
	if m.hasActive() {
		return MigrationResult{}, fmt.Errorf("model download is active")
	}
	plan, err := m.PlanMigrationContext(ctx, cleanTarget)
	if err != nil {
		return MigrationResult{}, err
	}
	if err := os.MkdirAll(cleanTarget, 0o700); err != nil {
		return MigrationResult{}, fmt.Errorf("create target cache: %w", err)
	}
	entries, err := os.ReadDir(cleanTarget)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("inspect target cache: %w", err)
	}
	if len(entries) != 0 {
		return MigrationResult{}, fmt.Errorf("target model cache is not empty")
	}
	var copied int64
	for _, model := range plan.Models {
		if err := ctx.Err(); err != nil {
			_ = os.RemoveAll(cleanTarget)
			return MigrationResult{}, err
		}
		sourceDir, err := m.modelDirAt(source, model.ID)
		if err != nil {
			_ = os.RemoveAll(cleanTarget)
			return MigrationResult{}, err
		}
		targetDir := filepath.Join(cleanTarget, filepath.FromSlash(model.ID))
		hashes, bytes, err := copyTreeContext(ctx, sourceDir, targetDir)
		if err != nil {
			_ = os.RemoveAll(cleanTarget)
			return MigrationResult{}, err
		}
		copied += bytes
		if err := verifyTreeContext(ctx, targetDir, hashes); err != nil {
			_ = os.RemoveAll(cleanTarget)
			return MigrationResult{}, err
		}
	}
	if copied != plan.Bytes {
		_ = os.RemoveAll(cleanTarget)
		return MigrationResult{}, fmt.Errorf("copied %d bytes, expected %d", copied, plan.Bytes)
	}
	if err := ctx.Err(); err != nil {
		_ = os.RemoveAll(cleanTarget)
		return MigrationResult{}, err
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			_ = os.RemoveAll(cleanTarget)
			return MigrationResult{}, err
		}
	}
	if removeSource {
		for _, model := range plan.Models {
			sourceDir, err := m.modelDirAt(source, model.ID)
			if err != nil {
				return MigrationResult{}, err
			}
			if err := os.RemoveAll(sourceDir); err != nil {
				return MigrationResult{}, fmt.Errorf("remove source model %s: %w", model.ID, err)
			}
			if err := removeEmptyDirs(source, filepath.Dir(sourceDir)); err != nil {
				return MigrationResult{}, err
			}
		}
	}
	if removeSource {
		if entries, err := os.ReadDir(source); err == nil && len(entries) == 0 {
			_ = os.Remove(source)
		}
	}
	m.setRoot(cleanTarget)
	return MigrationResult{
		SourceDir: source, TargetDir: cleanTarget,
		ModelCount: len(plan.Models), Bytes: copied, SourceRemove: removeSource,
	}, nil
}

func removeEmptyDirs(root, start string) error {
	root = filepath.Clean(root)
	for path := filepath.Clean(start); containsPath(root, path) || samePath(root, path); path = filepath.Dir(path) {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return nil
		}
		if samePath(path, root) {
			return nil
		}
	}
	return nil
}

func migrationTarget(source, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("target model cache directory is required")
	}
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("target model cache directory must be absolute")
	}
	source = filepath.Clean(source)
	target = filepath.Clean(target)
	if filepath.Clean(target) == filepath.Clean(source) || samePath(target, source) {
		return "", fmt.Errorf("target model cache is already active")
	}
	if containsPath(source, target) {
		return "", fmt.Errorf("target model cache is inside the active cache")
	}
	if containsPath(target, source) {
		return "", fmt.Errorf("active model cache is inside the target cache")
	}
	return target, nil
}

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func containsPath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	separator := string(os.PathSeparator)
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(child), strings.ToLower(parent)+separator)
	}
	return strings.HasPrefix(child, parent+separator)
}

func copyTree(source, target string) (map[string][sha256.Size]byte, int64, error) {
	return copyTreeContext(context.Background(), source, target)
}

func copyTreeContext(ctx context.Context, source, target string) (map[string][sha256.Size]byte, int64, error) {
	hashes := map[string][sha256.Size]byte{}
	var total int64
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		targetPath := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported non-regular model file %s", relative)
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
			return err
		}
		sourceFile, err := os.Open(path)
		if err != nil {
			return err
		}
		targetFile, err := os.Create(targetPath)
		if err != nil {
			_ = sourceFile.Close()
			return err
		}
		digest := sha256.New()
		size, err := io.Copy(io.MultiWriter(targetFile, digest), contextReader{ctx: ctx, reader: sourceFile})
		sourceCloseErr := sourceFile.Close()
		targetCloseErr := error(nil)
		if err != nil {
			_ = targetFile.Close()
			return fmt.Errorf("copy %s: %w", relative, err)
		}
		if err := targetFile.Sync(); err != nil {
			_ = targetFile.Close()
			return err
		}
		targetCloseErr = targetFile.Close()
		if sourceCloseErr != nil {
			return sourceCloseErr
		}
		if targetCloseErr != nil {
			return targetCloseErr
		}
		if err := os.Chmod(targetPath, info.Mode().Perm()); err != nil {
			return err
		}
		var hash [sha256.Size]byte
		copy(hash[:], digest.Sum(nil))
		hashes[filepath.ToSlash(relative)] = hash
		total += size
		return nil
	})
	return hashes, total, err
}

func verifyTree(root string, hashes map[string][sha256.Size]byte) error {
	return verifyTreeContext(context.Background(), root, hashes)
}

func verifyTreeContext(ctx context.Context, root string, hashes map[string][sha256.Size]byte) error {
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		want, ok := hashes[key]
		if !ok {
			return fmt.Errorf("unexpected copied file %s", key)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		digest := sha256.New()
		if _, err := io.Copy(digest, contextReader{ctx: ctx, reader: file}); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		var sum [sha256.Size]byte
		copy(sum[:], digest.Sum(nil))
		if sum != want {
			return fmt.Errorf("copied model hash mismatch: %s", key)
		}
		seen[key] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(hashes) {
		return fmt.Errorf("copied model is incomplete")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Active reports whether a download is in progress.
func (m *Manager) Active(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[id]
}

func (m *Manager) modelDir(id string) (string, error) {
	return m.modelDirAt(m.currentRoot(), id)
}

func (m *Manager) modelDirAt(root, id string) (string, error) {
	clean := pathClean(id)
	if clean == "" || clean != strings.TrimSpace(id) {
		return "", fmt.Errorf("invalid model id %q", id)
	}
	dir := filepath.Join(root, filepath.FromSlash(clean))
	cleanRoot := filepath.Clean(root)
	if dir == cleanRoot || !strings.HasPrefix(dir, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("model path escapes the cache")
	}
	return dir, nil
}

func pathClean(value string) string {
	value = filepath.ToSlash(strings.Trim(strings.TrimSpace(value), "/"))
	parts := make([]string, 0, 3)
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return ""
		}
		if strings.ContainsAny(part, "?#\\") {
			return ""
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "/")
}

func escapePath(value string) string {
	parts := strings.Split(value, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
