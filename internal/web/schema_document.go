package web

import (
	"net/http"
)

func (s *Server) compileDocumentSchema(w http.ResponseWriter, r *http.Request) {
	model, err := s.app.Knowledge.CompileDocumentSchema(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"detected": len(model.Tables) > 0, "tableCount": len(model.Tables), "fieldCount": len(model.Fields)})
}
