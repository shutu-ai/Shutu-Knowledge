// Command architecture-baseline produces a data-free, repeatable P0 input
// baseline. It only reads the supplied dataset/database paths and never copies
// source content into the report.
package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

const reportSchemaVersion = 1

type namedPaths struct{ values map[string]string }

func (p *namedPaths) String() string {
	if p == nil || len(p.values) == 0 {
		return ""
	}
	names := make([]string, 0, len(p.values))
	for name := range p.values {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+p.values[name])
	}
	return strings.Join(parts, ",")
}

func (p *namedPaths) Set(value string) error {
	name, path, ok := strings.Cut(value, "=")
	name = strings.TrimSpace(name)
	path = strings.TrimSpace(path)
	if !ok || name == "" || path == "" {
		return fmt.Errorf("expected name=path, got %q", value)
	}
	if strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("invalid name %q", name)
	}
	if p.values == nil {
		p.values = map[string]string{}
	}
	if _, exists := p.values[name]; exists {
		return fmt.Errorf("duplicate name %q", name)
	}
	p.values[name] = path
	return nil
}

type report struct {
	SchemaVersion     int                `json:"schemaVersion"`
	GeneratedAtUTC    string             `json:"generatedAtUtc"`
	ReportFingerprint string             `json:"reportFingerprint"`
	Tool              string             `json:"tool"`
	Host              hostSnapshot       `json:"host"`
	Config            *configSnapshot    `json:"config,omitempty"`
	Datasets          []datasetSnapshot  `json:"datasets"`
	Databases         []databaseSnapshot `json:"databases,omitempty"`
}

type hostSnapshot struct {
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	CPUs           int    `json:"cpus"`
	GoVersion      string `json:"goVersion"`
	AvailableBytes uint64 `json:"availableBytes,omitempty"`
	TotalBytes     uint64 `json:"totalBytes,omitempty"`
	MemoryBytes    uint64 `json:"memoryBytes,omitempty"`
	ResourcePath   string `json:"resourcePath"`
}

type configSnapshot struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Redacted any    `json:"redacted"`
}

type datasetSnapshot struct {
	Name           string             `json:"name"`
	Root           string             `json:"root"`
	Files          []fileSnapshot     `json:"files"`
	FileCount      int                `json:"fileCount"`
	DirectoryCount int                `json:"directoryCount"`
	TotalBytes     int64              `json:"totalBytes"`
	MaxDepth       int                `json:"maxDepth"`
	Text           textSnapshot       `json:"text"`
	Extensions     []extensionSummary `json:"extensions,omitempty"`
	Models         []modelSnapshot    `json:"models,omitempty"`
}

type fileSnapshot struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind"`
}

type textSnapshot struct {
	Files      int64 `json:"files"`
	Bytes      int64 `json:"bytes"`
	Characters int64 `json:"characters"`
	Lines      int64 `json:"lines"`
}

type extensionSummary struct {
	Extension  string `json:"extension"`
	Files      int64  `json:"files"`
	Bytes      int64  `json:"bytes"`
	Characters int64  `json:"characters"`
	Lines      int64  `json:"lines"`
}

type modelSnapshot struct {
	ManifestPath string   `json:"manifestPath"`
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Artifacts    []string `json:"artifacts,omitempty"`
	DownloadedAt int64    `json:"downloadedAt,omitempty"`
}

type storageFormatSnapshot struct {
	Available        bool   `json:"available"`
	FormatVersion    int64  `json:"formatVersion,omitempty"`
	MinReaderVersion int64  `json:"minReaderVersion,omitempty"`
	MinWriterVersion int64  `json:"minWriterVersion,omitempty"`
	MigrationStatus  string `json:"migrationStatus,omitempty"`
}

type databaseSnapshot struct {
	Name            string                 `json:"name"`
	Path            string                 `json:"path"`
	Bytes           int64                  `json:"bytes"`
	SHA256          string                 `json:"sha256"`
	StorageFormat   storageFormatSnapshot  `json:"storageFormat"`
	Documents       map[string]int64       `json:"documents,omitempty"`
	Chunks          int64                  `json:"chunks,omitempty"`
	EmbeddingModels []embeddingModelCounts `json:"embeddingModels,omitempty"`
}

type embeddingModelCounts struct {
	Model      string `json:"model"`
	Vectors    int64  `json:"vectors"`
	Dimensions []int  `json:"dimensions,omitempty"`
}

func main() {
	var datasets, databases namedPaths
	configPath := flag.String("config", "", "optional YAML config to hash and redact")
	outputPath := flag.String("output", "", "output JSON report path (required)")
	flag.Var(&datasets, "dataset", "isolated dataset as name=path; repeatable")
	flag.Var(&databases, "database", "SQLite database as name=path; repeatable")
	flag.Parse()

	if *outputPath == "" {
		fatal(errors.New("-output is required"))
	}
	if len(datasets.values) == 0 {
		fatal(errors.New("at least one -dataset name=path is required"))
	}

	r, err := buildReport(*configPath, datasets.values, databases.values)
	if err != nil {
		fatal(err)
	}
	if err := writeReport(*outputPath, r); err != nil {
		fatal(err)
	}
	fmt.Printf("architecture baseline: %s fingerprint=%s datasets=%d databases=%d\n",
		*outputPath, r.ReportFingerprint, len(r.Datasets), len(r.Databases))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "architecture baseline:", err)
	os.Exit(1)
}

func buildReport(configPath string, datasetPaths, databasePaths map[string]string) (report, error) {
	names := sortedKeys(datasetPaths)
	r := report{
		SchemaVersion:  reportSchemaVersion,
		GeneratedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		Tool:           "shutu-knowledge/architecture-baseline-v1",
		Datasets:       make([]datasetSnapshot, 0, len(names)),
	}
	for _, name := range names {
		dataset, err := scanDataset(name, datasetPaths[name])
		if err != nil {
			return report{}, err
		}
		r.Datasets = append(r.Datasets, dataset)
	}
	if configPath != "" {
		config, err := scanConfig(configPath)
		if err != nil {
			return report{}, err
		}
		r.Config = &config
	}
	for _, name := range sortedKeys(databasePaths) {
		database, err := scanDatabase(name, databasePaths[name])
		if err != nil {
			return report{}, err
		}
		r.Databases = append(r.Databases, database)
	}
	r.Host = makeHostSnapshot(firstDatasetRoot(r.Datasets))
	r.ReportFingerprint = reportFingerprint(r)
	return r, nil
}

func firstDatasetRoot(datasets []datasetSnapshot) string {
	if len(datasets) == 0 {
		return "."
	}
	return datasets[0].Root
}

func makeHostSnapshot(path string) hostSnapshot {
	available, total := diskCapacity(path)
	return hostSnapshot{
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		CPUs:           runtime.NumCPU(),
		GoVersion:      runtime.Version(),
		AvailableBytes: available,
		TotalBytes:     total,
		MemoryBytes:    hostMemoryBytes(),
		ResourcePath:   path,
	}
}

func scanDataset(name, root string) (datasetSnapshot, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return datasetSnapshot{}, fmt.Errorf("dataset %s path: %w", name, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return datasetSnapshot{}, fmt.Errorf("dataset %s: %w", name, err)
	}
	if !info.IsDir() {
		return datasetSnapshot{}, fmt.Errorf("dataset %s is not a directory", name)
	}
	d := datasetSnapshot{Name: name, Root: filepath.Clean(abs), Files: []fileSnapshot{}}
	extensions := map[string]*extensionSummary{}
	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		depth := len(strings.Split(filepath.ToSlash(rel), "/"))
		if depth > d.MaxDepth {
			d.MaxDepth = depth
		}
		if entry.IsDir() {
			d.DirectoryCount++
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			d.FileCount++
			d.Files = append(d.Files, fileSnapshot{Path: filepath.ToSlash(rel), Kind: "symlink"})
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		file, stats, err := scanFile(path, filepath.Ext(path))
		if err != nil {
			return fmt.Errorf("dataset %s file %s: %w", name, rel, err)
		}
		file.Path = filepath.ToSlash(rel)
		d.Files = append(d.Files, fileSnapshot(file))
		d.FileCount++
		d.TotalBytes += file.Bytes
		if stats.text {
			d.Text.Files++
			d.Text.Bytes += stats.bytes
			d.Text.Characters += stats.characters
			d.Text.Lines += stats.lines
			ext := strings.ToLower(filepath.Ext(path))
			bucket := extensions[ext]
			if bucket == nil {
				bucket = &extensionSummary{Extension: ext}
				extensions[ext] = bucket
			}
			bucket.Files++
			bucket.Bytes += stats.bytes
			bucket.Characters += stats.characters
			bucket.Lines += stats.lines
		}
		if strings.EqualFold(filepath.Base(path), "manifest.json") {
			if model, ok := readModelManifest(path, filepath.ToSlash(rel)); ok {
				d.Models = append(d.Models, model)
			}
		}
		return nil
	})
	if err != nil {
		return datasetSnapshot{}, err
	}
	sort.Slice(d.Files, func(i, j int) bool { return d.Files[i].Path < d.Files[j].Path })
	for _, ext := range sortedKeys(extensions) {
		d.Extensions = append(d.Extensions, *extensions[ext])
	}
	sort.Slice(d.Models, func(i, j int) bool { return d.Models[i].ManifestPath < d.Models[j].ManifestPath })
	return d, nil
}

type fileStats struct {
	text       bool
	bytes      int64
	characters int64
	lines      int64
}

func scanFile(path, extension string) (fileSnapshot, fileStats, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileSnapshot{}, fileStats{}, err
	}
	defer file.Close()
	hash := sha256.New()
	stats := fileStats{text: isTextExtension(extension)}
	buffer := make([]byte, 1024*1024)
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			_, _ = hash.Write(chunk)
			stats.bytes += int64(n)
			if stats.text {
				stats.characters += int64(utf8.RuneCount(chunk))
				stats.lines += int64(strings.Count(string(chunk), "\n"))
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return fileSnapshot{}, fileStats{}, readErr
		}
	}
	if stats.text && stats.bytes > 0 && stats.lines == 0 {
		stats.lines = 1
	} else if stats.text && stats.bytes > 0 {
		last, err := lastByte(path)
		if err != nil {
			return fileSnapshot{}, fileStats{}, err
		}
		if last != '\n' {
			stats.lines++
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return fileSnapshot{}, fileStats{}, err
	}
	return fileSnapshot{Bytes: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil)), Kind: "file"}, stats, nil
}

func lastByte(path string) (byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return 0, err
	}
	var b [1]byte
	_, err = file.ReadAt(b[:], info.Size()-1)
	return b[0], err
}

func isTextExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".txt", ".md", ".markdown", ".html", ".htm", ".json", ".csv", ".tsv", ".xml", ".yaml", ".yml", ".toml", ".log", ".rtf":
		return true
	default:
		return false
	}
}

func readModelManifest(path, relative string) (modelSnapshot, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 4*1024*1024 {
		return modelSnapshot{}, false
	}
	var manifest struct {
		ID         string   `json:"id"`
		Kind       string   `json:"kind"`
		Artifacts  []string `json:"artifacts"`
		Downloaded int64    `json:"downloadedAt"`
	}
	if json.Unmarshal(data, &manifest) != nil || strings.TrimSpace(manifest.ID) == "" {
		return modelSnapshot{}, false
	}
	sort.Strings(manifest.Artifacts)
	return modelSnapshot{ManifestPath: relative, ID: manifest.ID, Kind: manifest.Kind, Artifacts: manifest.Artifacts, DownloadedAt: manifest.Downloaded}, true
}

func scanConfig(path string) (configSnapshot, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return configSnapshot{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return configSnapshot{}, fmt.Errorf("config: %w", err)
	}
	var value any
	if err := yaml.Unmarshal(data, &value); err != nil {
		return configSnapshot{}, fmt.Errorf("decode config: %w", err)
	}
	return configSnapshot{
		Path:     filepath.Clean(abs),
		SHA256:   digest(data),
		Redacted: redact(value, ""),
	}, nil
}

func redact(value any, key string) any {
	if sensitiveKey(key) {
		return "<redacted>"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for child, childValue := range typed {
			out[child] = redact(childValue, child)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for child, childValue := range typed {
			out[fmt.Sprint(child)] = redact(childValue, fmt.Sprint(child))
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redact(child, key)
		}
		return out
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, token := range []string{"api_key", "api-key", "apikey", "token", "password", "secret", "cookie", "authorization", "credential"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

func scanDatabase(name, path string) (databaseSnapshot, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return databaseSnapshot{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return databaseSnapshot{}, fmt.Errorf("database %s: %w", name, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return databaseSnapshot{}, fmt.Errorf("database %s hash: %w", name, err)
	}
	// file: URI plus mode=ro prevents this inventory tool from changing the
	// database or running a checkpoint while an operator is collecting P0 data.
	dsn := "file:" + filepath.ToSlash(abs) + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return databaseSnapshot{}, fmt.Errorf("database %s open: %w", name, err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return databaseSnapshot{}, fmt.Errorf("database %s ping: %w", name, err)
	}
	d := databaseSnapshot{Name: name, Path: filepath.Clean(abs), Bytes: info.Size(), SHA256: digest(data), Documents: map[string]int64{}}
	if exists, err := tableExists(db, "storage_format"); err != nil {
		return databaseSnapshot{}, err
	} else if exists {
		err = db.QueryRow(`SELECT format_version, min_reader_version, min_writer_version, migration_status FROM storage_format WHERE id = 1`).Scan(
			&d.StorageFormat.FormatVersion, &d.StorageFormat.MinReaderVersion, &d.StorageFormat.MinWriterVersion, &d.StorageFormat.MigrationStatus)
		if err != nil {
			return databaseSnapshot{}, fmt.Errorf("database %s storage format: %w", name, err)
		}
		d.StorageFormat.Available = true
	}
	if exists, err := tableExists(db, "documents"); err != nil {
		return databaseSnapshot{}, err
	} else if exists {
		for key, query := range map[string]string{
			"total":      `SELECT COUNT(*) FROM documents`,
			"ready":      `SELECT COUNT(*) FROM documents WHERE status = 'ready'`,
			"characters": `SELECT COALESCE(SUM(char_count), 0) FROM documents`,
		} {
			var count int64
			if err := db.QueryRow(query).Scan(&count); err != nil {
				return databaseSnapshot{}, fmt.Errorf("database %s documents %s: %w", name, key, err)
			}
			d.Documents[key] = count
		}
	}
	if exists, err := tableExists(db, "chunks"); err != nil {
		return databaseSnapshot{}, err
	} else if exists {
		if err := db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&d.Chunks); err != nil {
			return databaseSnapshot{}, fmt.Errorf("database %s chunks: %w", name, err)
		}
		models, err := queryEmbeddingModels(db)
		if err != nil {
			return databaseSnapshot{}, fmt.Errorf("database %s embedding models: %w", name, err)
		}
		d.EmbeddingModels = models
	}
	return d, nil
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count)
	return count > 0, err
}

func queryEmbeddingModels(db *sql.DB) ([]embeddingModelCounts, error) {
	rows, err := db.Query(`SELECT COALESCE(embedding_model, ''), COUNT(*) FROM chunks WHERE embedding IS NOT NULL GROUP BY COALESCE(embedding_model, '') ORDER BY COALESCE(embedding_model, '')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []embeddingModelCounts
	for rows.Next() {
		var model embeddingModelCounts
		if err := rows.Scan(&model.Model, &model.Vectors); err != nil {
			return nil, err
		}
		var dimension int
		var embedding []byte
		if err := db.QueryRow(`SELECT embedding FROM chunks WHERE embedding IS NOT NULL AND COALESCE(embedding_model, '') = ? LIMIT 1`, model.Model).Scan(&embedding); err == nil {
			var vector []float64
			if json.Unmarshal(embedding, &vector) == nil && len(vector) > 0 {
				dimension = len(vector)
			}
		}
		if dimension > 0 {
			model.Dimensions = []int{dimension}
		}
		result = append(result, model)
	}
	return result, rows.Err()
}

func writeReport(path string, r report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(abs), ".architecture-baseline-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, abs)
}

func reportFingerprint(r report) string {
	stable := struct {
		SchemaVersion int                `json:"schemaVersion"`
		Tool          string             `json:"tool"`
		Config        *configSnapshot    `json:"config,omitempty"`
		Datasets      []datasetSnapshot  `json:"datasets"`
		Databases     []databaseSnapshot `json:"databases,omitempty"`
	}{
		SchemaVersion: r.SchemaVersion,
		Tool:          r.Tool,
		Config:        r.Config,
		Datasets:      append([]datasetSnapshot(nil), r.Datasets...),
		Databases:     append([]databaseSnapshot(nil), r.Databases...),
	}
	if stable.Config != nil {
		config := *stable.Config
		config.Path = ""
		stable.Config = &config
	}
	for i := range stable.Datasets {
		stable.Datasets[i].Root = ""
	}
	for i := range stable.Databases {
		stable.Databases[i].Path = ""
	}
	data, err := json.Marshal(stable)
	if err != nil {
		return ""
	}
	return digest(data)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
