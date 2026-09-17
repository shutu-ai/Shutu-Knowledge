package web

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/operations"
)

type operationTarget struct {
	BaseID     string `json:"baseId"`
	DocumentID string `json:"documentId"`
}

type operationSubmission struct {
	Type                 string          `json:"type"`
	CommandSchemaVersion int             `json:"commandSchemaVersion"`
	Target               operationTarget `json:"target"`
	Input                json.RawMessage `json:"input"`
	IdempotencyKey       string          `json:"idempotencyKey"`
}

func registerOperationsAPI(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("POST /api/operation-keys", s.createOperationKey)
	mux.HandleFunc("POST /api/operation-keys/rotate", s.rotateOperationKey)
	mux.HandleFunc("POST /api/operation-keys/revoke", s.revokeOperationKey)
	mux.HandleFunc("POST /api/uploads", s.createUpload)
	mux.HandleFunc("PUT /api/uploads/{id}/content", s.putUploadContent)
	mux.HandleFunc("POST /api/uploads/{id}/complete", s.completeUpload)
	mux.HandleFunc("POST /api/operations", s.submitOperation)
	mux.HandleFunc("GET /api/operations", s.listOperations)
	mux.HandleFunc("GET /api/operations/{id}", s.getOperation)
	mux.HandleFunc("POST /api/operations/{id}/cancel", s.cancelOperation)
	mux.HandleFunc("POST /api/operations/{id}/retry", s.retryOperation)
	mux.HandleFunc("GET /api/operations/{id}/events", s.operationEvents)
}

func (s *Server) submitOperation(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[operationSubmission](w, r)
	if !ok {
		return
	}
	if body.CommandSchemaVersion == 0 {
		body.CommandSchemaVersion = operations.CommandSchemaV1
	}
	if body.CommandSchemaVersion != operations.CommandSchemaV1 {
		writeOperationErr(w, operations.ErrUnsupportedCommand)
		return
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		idempotencyKey = body.IdempotencyKey
	}
	if strings.HasPrefix(idempotencyKey, "opcred.") {
		credential, err := s.app.Operations.ValidateIdempotencyCredential(
			r.Context(), idempotencyKey, operations.Request{
				Type: body.Type, BaseID: body.Target.BaseID,
				DocumentID: body.Target.DocumentID,
			})
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		idempotencyKey = credential.OperationKey
	}
	req := operations.Request{
		Type:                 body.Type,
		CommandSchemaVersion: body.CommandSchemaVersion,
		BaseID:               body.Target.BaseID,
		DocumentID:           body.Target.DocumentID,
		Payload:              body.Input,
		IdempotencyKey:       idempotencyKey,
	}
	if body.Type == "import_text" {
		if req.DocumentID == "" {
			req.PreallocateDocument = true
		}
		var input struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
	}
	if body.Type == "import_url" {
		if req.DocumentID == "" {
			req.PreallocateDocument = true
		}
		var input struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
	}
	if body.Type == "import_file" {
		if req.DocumentID == "" {
			req.PreallocateDocument = true
		}
		var input struct {
			UploadID          string `json:"uploadId"`
			FileName          string `json:"fileName"`
			Conflict          string `json:"conflict"`
			ParentDirectoryID string `json:"parentDirectoryId"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		if input.UploadID == "" {
			writeOperationErr(w, operations.ErrUploadNotFound)
			return
		}
		req.UploadID = input.UploadID
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
	}
	if body.Type == "import_files" {
		var input struct {
			Files             []json.RawMessage `json:"files"`
			Conflict          string            `json:"conflict"`
			ParentDirectoryID string            `json:"parentDirectoryId"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		if req.BaseID == "" || len(input.Files) == 0 {
			writeOperationErr(w, errors.New("import_files requires baseId and at least one file"))
			return
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		totalUnits := len(input.Files)
		req.TotalUnits = &totalUnits
		req.ResourceClass = operations.ResourceIO
	}
	if body.Type == "import_directory" {
		var input struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		input.Path = strings.TrimSpace(input.Path)
		if req.BaseID == "" || input.Path == "" {
			writeOperationErr(w, errors.New("import_directory requires baseId and path"))
			return
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		req.ResourceClass = operations.ResourceIO
	}
	if body.Type == "delete_documents" {
		var input struct {
			DocumentIDs []string `json:"documentIds"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		if req.BaseID == "" || len(input.DocumentIDs) == 0 {
			writeOperationErr(w, errors.New("delete_documents requires baseId and documentIds"))
			return
		}
		sort.Strings(input.DocumentIDs)
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		totalUnits := len(input.DocumentIDs)
		req.TotalUnits = &totalUnits
		req.ResourceClass = operations.ResourceIO
	}
	if body.Type == "delete_directory" || body.Type == "rescan_directory" || body.Type == "refresh_url" {
		if req.DocumentID == "" {
			var input struct {
				DocumentID string `json:"documentId"`
			}
			if err := json.Unmarshal(body.Input, &input); err == nil {
				req.DocumentID = strings.TrimSpace(input.DocumentID)
			}
		}
		if req.DocumentID == "" {
			writeOperationErr(w, errors.New("operation requires documentId"))
			return
		}
		encoded, err := json.Marshal(map[string]string{"documentId": req.DocumentID})
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		req.ResourceClass = operations.ResourceIO
		if body.Type == "refresh_url" {
			req.ResourceClass = operations.ResourceNetwork
		}
		if body.Type != "rescan_directory" {
			totalUnits := 1
			req.TotalUnits = &totalUnits
		}
	}
	if body.Type == "restore_base" {
		var input struct {
			Name   string           `json:"name"`
			Config *json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		if req.BaseID == "" {
			writeOperationErr(w, errors.New("restore_base requires source baseId"))
			return
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		req.ResourceClass = operations.ResourceIO
		totalUnits := 1
		req.TotalUnits = &totalUnits
	}
	if body.Type == "reindex_document" {
		if req.DocumentID == "" {
			writeOperationErr(w, operations.ErrNotFound)
			return
		}
		encoded, err := json.Marshal(map[string]string{"documentId": req.DocumentID})
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		totalUnits := 1
		req.TotalUnits = &totalUnits
		req.ResourceClass = "io"
		if req.IdempotencyKey == "" {
			key, err := s.app.Knowledge.ReindexIdempotencyKey(r.Context(), req.BaseID, []string{req.DocumentID})
			if err != nil {
				writeOperationErr(w, err)
				return
			}
			req.IdempotencyKey = key
		}
	}
	if body.Type == "delete_base" {
		if req.BaseID == "" {
			writeOperationErr(w, operations.ErrNotFound)
			return
		}
		req.Payload = json.RawMessage("{}")
		totalUnits := 1
		req.TotalUnits = &totalUnits
		req.ResourceClass = operations.ResourceIO
	}
	if body.Type == "reindex_base" {
		if req.BaseID == "" {
			writeOperationErr(w, operations.ErrNotFound)
			return
		}
		req.Payload = json.RawMessage("{}")
		req.ResourceClass = "io"
		if req.IdempotencyKey == "" {
			key, err := s.app.Knowledge.ReindexIdempotencyKey(r.Context(), req.BaseID, nil)
			if err != nil {
				writeOperationErr(w, err)
				return
			}
			req.IdempotencyKey = key
		}
	}
	if body.Type == "download_ocr_model" {
		req.Payload = json.RawMessage("{}")
		totalUnits := 100
		req.TotalUnits = &totalUnits
		req.ResourceClass = "network"
	}
	if body.Type == "self_test_reranker" || body.Type == "download_model" || body.Type == "ollama_pull" {
		input := map[string]any{}
		if len(body.Input) > 0 {
			if err := json.Unmarshal(body.Input, &input); err != nil {
				writeOperationErr(w, err)
				return
			}
		}
		if body.Type == "ollama_pull" {
			model, _ := input["model"].(string)
			model = strings.TrimSpace(model)
			if model == "" {
				writeOperationErr(w, errors.New("model is required"))
				return
			}
			input["model"] = model
		} else {
			modelID, _ := input["id"].(string)
			modelID = strings.TrimPrefix(strings.TrimSpace(modelID), "local:")
			if modelID == "" {
				writeOperationErr(w, errors.New("model id is required"))
				return
			}
			input["id"] = modelID
		}
		if body.Type == "download_model" {
			kind, _ := input["kind"].(string)
			if kind != "embedding" && kind != "rerank" {
				writeOperationErr(w, errors.New("model kind must be embedding or rerank"))
				return
			}
			input["managed"] = s.app.ManagedRuntime
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		totalUnits := 100
		req.TotalUnits = &totalUnits
		req.ResourceClass = operations.ResourceNetwork
		if body.Type == "self_test_reranker" {
			req.ResourceClass = operations.ResourceModel
		} else if body.Type == "download_model" && s.app.ManagedRuntime {
			req.ResourceClass = operations.ResourceModel
		}
	}
	if body.Type == "remove_ocr_model" {
		req.Payload = json.RawMessage("{}")
		totalUnits := 1
		req.TotalUnits = &totalUnits
		req.ResourceClass = operations.ResourceDisk
	}
	if body.Type == "remove_model" {
		var input struct {
			ID             string `json:"id"`
			Kind           string `json:"kind"`
			Managed        bool   `json:"managed"`
			Capability     string `json:"capability"`
			CustomReranker bool   `json:"customReranker"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		input.ID = strings.TrimPrefix(strings.TrimSpace(input.ID), "local:")
		if input.ID == "" || (input.Managed && input.Capability != "embedding" && input.Capability != "rerank") {
			writeOperationErr(w, errors.New("remove_model requires id and a valid managed capability"))
			return
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		totalUnits := 1
		req.TotalUnits = &totalUnits
		req.ResourceClass = operations.ResourceDisk
		if input.Managed {
			req.ResourceClass = operations.ResourceModel
		}
	}
	if body.Type == "ollama_delete" {
		var input struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		input.Model = strings.TrimSpace(input.Model)
		if input.Model == "" {
			writeOperationErr(w, errors.New("ollama model is required"))
			return
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		totalUnits := 1
		req.TotalUnits = &totalUnits
		req.ResourceClass = operations.ResourceModel
	}
	if body.Type == "migrate_model_cache" {
		var input struct {
			TargetDir    string `json:"targetDir"`
			RemoveSource bool   `json:"removeSource"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		if strings.TrimSpace(input.TargetDir) == "" {
			writeOperationErr(w, errors.New("targetDir is required"))
			return
		}
		resolved := s.app.ResolveModelCacheDir(input.TargetDir)
		encoded, err := json.Marshal(map[string]any{
			"targetDir": resolved, "removeSource": input.RemoveSource,
		})
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		if existing, found, err := s.app.Operations.FindIdempotencyOperationContext(r.Context(), req); found {
			writeOperationAccepted(w, existing)
			return
		} else if err != nil {
			writeOperationErr(w, err)
			return
		}
		totalUnits := 1
		req.TotalUnits = &totalUnits
		req.ResourceClass = "disk"
	}
	if body.Type == "plan_model_cache_migration" {
		var input struct {
			TargetDir string `json:"targetDir"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		if strings.TrimSpace(input.TargetDir) == "" {
			writeOperationErr(w, errors.New("targetDir is required"))
			return
		}
		resolved := s.app.ResolveModelCacheDir(input.TargetDir)
		encoded, err := json.Marshal(map[string]any{"targetDir": resolved})
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		totalUnits := 1
		req.TotalUnits = &totalUnits
		req.ResourceClass = operations.ResourceDisk
	}
	if body.Type == "reindex_documents" {
		var input struct {
			DocumentIDs []string `json:"documentIds"`
		}
		if err := json.Unmarshal(body.Input, &input); err != nil {
			writeOperationErr(w, err)
			return
		}
		if len(input.DocumentIDs) == 0 {
			writeOperationErr(w, errors.New("at least one document is required"))
			return
		}
		if req.BaseID == "" {
			writeOperationErr(w, operations.ErrNotFound)
			return
		}
		sort.Strings(input.DocumentIDs)
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		totalUnits := len(input.DocumentIDs)
		req.TotalUnits = &totalUnits
		req.ResourceClass = "io"
		if req.IdempotencyKey == "" {
			key, err := s.app.Knowledge.ReindexIdempotencyKey(r.Context(), req.BaseID, input.DocumentIDs)
			if err != nil {
				writeOperationErr(w, err)
				return
			}
			req.IdempotencyKey = key
		}
	}
	if body.Type == "maintenance_storage" {
		var input struct {
			DryRun          bool `json:"dryRun"`
			PurgeQuarantine bool `json:"purgeQuarantine"`
		}
		if len(body.Input) > 0 {
			if err := json.Unmarshal(body.Input, &input); err != nil {
				writeOperationErr(w, err)
				return
			}
		}
		if input.DryRun && input.PurgeQuarantine {
			writeOperationErr(w, errors.New("dryRun and purgeQuarantine are mutually exclusive"))
			return
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			writeOperationErr(w, err)
			return
		}
		req.Payload = encoded
		req.ResourceClass = operations.ResourceMaintenance
	}
	op, err := s.app.Operations.Submit(r.Context(), req)
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeOperationAccepted(w, op)
}

func (s *Server) createOperationKey(w http.ResponseWriter, r *http.Request) {
	if !s.operationKeyLimit.allow(remoteRateKey(r)) {
		writeJSON(w, http.StatusTooManyRequests, envelopeError("rate_limited", "operation key issuance is rate limited"))
		return
	}
	body, ok := decodeBody[operations.IdempotencyCredentialRequest](w, r)
	if !ok {
		return
	}
	credential, err := s.app.Operations.IssueIdempotencyCredential(r.Context(), body)
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "value": credential})
}

func (s *Server) rotateOperationKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RetentionSeconds int `json:"retentionSeconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeOperationErr(w, err)
		return
	}
	retention := time.Duration(body.RetentionSeconds) * time.Second
	keyID, err := s.app.Operations.RotateIdempotencySigningKey(r.Context(), retention)
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "value": map[string]string{"keyId": keyID}})
}

func (s *Server) revokeOperationKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		KeyID string `json:"keyId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeOperationErr(w, err)
		return
	}
	if strings.TrimSpace(body.KeyID) == "" {
		writeOperationErr(w, operations.ErrNotFound)
		return
	}
	if err := s.app.Operations.RevokeIdempotencySigningKey(r.Context(), body.KeyID); err != nil {
		writeOperationErr(w, err)
		return
	}
	writeOK(w, map[string]any{"keyId": body.KeyID, "revoked": true})
}

func remoteRateKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody[operations.UploadCreate](w, r)
	if !ok {
		return
	}
	session, err := s.app.Operations.CreateUpload(r.Context(), body)
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok": true,
		"value": map[string]any{
			"upload":      session,
			"contentUrl":  "/api/uploads/" + session.ID + "/content",
			"completeUrl": "/api/uploads/" + session.ID + "/complete",
		},
	})
}

func (s *Server) putUploadContent(w http.ResponseWriter, r *http.Request) {
	session, err := s.app.Operations.PutUploadContent(r.Context(), r.PathValue("id"), r.Body)
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeOK(w, session)
}

func (s *Server) completeUpload(w http.ResponseWriter, r *http.Request) {
	session, err := s.app.Operations.CompleteUpload(r.Context(), r.PathValue("id"))
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeOK(w, session)
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	op, err := s.app.Operations.GetContext(r.Context(), r.PathValue("id"))
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeOK(w, op)
}

func (s *Server) listOperations(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	states := query["state"]
	baseIDs := query["baseId"]
	docIDs := query["documentId"]
	if states == nil {
		states = nil
	}
	operationsList, cursor, err := s.app.Operations.List(r.Context(), operations.ListFilter{
		States:   states,
		BaseIDs:  baseIDs,
		DocIDs:   docIDs,
		ParentID: query.Get("parentOperationId"),
		Limit:    limit,
		Cursor:   query.Get("cursor"),
	})
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	if operationsList == nil {
		operationsList = []operations.Operation{}
	}
	writeOK(w, map[string]any{"operations": operationsList, "nextCursor": cursor})
}

func (s *Server) cancelOperation(w http.ResponseWriter, r *http.Request) {
	op, err := s.app.Operations.CancelContext(r.Context(), r.PathValue("id"))
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeOK(w, op)
}

func (s *Server) retryOperation(w http.ResponseWriter, r *http.Request) {
	op, err := s.app.Operations.RetryContext(r.Context(), r.PathValue("id"))
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "value": op})
}

func (s *Server) operationEvents(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.app.Operations.Events(r.Context(), r.PathValue("id"), after, limit)
	if err != nil {
		writeOperationErr(w, err)
		return
	}
	writeOK(w, map[string]any{"events": events})
}

func writeOperationAccepted(w http.ResponseWriter, op operations.Operation) {
	status := http.StatusAccepted
	if op.State == operations.StateSucceeded || op.State == operations.StateFailed || op.State == operations.StateCancelled {
		status = http.StatusOK
	}
	w.Header().Set("Location", "/api/operations/"+op.ID)
	w.Header().Set("Retry-After", "1")
	writeJSON(w, status, map[string]any{"ok": true, "value": op})
}

// writeLegacyJobAccepted keeps old jobId clients working while Operation is
// the durable authority. New callers should prefer the operation object.
func writeLegacyJobAccepted(w http.ResponseWriter, op operations.Operation, progressMode string) {
	value := map[string]any{
		"jobId": op.ID, "operationId": op.ID, "submitted": true,
	}
	if progressMode != "" {
		value["progressMode"] = progressMode
	}
	status := http.StatusAccepted
	if op.State == operations.StateSucceeded || op.State == operations.StateFailed || op.State == operations.StateCancelled {
		status = http.StatusOK
	}
	w.Header().Set("Location", "/api/operations/"+op.ID)
	w.Header().Set("Retry-After", "1")
	writeJSON(w, status, map[string]any{"ok": true, "value": value})
}

// writeDocumentOperationAccepted keeps document-id-bearing compatibility
// responses usable: old callers learn the stable document ID immediately while
// the durable operation remains the authority for readiness.
func writeDocumentOperationAccepted(w http.ResponseWriter, op operations.Operation, progressMode string) {
	value := map[string]any{
		"jobId": op.ID, "operationId": op.ID, "documentId": op.DocumentID, "submitted": true,
	}
	if progressMode != "" {
		value["progressMode"] = progressMode
	}
	status := http.StatusAccepted
	if op.State == operations.StateSucceeded || op.State == operations.StateFailed || op.State == operations.StateCancelled {
		status = http.StatusOK
	}
	w.Header().Set("Location", "/api/operations/"+op.ID)
	w.Header().Set("Retry-After", "1")
	writeJSON(w, status, map[string]any{"ok": true, "value": value})
}

func writeOperationErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, operations.ErrNotFound):
		writeJSON(w, http.StatusNotFound, envelopeError("not-found", err.Error()))
	case errors.Is(err, operations.ErrIdempotencyConflict):
		writeJSON(w, http.StatusConflict, envelopeError("idempotency_conflict", err.Error()))
	case errors.Is(err, operations.ErrOperationExpired):
		writeJSON(w, http.StatusGone, envelopeError("operation_expired", err.Error()))
	case errors.Is(err, operations.ErrIdempotencyCredentialExpired):
		writeJSON(w, http.StatusGone, envelopeError("operation_credential_expired", err.Error()))
	case errors.Is(err, operations.ErrIdempotencyCredentialInvalid):
		writeJSON(w, http.StatusUnauthorized, envelopeError("invalid_credential", err.Error()))
	case errors.Is(err, operations.ErrQueueFull):
		writeJSON(w, http.StatusTooManyRequests, envelopeError("queue_full", err.Error()))
	case errors.Is(err, operations.ErrResourceBudgetExceeded):
		writeJSON(w, http.StatusTooManyRequests, envelopeError("quota_exceeded", err.Error()))
	case errors.Is(err, operations.ErrDiskLowWater):
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusTooManyRequests, envelopeError("disk_low_water", err.Error()))
	case errors.Is(err, operations.ErrPayloadTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, envelopeError("payload_too_large", err.Error()))
	case errors.Is(err, operations.ErrUnsupportedCommand):
		writeJSON(w, http.StatusBadRequest, envelopeError("unsupported_command", err.Error()))
	case errors.Is(err, operations.ErrUploadNotFound):
		writeJSON(w, http.StatusNotFound, envelopeError("upload-not-found", err.Error()))
	case errors.Is(err, operations.ErrUploadScopeMismatch),
		errors.Is(err, operations.ErrUploadState),
		errors.Is(err, operations.ErrUploadNotComplete),
		errors.Is(err, operations.ErrUploadSizeMismatch),
		errors.Is(err, operations.ErrUploadHashMismatch):
		writeJSON(w, http.StatusConflict, envelopeError("upload-invalid", err.Error()))
	case errors.Is(err, operations.ErrUploadTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, envelopeError("upload-too-large", err.Error()))
	case errors.Is(err, operations.ErrUploadQuotaExceeded):
		writeJSON(w, http.StatusTooManyRequests, envelopeError("quota_exceeded", err.Error()))
	case errors.Is(err, operations.ErrTempQuotaExceeded):
		writeJSON(w, http.StatusTooManyRequests, envelopeError("quota_exceeded", err.Error()))
	case errors.Is(err, operations.ErrUploadStorageNotSet):
		writeJSON(w, http.StatusServiceUnavailable, envelopeError("upload-storage-unavailable", err.Error()))
	default:
		writeJSON(w, http.StatusBadRequest, envelopeError("error", err.Error()))
	}
}

func envelopeError(code, message string) map[string]any {
	return map[string]any{"ok": false, "error": map[string]string{"code": code, "message": operations.SafeErrorMessage(message)}}
}
