// Package skill validates the portable Shutu-Knowledge research skill package:
// frontmatter, referenced files, tool-name whitelist, and the trigger test set.
// It keeps the deliverable honest as docs and tools evolve.
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// AllowedTools is the complete read-only MCP tool surface. Any other
// knowledge_* name in the skill package is a contract violation.
var AllowedTools = map[string]bool{
	"knowledge_list_bases":       true,
	"knowledge_capabilities":     true,
	"knowledge_search":           true,
	"knowledge_explore":          true,
	"knowledge_expand":           true,
	"knowledge_evidence":         true,
	"knowledge_context":          true,
	"knowledge_compare_versions": true,
	"knowledge_diagnostics":      true,
}

// Frontmatter is the agentskills.io minimum: one name and one description.
type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Finding reports one lint problem.
type Finding struct {
	File    string
	Message string
}

// Validate lints the whole skill package rooted at dir.
func Validate(dir string) ([]Finding, error) {
	var findings []Finding
	skillPath := filepath.Join(dir, "SKILL.md")
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, err
	}
	content := string(raw)
	frontmatter, rest, err := parseFrontmatter(content)
	if err != nil {
		return append(findings, Finding{File: "SKILL.md", Message: err.Error()}), nil
	}
	if frontmatter.Name != "shutu-knowledge-research" {
		findings = append(findings, Finding{"SKILL.md",
			fmt.Sprintf("name = %q, want shutu-knowledge-research", frontmatter.Name)})
	}
	if strings.TrimSpace(frontmatter.Description) == "" {
		findings = append(findings, Finding{"SKILL.md", "description is empty"})
	}
	if len(frontmatter.Description) > 1024 {
		findings = append(findings, Finding{"SKILL.md",
			fmt.Sprintf("description is %d chars, want <= 1024", len(frontmatter.Description))})
	}
	for _, keyword := range []string{"evidence", "explore", "versions", "citations"} {
		if !strings.Contains(strings.ToLower(frontmatter.Description), keyword) {
			findings = append(findings, Finding{"SKILL.md",
				fmt.Sprintf("description lacks trigger keyword %q", keyword)})
		}
	}
	if len(rest) > 8000 {
		findings = append(findings, Finding{"SKILL.md",
			fmt.Sprintf("body is %d chars; keep the main skill lean and move detail into references/", len(rest))})
	}
	for _, reference := range referencedFiles(rest) {
		if _, err := os.Stat(filepath.Join(dir, reference)); err != nil {
			findings = append(findings, Finding{"SKILL.md",
				fmt.Sprintf("referenced file %q is missing", reference)})
		}
	}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		for _, name := range knowledgeToolNames(string(data)) {
			if !AllowedTools[name] {
				findings = append(findings, Finding{rel,
					fmt.Sprintf("unknown knowledge tool %q (not in the read-only MCP surface)", name)})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	triggerPath := filepath.Join(dir, "tests", "trigger-cases.md")
	triggerRaw, err := os.ReadFile(triggerPath)
	if err != nil {
		findings = append(findings, Finding{"tests/trigger-cases.md", "missing trigger test set"})
	} else {
		yes, no := countTriggerCases(string(triggerRaw))
		if yes < 10 {
			findings = append(findings, Finding{"tests/trigger-cases.md",
				fmt.Sprintf("only %d should-trigger cases, want >= 10", yes)})
		}
		if no < 10 {
			findings = append(findings, Finding{"tests/trigger-cases.md",
				fmt.Sprintf("only %d should-not-trigger cases, want >= 10", no)})
		}
	}
	return findings, nil
}

func parseFrontmatter(content string) (Frontmatter, string, error) {
	var frontmatter Frontmatter
	if !strings.HasPrefix(content, "---") {
		return frontmatter, content, fmt.Errorf("missing YAML frontmatter")
	}
	after := content[3:]
	after = strings.TrimLeft(after, "\r\n")
	end := strings.Index(after, "\n---")
	if end < 0 {
		return frontmatter, content, fmt.Errorf("unterminated YAML frontmatter")
	}
	block := after[:end]
	if err := yaml.Unmarshal([]byte(block), &frontmatter); err != nil {
		return frontmatter, content, fmt.Errorf("parse frontmatter: %w", err)
	}
	rest := after[end:]
	if idx := strings.Index(rest, "---"); idx >= 0 {
		rest = rest[idx+3:]
	}
	return frontmatter, strings.TrimSpace(rest), nil
}

var referencePattern = regexp.MustCompile("`((?:references|assets)/[A-Za-z0-9._/-]+\\.md)`")

func referencedFiles(body string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, match := range referencePattern.FindAllStringSubmatch(body, -1) {
		if !seen[match[1]] {
			seen[match[1]] = true
			out = append(out, match[1])
		}
	}
	return out
}

var toolPattern = regexp.MustCompile(`knowledge_[a-z_]+`)

func knowledgeToolNames(content string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, match := range toolPattern.FindAllString(content, -1) {
		if !seen[match] {
			seen[match] = true
			out = append(out, match)
		}
	}
	return out
}

// countTriggerCases counts numbered case rows under the should-trigger and
// should-not-trigger sections.
func countTriggerCases(content string) (int, int) {
	yes, no := 0, 0
	section := ""
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			switch {
			case strings.Contains(strings.ToLower(trimmed), "should trigger"):
				section = "yes"
			case strings.Contains(strings.ToLower(trimmed), "should not trigger"):
				section = "no"
			default:
				section = ""
			}
			continue
		}
		if section == "" || !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(trimmed, "|")
		if len(cells) < 3 {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimSpace(cells[1])); err != nil {
			continue
		}
		if section == "yes" {
			yes++
		} else {
			no++
		}
	}
	return yes, no
}