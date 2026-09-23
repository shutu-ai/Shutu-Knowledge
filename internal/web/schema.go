package web

import (
	"errors"
	"net/http"
)

type schemaSearchRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"topK"`
}

type schemaRequirementRequest struct {
	Query         string `json:"query"`
	MaxPerConcept int    `json:"maxPerConcept"`
	MaxTables     int    `json:"maxTables"`
}

func (s *Server) compileBaseSchema(w http.ResponseWriter, r *http.Request) {
	report, err := s.app.Knowledge.CompileBaseSchema(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, report)
}

func (s *Server) searchSchema(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[schemaSearchRequest](w, r)
	if !ok {
		return
	}
	if body.Query == "" {
		writeErr(w, errors.New("query is required"))
		return
	}
	result, err := s.app.Knowledge.SearchSchema(r.Context(), r.PathValue("id"), body.Query, body.TopK)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (s *Server) resolveDataRequirement(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[schemaRequirementRequest](w, r)
	if !ok {
		return
	}
	if body.Query == "" {
		writeErr(w, errors.New("query is required"))
		return
	}
	result, err := s.app.Knowledge.ResolveDataRequirement(r.Context(), r.PathValue("id"), body.Query, body.MaxPerConcept, body.MaxTables)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, result)
}

func (s *Server) getSchemaTable(w http.ResponseWriter, r *http.Request) {
	table, fields, err := s.app.Knowledge.GetSchemaTable(r.Context(), r.PathValue("id"), r.PathValue("tableId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, map[string]any{"table": table, "fields": fields})
}

func (s *Server) getSchemaField(w http.ResponseWriter, r *http.Request) {
	field, err := s.app.Knowledge.GetSchemaField(r.Context(), r.PathValue("id"), r.PathValue("fieldId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeOK(w, field)
}
