package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func helperCommand(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(os.Args[0] + " -test.run=TestHelperProcess --")
}

func TestManagerEmbedsReranksExtractsAndProbes(t *testing.T) {
	t.Setenv("SHUTU_RUNTIME_HELPER", "1")
	manager := NewManager(Options{Command: helperCommand(t), StartupTimeout: 2 * time.Second, RequestTimeout: 2 * time.Second})
	defer manager.Close()

	if !manager.Configured(CapabilityEmbedding) || !manager.Configured(CapabilityRerank) || !manager.Configured(CapabilityOCR) {
		t.Fatal("capabilities were not configured")
	}

	var embeddingResult struct {
		Vectors [][]float64 `json:"vectors"`
	}
	if err := manager.Call(context.Background(), CapabilityEmbedding, map[string]any{"texts": []string{"a", "b"}}, &embeddingResult); err != nil {
		t.Fatal(err)
	}
	if len(embeddingResult.Vectors) != 2 || embeddingResult.Vectors[0][0] != 1 {
		t.Fatalf("embedding vectors: %v", embeddingResult.Vectors)
	}

	var rerankResult struct {
		Scores []float64 `json:"scores"`
	}
	if err := manager.Call(context.Background(), CapabilityRerank, map[string]any{"query": "q", "documents": []string{"a"}}, &rerankResult); err != nil {
		t.Fatal(err)
	}
	if len(rerankResult.Scores) != 1 || rerankResult.Scores[0] != 0.75 {
		t.Fatalf("rerank scores: %v", rerankResult.Scores)
	}

	var ocr struct {
		Text string `json:"text"`
	}
	if err := manager.Call(context.Background(), CapabilityOCR, map[string]any{"format": "pdf", "data": "cGRm"}, &ocr); err != nil {
		t.Fatal(err)
	}
	if ocr.Text != "helper text" {
		t.Fatalf("ocr text: %q", ocr.Text)
	}

	health, err := manager.Probe(context.Background(), CapabilityEmbedding)
	if err != nil || !health.Ready || health.Model != "fixture-model" {
		t.Fatalf("probe: %+v %v", health, err)
	}
}

func TestManagerRebuildsCrashedHelper(t *testing.T) {
	t.Setenv("SHUTU_RUNTIME_HELPER", "1")
	manager := NewManager(Options{Command: helperCommand(t), StartupTimeout: 2 * time.Second, RequestTimeout: 2 * time.Second})
	defer manager.Close()

	var embeddingResult struct {
		Vectors [][]float64 `json:"vectors"`
	}
	if err := manager.Call(context.Background(), CapabilityEmbedding, map[string]any{"crash": true}, &embeddingResult); err != nil {
		t.Fatalf("expected crash response before exit: %v", err)
	}
	for manager.HasProcess() {
		time.Sleep(5 * time.Millisecond)
	}
	if err := manager.Call(context.Background(), CapabilityEmbedding, map[string]any{}, &embeddingResult); err != nil {
		t.Fatalf("helper was not rebuilt after crash: %v", err)
	}
}

func TestManagerHonorsCallDeadlineAndClose(t *testing.T) {
	t.Setenv("SHUTU_RUNTIME_HELPER", "1")
	manager := NewManager(Options{Command: helperCommand(t), StartupTimeout: 2 * time.Second, RequestTimeout: 30 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	var scores []float64
	start := time.Now()
	if err := manager.Call(ctx, CapabilityRerank, map[string]any{"sleepMs": 1000}, &scores); err == nil {
		t.Fatal("expected hung helper call to fail")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("call deadline took %s", elapsed)
	}
	manager.Close()
	if err := manager.Call(context.Background(), CapabilityEmbedding, map[string]any{}, &scores); err == nil {
		t.Fatal("closed manager accepted a call")
	}
}

func TestManagerUsesModelLoadBudgetOnlyForFirstModelInference(t *testing.T) {
	t.Setenv("SHUTU_RUNTIME_HELPER", "1")
	manager := NewManager(Options{
		Command: helperCommand(t), StartupTimeout: 2 * time.Second,
		RequestTimeout: 30 * time.Millisecond, ModelLoadTimeout: 250 * time.Millisecond,
	})
	defer manager.Close()
	var result struct {
		Vectors [][]float64 `json:"vectors"`
	}
	if err := manager.Call(context.Background(), CapabilityEmbedding, map[string]any{"sleepMs": 100}, &result); err != nil {
		t.Fatalf("first model inference should use the load budget: %v", err)
	}
	if err := manager.Call(context.Background(), CapabilityEmbedding, map[string]any{"sleepMs": 100}, &result); err == nil {
		t.Fatal("warmed model inference unexpectedly bypassed the ordinary request timeout")
	}
}

func TestManagerIdleLifecycle(t *testing.T) {
	t.Setenv("SHUTU_RUNTIME_HELPER", "1")
	manager := NewManager(Options{Command: helperCommand(t), StartupTimeout: 2 * time.Second, RequestTimeout: 2 * time.Second, IdleTimeout: 20 * time.Millisecond})
	defer manager.Close()
	var embeddingResult struct {
		Vectors [][]float64 `json:"vectors"`
	}
	if err := manager.Call(context.Background(), CapabilityEmbedding, map[string]any{}, &embeddingResult); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for manager.HasProcess() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if manager.HasProcess() {
		t.Fatal("idle helper was not stopped")
	}
	if err := manager.Call(context.Background(), CapabilityEmbedding, map[string]any{}, &embeddingResult); err != nil {
		t.Fatalf("idle helper was not rebuilt: %v", err)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("SHUTU_RUNTIME_HELPER") != "1" {
		return
	}
	if err := runHelper(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runHelper(stdin *os.File, stdout *os.File) error {
	reader := bufio.NewScanner(stdin)
	writer := bufio.NewWriter(stdout)
	id := uint64(0)
	reply := func(result any) error {
		data, err := json.Marshal(map[string]any{"id": id, "result": result})
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return err
		}
		return writer.Flush()
	}
	errorReply := func(code, message string) error {
		data, _ := json.Marshal(map[string]any{"id": id, "error": map[string]string{"code": code, "message": message}})
		if _, err := writer.Write(append(data, '\n')); err != nil {
			return err
		}
		return writer.Flush()
	}
	for reader.Scan() {
		var req request
		if err := json.Unmarshal(reader.Bytes(), &req); err != nil {
			return err
		}
		id = req.ID
		switch req.Method {
		case "initialize":
			if err := reply(initializeResult{Protocol: ProtocolVersion, Version: "test-helper", Capabilities: []string{CapabilityEmbedding, CapabilityRerank, CapabilityOCR}}); err != nil {
				return err
			}
		case CapabilityEmbedding:
			var body struct {
				Crash   bool `json:"crash"`
				SleepMS int  `json:"sleepMs"`
			}
			if err := json.Unmarshal(req.Params, &body); err != nil {
				return err
			}
			if body.Crash {
				if err := reply(map[string]any{"vectors": [][]float64{{1}}}); err != nil {
					return err
				}
				os.Exit(9)
			}
			if body.SleepMS > 0 {
				time.Sleep(time.Duration(body.SleepMS) * time.Millisecond)
			}
			if err := reply(map[string]any{"vectors": [][]float64{{1}, {2}}}); err != nil {
				return err
			}
		case CapabilityRerank:
			var body struct {
				SleepMS int `json:"sleepMs"`
			}
			if err := json.Unmarshal(req.Params, &body); err != nil {
				return err
			}
			if body.SleepMS > 0 {
				time.Sleep(time.Duration(body.SleepMS) * time.Millisecond)
			}
			if err := reply(map[string]any{"scores": []float64{0.75}}); err != nil {
				return err
			}
		case CapabilityOCR:
			if err := reply(map[string]any{"text": "helper text"}); err != nil {
				return err
			}
		case "health":
			if err := reply(Health{Ready: true, Model: "fixture-model"}); err != nil {
				return err
			}
		default:
			if err := errorReply("method_not_found", "unsupported method "+req.Method); err != nil {
				return err
			}
		}
	}
	return reader.Err()
}

func TestCommandRequiresCapability(t *testing.T) {
	t.Setenv("SHUTU_KNOWLEDGE_EMBEDDING_HELPER", "")
	manager := NewManager(Options{})
	defer manager.Close()
	err := manager.Call(context.Background(), CapabilityEmbedding, map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unconfigured call: %v", err)
	}
}

func TestWireErrorFormatting(t *testing.T) {
	if got := (&Error{Code: "boom", Message: "bad"}).Error(); got != "boom: bad" {
		t.Fatalf("error: %q", got)
	}
	if got := strconv.Itoa(ProtocolVersion); got != "1" {
		t.Fatalf("protocol: %s", got)
	}
}
