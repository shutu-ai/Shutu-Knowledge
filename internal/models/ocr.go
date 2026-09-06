package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// OCRModelID is the manifest-backed PaddleOCR artifact bundle. Inference is
// still owned by an optional helper; this bundle only makes that runtime
// deployable from the Web model workflow.
const OCRModelID = "PaddlePaddle/PP-OCRv5-mobile"

type ocrSource struct {
	artifact string
	repoPath string
	minBytes int64
	dict     bool
}

var ocrSources = []ocrSource{
	{
		artifact: "ppocrv5_det.onnx",
		repoPath: "/PaddlePaddle/PP-OCRv5_mobile_det_onnx/resolve/main/inference.onnx",
		minBytes: 1_000_000,
	},
	{
		artifact: "ppocrv5_rec.onnx",
		repoPath: "/PaddlePaddle/PP-OCRv5_mobile_rec_onnx/resolve/main/inference.onnx",
		minBytes: 1_000_000,
	},
	{
		artifact: "ppocrv5_dict.txt",
		repoPath: "/PaddlePaddle/PP-OCRv5_mobile_rec_onnx/resolve/main/inference.yml",
		minBytes: 10_000,
		dict:     true,
	},
}

func ocrArtifactNames() []string {
	names := make([]string, 0, len(ocrSources))
	for _, source := range ocrSources {
		names = append(names, source.artifact)
	}
	return names
}

// OCRStatus reports the shared PaddleOCR bundle without creating cache state.
func (m *Manager) OCRStatus() (Model, error) {
	model, err := m.inspect(OCRModelID)
	if err == nil {
		return model, nil
	}
	if !os.IsNotExist(err) {
		return Model{}, err
	}
	return Model{
		ID: OCRModelID, Kind: KindOCR, Artifacts: ocrArtifactNames(),
		Status: "not-downloaded", Lifecycle: LifecycleNotInstalled,
		Runtime: LifecycleRuntimeMiss, Missing: ocrArtifactNames(),
	}, nil
}

// DownloadOCR installs the upstream PP-OCRv5 mobile detector, recognizer, and
// validated CJK dictionary. Files publish atomically as one manifest bundle.
func (m *Manager) DownloadOCR(ctx context.Context, report func(progress int)) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	if m.active[OCRModelID] {
		m.mu.Unlock()
		return fmt.Errorf("model %s is already downloading", OCRModelID)
	}
	m.active[OCRModelID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.active, OCRModelID)
		m.mu.Unlock()
	}()

	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return err
	}
	dir, err := m.modelDir(OCRModelID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("model %s already exists", OCRModelID)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(m.root, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)

	if report != nil {
		report(0)
	}
	for index, source := range ocrSources {
		if err := m.downloadOCRSource(ctx, source, filepath.Join(temp, source.artifact)); err != nil {
			return err
		}
		if report != nil {
			report((index + 1) * 100 / len(ocrSources))
		}
	}

	manifest := Model{
		ID: OCRModelID, Kind: KindOCR, Artifacts: ocrArtifactNames(),
		Downloaded: m.now().UnixMilli(),
	}
	if err := m.writeManifest(temp, manifest); err != nil {
		return err
	}
	_ = os.Remove(dir)
	if err := os.Rename(temp, dir); err != nil {
		return err
	}
	return nil
}

func (m *Manager) downloadOCRSource(ctx context.Context, source ocrSource, target string) error {
	if !source.dict {
		return m.downloadToFile(ctx, m.ocrURL(source.repoPath), target, source.minBytes)
	}

	sourcePath := target + ".source"
	if err := m.downloadToFile(ctx, m.ocrURL(source.repoPath), sourcePath, source.minBytes); err != nil {
		return err
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	_ = os.Remove(sourcePath)
	characters, err := parseOCRCharacterDict(data)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	// PaddleOCR CTC reserves the leading blank token in its dictionary.
	output.WriteByte('\n')
	for _, character := range characters {
		output.WriteString(character)
		output.WriteByte('\n')
	}
	return os.WriteFile(target, output.Bytes(), 0o600)
}

func (m *Manager) ocrURL(repoPath string) string {
	endpoint, err := url.Parse(m.endpoint)
	if err != nil {
		return m.endpoint + repoPath
	}
	relative, err := url.Parse(repoPath)
	if err != nil {
		return m.endpoint + repoPath
	}
	return endpoint.ResolveReference(relative).String()
}

func (m *Manager) writeManifest(dir string, model Model) error {
	data, err := json.MarshalIndent(model, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600)
}

func (m *Manager) downloadToFile(ctx context.Context, downloadURL, target string, minBytes int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", target, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", target, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer file.Close()
	written, err := io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("download %s: %w", target, err)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if written < minBytes {
		_ = os.Remove(target)
		return fmt.Errorf("download %s is too small: %d < %d bytes", target, written, minBytes)
	}
	return nil
}

func parseOCRCharacterDict(data []byte) ([]string, error) {
	lines := strings.Split(string(data), "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "character_dict:" {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("OCR dictionary has no character_dict")
	}

	characters := make([]string, 0, 20000)
	for _, raw := range lines[start:] {
		line := strings.TrimLeft(raw, " \t")
		if !strings.HasPrefix(line, "- ") {
			if len(characters) > 0 {
				break
			}
			continue
		}
		value := strings.TrimPrefix(line, "- ")
		if len(value) >= 2 {
			if value[0] == '\'' && value[len(value)-1] == '\'' ||
				value[0] == '"' && value[len(value)-1] == '"' {
				value = value[1 : len(value)-1]
			}
		}
		characters = append(characters, value)
	}
	if len(characters) < 1000 || !containsHan(characters) {
		return nil, fmt.Errorf("OCR dictionary is incomplete: %d entries", len(characters))
	}
	return characters, nil
}

func containsHan(characters []string) bool {
	for _, character := range characters {
		for _, value := range character {
			if unicode.Is(unicode.Han, value) {
				return true
			}
		}
	}
	return false
}
