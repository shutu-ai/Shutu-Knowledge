package models

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/httpx"
)

// OllamaModel is one model in the local Ollama registry.
type OllamaModel struct {
	Name       string    `json:"name"`
	SizeBytes  int64     `json:"size"`
	Digest     string    `json:"digest"`
	ModifiedAt time.Time `json:"modified_at"`
}

// Ollama manages models on an already-running Ollama endpoint. It never
// changes Knowledge configuration as a side effect of browsing or pulling.
type Ollama struct {
	baseURL string
	client  *http.Client
}

// NewOllama creates an API client. An empty base URL selects the standard
// local endpoint.
func NewOllama(baseURL string, client *http.Client) *Ollama {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "http://127.0.0.1:11434"
	}
	if client == nil {
		client = httpx.NewClient(5 * time.Minute)
	}
	return &Ollama{baseURL: base, client: client}
}

// Tags lists installed models.
func (o *Ollama) Tags(ctx context.Context) ([]OllamaModel, error) {
	var payload struct {
		Models []OllamaModel `json:"models"`
	}
	if err := o.do(ctx, http.MethodGet, "/api/tags", nil, &payload); err != nil {
		return nil, err
	}
	if payload.Models == nil {
		payload.Models = []OllamaModel{}
	}
	return payload.Models, nil
}

// Pull streams a model download. Progress is whole-percentage when Ollama
// supplies totals; otherwise it reports completion at the end.
func (o *Ollama) Pull(ctx context.Context, model string, report func(progress int)) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("ollama model is required")
	}
	body, err := json.Marshal(map[string]any{"model": model, "stream": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama pull failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama pull failed: HTTP %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var update struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &update); err != nil {
			return fmt.Errorf("ollama pull update decode: %w", err)
		}
		if update.Error != "" {
			return fmt.Errorf("ollama pull failed: %s", update.Error)
		}
		if update.Total > 0 && report != nil {
			progress := int(float64(update.Completed) / float64(update.Total) * 100)
			if progress < 0 {
				progress = 0
			}
			if progress > 99 {
				progress = 99
			}
			report(progress)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ollama pull stream: %w", err)
	}
	report(100)
	return nil
}

// Delete removes a model from the Ollama registry.
func (o *Ollama) Delete(ctx context.Context, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("ollama model is required")
	}
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return err
	}
	return o.do(ctx, http.MethodDelete, "/api/delete", body, nil)
}

func (o *Ollama) do(ctx context.Context, method, path string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, o.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ollama request failed: HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("ollama response decode: %w", err)
	}
	return nil
}
