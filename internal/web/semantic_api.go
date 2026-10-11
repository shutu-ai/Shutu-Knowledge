package web

import (
	"errors"
	"net/http"

	"github.com/shutu-ai/shutu-knowledge/internal/operations"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

type semanticSearchRequest struct {
	Query string              `json:"query"`
	TopK  int                 `json:"topK,omitempty"`
	Kinds []semantic.UnitKind `json:"kinds,omitempty"`
}

type semanticContextRequest struct {
	Query       string `json:"query"`
	TokenBudget int    `json:"tokenBudget,omitempty"`
}

func (s *Server) compileSemanticMemory(w http.ResponseWriter, r *http.Request) {
	compilation, err := s.app.Knowledge.CompileSemanticMemory(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, compilation)
}

func (s *Server) compileSemanticMemoryOperation(w http.ResponseWriter, r *http.Request) {
	baseID := r.PathValue("id")
	if _, err := s.app.Knowledge.GetBaseWithContext(r.Context(), baseID); err != nil {
		writeErr(w, err)
		return
	}
	existing, _, err := s.app.Operations.List(r.Context(), operations.ListFilter{
		States:  []string{operations.StateQueued, operations.StateRunning, operations.StateCancelling},
		BaseIDs: []string{baseID}, Limit: 200,
	})
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	for _, operation := range existing {
		if operation.Type == "semantic_compile" {
			writeOperationAccepted(w, operation)
			return
		}
	}

	total, err := s.app.Knowledge.CountSemanticCompileDocumentsContext(r.Context(), baseID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if total == 0 {
		writeErr(w, errors.New("no ready documents with Document IR in this knowledge base"))
		return
	}
	operation, err := s.app.Operations.Submit(r.Context(), operations.Request{
		Type: "semantic_compile", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: baseID, Payload: []byte("{}"), ResourceClass: operations.ResourceIO,
		TotalUnits: &total,
	})
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeOperationAccepted(w, operation)
}

func (s *Server) getSemanticCompilation(w http.ResponseWriter, r *http.Request) {
	compilation, err := s.app.Knowledge.GetSemanticCompilation(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, compilation)
}

func (s *Server) searchSemanticMemory(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[semanticSearchRequest](w, r)
	if !ok {
		return
	}
	result, err := s.app.Knowledge.SearchSemanticMemory(r.Context(), r.PathValue("id"), semantic.SearchOptions{
		Query: body.Query, TopK: body.TopK, Kinds: body.Kinds,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (s *Server) compileKnowledgeContext(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[semanticContextRequest](w, r)
	if !ok {
		return
	}
	result, err := s.app.AgentQuery.ContextForBase(r.Context(), r.PathValue("id"), body.Query, body.TokenBudget)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (s *Server) getSemanticWiki(w http.ResponseWriter, r *http.Request) {
	view, err := s.app.Knowledge.GetSemanticWiki(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, view)
}

func (s *Server) resolveSemanticUnitEvidence(w http.ResponseWriter, r *http.Request) {
	sources, err := s.app.Knowledge.ResolveSemanticUnitEvidence(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, sources)
}
