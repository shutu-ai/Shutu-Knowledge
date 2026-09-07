// Command runtime_smoke exercises the real Knowledge-managed runtime through
// the same Go supervisor used by the application. It is intentionally a
// command, rather than an unconditional unit test, because model downloads
// are large and require an operator-selected cache.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/app"
	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/parser"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
)

func main() {
	home := flag.String("home", filepath.Join(os.TempDir(), "shutu-runtime-smoke"), "managed runtime home")
	cache := flag.String("model-cache", "", "existing Transformers.js model cache")
	ocrImage := flag.String("ocr-image", "", "PNG/JPEG containing readable text")
	doc := flag.String("doc", "", "real .doc fixture")
	ppt := flag.String("ppt", "", "real .ppt fixture")
	xls := flag.String("xls", "", "real .xls fixture")
	codecPDF := flag.String("codec-pdf", "", "real PDF fixture containing JBIG2 or JPX")
	corruptionRecovery := flag.Bool("corruption-recovery", false, "verify model corruption fails and a restored model reloads")
	lifecycle := flag.Bool("lifecycle", false, "verify model removal, cache invalidation, and real reinstall")
	offlineRestart := flag.Bool("offline-restart", false, "restart the managed process with remote model/OCR access disabled")
	knowledgeE2E := flag.Bool("knowledge-e2e", false, "run fresh application import, vector retrieval, OCR, and offline restart")
	flag.Parse()

	ctx := context.Background()
	command, err := runtime.PrepareManagedRuntime(ctx, *home, *cache)
	fatal(err)
	if *offlineRestart {
		command += " --offline"
	}
	manager := runtime.NewManager(runtime.Options{
		Command: command, StartupTimeout: 5 * time.Minute, RequestTimeout: 5 * time.Minute,
		ModelLoadTimeout: 30 * time.Minute,
	})
	defer func() { manager.Close() }()

	embeddingStarted := time.Now()
	embedding, err := manager.LoadModel(ctx, runtime.CapabilityEmbedding, "")
	fatal(err)
	embeddingLoadMS := time.Since(embeddingStarted).Milliseconds()
	var vectors struct {
		Vectors [][]float64 `json:"vectors"`
	}
	fatal(manager.Call(ctx, runtime.CapabilityEmbedding, map[string]any{
		"texts": []string{"知识库 runtime smoke", "Knowledge runtime smoke"},
	}, &vectors))
	if len(vectors.Vectors) != 2 || len(vectors.Vectors[0]) == 0 {
		fatal(fmt.Errorf("invalid embedding result: %+v", vectors))
	}
	fmt.Printf("embedding: ready=%t model=%s dimension=%d load-ms=%d\n", embedding.Ready, embedding.Model, len(vectors.Vectors[0]), embeddingLoadMS)
	if err := checkEmbeddingBehavior(ctx, manager); err != nil {
		fatal(err)
	}

	rerankLoadStarted := time.Now()
	reranker, err := manager.LoadModel(ctx, runtime.CapabilityRerank, "")
	fatal(err)
	rerankLoadMS := time.Since(rerankLoadStarted).Milliseconds()
	var scores struct {
		Scores []float64 `json:"scores"`
	}
	rerankStarted := time.Now()
	fatal(manager.Call(ctx, runtime.CapabilityRerank, map[string]any{
		"query": "runtime smoke", "documents": []string{"runtime smoke", "unrelated text", "another unrelated gardening note"},
	}, &scores))
	rerankMS := time.Since(rerankStarted).Milliseconds()
	if len(scores.Scores) != 3 || scores.Scores[0] <= scores.Scores[1] || scores.Scores[0] <= scores.Scores[2] {
		fatal(fmt.Errorf("reranker did not order evidence: %+v", scores.Scores))
	}
	fmt.Printf("rerank: ready=%t scores=%v load-ms=%d infer-ms=%d\n", reranker.Ready, scores.Scores, rerankLoadMS, rerankMS)
	if *lifecycle {
		if err := checkModelLifecycle(ctx, manager, *cache); err != nil {
			fatal(err)
		}
	}
	if *corruptionRecovery {
		if *cache == "" {
			fatal(fmt.Errorf("-corruption-recovery requires -model-cache"))
		}
		manager.Close()
		modelPath := filepath.Join(*cache, "onnx-community", "Qwen3-Embedding-0.6B-ONNX", "c25a394dd583836952667c12f008335071b3f43d", "onnx", "model_q4.onnx")
		file, openErr := os.OpenFile(modelPath, os.O_RDWR, 0)
		fatal(openErr)
		original := []byte{0}
		_, readErr := file.ReadAt(original, 0)
		fatal(readErr)
		_, writeErr := file.WriteAt([]byte{original[0] ^ 0xff}, 0)
		fatal(writeErr)
		fatal(file.Close())
		failedManager := runtime.NewManager(runtime.Options{Command: command, StartupTimeout: 5 * time.Minute, RequestTimeout: 5 * time.Minute, ModelLoadTimeout: 30 * time.Minute})
		if _, loadErr := failedManager.LoadModel(ctx, runtime.CapabilityEmbedding, ""); loadErr == nil {
			failedManager.Close()
			fatal(fmt.Errorf("corrupted model unexpectedly loaded"))
		}
		corruptHealth, probeErr := failedManager.Probe(ctx, runtime.CapabilityEmbedding)
		if probeErr == nil || corruptHealth.Lifecycle != "FAILED" || corruptHealth.LastError == "" || corruptHealth.Remediation == "" {
			failedManager.Close()
			fatal(fmt.Errorf("corrupted model did not expose actionable FAILED health: health=%+v probe=%v", corruptHealth, probeErr))
		}
		failedManager.Close()
		file, openErr = os.OpenFile(modelPath, os.O_RDWR, 0)
		fatal(openErr)
		_, writeErr = file.WriteAt(original, 0)
		fatal(writeErr)
		fatal(file.Close())
		manager = runtime.NewManager(runtime.Options{Command: command, StartupTimeout: 5 * time.Minute, RequestTimeout: 5 * time.Minute, ModelLoadTimeout: 30 * time.Minute})
		if _, loadErr := manager.LoadModel(ctx, runtime.CapabilityEmbedding, ""); loadErr != nil {
			fatal(fmt.Errorf("restored model did not reload: %w", loadErr))
		}
		fmt.Println("corruption recovery: failed-on-corrupt, Doctor-health=FAILED, ready-after-restore")
	}

	var pages struct {
		Pages []struct {
			Page int    `json:"page"`
			PNG  string `json:"png"`
		} `json:"pages"`
	}
	pdfStarted := time.Now()
	fatal(manager.Call(ctx, runtime.CapabilityPDFRender, map[string]any{
		"format": "pdf", "data": base64.StdEncoding.EncodeToString(smokePDF()),
	}, &pages))
	pdfMS := time.Since(pdfStarted).Milliseconds()
	if _, err := parser.ParseRenderedPDFPages(mustJSON(pages)); err != nil {
		fatal(err)
	}
	fmt.Printf("pdf: pages=%d render-ms=%d\n", len(pages.Pages), pdfMS)
	var malformedPages struct {
		Pages []struct {
			Page int    `json:"page"`
			PNG  string `json:"png"`
		} `json:"pages"`
	}
	if err := manager.Call(ctx, runtime.CapabilityPDFRender, map[string]any{
		"format": "pdf", "data": base64.StdEncoding.EncodeToString([]byte("not a PDF")),
	}, &malformedPages); err == nil || !strings.Contains(strings.ToLower(err.Error()), "pdf") {
		fatal(fmt.Errorf("malformed PDF did not produce actionable renderer error: %v", err))
	}
	fmt.Println("pdf malformed: failure surfaced with component error")
	if *codecPDF != "" {
		data, readErr := os.ReadFile(*codecPDF)
		fatal(readErr)
		var codecPages struct {
			Pages []struct {
				Page int    `json:"page"`
				PNG  string `json:"png"`
			} `json:"pages"`
		}
		fatal(manager.Call(ctx, runtime.CapabilityPDFRender, map[string]any{
			"format": "pdf", "data": base64.StdEncoding.EncodeToString(data),
		}, &codecPages))
		if _, err := parser.ParseRenderedPDFPages(mustJSON(codecPages)); err != nil {
			fatal(err)
		}
		fmt.Printf("codec pdf: %s pages=%d\n", filepath.Base(*codecPDF), len(codecPages.Pages))
	}

	{
		var data []byte
		if *ocrImage != "" {
			readData, readErr := os.ReadFile(*ocrImage)
			fatal(readErr)
			data = readData
		} else {
			data = blankPNG()
		}
		var result struct {
			Text string `json:"text"`
		}
		ocrStarted := time.Now()
		fatal(manager.Call(ctx, runtime.CapabilityOCR, map[string]any{
			"format": "png", "data": base64.StdEncoding.EncodeToString(data),
		}, &result))
		ocrMS := time.Since(ocrStarted).Milliseconds()
		if *ocrImage != "" && result.Text == "" {
			fatal(fmt.Errorf("OCR returned empty text"))
		}
		fmt.Printf("ocr: image=%t text=%q infer-ms=%d\n", *ocrImage != "", result.Text, ocrMS)
		if *ocrImage != "" {
			fatal(checkOCRVariants(ctx, manager, data))
		}
	}

	for _, fixture := range []struct {
		format string
		path   string
	}{
		{"doc", *doc}, {"ppt", *ppt}, {"xls", *xls},
	} {
		if fixture.path == "" {
			continue
		}
		data, readErr := os.ReadFile(fixture.path)
		fatal(readErr)
		var result struct {
			Text string `json:"text"`
		}
		officeStarted := time.Now()
		fatal(manager.Call(ctx, runtime.CapabilityOffice, map[string]any{
			"format": fixture.format, "data": base64.StdEncoding.EncodeToString(data),
		}, &result))
		officeMS := time.Since(officeStarted).Milliseconds()
		if result.Text == "" {
			fatal(fmt.Errorf("%s conversion returned empty text", fixture.format))
		}
		fmt.Printf("office %s: non-empty markdown bytes=%d convert-ms=%d\n", fixture.format, len(result.Text), officeMS)
	}

	if *knowledgeE2E {
		if *ocrImage == "" {
			fatal(fmt.Errorf("-knowledge-e2e requires -ocr-image so the scanned-PDF path is real"))
		}
		fatal(runKnowledgeE2E(ctx, *home, *cache, *ocrImage, *doc, *ppt, *xls))
	}
}

func checkModelLifecycle(ctx context.Context, manager *runtime.Manager, modelCache string) error {
	if strings.TrimSpace(modelCache) == "" {
		return fmt.Errorf("-lifecycle requires -model-cache so removal is isolated")
	}
	checks := []struct {
		capability string
		id         string
		relative   string
	}{
		{runtime.CapabilityEmbedding, "onnx-community/Qwen3-Embedding-0.6B-ONNX", filepath.Join("onnx-community", "Qwen3-Embedding-0.6B-ONNX")},
		{runtime.CapabilityRerank, "Xenova/bge-reranker-base", filepath.Join("Xenova", "bge-reranker-base")},
	}
	for _, check := range checks {
		if err := manager.RemoveModel(ctx, check.capability, ""); err != nil {
			return fmt.Errorf("remove %s model: %w", check.capability, err)
		}
		if _, err := os.Stat(filepath.Join(modelCache, check.relative)); !os.IsNotExist(err) {
			return fmt.Errorf("remove %s model left cache at %s", check.capability, filepath.Join(modelCache, check.relative))
		}
		health, probeErr := manager.Probe(ctx, check.capability)
		if probeErr == nil || health.Ready || health.Lifecycle == "READY" {
			return fmt.Errorf("removed %s model still probes ready: %+v", check.capability, health)
		}
		if _, err := manager.LoadModel(ctx, check.capability, ""); err != nil {
			return fmt.Errorf("reinstall %s model after removal: %w", check.capability, err)
		}
		if _, err := os.Stat(filepath.Join(modelCache, check.relative)); err != nil {
			return fmt.Errorf("reinstall %s model did not repopulate cache: %w", check.capability, err)
		}
	}
	fmt.Println("model lifecycle: removed, cache-invalidated, reinstalled, and re-smoked embedding+rerank")
	return nil
}

func checkEmbeddingBehavior(ctx context.Context, manager *runtime.Manager) error {
	texts := []string{
		"中文知识库检索与文档索引",
		"English knowledge retrieval and document indexing",
		"中英文 mixed runtime pipeline",
		strings.Repeat("long input for embedding stability ", 160),
		"",
		"   \t  ",
	}
	var payload struct {
		Vectors [][]float64 `json:"vectors"`
	}
	started := time.Now()
	if err := manager.Call(ctx, runtime.CapabilityEmbedding, map[string]any{"texts": texts}, &payload); err != nil {
		return fmt.Errorf("embedding behavior batch: %w", err)
	}
	if len(payload.Vectors) != len(texts) {
		return fmt.Errorf("embedding behavior returned %d vectors for %d inputs", len(payload.Vectors), len(texts))
	}
	dimension := 0
	for index, vector := range payload.Vectors {
		if dimension == 0 {
			dimension = len(vector)
		}
		if len(vector) != dimension || dimension == 0 {
			return fmt.Errorf("embedding behavior dimension mismatch at %d: %d vs %d", index, len(vector), dimension)
		}
		var norm float64
		for _, value := range vector {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("embedding behavior returned non-finite value at %d", index)
			}
			norm += value * value
		}
		if norm == 0 {
			return fmt.Errorf("embedding behavior returned a zero vector at %d", index)
		}
	}
	var similarity struct {
		Vectors [][]float64 `json:"vectors"`
	}
	if err := manager.Call(ctx, runtime.CapabilityEmbedding, map[string]any{"texts": []string{
		"The reimbursement policy requires an invoice and manager approval.",
		"The reimbursement policy requires an invoice and manager approval.",
		"Basil plants need morning water and indirect sunlight.",
	}}, &similarity); err != nil {
		return fmt.Errorf("embedding similarity probe: %w", err)
	}
	if len(similarity.Vectors) != 3 {
		return fmt.Errorf("embedding similarity returned %d vectors", len(similarity.Vectors))
	}
	same := cosine(similarity.Vectors[0], similarity.Vectors[1])
	unrelated := cosine(similarity.Vectors[0], similarity.Vectors[2])
	if !isFinite(same) || !isFinite(unrelated) || same <= unrelated {
		return fmt.Errorf("embedding similarity ordering failed: same=%f unrelated=%f", same, unrelated)
	}
	fmt.Printf("embedding behavior: cases=%d dimension=%d same-cosine=%.4f unrelated-cosine=%.4f batch-ms=%d\n", len(texts), dimension, same, unrelated, time.Since(started).Milliseconds())
	return nil
}

func cosine(left, right []float64) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return math.NaN()
	}
	var dot, leftNorm, rightNorm float64
	for index := range left {
		dot += left[index] * right[index]
		leftNorm += left[index] * left[index]
		rightNorm += right[index] * right[index]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return math.NaN()
	}
	return dot / math.Sqrt(leftNorm*rightNorm)
}

func isFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func checkOCRVariants(ctx context.Context, manager *runtime.Manager, source []byte) error {
	variants := []struct {
		name string
		data []byte
	}{
		{"rotated", mustImageVariant(rotatedPNG, source)},
		{"low-quality", mustImageVariant(lowQualityPNG, source)},
	}
	for _, variant := range variants {
		var result struct {
			Text string `json:"text"`
		}
		started := time.Now()
		if err := manager.Call(ctx, runtime.CapabilityOCR, map[string]any{
			"format": "png", "data": base64.StdEncoding.EncodeToString(variant.data),
		}, &result); err != nil {
			return fmt.Errorf("OCR %s fixture: %w", variant.name, err)
		}
		if strings.TrimSpace(result.Text) == "" {
			return fmt.Errorf("OCR %s fixture returned empty text", variant.name)
		}
		fmt.Printf("ocr %s: raw-text=%q infer-ms=%d\n", variant.name, result.Text, time.Since(started).Milliseconds())
	}
	multi, err := multiImagePDF(source)
	if err != nil {
		return fmt.Errorf("multi-page OCR fixture: %w", err)
	}
	var rendered struct {
		Pages []struct {
			Page int    `json:"page"`
			PNG  string `json:"png"`
		} `json:"pages"`
	}
	if err := manager.Call(ctx, runtime.CapabilityPDFRender, map[string]any{
		"format": "pdf", "data": base64.StdEncoding.EncodeToString(multi),
	}, &rendered); err != nil {
		return fmt.Errorf("multi-page PDF render: %w", err)
	}
	if len(rendered.Pages) != 2 || rendered.Pages[0].Page != 1 || rendered.Pages[1].Page != 2 {
		return fmt.Errorf("multi-page PDF metadata was not retained: %+v", rendered.Pages)
	}
	for _, page := range rendered.Pages {
		var result struct {
			Text string `json:"text"`
		}
		if err := manager.Call(ctx, runtime.CapabilityOCR, map[string]any{
			"format": "png", "data": page.PNG,
		}, &result); err != nil {
			return fmt.Errorf("OCR multi-page page %d: %w", page.Page, err)
		}
		if !strings.Contains(strings.ToLower(result.Text), "knowledge") {
			return fmt.Errorf("OCR multi-page page %d did not recover expected text: %q", page.Page, result.Text)
		}
	}
	fmt.Printf("ocr multi-page: pages=%d page-metadata=1,2 text-recovered=true\n", len(rendered.Pages))
	return nil
}

func mustImageVariant(makeVariant func([]byte) ([]byte, error), source []byte) []byte {
	data, err := makeVariant(source)
	fatal(err)
	return data
}

func rotatedPNG(data []byte) ([]byte, error) {
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	rotated := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			rotated.Set(x, y, source.At(bounds.Min.X+bounds.Dx()-1-x, bounds.Min.Y+bounds.Dy()-1-y))
		}
	}
	return encodePNG(rotated)
}

func lowQualityPNG(data []byte) ([]byte, error) {
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	width, height := max(1, bounds.Dx()/2), max(1, bounds.Dy()/2)
	gray := image.NewGray(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			sourceX := bounds.Min.X + x*bounds.Dx()/width
			sourceY := bounds.Min.Y + y*bounds.Dy()/height
			gray.SetGray(x, y, color.GrayModel.Convert(source.At(sourceX, sourceY)).(color.Gray))
		}
	}
	return encodePNG(gray)
}

func encodePNG(source image.Image) ([]byte, error) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func runKnowledgeE2E(ctx context.Context, home, modelCache, imagePath, docPath, pptPath, xlsPath string) error {
	if strings.TrimSpace(modelCache) == "" {
		modelCache = filepath.Join(home, "models")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	for _, name := range []string{
		"SHUTU_KNOWLEDGE_OFFLINE", "SHUTU_KNOWLEDGE_DISABLE_MANAGED_RUNTIME",
		"SHUTU_KNOWLEDGE_RUNTIME_HELPER", "SHUTU_KNOWLEDGE_EMBEDDING_HELPER",
		"SHUTU_KNOWLEDGE_RERANK_HELPER", "SHUTU_KNOWLEDGE_OCR_HELPER",
	} {
		_ = os.Unsetenv(name)
	}
	_ = os.Setenv("SHUTU_KNOWLEDGE_HOME", home)
	cfg := config.Defaults()
	cfg.Embedding.Provider = "local"
	cfg.Embedding.Model = "onnx-community/Qwen3-Embedding-0.6B-ONNX"
	cfg.Embedding.Batch = 4
	cfg.Rerank.Enabled = true
	cfg.Rerank.Model = "local:Xenova/bge-reranker-base"
	cfg.Retrieval.Mode = "vector"
	cfg.Retrieval.TopK = 3
	cfg.Models.CacheDir = modelCache
	cfg.OCR.Mode = "auto"
	if err := config.Save(filepath.Join(home, "config.yaml"), cfg); err != nil {
		return err
	}

	application, err := app.New(ctx)
	if err != nil {
		return fmt.Errorf("fresh application start: %w", err)
	}
	closeApplication := true
	defer func() {
		if closeApplication {
			application.Close()
		}
	}()

	base, err := application.Knowledge.CreateBase("runtime-e2e", "real runtime validation", "runtime", knowledge.BaseConfig{})
	if err != nil {
		return fmt.Errorf("create e2e base: %w", err)
	}
	if _, err := application.Knowledge.AddFileDocument(ctx, base.ID, "broken-runtime.png", []byte("not an image"), ""); err == nil {
		return fmt.Errorf("malformed image unexpectedly imported successfully")
	}
	if _, err := application.Knowledge.AddFileDocument(ctx, base.ID, "broken-runtime.pdf", []byte("not a PDF"), ""); err == nil {
		return fmt.Errorf("malformed PDF unexpectedly imported successfully")
	}
	recoveryDoc, err := application.Knowledge.AddTextDocument(ctx, base.ID, "after-failure.md", "Import continues after a malformed image and this document remains searchable.")
	if err != nil || recoveryDoc.Status != knowledge.StatusReady {
		return fmt.Errorf("document failure was not isolated: status=%s err=%v", recoveryDoc.Status, err)
	}
	items := []knowledge.AddFilesItem{
		{FileName: "expense-reimbursement.md", ContentBase64: base64.StdEncoding.EncodeToString([]byte("# Expense reimbursement\nAn expense report requires the original invoice and manager approval before reimbursement."))},
		{FileName: "travel-policy.md", ContentBase64: base64.StdEncoding.EncodeToString([]byte("# Travel policy\nBook standard rail travel through the company portal and retain the itinerary."))},
		{FileName: "garden-notes.md", ContentBase64: base64.StdEncoding.EncodeToString([]byte("# Garden notes\nWater the basil in the morning and move seedlings into indirect light."))},
	}
	batch, err := application.Knowledge.AddFiles(ctx, base.ID, items, "rename", "")
	if err != nil {
		return fmt.Errorf("import semantic documents: %w", err)
	}
	if len(batch.Accepted) != len(items) {
		return fmt.Errorf("import accepted %d/%d documents", len(batch.Accepted), len(items))
	}
	for _, accepted := range batch.Accepted {
		doc, _, getErr := application.Knowledge.GetDocument(accepted.ID, true)
		if getErr != nil {
			return getErr
		}
		if doc.Status != knowledge.StatusReady || doc.ChunkCount == 0 {
			return fmt.Errorf("document %s did not reach ready after embedding: status=%s chunks=%d error=%s", doc.Title, doc.Status, doc.ChunkCount, doc.ErrorMessage)
		}
	}
	stats, err := application.Knowledge.Stats(base.ID)
	if err != nil || !stats.Embedded || stats.Dimensions != 1024 {
		return fmt.Errorf("vector index is not ready: stats=%+v err=%v", stats, err)
	}
	semantic, err := application.Knowledge.Search(ctx, knowledge.SearchRequest{
		Query:   "What invoice and approval are needed for expense reimbursement?",
		Mode:    "vector",
		TopK:    3,
		BaseIDs: []string{base.ID},
	})
	if err != nil {
		return fmt.Errorf("semantic retrieval: %w", err)
	}
	if len(semantic.Hits) == 0 || semantic.Hits[0].DocumentTitle != "expense-reimbursement.md" {
		return fmt.Errorf("semantic retrieval returned wrong top result: %+v", semantic.Hits)
	}
	if semantic.Rerank == nil || semantic.Rerank.Status != "applied" || !semantic.Reranked {
		return fmt.Errorf("local reranker was not applied in retrieval: %+v", semantic.Rerank)
	}

	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return fmt.Errorf("read OCR image: %w", err)
	}
	imageDoc, err := application.Knowledge.AddFileDocument(ctx, base.ID, "scanned-runtime.png", imageData, "")
	if err != nil {
		return fmt.Errorf("import raster image: %w", err)
	}
	if imageDoc.Status != knowledge.StatusReady || !strings.Contains(strings.ToLower(imageDoc.RawText), "knowledge") {
		return fmt.Errorf("raster image did not complete OCR: status=%s text=%q error=%s", imageDoc.Status, imageDoc.RawText, imageDoc.ErrorMessage)
	}
	scannedPDF, err := imagePDF(imageData)
	if err != nil {
		return fmt.Errorf("create scanned PDF fixture: %w", err)
	}
	scanned, err := application.Knowledge.AddFileDocument(ctx, base.ID, "scanned-runtime.pdf", scannedPDF, "")
	if err != nil {
		return fmt.Errorf("import scanned PDF: %w", err)
	}
	if scanned.Status != knowledge.StatusReady || !strings.Contains(strings.ToLower(scanned.RawText), "knowledge") {
		return fmt.Errorf("scanned PDF did not complete OCR: status=%s text=%q error=%s", scanned.Status, scanned.RawText, scanned.ErrorMessage)
	}
	rotatedImage, err := rotatedPNG(imageData)
	if err != nil {
		return fmt.Errorf("create rotated OCR fixture: %w", err)
	}
	rotatedDoc, err := application.Knowledge.AddFileDocument(ctx, base.ID, "scanned-runtime-rotated.png", rotatedImage, "")
	if err != nil || rotatedDoc.Status != knowledge.StatusReady || !strings.Contains(strings.ToLower(rotatedDoc.RawText), "knowledge") {
		return fmt.Errorf("rotated image did not complete OCR: status=%s text=%q error=%s import=%v", rotatedDoc.Status, rotatedDoc.RawText, rotatedDoc.ErrorMessage, err)
	}
	lowQualityImage, err := lowQualityPNG(imageData)
	if err != nil {
		return fmt.Errorf("create low-quality OCR fixture: %w", err)
	}
	lowQualityDoc, err := application.Knowledge.AddFileDocument(ctx, base.ID, "scanned-runtime-low-quality.png", lowQualityImage, "")
	if err != nil || lowQualityDoc.Status != knowledge.StatusReady || !strings.Contains(strings.ToLower(lowQualityDoc.RawText), "knowledge") {
		return fmt.Errorf("low-quality image did not complete OCR: status=%s text=%q error=%s import=%v", lowQualityDoc.Status, lowQualityDoc.RawText, lowQualityDoc.ErrorMessage, err)
	}
	multiPDF, err := multiImagePDF(imageData)
	if err != nil {
		return fmt.Errorf("create multi-page OCR fixture: %w", err)
	}
	multiDoc, err := application.Knowledge.AddFileDocument(ctx, base.ID, "scanned-runtime-multi.pdf", multiPDF, "")
	if err != nil || multiDoc.Status != knowledge.StatusReady || !strings.Contains(strings.ToLower(multiDoc.RawText), "knowledge") {
		return fmt.Errorf("multi-page PDF did not complete OCR: status=%s text=%q error=%s import=%v", multiDoc.Status, multiDoc.RawText, multiDoc.ErrorMessage, err)
	}
	ocrSearch, err := application.Knowledge.Search(ctx, knowledge.SearchRequest{
		Query: "Knowledge Runtime OCR 7788", Mode: "vector", TopK: 10, BaseIDs: []string{base.ID},
	})
	if err != nil {
		return fmt.Errorf("OCR retrieval: %w", err)
	}
	foundOCR := false
	foundMulti := false
	for _, hit := range ocrSearch.Hits {
		if hit.DocID == scanned.ID {
			foundOCR = true
		}
		if hit.DocID == multiDoc.ID {
			foundMulti = true
		}
	}
	if !foundOCR {
		return fmt.Errorf("OCR document was not retrieved: %+v", ocrSearch.Hits)
	}
	if !foundMulti {
		return fmt.Errorf("multi-page OCR document was not retrieved: %+v", ocrSearch.Hits)
	}
	officeCount := 0
	for _, fixture := range []struct {
		format string
		path   string
	}{
		{"doc", docPath}, {"ppt", pptPath}, {"xls", xlsPath},
	} {
		if strings.TrimSpace(fixture.path) == "" {
			continue
		}
		data, readErr := os.ReadFile(fixture.path)
		if readErr != nil {
			return fmt.Errorf("read office %s fixture: %w", fixture.format, readErr)
		}
		officeDoc, importErr := application.Knowledge.AddFileDocument(ctx, base.ID, filepath.Base(fixture.path), data, "")
		if importErr != nil {
			return fmt.Errorf("import office %s: %w", fixture.format, importErr)
		}
		if officeDoc.Status != knowledge.StatusReady || officeDoc.ChunkCount == 0 || strings.TrimSpace(officeDoc.RawText) == "" {
			return fmt.Errorf("office %s did not complete parse/index: status=%s chunks=%d text=%q error=%s", fixture.format, officeDoc.Status, officeDoc.ChunkCount, officeDoc.RawText, officeDoc.ErrorMessage)
		}
		words := strings.Fields(officeDoc.RawText)
		if len(words) > 8 {
			words = words[:8]
		}
		officeSearch, searchErr := application.Knowledge.Search(ctx, knowledge.SearchRequest{
			Query: strings.Join(words, " "), Mode: "vector", TopK: 10, BaseIDs: []string{base.ID},
		})
		if searchErr != nil {
			return fmt.Errorf("office %s retrieval: %w", fixture.format, searchErr)
		}
		found := false
		for _, hit := range officeSearch.Hits {
			if hit.DocID == officeDoc.ID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("office %s was not retrieved from vector index: %+v", fixture.format, officeSearch.Hits)
		}
		officeCount++
	}
	if strings.TrimSpace(docPath) != "" {
		if _, err := application.Knowledge.AddFileDocument(ctx, base.ID, "broken-runtime.doc", []byte("not an OLE document"), ""); err == nil {
			return fmt.Errorf("malformed Office document unexpectedly imported successfully")
		}
	}
	fmt.Printf("knowledge e2e: documents=%d vector-dimension=%d semantic-top=%s rerank=%s OCR-PDF=%s OCR-image=%s OCR-rotated=%s OCR-low-quality=%s OCR-multi=%s office=%d failure-isolated=true\n", len(batch.Accepted)+6+officeCount, stats.Dimensions, semantic.Hits[0].DocumentTitle, semantic.Rerank.Status, scanned.ID, imageDoc.ID, rotatedDoc.ID, lowQualityDoc.ID, multiDoc.ID, officeCount)

	application.Close()
	closeApplication = false
	_ = os.Setenv("SHUTU_KNOWLEDGE_OFFLINE", "1")
	cfg.Runtime.Offline = true
	if err := config.Save(filepath.Join(home, "config.yaml"), cfg); err != nil {
		return err
	}
	offline, err := app.New(ctx)
	if err != nil {
		return fmt.Errorf("offline application restart: %w", err)
	}
	defer offline.Close()
	offlineSearch, err := offline.Knowledge.Search(ctx, knowledge.SearchRequest{
		Query: "What invoice and approval are needed for expense reimbursement?", Mode: "vector", TopK: 3, BaseIDs: []string{base.ID},
	})
	if err != nil {
		return fmt.Errorf("offline semantic retrieval: %w", err)
	}
	if len(offlineSearch.Hits) == 0 || offlineSearch.Hits[0].DocumentTitle != "expense-reimbursement.md" {
		return fmt.Errorf("offline retrieval returned wrong result: %+v", offlineSearch.Hits)
	}
	fmt.Println("offline restart: embedding, reranker, vector retrieval passed")
	return nil
}

func imagePDF(data []byte) ([]byte, error) {
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(rgba, rgba.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), source, bounds.Min, draw.Over)
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, rgba, &jpeg.Options{Quality: 95}); err != nil {
		return nil, err
	}
	return jpegPDF(jpegData.Bytes(), bounds.Dx(), bounds.Dy()), nil
}

func jpegPDF(jpegData []byte, width, height int) []byte {
	objects := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
		fmt.Sprintf("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>\nendobj\n", width, height),
		fmt.Sprintf("4 0 obj\n<< /Length %d >>\nstream\nq\n%d 0 0 %d 0 0 cm\n/Im0 Do\nQ\nendstream\nendobj\n", len(fmt.Sprintf("q\n%d 0 0 %d 0 0 cm\n/Im0 Do\nQ\n", width, height)), width, height),
	}
	var body bytes.Buffer
	body.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for _, object := range objects {
		offsets = append(offsets, body.Len())
		body.WriteString(object)
	}
	offsets = append(offsets, body.Len())
	fmt.Fprintf(&body, "5 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", width, height, len(jpegData))
	body.Write(jpegData)
	body.WriteString("\nendstream\nendobj\n")
	xref := body.Len()
	fmt.Fprintf(&body, "xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&body, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&body, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return body.Bytes()
}

func multiImagePDF(data []byte) ([]byte, error) {
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(rgba, rgba.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), source, bounds.Min, draw.Over)
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, rgba, &jpeg.Options{Quality: 95}); err != nil {
		return nil, err
	}
	return multiJPEGPDF(jpegData.Bytes(), bounds.Dx(), bounds.Dy()), nil
}

func multiJPEGPDF(jpegData []byte, width, height int) []byte {
	page := func(pageObject, imageObject, contentObject int) string {
		return fmt.Sprintf("%d 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>\nendobj\n", pageObject, width, height, imageObject, contentObject)
	}
	image := func(object int) string {
		return fmt.Sprintf("%d 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", object, width, height, len(jpegData))
	}
	content := func(object, imageObject int) string {
		stream := fmt.Sprintf("q\n%d 0 0 %d 0 0 cm\n/Im0 Do\nQ\n", width, height)
		return fmt.Sprintf("%d 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", object, len(stream), stream)
	}
	var body bytes.Buffer
	body.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	objects := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>\nendobj\n",
		page(3, 5, 6), page(4, 7, 8),
	}
	for _, object := range objects {
		offsets = append(offsets, body.Len())
		body.WriteString(object)
	}
	for _, object := range []string{image(5), content(6, 5), image(7), content(8, 7)} {
		offsets = append(offsets, body.Len())
		body.WriteString(object)
		if strings.Contains(object, "stream\n") && strings.Contains(object, "/Subtype /Image") {
			body.Write(jpegData)
			body.WriteString("\nendstream\nendobj\n")
		}
	}
	xref := body.Len()
	fmt.Fprintf(&body, "xref\n0 9\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&body, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&body, "trailer\n<< /Size 9 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return body.Bytes()
}

func blankPNG() []byte {
	canvas := image.NewRGBA(image.Rect(0, 0, 96, 96))
	var encoded bytes.Buffer
	fatal(png.Encode(&encoded, canvas))
	return encoded.Bytes()
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	fatal(err)
	return data
}

func smokePDF() []byte {
	objects := []string{
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n",
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n",
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R /Resources << >> >>\nendobj\n",
		"4 0 obj\n<< /Length 9 >>\nstream\nq 0 0 m\nQ\nendstream\nendobj\n",
	}
	var body bytes.Buffer
	body.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for _, object := range objects {
		offsets = append(offsets, body.Len())
		body.WriteString(object)
	}
	xref := body.Len()
	fmt.Fprintf(&body, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&body, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&body, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return body.Bytes()
}

func fatal(err error) {
	if err != nil {
		// Panic runs the manager/app defers so a failed smoke never leaves a
		// managed Node process holding model-cache files or ports open.
		panic(fmt.Errorf("runtime smoke: %w", err))
	}
}
