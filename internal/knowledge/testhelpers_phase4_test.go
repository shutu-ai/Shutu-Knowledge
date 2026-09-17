package knowledge

import (
	"archive/zip"
	"bytes"

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
