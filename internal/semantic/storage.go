package semantic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// ErrNotFound is returned when an explicitly requested compilation or unit is
// absent. It must not be interpreted as an invalid base.
var ErrNotFound = errors.New("semantic knowledge not found")

const compilationColumns = `base_id, generation, state, compiler, compiler_version,
	model, model_version, prompt_version, source_document_ids, metadata, created_at, completed_at`

const unitColumns = `id, base_id, generation, unit_type, title, canonical_key, content,
	aliases, parent_unit_id, confidence, status, valid_from, valid_to, superseded_by,
	metadata, created_at, updated_at`

const relationColumns = `id, base_id, generation, relation_type, subject_unit_id, object_unit_id,
	predicate, statement, canonical_key, confidence, status, valid_from, valid_to,
	superseded_by, metadata, created_at, updated_at`

// Store owns the 0.4 SQLite projection. It is independent from the existing
// evidence store so legacy search never depends on compiled semantic memory.
type Store struct {
	db *storage.DB
}

func NewStore(db *storage.DB) *Store { return &Store{db: db} }

// CreateCompilation validates and writes one immutable building generation in
// a single transaction. No row is visible as active until ActivateCompilation.
func (s *Store) CreateCompilation(ctx context.Context, compilation Compilation) error {
	if compilation.State == "" {
		compilation.State = CompilationBuilding
	}
	if compilation.State != CompilationBuilding {
		return fmt.Errorf("new semantic compilation must be in building state, got %q", compilation.State)
	}
	if err := compilation.Validate(); err != nil {
		return err
	}
	return s.db.WriteTx(ctx, storage.NormalWrite, nil, func(tx *sql.Tx) error {
		sourceDocs, err := jsonStringList(compilation.SourceDocumentIDs)
		if err != nil {
			return err
		}
		metadata, err := jsonStringMap(compilation.Metadata)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_compilations
			(base_id, generation, state, compiler, compiler_version, model, model_version,
			 prompt_version, source_document_ids, metadata, created_at, completed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			compilation.BaseID, compilation.Generation, compilation.State, compilation.Compiler,
			compilation.CompilerVersion, compilation.Model, compilation.ModelVersion,
			compilation.PromptVersion, sourceDocs, metadata, compilation.CreatedAt); err != nil {
			return err
		}
		if err := insertUnits(ctx, tx, compilation.Units); err != nil {
			return err
		}
		return insertRelations(ctx, tx, compilation.Relations)
	})
}

// ActivateCompilation atomically retires the current generation and publishes
// the requested building generation. Failed compilations cannot be activated.
func (s *Store) ActivateCompilation(ctx context.Context, baseID string, generation int64) error {
	return s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM knowledge_compilations
			WHERE base_id = ? AND generation = ?`, baseID, generation).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state != string(CompilationBuilding) {
			return fmt.Errorf("semantic compilation %s/%d cannot activate from state %q", baseID, generation, state)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE knowledge_compilations
			SET state = ?, completed_at = COALESCE(completed_at, strftime('%s','now'))
			WHERE base_id = ? AND state = ?`,
			CompilationRetired, baseID, CompilationActive); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE knowledge_compilations
			SET state = ?, completed_at = COALESCE(completed_at, strftime('%s','now'))
			WHERE base_id = ? AND generation = ? AND state = ?`,
			CompilationActive, baseID, generation, CompilationBuilding)
		if err != nil {
			return err
		}
		return nil
	})
}

// RetireActiveCompilation removes semantic memory from visibility without
// deleting immutable history. It is the conservative degradation path when
// synchronous delete propagation fails.
func (s *Store) RetireActiveCompilation(ctx context.Context, baseID string) error {
	result, err := s.db.ExecPriority(ctx, storage.ControlWrite, `UPDATE knowledge_compilations
		SET state = ?, completed_at = COALESCE(completed_at, strftime('%s','now'))
		WHERE base_id = ? AND state = ?`, CompilationRetired, baseID, CompilationActive)
	if err != nil {
		return err
	}
	if changed(result) == 0 {
		return ErrNotFound
	}
	return nil
}

// FailCompilation records a terminal failed build without exposing it.
func (s *Store) FailCompilation(ctx context.Context, baseID string, generation int64) error {
	result, err := s.db.ExecPriority(ctx, storage.NormalWrite, `UPDATE knowledge_compilations
		SET state = ?, completed_at = strftime('%s','now')
		WHERE base_id = ? AND generation = ? AND state = ?`,
		CompilationFailed, baseID, generation, CompilationBuilding)
	if err != nil {
		return err
	}
	if changed(result) == 0 {
		return ErrNotFound
	}
	return nil
}

// NextGeneration returns one above the newest persisted generation. It does
// not reserve the value; the caller's compilation write transaction remains
// authoritative for uniqueness.
func (s *Store) NextGeneration(ctx context.Context, baseID string) (int64, error) {
	var generation int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation), 0) + 1
		FROM knowledge_compilations WHERE base_id = ?`, baseID).Scan(&generation)
	return generation, err
}

// GetActiveCompilation returns the currently published immutable set.
func (s *Store) GetActiveCompilation(ctx context.Context, baseID string) (Compilation, error) {
	var generation int64
	var state string
	err := s.db.QueryRowContext(ctx, `SELECT generation, state FROM knowledge_compilations
		WHERE base_id = ? AND state = ?`, baseID, CompilationActive).Scan(&generation, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return Compilation{}, ErrNotFound
	}
	if err != nil {
		return Compilation{}, err
	}
	return s.getCompilation(ctx, baseID, generation)
}

// GetCompilation returns a building, active, retired, or failed generation.
func (s *Store) GetCompilation(ctx context.Context, baseID string, generation int64) (Compilation, error) {
	return s.getCompilation(ctx, baseID, generation)
}

// ResolveUnitEvidence loads the unit's compilation and follows every
// derived_from link down to exact Document IR/chunk pointers.
func (s *Store) ResolveUnitEvidence(ctx context.Context, unitID string) ([]EvidenceSource, error) {
	var baseID string
	var generation int64
	err := s.db.QueryRowContext(ctx, `SELECT base_id, generation FROM knowledge_units
		WHERE id = ?`, unitID).Scan(&baseID, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	compilation, err := s.getCompilation(ctx, baseID, generation)
	if err != nil {
		return nil, err
	}
	return ResolveEvidence(compilation.Units, unitID)
}

func (s *Store) getCompilation(ctx context.Context, baseID string, generation int64) (Compilation, error) {
	var compilation Compilation
	var sourceDocs, metadata string
	var completedAt sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT `+compilationColumns+` FROM knowledge_compilations
		WHERE base_id = ? AND generation = ?`, baseID, generation).Scan(
		&compilation.BaseID, &compilation.Generation, &compilation.State, &compilation.Compiler,
		&compilation.CompilerVersion, &compilation.Model, &compilation.ModelVersion,
		&compilation.PromptVersion, &sourceDocs, &metadata, &compilation.CreatedAt,
		&completedAt,
	)
	compilation.CompletedAt = completedAt.Int64
	if errors.Is(err, sql.ErrNoRows) {
		return Compilation{}, ErrNotFound
	}
	if err != nil {
		return Compilation{}, err
	}
	if err := decodeStringList(sourceDocs, &compilation.SourceDocumentIDs); err != nil {
		return Compilation{}, err
	}
	if err := decodeStringMap(metadata, &compilation.Metadata); err != nil {
		return Compilation{}, err
	}
	if compilation.Units, err = s.listUnits(ctx, baseID, generation); err != nil {
		return Compilation{}, err
	}
	if compilation.Relations, err = s.listRelations(ctx, baseID, generation); err != nil {
		return Compilation{}, err
	}
	return compilation, nil
}

func (s *Store) listUnits(ctx context.Context, baseID string, generation int64) ([]Unit, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+unitColumns+` FROM knowledge_units
		WHERE base_id = ? AND generation = ? ORDER BY unit_type, canonical_key, id`, baseID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unit
	for rows.Next() {
		unit, aliases, metadata, err := scanUnit(rows)
		if err != nil {
			return nil, err
		}
		if err := decodeStringList(aliases, &unit.Aliases); err != nil {
			return nil, err
		}
		if err := decodeStringMap(metadata, &unit.Metadata); err != nil {
			return nil, err
		}
		syncTemporalFields(&unit)
		out = append(out, unit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byID := make(map[string]*Unit, len(out))
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	if err := s.attachUnitSources(ctx, byID); err != nil {
		return nil, err
	}
	if err := s.attachDerivedFrom(ctx, byID); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) listRelations(ctx context.Context, baseID string, generation int64) ([]Relation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+relationColumns+` FROM knowledge_relations
		WHERE base_id = ? AND generation = ? ORDER BY relation_type, canonical_key, id`, baseID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Relation
	for rows.Next() {
		relation, metadata, err := scanRelation(rows)
		if err != nil {
			return nil, err
		}
		if err := decodeStringMap(metadata, &relation.Metadata); err != nil {
			return nil, err
		}
		out = append(out, relation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byID := make(map[string]*Relation, len(out))
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	if err := s.attachRelationSources(ctx, byID); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) attachUnitSources(ctx context.Context, units map[string]*Unit) error {
	if len(units) == 0 {
		return nil
	}
	args := make([]any, 0, len(units))
	placeholders := make([]string, 0, len(units))
	for id := range units {
		args = append(args, id)
		placeholders = append(placeholders, "?")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT provenance_id, unit_id, doc_id, index_generation,
		source_version, node_id, chunk_id, source_anchor, source_order
		FROM knowledge_unit_sources WHERE unit_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY unit_id, source_order, provenance_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		source, err := scanSource(rows)
		if err != nil {
			return err
		}
		unit, ok := units[source.UnitID]
		if !ok {
			return fmt.Errorf("semantic provenance references missing unit %s", source.UnitID)
		}
		unit.Sources = append(unit.Sources, source)
	}
	return rows.Err()
}

func (s *Store) attachDerivedFrom(ctx context.Context, units map[string]*Unit) error {
	if len(units) == 0 {
		return nil
	}
	args := make([]any, 0, len(units))
	placeholders := make([]string, 0, len(units))
	for id := range units {
		args = append(args, id)
		placeholders = append(placeholders, "?")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT unit_id, derived_unit_id FROM knowledge_unit_derived_from
		WHERE unit_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY unit_id, link_order, derived_unit_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var unitID, derivedID string
		if err := rows.Scan(&unitID, &derivedID); err != nil {
			return err
		}
		unit, ok := units[unitID]
		if !ok {
			return fmt.Errorf("semantic derived_from references missing unit %s", unitID)
		}
		unit.DerivedFrom = append(unit.DerivedFrom, derivedID)
	}
	return rows.Err()
}

func (s *Store) attachRelationSources(ctx context.Context, relations map[string]*Relation) error {
	if len(relations) == 0 {
		return nil
	}
	args := make([]any, 0, len(relations))
	placeholders := make([]string, 0, len(relations))
	for id := range relations {
		args = append(args, id)
		placeholders = append(placeholders, "?")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT provenance_id, relation_id, doc_id, index_generation,
		source_version, node_id, chunk_id, source_anchor, source_order
		FROM knowledge_relation_sources WHERE relation_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY relation_id, source_order, provenance_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		source, err := scanRelationSource(rows)
		if err != nil {
			return err
		}
		relation, ok := relations[source.UnitID]
		if !ok {
			return fmt.Errorf("semantic relation provenance references missing relation %s", source.UnitID)
		}
		relation.Sources = append(relation.Sources, source.EvidenceSource)
	}
	return rows.Err()
}

func insertUnits(ctx context.Context, tx *sql.Tx, units []Unit) error {
	for _, unit := range units {
		aliases, err := jsonStringList(unit.Aliases)
		if err != nil {
			return err
		}
		metadata, err := jsonStringMap(unit.Metadata)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_units
			(id, base_id, generation, unit_type, title, canonical_key, content, aliases,
			 parent_unit_id, confidence, status, valid_from, valid_to, superseded_by, metadata, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			unit.ID, unit.BaseID, unit.Generation, unit.Type, unit.Title, unit.CanonicalKey,
			unit.Content, aliases, nullString(unit.ParentUnitID), unit.Confidence, unit.Status,
			nullInt64(unit.ValidFrom), nullInt64(unit.ValidTo), nullString(unit.SupersededBy), metadata,
			unit.CreatedAt, unit.UpdatedAt); err != nil {
			return err
		}
		for _, source := range unit.Sources {
			if err := insertSource(ctx, tx, `knowledge_unit_sources`, "unit_id", unit.ID, source); err != nil {
				return err
			}
		}
		for order, derived := range unit.DerivedFrom {
			if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_unit_derived_from
				(unit_id, derived_unit_id, link_order) VALUES (?, ?, ?)`, unit.ID, derived, order); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertRelations(ctx context.Context, tx *sql.Tx, relations []Relation) error {
	for _, relation := range relations {
		metadata, err := jsonStringMap(relation.Metadata)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_relations
			(id, base_id, generation, relation_type, subject_unit_id, object_unit_id, predicate,
			 statement, canonical_key, confidence, status, valid_from, valid_to, superseded_by,
			 metadata, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			relation.ID, relation.BaseID, relation.Generation, relation.Type, relation.SubjectUnitID,
			relation.ObjectUnitID, relation.Predicate, relation.Statement, relation.CanonicalKey,
			relation.Confidence, relation.Status, nullInt64(relation.ValidFrom), nullInt64(relation.ValidTo),
			nullString(relation.SupersededBy), metadata, relation.CreatedAt, relation.UpdatedAt); err != nil {
			return err
		}
		for _, source := range relation.Sources {
			if err := insertSource(ctx, tx, `knowledge_relation_sources`, "relation_id", relation.ID, source); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertSource(ctx context.Context, tx *sql.Tx, table, idColumn, ownerID string, source EvidenceSource) error {
	anchor, err := json.Marshal(source.SourceAnchor)
	if err != nil {
		return fmt.Errorf("marshal knowledge source anchor: %w", err)
	}
	if source.SourceAnchor == nil {
		anchor = []byte("{}")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO `+table+`
		(`+idColumn+`, doc_id, index_generation, source_version, node_id, chunk_id, source_anchor, source_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ownerID, source.DocumentID, source.IndexGeneration, source.SourceVersion,
		nullString(source.NodeID), nullString(source.ChunkID), string(anchor), source.SourceOrder)
	return err
}

func scanUnit(row interface{ Scan(...any) error }) (unit Unit, aliases, metadata string, err error) {
	var parent, superseded sql.NullString
	var validFrom, validTo sql.NullInt64
	err = row.Scan(&unit.ID, &unit.BaseID, &unit.Generation, &unit.Type, &unit.Title,
		&unit.CanonicalKey, &unit.Content, &aliases, &parent, &unit.Confidence,
		&unit.Status, &validFrom, &validTo, &superseded, &metadata,
		&unit.CreatedAt, &unit.UpdatedAt)
	unit.ParentUnitID = parent.String
	unit.SupersededBy = superseded.String
	unit.ValidFrom = validFrom.Int64
	unit.ValidTo = validTo.Int64
	return unit, aliases, metadata, err
}

func scanRelation(row interface{ Scan(...any) error }) (relation Relation, metadata string, err error) {
	var superseded sql.NullString
	var validFrom, validTo sql.NullInt64
	err = row.Scan(&relation.ID, &relation.BaseID, &relation.Generation, &relation.Type,
		&relation.SubjectUnitID, &relation.ObjectUnitID, &relation.Predicate, &relation.Statement,
		&relation.CanonicalKey, &relation.Confidence, &relation.Status, &validFrom,
		&validTo, &superseded, &metadata, &relation.CreatedAt, &relation.UpdatedAt)
	relation.SupersededBy = superseded.String
	relation.ValidFrom = validFrom.Int64
	relation.ValidTo = validTo.Int64
	return relation, metadata, err
}

func scanSource(rows *sql.Rows) (EvidenceSource, error) {
	var source EvidenceSource
	var anchor string
	var id int64
	var nodeID, chunkID sql.NullString
	if err := rows.Scan(&id, &source.UnitID, &source.DocumentID, &source.IndexGeneration,
		&source.SourceVersion, &nodeID, &chunkID, &anchor, &source.SourceOrder); err != nil {
		return source, err
	}
	source.NodeID = nodeID.String
	source.ChunkID = chunkID.String
	if anchor != "" {
		if err := json.Unmarshal([]byte(anchor), &source.SourceAnchor); err != nil {
			return source, fmt.Errorf("decode knowledge source anchor: %w", err)
		}
	}
	return source, nil
}

type relationSourceRow struct {
	EvidenceSource
}

func scanRelationSource(rows *sql.Rows) (relationSourceRow, error) {
	var out relationSourceRow
	var anchor string
	var id int64
	var nodeID, chunkID sql.NullString
	if err := rows.Scan(&id, &out.UnitID, &out.DocumentID, &out.IndexGeneration,
		&out.SourceVersion, &nodeID, &chunkID, &anchor, &out.SourceOrder); err != nil {
		return out, err
	}
	out.NodeID = nodeID.String
	out.ChunkID = chunkID.String
	if anchor != "" {
		if err := json.Unmarshal([]byte(anchor), &out.SourceAnchor); err != nil {
			return out, fmt.Errorf("decode relation source anchor: %w", err)
		}
	}
	return out, nil
}

func jsonStringList(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("marshal string list: %w", err)
	}
	return string(data), nil
}

func jsonStringMap(values map[string]string) (string, error) {
	if values == nil {
		values = map[string]string{}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("marshal string map: %w", err)
	}
	return string(data), nil
}

func decodeStringList(value string, out *[]string) error {
	if strings.TrimSpace(value) == "" {
		*out = nil
		return nil
	}
	if err := json.Unmarshal([]byte(value), out); err != nil {
		return fmt.Errorf("decode string list: %w", err)
	}
	if len(*out) == 0 {
		*out = nil
	}
	return nil
}

func decodeStringMap(value string, out *map[string]string) error {
	if strings.TrimSpace(value) == "" {
		*out = nil
		return nil
	}
	if err := json.Unmarshal([]byte(value), out); err != nil {
		return fmt.Errorf("decode string map: %w", err)
	}
	if len(*out) == 0 {
		*out = nil
	}
	return nil
}

func nullInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func changed(result sql.Result) int64 {
	if result == nil {
		return 0
	}
	n, _ := result.RowsAffected()
	return n
}

// DocumentChange is one durable incremental-compilation input.
type DocumentChange struct {
	BaseID             string
	DocumentID         string
	ChangeType         string
	IndexGeneration    int64
	SourceVersion      int64
	ContentHash        string
	CreatedAt          int64
	UpdatedAt          int64
	ResolvedGeneration int64
}

// MarkDocumentChanged records an unresolved update or delete. The row is keyed
// per document, so repeated changes collapse to the latest observable state.
func (s *Store) MarkDocumentChanged(ctx context.Context, change DocumentChange) error {
	if change.ChangeType != "updated" && change.ChangeType != "deleted" {
		return fmt.Errorf("unsupported semantic document change %q", change.ChangeType)
	}
	if strings.TrimSpace(change.BaseID) == "" || strings.TrimSpace(change.DocumentID) == "" {
		return fmt.Errorf("semantic document change lacks base/document identity")
	}
	if change.CreatedAt <= 0 || change.UpdatedAt <= 0 {
		return fmt.Errorf("semantic document change %s lacks timestamps", change.DocumentID)
	}
	_, err := s.db.ExecPriority(ctx, storage.NormalWrite, `INSERT INTO knowledge_compilation_queue
		(base_id, doc_id, change_type, index_generation, source_version, content_hash,
		 created_at, updated_at, resolved_generation)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(base_id, doc_id) DO UPDATE SET
		  change_type = excluded.change_type,
		  index_generation = excluded.index_generation,
		  source_version = excluded.source_version,
		  content_hash = excluded.content_hash,
		  updated_at = excluded.updated_at,
		  resolved_generation = NULL`,
		change.BaseID, change.DocumentID, change.ChangeType, change.IndexGeneration,
		change.SourceVersion, change.ContentHash, change.CreatedAt, change.UpdatedAt)
	return err
}

// DeleteBase removes every semantic compilation and queue record for a base.
// It is used only after base-scoped evidence cleanup has completed.
func (s *Store) DeleteBase(ctx context.Context, baseID string) error {
	return s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_unit_derived_from
			WHERE unit_id IN (SELECT id FROM knowledge_units WHERE base_id = ?)
			   OR derived_unit_id IN (SELECT id FROM knowledge_units WHERE base_id = ?)`,
			baseID, baseID); err != nil {
			return err
		}
		simpleStatements := []string{
			`DELETE FROM knowledge_unit_sources WHERE unit_id IN
				(SELECT id FROM knowledge_units WHERE base_id = ?)`,
			`DELETE FROM knowledge_relation_sources WHERE relation_id IN
				(SELECT id FROM knowledge_relations WHERE base_id = ?)`,
			`DELETE FROM knowledge_relations WHERE base_id = ?`,
			`DELETE FROM knowledge_units WHERE base_id = ?`,
			`DELETE FROM knowledge_compilations WHERE base_id = ?`,
			`DELETE FROM knowledge_compilation_queue WHERE base_id = ?`,
		}
		for _, statement := range simpleStatements {
			if _, err := tx.ExecContext(ctx, statement, baseID); err != nil {
				return err
			}
		}
		return nil
	})
}

// PendingDocumentChanges returns the unresolved changes in deterministic
// document order. It is the restart-recovery work list for a base.
func (s *Store) PendingDocumentChanges(ctx context.Context, baseID string) ([]DocumentChange, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT base_id, doc_id, change_type,
		index_generation, source_version, content_hash, created_at, updated_at,
		COALESCE(resolved_generation, 0)
		FROM knowledge_compilation_queue
		WHERE base_id = ? AND resolved_generation IS NULL ORDER BY doc_id`, baseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DocumentChange
	for rows.Next() {
		var change DocumentChange
		if err := rows.Scan(&change.BaseID, &change.DocumentID, &change.ChangeType,
			&change.IndexGeneration, &change.SourceVersion, &change.ContentHash,
			&change.CreatedAt, &change.UpdatedAt, &change.ResolvedGeneration); err != nil {
			return nil, err
		}
		out = append(out, change)
	}
	return out, rows.Err()
}

// ResolveDocumentChanges marks every pending row consumed by a generation.
func (s *Store) ResolveDocumentChanges(ctx context.Context, baseID string, generation, resolvedAt int64) error {
	if generation <= 0 || resolvedAt <= 0 {
		return fmt.Errorf("semantic resolution requires generation and timestamp")
	}
	_, err := s.db.ExecPriority(ctx, storage.NormalWrite, `UPDATE knowledge_compilation_queue
		SET resolved_generation = ?, updated_at = ?
		WHERE base_id = ? AND resolved_generation IS NULL`,
		generation, resolvedAt, baseID)
	return err
}
