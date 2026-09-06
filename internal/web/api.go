package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/models"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

// registerKnowledgeAPI mounts the Knowledge REST surface on the mux.
func registerKnowledgeAPI(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/bases", s.listBases)
	mux.HandleFunc("POST /api/bases", s.createBase)
	mux.HandleFunc("GET /api/bases/{id}", s.getBase)
	mux.HandleFunc("PATCH /api/bases/{id}", s.patchBase)
	mux.HandleFunc("DELETE /api/bases/{id}", s.deleteBase)
	mux.HandleFunc("GET /api/bases/{id}/stats", s.baseStats)
	mux.HandleFunc("GET /api/bases/{id}/name", s.baseName)
	mux.HandleFunc("POST /api/bases/{id}/restore", s.restoreBase)
	mux.HandleFunc("GET /api/bases/{id}/documents", s.listDocuments)
	mux.HandleFunc("POST /api/bases/{id}/documents", s.addDocument)
	mux.HandleFunc("POST /api/bases/{id}/files", s.addFiles)
	mux.HandleFunc("POST /api/bases/{id}/reindex", s.reindexBase)
	mux.HandleFunc("POST /api/bases/{id}/url", s.addURLDocument)
	mux.HandleFunc("POST /api/bases/{id}/import-directory", s.importDirectory)
	mux.HandleFunc("POST /api/bases/{id}/directories", s.createDirectory)
	mux.HandleFunc("POST /api/documents/{id}/refresh", s.refreshDocument)
	mux.HandleFunc("POST /api/documents/{id}/rescan", s.rescanDirectory)
	mux.HandleFunc("POST /api/documents/{id}/source-path", s.repointSource)
	mux.HandleFunc("GET /api/documents/{id}", s.getDocument)
	mux.HandleFunc("PATCH /api/documents/{id}", s.patchDocument)
	mux.HandleFunc("DELETE /api/documents/{id}", s.deleteDocument)
	mux.HandleFunc("POST /api/documents/delete", s.deleteDocuments)
	mux.HandleFunc("POST /api/documents/reindex", s.reindexDocuments)
	mux.HandleFunc("POST /api/documents/{id}/delete-tree", s.deleteDocumentTree)
	mux.HandleFunc("GET /api/documents/{id}/chunks", s.listChunks)
	mux.HandleFunc("GET /api/documents/{id}/raw", s.rawDocument)
	mux.HandleFunc("POST /api/documents/{id}/reindex", s.reindexOne)
	mux.HandleFunc("GET /api/jobs/{id}", s.jobStatus)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.jobCancel)
	mux.HandleFunc("GET /api/groups", s.listGroups)
	mux.HandleFunc("POST /api/groups", s.createGroup)
	mux.HandleFunc("PATCH /api/groups", s.renameGroup)
	mux.HandleFunc("DELETE /api/groups", s.deleteGroup)
	mux.HandleFunc("GET /api/scope", s.getScope)
	mux.HandleFunc("PUT /api/scope", s.putScope)
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("PUT /api/config", s.putConfig)
	mux.HandleFunc("GET /api/model-suggestions", s.modelSuggestions)
	mux.HandleFunc("POST /api/probe-embedding-dimensions", s.probeEmbeddingDimensions)
	mux.HandleFunc("GET /api/indexing-status", s.indexingStatus)
	mux.HandleFunc("GET /api/local-models", s.listLocalModels)
	mux.HandleFunc("GET /api/ocr/model", s.ocrModel)
	mux.HandleFunc("POST /api/ocr/model/download", s.downloadOCRModel)
	mux.HandleFunc("POST /api/ocr/model/remove", s.removeOCRModel)
	mux.HandleFunc("GET /api/runtime-status", s.runtimeStatus)
	mux.HandleFunc("POST /api/local-models/download", s.downloadLocalModel)
	mux.HandleFunc("POST /api/local-models/remove", s.removeLocalModel)
	mux.HandleFunc("POST /api/local-rerankers", s.registerLocalReranker)
	mux.HandleFunc("POST /api/local-models/self-test", s.selfTestLocalReranker)
	mux.HandleFunc("GET /api/local-models/cache-migration", s.planModelCacheMigration)
	mux.HandleFunc("POST /api/local-models/cache-migration", s.migrateModelCache)
	mux.HandleFunc("GET /api/ollama/models", s.listOllamaModels)
	mux.HandleFunc("POST /api/ollama/pull", s.pullOllamaModel)
	mux.HandleFunc("POST /api/ollama/delete", s.deleteOllamaModel)
	mux.HandleFunc("GET /api/stats", s.globalStats)
	mux.HandleFunc("GET /api/metrics", s.metrics)
	mux.HandleFunc("POST /api/search", s.search)
	mux.HandleFunc("GET /api/search-history", s.listSearchHistory)
	mux.HandleFunc("DELETE /api/search-history", s.clearSearchHistory)
	mux.HandleFunc("DELETE /api/search-history/{id}", s.deleteSearchHistory)
	mux.HandleFunc("GET /api/documents/{id}/context", s.documentContext)
}

func writeErr(w http.ResponseWriter, err error) {
	var conflict *knowledge.ConflictError
	switch {
	case errors.As(err, &conflict):
		writeJSON(w, http.StatusConflict, map[string]any{
			"ok": false, "error": map[string]any{"code": "conflict", "conflicts": conflict.Conflicts, "message": err.Error()},
		})
	case errors.Is(err, knowledge.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": map[string]string{"code": "not-found", "message": err.Error()}})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": map[string]string{"code": "error", "message": err.Error()}})
	}
}

func writeOK(w http.ResponseWriter, value any) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "value": value})
}

func redactedBase(base knowledge.Base) knowledge.Base {
	base.Config = base.Config.Redacted()
	return base
}

func decodeBody[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var body T
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, err)
		return body, false
	}
	return body, true
}

func (s *Server) listBases(w http.ResponseWriter, _ *http.Request) {
	bases, err := s.app.Knowledge.ListBases()
	if err != nil {
		writeErr(w, err)
		return
	}
	for index := range bases {
		bases[index].Base.Config = bases[index].Base.Config.Redacted()
	}
	writeOK(w, bases)
}

type createBaseRequest struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Group       string               `json:"group"`
	Config      knowledge.BaseConfig `json:"config"`
}

func (s *Server) createBase(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[createBaseRequest](w, r)
	if !ok {
		return
	}
	base, err := s.app.Knowledge.CreateBase(body.Name, body.Description, body.Group, body.Config)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, redactedBase(base))
}

func (s *Server) getBase(w http.ResponseWriter, r *http.Request) {
	base, err := s.app.Knowledge.GetBase(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, redactedBase(base))
}

type patchBaseRequest struct {
	Name        *string               `json:"name"`
	Description *string               `json:"description"`
	Group       *string               `json:"group"`
	Config      *knowledge.BaseConfig `json:"config"`
}

func (s *Server) patchBase(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[patchBaseRequest](w, r)
	if !ok {
		return
	}
	if body.Config != nil {
		current, err := s.app.Knowledge.GetBase(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		config := *body.Config
		config.EmbeddingAPIKeySet = false
		config.RerankAPIKeySet = false
		config.MineruAPIKeySet = false
		if config.ClearEmbeddingAPIKey {
			config.EmbeddingAPIKey = ""
		} else if config.EmbeddingAPIKey == "" {
			config.EmbeddingAPIKey = current.Config.EmbeddingAPIKey
		}
		if config.ClearRerankAPIKey {
			config.RerankAPIKey = ""
		} else if config.RerankAPIKey == "" {
			config.RerankAPIKey = current.Config.RerankAPIKey
		}
		if config.ClearMineruAPIKey {
			config.MineruAPIKey = ""
		} else if config.MineruAPIKey == "" {
			config.MineruAPIKey = current.Config.MineruAPIKey
		}
		config.ClearEmbeddingAPIKey = false
		config.ClearRerankAPIKey = false
		config.ClearMineruAPIKey = false
		body.Config = &config
	}
	base, err := s.app.Knowledge.RenameBase(r.PathValue("id"), body.Name, body.Description, body.Group, body.Config)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, redactedBase(base))
}

func (s *Server) deleteBase(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Knowledge.DeleteBase(r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]bool{"deleted": true})
}

func (s *Server) baseStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.app.Knowledge.Stats(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, stats)
}

func (s *Server) baseName(w http.ResponseWriter, r *http.Request) {
	base, err := s.app.Knowledge.GetBase(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"id": base.ID, "name": base.Name, "group": base.Group})
}

func (s *Server) globalStats(w http.ResponseWriter, _ *http.Request) {
	stats, err := s.app.Knowledge.Stats("")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, stats)
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	writeOK(w, s.app.Knowledge.Metrics())
}

type restoreBaseRequest struct {
	Name   string                `json:"name"`
	Config *knowledge.BaseConfig `json:"config"`
}

func (s *Server) restoreBase(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[restoreBaseRequest](w, r)
	if !ok {
		return
	}
	base, err := s.app.Knowledge.RestoreBase(r.Context(), r.PathValue("id"), body.Name, body.Config)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, redactedBase(base))
}

func (s *Server) indexingStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := s.app.Knowledge.IndexingStatus()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, status)
}

type probeEmbeddingRequest struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
}

func (s *Server) probeEmbeddingDimensions(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[probeEmbeddingRequest](w, r)
	if !ok {
		return
	}
	dimensions, err := s.app.Knowledge.ProbeEmbeddingDimensions(r.Context(), body.Provider, body.BaseURL, body.Model, body.APIKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]int{"dimensions": dimensions})
}

func (s *Server) rawDocument(w http.ResponseWriter, r *http.Request) {
	raw, err := s.app.Knowledge.GetRawFile(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	contentType := raw.MimeType
	if strings.TrimSpace(contentType) == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(raw.FileName)))
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/octet-stream"
	}
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition+`; filename="download"; filename*=UTF-8''`+url.PathEscape(raw.FileName))
	w.Header().Set("Content-Length", strconv.Itoa(len(raw.Bytes)))
	_, _ = w.Write(raw.Bytes)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[knowledge.SearchRequest](w, r)
	if !ok {
		return
	}
	result, err := s.app.Knowledge.Search(r.Context(), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Recall Test owns its replay history. Extension tool and automatic-RAG
	// searches call Knowledge.Search directly and are intentionally excluded.
	if _, err := s.app.Knowledge.SaveSearchHistory(body, result); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (s *Server) listSearchHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 20
	}
	items, err := s.app.Knowledge.ListSearchHistory(limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, items)
}

func (s *Server) deleteSearchHistory(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Knowledge.DeleteSearchHistory(r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]bool{"deleted": true})
}

func (s *Server) clearSearchHistory(w http.ResponseWriter, _ *http.Request) {
	if err := s.app.Knowledge.DeleteSearchHistory(""); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]bool{"cleared": true})
}

func (s *Server) documentContext(w http.ResponseWriter, r *http.Request) {
	opts := knowledge.ContextOptions{
		AnchorChunkID: r.URL.Query().Get("anchorChunkId"),
		Focus:         r.URL.Query().Get("focus"),
		CrossHeading:  r.URL.Query().Get("crossHeading") == "1",
	}
	if raw := r.URL.Query().Get("anchorIndex"); raw != "" {
		if index, err := strconv.Atoi(raw); err == nil {
			opts.AnchorIndex = &index
		}
	}
	if raw := r.URL.Query().Get("before"); raw != "" {
		if before, err := strconv.Atoi(raw); err == nil {
			opts.Before = &before
		}
	}
	if raw := r.URL.Query().Get("after"); raw != "" {
		if after, err := strconv.Atoi(raw); err == nil {
			opts.After = &after
		}
	}
	if raw := r.URL.Query().Get("maxTokens"); raw != "" {
		if tokens, err := strconv.Atoi(raw); err == nil {
			opts.MaxTokens = tokens
		}
	}
	window, err := s.app.Knowledge.GetDocumentContext(r.PathValue("id"), opts)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, window)
}

func (s *Server) listDocuments(w http.ResponseWriter, r *http.Request) {
	docs, err := s.app.Knowledge.ListDocuments(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, docs)
}

type addDocumentRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	URL     string `json:"url"`
}

func (s *Server) addDocument(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[addDocumentRequest](w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeErr(w, errors.New("content is required (url imports arrive in the ingestion phase)"))
		return
	}
	doc, err := s.app.Knowledge.AddTextDocument(r.Context(), r.PathValue("id"), body.Title, body.Content)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, doc)
}

type addFilesRequest struct {
	Files       []knowledge.AddFilesItem `json:"files"`
	Conflict    string                   `json:"conflict"`
	ParentDirID string                   `json:"parentDirectoryId"`
}

func (s *Server) addFiles(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[addFilesRequest](w, r)
	if !ok {
		return
	}
	result, err := s.app.Knowledge.AddFiles(r.Context(), r.PathValue("id"), body.Files, body.Conflict, body.ParentDirID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if result.Status == "conflicts" {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": map[string]any{"code": "conflict", "conflicts": result.Conflicts}})
		return
	}
	writeOK(w, result)
}

func (s *Server) reindexBase(w http.ResponseWriter, r *http.Request) {
	jobID, err := s.app.Knowledge.ReindexBase(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]string{"jobId": jobID})
}

type addURLRequest struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

func (s *Server) addURLDocument(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[addURLRequest](w, r)
	if !ok {
		return
	}
	doc, err := s.app.Knowledge.AddUrlDocument(r.Context(), r.PathValue("id"), body.URL, body.Title)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, doc)
}

type importDirectoryRequest struct {
	Path string `json:"path"`
}

func (s *Server) importDirectory(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[importDirectoryRequest](w, r)
	if !ok {
		return
	}
	jobID, err := s.app.Knowledge.ImportDirectoryTree(r.Context(), r.PathValue("id"), body.Path)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"jobId": jobID, "submitted": true})
}

type createDirectoryRequest struct {
	Title             string `json:"title"`
	ParentDirectoryID string `json:"parentDirectoryId"`
	SourcePath        string `json:"sourcePath"`
}

func (s *Server) createDirectory(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[createDirectoryRequest](w, r)
	if !ok {
		return
	}
	doc, err := s.app.Knowledge.CreateDirectory(r.PathValue("id"), body.Title, body.ParentDirectoryID, body.SourcePath)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, doc)
}

func (s *Server) rescanDirectory(w http.ResponseWriter, r *http.Request) {
	jobID, err := s.app.Knowledge.RescanDirectory(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"jobId": jobID, "submitted": true})
}

type repointSourceRequest struct {
	Path string `json:"path"`
}

func (s *Server) repointSource(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[repointSourceRequest](w, r)
	if !ok {
		return
	}
	doc, err := s.app.Knowledge.RepointSource(r.PathValue("id"), body.Path)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, doc)
}

func (s *Server) refreshDocument(w http.ResponseWriter, r *http.Request) {
	changed, doc, err := s.app.Knowledge.RefreshUrlDocument(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"changed": changed, "document": doc})
}

func (s *Server) getDocument(w http.ResponseWriter, r *http.Request) {
	includeChunks := r.URL.Query().Get("includeChunks") != "false"
	doc, chunks, err := s.app.Knowledge.GetDocument(r.PathValue("id"), includeChunks)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !includeChunks {
		writeOK(w, doc)
		return
	}
	writeOK(w, map[string]any{"document": doc, "chunks": chunks})
}

type patchDocumentRequest struct {
	Title string `json:"title"`
}

func (s *Server) patchDocument(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[patchDocumentRequest](w, r)
	if !ok {
		return
	}
	doc, err := s.app.Knowledge.RenameDocument(r.PathValue("id"), body.Title)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, doc)
}

func (s *Server) deleteDocument(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Knowledge.DeleteDocument(r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]bool{"deleted": true})
}

func (s *Server) deleteDocumentTree(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.app.Knowledge.DeleteDirectoryRecursive(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]int{"deleted": deleted})
}

type deleteDocumentsRequest struct {
	IDs []string `json:"ids"`
}

func (s *Server) deleteDocuments(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[deleteDocumentsRequest](w, r)
	if !ok {
		return
	}
	deleted, err := s.app.Knowledge.DeleteDocuments(body.IDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]int{"deleted": deleted})
}

func (s *Server) reindexDocuments(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[deleteDocumentsRequest](w, r)
	if !ok {
		return
	}
	reindexed := 0
	skipped := 0
	for _, id := range body.IDs {
		if _, err := s.app.Knowledge.ReindexDocument(r.Context(), id); err != nil {
			if errors.Is(err, knowledge.ErrNotFound) {
				skipped++
				continue
			}
			writeErr(w, err)
			return
		}
		reindexed++
	}
	writeOK(w, map[string]int{"reindexed": reindexed, "skipped": skipped})
}

func (s *Server) reindexOne(w http.ResponseWriter, r *http.Request) {
	doc, err := s.app.Knowledge.ReindexDocument(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, doc)
}

func (s *Server) listChunks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	chunks, err := s.app.Knowledge.ListChunks(r.PathValue("id"), limit, offset)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, chunks)
}

func (s *Server) jobStatus(w http.ResponseWriter, r *http.Request) {
	job, ok := s.app.Jobs.Status(r.PathValue("id"))
	if !ok {
		writeErr(w, knowledge.ErrNotFound)
		return
	}
	writeOK(w, job)
}

func (s *Server) jobCancel(w http.ResponseWriter, r *http.Request) {
	if !s.app.Jobs.Cancel(r.PathValue("id")) {
		writeErr(w, knowledge.ErrNotFound)
		return
	}
	writeOK(w, map[string]bool{"cancelled": true})
}

func (s *Server) listGroups(w http.ResponseWriter, _ *http.Request) {
	groups, err := s.app.Knowledge.ListGroups()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, groups)
}

type groupRequest struct {
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[groupRequest](w, r)
	if !ok {
		return
	}
	groups, err := s.app.Knowledge.CreateGroup(body.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, groups)
}

func (s *Server) renameGroup(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[groupRequest](w, r)
	if !ok {
		return
	}
	groups, err := s.app.Knowledge.RenameGroup(body.From, body.To)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, groups)
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[groupRequest](w, r)
	if !ok {
		return
	}
	if err := s.app.Knowledge.DeleteGroup(body.Name); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]bool{"deleted": true})
}

func (s *Server) getScope(w http.ResponseWriter, _ *http.Request) {
	enabled, baseIDs, err := s.app.Knowledge.EnabledScope()
	if err != nil {
		writeErr(w, err)
		return
	}
	if baseIDs == nil {
		baseIDs = []string{}
	}
	writeOK(w, map[string]any{"enabled": enabled, "enabledBaseIds": baseIDs})
}

type scopeRequest struct {
	Enabled        *bool     `json:"enabled"`
	EnabledBaseIDs *[]string `json:"enabledBaseIds"`
}

func (s *Server) putScope(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[scopeRequest](w, r)
	if !ok {
		return
	}
	if err := s.app.Knowledge.SetEnabledScope(body.Enabled, body.EnabledBaseIDs); err != nil {
		writeErr(w, err)
		return
	}
	s.getScope(w, r)
}

func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request) {
	config := s.app.Knowledge.GlobalConfig()
	writeOK(w, map[string]any{
		"config":             config,
		"embeddingApiKeySet": config.Embedding.APIKey != "",
		"rerankApiKeySet":    config.Rerank.APIKey != "",
		"mineruApiKeySet":    config.Processing.APIKey != "",
		"captionApiKeySet":   config.Captioning.APIKey != "",
	})
}

type configRequest struct {
	Config               config.Config `json:"config"`
	EmbeddingAPIKey      *string       `json:"embeddingApiKey"`
	ClearEmbeddingAPIKey bool          `json:"clearEmbeddingApiKey"`
	RerankAPIKey         *string       `json:"rerankApiKey"`
	ClearRerankAPIKey    bool          `json:"clearRerankApiKey"`
	MineruAPIKey         *string       `json:"mineruApiKey"`
	ClearMineruAPIKey    bool          `json:"clearMineruApiKey"`
	CaptionAPIKey        *string       `json:"captionApiKey"`
	ClearCaptionAPIKey   bool          `json:"clearCaptionApiKey"`
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[configRequest](w, r)
	if !ok {
		return
	}
	current := s.app.Knowledge.GlobalConfig()
	next := body.Config.Normalized()
	next.Embedding.APIKey = current.Embedding.APIKey
	next.Rerank.APIKey = current.Rerank.APIKey
	next.Processing.APIKey = current.Processing.APIKey
	next.Captioning.APIKey = current.Captioning.APIKey
	if body.ClearEmbeddingAPIKey {
		next.Embedding.APIKey = ""
	} else if body.EmbeddingAPIKey != nil && *body.EmbeddingAPIKey != "" {
		next.Embedding.APIKey = *body.EmbeddingAPIKey
	}
	if body.ClearRerankAPIKey {
		next.Rerank.APIKey = ""
	} else if body.RerankAPIKey != nil && *body.RerankAPIKey != "" {
		next.Rerank.APIKey = *body.RerankAPIKey
	}
	if body.ClearMineruAPIKey {
		next.Processing.APIKey = ""
	} else if body.MineruAPIKey != nil && *body.MineruAPIKey != "" {
		next.Processing.APIKey = *body.MineruAPIKey
	}
	if body.ClearCaptionAPIKey {
		next.Captioning.APIKey = ""
	} else if body.CaptionAPIKey != nil && *body.CaptionAPIKey != "" {
		next.Captioning.APIKey = *body.CaptionAPIKey
	}
	if err := s.app.UpdateConfig(next); err != nil {
		writeErr(w, err)
		return
	}
	s.getConfig(w, r)
}

func (s *Server) modelSuggestions(w http.ResponseWriter, _ *http.Request) {
	writeOK(w, map[string][]string{
		"embedding": []string{
			"text-embedding-3-small", "text-embedding-3-large",
			"nomic-embed-text", "onnx-community/Qwen3-Embedding-0.6B-ONNX",
		},
		"rerank": []string{
			"jina-reranker-v2-base-multilingual",
			"BAAI/bge-reranker-v2-m3",
		},
	})
}

func (s *Server) listLocalModels(w http.ResponseWriter, _ *http.Request) {
	list, err := s.app.ListLocalModels()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"models": list, "cacheDir": s.app.Models.Root()})
}

func (s *Server) ocrModel(w http.ResponseWriter, _ *http.Request) {
	model, err := s.app.Models.OCRStatus()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, model)
}

func (s *Server) downloadOCRModel(w http.ResponseWriter, _ *http.Request) {
	jobID, err := s.app.Jobs.Submit("download-ocr-model", models.OCRModelID, 100, func(ctx context.Context, report func(progress int)) error {
		return s.app.Models.DownloadOCR(ctx, report)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]string{"jobId": jobID})
}

func (s *Server) removeOCRModel(w http.ResponseWriter, _ *http.Request) {
	status, err := s.app.Models.OCRStatus()
	if err != nil {
		writeErr(w, err)
		return
	}
	if status.Status != "not-downloaded" {
		if err := s.app.Models.Remove(models.OCRModelID); err != nil {
			writeErr(w, err)
			return
		}
	}
	writeOK(w, map[string]bool{"removed": true})
}

func (s *Server) registerLocalReranker(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[struct {
		ID string `json:"id"`
	}](w, r)
	if !ok {
		return
	}
	item, err := s.app.Knowledge.RegisterCustomReranker(body.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, item)
}

func (s *Server) selfTestLocalReranker(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[struct {
		ID string `json:"id"`
	}](w, r)
	if !ok {
		return
	}
	result, err := s.app.SelfTestReranker(r.Context(), body.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (s *Server) runtimeStatus(w http.ResponseWriter, r *http.Request) {
	if s.app.Runtime == nil {
		writeOK(w, map[string]any{"status": map[string]runtime.Health{}})
		return
	}
	writeOK(w, map[string]any{"status": s.app.Runtime.Status(r.Context())})
}

type localModelDownloadRequest struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Artifacts []string `json:"artifacts"`
}

func (s *Server) downloadLocalModel(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[localModelDownloadRequest](w, r)
	if !ok {
		return
	}
	jobID, err := s.app.Jobs.Submit("download-model", body.ID, 100, func(ctx context.Context, report func(progress int)) error {
		return s.app.Models.Download(ctx, models.DownloadRequest(body), report)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]string{"jobId": jobID})
}

type localModelRemoveRequest struct {
	ID string `json:"id"`
}

func (s *Server) removeLocalModel(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[localModelRemoveRequest](w, r)
	if !ok {
		return
	}
	model, err := s.app.Models.Get(body.ID)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		writeErr(w, err)
		return
	}
	if err != nil {
		if customErr := s.app.Knowledge.DeleteCustomReranker(body.ID); customErr != nil {
			writeErr(w, errors.Join(err, customErr))
			return
		}
		writeOK(w, map[string]bool{"removed": true})
		return
	}
	if err := s.app.Models.Remove(body.ID); err != nil {
		writeErr(w, err)
		return
	}
	if model.Kind == models.KindRerank {
		_ = s.app.Knowledge.DeleteCustomReranker(body.ID)
	}
	writeOK(w, map[string]bool{"removed": true})
}

func (s *Server) planModelCacheMigration(w http.ResponseWriter, r *http.Request) {
	plan, err := s.app.PlanModelCacheMigration(r.URL.Query().Get("targetDir"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, plan)
}

type modelCacheMigrationRequest struct {
	TargetDir    string `json:"targetDir"`
	RemoveSource bool   `json:"removeSource"`
}

func (s *Server) migrateModelCache(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[modelCacheMigrationRequest](w, r)
	if !ok {
		return
	}
	result, err := s.app.MigrateModelCache(body.TargetDir, body.RemoveSource)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (s *Server) listOllamaModels(w http.ResponseWriter, r *http.Request) {
	list, err := s.app.Ollama.Tags(r.Context())
	if err == nil {
		writeOK(w, map[string]any{"available": true, "models": list, "baseUrl": "http://127.0.0.1:11434"})
		return
	}
	writeOK(w, map[string]any{
		"available": false, "models": []models.OllamaModel{},
		"baseUrl": "http://127.0.0.1:11434", "error": err.Error(),
	})
}

type ollamaModelRequest struct {
	Model string `json:"model"`
}

func (s *Server) pullOllamaModel(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[ollamaModelRequest](w, r)
	if !ok {
		return
	}
	jobID, err := s.app.Jobs.Submit("ollama-pull", body.Model, 100, func(ctx context.Context, report func(progress int)) error {
		return s.app.Ollama.Pull(ctx, body.Model, report)
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]string{"jobId": jobID})
}

func (s *Server) deleteOllamaModel(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[ollamaModelRequest](w, r)
	if !ok {
		return
	}
	if err := s.app.Ollama.Delete(r.Context(), body.Model); err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]bool{"deleted": true})
}

// Service exposes the knowledge service for tests and the extension adapter.
func (s *Server) Service() *knowledge.Service { return s.app.Knowledge }
