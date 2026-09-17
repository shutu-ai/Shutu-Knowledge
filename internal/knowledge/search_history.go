package knowledge

import (
	"context"
	"database/sql"
	"strings"
)

// SearchHistoryItem is one replayable Recall Test invocation.
type SearchHistoryItem struct {
	ID        string `json:"id"`
	BaseID    string `json:"baseId,omitempty"`
	Query     string `json:"query"`
	Mode      string `json:"mode"`
	TopK      int    `json:"topK"`
	MMR       bool   `json:"mmr"`
	TotalHits int    `json:"totalHits"`
	ElapsedMS int64  `json:"elapsedMs"`
	CreatedAt int64  `json:"createdAt"`
}

const searchHistoryColumns = `id, base_id, query, mode, top_k, mmr, total_hits, elapsed_ms, created_at`

func scanSearchHistory(row interface{ Scan(...any) error }) (SearchHistoryItem, error) {
	var item SearchHistoryItem
	var baseID sql.NullString
	var mmr int
	err := row.Scan(&item.ID, &baseID, &item.Query, &item.Mode, &item.TopK, &mmr,
		&item.TotalHits, &item.ElapsedMS, &item.CreatedAt)
	if err != nil {
		return SearchHistoryItem{}, err
	}
	item.BaseID = baseID.String
	item.MMR = mmr != 0
	return item, nil
}

// SaveSearchHistory records one explicit recall invocation and retains the
// newest 50 entries. This is deliberately separate from automatic retrieval.
func (s *Service) SaveSearchHistory(req SearchRequest, result SearchResult) (SearchHistoryItem, error) {
	return s.SaveSearchHistoryContext(context.Background(), req, result)
}

// SaveSearchHistoryContext binds the explicit search-history writes to the
// request context so a disconnected recall request does not keep enqueueing
// mutations after its response has been abandoned.
func (s *Service) SaveSearchHistoryContext(ctx context.Context, req SearchRequest, result SearchResult) (SearchHistoryItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SearchHistoryItem{}, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return SearchHistoryItem{}, nil
	}
	id, err := newID()
	if err != nil {
		return SearchHistoryItem{}, err
	}
	item := SearchHistoryItem{
		ID: id, BaseID: req.BaseID, Query: query, Mode: result.Mode,
		TopK: req.TopK, MMR: req.MMR, TotalHits: result.Total,
		ElapsedMS: result.ElapsedMS, CreatedAt: Now().UnixMilli(),
	}
	if item.TopK <= 0 {
		item.TopK = s.global.Retrieval.TopK
	}
	_, err = s.store.db.ExecContext(ctx,
		`INSERT INTO search_history (`+searchHistoryColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, nullableString(item.BaseID), item.Query, item.Mode, item.TopK,
		boolToInt(item.MMR), item.TotalHits, item.ElapsedMS, item.CreatedAt,
	)
	if err != nil {
		return SearchHistoryItem{}, err
	}
	_, err = s.store.db.ExecContext(ctx, `DELETE FROM search_history WHERE id NOT IN (
		SELECT id FROM search_history ORDER BY created_at DESC, id DESC LIMIT 50
	)`)
	return item, err
}

// ListSearchHistory returns newest-first replay entries.
func (s *Service) ListSearchHistory(limit int) ([]SearchHistoryItem, error) {
	return s.ListSearchHistoryContext(context.Background(), limit)
}

// ListSearchHistoryContext uses the caller's cancellation boundary for the
// bounded history query.
func (s *Service) ListSearchHistoryContext(ctx context.Context, limit int) ([]SearchHistoryItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	rows, err := s.store.db.QueryContext(ctx,
		`SELECT `+searchHistoryColumns+` FROM search_history
		 ORDER BY created_at DESC, id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchHistoryItem{}
	for rows.Next() {
		item, err := scanSearchHistory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeleteSearchHistory removes one item. An empty ID clears all history.
func (s *Service) DeleteSearchHistory(id string) error {
	return s.DeleteSearchHistoryContext(context.Background(), id)
}

// DeleteSearchHistoryContext binds history cleanup to the caller context.
func (s *Service) DeleteSearchHistoryContext(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		_, err := s.store.db.ExecContext(ctx, `DELETE FROM search_history`)
		return err
	}
	_, err := s.store.db.ExecContext(ctx, `DELETE FROM search_history WHERE id = ?`, id)
	return err
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
