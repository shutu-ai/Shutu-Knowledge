package knowledge

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/parser"
)

func newTestRegistry(helper parser.HelperRunner) *parser.Registry {
	if helper == nil {
		return parser.NewRegistry()
	}
	return parser.NewRegistry(parser.WithLegacyHelper(helper))
}

func newExecHelper(template string) parser.ExecHelper {
	return parser.ExecHelper{Template: template}
}

func zipWithMarkdown() []byte {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entry, err := writer.Create("full.md")
	if err == nil {
		_, _ = entry.Write([]byte("# Recovered\n\nminedu markdown body"))
	}
	_ = writer.Close()
	return buf.Bytes()
}

func waitJob(t *testing.T, service *Service, jobID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, ok := service.jobMgr.Status(jobID)
		if !ok {
			t.Fatal("job missing")
		}
		switch job.Status {
		case "done":
			return
		case "failed":
			t.Fatalf("job failed: %s", job.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("job timeout: %+v", job)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitJobAllowFailed(t *testing.T, service *Service, jobID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, ok := service.jobMgr.Status(jobID)
		if !ok {
			t.Fatal("job missing")
		}
		switch job.Status {
		case "done", "failed":
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job timeout: %+v", job)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
