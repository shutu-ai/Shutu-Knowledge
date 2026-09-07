package runtime

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The managed runtime is deliberately kept outside the Knowledge process.
// JavaScript dependencies are installed into the Knowledge data domain and
// never into the Agent or the user's global npm tree.
const managedNodeVersion = "22.14.0"

// Official Node.js release archives. The checksums are pinned so a runtime is
// never selected merely because a moving "latest" URL happened to respond.
var managedNodeArchives = map[string]struct {
	URL    string
	SHA256 string
	Suffix string
}{
	"windows/amd64": {
		URL:    "https://nodejs.org/dist/v22.14.0/node-v22.14.0-win-x64.zip",
		SHA256: "55b639295920b219bb2acbcfa00f90393a2789095b7323f79475c9f34795f217",
		Suffix: "win-x64",
	},
	"linux/amd64": {
		URL:    "https://nodejs.org/dist/v22.14.0/node-v22.14.0-linux-x64.tar.xz",
		SHA256: "69b09dba5c8dcb05c4e4273a4340db1005abeafe3927efda2bc5b249e80437ec",
		Suffix: "linux-x64",
	},
}

//go:embed assets/managed-runtime.mjs assets/managed-bootstrap.mjs assets/package.json assets/package-lock.json assets/runtime-manifest.json
var managedAssets embed.FS

// PrepareManagedRuntime materializes the pinned runtime assets, ensures the
// Node executable is available, and returns a quoted command suitable for the
// supervised JSON runtime manager. npm dependencies are installed lazily by
// the bootstrap process on the first runtime request.
//
// It is intentionally idempotent. Model weights remain lazy: they are
// downloaded only on the first real inference call and then reused offline.
func PrepareManagedRuntime(ctx context.Context, home, modelCache string) (string, error) {
	root := filepath.Join(home, "runtime")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create managed runtime directory: %w", err)
	}
	if err := writeManagedAssets(root); err != nil {
		return "", err
	}
	node, _, err := ensureNode(ctx, root)
	if err != nil {
		return "", err
	}
	bootstrap := filepath.Join(root, "managed-bootstrap.mjs")
	if strings.TrimSpace(modelCache) == "" {
		modelCache = filepath.Join(home, "models")
	}
	command := quoteCommandArg(node) + " " + quoteCommandArg(bootstrap) +
		" --runtime-home " + quoteCommandArg(root) +
		" --model-cache " + quoteCommandArg(modelCache)
	return command, nil
}

func writeManagedAssets(root string) error {
	for _, name := range []string{"assets/managed-runtime.mjs", "assets/managed-bootstrap.mjs", "assets/package.json", "assets/package-lock.json", "assets/runtime-manifest.json"} {
		data, err := managedAssets.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read embedded managed asset %s: %w", name, err)
		}
		target := filepath.Join(root, filepath.Base(name))
		if current, readErr := os.ReadFile(target); readErr == nil && string(current) == string(data) {
			continue
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return fmt.Errorf("write managed asset %s: %w", target, err)
		}
	}
	return nil
}

func ensureNode(ctx context.Context, root string) (node, npm string, err error) {
	if path, lookErr := exec.LookPath("node"); lookErr == nil {
		if version, versionErr := installedNodeVersion(ctx, path); versionErr == nil && version == managedNodeVersion {
			if npmPath, npmErr := findNPM(filepath.Dir(path)); npmErr == nil {
				return path, npmPath, nil
			}
		}
	}
	key := runtime.GOOS + "/" + runtime.GOARCH
	archive, ok := managedNodeArchives[key]
	if !ok {
		return "", "", fmt.Errorf("managed Node.js runtime does not support %s", key)
	}
	// Node's official archives contain a versioned top-level directory whose
	// name includes the `v` prefix (node-v22.14.0-win-x64, etc.).
	installRoot := filepath.Join(root, "node", "node-v"+managedNodeVersion+"-"+archive.Suffix)
	nodePath := filepath.Join(installRoot, "bin", "node")
	if runtime.GOOS == "windows" {
		nodePath = filepath.Join(installRoot, "node.exe")
	}
	npmDir := filepath.Dir(nodePath)
	if runtime.GOOS == "windows" {
		npmDir = installRoot
	}
	if _, nodeErr := os.Stat(nodePath); nodeErr == nil {
		npmPath, npmErr := findNPM(npmDir)
		if npmErr == nil {
			return nodePath, npmPath, nil
		}
	}
	archivePath := filepath.Join(root, "node-"+managedNodeVersion+"-"+archive.Suffix+filepath.Ext(archive.URL))
	if filepath.Ext(archive.URL) == ".xz" {
		archivePath = filepath.Join(root, "node-"+managedNodeVersion+"-"+archive.Suffix+".tar.xz")
	}
	if err := downloadPinned(ctx, archive.URL, archive.SHA256, archivePath); err != nil {
		return "", "", err
	}
	if runtime.GOOS == "windows" {
		if err := os.MkdirAll(filepath.Join(root, "node"), 0o700); err != nil {
			return "", "", err
		}
		if err := extractZip(archivePath, filepath.Join(root, "node")); err != nil {
			return "", "", err
		}
	} else {
		tar, lookErr := exec.LookPath("tar")
		if lookErr != nil {
			return "", "", fmt.Errorf("extract managed Node.js runtime: tar is unavailable: %w", lookErr)
		}
		if err := os.MkdirAll(filepath.Join(root, "node"), 0o700); err != nil {
			return "", "", err
		}
		command := exec.CommandContext(ctx, tar, "-xJf", archivePath, "-C", filepath.Join(root, "node"))
		if output, runErr := command.CombinedOutput(); runErr != nil {
			return "", "", fmt.Errorf("extract managed Node.js runtime: %w: %s", runErr, trimOutput(output))
		}
	}
	npmPath, err := findNPM(npmDir)
	if err != nil {
		return "", "", fmt.Errorf("managed Node.js npm not found after extraction: %w", err)
	}
	if _, err := os.Stat(nodePath); err != nil {
		return "", "", fmt.Errorf("managed Node.js executable not found after extraction: %w", err)
	}
	return nodePath, npmPath, nil
}

func installedNodeVersion(ctx context.Context, path string) (string, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(checkCtx, path, "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(string(output), "v")), nil
}

func findNPM(dir string) (string, error) {
	for _, name := range []string{"npm.cmd", "npm"} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	if path, err := exec.LookPath("npm"); err == nil {
		return path, nil
	}
	return "", errors.New("npm is not available")
}

func downloadPinned(ctx context.Context, source, expected, target string) error {
	if hash, err := fileSHA256(target); err == nil && strings.EqualFold(hash, expected) {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 20 * time.Minute}).Do(request)
	if err != nil {
		return fmt.Errorf("download managed Node.js runtime: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download managed Node.js runtime: HTTP %d", response.StatusCode)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".node-download-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, response.Body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	hash, err := fileSHA256(temporaryPath)
	if err != nil || !strings.EqualFold(hash, expected) {
		return fmt.Errorf("managed Node.js checksum mismatch: got %s want %s", hash, expected)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("publish managed Node.js archive: %w", err)
	}
	return nil
}

func extractZip(archivePath, root string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open managed Node.js archive: %w", err)
	}
	defer reader.Close()
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	for _, entry := range reader.File {
		target := filepath.Join(root, filepath.FromSlash(entry.Name))
		targetAbs, err := filepath.Abs(target)
		if err != nil || (targetAbs != rootAbs && !strings.HasPrefix(targetAbs, rootAbs+string(filepath.Separator))) {
			return fmt.Errorf("managed Node.js archive contains unsafe path %q", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		_ = input.Close()
		_ = output.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func trimOutput(data []byte) string {
	text := strings.TrimSpace(string(data))
	if len(text) > 4000 {
		return text[len(text)-4000:]
	}
	return text
}

func quoteCommandArg(value string) string {
	// splitCommand removes the surrounding quotes but does not interpret shell
	// escapes. Keep Windows path separators literal; doubling them would make
	// the managed executable path invalid on Windows.
	return `"` + value + `"`
}
