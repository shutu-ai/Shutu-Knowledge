package models

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestModelReadContextHonorsCancellation(t *testing.T) {
	manager := NewManager(t.TempDir(), "https://huggingface.co", http.DefaultClient)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.GetContext(ctx, "owner/model"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled model lookup error = %v, want context.Canceled", err)
	}
	if _, err := manager.ListPageContext(ctx, 10, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled model page lookup error = %v, want context.Canceled", err)
	}
	if _, err := manager.OCRStatusContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled OCR status error = %v, want context.Canceled", err)
	}
}

func TestManagerDownloadListAndRemove(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/resolve/main/model.onnx") && !strings.HasSuffix(r.URL.Path, "/resolve/main/tokenizer.json") {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/model.onnx") {
			_, _ = w.Write([]byte("onnx-bytes"))
			return
		}
		_, _ = w.Write([]byte("tokenizer"))
	}))
	defer server.Close()

	root := t.TempDir()
	manager := NewManager(root, server.URL, server.Client())
	var progress []int
	err := manager.Download(context.Background(), DownloadRequest{
		ID: "example/model", Kind: KindEmbedding,
		Artifacts: []string{"tokenizer.json", "model.onnx"},
	}, func(value int) { progress = append(progress, value) })
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) == 0 || progress[len(progress)-1] != 100 {
		t.Fatalf("download progress: %v", progress)
	}

	models, err := manager.List()
	if err != nil || len(models) != 1 {
		t.Fatalf("list: %v %+v", err, models)
	}
	if models[0].Status != "installed" || models[0].Lifecycle != LifecycleInstalled || models[0].Ready || models[0].Runtime != LifecycleRuntimeMiss || models[0].Kind != KindEmbedding || models[0].SizeBytes != 19 {
		t.Fatalf("ready model: %+v", models[0])
	}
	if _, err := os.Stat(filepath.Join(root, "example", "model", "model.onnx")); err != nil {
		t.Fatalf("artifact path: %v", err)
	}
	if err := manager.Download(context.Background(), DownloadRequest{ID: "example/model", Kind: KindEmbedding}, nil); err == nil {
		t.Fatal("duplicate download should fail")
	}
	if err := manager.Remove("example/model"); err != nil {
		t.Fatal(err)
	}
	models, _ = manager.List()
	if len(models) != 0 {
		t.Fatalf("model was not removed: %+v", models)
	}
}

func TestManagerListPageBoundsAndAdvances(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root, "https://huggingface.co", http.DefaultClient)
	for _, id := range []string{"owner/a", "owner/b", "owner/c"} {
		dir, err := manager.modelDir(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "model.onnx"), []byte(id), 0o600); err != nil {
			t.Fatal(err)
		}
		manifest := fmt.Sprintf(`{"id":%q,"kind":"embedding","artifacts":["model.onnx"]}`, id)
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	page, err := manager.ListPage(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Models) != 2 || page.Models[0].ID != "owner/a" || page.Models[1].ID != "owner/b" || !page.HasMore || page.NextOffset != 2 {
		t.Fatalf("first model page: %+v", page)
	}
	next, err := manager.ListPage(2, page.NextOffset)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Models) != 1 || next.Models[0].ID != "owner/c" || next.HasMore || next.NextOffset != 0 {
		t.Fatalf("last model page: %+v", next)
	}
	if _, err := manager.ListPage(2, -1); err == nil {
		t.Fatal("negative model page offset should fail")
	}

	missing := NewManager(filepath.Join(t.TempDir(), "not-created"), "https://huggingface.co", http.DefaultClient)
	empty, err := missing.ListPage(2, 0)
	if err != nil || len(empty.Models) != 0 || empty.HasMore {
		t.Fatalf("missing cache should be an empty page: %+v %v", empty, err)
	}
}

func TestManagerCountContextDoesNotDecodeCatalog(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root, "https://huggingface.co", http.DefaultClient)
	for _, id := range []string{"owner/a", "owner/b"} {
		dir, err := manager.modelDir(id)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("not-json"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	count, err := manager.CountContext(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("manifest count: %d %v", count, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.CountContext(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled count: %v", err)
	}
}

func TestManagerRejectsUnsafeIDsAndIncompleteModels(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root, "https://huggingface.co", http.DefaultClient)
	if _, err := manager.modelDir("../escape"); err == nil {
		t.Fatal("path traversal was accepted")
	}
	if _, err := manager.modelDir("/absolute"); err == nil {
		t.Fatal("absolute model id was accepted")
	}

	bad := filepath.Join(root, "bad")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.List(); err != nil {
		t.Fatalf("manifest decode error should be surfaced: %v", err)
	}
	dir, err := manager.modelDir("bad/model")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove("bad/model"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove missing: %v", err)
	}
	if dir == "" {
		t.Fatal("expected resolved directory")
	}
}

func TestManagerMigratesVerifiedModelCache(t *testing.T) {
	source := t.TempDir()
	modelFile := filepath.Join(source, "example", "model", "model.onnx")
	if err := os.MkdirAll(filepath.Dir(modelFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelFile, []byte("onnx-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"example/model","kind":"embedding","artifacts":["model.onnx"],"status":"ready"}`
	if err := os.WriteFile(filepath.Join(source, "example", "model", "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(source, "https://huggingface.co", http.DefaultClient)

	target := filepath.Join(t.TempDir(), "models")
	plan, err := manager.PlanMigration(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Models) != 1 || plan.Bytes <= 0 || plan.TargetDir != filepath.Clean(target) {
		t.Fatalf("migration plan: %+v", plan)
	}
	if _, err := manager.PlanMigration(filepath.Join(source, "nested")); err == nil {
		t.Fatal("migration into active cache must be rejected")
	}
	if _, err := manager.PlanMigration(filepath.Dir(source)); err == nil {
		t.Fatal("migration to a parent of active cache must be rejected")
	}

	result, err := manager.Migrate(target, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.ModelCount != 1 || result.SourceRemove || manager.Root() != filepath.Clean(target) {
		t.Fatalf("copy migration result: %+v root=%s", result, manager.Root())
	}
	copied, err := manager.List()
	if err != nil || len(copied) != 1 || copied[0].Status != "installed" || copied[0].Lifecycle != LifecycleInstalled || copied[0].Ready {
		t.Fatalf("copied cache: %v %+v", err, copied)
	}
	if _, err := os.Stat(modelFile); err != nil {
		t.Fatalf("copy mode must retain source: %v", err)
	}
	if _, err := manager.Migrate(target, false); err == nil {
		t.Fatal("migrating to the active cache must fail")
	}
}

func TestManagerMigrateRemovesSourceExplicitly(t *testing.T) {
	source := t.TempDir()
	modelDir := filepath.Join(source, "example", "model")
	if err := os.MkdirAll(modelDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"example/model","kind":"embedding","artifacts":["model.onnx"],"status":"ready"}`
	if err := os.WriteFile(filepath.Join(modelDir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(source, "https://huggingface.co", http.DefaultClient)
	target := filepath.Join(t.TempDir(), "models")
	result, err := manager.Migrate(target, true)
	if err != nil || result.ModelCount != 1 || !result.SourceRemove {
		t.Fatalf("move migration: %+v %v", result, err)
	}
	if manager.Root() != filepath.Clean(target) {
		t.Fatalf("active root: %s", manager.Root())
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty source should be removed: %v", err)
	}
}

func TestManagerMigrationCommitFailureRetainsSource(t *testing.T) {
	source := t.TempDir()
	modelDir := filepath.Join(source, "example", "model")
	if err := os.MkdirAll(modelDir, 0o700); err != nil {
		t.Fatal(err)
	}
	modelFile := filepath.Join(modelDir, "model.onnx")
	if err := os.WriteFile(modelFile, []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"example/model","kind":"embedding","artifacts":["model.onnx"],"status":"ready"}`
	if err := os.WriteFile(filepath.Join(modelDir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(source, "https://huggingface.co", http.DefaultClient)
	target := filepath.Join(t.TempDir(), "models")
	commitErr := errors.New("config persistence failed")
	if _, err := manager.MigrateContext(context.Background(), target, true, func() error { return commitErr }); !errors.Is(err, commitErr) {
		t.Fatalf("migration commit error = %v", err)
	}
	if manager.Root() != filepath.Clean(source) {
		t.Fatalf("active root changed after rejected commit: %s", manager.Root())
	}
	if _, err := os.Stat(modelFile); err != nil {
		t.Fatalf("source model was not retained: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uncommitted target remains: %v", err)
	}
}

func TestManagerMigrationHonorsCanceledContext(t *testing.T) {
	source := t.TempDir()
	modelDir := filepath.Join(source, "example", "model")
	if err := os.MkdirAll(modelDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"example/model","kind":"embedding","artifacts":["model.onnx"],"status":"ready"}`
	if err := os.WriteFile(filepath.Join(modelDir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(source, "https://huggingface.co", http.DefaultClient)
	target := filepath.Join(t.TempDir(), "models")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.MigrateContext(ctx, target, false, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled migration error = %v", err)
	}
	if manager.Root() != filepath.Clean(source) {
		t.Fatalf("active root changed after cancellation: %s", manager.Root())
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled target remains: %v", err)
	}
}

func TestManagerPlanMigrationHonorsCanceledContext(t *testing.T) {
	manager := NewManager(t.TempDir(), "https://huggingface.co", http.DefaultClient)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.PlanMigrationContext(ctx, filepath.Join(t.TempDir(), "target")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled migration plan error = %v", err)
	}
}

func TestManagerRemoveHonorsCanceledContext(t *testing.T) {
	source := t.TempDir()
	modelDir := filepath.Join(source, "example", "model")
	if err := os.MkdirAll(filepath.Join(modelDir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	modelFile := filepath.Join(modelDir, "nested", "weights.bin")
	if err := os.WriteFile(modelFile, []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(source, "https://huggingface.co", http.DefaultClient)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.RemoveContext(ctx, "example/model"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled removal error = %v", err)
	}
	if _, err := os.Stat(modelFile); err != nil {
		t.Fatalf("canceled removal changed model tree: %v", err)
	}
	if err := manager.RemoveContext(context.Background(), "example/model"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(modelDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retry did not remove model tree: %v", err)
	}
}

func TestOllamaTagsPullAndDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"nomic-embed-text","size":10}]}`))
		case r.URL.Path == "/api/pull":
			_, _ = w.Write([]byte(`{"status":"pulling","total":100,"completed":50}` + "\n" + `{"status":"success"}` + "\n"))
		case r.URL.Path == "/api/delete":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewOllama(server.URL, server.Client())

	tags, err := client.Tags(context.Background())
	if err != nil || len(tags) != 1 || tags[0].Name != "nomic-embed-text" {
		t.Fatalf("tags: %v %+v", err, tags)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	values := []int{}
	if err := client.Pull(ctx, "nomic-embed-text", func(value int) { values = append(values, value) }); err != nil {
		t.Fatal(err)
	}
	if len(values) == 0 || values[len(values)-1] != 100 {
		t.Fatalf("pull progress: %v", values)
	}
	if err := client.Delete(context.Background(), "nomic-embed-text"); err != nil {
		t.Fatal(err)
	}
}
