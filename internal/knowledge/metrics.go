package knowledge

import "time"

// MetricsSnapshot is the process-local structured observability surface. It
// aggregates behavior without recording document text, queries, credentials,
// or raw provider diagnostics.
type MetricsSnapshot struct {
	StartedAt           int64 `json:"startedAt"`
	Imports             int64 `json:"imports"`
	ImportDurationMS    int64 `json:"importDurationMs"`
	ParseDurationMS     int64 `json:"parseDurationMs"`
	ChunkCount          int64 `json:"chunkCount"`
	EmbeddingDurationMS int64 `json:"embeddingDurationMs"`
	Searches            int64 `json:"searches"`
	SearchDurationMS    int64 `json:"searchDurationMs"`
	RerankDurationMS    int64 `json:"rerankDurationMs"`
	CandidateCount      int64 `json:"candidateCount"`
	ContextCount        int64 `json:"contextCount"`
	ModelErrors         int64 `json:"modelErrors"`
	JobFailures         int64 `json:"jobFailures"`
}

func (s *Service) recordMetric(update func(*MetricsSnapshot)) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	if s.metrics.StartedAt == 0 {
		s.metrics.StartedAt = Now().UnixMilli()
	}
	update(&s.metrics)
}

func durationMS(started time.Time) int64 {
	if started.IsZero() {
		return 0
	}
	return maxInt64(0, Now().UnixMilli()-started.UnixMilli())
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Metrics returns a copy of the current structured counters.
func (s *Service) Metrics() MetricsSnapshot {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	return s.metrics
}

// ObserveJobFailure is wired to the job manager. It receives only a kind;
// job errors remain in the job row and are never copied into metrics.
func (s *Service) ObserveJobFailure(kind string) {
	if kind == "" {
		kind = "unknown"
	}
	s.recordMetric(func(m *MetricsSnapshot) { m.JobFailures++ })
}
