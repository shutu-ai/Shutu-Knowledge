// Package caption implements best-effort PDF image descriptions through
// OpenAI-compatible or Ollama vision models. Provider failures never remove
// or block the document text that was recovered by the parsing pipeline.
package caption

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/httpx"
	"github.com/shutu-ai/shutu-knowledge/internal/parser"
)

const (
	minCaptionEdge   = 160
	maxCaptionPixels = 4_000_000
	maxCaptionImages = 20
	captionPrompt    = "请用简洁的中文描述这张图片/图表的内容：说明它展示的主题、数据趋势或关键结论。若信息不足请直接说无法判断。"
)

// Config selects the vision provider. BaseURL empty means the embedding base
// URL for OpenAI-compatible services and localhost for Ollama.
type Config struct {
	Provider         string
	Model            string
	BaseURL          string
	APIKey           string
	EmbeddingBaseURL string
}

// Options permits tests and callers to inject deterministic dependencies.
type Options struct {
	HTTPClient     *http.Client
	ExtractImages  func([]byte) ([]parser.PDFImage, error)
	RequestTimeout time.Duration
}

// Result reports the searchable caption block and provider degradation.
type Result struct {
	Text       string
	ImageCount int
	Failures   int
	FirstError string
}

// PDFImages extracts, filters, and describes embedded PDF rasters.
func PDFImages(ctx context.Context, data []byte, cfg Config, opts Options) Result {
	if cfg.Provider == "off" || strings.TrimSpace(cfg.Model) == "" {
		return Result{}
	}
	if cfg.Provider != "openai" && cfg.Provider != "ollama" {
		return Result{Failures: 1, FirstError: "unsupported caption provider"}
	}
	extract := opts.ExtractImages
	if extract == nil {
		extract = func(data []byte) ([]parser.PDFImage, error) {
			return parser.ExtractPDFImages(data)
		}
	}
	images, err := extract(data)
	if err != nil {
		return Result{Failures: 1, FirstError: fmt.Sprintf("extract images: %v", err)}
	}
	selected := make([]parser.PDFImage, 0, len(images))
	for _, image := range images {
		if image.Width < minCaptionEdge || image.Height < minCaptionEdge {
			continue
		}
		if image.Width*image.Height > maxCaptionPixels {
			continue
		}
		selected = append(selected, image)
		if len(selected) == maxCaptionImages {
			break
		}
	}
	if len(selected) == 0 {
		return Result{}
	}

	result := Result{ImageCount: len(selected)}
	descriptions := make([]string, 0, len(selected))
	for _, image := range selected {
		text, err := CaptionImage(ctx, image.PNG, cfg, opts)
		if err != nil {
			result.Failures++
			if result.FirstError == "" {
				result.FirstError = err.Error()
			}
			continue
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			descriptions = append(descriptions, fmt.Sprintf("（第 %d 页图表描述）%s", image.Page, trimmed))
		}
	}
	if len(descriptions) > 0 {
		result.Text = "\n\n[文档图表描述]\n" + strings.Join(descriptions, "\n")
	}
	return result
}

// CaptionImage sends one PNG to the configured vision provider.
func CaptionImage(ctx context.Context, png []byte, cfg Config, opts Options) (string, error) {
	switch cfg.Provider {
	case "ollama":
		return captionOllama(ctx, png, cfg, opts)
	case "openai":
		return captionOpenAI(ctx, png, cfg, opts)
	default:
		return "", fmt.Errorf("unsupported caption provider %q", cfg.Provider)
	}
}

func captionOpenAI(ctx context.Context, png []byte, cfg Config, opts Options) (string, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(cfg.EmbeddingBaseURL)
	}
	if baseURL == "" {
		return "", fmt.Errorf("captioning base URL is empty (set it or the embedding base URL)")
	}
	payload := map[string]any{
		"model": strings.TrimSpace(cfg.Model),
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": captionPrompt},
				{"type": "image_url", "image_url": map[string]string{
					"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
				}},
			},
		}},
	}
	headers := map[string]string{"Authorization": "Bearer " + cfg.APIKey}
	if cfg.APIKey == "" {
		delete(headers, "Authorization")
	}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := callVision(ctx, opts, 120*time.Second, strings.TrimRight(baseURL, "/")+"/chat/completions",
		payload, headers, &response); err != nil {
		return "", err
	}
	if len(response.Choices) == 0 || response.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("caption response missing content")
	}
	return response.Choices[0].Message.Content, nil
}

func captionOllama(ctx context.Context, png []byte, cfg Config, opts Options) (string, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	payload := map[string]any{
		"model": strings.TrimSpace(cfg.Model),
		"messages": []map[string]any{{
			"role":    "user",
			"content": captionPrompt,
			"images":  []string{base64.StdEncoding.EncodeToString(png)},
		}},
		"stream": false,
	}
	var response struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := callVision(ctx, opts, 180*time.Second, strings.TrimRight(baseURL, "/")+"/api/chat",
		payload, nil, &response); err != nil {
		return "", err
	}
	if response.Message.Content == "" {
		return "", fmt.Errorf("ollama caption response missing content")
	}
	return response.Message.Content, nil
}

func callVision(ctx context.Context, opts Options, timeout time.Duration, endpoint string, payload, headers any, out any) error {
	if opts.RequestTimeout > 0 {
		timeout = opts.RequestTimeout
	}
	if timeout < 15*time.Second {
		timeout = 15 * time.Second
	}
	client := opts.HTTPClient
	if client == nil {
		client = httpx.NewClient(timeout)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode caption request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if headerMap, ok := headers.(map[string]string); ok {
		for key, value := range headerMap {
			request.Header.Set(key, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("caption request failed: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read caption response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("caption request failed: HTTP %d %s", response.StatusCode, truncateForError(responseBody))
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("decode caption response: %w", err)
	}
	return nil
}

func truncateForError(data []byte) string {
	if len(data) > 200 {
		data = data[:200]
	}
	return strings.TrimSpace(string(data))
}
