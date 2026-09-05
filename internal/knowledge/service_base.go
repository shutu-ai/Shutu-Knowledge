package knowledge

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/embedding"
	"github.com/shutu-ai/shutu-knowledge/internal/jobs"
	"github.com/shutu-ai/shutu-knowledge/internal/parser"
	"github.com/shutu-ai/shutu-knowledge/internal/rerank"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// ErrNotFound is returned for missing bases/documents.
var ErrNotFound = errors.New("not found")

// Service is the Knowledge Core facade: bases, documents, lifecycle.
type Service struct {
	store    *store
	raw      *storage.RawFileStore
	parsers  *parser.Registry
	global   config.Config
	jobMgr   *jobs.Manager
	embedder embedding.Provider
	reranker rerank.Provider
}

// NewService builds the service over the shared database.
func NewService(db *storage.DB, raw *storage.RawFileStore, global config.Config, jobMgr *jobs.Manager) *Service {
	service := &Service{store: newStore(db), raw: raw, parsers: parser.NewRegistry(), global: global, jobMgr: jobMgr}
	service.applyConfiguredProviders()
	return service
}

// applyConfiguredProviders builds providers from config (tests override via
// SetProviders).
func (s *Service) applyConfiguredProviders() {
	s.embedder = embedding.New(embedding.Config{
		Provider: s.global.Embedding.Provider,
		BaseURL:  s.global.Embedding.BaseURL,
		Model:    s.global.Embedding.Model,
		APIKey:   s.global.Embedding.APIKey,
	})
	if s.global.Rerank.Enabled && s.global.Rerank.Model != "" && s.global.Rerank.BaseURL != "" {
		s.reranker = rerank.New(rerank.Config{
			BaseURL: s.global.Rerank.BaseURL,
			Model:   s.global.Rerank.Model,
			APIKey:  s.global.Rerank.APIKey,
			Timeout: time.Duration(s.global.Rerank.TimeoutMS) * time.Millisecond,
		})
	}
}

// SetProviders overrides the model providers (tests, future local helpers).
func (s *Service) SetProviders(embedder embedding.Provider, reranker rerank.Provider) {
	if embedder != nil {
		s.embedder = embedder
	}
	if reranker != nil {
		s.reranker = reranker
	}
}

// EmbeddingModelKey reports the active vector-space identity ("" when none).
func (s *Service) EmbeddingModelKey() string {
	if s.global.Embedding.Provider == "none" {
		return ""
	}
	return s.embedder.ModelKey()
}

func now() int64 { return Now().UnixMilli() }

func newID() (string, error) { return jobs.NewID() }

// CreateBase creates a knowledge base.
func (s *Service) CreateBase(name, description, group string, cfg BaseConfig) (Base, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Base{}, fmt.Errorf("base name is required")
	}
	id, err := newID()
	if err != nil {
		return Base{}, err
	}
	base := Base{ID: id, Name: name, Description: description, Group: group, Config: cfg, CreatedAt: now(), UpdatedAt: now()}
	if err := s.store.putBase(base); err != nil {
		return Base{}, err
	}
	return base, nil
}

// RenameBase updates name/description/group/config.
func (s *Service) RenameBase(id string, name, description, group *string, cfg *BaseConfig) (Base, error) {
	base, err := s.store.getBase(id)
	if err != nil {
		return Base{}, err
	}
	if name != nil && strings.TrimSpace(*name) != "" {
		base.Name = strings.TrimSpace(*name)
	}
	if description != nil {
		base.Description = *description
	}
	if group != nil {
		base.Group = *group
	}
	if cfg != nil {
		base.Config = *cfg
	}
	base.UpdatedAt = now()
	if err := s.store.putBase(base); err != nil {
		return Base{}, err
	}
	return base, nil
}

// DeleteBase removes the base with its documents, chunks, and raw files.
func (s *Service) DeleteBase(id string) error {
	if _, err := s.store.getBase(id); err != nil {
		return err
	}
	docs, err := s.store.listDocuments(id)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if err := s.store.deleteChunks(doc.ID); err != nil {
			return err
		}
		if err := s.store.deleteDocument(doc.ID); err != nil {
			return err
		}
	}
	if err := s.store.deleteChunksByBase(id); err != nil {
		return err
	}
	if err := s.raw.DeleteBase(id); err != nil {
		return err
	}
	return s.store.deleteBase(id)
}

// GetBase returns one base.
func (s *Service) GetBase(id string) (Base, error) { return s.store.getBase(id) }

// ListBases returns summaries with document/chunk counts.
func (s *Service) ListBases() ([]BaseSummary, error) {
	bases, err := s.store.listBases()
	if err != nil {
		return nil, err
	}
	out := make([]BaseSummary, 0, len(bases))
	for _, base := range bases {
		stats, err := s.store.statsFor(base.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, BaseSummary{Base: base, DocumentCount: stats.DocumentCount, ChunkCount: stats.ChunkCount, CharCount: stats.CharCount, TokenCount: stats.TokenCount})
	}
	return out, nil
}

// Stats returns aggregate stats for a base or all bases.
func (s *Service) Stats(baseID string) (Stats, error) {
	stats, err := s.store.statsFor(baseID)
	if err != nil {
		return Stats{}, err
	}
	modelKey := s.EmbeddingModelKey()
	if modelKey == "" {
		return stats, nil
	}
	counts, err := s.store.VectorModelCounts(baseID)
	if err != nil {
		return Stats{}, err
	}
	embedded := 0
	stale := 0
	for model, count := range counts {
		embedded += count
		if model != modelKey {
			stale += count
		}
	}
	stats.Embedded = embedded > 0
	stats.StaleChunks = stale
	if dimensions, err := s.store.VectorDimensions(baseID); err == nil {
		stats.Dimensions = dimensions
	}
	return stats, nil
}

// ListGroups returns the persisted plus implicit group names.
func (s *Service) ListGroups() ([]string, error) {
	var out []string
	if err := s.kvGet("groups", &out); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(out))
	for _, g := range out {
		if g != "" && !seen[g] {
			seen[g] = true
			unique = append(unique, g)
		}
	}
	bases, err := s.store.listBases()
	if err != nil {
		return nil, err
	}
	for _, b := range bases {
		if b.Group != "" && !seen[b.Group] {
			seen[b.Group] = true
			unique = append(unique, b.Group)
		}
	}
	return unique, nil
}

// CreateGroup registers an empty group.
func (s *Service) CreateGroup(name string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("group name is required")
	}
	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if g == name {
			return groups, nil
		}
	}
	next := append(groups, name)
	return next, s.kvSet("groups", next)
}

// RenameGroup renames a group and every base inside it.
func (s *Service) RenameGroup(from, to string) ([]string, error) {
	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	next := make([]string, 0, len(groups))
	for _, g := range groups {
		if g == from {
			g = to
		}
		next = append(next, g)
	}
	bases, err := s.store.listBases()
	if err != nil {
		return nil, err
	}
	for _, b := range bases {
		if b.Group == from {
			b.Group = to
			b.UpdatedAt = now()
			if err := s.store.putBase(b); err != nil {
				return nil, err
			}
		}
	}
	return next, s.kvSet("groups", next)
}

// DeleteGroup removes a group; member bases become ungrouped.
func (s *Service) DeleteGroup(name string) error {
	groups, err := s.ListGroups()
	if err != nil {
		return err
	}
	next := make([]string, 0, len(groups))
	for _, g := range groups {
		if g != name {
			next = append(next, g)
		}
	}
	bases, err := s.store.listBases()
	if err != nil {
		return err
	}
	for _, b := range bases {
		if b.Group == name {
			b.Group = ""
			b.UpdatedAt = now()
			if err := s.store.putBase(b); err != nil {
				return err
			}
		}
	}
	return s.kvSet("groups", next)
}

// EnabledScope returns the invocation switch and the pinned base ids.
func (s *Service) EnabledScope() (bool, []string, error) {
	var scope struct {
		Enabled        bool     `json:"enabled"`
		EnabledBaseIDs []string `json:"enabledBaseIds"`
	}
	if !s.kvHas("scope") {
		return true, []string{}, nil
	}
	if err := s.kvGet("scope", &scope); err != nil {
		return false, nil, err
	}
	if scope.EnabledBaseIDs == nil {
		scope.EnabledBaseIDs = []string{}
	}
	return scope.Enabled, scope.EnabledBaseIDs, nil
}

// SetEnabledScope updates the invocation switch and/or pinned base ids.
func (s *Service) SetEnabledScope(enabled *bool, baseIDs *[]string) error {
	var scope struct {
		Enabled        bool     `json:"enabled"`
		EnabledBaseIDs []string `json:"enabledBaseIds"`
	}
	scope.Enabled = true
	if err := s.kvGet("scope", &scope); err != nil {
		return err
	}
	if enabled != nil {
		scope.Enabled = *enabled
	}
	if baseIDs != nil {
		scope.EnabledBaseIDs = *baseIDs
	}
	if scope.EnabledBaseIDs == nil {
		scope.EnabledBaseIDs = []string{}
	}
	return s.kvSet("scope", scope)
}

// ── key-value storage ────────────────────────────────────────────────────────

func (s *Service) kvGet(key string, out any) error {
	var value string
	err := s.store.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if value == "" {
		return nil
	}
	return json.Unmarshal([]byte(value), out)
}

func (s *Service) kvSet(key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.store.db.Exec(
		`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, string(data),
	)
	return err
}

// kvHas reports whether a key exists.
func (s *Service) kvHas(key string) bool {
	var value string
	err := s.store.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	return err == nil
}
