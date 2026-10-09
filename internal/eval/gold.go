// Package eval implements the deterministic retrieval evaluation harness: it
// loads a gold set, replays questions through the shared agentquery layer, and
// produces frozen, auditable reports. It never needs an LLM, so toy runs are
// reproducible from a clean checkout.
package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GoldQuestion is one annotated benchmark entry (plan §12.3).
type GoldQuestion struct {
	QuestionID           string            `json:"question_id"`
	Question             string            `json:"question"`
	Language             string            `json:"language,omitempty"`
	VersionPolicy        string            `json:"version_policy,omitempty"`
	Mode                 string            `json:"mode,omitempty"`
	GoldAnswer           string            `json:"gold_answer,omitempty"`
	RequiredClaims       []string          `json:"required_claims,omitempty"`
	GoldEvidenceLocators []EvidenceLocator `json:"gold_evidence_locators,omitempty"`
	ForbiddenClaims      []string          `json:"forbidden_claims,omitempty"`
	ExpectedAbstain      bool              `json:"expected_abstain,omitempty"`
	Category             string            `json:"category"`
	Difficulty           string            `json:"difficulty,omitempty"`
	GoldVersion          string            `json:"gold_version,omitempty"`
}

// EvidenceLocator pins one piece of gold evidence to a document plus a
// distinctive substring that must appear in the retrieved chunk text.
type EvidenceLocator struct {
	Document string `json:"document"`
	Needle   string `json:"needle"`
}

// LoadGoldSet reads a JSONL gold set and validates every entry.
func LoadGoldSet(path string) ([]GoldQuestion, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var questions []GoldQuestion
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		var question GoldQuestion
		if err := json.Unmarshal([]byte(line), &question); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", filepath.Base(path), lineNumber, err)
		}
		if err := question.Validate(); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", filepath.Base(path), lineNumber, err)
		}
		questions = append(questions, question)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(questions) == 0 {
		return nil, fmt.Errorf("%s: gold set is empty", filepath.Base(path))
	}
	seen := map[string]bool{}
	for _, question := range questions {
		if seen[question.QuestionID] {
			return nil, fmt.Errorf("duplicate question_id %q", question.QuestionID)
		}
		seen[question.QuestionID] = true
	}
	return questions, nil
}

// Validate enforces the annotation contract. Missing evidence on answerable
// questions would make recall metrics meaningless; missing answers defeat the
// audit purpose.
func (q GoldQuestion) Validate() error {
	if strings.TrimSpace(q.QuestionID) == "" {
		return fmt.Errorf("question_id is required")
	}
	if strings.TrimSpace(q.Question) == "" {
		return fmt.Errorf("question is required")
	}
	if strings.TrimSpace(q.Category) == "" {
		return fmt.Errorf("category is required")
	}
	if q.ExpectedAbstain {
		if len(q.GoldEvidenceLocators) != 0 {
			return fmt.Errorf("%s: abstain questions carry no gold evidence", q.QuestionID)
		}
		return nil
	}
	if len(q.GoldEvidenceLocators) == 0 {
		return fmt.Errorf("%s: answerable questions need gold_evidence_locators", q.QuestionID)
	}
	for _, locator := range q.GoldEvidenceLocators {
		if strings.TrimSpace(locator.Document) == "" || strings.TrimSpace(locator.Needle) == "" {
			return fmt.Errorf("%s: evidence locators need document and needle", q.QuestionID)
		}
	}
	if strings.TrimSpace(q.GoldAnswer) == "" {
		return fmt.Errorf("%s: answerable questions need gold_answer", q.QuestionID)
	}
	return nil
}
