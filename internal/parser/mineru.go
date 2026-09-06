package parser

import (
	"archive/zip"
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

// MinerU settings for the optional remote document processor.
type MineruSettings struct {
	APIKey  string
	APIHost string // empty = https://mineru.net
	Client  *http.Client
}

const (
	mineruPollInterval = 5 * time.Second
	mineruExtractLimit = 30 * time.Minute
)

type mineruEnvelope struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
	Msg  string          `json:"msg"`
}

type mineruBatchData struct {
	BatchID  string              `json:"batch_id"`
	FileURLs []string            `json:"file_urls"`
	Headers  []map[string]string `json:"headers"`
}

type mineruExtractResult struct {
	State      string `json:"state"`
	ErrMsg     string `json:"err_msg"`
	FullZipURL string `json:"full_zip_url"`
}

func (m MineruSettings) host() string {
	host := strings.TrimRight(strings.TrimSpace(m.APIHost), "/")
	if host == "" {
		host = "https://mineru.net"
	}
	return host
}

func (m MineruSettings) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return httpx.NewClient(120 * time.Second)
}

func (m MineruSettings) apiJSON(ctx context.Context, method, url string, body []byte, extraHeaders map[string]string) (mineruEnvelope, error) {
	var envelope mineruEnvelope
	req, err := http.NewRequestWithContext(ctx, method, url, bodyBytes(body))
	if err != nil {
		return envelope, err
	}
	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	req.Header.Set("Accept", "*/*")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}
	resp, err := m.client().Do(req)
	if err != nil {
		return envelope, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return envelope, fmt.Errorf("mineru request failed: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return envelope, fmt.Errorf("mineru response decode: %w", err)
	}
	return envelope, nil
}

func bodyBytes(body []byte) io.Reader {
	if body == nil {
		return nil
	}
	return bytes.NewReader(body)
}

// ExtractPDFWithMineru sends one PDF through the remote processor and
// returns the recovered Markdown. Any failure is an error so the caller can
// fall back to the local parse + OCR chain.
func ExtractPDFWithMineru(ctx context.Context, fileName string, pdf []byte, settings MineruSettings) (string, error) {
	if strings.TrimSpace(settings.APIKey) == "" {
		return "", fmt.Errorf("mineru api key is empty")
	}
	host := settings.host()

	// 1. Create the batch task and collect the signed upload URL.
	createBody, err := json.Marshal(map[string]any{
		"files": []map[string]any{{"name": fileName, "data_id": "shutu-knowledge"}},
	})
	if err != nil {
		return "", err
	}
	envelope, err := settings.apiJSON(ctx, http.MethodPost, host+"/api/v4/file-urls/batch", createBody, nil)
	if err != nil {
		return "", err
	}
	if envelope.Code != 0 {
		return "", fmt.Errorf("mineru batch create failed: %s", envelope.Msg)
	}
	var batch mineruBatchData
	if err := json.Unmarshal(envelope.Data, &batch); err != nil {
		return "", fmt.Errorf("mineru batch data: %w", err)
	}
	if batch.BatchID == "" || len(batch.FileURLs) == 0 {
		return "", fmt.Errorf("mineru batch create returned no upload URL")
	}

	// 2. Upload the bytes to the signed URL.
	var headers map[string]string
	if len(batch.Headers) > 0 {
		headers = batch.Headers[0]
	}
	upload, err := settings.upload(ctx, batch.FileURLs[0], pdf, headers)
	if err != nil {
		return "", err
	}
	if upload.StatusCode < 200 || upload.StatusCode >= 300 {
		return "", fmt.Errorf("mineru upload failed: HTTP %d", upload.StatusCode)
	}
	_ = upload.Body.Close()

	// 3. Poll until done (or failed/aborted).
	deadline := time.Now().Add(mineruExtractLimit)
	for {
		if ctx.Err() != nil {
			return "", fmt.Errorf("mineru extraction aborted: %w", ctx.Err())
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("mineru extract timed out")
		}
		time.Sleep(mineruPollInterval)
		poll, err := settings.apiJSON(ctx, http.MethodGet, host+"/api/v4/extract-results/batch/"+batch.BatchID, nil, nil)
		if err != nil {
			return "", err
		}
		if poll.Code != 0 {
			return "", fmt.Errorf("mineru poll failed: %s", poll.Msg)
		}
		var payload struct {
			ExtractResult []mineruExtractResult `json:"extract_result"`
		}
		if err := json.Unmarshal(poll.Data, &payload); err != nil {
			return "", fmt.Errorf("mineru poll data: %w", err)
		}
		if len(payload.ExtractResult) == 0 {
			continue
		}
		result := payload.ExtractResult[0]
		switch result.State {
		case "done":
			if result.FullZipURL == "" {
				return "", fmt.Errorf("mineru extract done without a result zip")
			}
			return settings.downloadMarkdown(ctx, result.FullZipURL)
		case "failed":
			return "", fmt.Errorf("mineru extract failed: %s", result.ErrMsg)
		}
	}
}

func (m MineruSettings) upload(ctx context.Context, url string, body []byte, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return m.client().Do(req)
}

func (m MineruSettings) downloadMarkdown(ctx context.Context, zipURL string) (string, error) {
	download, err := m.client().Do(mustRequest(ctx, zipURL))
	if err != nil {
		return "", fmt.Errorf("mineru result download failed: %w", err)
	}
	defer download.Body.Close()
	if download.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mineru result download failed: HTTP %d", download.StatusCode)
	}
	data, err := io.ReadAll(download.Body)
	if err != nil {
		return "", err
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("mineru result zip: %w", err)
	}
	for _, file := range reader.File {
		name := strings.ReplaceAll(file.Name, "\\", "/")
		if file.FileInfo().IsDir() || strings.Contains(name, "__assets__") || !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		content, err := file.Open()
		if err != nil {
			continue
		}
		text, err := io.ReadAll(content)
		_ = content.Close()
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(text)) != "" {
			return string(text), nil
		}
	}
	return "", fmt.Errorf("mineru result zip contains no markdown")
}

func mustRequest(ctx context.Context, url string) *http.Request {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		panic(err) // static URL construction; unreachable in practice
	}
	return req
}
