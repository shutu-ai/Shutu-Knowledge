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
	"net"
	"net/http"
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
		profile     = flag.String("profile", "core", "identity, host, core, agent, web, or all")
		allowDirty  = flag.Bool("allow-dirty", false, "permit acceptance on a dirty work tree (local diagnostics only)")
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
	case "identity", "host", "core", "agent", "web", "all":
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
		return errors.New("work tree is dirty; run acceptance from a clean candidate checkout or pass -allow-dirty for diagnostics")
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
	var nativeErr error
	if profileIncludesNativeLifecycle(profile) {
		nativeStage, err := runNativeLifecycle(binaryPath, resultRoot)
		result.Stages = append(result.Stages, nativeStage)
		if err != nil {
			nativeErr = err
		}
		writeResult(filepath.Join(resultRoot, "result.json"), result)
	}
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
	if nativeErr != nil || testErr != nil || manifestErr != nil {
		result.Status = "failed"
	}
	writeResult(filepath.Join(resultRoot, "result.json"), result)
	fmt.Printf("release acceptance: %s\nresult: %s\n", result.Status, resultRoot)
	return errors.Join(nativeErr, testErr, manifestErr)
}

func profileIncludesNativeLifecycle(profile string) bool {
	return profile == "host" || profile == "all"
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
	repoRoot, err := os.Getwd()
	if err != nil {
		return "", false, err
	}
	return gitStateAt(repoRoot)
}

func gitStateAt(repoRoot string) (string, bool, error) {
	commitBytes, err := output("git", "-C", repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	commit := strings.TrimSpace(commitBytes)
	if matched, err := filepath.Match("????????????????????????????????????????", commit); err != nil || !matched {
		return "", false, fmt.Errorf("invalid candidate commit %q", commit)
	}
	statusBytes, err := output("git", "-C", repoRoot, "status", "--porcelain", "--untracked-files=all")
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

// runNativeLifecycle exercises the candidate binary as a real host process.
// It intentionally disables the optional managed runtime so this stage tests
// process ownership and storage recovery without downloading model assets.
func runNativeLifecycle(binaryPath, resultRoot string) (stageResult, error) {
	started := time.Now()
	stage := stageResult{
		Name:      "native-host-lifecycle",
		Command:   []string{binaryPath, "serve"},
		Status:    "running",
		StartedAt: started,
		Host: map[string]any{
			"mode":                   "serve",
			"managedRuntimeDisabled": true,
			"checks":                 []string{"healthz", "duplicate-instance", "crash-restart"},
		},
	}
	home := filepath.Join(resultRoot, "native-host")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return finishNativeStage(stage, home, err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return finishNativeStage(stage, home, fmt.Errorf("reserve native host port: %w", err))
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	stage.Host["addr"] = addr
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("server:\n  addr: "+addr+"\n"), 0o600); err != nil {
		return finishNativeStage(stage, home, fmt.Errorf("write native host config: %w", err))
	}

	start := func(name string) (*exec.Cmd, *os.File, *os.File, error) {
		stdout, err := os.OpenFile(filepath.Join(home, name+".out.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return nil, nil, nil, err
		}
		stderr, err := os.OpenFile(filepath.Join(home, name+".err.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			_ = stdout.Close()
			return nil, nil, nil, err
		}
		command := exec.Command(binaryPath, "serve")
		if err := configureOwnedProcess(command); err != nil {
			_ = stdout.Close()
			_ = stderr.Close()
			return nil, nil, nil, err
		}
		command.Env = append(os.Environ(),
			"SHUTU_KNOWLEDGE_HOME="+home,
			"SHUTU_KNOWLEDGE_DISABLE_MANAGED_RUNTIME=1",
		)
		command.Stdout = stdout
		command.Stderr = stderr
		if err := command.Start(); err != nil {
			_ = stdout.Close()
			_ = stderr.Close()
			return nil, nil, nil, err
		}
		return command, stdout, stderr, nil
	}

	first, firstOut, firstErr, err := start("first")
	if err != nil {
		return finishNativeStage(stage, home, fmt.Errorf("start native host: %w", err))
	}
	defer func() {
		_ = firstOut.Close()
		_ = firstErr.Close()
	}()
	if err := waitForNativeHealth(addr, first, 30*time.Second); err != nil {
		_ = terminateOwnedProcessTree(first)
		_, _ = first.Process.Wait()
		return finishNativeStage(stage, home, fmt.Errorf("native host health: %w", err))
	}

	second, secondOut, secondErr, err := start("duplicate")
	if err != nil {
		_ = terminateOwnedProcessTree(first)
		_, _ = first.Process.Wait()
		return finishNativeStage(stage, home, fmt.Errorf("start duplicate native host: %w", err))
	}
	secondWait := make(chan error, 1)
	go func() { secondWait <- second.Wait() }()
	select {
	case duplicateErr := <-secondWait:
		if duplicateErr == nil {
			_ = terminateOwnedProcessTree(second)
			_ = terminateOwnedProcessTree(first)
			_, _ = first.Process.Wait()
			_ = secondOut.Close()
			_ = secondErr.Close()
			return finishNativeStage(stage, home, errors.New("duplicate native host unexpectedly started"))
		}
	case <-time.After(10 * time.Second):
		_ = terminateOwnedProcessTree(second)
		<-secondWait
		_ = secondOut.Close()
		_ = secondErr.Close()
		_ = terminateOwnedProcessTree(first)
		_, _ = first.Process.Wait()
		return finishNativeStage(stage, home, errors.New("duplicate native host did not fail within 10s"))
	}
	_ = secondOut.Close()
	_ = secondErr.Close()

	if err := terminateOwnedProcessTree(first); err != nil {
		return finishNativeStage(stage, home, fmt.Errorf("terminate native host: %w", err))
	}
	if _, err := first.Process.Wait(); err != nil {
		// Process.Kill normally reports a non-nil wait error; the process is
		// nevertheless reaped, which is the crash/restart boundary under test.
		stage.Host["firstExit"] = err.Error()
	}
	restarted, restartOut, restartErr, err := start("restart")
	if err != nil {
		return finishNativeStage(stage, home, fmt.Errorf("restart native host after kill: %w", err))
	}
	if err := waitForNativeHealth(addr, restarted, 30*time.Second); err != nil {
		_ = terminateOwnedProcessTree(restarted)
		_, _ = restarted.Process.Wait()
		_ = restartOut.Close()
		_ = restartErr.Close()
		return finishNativeStage(stage, home, fmt.Errorf("native host restart health: %w", err))
	}
	if err := terminateOwnedProcessTree(restarted); err != nil {
		_ = restartOut.Close()
		_ = restartErr.Close()
		return finishNativeStage(stage, home, fmt.Errorf("terminate restarted native host: %w", err))
	}
	_, _ = restarted.Process.Wait()
	_ = restartOut.Close()
	_ = restartErr.Close()
	stage.Status = "passed"
	stage.Log = home
	stage.Duration = time.Since(started).String()
	return stage, nil
}

func waitForNativeHealth(addr string, command *exec.Cmd, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		if command.ProcessState != nil {
			return fmt.Errorf("process exited with %v", command.ProcessState)
		}
		response, err := client.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("health endpoint did not become ready within %s", timeout)
}

func finishNativeStage(stage stageResult, logDir string, err error) (stageResult, error) {
	stage.Status = "failed"
	stage.Log = logDir
	stage.Duration = time.Since(stage.StartedAt).String()
	stage.Error = err.Error()
	return stage, err
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
