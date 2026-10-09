package skill

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoSkillDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve repo root")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	return filepath.Join(root, "skills", "shutu-knowledge-research")
}

// TestValidateShippedSkillPackage is the G4 lint gate for the shipped skill.
func TestValidateShippedSkillPackage(t *testing.T) {
	findings, err := Validate(repoSkillDir(t))
	if err != nil {
		t.Fatalf("validate skill package: %v", err)
	}
	if len(findings) != 0 {
		for _, finding := range findings {
			t.Errorf("%s: %s", finding.File, finding.Message)
		}
	}
}

func TestValidateRejectsUnknownToolName(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("SKILL.md", "---\nname: shutu-knowledge-research\ndescription: Search evidence, explore concepts, compare versions with citations.\n---\n\nBody mentions knowledge_compile.\n")
	write("tests/trigger-cases.md", triggerFile(10, 10))
	findings, err := Validate(dir)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	found := false
	for _, finding := range findings {
		if strings.Contains(finding.Message, "knowledge_compile") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an unknown-tool finding, got %+v", findings)
	}
}

func TestValidateRejectsMissingReferencesAndShortTriggers(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("SKILL.md", "---\nname: shutu-knowledge-research\ndescription: Search evidence, explore concepts, compare versions with citations.\n---\n\nSee `references/missing.md`.\n")
	write("tests/trigger-cases.md", triggerFile(3, 4))
	findings, err := Validate(dir)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	joined := make([]string, 0, len(findings))
	for _, finding := range findings {
		joined = append(joined, finding.Message)
	}
	joinedAll := strings.Join(joined, "\n")
	if !strings.Contains(joinedAll, "missing.md") {
		t.Fatalf("expected missing-reference finding, got %+v", findings)
	}
	if !strings.Contains(joinedAll, "should-trigger") || !strings.Contains(joinedAll, "should-not-trigger") {
		t.Fatalf("expected short-trigger findings, got %+v", findings)
	}
}

// triggerFile builds a trigger-cases document with n yes and m no rows.
func triggerFile(n, m int) string {
	var builder strings.Builder
	builder.WriteString("# Trigger Test Cases\n\n## Should trigger\n\n")
	for i := 1; i <= n; i++ {
		builder.WriteString("| " + itoa(i) + " | prompt | reason |\n")
	}
	builder.WriteString("\n## Should not trigger\n\n")
	for i := 1; i <= m; i++ {
		builder.WriteString("| " + itoa(i) + " | prompt | reason |\n")
	}
	return builder.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}