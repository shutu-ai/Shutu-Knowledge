package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
	"github.com/shutu-ai/shutu-knowledge/internal/httpx"
)

// URL fetch limits (audited reference posture: bounded body, hard timeout).
const (
	urlFetchTimeout = 30 * time.Second
	urlMaxBytes     = 10 << 20
	urlUserAgent    = "shutu-knowledge/0.1 (+knowledge ingestion)"
)

// FetchURL downloads one URL and returns (finalURL, bytes, contentType).
func FetchURL(ctx context.Context, rawURL string) (string, []byte, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", nil, "", fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", nil, "", fmt.Errorf("only http/https urls are supported")
	}
	client := httpx.NewClient(urlFetchTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", nil, "", err
	}
	req.Header.Set("User-Agent", urlUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/*;q=0.9,*/*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, "", fmt.Errorf("fetch failed: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, urlMaxBytes+1))
	if err != nil {
		return "", nil, "", fmt.Errorf("read body: %w", err)
	}
	if len(body) > urlMaxBytes {
		return "", nil, "", fmt.Errorf("page exceeds %d MB limit", urlMaxBytes>>20)
	}
	contentType := resp.Header.Get("Content-Type")
	return resp.Request.URL.String(), body, contentType, nil
}

// fileNameForURL derives a parser dispatch name from the URL path; HTML is
// the default so extension-less pages still parse.
func fileNameForURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "page.html"
	}
	base := path.Base(parsed.Path)
	if base == "/" || base == "." || base == "" || path.Ext(base) == "" {
		return "page.html"
	}
	return base
}

// AddUrlDocument fetches a page and imports it (title from the page when absent).
func (s *Service) AddUrlDocument(ctx context.Context, baseID, rawURL, title string) (Document, error) {
	if _, err := s.store.getBase(baseID); err != nil {
		return Document{}, err
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return Document{}, fmt.Errorf("url is required")
	}
	finalURL, body, _, err := FetchURL(ctx, rawURL)
	if err != nil {
		return Document{}, err
	}
	doc := s.newDocument(baseID, strings.TrimSpace(title), "url")
	doc.TitleLocked = strings.TrimSpace(title) != ""
	doc.URL = finalURL
	doc.FileName = fileNameForURL(finalURL)
	if err := s.ingestFetched(ctx, &doc, body); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// RefreshUrlDocument re-fetches one URL document; unchanged content skips
// re-chunking (incremental update).
func (s *Service) RefreshUrlDocument(ctx context.Context, id string) (bool, Document, error) {
	doc, err := s.store.getDocument(id)
	if err != nil {
		return false, Document{}, err
	}
	if doc.SourceType != "url" || doc.URL == "" {
		return false, Document{}, fmt.Errorf("document %s is not a url source", id)
	}
	_, body, _, err := FetchURL(ctx, doc.URL)
	if err != nil {
		return false, Document{}, err
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	if doc.ContentHash == hash {
		return false, doc, nil
	}
	if err := s.ingestFetched(ctx, &doc, body); err != nil {
		return false, doc, err
	}
	return true, doc, nil
}

// ingestFetched parses fetched bytes and runs the standard ingest path.
func (s *Service) ingestFetched(ctx context.Context, doc *Document, body []byte) error {
	base, err := s.store.getBase(doc.BaseID)
	if err != nil {
		return err
	}
	parsed, err := s.parsers.Parse(doc.FileName, body)
	if err != nil {
		return s.failDocument(doc, ErrParseFailed, err)
	}
	if parsed.Title != "" && !doc.TitleLocked {
		doc.Title = parsed.Title
	}
	sum := sha256.Sum256(body)
	doc.ContentHash = hex.EncodeToString(sum[:])
	doc.RawText = chunk.Normalize(parsed.Text)
	return s.ingest(ctx, doc, base.Config, nil)
}

// RefreshStaleURLs refreshes every URL document older than its base's
// configured interval (urlRefreshHours; 0 = off). Returns per-doc errors.
func (s *Service) RefreshStaleURLs(ctx context.Context, now int64) []error {
	bases, err := s.store.listBases()
	if err != nil {
		return []error{err}
	}
	var failures []error
	for _, base := range bases {
		intervalHours := ResolveBaseConfig(s.global, base.Config).URLRefreshHours
		if intervalHours <= 0 {
			continue
		}
		cutoff := now - int64(intervalHours)*3_600_000
		docs, err := s.store.listDocuments(base.ID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, doc := range docs {
			if doc.SourceType != "url" {
				continue
			}
			updatedAt := doc.UpdatedAt
			if updatedAt == 0 {
				updatedAt = doc.CreatedAt
			}
			if updatedAt > cutoff {
				continue
			}
			if _, _, err := s.RefreshUrlDocument(ctx, doc.ID); err != nil {
				failures = append(failures, fmt.Errorf("refresh %s: %w", doc.URL, err))
			}
		}
	}
	return failures
}
