package parser

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

// PageRenderer is the bounded full-page PDF rasterization surface used by
// OCR. ExecHelper and RuntimeRenderer both implement it, so configuration and
// the Knowledge pipeline do not fork for managed versus explicit runtimes.
type PageRenderer interface {
	Available() bool
	DecodeLimit(ctx context.Context, format string, input []byte, limit int) ([]byte, error)
}

// RuntimeRenderer forwards a PDF to the Knowledge-managed PDF.js runtime and
// returns its validated JSON/PNG envelope to the existing OCR pipeline.
type RuntimeRenderer struct {
	manager runtime.Caller
}

func NewRuntimeRenderer(manager runtime.Caller) *RuntimeRenderer {
	if manager == nil || !manager.Configured(runtime.CapabilityPDFRender) {
		return nil
	}
	return &RuntimeRenderer{manager: manager}
}

func (r *RuntimeRenderer) Available() bool {
	return r != nil && r.manager != nil && r.manager.Configured(runtime.CapabilityPDFRender)
}

func (r *RuntimeRenderer) DecodeLimit(ctx context.Context, format string, input []byte, limit int) ([]byte, error) {
	if !r.Available() {
		return nil, &runtime.Error{Code: "runtime_unconfigured", Message: "PDF renderer runtime is not configured"}
	}
	var payload struct {
		Pages []struct {
			Page int    `json:"page"`
			PNG  string `json:"png"`
		} `json:"pages"`
	}
	if err := r.manager.Call(ctx, runtime.CapabilityPDFRender, map[string]any{
		"format": format,
		"data":   base64.StdEncoding.EncodeToString(input),
		"limit":  limit,
	}, &payload); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}
