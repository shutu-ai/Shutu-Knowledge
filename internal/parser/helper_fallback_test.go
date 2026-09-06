package parser

import (
	"context"
	"errors"
	"testing"
)

type fallbackFake struct {
	name      string
	available bool
	text      string
	err       error
	calls     int
}

func (h *fallbackFake) Available() bool { return h.available }

func (h *fallbackFake) Run(context.Context, string, []byte) (string, error) {
	h.calls++
	if h.err != nil {
		return "", h.err
	}
	return h.text, nil
}

func TestFallbackHelperPrefersHealthyPrimary(t *testing.T) {
	primary := &fallbackFake{name: "paddle", available: true, text: "primary"}
	fallback := &fallbackFake{name: "tesseract", available: true, text: "secondary"}
	helper := FallbackHelper{Primary: primary, Fallback: fallback}

	text, err := helper.Run(context.Background(), "pdf", []byte("scan"))
	if err != nil || text != "primary" || primary.calls != 1 || fallback.calls != 0 {
		t.Fatalf("primary path: text=%q calls=%d/%d err=%v", text, primary.calls, fallback.calls, err)
	}
}

func TestFallbackHelperRunsSecondaryAfterPrimaryFailure(t *testing.T) {
	primaryErr := errors.New("paddle model failed")
	primary := &fallbackFake{name: "paddle", available: true, err: primaryErr}
	fallback := &fallbackFake{name: "tesseract", available: true, text: "secondary"}
	helper := FallbackHelper{Primary: primary, Fallback: fallback}

	text, err := helper.Run(context.Background(), "pdf", []byte("scan"))
	if err != nil || text != "secondary" || primary.calls != 1 || fallback.calls != 1 {
		t.Fatalf("fallback path: text=%q calls=%d/%d err=%v", text, primary.calls, fallback.calls, err)
	}
}

func TestFallbackHelperFailsClosedWithoutFallback(t *testing.T) {
	primary := &fallbackFake{name: "paddle", available: true, err: errors.New("primary failed")}
	helper := FallbackHelper{Primary: primary, Fallback: NoopHelper{}}
	_, err := helper.Run(context.Background(), "pdf", []byte("scan"))
	if err == nil || err.Error() != "primary failed; no OCR fallback is available" {
		t.Fatalf("missing fallback error: %v", err)
	}
}
