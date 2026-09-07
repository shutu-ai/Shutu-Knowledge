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

// List returns every model directory. A directory without a complete
// manifest is reported as incomplete rather than silently ignored.
func (m *Manager) List() ([]Model, error) {
	var out []Model
	err := filepath.WalkDir(m.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == "manifest.json" {
			relative, relErr := filepath.Rel(m.root, filepath.Dir(path))
			if relErr != nil {
				return relErr
			}
			model, err := m.inspect(filepath.ToSlash(relative))
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

func (m *Manager) inspect(id string) (Model, error) {
	dir, err := m.modelDir(id)
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

// Get returns one manifest-backed model.
func (m *Manager) Get(id string) (Model, error) {
	return m.inspect(id)
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
	m.opMu.Lock()
	defer m.opMu.Unlock()
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
	return os.RemoveAll(dir)
}

// PlanMigration validates a complete move to an empty directory. It never
// creates the target and never changes the active cache.
func (m *Manager) PlanMigration(target string) (MigrationPlan, error) {
	source := m.currentRoot()
	cleanTarget, err := migrationTarget(source, target)
	if err != nil {
		return MigrationPlan{}, err
	}
	if m.hasActive() {
		return MigrationPlan{}, fmt.Errorf("model download is active")
	}
	models, err := m.List()
	if err != nil {
		return MigrationPlan{}, err
	}
	var total int64
	for _, model := range models {
		modelDir, err := m.modelDir(model.ID)
		if err != nil {
			return MigrationPlan{}, err
		}
		size, err := treeSize(modelDir)
		if err != nil {
			return MigrationPlan{}, err
		}
		total += size
	}
	return MigrationPlan{SourceDir: source, TargetDir: cleanTarget, Models: models, Bytes: total}, nil
}

func treeSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
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
	m.opMu.Lock()
	defer m.opMu.Unlock()
	source := m.currentRoot()
	cleanTarget, err := migrationTarget(source, target)
	if err != nil {
		return MigrationResult{}, err
	}
	if m.hasActive() {
		return MigrationResult{}, fmt.Errorf("model download is active")
	}
	plan, err := m.PlanMigration(cleanTarget)
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
		sourceDir, err := m.modelDir(model.ID)
		if err != nil {
			return MigrationResult{}, err
		}
		targetDir := filepath.Join(cleanTarget, filepath.FromSlash(model.ID))
		hashes, bytes, err := copyTree(sourceDir, targetDir)
		if err != nil {
			return MigrationResult{}, err
		}
		copied += bytes
		if err := verifyTree(targetDir, hashes); err != nil {
			return MigrationResult{}, err
		}
		if removeSource {
			if err := os.RemoveAll(sourceDir); err != nil {
				return MigrationResult{}, fmt.Errorf("remove source model %s: %w", model.ID, err)
			}
			if err := removeEmptyDirs(source, filepath.Dir(sourceDir)); err != nil {
				return MigrationResult{}, err
			}
		}
	}
	if copied != plan.Bytes {
		return MigrationResult{}, fmt.Errorf("copied %d bytes, expected %d", copied, plan.Bytes)
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
	hashes := map[string][sha256.Size]byte{}
	var total int64
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
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
		defer sourceFile.Close()
		targetFile, err := os.Create(targetPath)
		if err != nil {
			return err
		}
		defer targetFile.Close()
		digest := sha256.New()
		size, err := io.Copy(io.MultiWriter(targetFile, digest), sourceFile)
		if err != nil {
			return fmt.Errorf("copy %s: %w", relative, err)
		}
		if err := targetFile.Sync(); err != nil {
			return err
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
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
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
		if _, err := io.Copy(digest, file); err != nil {
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

// Active reports whether a download is in progress.
func (m *Manager) Active(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[id]
}

func (m *Manager) modelDir(id string) (string, error) {
	clean := pathClean(id)
	if clean == "" || clean != strings.TrimSpace(id) {
		return "", fmt.Errorf("invalid model id %q", id)
	}
	dir := filepath.Join(m.root, filepath.FromSlash(clean))
	cleanRoot := filepath.Clean(m.root)
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
