package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

const knowledgeDeleteRaceChildEnv = "KNOWLEDGE_DELETE_RACE_CHILD"

type deleteRaceMoveResult struct {
	DocumentID string `json:"documentId"`
	Conflict   bool   `json:"conflict"`
	Error      string `json:"error,omitempty"`
}

type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestCrossProcessDeleteFenceRejectsStaleSnapshot(t *testing.T) {
	if os.Getenv(knowledgeDeleteRaceChildEnv) == "1" {
		if err := runDeleteRaceChild(t); err != nil {
			t.Fatal(err)
		}
		return
	}

	f := newFixture(t)
	base := f.createBase(t)
	root, err := f.service.CreateDirectory(base.ID, "Cross Root", "", "")
	if err != nil {
		t.Fatal(err)
	}
	nested, err := f.service.CreateDirectory(base.ID, "Nested", root.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	injected := f.service.newDocument(base.ID, "Injected After Delete", "file")
	injected.ParentDirectoryID = root.ID
	injected.Status = StatusReady

	workspace := filepath.Join(t.TempDir(), "race")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	injectedPath := filepath.Join(workspace, "injected.json")
	if err := writeJSONFile(injectedPath, injected); err != nil {
		t.Fatal(err)
	}

	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(testBinary, "-test.run=^TestCrossProcessDeleteFenceRejectsStaleSnapshot$")
	var childLog synchronizedBuffer
	child.Stdout = &childLog
	child.Stderr = &childLog
	child.Env = append(os.Environ(),
		knowledgeDeleteRaceChildEnv+"=1",
		"KNOWLEDGE_DELETE_RACE_BASE="+base.ID,
		"KNOWLEDGE_DELETE_RACE_WORKSPACE="+workspace,
	)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if child.Process != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})

	waitForRaceMarker(t, workspace, "snapshot-done", child, childLog.String())
	var snapshot []Document
	if err := readJSONFile(filepath.Join(workspace, "snapshot.json"), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 2 {
		t.Fatalf("cross-process snapshot = %d document(s), want root and nested", len(snapshot))
	}

	// Commit the ancestor tombstone only after the other process has read its
	// scan snapshot. The child's subsequent writes therefore replay the exact
	// lost-race timing rather than simulating it inside one database handle.
	if _, err := f.service.store.markDocumentTreeDeleting(root.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "delete-done"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForRaceMarker(t, workspace, "child-done", child, childLog.String())
	if err := child.Wait(); err != nil {
		t.Fatalf("race child failed: %v: %s", err, childLog.String())
	}

	var results []deleteRaceMoveResult
	if err := readJSONFile(filepath.Join(workspace, "results.json"), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(snapshot)+1 {
		t.Fatalf("race results = %d, want one per stale snapshot plus injection", len(results))
	}
	expected := map[string]bool{root.ID: false, nested.ID: false, injected.ID: false}
	for _, result := range results {
		if _, ok := expected[result.DocumentID]; !ok {
			t.Fatalf("unexpected stale writer %s", result.DocumentID)
		}
		if !result.Conflict {
			t.Fatalf("stale writer %s did not conflict: %+v", result.DocumentID, result)
		}
		expected[result.DocumentID] = true
	}
	for documentID, observed := range expected {
		if !observed {
			t.Fatalf("missing race result for %s", documentID)
		}
	}

	for _, document := range []Document{root, nested} {
		tombstone, err := f.service.store.getDocumentIncludingDeleting(document.ID)
		if err != nil || tombstone.LifecycleState != LifecycleDeleting {
			t.Fatalf("tombstone %s = %+v %v", document.ID, tombstone, err)
		}
		if _, err := f.service.store.getDocument(document.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted document %s remained readable: %v", document.ID, err)
		}
	}
	if _, err := f.service.store.getDocument(injected.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("injected child remained readable: %v", err)
	}
	docs, err := f.service.ListDocuments(base.ID)
	if err != nil || len(docs) != 0 {
		t.Fatalf("visible tree after cross-process race = %v %v", docs, err)
	}
}

func runDeleteRaceChild(t *testing.T) error {
	home := os.Getenv("SHUTU_KNOWLEDGE_HOME")
	workspace := os.Getenv("KNOWLEDGE_DELETE_RACE_WORKSPACE")
	if home == "" || workspace == "" {
		return fmt.Errorf("child missing SHUTU_KNOWLEDGE_HOME or race workspace")
	}
	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	store := newStore(db)

	snapshot, err := store.listDocumentMetadata(baseIDFromEnv(t))
	if err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(workspace, "snapshot.json"), snapshot); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(workspace, "snapshot-done"), []byte("1"), 0o600); err != nil {
		return err
	}
	waitForFile(t, filepath.Join(workspace, "delete-done"))

	var injected Document
	if err := readJSONFile(filepath.Join(workspace, "injected.json"), &injected); err != nil {
		return err
	}
	var results []deleteRaceMoveResult
	for _, stale := range snapshot {
		stale.Title = "Moved " + stale.Title
		stale.SourcePath = filepath.Join(workspace, "moved-"+stale.ID)
		stale.ParentDirectoryID = ""
		err := store.putDocument(stale)
		results = append(results, deleteRaceMoveResult{
			DocumentID: stale.ID, Conflict: errors.Is(err, ErrConflict),
		})
	}
	err = store.putDocument(injected)
	results = append(results, deleteRaceMoveResult{
		DocumentID: injected.ID, Conflict: errors.Is(err, ErrConflict),
	})
	if err := writeJSONFile(filepath.Join(workspace, "results.json"), results); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(workspace, "child-done"), []byte("1"), 0o600); err != nil {
		return err
	}
	return nil
}

func baseIDFromEnv(t *testing.T) string {
	t.Helper()
	baseID := os.Getenv("KNOWLEDGE_DELETE_RACE_BASE")
	if baseID == "" {
		t.Fatal("child missing race base ID")
	}
	return baseID
}

func waitForRaceMarker(t *testing.T, workspace, name string, child *exec.Cmd, log string) {
	t.Helper()
	path := filepath.Join(workspace, name)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if child.ProcessState != nil || time.Now().After(deadline) {
			t.Fatalf("race child did not reach %s: %s", name, log)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("marker %s was not created", path)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func writeJSONFile(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func readJSONFile(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
