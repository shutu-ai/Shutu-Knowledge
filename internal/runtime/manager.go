// Package runtime owns optional helper processes used by local ML
// capabilities. Knowledge starts and supervises the process, but never
// links an inference engine into its own binary.
package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProtocolVersion is the helper wire contract version.
const ProtocolVersion = 1

// Capability names supported by the helper contract.
const (
	CapabilityEmbedding = "embedding"
	CapabilityRerank    = "rerank"
	CapabilityOCR       = "ocr"
	CapabilityPDFRender = "pdf_render"
	CapabilityOffice    = "office"
)

// Error is a structured helper failure. Code is stable enough for callers
// to distinguish unavailable runtimes from malformed inference output.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

type request struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	Protocol int `json:"protocol"`
}

type initializeResult struct {
	Protocol     int      `json:"protocol"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

type healthParams struct {
	Capability string `json:"capability"`
}

// Health is a readiness report from a live helper process.
type Health struct {
	Ready        bool              `json:"ready"`
	Status       string            `json:"status,omitempty"` // ready | loading | not_installed | failed | unavailable
	Version      string            `json:"version,omitempty"`
	Path         string            `json:"path,omitempty"`
	Model        string            `json:"model,omitempty"`
	Capability   string            `json:"capability"`
	Lifecycle    string            `json:"lifecycle,omitempty"`
	LastError    string            `json:"lastError,omitempty"`
	Remediation  string            `json:"remediation,omitempty"`
	LastLoaded   time.Time         `json:"lastLoaded,omitempty"`
	Details      map[string]string `json:"details,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
}

// Caller is the surface capability adapters depend on. It lets integration
// tests substitute an in-memory caller without changing production wiring.
type Caller interface {
	Call(ctx context.Context, capability string, params, out any) error
	Configured(capability string) bool
}

var _ Caller = (*Manager)(nil)

// Controller is the process lifecycle surface used by application wiring.
// Keeping it as an interface lets API-level tests inject a live helper stub.
type Controller interface {
	Caller
	Probe(ctx context.Context, capability string) (Health, error)
	Status(ctx context.Context) map[string]Health
	Close()
}

var _ Controller = (*Manager)(nil)

// ManagedModelController exposes model lifecycle operations owned by the
// Knowledge-managed runtime. It is intentionally separate from Caller so
// ordinary helper implementations cannot claim model installation semantics.
type ManagedModelController interface {
	LoadModel(ctx context.Context, capability, model string) (Health, error)
	RemoveModel(ctx context.Context, capability, model string) error
}

var _ ManagedModelController = (*Manager)(nil)

// Options configures process supervision.
type Options struct {
	// Command is a non-shell command template. The first field must be an
	// executable; the helper speaks one JSON object per stdin/stdout line.
	Command string
	// StartupTimeout bounds the initialize handshake (default 10s).
	StartupTimeout time.Duration
	// RequestTimeout bounds each capability call (default 60s).
	RequestTimeout time.Duration
	// ModelLoadTimeout bounds first-install/reinstall model downloads. It is
	// separate because a multi-gigabyte model may take much longer than a
	// normal inference request (default: RequestTimeout).
	ModelLoadTimeout time.Duration
	// IdleTimeout stops an unused process; zero keeps it until Close.
	IdleTimeout time.Duration
	// Now is injectable for tests.
	Now func() time.Time
	// EmbeddingCommand overrides Command for embeddings.
	EmbeddingCommand string
	// RerankCommand overrides Command for reranking.
	RerankCommand string
	// OCRCommand overrides Command for OCR.
	OCRCommand string
}

type helperProcess struct {
	command   string
	cancel    context.CancelFunc
	stdin     io.WriteCloser
	scanner   *bufio.Scanner
	wait      chan struct{}
	lastUsed  time.Time
	stderr    captureBuffer
	lastError string
	stop      func()
	stopMu    sync.RWMutex
	closeOnce sync.Once
	mu        sync.Mutex
}

// captureBuffer is safe for a helper process to write while the supervisor
// is synchronously reading stdout. Keeping stderr bounded preserves the
// actionable cause of a failure without allowing a noisy child to consume
// unbounded memory.
type captureBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *captureBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const maxBytes = 256 * 1024
	if len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
	}
	if b.buf.Len()+len(data) > maxBytes {
		old := b.buf.Bytes()
		keep := maxBytes - len(data)
		b.buf.Reset()
		if keep > 0 && len(old) > keep {
			_, _ = b.buf.Write(old[len(old)-keep:])
		} else if keep > 0 {
			_, _ = b.buf.Write(old)
		}
	}
	return b.buf.Write(data)
}

func (b *captureBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *captureBuffer) Reset() {
	b.mu.Lock()
	b.buf.Reset()
	b.mu.Unlock()
}

// Manager lazily starts one supervised helper per configured command and
// rebuilds it after a crash. A process handles requests serially.
type Manager struct {
	command          string
	embeddingCommand string
	rerankCommand    string
	ocrCommand       string
	startupTimeout   time.Duration
	requestTimeout   time.Duration
	modelLoadTimeout time.Duration
	idleTimeout      time.Duration
	now              func() time.Time
	stop             chan struct{}
	stopOnce         sync.Once
	done             chan struct{}

	mu        sync.Mutex
	processes map[string]*helperProcess
	warmed    map[string]bool
}

// NewManager creates a supervisor and starts its idle reaper.
func NewManager(options Options) *Manager {
	if options.StartupTimeout <= 0 {
		options.StartupTimeout = 10 * time.Second
	}
	if options.RequestTimeout <= 0 {
		options.RequestTimeout = 60 * time.Second
	}
	if options.ModelLoadTimeout <= 0 {
		options.ModelLoadTimeout = options.RequestTimeout
	}
	if options.IdleTimeout < 0 {
		options.IdleTimeout = 0
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	manager := &Manager{
		command:          strings.TrimSpace(options.Command),
		embeddingCommand: strings.TrimSpace(options.EmbeddingCommand),
		rerankCommand:    strings.TrimSpace(options.RerankCommand),
		ocrCommand:       strings.TrimSpace(options.OCRCommand),
		startupTimeout:   options.StartupTimeout,
		requestTimeout:   options.RequestTimeout,
		modelLoadTimeout: options.ModelLoadTimeout,
		idleTimeout:      options.IdleTimeout,
		now:              now,
		stop:             make(chan struct{}),
		done:             make(chan struct{}),
		processes:        map[string]*helperProcess{},
		warmed:           map[string]bool{},
	}
	go manager.reap()
	return manager
}

// Configured reports whether a capability has an isolated runtime command.
func (m *Manager) Configured(capability string) bool {
	return m.commandFor(capability) != ""
}

// Call invokes one capability. The params value is JSON encoded and the
// result, when present, is decoded into out.
func (m *Manager) Call(ctx context.Context, capability string, params, out any) error {
	return m.invoke(ctx, capability, capability, params, out)
}

func (m *Manager) invoke(ctx context.Context, method, capability string, params, out any) error {
	timeout := m.requestTimeout
	if m.isFirstModelInference(method, capability) {
		timeout = m.modelLoadTimeout
	}
	return m.invokeWithTimeout(ctx, method, capability, params, out, timeout)
}

func (m *Manager) isFirstModelInference(method, capability string) bool {
	if (capability != CapabilityEmbedding && capability != CapabilityRerank) || method == "health" || method == "load" || method == "remove" {
		return false
	}
	m.mu.Lock()
	warmed := m.warmed[capability]
	m.mu.Unlock()
	return !warmed
}

func (m *Manager) markWarmed(method, capability string) {
	if (method == "load" || method == CapabilityEmbedding || method == CapabilityRerank) &&
		(capability == CapabilityEmbedding || capability == CapabilityRerank) {
		m.mu.Lock()
		m.warmed[capability] = true
		m.mu.Unlock()
	}
}

func (m *Manager) invokeWithTimeout(ctx context.Context, method, capability string, params, out any, timeout time.Duration) error {
	command := m.commandFor(capability)
	if command == "" {
		return &Error{Code: "runtime_unconfigured", Message: fmt.Sprintf("%s runtime command is not configured", capability)}
	}

	m.mu.Lock()
	if m.isStopped() {
		m.mu.Unlock()
		return errors.New("runtime manager is closed")
	}
	helper := m.processes[command]
	if helper == nil {
		helper = &helperProcess{command: command, lastUsed: m.now()}
		m.processes[command] = helper
	}
	m.mu.Unlock()
	err := helper.call(ctx, method, capability, params, out, m.startupTimeout, timeout)
	if err == nil {
		m.markWarmed(method, capability)
	}
	return err
}

// Probe asks a configured helper whether a capability is ready.
func (m *Manager) Probe(ctx context.Context, capability string) (Health, error) {
	var health Health
	err := m.invoke(ctx, "health", capability, healthParams{Capability: capability}, &health)
	if err != nil {
		health.Capability = capability
		health.Status = "failed"
		health.Lifecycle = "FAILED"
		health.LastError = err.Error()
		health.Remediation = remediation(capability)
		return health, err
	}
	health.Capability = capability
	if health.Path == "" {
		health.Path = m.commandFor(capability)
	}
	if health.Status == "" {
		switch {
		case health.Ready:
			health.Status = "ready"
		case health.Lifecycle == "FAILED":
			health.Status = "failed"
		case health.Lifecycle == "LOADING":
			health.Status = "loading"
		default:
			health.Status = "not_installed"
		}
	}
	if health.LastError == "" && health.Details != nil {
		health.LastError = health.Details["error"]
	}
	if health.Remediation == "" && !health.Ready {
		health.Remediation = remediation(capability)
	}
	if !health.Ready {
		return health, &Error{Code: "runtime_not_ready", Message: fmt.Sprintf("%s runtime is not ready", capability)}
	}
	return health, nil
}

// LoadModel performs the runtime's real model load and inference smoke. A
// successful return is the only point at which a model may be marked READY.
func (m *Manager) LoadModel(ctx context.Context, capability, model string) (Health, error) {
	var health Health
	err := m.invokeWithTimeout(ctx, "load", capability, map[string]string{"model": model, "capability": capability}, &health, m.modelLoadTimeout)
	if err != nil {
		health.Capability = capability
		health.Status = "failed"
		health.Lifecycle = "FAILED"
		health.LastError = err.Error()
		health.Path = m.commandFor(capability)
		health.Remediation = remediation(capability)
		return health, err
	}
	health.Capability = capability
	if !health.Ready {
		if health.Status == "" {
			health.Status = "failed"
		}
		if health.Lifecycle == "" {
			health.Lifecycle = "FAILED"
		}
		if health.Remediation == "" {
			health.Remediation = remediation(capability)
		}
		return health, &Error{Code: "runtime_not_ready", Message: fmt.Sprintf("%s runtime is not ready", capability)}
	}
	if health.Status == "" {
		health.Status = "ready"
	}
	if health.Lifecycle == "" {
		health.Lifecycle = "READY"
	}
	if health.Path == "" {
		health.Path = m.commandFor(capability)
	}
	return health, nil
}

// RemoveModel invalidates the runtime cache and persisted readiness state.
func (m *Manager) RemoveModel(ctx context.Context, capability, model string) error {
	return m.invoke(ctx, "remove", capability, map[string]string{"model": model, "capability": capability}, nil)
}

// Status probes every configured capability, including unavailable ones.
func (m *Manager) Status(ctx context.Context) map[string]Health {
	capabilities := []string{CapabilityEmbedding, CapabilityRerank, CapabilityOCR, CapabilityPDFRender, CapabilityOffice}
	status := make(map[string]Health, len(capabilities))
	for _, capability := range capabilities {
		if !m.Configured(capability) {
			status[capability] = Health{
				Capability: capability, Status: "unavailable", Lifecycle: "NOT_CONFIGURED",
				Remediation: "enable the managed runtime or configure a Knowledge-owned runtime command",
			}
			continue
		}
		health, err := m.Probe(ctx, capability)
		if err != nil {
			health.Capability = capability
			health.Ready = false
			if details := err.Error(); details != "" {
				if health.Details == nil {
					health.Details = map[string]string{}
				}
				health.Details["error"] = details
				health.LastError = details
			}
			if health.Status == "" {
				health.Status = "failed"
			}
			if health.Lifecycle == "" {
				health.Lifecycle = "FAILED"
			}
			if health.Remediation == "" {
				health.Remediation = remediation(capability)
			}
		}
		status[capability] = health
	}
	return status
}

func remediation(capability string) string {
	switch capability {
	case CapabilityEmbedding:
		return "select or download the pinned embedding model, then retry the runtime self-test"
	case CapabilityRerank:
		return "select or download the pinned reranker model, then retry the runtime self-test"
	case CapabilityOCR:
		return "allow one online start to cache OCR language data, or disable offline mode"
	case CapabilityPDFRender:
		return "verify the managed PDF runtime installation and rerun Doctor"
	case CapabilityOffice:
		return "verify the managed Office runtime installation and rerun Doctor"
	default:
		return "inspect the runtime logs and rerun Doctor"
	}
}

// Close stops idle reaping and all active helper processes.
func (m *Manager) Close() {
	m.stopOnce.Do(func() { close(m.stop) })
	m.mu.Lock()
	helpers := make([]*helperProcess, 0, len(m.processes))
	for _, helper := range m.processes {
		helpers = append(helpers, helper)
	}
	m.processes = map[string]*helperProcess{}
	m.mu.Unlock()
	for _, helper := range helpers {
		helper.close()
	}
	<-m.done
}

// HasProcess is a test hook for observing process retirement.
func (m *Manager) HasProcess() bool {
	m.mu.Lock()
	helpers := make([]*helperProcess, 0, len(m.processes))
	for _, helper := range m.processes {
		helpers = append(helpers, helper)
	}
	m.mu.Unlock()
	for _, helper := range helpers {
		helper.mu.Lock()
		running := helper.runningLocked()
		helper.mu.Unlock()
		if running {
			return true
		}
	}
	return false
}

func (m *Manager) reap() {
	defer close(m.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-ticker.C:
			if m.idleTimeout <= 0 {
				continue
			}
			m.mu.Lock()
			var expired []*helperProcess
			for command, helper := range m.processes {
				helper.mu.Lock()
				idle := now.Sub(helper.lastUsed)
				helper.mu.Unlock()
				if idle >= m.idleTimeout {
					expired = append(expired, helper)
					delete(m.processes, command)
				}
			}
			m.mu.Unlock()
			for _, helper := range expired {
				helper.close()
			}
		}
	}
}

func (m *Manager) isStopped() bool {
	select {
	case <-m.stop:
		return true
	default:
		return false
	}
}

func (m *Manager) commandFor(capability string) string {
	// Environment overrides are useful for deployments that run one protocol
	// gateway in front of capability-specific runtimes.
	if command := strings.TrimSpace(osHelperCommand(capability)); command != "" {
		return command
	}
	var configured string
	switch capability {
	case CapabilityEmbedding:
		configured = m.embeddingCommand
	case CapabilityRerank:
		configured = m.rerankCommand
	case CapabilityOCR:
		configured = m.ocrCommand
	default:
		return strings.TrimSpace(m.command)
	}
	if strings.TrimSpace(configured) != "" {
		return strings.TrimSpace(configured)
	}
	return strings.TrimSpace(m.command)
}

// call serializes requests on one process and recreates it after EOF or a
// failed startup. The lock intentionally includes stdin, stdout, and wait.
func (p *helperProcess) call(parent context.Context, method, capability string, params, out any, startupTimeout, requestTimeout time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := parent.Err(); err != nil {
		return err
	}
	if !p.runningLocked() {
		if err := p.startLocked(startupTimeout); err != nil {
			p.closeLocked()
			return err
		}
	}

	encoded, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", capability, err)
	}
	callCtx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()
	deadline := time.AfterFunc(requestTimeout, p.stopProcess)
	defer deadline.Stop()

	id := nextRequestID()
	requestData, err := json.Marshal(request{ID: id, Method: method, Params: encoded})
	if err != nil {
		return err
	}
	requestData = append(requestData, '\n')
	if _, err := p.stdin.Write(requestData); err != nil {
		p.closeLocked()
		return p.withStderr(fmt.Errorf("write %s request: %w", capability, err))
	}
	if err := callCtx.Err(); err != nil {
		p.closeLocked()
		return fmt.Errorf("%s request: %w", capability, err)
	}
	if !p.scanner.Scan() {
		scanErr := p.scanner.Err()
		p.closeLocked()
		if scanErr != nil {
			return p.withStderr(fmt.Errorf("read %s response: %w", capability, scanErr))
		}
		if callCtx.Err() != nil {
			return fmt.Errorf("%s request: %w", capability, callCtx.Err())
		}
		return p.withStderr(fmt.Errorf("%s helper closed stdout", capability))
	}
	if err := callCtx.Err(); err != nil {
		p.closeLocked()
		return fmt.Errorf("%s request: %w", capability, err)
	}
	var reply response
	if err := json.Unmarshal(p.scanner.Bytes(), &reply); err != nil {
		p.closeLocked()
		return p.withStderr(fmt.Errorf("decode %s response: %w", capability, err))
	}
	if reply.ID != id {
		p.closeLocked()
		return p.withStderr(fmt.Errorf("%s helper returned request id %d, expected %d", capability, reply.ID, id))
	}
	if reply.Error != nil {
		return &Error{Code: reply.Error.Code, Message: reply.Error.Message}
	}
	if out != nil && len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, out); err != nil {
			return fmt.Errorf("decode %s result: %w", capability, err)
		}
	}
	p.lastUsed = time.Now()
	return nil
}

func (p *helperProcess) runningLocked() bool {
	select {
	case <-p.wait:
		return false
	default:
		return p.stdin != nil && p.scanner != nil
	}
}

func (p *helperProcess) startLocked(startupTimeout time.Duration) error {
	words, ok := splitCommand(p.command)
	if !ok || len(words) == 0 {
		return &Error{Code: "runtime_unconfigured", Message: "helper command is empty"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, words[0], words[1:]...)
	if err := configureProcess(cmd); err != nil {
		cancel()
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return fmt.Errorf("helper stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		cancel()
		return fmt.Errorf("helper stdout: %w", err)
	}
	p.stderr.Reset()
	cmd.Stderr = &p.stderr // Keep diagnostics off stdout but available to Doctor.
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		cancel()
		return fmt.Errorf("start helper: %w", err)
	}
	p.cancel = cancel
	p.stopMu.Lock()
	p.stop = func() {
		terminateProcessTree(cmd)
		cancel()
	}
	p.stopMu.Unlock()
	p.stdin = stdin
	p.scanner = bufio.NewScanner(stdout)
	p.scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	wait := make(chan struct{})
	p.wait = wait
	go func(wait chan struct{}) {
		_ = cmd.Wait()
		close(wait)
	}(wait)

	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, startupTimeout)
	defer handshakeCancel()
	deadline := time.AfterFunc(startupTimeout, p.stopProcess)
	defer deadline.Stop()
	err = p.handshakeLocked(handshakeCtx)
	if err != nil {
		p.closeLocked()
		return p.withStderr(err)
	}
	p.lastUsed = time.Now()
	return nil
}

// splitCommand supports quoted executable paths and arguments. Managed
// runtimes live below the user data directory, which may itself contain
// spaces, so strings.Fields would silently launch the wrong executable.
func splitCommand(command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, false
	}
	var words []string
	var word strings.Builder
	var quote rune
	flush := func() {
		if word.Len() == 0 {
			return
		}
		words = append(words, word.String())
		word.Reset()
	}
	for _, char := range command {
		switch {
		case quote != 0:
			if char == quote {
				quote = 0
			} else {
				word.WriteRune(char)
			}
		case char == '\'' || char == '"':
			quote = char
		case char == ' ' || char == '\t' || char == '\r' || char == '\n':
			flush()
		default:
			word.WriteRune(char)
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	return words, len(words) > 0
}

func (p *helperProcess) handshakeLocked(ctx context.Context) error {
	params, err := json.Marshal(initializeParams{Protocol: ProtocolVersion})
	if err != nil {
		return err
	}
	requestData, err := json.Marshal(request{ID: nextRequestID(), Method: "initialize", Params: params})
	if err != nil {
		return err
	}
	requestData = append(requestData, '\n')
	if _, err := p.stdin.Write(requestData); err != nil {
		return fmt.Errorf("helper handshake: %w", err)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("helper handshake: %w", ctx.Err())
	}
	if !p.scanner.Scan() {
		scanErr := p.scanner.Err()
		if scanErr != nil {
			return fmt.Errorf("helper handshake: %w", scanErr)
		}
		if ctx.Err() != nil {
			return fmt.Errorf("helper handshake: %w", ctx.Err())
		}
		return fmt.Errorf("helper handshake: helper exited")
	}
	var reply response
	if err := json.Unmarshal(p.scanner.Bytes(), &reply); err != nil {
		return fmt.Errorf("decode helper handshake: %w", err)
	}
	if reply.Error != nil {
		return &Error{Code: reply.Error.Code, Message: reply.Error.Message}
	}
	var result initializeResult
	if len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, &result); err != nil {
			return fmt.Errorf("decode helper handshake result: %w", err)
		}
	}
	if result.Protocol != ProtocolVersion {
		return fmt.Errorf("helper protocol %d, expected %d", result.Protocol, ProtocolVersion)
	}
	return nil
}

func (p *helperProcess) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeLocked()
}

func (p *helperProcess) closeLocked() {
	if p.stdin == nil {
		return
	}
	p.closeOnce.Do(func() {
		_ = p.stdin.Close()
		p.stopMu.RLock()
		stop := p.stop
		p.stopMu.RUnlock()
		if stop != nil {
			stop()
		} else if p.cancel != nil {
			p.cancel()
		}
		p.lastError = strings.TrimSpace(p.stderr.String())
	})
	// A reaper may have recreated the process object after an idle close.
	// This object is terminal until a new process replaces it by call.
	p.stdin = nil
	p.scanner = nil
	p.cancel = nil
	p.stopMu.Lock()
	p.stop = nil
	p.stopMu.Unlock()
	p.closeOnce = sync.Once{}
}

func (p *helperProcess) stopProcess() {
	p.stopMu.RLock()
	stop := p.stop
	p.stopMu.RUnlock()
	if stop != nil {
		stop()
	}
}

func (p *helperProcess) withStderr(err error) error {
	if p.lastError == "" {
		return err
	}
	return fmt.Errorf("%w (helper stderr: %s)", err, p.lastError)
}

var requestID atomic.Uint64

func nextRequestID() uint64 { return requestID.Add(1) }
