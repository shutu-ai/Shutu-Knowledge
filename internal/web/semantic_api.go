package web

import (
	"net/http"

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
	result, err := s.app.Knowledge.CompileKnowledgeContext(r.Context(), r.PathValue("id"), body.Query, body.TokenBudget)
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
