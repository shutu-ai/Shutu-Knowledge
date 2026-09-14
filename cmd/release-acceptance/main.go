// Command release-acceptance runs the native release-host acceptance profile.
// It is intentionally separate from unit-test workflows so an operator runs it
// on the actual host filesystem, process supervisor, and volume under test.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
	"github.com/shutu-ai/shutu-knowledge/internal/version"
	"gopkg.in/yaml.v3"
)

const ldflagsPrefix = "github.com/shutu-ai/shutu-knowledge/internal/version.GitCommit="

type stageResult struct {
	Name      string         `json:"name"`
	Command   []string       `json:"command"`
	Status    string         `json:"status"`
	StartedAt time.Time      `json:"startedAt"`
	Duration  string         `json:"duration"`
	Log       string         `json:"log"`
	Error     string         `json:"error,omitempty"`
	Build     *version.Build `json:"build,omitempty"`
	Host      map[string]any `json:"host,omitempty"`
	Stages    []stageResult  `json:"stages,omitempty"`
}

func main() {
	var (
		output      = flag.String("output", filepath.Join(".tmp", "release-acceptance"), "result directory")
		profile     = flag.String("profile", "core", "identity, core, agent, web, or all")
		allowDirty  = flag.Bool("allow-dirty", false, "permit acceptance on a dirty tracked work tree (local diagnostics only)")
		archiveRoot = flag.String("candidate-archive", "", "extracted formal package root for identity-only acceptance")
	)
	flag.Parse()

	started := time.Now().UTC()
	if err := run(started, *output, *profile, *allowDirty, *archiveRoot); err != nil {
		fmt.Fprintln(os.Stderr, "release acceptance failed:", err)
		os.Exit(1)
	}
}

func run(started time.Time, output, profile string, allowDirty bool, archiveRoot string) error {
	if _, err := os.Stat("go.mod"); err != nil {
		return errors.New("release acceptance must run from the repository root")
	}
	if err := supportedHost(); err != nil {
		return err
	}
	switch profile {
	case "identity", "core", "agent", "web", "all":
	default:
		return fmt.Errorf("unknown profile %q", profile)
	}
	if profile == "identity" && archiveRoot == "" {
		return errors.New("identity profile requires -candidate-archive")
	}

	if err := rejectCrossCompile(); err != nil {
		return err
	}
	commit, dirty, err := gitState()
	if err != nil {
		return err
	}
	if dirty && !allowDirty {
		return errors.New("tracked work tree is dirty; run acceptance from a clean candidate checkout or pass -allow-dirty for diagnostics")
	}

	resultRoot := filepath.Join(output, started.Format("20060102-150405"))
	if err := os.MkdirAll(resultRoot, 0o755); err != nil {
		return err
	}
	result := stageResult{
		Name:      "release-acceptance",
		Status:    "running",
		StartedAt: started,
		Host: map[string]any{
			"goos":                  runtime.GOOS,
			"goarch":                runtime.GOARCH,
			"candidateCommit":       commit,
			"sourceDirty":           dirty,
			"profile":               profile,
			"rawVolume":             os.Getenv("SHUTU_TEST_RAW_VOLUME"),
			"rawVolumeAckPresent":   os.Getenv("SHUTU_TEST_RAW_VOLUME_ACK") == "dedicated-destructive-volume",
			"supportedEnvelopes":    envelope(),
			"extensionManifestPath": "extension.yaml",
		},
	}
	writeResult(filepath.Join(resultRoot, "result.json"), result)

	binaryPath := filepath.Join(resultRoot, "shutu-knowledge")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}
	var identity *version.Build
	version.GitCommit = commit
	if archiveRoot != "" {
		identity, err = archiveIdentity(archiveRoot)
	} else {
		identity, err = buildAndIdentify(commit, binaryPath, resultRoot)
	}
	if err != nil {
		return err
	}
	result.Build = identity
	writeResult(filepath.Join(resultRoot, "result.json"), result)

	var testErr error
	if profile != "identity" {
		testErr = runTests(resultRoot, profile, &result)
	}
	manifestStarted := time.Now()
	manifestErr := validateExtensionManifest()
	if manifestErr != nil {
		result.Stages = append(result.Stages, stageResult{
			Name: "extension-manifest", Status: "failed", StartedAt: manifestStarted,
			Duration: time.Since(manifestStarted).String(), Error: manifestErr.Error(),
		})
	} else {
		result.Stages = append(result.Stages, stageResult{
			Name: "extension-manifest", Status: "passed", StartedAt: manifestStarted,
			Duration: time.Since(manifestStarted).String(),
		})
	}
	result.Duration = time.Since(started).String()
	result.Status = "passed"
	if testErr != nil || manifestErr != nil {
		result.Status = "failed"
	}
	writeResult(filepath.Join(resultRoot, "result.json"), result)
	fmt.Printf("release acceptance: %s\nresult: %s\n", result.Status, resultRoot)
	return errors.Join(testErr, manifestErr)
}

func supportedHost() error {
	switch {
	case runtime.GOOS == "windows" && runtime.GOARCH == "amd64":
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
	default:
		return fmt.Errorf("unsupported native acceptance host %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return nil
}

func rejectCrossCompile() error {
	for _, variable := range []string{"GOOS", "GOARCH"} {
		value := os.Getenv(variable)
		if value == "" {
			continue
		}
		actual := runtime.GOOS
		if variable == "GOARCH" {
			actual = runtime.GOARCH
		}
		if !strings.EqualFold(value, actual) {
			return fmt.Errorf("acceptance must run natively; %s=%s does not match host %s", variable, value, actual)
		}
	}
	return nil
}

func gitState() (string, bool, error) {
	commitBytes, err := output("git", "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	commit := strings.TrimSpace(commitBytes)
	if matched, err := filepath.Match("????????????????????????????????????????", commit); err != nil || !matched {
		return "", false, fmt.Errorf("invalid candidate commit %q", commit)
	}
	statusBytes, err := output("git", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", false, err
	}
	return commit, strings.TrimSpace(statusBytes) != "", nil
}

func envelope() map[string]int {
	return map[string]int{
		"format":    storage.CurrentStorageFormatVersion,
		"reader":    storage.CurrentStorageReaderVersion,
		"writer":    storage.CurrentStorageWriterVersion,
		"minReader": storage.MinStorageReaderVersion,
		"minWriter": storage.MinStorageWriterVersion,
	}
}

func buildAndIdentify(commit, binaryPath, resultRoot string) (*version.Build, error) {
	ldflags := "-ldflags=-X " + ldflagsPrefix + commit
	build := exec.Command("go", "build", "-trimpath", ldflags, "-o", binaryPath,
		"github.com/shutu-ai/shutu-knowledge/cmd/shutu-knowledge")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return nil, fmt.Errorf("build candidate: %w", err)
	}
	return identify(binaryPath)
}

func archiveIdentity(root string) (*version.Build, error) {
	name := "shutu-knowledge"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return identify(filepath.Join(root, "bin", name))
}

func identify(binaryPath string) (*version.Build, error) {
	outputBytes, err := output(binaryPath, "version")
	if err != nil {
		return nil, err
	}
	var build version.Build
	if err := json.Unmarshal([]byte(strings.TrimSpace(outputBytes)), &build); err != nil {
		return nil, fmt.Errorf("parse candidate version: %w", err)
	}
	want := version.Current()
	if build != want {
		return nil, fmt.Errorf("candidate identity mismatch: got %+v want %+v", build, want)
	}
	return &build, nil
}

func runTests(resultRoot, profile string, result *stageResult) error {
	var groups [][]string
	core := []string{"./internal/storage", "./internal/operations", "./internal/runtime",
		"./internal/app", "./internal/knowledge"}
	if profile == "core" || profile == "all" {
		groups = append(groups, core)
	}
	if profile == "agent" || profile == "all" {
		groups = append(groups, []string{"./internal/extension"})
	}
	if profile == "web" || profile == "all" {
		groups = append(groups, []string{"./internal/web"})
	}

	var firstErr error
	for _, packages := range groups {
		for _, packageName := range packages {
			name := "go-test-" + strings.TrimPrefix(packageName, "./internal/")
			logPath := filepath.Join(resultRoot, name+".log")
			command := []string{"go", "test", packageName, "-count=1"}
			started := time.Now()
			err := runLogged(command, logPath)
			status := "passed"
			message := ""
			if err != nil {
				status = "failed"
				message = err.Error()
				if firstErr == nil {
					firstErr = err
				}
			}
			result.Stages = append(result.Stages, stageResult{
				Name: name, Command: command, Status: status, StartedAt: started,
				Duration: time.Since(started).String(), Log: logPath, Error: message,
			})
			writeResult(filepath.Join(resultRoot, "result.json"), *result)
		}
	}
	return firstErr
}

func validateExtensionManifest() error {
	data, err := os.ReadFile("extension.yaml")
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return err
	}
	got, _ := manifest["version"].(string)
	if got != version.ExtensionVersion() {
		return fmt.Errorf("extension manifest version = %q, want %q", got, version.ExtensionVersion())
	}
	return nil
}

func runLogged(command []string, logPath string) error {
	file, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer file.Close()
	command2 := exec.Command(command[0], command[1:]...)
	command2.Stdout = io.MultiWriter(os.Stdout, file)
	command2.Stderr = io.MultiWriter(os.Stderr, file)
	return command2.Run()
}

func writeResult(path string, result stageResult) {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		panic(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		panic(err)
	}
}

func output(name string, arguments ...string) (string, error) {
	command := exec.Command(name, arguments...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(arguments, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
