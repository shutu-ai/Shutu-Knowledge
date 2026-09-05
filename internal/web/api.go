package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
)

// registerKnowledgeAPI mounts the Knowledge REST surface on the mux.
func registerKnowledgeAPI(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/bases", s.listBases)
	mux.HandleFunc("POST /api/bases", s.createBase)
	mux.HandleFunc("GET /api/bases/{id}", s.getBase)
	mux.HandleFunc("PATCH /api/bases/{id}", s.patchBase)
	mux.HandleFunc("DELETE /api/bases/{id}", s.deleteBase)
	mux.HandleFunc("GET /api/bases/{id}/stats", s.baseStats)
	mux.HandleFunc("GET /api/bases/{id}/documents", s.listDocuments)
	mux.HandleFunc("POST /api/bases/{id}/documents", s.addDocument)
	mux.HandleFunc("POST /api/bases/{id}/files", s.addFiles)
	mux.HandleFunc("POST /api/bases/{id}/reindex", s.reindexBase)
	mux.HandleFunc("GET /api/documents/{id}", s.getDocument)
	mux.HandleFunc("PATCH /api/documents/{id}", s.patchDocument)
	mux.HandleFunc("DELETE /api/documents/{id}", s.deleteDocument)
	mux.HandleFunc("POST /api/documents/delete", s.deleteDocuments)
	mux.HandleFunc("POST /api/documents/reindex", s.reindexDocuments)
	mux.HandleFunc("GET /api/documents/{id}/chunks", s.listChunks)
	mux.HandleFunc("POST /api/documents/{id}/reindex", s.reindexOne)
	mux.HandleFunc("GET /api/jobs/{id}", s.jobStatus)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.jobCancel)
	mux.HandleFunc("GET /api/groups", s.listGroups)
	mux.HandleFunc("POST /api/groups", s.createGroup)
	mux.HandleFunc("PATCH /api/groups", s.renameGroup)
	mux.HandleFunc("DELETE /api/groups", s.deleteGroup)
	mux.HandleFunc("GET /api/scope", s.getScope)
	mux.HandleFunc("PUT /api/scope", s.putScope)
	mux.HandleFunc("GET /api/stats", s.globalStats)
	mux.HandleFunc("POST /api/search", s.search)
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
	writeOK(w, base)
}

func (s *Server) getBase(w http.ResponseWriter, r *http.Request) {
	base, err := s.app.Knowledge.GetBase(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, base)
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
	base, err := s.app.Knowledge.RenameBase(r.PathValue("id"), body.Name, body.Description, body.Group, body.Config)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, base)
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

func (s *Server) globalStats(w http.ResponseWriter, _ *http.Request) {
	stats, err := s.app.Knowledge.Stats("")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, stats)
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
	writeOK(w, result)
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

// Service exposes the knowledge service for tests and the extension adapter.
func (s *Server) Service() *knowledge.Service { return s.app.Knowledge }
