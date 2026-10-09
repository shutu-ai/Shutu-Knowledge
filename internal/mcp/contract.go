// Package mcp is the Model Context Protocol adapter for Shutu-Knowledge. It
// maps the official Go SDK protocol surface onto the shared agentquery
// application layer and never implements separate retrieval semantics. All
// tools are read-only; every result uses one stable envelope so stdio and
// streamable HTTP transports are wire-identical for the same input.
package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/agentquery"
)

// EnvelopeErrorCode is the transport-independent error taxonomy exposed by
// every tool. Codes mirror the shared application layer and stay stable.
type EnvelopeErrorCode string

const (
	CodeInvalidArgument    EnvelopeErrorCode = "INVALID_ARGUMENT"
	CodePermissionDenied   EnvelopeErrorCode = "PERMISSION_DENIED"
	CodeBaseNotFound       EnvelopeErrorCode = "BASE_NOT_FOUND"
	CodeSemanticNotReady   EnvelopeErrorCode = "SEMANTIC_NOT_READY"
	CodeGenerationNotFound EnvelopeErrorCode = "GENERATION_NOT_FOUND"
	CodeVersionUnsupported EnvelopeErrorCode = "VERSION_UNSUPPORTED"
	CodeUnitNotFound       EnvelopeErrorCode = "UNIT_NOT_FOUND"
	CodeEvidenceUnresolved EnvelopeErrorCode = "EVIDENCE_UNRESOLVED"
	CodeIndexNotReady      EnvelopeErrorCode = "INDEX_NOT_READY"
	CodeInternal           EnvelopeErrorCode = "INTERNAL_ERROR"
)

// Envelope is the unified response contract from plan section 6.10. Identical
// inputs produce identical envelopes on every transport.
type Envelope struct {
	Status   string           `json:"status"`
	TraceID  string           `json:"trace_id"`
	Data     any              `json:"data,omitempty"`
	Context  *EnvelopeContext `json:"context,omitempty"`
	Warnings []string         `json:"warnings,omitempty"`
	Degraded bool             `json:"degraded,omitempty"`
	Error    *EnvelopeError   `json:"error,omitempty"`
}

// EnvelopeContext carries the identity information every auditable answer
// needs: which bases answered and which semantic generation was used.
type EnvelopeContext struct {
	BaseIDs            []string `json:"base_ids,omitempty"`
	SemanticGeneration int64    `json:"semantic_generation,omitempty"`
	EffectiveMode      string   `json:"effective_mode,omitempty"`
}

// EnvelopeError is the stable machine-readable error object.
type EnvelopeError struct {
	Code    EnvelopeErrorCode `json:"code"`
	Message string            `json:"message"`
}

// okEnvelope wraps successful data.
func okEnvelope(traceID string, data any, context *EnvelopeContext, warnings []string, degraded bool) *Envelope {
	return &Envelope{
		Status: "ok", TraceID: traceID, Data: data, Context: context,
		Warnings: warnings, Degraded: degraded,
	}
}

// errorEnvelope wraps one stable application error.
func errorEnvelope(traceID string, code EnvelopeErrorCode, message string) *Envelope {
	return &Envelope{
		Status: "error", TraceID: traceID,
		Error: &EnvelopeError{Code: code, Message: message},
	}
}

// mapAgentQueryError converts one shared application error into its stable
// envelope representation. Unknown errors stay internal and opaque.
func mapAgentQueryError(err error) (EnvelopeErrorCode, string) {
	var appErr *agentquery.Error
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case agentquery.ErrInvalidArgument:
			return CodeInvalidArgument, appErr.Error()
		case agentquery.ErrPermissionDenied:
			return CodePermissionDenied, appErr.Error()
		case agentquery.ErrBaseNotFound:
			return CodeBaseNotFound, appErr.Error()
		case agentquery.ErrSemanticNotReady:
			return CodeSemanticNotReady, appErr.Error()
		case agentquery.ErrGenerationNotFound:
			return CodeGenerationNotFound, appErr.Error()
		case agentquery.ErrVersionUnsupported:
			return CodeVersionUnsupported, appErr.Error()
		case agentquery.ErrUnitNotFound:
			return CodeUnitNotFound, appErr.Error()
		}
	}
	return CodeInternal, err.Error()
}

// newTraceID returns one random, URL-safe trace identifier.
func newTraceID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("trace-%d", time.Now().UnixNano())
	}
	return "trace-" + hex.EncodeToString(buf)
}

const maxTraceRecords = 256

// traceRecord is one bounded diagnostic entry. It never stores query text or
// document content, only routing and degradation facts.
type traceRecord struct {
	TraceID       string    `json:"trace_id"`
	Tool          string    `json:"tool"`
	StartedAt     time.Time `json:"started_at"`
	ElapsedMS     int64     `json:"elapsed_ms"`
	BaseIDs       []string  `json:"base_ids,omitempty"`
	EffectiveMode string    `json:"effective_mode,omitempty"`
	Degraded      bool      `json:"degraded"`
	WarningCodes  []string  `json:"warning_codes,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
}

// traceRegistry keeps a bounded in-memory ring of recent tool executions for
// knowledge_diagnostics. It is per-process and intentionally never persisted.
type traceRegistry struct {
	mu      sync.Mutex
	records []traceRecord
}

func newTraceRegistry() *traceRegistry {
	return &traceRegistry{}
}

func (r *traceRegistry) record(record traceRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record)
	if len(r.records) > maxTraceRecords {
		r.records = r.records[len(r.records)-maxTraceRecords:]
	}
}

func (r *traceRegistry) lookup(traceID string) (traceRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.records) - 1; i >= 0; i-- {
		if r.records[i].TraceID == traceID {
			return r.records[i], true
		}
	}
	return traceRecord{}, false
}

func (r *traceRegistry) recent(limit int) []traceRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit <= 0 || limit > len(r.records) {
		limit = len(r.records)
	}
	out := make([]traceRecord, 0, limit)
	for i := len(r.records) - limit; i < len(r.records); i++ {
		out = append(out, r.records[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// clipText bounds one excerpt to maxChars and reports whether it was cut.
func clipText(text string, maxChars int) (string, bool) {
	runes := []rune(strings.TrimSpace(text))
	if maxChars <= 0 || len(runes) <= maxChars {
		return string(runes), false
	}
	return string(runes[:maxChars]), true
}

// contentHash makes excerpts independently verifiable.
func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256-" + hex.EncodeToString(sum[:16])
}

// warningCodes flattens shared diagnostics into deduplicated code strings so
// the envelope warnings stay bounded and stable.
func warningCodes(diagnostics []agentquery.Diagnostic) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, diagnostic := range diagnostics {
		code := diagnostic.Code
		if code == "" {
			continue
		}
		if !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	return out
}

// marshalEnvelope renders one envelope as the tool text content.
func marshalEnvelope(envelope *Envelope) string {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		encoded, _ = json.Marshal(errorEnvelope(envelope.TraceID, CodeInternal, "marshal envelope: "+err.Error()))
	}
	return string(encoded)
}
