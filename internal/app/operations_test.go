package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/operations"
)

type gatedBatchEmbedder struct {
	firstStarted  chan struct{}
	firstRelease  chan struct{}
	secondStarted chan struct{}
	secondRelease chan struct{}
	firstOnce     sync.Once
	secondOnce    sync.Once
	calls         atomic.Int32
}

func (e *gatedBatchEmbedder) Embed(_ context.Context, texts []string) ([][]float64, error) {
	switch e.calls.Add(1) {
	case 1:
		e.firstOnce.Do(func() { close(e.firstStarted) })
		<-e.firstRelease
	case 2:
		e.secondOnce.Do(func() { close(e.secondStarted) })
		<-e.secondRelease
	default:
		return nil, context.Canceled
	}
	out := make([][]float64, 0, len(texts))
	for range texts {
		out = append(out, []float64{1, 0})
	}
	return out, nil
}

func (e *gatedBatchEmbedder) ModelKey() string { return "fake:gated-batch" }

func waitForAppOperation(t *testing.T, application *App, id, want string) operations.Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := application.Operations.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == want {
			return op
		}
		if op.State == operations.StateFailed || op.State == operations.StateCancelled {
			t.Fatalf("operation %s ended %s, want %s: %+v", id, op.State, want, op)
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s state = %s, want %s", id, op.State, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAppOperationEnricherCapturesNonSecretReplayMetadata(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	application, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	base, err := application.Knowledge.CreateBase("Envelope Base", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	application.Config.Embedding.APIKey = "secret-api-key"
	payload, err := json.Marshal(importTextCommand{Title: "Envelope", Content: "replay metadata"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := application.Operations.Submit(context.Background(), operations.Request{
		Type: "import_text", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: payload, IdempotencyKey: "app-envelope-key",
		PreallocateDocument: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.PrincipalRef == "" || op.ScopeRef != "base:"+base.ID || op.InputSHA256 == "" ||
		!strings.HasPrefix(op.ConfigSnapshotRef, "config:") || !strings.HasPrefix(op.ModelSnapshotRef, "model:") ||
		op.SourceVersion == "" || op.ExpectedTargetEpoch == nil || op.ExpectedAncestorEpoch == nil ||
		!strings.HasPrefix(op.SourceVersion, "base:") || strings.Contains(op.ConfigSnapshotRef, "secret-api-key") {
		t.Fatalf("operation replay metadata = %+v", op)
	}
	if recovered := waitForAppOperation(t, application, op.ID, operations.StateSucceeded); recovered.ID != op.ID {
		t.Fatalf("completed operation changed identity: %+v", recovered)
	}
	items, err := application.Operations.ListItems(context.Background(), op.ID, 10)
	if err != nil || len(items) != 1 || items[0].ItemKey != op.DocumentID || !items[0].Committed {
		t.Fatalf("import publication marker = %+v, err=%v", items, err)
	}
	if err := application.Knowledge.DeleteBase(base.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := application.Operations.Submit(context.Background(), operations.Request{
		Type: "import_text", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: payload, IdempotencyKey: "app-envelope-key",
		PreallocateDocument: true,
	})
	if err != nil || replayed.ID != op.ID {
		t.Fatalf("replay after target deletion = %+v, err=%v", replayed, err)
	}
}

func TestImportFilesOperationPersistsStableItemAllocations(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	ctx := context.Background()
	application, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	base, err := application.Knowledge.CreateBase("Batch envelope", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	files := []knowledge.AddFilesItem{
		{FileName: "first.txt", ContentBase64: base64.StdEncoding.EncodeToString([]byte("first batch file"))},
		{FileName: "second.txt", ContentBase64: base64.StdEncoding.EncodeToString([]byte("second batch file"))},
	}
	payload, err := json.Marshal(importFilesCommand{Files: files, Conflict: "rename"})
	if err != nil {
		t.Fatal(err)
	}
	total := len(files)
	op, err := application.Operations.Submit(ctx, operations.Request{
		Type: "import_files", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: payload, TotalUnits: &total, ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	op = waitForAppOperation(t, application, op.ID, operations.StateSucceeded)
	items, err := application.Operations.ListItems(ctx, op.ID, 10)
	if err != nil || len(items) != len(files) {
		t.Fatalf("batch item markers = %+v, err=%v", items, err)
	}
	seenIDs := map[string]bool{}
	allocatedIDs := make([]string, len(items))
	for index, item := range items {
		if item.State != operations.ItemCommitted || !item.Committed {
			t.Fatalf("batch item %d not committed: %+v", index, item)
		}
		var allocation importFileAllocation
		if err := json.Unmarshal(item.Result, &allocation); err != nil || allocation.DocumentID == "" || seenIDs[allocation.DocumentID] {
			t.Fatalf("batch item %d allocation = %s, err=%v", index, item.Result, err)
		}
		allocatedIDs[index] = allocation.DocumentID
		seenIDs[allocation.DocumentID] = true
	}
	replayFiles := []knowledge.AddFilesItem{
		{FileName: "first.txt", ContentBase64: files[0].ContentBase64},
		{FileName: "second.txt", ContentBase64: files[1].ContentBase64},
	}
	replay := op
	replay.Attempt++
	if err := application.prepareImportFilesOperation(ctx, replay, replayFiles); err != nil {
		t.Fatal(err)
	}
	for index, file := range replayFiles {
		if file.DocumentID != allocatedIDs[index] {
			t.Fatalf("replay changed item %d allocation: got %q want %q", index, file.DocumentID, allocatedIDs[index])
		}
	}
	documents, err := application.Knowledge.ListDocuments(base.ID)
	if err != nil || len(documents) != len(files) {
		t.Fatalf("batch documents = %d, err=%v", len(documents), err)
	}
}

func TestImportFilesExecutorSkipsResolvedItems(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	ctx := context.Background()
	application, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	base, err := application.Knowledge.CreateBase("Resolved batch", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	files := []knowledge.AddFilesItem{{
		FileName:      "already-committed.txt",
		ContentBase64: base64.StdEncoding.EncodeToString([]byte("must not be imported")),
	}}
	payload, err := json.Marshal(importFilesCommand{Files: files, Conflict: "replace"})
	if err != nil {
		t.Fatal(err)
	}
	operations.SetTestPreDispatchHook(func(op operations.Operation) {
		if op.Type != "import_files" {
			return
		}
		if err := application.Operations.MarkItem(ctx, op.ID, op.Attempt,
			importFilesItemKey(0, files[0].FileName), operations.ItemCommitted,
			importFileAllocation{DocumentID: "historical-document-id"}, "", ""); err != nil {
			t.Errorf("seed resolved batch item: %v", err)
		}
	})
	t.Cleanup(func() { operations.SetTestPreDispatchHook(nil) })
	total := 1
	op, err := application.Operations.Submit(ctx, operations.Request{
		Type: "import_files", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: payload, TotalUnits: &total, ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	if op = waitForAppOperation(t, application, op.ID, operations.StateSucceeded); op.State != operations.StateSucceeded {
		t.Fatalf("resolved batch operation: %+v", op)
	}
	documents, err := application.Knowledge.ListDocuments(base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 0 {
		t.Fatalf("resolved batch item produced business effect: %+v", documents)
	}
}

func TestDirectoryExecutorsSkipResolvedAggregates(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	ctx := context.Background()
	application, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	base, err := application.Knowledge.CreateBase("Resolved directory", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "tracked-directory")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	directory, err := application.Knowledge.CreateDirectory(base.ID, "tracked-directory", "", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	operations.SetTestPreDispatchHook(func(op operations.Operation) {
		var itemKey string
		switch op.Type {
		case "import_directory":
			tracked, findErr := application.Knowledge.FindDirectoryByPath(op.BaseID, root)
			if findErr != nil {
				t.Errorf("find tracked directory for import marker: %v", findErr)
				return
			}
			itemKey = tracked.ID
		case "rescan_directory", "delete_directory":
			itemKey = op.DocumentID
		default:
			return
		}
		if err := application.Operations.MarkItem(ctx, op.ID, op.Attempt, itemKey,
			operations.ItemCommitted, nil, "", ""); err != nil {
			t.Errorf("seed directory aggregate marker: %v", err)
		}
	})
	t.Cleanup(func() { operations.SetTestPreDispatchHook(nil) })

	pathPayload, err := json.Marshal(importDirectoryCommand{Path: root})
	if err != nil {
		t.Fatal(err)
	}
	importOp, err := application.Operations.Submit(ctx, operations.Request{
		Type: "import_directory", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: pathPayload, ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForAppOperation(t, application, importOp.ID, operations.StateSucceeded)

	for _, kind := range []string{"rescan_directory", "delete_directory"} {
		payload, err := json.Marshal(documentCommand{DocumentID: directory.ID})
		if err != nil {
			t.Fatal(err)
		}
		op, err := application.Operations.Submit(ctx, operations.Request{
			Type: kind, CommandSchemaVersion: operations.CommandSchemaV1,
			BaseID: base.ID, DocumentID: directory.ID, Payload: payload,
			ResourceClass: operations.ResourceIO,
		})
		if err != nil {
			t.Fatal(err)
		}
		waitForAppOperation(t, application, op.ID, operations.StateSucceeded)
	}
	if current, _, err := application.Knowledge.GetDocument(directory.ID, false); err != nil || current.Status != knowledge.StatusReady {
		t.Fatalf("resolved directory operations changed the document: %+v %v", current, err)
	}
}

func TestSingletonExecutorsSkipResolvedEffects(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	ctx := context.Background()
	application, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	target := filepath.Join(t.TempDir(), "model-cache")
	markers := map[string]string{
		"download_ocr_model":         "effect:download_ocr_model",
		"remove_ocr_model":           "effect:remove_ocr_model",
		"download_model":             "effect:download_model:embedding:fixture-model",
		"remove_model":               "effect:remove_model:fixture-model",
		"self_test_reranker":         "effect:self_test_reranker:fixture-reranker",
		"plan_model_cache_migration": "effect:plan_model_cache_migration:" + target,
		"migrate_model_cache":        "effect:migrate_model_cache:" + target + ":false",
		"ollama_pull":                "effect:ollama_pull:fixture:latest",
		"ollama_delete":              "effect:ollama_delete:fixture:latest",
		"maintenance_storage":        "effect:maintenance_storage:false:false:true:false:false:0",
	}
	operations.SetTestPreDispatchHook(func(op operations.Operation) {
		itemKey := markers[op.Type]
		if itemKey == "" {
			return
		}
		if err := application.Operations.MarkItem(ctx, op.ID, op.Attempt, itemKey,
			operations.ItemCommitted, map[string]any{"replayed": true, "operationType": op.Type}, "", ""); err != nil {
			t.Errorf("seed %s marker: %v", op.Type, err)
		}
	})
	t.Cleanup(func() { operations.SetTestPreDispatchHook(nil) })
	cases := []struct {
		kind    string
		payload any
		class   string
	}{
		{"download_ocr_model", map[string]any{}, operations.ResourceModel},
		{"remove_ocr_model", map[string]any{}, operations.ResourceModel},
		{"download_model", map[string]any{"id": "fixture-model", "kind": "embedding"}, operations.ResourceModel},
		{"remove_model", map[string]any{"id": "fixture-model", "kind": "embedding"}, operations.ResourceModel},
		{"self_test_reranker", map[string]any{"id": "fixture-reranker"}, operations.ResourceModel},
		{"plan_model_cache_migration", map[string]any{"targetDir": target}, operations.ResourceIO},
		{"migrate_model_cache", map[string]any{"targetDir": target}, operations.ResourceIO},
		{"ollama_pull", map[string]any{"model": "fixture:latest"}, operations.ResourceNetwork},
		{"ollama_delete", map[string]any{"model": "fixture:latest"}, operations.ResourceNetwork},
		{"maintenance_storage", map[string]any{"sqliteOnly": true}, operations.ResourceMaintenance},
	}
	for index, testCase := range cases {
		payload, err := json.Marshal(testCase.payload)
		if err != nil {
			t.Fatalf("marshal %s: %v", testCase.kind, err)
		}
		op, err := application.Operations.Submit(ctx, operations.Request{
			Type: testCase.kind, CommandSchemaVersion: operations.CommandSchemaV1,
			Payload: payload, ResourceClass: testCase.class,
			IdempotencyKey: fmt.Sprintf("resolved-singleton-%d", index),
		})
		if err != nil {
			t.Fatalf("submit %s: %v", testCase.kind, err)
		}
		completed := waitForAppOperation(t, application, op.ID, operations.StateSucceeded)
		items, err := application.Operations.ListItems(ctx, op.ID, 5)
		if err != nil || len(items) != 1 || len(items[0].Result) == 0 {
			t.Fatalf("resolved %s marker result = %+v, err=%v", testCase.kind, items, err)
		}
		if string(completed.Result) != string(items[0].Result) {
			t.Fatalf("resolved %s result was not recovered from marker: operation=%s marker=%s", testCase.kind, completed.Result, items[0].Result)
		}
	}
}

func TestResolvedURLOperationConsumesLeftoverCapture(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	ctx := context.Background()
	application, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	base, err := application.Knowledge.CreateBase("URL cleanup", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := application.Knowledge.AddTextDocument(ctx, base.ID, "already published", "published body")
	if err != nil {
		t.Fatal(err)
	}

	operations.SetTestPreDispatchHook(func(op operations.Operation) {
		if op.Type != "import_url" {
			return
		}
		if _, err := application.Operations.EnsureURLCapture(ctx, op.ID,
			"https://example.invalid/source", "https://example.invalid/source", "text/plain",
			[]byte("captured body")); err != nil {
			t.Errorf("seed URL capture: %v", err)
			return
		}
		if err := application.Operations.MarkItem(ctx, op.ID, op.Attempt, op.DocumentID,
			operations.ItemCommitted, map[string]any{"published": true}, "", ""); err != nil {
			t.Errorf("seed URL marker: %v", err)
		}
	})
	t.Cleanup(func() { operations.SetTestPreDispatchHook(nil) })

	payload, err := json.Marshal(importURLCommand{URL: "https://example.invalid/source"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := application.Operations.Submit(ctx, operations.Request{
		Type: "import_url", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, DocumentID: doc.ID, Payload: payload,
		ResourceClass: operations.ResourceNetwork, IdempotencyKey: "url-cleanup-replay",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForAppOperation(t, application, op.ID, operations.StateSucceeded)
	capture, err := application.Operations.GetURLCapture(ctx, op.ID)
	if err != nil || capture.State != operations.URLStateConsumed || len(capture.Body) != 0 {
		t.Fatalf("resolved URL capture = %+v, err=%v", capture, err)
	}
}

func TestCancelBatchReindexKeepsCommittedPartialResult(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	ctx := context.Background()
	application, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	base, err := application.Knowledge.CreateBase("Batch Cancel", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := application.Knowledge.AddTextDocument(ctx, base.ID, "First", "# First\n\ncommitted before cancellation")
	if err != nil {
		t.Fatal(err)
	}
	second, err := application.Knowledge.AddTextDocument(ctx, base.ID, "Second", "# Second\n\nuncommitted at cancellation")
	if err != nil {
		t.Fatal(err)
	}

	// Activation is normally lexical because the default provider is none.
	// Enable the gated provider only for re-indexing so each batch unit has
	// an explicit, repeatable commit/cancel breakpoint.
	application.Config.Embedding.Provider = "openai"
	application.Knowledge.SetGlobalConfig(application.Config)
	embedder := &gatedBatchEmbedder{
		firstStarted:  make(chan struct{}),
		firstRelease:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		secondRelease: make(chan struct{}),
	}
	application.Knowledge.SetProviders(embedder, nil)

	payload, err := json.Marshal(reindexDocumentsCommand{DocumentIDs: []string{first.ID, second.ID}})
	if err != nil {
		t.Fatal(err)
	}
	total := 2
	op, err := application.Operations.Submit(ctx, operations.Request{
		Type: "reindex_documents", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: payload, TotalUnits: &total,
		ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-embedder.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first reindex did not start")
	}
	close(embedder.firstRelease)
	select {
	case <-embedder.secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second reindex did not start after first commit")
	}
	cancelling, err := application.Operations.Cancel(op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelling.State != operations.StateCancelling || !cancelling.CancelRequested {
		t.Fatalf("cancel state = %+v", cancelling)
	}
	close(embedder.secondRelease)

	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := application.Operations.Get(op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == operations.StateCancelled {
			op = current
			break
		}
		if current.State == operations.StateFailed || current.State == operations.StateSucceeded {
			t.Fatalf("batch ended %s instead of cancelled: %+v", current.State, current)
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch state = %s", current.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var result documentOperationResult
	if err := json.Unmarshal(op.Result, &result); err != nil {
		t.Fatalf("decode partial result %s: %v", op.Result, err)
	}
	if result.Succeeded != 1 || result.Failed != 0 || result.Skipped != 0 || !result.Partial {
		t.Fatalf("partial result = %+v", result)
	}
	items, err := application.Operations.ListItems(context.Background(), op.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ItemKey != first.ID || !items[0].Committed ||
		items[1].ItemKey != second.ID || items[1].State != operations.ItemCancelled || items[1].Committed {
		t.Fatalf("partial operation items = %+v", items)
	}
	ready, _, err := application.Knowledge.GetDocument(first.ID, false)
	if err != nil || ready.Status != knowledge.StatusReady {
		t.Fatalf("committed first document: %+v %v", ready, err)
	}
	uncommitted, _, err := application.Knowledge.GetDocument(second.ID, false)
	if err != nil || uncommitted.Status == knowledge.StatusReady {
		t.Fatalf("uncommitted second document leaked ready state: %+v %v", uncommitted, err)
	}
	if _, err := application.Operations.Retry(op.ID); err == nil {
		t.Fatal("cancelled partial batch accepted retry")
	}
}

func TestCancelDeleteBatchStopsAfterCommittedUnit(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	application, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	base, err := application.Knowledge.CreateBase("Batch Delete Cancel", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := application.Knowledge.AddTextDocument(ctx, base.ID, "First", "committed deletion")
	if err != nil {
		t.Fatal(err)
	}
	second, err := application.Knowledge.AddTextDocument(ctx, base.ID, "Second", "protected by cancellation")
	if err != nil {
		t.Fatal(err)
	}
	report := func(operations.Progress) {}
	probe := func(index int, result documentOperationResult) {
		if index == 0 && result.Succeeded == 1 {
			cancel()
		}
	}
	result, err := application.runDeleteDocumentsWithProbe(ctx, base.ID,
		[]string{first.ID, second.ID}, report, probe)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("delete batch error = %v, want context.Canceled", err)
	}
	if result.Succeeded != 1 || result.Failed != 0 || result.Skipped != 0 || !result.Partial {
		t.Fatalf("partial delete result = %+v", result)
	}
	if _, _, err := application.Knowledge.GetDocument(first.ID, false); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatalf("committed deletion: %v", err)
	}
	if _, _, err := application.Knowledge.GetDocument(second.ID, false); err != nil {
		t.Fatalf("uncommitted document was deleted: %v", err)
	}
}

func TestQueuedDeleteBatchCancellationPreservesDocuments(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	ctx := context.Background()
	application, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	application.Operations.SetResourceLimits(operations.ResourceLimits{
		IO:          1,
		DBWrite:     1,
		Disk:        1,
		Network:     1,
		Model:       1,
		Maintenance: 1,
		MaxPerBase:  1,
	})
	base, err := application.Knowledge.CreateBase("Queued Delete Cancel", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := application.Knowledge.AddTextDocument(ctx, base.ID, "First", "must survive cancel")
	if err != nil {
		t.Fatal(err)
	}
	second, err := application.Knowledge.AddTextDocument(ctx, base.ID, "Second", "must also survive cancel")
	if err != nil {
		t.Fatal(err)
	}

	application.Config.Embedding.Provider = "openai"
	application.Knowledge.SetGlobalConfig(application.Config)
	blocked := &gatedBatchEmbedder{
		firstStarted:  make(chan struct{}),
		firstRelease:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		secondRelease: make(chan struct{}),
	}
	application.Knowledge.SetProviders(blocked, nil)
	importPayload, err := json.Marshal(importTextCommand{Title: "Quota Owner", Content: "occupies the base slot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Operations.Submit(ctx, operations.Request{
		Type: "import_text", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: importPayload, PreallocateDocument: true,
		ResourceClass: operations.ResourceIO,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("quota-owning import did not start")
	}

	deletePayload, err := json.Marshal(deleteDocumentsCommand{DocumentIDs: []string{first.ID, second.ID}})
	if err != nil {
		t.Fatal(err)
	}
	total := 2
	deleteOp, err := application.Operations.Submit(ctx, operations.Request{
		Type: "delete_documents", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: deletePayload, TotalUnits: &total,
		ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleteOp.State != operations.StateQueued {
		t.Fatalf("delete operation state = %s, want queued", deleteOp.State)
	}
	cancelled, err := application.Operations.Cancel(deleteOp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != operations.StateCancelled || cancelled.Attempt != 0 {
		t.Fatalf("queued delete cancellation = %+v", cancelled)
	}
	close(blocked.firstRelease)

	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := application.Operations.Get(deleteOp.ID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateCancelled {
			break
		}
		if op.State != operations.StateCancelled && op.StateRevision > cancelled.StateRevision {
			t.Fatalf("cancelled delete mutated after terminal intent: %+v", op)
		}
		if time.Now().After(deadline) {
			t.Fatalf("delete state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, document := range []knowledge.Document{first, second} {
		if _, _, err := application.Knowledge.GetDocument(document.ID, false); err != nil {
			t.Fatalf("document %s did not survive queued cancellation: %v", document.ID, err)
		}
	}
	if _, err := application.Operations.Retry(deleteOp.ID); err == nil {
		t.Fatal("cancelled delete batch accepted retry")
	}
}

func TestImportOperationPreDispatchRecoveryCreatesBusinessEffect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	ctx := context.Background()
	first, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first.Operations.SetResourceLimits(operations.ResourceLimits{
		IO: 1, DBWrite: 1, Disk: 1, Network: 1, Model: 1,
		Maintenance: 1, MaxPerBase: 1,
	})
	base, err := first.Knowledge.CreateBase("Pre-dispatch Recovery", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	first.Config.Embedding.Provider = "openai"
	first.Knowledge.SetGlobalConfig(first.Config)
	blocked := &gatedBatchEmbedder{
		firstStarted:  make(chan struct{}),
		firstRelease:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		secondRelease: make(chan struct{}),
	}
	first.Knowledge.SetProviders(blocked, nil)

	ownerPayload, err := json.Marshal(importTextCommand{
		Title: "Quota Owner", Content: "occupies the same-base admission slot",
	})
	if err != nil {
		t.Fatal(err)
	}
	total := 1
	if _, err := first.Operations.Submit(ctx, operations.Request{
		Type: "import_text", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: ownerPayload, PreallocateDocument: true,
		TotalUnits: &total, ResourceClass: operations.ResourceIO,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("quota-owning import did not start")
	}

	commandContent := "the claimed command creates exactly one publication after restart"
	commandPayload, err := json.Marshal(importTextCommand{
		Title:   "Recovered Input",
		Content: commandContent,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := first.Operations.Submit(ctx, operations.Request{
		Type: "import_text", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: commandPayload, PreallocateDocument: true,
		TotalUnits: &total, ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.State != operations.StateQueued || claimed.DocumentID == "" {
		t.Fatalf("claimed command was not queued with a stable target: %+v", claimed)
	}
	if _, _, err := first.Knowledge.GetDocument(claimed.DocumentID, false); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatalf("pre-dispatch command already had a business effect: %v", err)
	}

	// Move the queued command to the exact post-claim/pre-dispatch
	// breakpoint. Its immutable command, target, and attempt survive; no
	// executor work has happened.
	now := time.Now().UnixMilli()
	if _, err := first.DB.Exec(`UPDATE operations
		SET state = ?, attempt = 1, started_at = ?,
		    state_revision = state_revision + 1, updated_at = ?
		WHERE id = ? AND state = ?`,
		operations.StateRunning, now, now, claimed.ID, operations.StateQueued); err != nil {
		t.Fatal(err)
	}
	close(blocked.firstRelease)
	waitForAppOperation(t, first, claimed.ID, operations.StateRunning)
	first.Close()

	second, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	recovered := waitForAppOperation(t, second, claimed.ID, operations.StateSucceeded)
	if recovered.Attempt != 2 {
		t.Fatalf("recovered attempt = %d, want 2", recovered.Attempt)
	}
	var result documentOperationResult
	if err := json.Unmarshal(recovered.Result, &result); err != nil {
		t.Fatalf("decode recovered result %s: %v", recovered.Result, err)
	}
	if len(result.Documents) != 1 || result.Documents[0].ID != claimed.DocumentID {
		t.Fatalf("recovery changed stable target: %+v", result)
	}
	document, _, err := second.Knowledge.GetDocument(claimed.DocumentID, false)
	if err != nil {
		t.Fatal(err)
	}
	if document.BaseID != base.ID || document.Title != "Recovered Input" ||
		document.RawText != commandContent || document.Status != knowledge.StatusReady {
		t.Fatalf("recovered business effect has wrong input: %+v", document)
	}
	if document.SourceVersion != 1 || document.ActiveIndexGen != 1 || document.ChunkCount == 0 {
		t.Fatalf("recovered business effect was not published once: %+v", document)
	}
	var dbChunks, dbDocuments int
	if err := second.DB.QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ?`,
		document.ID).Scan(&dbChunks); err != nil {
		t.Fatal(err)
	}
	if err := second.DB.QueryRow(`SELECT COUNT(*) FROM documents WHERE id = ?`,
		document.ID).Scan(&dbDocuments); err != nil {
		t.Fatal(err)
	}
	if dbChunks != document.ChunkCount || dbDocuments != 1 {
		t.Fatalf("recovery duplicated business effect: documents=%d chunks=%d want chunks=%d",
			dbDocuments, dbChunks, document.ChunkCount)
	}
}

func TestImportOperationRestartReusesPublishedDocument(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	ctx := context.Background()
	first, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base, err := first.Knowledge.CreateBase("Published Recovery", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(importTextCommand{
		Title:   "Recovered Publication",
		Content: "# Recovery\n\nthe business effect survives terminal-write loss",
	})
	if err != nil {
		t.Fatal(err)
	}
	total := 1
	op, err := first.Operations.Submit(ctx, operations.Request{
		Type: "import_text", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, Payload: payload, PreallocateDocument: true,
		TotalUnits: &total, ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err = first.Operations.Get(op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateSucceeded {
			break
		}
		if op.State == operations.StateFailed || op.State == operations.StateCancelled {
			t.Fatalf("initial import ended %s: %+v", op.State, op)
		}
		if time.Now().After(deadline) {
			t.Fatalf("initial import state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if op.DocumentID == "" {
		t.Fatalf("successful import omitted document ID: %+v", op)
	}
	published, _, err := first.Knowledge.GetDocument(op.DocumentID, false)
	if err != nil || published.Status != knowledge.StatusReady {
		t.Fatalf("published document: %+v %v", published, err)
	}
	var publishedChunks int
	if err := first.DB.QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ?`,
		published.ID).Scan(&publishedChunks); err != nil {
		t.Fatal(err)
	}
	if publishedChunks == 0 {
		t.Fatalf("published document has no chunks: %+v", published)
	}

	// Move the durable row back to the exact post-publish crash breakpoint:
	// the ready document is committed, but the operation result and terminal
	// state were lost. Only the persisted command and attempt remain.
	if _, err := first.DB.Exec(`UPDATE operations
		SET state = ?, result = NULL, finished_at = NULL,
		    state_revision = state_revision + 1, updated_at = ?
		WHERE id = ?`, operations.StateRunning, time.Now().UnixMilli(), op.ID); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	deadline = time.Now().Add(5 * time.Second)
	for {
		op, err = second.Operations.Get(op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateSucceeded {
			break
		}
		if op.State == operations.StateFailed || op.State == operations.StateCancelled {
			t.Fatalf("recovered import ended %s: %+v", op.State, op)
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovered import state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if op.Attempt != 2 {
		t.Fatalf("recovered attempt = %d, want 2", op.Attempt)
	}
	var result documentOperationResult
	if err := json.Unmarshal(op.Result, &result); err != nil {
		t.Fatalf("decode recovered result %s: %v", op.Result, err)
	}
	if len(result.Documents) != 1 || result.Documents[0].ID != published.ID {
		t.Fatalf("recovered result did not reuse publication: %+v", result)
	}
	recovered, _, err := second.Knowledge.GetDocument(published.ID, false)
	if err != nil || recovered.Status != knowledge.StatusReady {
		t.Fatalf("recovered document: %+v %v", recovered, err)
	}
	if recovered.SourceVersion != published.SourceVersion ||
		recovered.ActiveIndexGen != published.ActiveIndexGen ||
		recovered.ChunkCount != published.ChunkCount {
		t.Fatalf("recovery changed publication: before=%+v after=%+v", published, recovered)
	}
	var chunkCount int
	if err := second.DB.QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ?`,
		published.ID).Scan(&chunkCount); err != nil {
		t.Fatal(err)
	}
	if chunkCount != publishedChunks {
		t.Fatalf("chunk count changed from %d to %d", publishedChunks, chunkCount)
	}
	documents, err := second.Knowledge.ListDocuments(base.ID)
	if err != nil || len(documents) != 1 {
		t.Fatalf("document count after recovery: %v %v", documents, err)
	}
}

func TestDurableDeleteCleanupContinuesAfterCommittedCancel(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	ctx := context.Background()
	application, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	base, err := application.Knowledge.CreateBase("Boundary Cancel", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	document, err := application.Knowledge.AddTextDocument(ctx, base.ID, "Boundary", "# Boundary\n\nlogical fence then physical cleanup")
	if err != nil {
		t.Fatal(err)
	}

	boundaryDone := make(chan struct{})
	boundaryEntered := make(chan struct{})
	operationIDs := make(chan string, 1)
	var boundaryErr error
	application.deleteDocumentBoundary = func() {
		// This runs only after markDocumentTreeDeleting committed and before
		// the first physical cleanup item. The cancel intent must not leave
		// the committed tombstone half-cleaned.
		close(boundaryEntered)
		operationID := <-operationIDs
		_, boundaryErr = application.Operations.Cancel(operationID)
		close(boundaryDone)
	}

	payload, err := json.Marshal(documentCommand{DocumentID: document.ID})
	if err != nil {
		t.Fatal(err)
	}
	total := 1
	operation, err := application.Operations.Submit(ctx, operations.Request{
		Type: "delete_document", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, DocumentID: document.ID, Payload: payload,
		TotalUnits: &total, ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	operationID := operation.ID
	// Submit creates the durable ID before returning; hold the executor at
	// the boundary until the test can hand that authoritative ID back.
	select {
	case <-boundaryEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("delete cleanup boundary was not reached")
	}
	select {
	case operationIDs <- operationID:
	case <-time.After(2 * time.Second):
		t.Fatal("boundary did not accept operation ID")
	}
	select {
	case <-boundaryDone:
	case <-time.After(2 * time.Second):
		t.Fatal("boundary cancel did not complete")
	}
	if boundaryErr != nil {
		t.Fatalf("record cancel intent at cleanup boundary: %v", boundaryErr)
	}

	deadline := time.Now().Add(5 * time.Second)
	var op operations.Operation
	for {
		op, err = application.Operations.Get(operationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State == operations.StateSucceeded {
			break
		}
		if op.State == operations.StateCancelled || op.State == operations.StateFailed {
			t.Fatalf("committed cleanup ended %s: %+v", op.State, op)
		}
		if time.Now().After(deadline) {
			t.Fatalf("delete operation state = %s", op.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !op.CancelRequested {
		t.Fatalf("cancel intent was not persisted at boundary: %+v", op)
	}
	var result documentOperationResult
	if err := json.Unmarshal(op.Result, &result); err != nil {
		t.Fatalf("decode delete result %s: %v", op.Result, err)
	}
	if result.Succeeded != 1 || result.Partial {
		t.Fatalf("completed cleanup result = %+v", result)
	}
	if _, _, err := application.Knowledge.GetDocument(document.ID, false); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatalf("deleted document remained readable: %v", err)
	}
	var chunks int
	if err := application.DB.QueryRow(`SELECT COUNT(*) FROM chunks WHERE doc_id = ?`,
		document.ID).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if chunks != 0 {
		t.Fatalf("chunks remained after completed cleanup: %d", chunks)
	}
	if _, err := application.Operations.Retry(operationID); err == nil {
		t.Fatal("successful cleanup accepted retry after committed cancel intent")
	}
}

func TestDurableDeleteReplayResumesCommittedPhysicalCleanup(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_HOME", t.TempDir())
	ctx := context.Background()
	application, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	base, err := application.Knowledge.CreateBase("Replay Cleanup", "", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	document, err := application.Knowledge.AddFileDocument(ctx, base.ID, "replay.txt", []byte("logical fence and physical cleanup"), "")
	if err != nil {
		t.Fatal(err)
	}
	if document.RawFilePath == "" {
		t.Fatal("file document did not publish a raw path")
	}
	rawPath := filepath.Join(application.RawStore.Root(), filepath.FromSlash(document.RawFilePath))
	if err := os.Remove(rawPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rawPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rawPath, "blocker"), []byte("keep cleanup failing"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The directory-shaped raw path makes the logical fence commit but forces
	// the subsequent physical remove to fail. This is the crash/cleanup window
	// that a committed item marker must not hide on replay.
	payload, err := json.Marshal(documentCommand{DocumentID: document.ID})
	if err != nil {
		t.Fatal(err)
	}
	total := 1
	op, err := application.Operations.Submit(ctx, operations.Request{
		Type: "delete_document", CommandSchemaVersion: operations.CommandSchemaV1,
		BaseID: base.ID, DocumentID: document.ID, Payload: payload,
		TotalUnits: &total, ResourceClass: operations.ResourceIO,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, getErr := application.Operations.Get(op.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.State == operations.StateFailed {
			op = current
			break
		}
		if current.State == operations.StateSucceeded || current.State == operations.StateCancelled || time.Now().After(deadline) {
			t.Fatalf("initial cleanup failure state = %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
	items, err := application.Operations.ListItems(ctx, op.ID, 10)
	if err != nil || len(items) != 1 || !items[0].Committed {
		t.Fatalf("logical delete marker after cleanup failure = %+v, err=%v", items, err)
	}
	if err := os.RemoveAll(rawPath); err != nil {
		t.Fatal(err)
	}
	if _, err := application.Operations.Retry(op.ID); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		current, getErr := application.Operations.Get(op.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if current.State == operations.StateSucceeded {
			break
		}
		if current.State == operations.StateFailed || current.State == operations.StateCancelled || time.Now().After(deadline) {
			t.Fatalf("cleanup replay state = %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(rawPath); !os.IsNotExist(err) {
		t.Fatalf("raw cleanup replay left path, err=%v", err)
	}
}
