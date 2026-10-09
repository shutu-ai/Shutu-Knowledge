package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shutu-ai/shutu-knowledge/internal/agentquery"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

// Input DTOs use snake_case JSON fields per the plan protocol examples. The
// official SDK derives each input schema from these structs, so adding a field
// with a jsonschema description is the only change needed to widen a tool.
type mcpVersionPolicy struct {
	Type        string `json:"type,omitempty" jsonschema:"current, all, as_of, or range"`
	AsOf        string `json:"as_of,omitempty" jsonschema:"as_of version identity"`
	FromVersion string `json:"from_version,omitempty" jsonschema:"range start (inclusive)"`
	ToVersion   string `json:"to_version,omitempty" jsonschema:"range end (inclusive)"`
}

func versionPolicyFromInput(policy *mcpVersionPolicy) agentquery.VersionPolicy {
	if policy == nil {
		return agentquery.VersionPolicy{}
	}
	return agentquery.VersionPolicy{
		Type: agentquery.DocumentVersionPolicy(policy.Type),
		AsOf: policy.AsOf, FromVersion: policy.FromVersion, ToVersion: policy.ToVersion,
	}
}
func generationFromInput(generation string) agentquery.Generation {
	if strings.TrimSpace(generation) == "" {
		return agentquery.GenerationActive
	}
	return agentquery.Generation(generation)
}
func relationTypesFromInput(types []string) ([]semantic.RelationType, error) {
	out := make([]semantic.RelationType, 0, len(types))
	for _, relationType := range types {
		switch semantic.RelationType(relationType) {
		case semantic.RelationDerivedFrom, semantic.RelationPartOf, semantic.RelationMentions,
			semantic.RelationRelatedTo, semantic.RelationSupports, semantic.RelationContradicts,
			semantic.RelationSupersedes, semantic.RelationSameTopic:
			out = append(out, semantic.RelationType(relationType))
		default:
			return nil, errInvalid("unsupported relation type %q", relationType)
		}
	}
	return out, nil
}

// runTool wraps one tool execution with a fresh trace id, trace recording, and
// the single envelope contract. Application-level errors are returned as
// IsError results carrying the envelope; only infrastructure failures return
// a Go error so the SDK maps them onto the transport.
// runTool wraps one tool execution with the request limits (concurrency slot +
// per-call deadline), a fresh trace id, trace recording, and the single
// envelope contract. Application-level errors are returned as IsError results
// carrying the envelope; only infrastructure failures return a Go error.
func runTool[In any](s *server, name string, ctx context.Context, input In, run func(ctx context.Context, traceID string) (*Envelope, error)) (*mcp.CallToolResult, any, error) {
	release, err := s.limiter.acquire(ctx)
	if err != nil {
		if errors.Is(err, errDeadline) {
			return errorToolResult(errorEnvelope(newTraceID(), CodeResourceLimit, "too many concurrent tool calls; retry after the current calls finish")), nil, nil
		}
		return nil, nil, err
	}
	defer release()
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	startedAt := time.Now()
	traceID := newTraceID()
	envelope, err := run(callCtx, traceID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return errorToolResult(errorEnvelope(traceID, CodeTimeout, "tool call exceeded its deadline; results are partial and no citation in it should be trusted as complete")), nil, nil
		}
		return nil, nil, err
	}
	record := traceRecord{
		TraceID: traceID, Tool: name, StartedAt: startedAt,
		ElapsedMS: time.Since(startedAt).Milliseconds(),
	}
	if envelope.Context != nil {
		record.BaseIDs = envelope.Context.BaseIDs
		record.EffectiveMode = envelope.Context.EffectiveMode
	}
	record.Degraded = envelope.Degraded
	record.WarningCodes = envelope.Warnings
	if envelope.Error != nil {
		record.ErrorCode = string(envelope.Error.Code)
	}
	s.traces.record(record)
	if envelope.Error != nil {
		return errorToolResult(envelope), nil, nil
	}
	result := &mcp.CallToolResult{}
	result.Content = []mcp.Content{&mcp.TextContent{Text: marshalEnvelope(envelope)}}
	result.StructuredContent = envelope
	return result, nil, nil
}
func errorToolResult(envelope *Envelope) *mcp.CallToolResult {
	result := &mcp.CallToolResult{IsError: true}
	result.Content = []mcp.Content{&mcp.TextContent{Text: marshalEnvelope(envelope)}}
	result.StructuredContent = envelope
	return result
}

// semanticCompilation loads the active or pinned immutable compilation for one
// base through the knowledge core. Missing generations map to the stable
// GENERATION_NOT_FOUND code.
func (s *server) semanticCompilation(ctx context.Context, baseID string, generation agentquery.Generation) (semantic.Compilation, error) {
	if generation == agentquery.GenerationActive || generation == "" {
		return s.knowledge.GetSemanticCompilation(ctx, baseID)
	}
	number, err := strconv.ParseInt(string(generation), 10, 64)
	if err != nil || number <= 0 {
		return semantic.Compilation{}, &agentquery.Error{
			Code:    agentquery.ErrGenerationNotFound,
			Message: "generation must be active or a positive integer",
		}
	}
	compilation, err := s.knowledge.GetSemanticCompilationByID(ctx, baseID, number)
	if err != nil {
		return semantic.Compilation{}, &agentquery.Error{
			Code: agentquery.ErrGenerationNotFound, BaseID: baseID,
			Message: "semantic generation is unavailable",
		}
	}
	return compilation, nil
}
func compilationUnitByID(compilation semantic.Compilation, unitID string) (semantic.Unit, bool) {
	for _, unit := range compilation.Units {
		if unit.ID == unitID {
			return unit, true
		}
	}
	return semantic.Unit{}, false
}
func chunkByID(chunks []knowledge.Chunk, chunkID string) (knowledge.Chunk, bool) {
	for _, chunk := range chunks {
		if chunk.ID == chunkID {
			return chunk, true
		}
	}
	return knowledge.Chunk{}, false
}
func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
func sortStrings(values []string) { sort.Strings(values) }

// errInvalid builds one argument-level error mapped by callers.
func errInvalid(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
