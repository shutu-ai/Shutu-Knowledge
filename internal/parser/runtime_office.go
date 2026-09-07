package parser

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

// RuntimeOfficeHelper adapts the pinned MIT anydoc native package in the
// managed runtime to the existing legacy parser surface.
type RuntimeOfficeHelper struct {
	manager runtime.Caller
}

func NewRuntimeOfficeHelper(manager runtime.Caller) *RuntimeOfficeHelper {
	if manager == nil || !manager.Configured(runtime.CapabilityOffice) {
		return nil
	}
	return &RuntimeOfficeHelper{manager: manager}
}

func (h *RuntimeOfficeHelper) Available() bool {
	return h != nil && h.manager != nil && h.manager.Configured(runtime.CapabilityOffice)
}

func (h *RuntimeOfficeHelper) Run(ctx context.Context, format string, input []byte) (string, error) {
	if !h.Available() {
		return "", fmt.Errorf("managed Office runtime is not configured")
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := h.manager.Call(ctx, runtime.CapabilityOffice, map[string]any{
		"format": format,
		"data":   base64.StdEncoding.EncodeToString(input),
	}, &payload); err != nil {
		return "", err
	}
	if payload.Text == "" {
		return "", fmt.Errorf("managed Office runtime returned empty text")
	}
	return payload.Text, nil
}
