package knowledge

import "time"

// MetricsSnapshot is the process-local structured observability surface. It
// aggregates behavior without recording document text, queries, credentials,
// or raw provider diagnostics.
type MetricsSnapshot struct {
	StartedAt           int64            `json:"startedAt"`
	Imports             int64            `json:"imports"`
	ImportDurationMS    int64            `json:"importDurationMs"`
	QueueWaitMS         int64            `json:"queueWaitMs"`
	RunTimeMS           int64            `json:"runTimeMs"`
	ParseDurationMS     int64            `json:"parseDurationMs"`
	DiskReadMS          int64            `json:"diskReadMs"`
	DBWaitMS            int64            `json:"dbWaitMs"`
	DBTransactionMS     int64            `json:"dbTransactionMs"`
	FTSTimeMS           int64            `json:"ftsTimeMs"`
	VectorTimeMS        int64            `json:"vectorTimeMs"`
	ChunkCount          int64            `json:"chunkCount"`
	NodeCount           int64            `json:"nodeCount"`
	TableCount          int64            `json:"tableCount"`
	FigureCount         int64            `json:"figureCount"`
	ParserFallbacks     int64            `json:"parserFallbacks"`
	ParserSelections    map[string]int64 `json:"parserSelections,omitempty"`
	EmbeddingDurationMS int64            `json:"embeddingDurationMs"`
	Searches            int64            `json:"searches"`
	SearchDurationMS    int64            `json:"searchDurationMs"`
	RerankDurationMS    int64            `json:"rerankDurationMs"`
	CandidateCount      int64            `json:"candidateCount"`
	ContextCount        int64            `json:"contextCount"`
	ModelErrors         int64            `json:"modelErrors"`
	ModelSchedulerWaits int64            `json:"modelSchedulerWaits"`
	SearchTimeouts      int64            `json:"searchTimeouts"`
	JobFailures         int64            `json:"jobFailures"`
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
	out := s.metrics
	if s.metrics.ParserSelections != nil {
		out.ParserSelections = make(map[string]int64, len(s.metrics.ParserSelections))
		for parser, count := range s.metrics.ParserSelections {
			out.ParserSelections[parser] = count
		}
	}
	return out
}

// ObserveJobFailure is wired to the job manager. It receives only a kind;
// job errors remain in the job row and are never copied into metrics.
func (s *Service) ObserveJobFailure(kind string) {
	if kind == "" {
		kind = "unknown"
	}
	s.recordMetric(func(m *MetricsSnapshot) { m.JobFailures++ })
}
