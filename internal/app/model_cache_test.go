package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/models"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

func TestCachedManagedModelsRestoreInstalledModelsBeforeRuntimeStarts(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "models")
	if err := os.MkdirAll(filepath.Join(cache, "onnx-community", "Qwen3-Embedding-0.6B-ONNX", "revision"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	state := managedRuntimeState{Components: map[string]managedRuntimeComponent{
		runtime.CapabilityEmbedding: {
			Lifecycle: models.LifecycleInstalled, Model: "onnx-community/Qwen3-Embedding-0.6B-ONNX",
		},
		runtime.CapabilityRerank: {Lifecycle: models.LifecycleNotInstalled},
	}}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "runtime", "runtime-state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	views := cachedManagedModels(home, cache)
	if len(views) != 1 || views[0].ID != "onnx-community/Qwen3-Embedding-0.6B-ONNX" {
		t.Fatalf("cached managed models: %+v", views)
	}
	if views[0].Kind != models.KindEmbedding || views[0].Status != "installed" || views[0].Lifecycle != models.LifecycleInstalled || views[0].Ready {
		t.Fatalf("cached managed model state: %+v", views[0])
	}
}

func TestCachedManagedModelsRejectsUnsafeModelIDs(t *testing.T) {
	if safeModelCachePath(t.TempDir(), "../outside") {
		t.Fatal("unsafe model cache path was accepted")
	}
}
