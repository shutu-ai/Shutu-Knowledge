package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/operations"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

const (
	appKillModeEnv     = "APP_IMPORT_KILL_MODE"
	appKillOperationID = "APP_IMPORT_KILL_OPERATION"
	appKillMarkerEnv   = "APP_IMPORT_KILL_MARKER"
	appKillPreDispatch = "pre-dispatch"
	appKillPostPublish = "post-publish"
	appKillEmbedding   = "during-embedding"
	appParserStateEnv  = "APP_LEGACY_PARSER_STATE"
	appKillParserOwner = "legacy-parser-owner"
)

func forceKillTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		out, err := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("taskkill %d: %v: %s", cmd.Process.Pid, err, out)
		}
		return nil
	}
	return cmd.Process.Kill()
}

func TestMain(m *testing.M) {
	isLegacyParserHelper := false
	for _, arg := range os.Args {
		if arg == "-test.run=^TestLegacyParserHelperProcess$" {
			isLegacyParserHelper = true
			break
		}
	}
	if isLegacyParserHelper {
		statePath := os.Getenv(appParserStateEnv)
		os.Exit(runLegacyParserHelperProcess(statePath, os.Args))
	}
	switch mode := os.Getenv(appKillModeEnv); mode {
	case appKillPreDispatch, appKillPostPublish, appKillEmbedding, appKillParserOwner:
		os.Exit(runImportKillChild(
			mode,
			os.Getenv(appKillOperationID),
			os.Getenv(appKillMarkerEnv),
		))
	case "":
		os.Exit(m.Run())
	default:
		os.Exit(2)
	}
}

func runLegacyParserHelperProcess(statePath string, args []string) int {
	// ExecHelper appends or substitutes {input}; the test binary separator
	// makes the input path the final argument without involving the shell.
	if len(args) == 0 {
		return 2
	}
	inputPath := args[len(args)-1]
	if _, err := os.Stat(inputPath); err != nil {
		return 2
	}
	if _, err := os.Stat(statePath); err == nil {
		converted, readErr := os.ReadFile(statePath)
		if readErr != nil {
			return 2
		}
		fmt.Println(string(converted))
		return 0
	}
	if err := os.WriteFile(statePath, []byte("legacy parser recovered after forced death"), 0o600); err != nil {
		return 2
	}
	// First call is the real parser breakpoint: the parent sees the marker,
	// force-kills the App process tree (including this helper), and a fresh
	// App owner later invokes the helper again.
	for {
		time.Sleep(time.Hour)
	}
}

func runImportKillChild(mode, operationID, markerDir string) int {
	switch mode {
	case appKillPreDispatch:
		operations.SetTestPreDispatchHook(func(op operations.Operation) {
			if op.ID == operationID {
				reachKillMarker(markerDir, mode)
			}
		})
	case appKillEmbedding:
		// The parent watches the real embedding HTTP request. Since ingest
		// commits staged chunks before calling the provider, that request is
		// the process-kill breakpoint.
	case appKillParserOwner:
		// The parent watches the legacy parser helper marker. No in-process
		// hook is needed; the external parser is the breakpoint.
	case appKillPostPublish:
		testImportPublished = func(op operations.Operation, _ knowledge.Document) {
			if op.ID == operationID {
				reachKillMarker(markerDir, mode)
			}
		}
	}
	application, err := New(context.Background())
	if err != nil {
		return 2
	}
	// The parent force-kills this process at the installed boundary. Normal
	// cleanup would misrepresent that crash and is intentionally omitted.
	_ = application
	for {
		time.Sleep(time.Hour)
	}
}

func reachKillMarker(markerDir, mode string) {
	if err := os.WriteFile(filepath.Join(markerDir, "reached"), []byte(mode), 0o600); err != nil {
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestImportProcessKillReplaysExactBusinessEffect(t *testing.T) {
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	runBreakpoint := func(mode string) operations.Operation {
		t.Helper()
		home := t.TempDir()
		t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
		ctx := context.Background()
		first, err := New(ctx)
		if err != nil {
			t.Fatal(err)
		}
		base, err := first.Knowledge.CreateBase("Process Kill Replay", "", "", knowledge.BaseConfig{})
		if err != nil {
			t.Fatal(err)
		}
		if mode == appKillPreDispatch {
			first.Operations.Stop()
		}
		content := "durable input survives a forced owner death at " + mode
		payload, err := json.Marshal(importTextCommand{Title: "Recovered Publication", Content: content})
		if err != nil {
			t.Fatal(err)
		}
		total := 1
		operation, err := first.Operations.Submit(ctx, operations.Request{
			Type: "import_text", CommandSchemaVersion: operations.CommandSchemaV1,
			BaseID: base.ID, Payload: payload, PreallocateDocument: true,
			TotalUnits: &total, ResourceClass: operations.ResourceIO,
		})
		if err != nil {
			t.Fatal(err)
		}
		if operation.DocumentID == "" {
			t.Fatalf("submission did not preallocate a stable document: %+v", operation)
		}
		if mode == appKillPostPublish {
			operation = waitForAppOperation(t, first, operation.ID, operations.StateSucceeded)
		}
		now := time.Now().UnixMilli()
		if _, err := first.DB.Exec(`UPDATE operations
			SET state = ?, attempt = 1, result = NULL, finished_at = NULL,
			    started_at = ?, state_revision = state_revision + 1, updated_at = ?
			WHERE id = ?`,
			operations.StateRunning, now, now, operation.ID); err != nil {
			t.Fatal(err)
		}
		first.Close()

		markerDir := filepath.Join(home, "kill-marker")
		if err := os.MkdirAll(markerDir, 0o700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(testBinary)
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("%s=%s", appKillModeEnv, mode),
			fmt.Sprintf("%s=%s", appKillOperationID, operation.ID),
			fmt.Sprintf("%s=%s", appKillMarkerEnv, markerDir),
		)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = forceKillTree(cmd)
			_ = cmd.Wait()
		})
		marker := filepath.Join(markerDir, "reached")
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("child did not reach %s breakpoint", mode)
				return operation
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := forceKillTree(cmd); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err == nil {
			t.Fatal("forced kill child exited successfully")
		}
		return operation
	}

	// C02: a new process recovers a command whose old owner died after durable
	// claim/admission but before executor dispatch.
	claimed := runBreakpoint(appKillPreDispatch)
	second, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	recovered := waitForAppOperation(t, second, claimed.ID, operations.StateSucceeded)
	if recovered.Attempt != 3 {
		t.Fatalf("pre-dispatch replay attempt = %d, want 3", recovered.Attempt)
	}
	assertOneReadyImport(t, second, recovered, 1, "durable input survives a forced owner death at "+appKillPreDispatch)

	// C03: a process dies after the document/chunk publication committed but
	// before the operation terminal result; replay reuses the same effect.
	published := runBreakpoint(appKillPostPublish)
	third, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(third.Close)
	recovered = waitForAppOperation(t, third, published.ID, operations.StateSucceeded)
	if recovered.Attempt != 3 {
		t.Fatalf("post-publish replay attempt = %d, want 3", recovered.Attempt)
	}
	assertOneReadyImport(t, third, recovered, 2, "durable input survives a forced owner death at "+appKillPostPublish)
}

func assertOneReadyImport(t *testing.T, application *App, operation operations.Operation, wantFinishEvents int, content string) {
	t.Helper()
	document, _, err := application.Knowledge.GetDocument(operation.DocumentID, false)
	if err != nil {
		t.Fatal(err)
	}
	if document.Title != "Recovered Publication" || document.RawText != content ||
		document.Status != knowledge.StatusReady || document.SourceVersion != 1 ||
		document.ActiveIndexGen != 1 || document.ChunkCount == 0 {
		t.Fatalf("ready publication has wrong identity/content: %+v", document)
	}
	var documents, chunks int
	if err := application.DB.QueryRow(`SELECT COUNT(*) FROM documents WHERE id = ?`,
		operation.DocumentID).Scan(&documents); err != nil {
		t.Fatal(err)
	}
	if err := application.DB.QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ?`,
		operation.DocumentID).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if documents != 1 || chunks != document.ChunkCount {
		t.Fatalf("replay changed durable effect: documents=%d chunks=%d operation chunks=%d",
			documents, chunks, document.ChunkCount)
	}
	var finishEvents int
	if err := application.DB.QueryRow(`SELECT COUNT(*) FROM operation_events
		WHERE operation_id = ? AND kind = ?`, operation.ID, "finished").Scan(&finishEvents); err != nil {
		t.Fatal(err)
	}
	if finishEvents != wantFinishEvents {
		t.Fatalf("terminal finish events = %d, want %d", finishEvents, wantFinishEvents)
	}
}

func TestEmbeddingProcessKillRecoversStagedGeneration(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_DISABLE_MANAGED_RUNTIME", "1")
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	var embeddingRequests atomic.Int32
	embeddingReached := make(chan struct{})
	releaseEmbedding := make(chan struct{})
	embeddingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if embeddingRequests.Add(1) == 1 {
			close(embeddingReached)
			<-releaseEmbedding
		}
		texts := struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}{}
		_ = json.NewDecoder(r.Body).Decode(&texts)
		data := make([]map[string]any, len(texts.Input))
		for index := range texts.Input {
			data[index] = map[string]any{"index": index, "embedding": []float64{1, 0}}
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(embeddingServer.Close)

	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	configText := fmt.Sprintf(`
embedding:
  provider: openai
  baseUrl: %s
  model: kill-model
`, embeddingServer.URL)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	first, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first.Operations.Stop()
	base, err := first.Knowledge.CreateBase("Embedding Kill", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	content := "staged generation survives a forced death during embedding"
	payload, err := json.Marshal(importTextCommand{Title: "Embedding Recovery", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	total := 1
	operation, err := first.Operations.Submit(ctx, operations.Request{
		Type: "import_text", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: payload, PreallocateDocument: true,
		TotalUnits: &total, ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	cmd := exec.Command(testBinary)
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("%s=%s", appKillModeEnv, appKillEmbedding),
		fmt.Sprintf("%s=%s", appKillOperationID, operation.ID),
		fmt.Sprintf("%s=%s", appKillMarkerEnv, filepath.Join(home, "unused-marker")),
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = forceKillTree(cmd)
		_ = cmd.Wait()
	})
	select {
	case <-embeddingReached:
	case <-time.After(20 * time.Second):
		t.Fatal("child did not reach the real embedding request")
	}
	if err := forceKillTree(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("forced embedding child exited successfully")
	}

	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	var state, documentStatus string
	var attempt, activeGeneration, desiredGeneration int64
	var incomplete int
	if err := db.QueryRow(`SELECT state, attempt FROM operations WHERE id = ?`,
		operation.ID).Scan(&state, &attempt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status, incomplete, active_index_generation,
		desired_index_generation FROM documents WHERE id = ?`,
		operation.DocumentID).Scan(&documentStatus, &incomplete,
		&activeGeneration, &desiredGeneration); err != nil {
		t.Fatal(err)
	}
	var stagedChunks, generationMappings, stagedVectors int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ?`,
		operation.DocumentID).Scan(&stagedChunks); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM document_generations WHERE doc_id = ?`,
		operation.DocumentID).Scan(&generationMappings); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ? AND embedding IS NOT NULL`,
		operation.DocumentID).Scan(&stagedVectors); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if state != operations.StateRunning || attempt != 1 || documentStatus != knowledge.StatusProcessing ||
		incomplete != 1 || activeGeneration != 0 || desiredGeneration != 1 ||
		stagedChunks == 0 || generationMappings != 0 || stagedVectors != 0 {
		t.Fatalf("unexpected embedding breakpoint: operation=%s/%d document=%s incomplete=%d generations=%d/%d chunks=%d mappings=%d vectors=%d",
			state, attempt, documentStatus, incomplete, activeGeneration, desiredGeneration,
			stagedChunks, generationMappings, stagedVectors)
	}

	close(releaseEmbedding)
	second, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	recovered := waitForAppOperation(t, second, operation.ID, operations.StateSucceeded)
	if recovered.Attempt != 2 {
		t.Fatalf("embedding replay attempt = %d, want 2", recovered.Attempt)
	}
	document, _, err := second.Knowledge.GetDocument(operation.DocumentID, false)
	if err != nil {
		t.Fatal(err)
	}
	if document.Status != knowledge.StatusReady || document.ActiveIndexGen != 1 ||
		document.SourceVersion != 2 || document.ChunkCount == 0 ||
		!document.EmbeddingReady || document.EmbeddingModel != "openai:kill-model" {
		t.Fatalf("recovered document has wrong published state: status=%s generation=%d version=%d chunks=%d ready=%t model=%s",
			document.Status, document.ActiveIndexGen, document.SourceVersion,
			document.ChunkCount, document.EmbeddingReady, document.EmbeddingModel)
	}
	var mappings, vectors int64
	if err := second.DB.QueryRow(`SELECT COUNT(*) FROM document_generations WHERE doc_id = ?`,
		operation.DocumentID).Scan(&mappings); err != nil {
		t.Fatal(err)
	}
	if err := second.DB.QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ? AND embedding IS NOT NULL`,
		operation.DocumentID).Scan(&vectors); err != nil {
		t.Fatal(err)
	}
	if mappings != 1 || vectors != int64(document.ChunkCount) {
		t.Fatalf("recovered publication mismatch: mappings=%d vectors=%d chunks=%d",
			mappings, vectors, document.ChunkCount)
	}
	result, err := second.Knowledge.Search(ctx, knowledge.SearchRequest{
		Query: content, Mode: "lexical", TopK: 4,
	})
	if err != nil || result.Total < 1 || result.Hits[0].DocID != operation.DocumentID ||
		result.Hits[0].IndexGeneration != 1 {
		t.Fatalf("recovered search invalid: result=%+v err=%v", result, err)
	}
}

func TestLegacyParserProcessKillRecoversRawVersion(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_DISABLE_MANAGED_RUNTIME", "1")
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	slashTestBinary := filepath.ToSlash(testBinary)

	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	parserState := filepath.Join(home, "legacy-parser.marker")
	t.Setenv(appParserStateEnv, parserState)
	configText := fmt.Sprintf(
		"helpers:\n  legacyOffice: '%s'\n",
		fmt.Sprintf(`"%s" -test.run=^TestLegacyParserHelperProcess$ -- {input}`, slashTestBinary),
	)
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	first, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first.Operations.Stop()
	base, err := first.Knowledge.CreateBase("Parser Kill", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("legacy raw payload at parser interruption boundary")
	upload, err := first.Operations.CreateUpload(ctx, operations.UploadCreate{
		BaseID: base.ID, FileName: "legacy.doc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Operations.PutUploadContent(ctx, upload.ID, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Operations.CompleteUpload(ctx, upload.ID); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(importFileCommand{
		UploadID: upload.ID, FileName: "legacy.doc", Conflict: "rename",
	})
	if err != nil {
		t.Fatal(err)
	}
	total := 1
	operation, err := first.Operations.Submit(ctx, operations.Request{
		Type: "import_file", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: payload,
		UploadID: upload.ID, PreallocateDocument: true, TotalUnits: &total,
		ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	cmd := exec.Command(testBinary)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("%s=%s", appKillModeEnv, appKillParserOwner),
	)
	var childOutput bytes.Buffer
	cmd.Stdout = &childOutput
	cmd.Stderr = &childOutput
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = forceKillTree(cmd)
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(parserState); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Logf("parser child output: %s", childOutput.String())
			_ = forceKillTree(cmd)
			_ = cmd.Wait()
			if debugDB, openErr := storage.Open(filepath.Join(home, "knowledge.db")); openErr == nil {
				var debugState string
				var debugAttempt int64
				var debugError, debugDocument string
				if scanErr := debugDB.QueryRow(`SELECT state, attempt, COALESCE(error_message,''),
					COALESCE(document_id,'') FROM operations WHERE id = ?`,
					operation.ID).Scan(&debugState, &debugAttempt, &debugError, &debugDocument); scanErr == nil {
					t.Logf("parser breakpoint operation: state=%s attempt=%d document=%s error=%q",
						debugState, debugAttempt, debugDocument, debugError)
				} else {
					t.Logf("read debug operation: %v", scanErr)
				}
				_ = debugDB.Close()
			} else {
				t.Logf("open debug database: %v", openErr)
			}
			t.Fatal("child did not reach the legacy parser call")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := forceKillTree(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("forced parser child exited successfully")
	}

	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state, documentID, documentStatus, rawPath string
	var attempt, activeGeneration int64
	var incomplete int
	if err := db.QueryRow(`SELECT state, attempt, COALESCE(document_id, '')
		FROM operations WHERE id = ?`,
		operation.ID).Scan(&state, &attempt, &documentID); err != nil {
		t.Fatal(err)
	}
	if documentID == "" {
		t.Fatal("killed operation lost its durable document ID")
	}
	var documentCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM documents`).Scan(&documentCount); err != nil {
		t.Fatal(err)
	}
	t.Logf("breakpoint operation=%s durableDocument=%s documents=%d", operation.ID, documentID, documentCount)
	if err := db.QueryRow(`SELECT status, incomplete, active_index_generation,
		COALESCE(raw_file_path, '') FROM documents WHERE id = ?`,
		documentID).Scan(&documentStatus, &incomplete,
		&activeGeneration, &rawPath); err != nil {
		t.Fatalf("read breakpoint document: operation=%s document=%s documents=%d err=%v",
			operation.ID, documentID, documentCount, err)
	}
	var chunks, mappings, vectors int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ?`,
		documentID).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM document_generations WHERE doc_id = ?`,
		documentID).Scan(&mappings); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ? AND embedding IS NOT NULL`,
		documentID).Scan(&vectors); err != nil {
		t.Fatal(err)
	}
	rawVersions, globErr := filepath.Glob(filepath.Join(home, "raw", base.ID,
		".generations", documentID, "v*.doc"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(rawVersions) != 1 {
		t.Fatalf("staged raw versions = %v, want exactly one", rawVersions)
	}
	stagedRaw, err := os.ReadFile(rawVersions[0])
	if err != nil || string(stagedRaw) != string(content) {
		t.Fatalf("staged raw bytes = %q %v, want %q", stagedRaw, err, content)
	}
	if state != operations.StateRunning || attempt != 1 || documentStatus != knowledge.StatusProcessing ||
		incomplete != 1 || activeGeneration != 0 || rawPath != "" ||
		chunks != 0 || mappings != 0 || vectors != 0 {
		t.Fatalf("unexpected parser breakpoint: operation=%s/%d document=%s incomplete=%d generation=%d raw=%q chunks=%d mappings=%d vectors=%d",
			state, attempt, documentStatus, incomplete, activeGeneration, rawPath,
			chunks, mappings, vectors)
	}

	second, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	recovered := waitForAppOperation(t, second, operation.ID, operations.StateSucceeded)
	if recovered.Attempt != 2 {
		t.Fatalf("parser replay attempt = %d, want 2", recovered.Attempt)
	}
	document, _, err := second.Knowledge.GetDocument(operation.DocumentID, false)
	if err != nil {
		t.Fatal(err)
	}
	if document.Status != knowledge.StatusReady || document.ActiveIndexGen != 1 ||
		document.SourceVersion != 2 || document.ChunkCount == 0 ||
		document.RawText != "legacy parser recovered after forced death" {
		t.Fatalf("recovered parser document mismatch: status=%s generation=%d version=%d chunks=%d text=%q",
			document.Status, document.ActiveIndexGen, document.SourceVersion,
			document.ChunkCount, document.RawText)
	}
	if err := second.DB.QueryRow(`SELECT COUNT(*) FROM document_generations WHERE doc_id = ?`,
		operation.DocumentID).Scan(&mappings); err != nil {
		t.Fatal(err)
	}
	if mappings != 1 {
		t.Fatalf("generation mappings = %d, want 1", mappings)
	}
	result, err := second.Knowledge.Search(ctx, knowledge.SearchRequest{
		Query: "legacy parser recovered", Mode: "lexical", TopK: 4,
	})
	if err != nil || result.Total < 1 || result.Hits[0].DocID != operation.DocumentID ||
		result.Hits[0].IndexGeneration != 1 {
		t.Fatalf("recovered parser search invalid: result=%+v err=%v", result, err)
	}
}
