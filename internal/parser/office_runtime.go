package parser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LibreOfficeHelper is a Knowledge-owned adapter for the system LibreOffice
// runtime. It discovers the executable and invokes it directly; users never
// need to write a converter script or configure a helper command.
type LibreOfficeHelper struct {
	path      string
	extraArgs []string // test-only argv prefix for an injected executable
}

// NewLibreOfficeHelper discovers the standard LibreOffice executable names
// and Windows installation paths. Discovery is repeated by Available so a
// newly installed system dependency is picked up without restarting config.
func NewLibreOfficeHelper() *LibreOfficeHelper {
	return &LibreOfficeHelper{}
}

func (h *LibreOfficeHelper) executable() string {
	if h == nil {
		return ""
	}
	if h.path != "" {
		if _, err := os.Stat(h.path); err == nil {
			return h.path
		}
	}
	for _, name := range []string{"soffice", "libreoffice"} {
		if path, err := exec.LookPath(name); err == nil {
			h.path = path
			return path
		}
	}
	if os.PathSeparator == '\\' {
		for _, path := range []string{
			`C:\Program Files\LibreOffice\program\soffice.exe`,
			`C:\Program Files (x86)\LibreOffice\program\soffice.exe`,
		} {
			if _, err := os.Stat(path); err == nil {
				h.path = path
				return path
			}
		}
	}
	return ""
}

func (h *LibreOfficeHelper) Available() bool { return h.executable() != "" }

// Path returns the detected executable path for Doctor and diagnostics.
func (h *LibreOfficeHelper) Path() string { return h.executable() }

// Version returns the first version line reported by LibreOffice.
func (h *LibreOfficeHelper) Version(ctx context.Context) string {
	path := h.executable()
	if path == "" {
		return ""
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(callCtx, path, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func (h *LibreOfficeHelper) Run(ctx context.Context, format string, input []byte) (string, error) {
	path := h.executable()
	if path == "" {
		return "", fmt.Errorf("LibreOffice is not detected; install LibreOffice and restart or rerun doctor")
	}
	if format != "doc" && format != "ppt" && format != "xls" {
		return "", fmt.Errorf("LibreOffice legacy format %q is unsupported", format)
	}
	workspace, err := os.MkdirTemp("", "shutu-knowledge-office-*")
	if err != nil {
		return "", fmt.Errorf("create LibreOffice workspace: %w", err)
	}
	defer os.RemoveAll(workspace)
	inputPath := filepath.Join(workspace, "input."+format)
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		return "", fmt.Errorf("write legacy Office input: %w", err)
	}
	profile := filepath.Join(workspace, "profile")
	profileURL := "file:///" + strings.ReplaceAll(filepath.ToSlash(profile), " ", "%20")
	callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	args := append([]string{}, h.extraArgs...)
	args = append(args,
		"--headless", "--nologo", "--nodefault", "--nolockcheck", "--norestore",
		"-env:UserInstallation="+profileURL,
		"--convert-to", "txt:Text", "--outdir", workspace, inputPath,
	)
	command := exec.CommandContext(callCtx, path, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		if callCtx.Err() != nil {
			return "", fmt.Errorf("LibreOffice conversion canceled: %w", callCtx.Err())
		}
		return "", fmt.Errorf("LibreOffice conversion failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	textPath := filepath.Join(workspace, "input.txt")
	text, err := os.ReadFile(textPath)
	if err != nil {
		return "", fmt.Errorf("LibreOffice produced no text output: %w: %s", err, strings.TrimSpace(string(output)))
	}
	text = []byte(strings.TrimSpace(string(text)))
	if len(text) == 0 {
		return "", fmt.Errorf("LibreOffice produced empty text output")
	}
	return string(text), nil
}
