package knowledge

import (
	"fmt"
	"regexp"
	"strings"
)

// CustomReranker is an experimental Hugging Face cross-encoder registered by
// the user. Registration records intent; it never means artifacts or runtime
// readiness.
type CustomReranker struct {
	ID      string `json:"id"`
	AddedAt int64  `json:"addedAt"`
}

// RerankSelfTest is the latest result for a reranker artifact set. It is only
// current while the downloaded model's artifact bytes remain unchanged.
type RerankSelfTest struct {
	ID            string    `json:"id"`
	Healthy       bool      `json:"healthy"`
	LatencyMS     int64     `json:"latencyMs"`
	Scores        []float64 `json:"scores,omitempty"`
	Error         string    `json:"error,omitempty"`
	CheckedAt     int64     `json:"checkedAt"`
	ArtifactBytes int64     `json:"artifactBytes"`
	DownloadedAt  int64     `json:"downloadedAt"`
	ArtifactCount int       `json:"artifactCount"`
	Current       bool      `json:"current"`
}

var huggingFaceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)?$`)

func normalizeHuggingFaceID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if len(id) < 3 || len(id) > 200 || strings.Contains(id, "..") ||
		strings.Contains(id, "\\") || strings.HasPrefix(id, "/") || strings.HasSuffix(id, "/") ||
		!huggingFaceIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid Hugging Face repository id; expected \"owner/model\" without paths or \"..\"")
	}
	return id, nil
}

// ListCustomRerankers returns user-registered experimental rerankers.
func (s *Service) ListCustomRerankers() ([]CustomReranker, error) {
	out := []CustomReranker{}
	if err := s.kvGet("custom_rerankers", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RegisterCustomReranker records an experimental local reranker. Download,
// artifact validation, and runtime self-test remain separate steps.
func (s *Service) RegisterCustomReranker(id string) (CustomReranker, error) {
	id, err := normalizeHuggingFaceID(id)
	if err != nil {
		return CustomReranker{}, err
	}
	items, err := s.ListCustomRerankers()
	if err != nil {
		return CustomReranker{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	item := CustomReranker{ID: id, AddedAt: Now().UnixMilli()}
	items = append(items, item)
	return item, s.kvSet("custom_rerankers", items)
}

// DeleteCustomReranker removes an experimental registration.
func (s *Service) DeleteCustomReranker(id string) error {
	id, err := normalizeHuggingFaceID(id)
	if err != nil {
		return err
	}
	items, err := s.ListCustomRerankers()
	if err != nil {
		return err
	}
	next := items[:0]
	for _, item := range items {
		if item.ID != id {
			next = append(next, item)
		}
	}
	if err := s.kvSet("custom_rerankers", next); err != nil {
		return err
	}
	tests, err := s.rerankSelfTests()
	if err != nil {
		return err
	}
	delete(tests, id)
	return s.kvSet("rerank_self_tests", tests)
}

// SaveRerankSelfTest persists the latest validation result for an artifact set.
func (s *Service) SaveRerankSelfTest(result RerankSelfTest) error {
	if strings.TrimSpace(result.ID) == "" {
		return fmt.Errorf("reranker id is required")
	}
	tests, err := s.rerankSelfTests()
	if err != nil {
		return err
	}
	result.CheckedAt = Now().UnixMilli()
	tests[result.ID] = result
	return s.kvSet("rerank_self_tests", tests)
}

// GetRerankSelfTest returns the latest persisted result and marks whether it
// still matches the supplied artifact envelope.
func (s *Service) GetRerankSelfTest(id string, artifactBytes, downloadedAt int64, artifactCount int) (RerankSelfTest, error) {
	tests, err := s.rerankSelfTests()
	if err != nil {
		return RerankSelfTest{}, err
	}
	result := tests[id]
	result.Current = result.Healthy && result.ArtifactBytes == artifactBytes &&
		result.DownloadedAt == downloadedAt && result.ArtifactCount == artifactCount
	return result, nil
}

func (s *Service) rerankSelfTests() (map[string]RerankSelfTest, error) {
	out := map[string]RerankSelfTest{}
	if err := s.kvGet("rerank_self_tests", &out); err != nil {
		return nil, err
	}
	return out, nil
}
