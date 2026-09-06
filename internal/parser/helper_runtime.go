package parser

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

// RuntimeHelper sends OCR work to the isolated ML helper process. Unlike
// ExecHelper, bytes cross a typed JSON channel instead of a temp file.
type RuntimeHelper struct {
	manager   runtime.Caller
	artifacts func() (path string, ready bool)
}

// NewRuntimeHelper adapts the shared runtime manager to the parser helper
// contract. It returns nil when no OCR runtime is configured.
func NewRuntimeHelper(manager runtime.Caller) *RuntimeHelper {
	if manager == nil || !manager.Configured(runtime.CapabilityOCR) {
		return nil
	}
	return &RuntimeHelper{manager: manager}
}

// NewRuntimeHelperWithArtifacts additionally gates calls on a complete model
// artifact bundle and passes its absolute directory to the helper contract.
func NewRuntimeHelperWithArtifacts(manager runtime.Caller, artifacts func() (string, bool)) *RuntimeHelper {
	helper := NewRuntimeHelper(manager)
	if helper == nil {
		return nil
	}
	helper.artifacts = artifacts
	return helper
}

func (h *RuntimeHelper) Available() bool {
	if h == nil || h.manager == nil || !h.manager.Configured(runtime.CapabilityOCR) {
		return false
	}
	if h.artifacts == nil {
		return true
	}
	_, ready := h.artifacts()
	return ready
}

func (h *RuntimeHelper) Run(ctx context.Context, format string, input []byte) (string, error) {
	if !h.Available() {
		return "", fmt.Errorf("OCR runtime is not configured")
	}
	params := map[string]any{
		"format": format,
		"data":   base64.StdEncoding.EncodeToString(input),
	}
	if h.artifacts != nil {
		if path, ready := h.artifacts(); ready {
			params["modelPath"] = path
		}
	}
	var payload struct {
		Text string `json:"text"`
	}
	err := h.manager.Call(ctx, runtime.CapabilityOCR, params, &payload)
	if err != nil {
		return "", fmt.Errorf("OCR runtime: %w", err)
	}
	return payload.Text, nil
}
