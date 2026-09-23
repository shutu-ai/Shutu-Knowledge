package schemamodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// Store owns the additive SQLite projection for schema entities.
type Store struct {
	db *storage.DB
}

// NewStore returns a schema-store adapter over the existing database.
func NewStore(db *storage.DB) *Store { return &Store{db: db} }

// ReplaceDocumentModel atomically publishes one document generation. An empty
// model is still published as explicit `absent`, preserving migration/on-demand
// behavior and making a prior accidental model impossible to mix with new rows.
func (s *Store) ReplaceDocumentModel(ctx context.Context, input DocumentInput, model Model, compileMS int64, detected bool) error {
	if input.BaseID == "" || input.DocumentID == "" || input.Generation <= 0 {
		return fmt.Errorf("schema replace requires base/document/generation")
	}
	model.BaseID = input.BaseID
	model.Generation = input.Generation
	if err := model.Validate(); detected && err != nil {
		return fmt.Errorf("schema model validation: %w", err)
	}
	now := time.Now().Unix()
	return s.db.WriteTx(ctx, storage.MaintenanceWrite, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_tables WHERE base_id = ? AND doc_id = ?
			AND generation <> ?`, input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_fields WHERE base_id = ? AND doc_id = ?
			AND generation <> ?`, input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_enums WHERE base_id = ? AND doc_id = ?
			AND generation <> ?`, input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_key_candidates WHERE base_id = ? AND doc_id = ?
			AND generation <> ?`, input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_entities WHERE base_id = ? AND doc_id = ?
			AND generation <> ?`, input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_generations WHERE base_id = ? AND doc_id = ?
			AND generation <> ?`, input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_tables WHERE base_id = ? AND doc_id = ? AND generation = ?`,
			input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_fields WHERE base_id = ? AND doc_id = ? AND generation = ?`,
			input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_enums WHERE base_id = ? AND doc_id = ? AND generation = ?`,
			input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_key_candidates WHERE base_id = ? AND doc_id = ? AND generation = ?`,
			input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_entities WHERE base_id = ? AND doc_id = ? AND generation = ?`,
			input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_concept_fields WHERE base_id = ? AND doc_id = ? AND generation = ?`,
			input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_concepts WHERE base_id = ? AND doc_id = ? AND generation = ?`,
			input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_generations WHERE base_id = ? AND doc_id = ? AND generation = ?`,
			input.BaseID, input.DocumentID, input.Generation); err != nil {
			return err
		}
		state, completedAt := "active", &now
		if !detected {
			state = "absent"
		}
		metadata, err := jsonString(map[string]any{"source_path": input.SourcePath, "sheet": input.SheetName})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_generations
			(base_id, doc_id, generation, state, compiler, compiler_version, detected,
			 table_count, field_count, enum_count, concept_count, key_count, compile_ms,
			 metadata, created_at, completed_at)
			VALUES (?, ?, ?, ?, 'builtin', 'schema-model-v1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			input.BaseID, input.DocumentID, input.Generation, state, boolInt(detected),
			len(model.Tables), len(model.Fields), len(model.Enums), len(model.Concepts), len(model.KeyCandidates),
			compileMS, metadata, now, completedAt); err != nil {
			return err
		}
		if !detected {
			return nil
		}
		for _, table := range model.Tables {
			aliases, signals, fieldIDs, scopeJSON, sourceJSON, err := tableJSON(table)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO schema_tables
				(id, base_id, doc_id, generation, name, display_name, aliases, description,
				 business_purpose, purpose_evidence, purpose_inferred, scope, region_anchor,
				 field_ids, source, confidence, detection_signals)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				table.ID, table.BaseID, table.DocumentID, table.Generation, table.Name,
				table.DisplayName, aliases, table.Description, table.BusinessPurpose,
				table.PurposeEvidence, boolInt(table.PurposeInferred), scopeJSON, table.RegionAnchor,
				fieldIDs, sourceJSON, table.Confidence, signals); err != nil {
				return err
			}
			if err := insertSchemaEntity(ctx, tx, table.BaseID, table.DocumentID, table.Generation,
				"TABLE", table.ID, table.DisplayName, tableRepresentation(table), table.Aliases,
				table.Scope.String(), "", conceptsFor(table.Name+" "+table.Description), table.Source); err != nil {
				return err
			}
		}
		for _, field := range model.Fields {
			enumID, enumRow, err := fieldEnumJSON(field)
			if err != nil {
				return err
			}
			aliases, concepts, scopeJSON, sourceJSON, err := fieldJSON(field)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO schema_fields
				(id, base_id, doc_id, generation, table_id, table_name, scope, name, display_name,
				 aliases, data_type, description, nullable, enum_id, concepts, source, confidence)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				field.ID, field.BaseID, field.DocumentID, field.Generation, field.TableID,
				field.TableName, scopeJSON, field.Name, field.DisplayName, aliases, field.DataType,
				field.Description, boolInt(field.Nullable), nullString(enumID), concepts, sourceJSON,
				field.Confidence); err != nil {
				return err
			}
			if enumRow != nil {
				values, sourceJSON, err := enumJSON(*enumRow)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO schema_enums
					(id, base_id, doc_id, generation, field_id, description, enum_values, source, confidence)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					enumRow.ID, field.BaseID, field.DocumentID, field.Generation,
					enumRow.FieldID, enumRow.Description, values, sourceJSON, enumRow.Confidence); err != nil {
					return err
				}
			}
			for _, hint := range field.KeyHints {
				evidence, err := jsonString(hint.Evidence)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO schema_key_candidates
					(id, base_id, doc_id, generation, field_id, key_type, confidence, reason, evidence)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					hint.ID, field.BaseID, field.DocumentID, field.Generation, field.ID,
					string(hint.Type), hint.Confidence, hint.Reason, evidence); err != nil {
					return err
				}
			}
			if err := insertSchemaEntity(ctx, tx, field.BaseID, field.DocumentID, field.Generation,
				"FIELD", field.ID, field.Name, fieldRepresentation(field), field.Aliases, field.Scope.String(),
				field.TableID, field.Concepts, field.Source); err != nil {
				return err
			}
		}
		for _, concept := range model.Concepts {
			aliases, _, err := conceptJSON(concept)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO schema_concepts
				(id, base_id, doc_id, generation, name, aliases, confidence) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				concept.ID, concept.BaseID, concept.DocumentID, concept.Generation, concept.Name, aliases, concept.Confidence); err != nil {
				return err
			}
			for _, fieldID := range concept.FieldIDs {
				if _, err := tx.ExecContext(ctx, `INSERT INTO schema_concept_fields
					(concept_id, field_id, base_id, doc_id, generation) VALUES (?, ?, ?, ?, ?)`,
					concept.ID, fieldID, concept.BaseID, concept.DocumentID, concept.Generation); err != nil {
					return err
				}
			}

			if err := insertSchemaEntity(ctx, tx, concept.BaseID, concept.DocumentID, concept.Generation,
				"BUSINESS_CONCEPT", concept.ID, concept.Name, concept.Name, concept.Aliases, "", "", nil, SourceRef{}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ActiveModels loads all currently published document models for a base.
func (s *Store) ActiveModels(ctx context.Context, baseID string) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT doc_id, generation FROM schema_generations
		WHERE base_id = ? AND state = 'active' AND detected = 1 ORDER BY doc_id`, baseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scope struct {
		doc        string
		generation int64
	}
	scopes := make([]scope, 0)
	for rows.Next() {
		var item scope
		if err := rows.Scan(&item.doc, &item.generation); err != nil {
			return nil, err
		}
		scopes = append(scopes, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(scopes))
	for _, item := range scopes {
		model, err := s.LoadModel(ctx, baseID, item.doc, item.generation)
		if err != nil {
			return nil, err
		}
		models = append(models, model)
	}
	return models, nil
}

// LoadModel reconstructs one active schema generation.
func (s *Store) LoadModel(ctx context.Context, baseID, docID string, generation int64) (Model, error) {
	model := Model{BaseID: baseID, Generation: generation}
	rows, err := s.db.QueryContext(ctx, `SELECT id, doc_id, generation, name, display_name, aliases,
		description, business_purpose, purpose_evidence, purpose_inferred, scope, region_anchor,
		field_ids, source, confidence, detection_signals
		FROM schema_tables WHERE base_id = ? AND doc_id = ? AND generation = ?`, baseID, docID, generation)
	if err != nil {
		return Model{}, err
	}
	for rows.Next() {
		var table LogicalTable
		var aliases, fieldIDs, rawScope, source, signals string
		var inferred int
		if err := rows.Scan(&table.ID, &table.DocumentID, &table.Generation, &table.Name,
			&table.DisplayName, &aliases, &table.Description, &table.BusinessPurpose,
			&table.PurposeEvidence, &inferred, &rawScope, &table.RegionAnchor, &fieldIDs,
			&source, &table.Confidence, &signals); err != nil {
			_ = rows.Close()
			return Model{}, err
		}
		table.PurposeInferred = inferred != 0
		table.BaseID = baseID
		_ = json.Unmarshal([]byte(aliases), &table.Aliases)
		_ = json.Unmarshal([]byte(fieldIDs), &table.FieldIDs)
		_ = json.Unmarshal([]byte(signals), &table.DetectionSignals)
		_ = json.Unmarshal([]byte(rawScope), &table.Scope)
		_ = json.Unmarshal([]byte(source), &table.Source)
		model.Tables = append(model.Tables, table)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Model{}, err
	}
	_ = rows.Close()

	fieldRows, err := s.db.QueryContext(ctx, `SELECT f.id, f.doc_id, f.generation, f.table_id,
		f.table_name, f.scope, f.name, f.display_name, f.aliases, f.data_type, f.description,
		f.nullable, f.enum_id, f.concepts, f.source, f.confidence,
		e.id, e.field_id, e.description, e.enum_values, e.source, e.confidence
		FROM schema_fields f LEFT JOIN schema_enums e
		  ON e.base_id = f.base_id AND e.doc_id = f.doc_id AND e.generation = f.generation
		 AND e.field_id = f.id
		WHERE f.base_id = ? AND f.doc_id = ? AND f.generation = ?`, baseID, docID, generation)
	if err != nil {
		return Model{}, err
	}
	defer fieldRows.Close()
	for fieldRows.Next() {
		var field Field
		var aliases, rawScope, concepts, source string
		var nullable int
		var fieldEnumID, enumID, enumFieldID, enumDescription, enumValues, enumSource sql.NullString
		var enumConfidence sql.NullFloat64
		if err := fieldRows.Scan(&field.ID, &field.DocumentID, &field.Generation, &field.TableID,
			&field.TableName, &rawScope, &field.Name, &field.DisplayName, &aliases, &field.DataType,
			&field.Description, &nullable, &fieldEnumID, &concepts, &source, &field.Confidence,
			&enumID, &enumFieldID, &enumDescription, &enumValues, &enumSource, &enumConfidence); err != nil {
			return Model{}, err
		}
		field.BaseID = baseID
		field.Nullable = nullable != 0
		_ = json.Unmarshal([]byte(aliases), &field.Aliases)
		_ = json.Unmarshal([]byte(concepts), &field.Concepts)
		_ = json.Unmarshal([]byte(rawScope), &field.Scope)
		_ = json.Unmarshal([]byte(source), &field.Source)
		if enumID.Valid && enumValues.Valid {
			enum := Enum{ID: enumID.String, FieldID: enumFieldID.String, Description: enumDescription.String, Confidence: enumConfidence.Float64}
			if err := json.Unmarshal([]byte(enumValues.String), &enum.Values); err != nil {
				return Model{}, err
			}
			if err := json.Unmarshal([]byte(enumSource.String), &enum.Source); err != nil {
				return Model{}, err
			}
			field.Enum = &enum
		}
		model.Fields = append(model.Fields, field)
	}
	if err := fieldRows.Err(); err != nil {
		return Model{}, err
	}

	keyRows, err := s.db.QueryContext(ctx, `SELECT id, doc_id, generation, field_id, key_type,
		confidence, reason, evidence FROM schema_key_candidates
		WHERE base_id = ? AND doc_id = ? AND generation = ?`, baseID, docID, generation)
	if err != nil {
		return Model{}, err
	}
	defer keyRows.Close()
	for keyRows.Next() {
		var candidate KeyCandidate
		var docIDValue string
		var generationValue int64
		var keyType, evidence string
		if err := keyRows.Scan(&candidate.ID, &docIDValue, &generationValue, &candidate.FieldID,
			&keyType, &candidate.Confidence, &candidate.Reason, &evidence); err != nil {
			return Model{}, err
		}
		candidate.Type = KeyCandidateType(keyType)
		_ = json.Unmarshal([]byte(evidence), &candidate.Evidence)
		model.KeyCandidates = append(model.KeyCandidates, candidate)
	}
	if err := keyRows.Err(); err != nil {
		return Model{}, err
	}

	conceptRows, err := s.db.QueryContext(ctx, `SELECT c.id, c.base_id, c.doc_id, c.generation, c.name,
		c.aliases, c.confidence, group_concat(cf.field_id, char(1))
		FROM schema_concepts c JOIN schema_concept_fields cf
		  ON cf.concept_id = c.id AND cf.base_id = c.base_id AND cf.generation = c.generation
		WHERE c.base_id = ? AND c.doc_id = ? AND c.generation = ? GROUP BY c.id`, baseID, docID, generation)
	if err != nil {
		return Model{}, err
	}
	defer conceptRows.Close()
	for conceptRows.Next() {
		var concept BusinessConcept
		var aliases, fieldList string
		if err := conceptRows.Scan(&concept.ID, &concept.BaseID, &concept.DocumentID, &concept.Generation, &concept.Name,
			&aliases, &concept.Confidence, &fieldList); err != nil {
			return Model{}, err
		}
		_ = json.Unmarshal([]byte(aliases), &concept.Aliases)
		concept.FieldIDs = strings.Split(fieldList, string(rune(1)))
		model.Concepts = append(model.Concepts, concept)
	}
	if err := conceptRows.Err(); err != nil {
		return Model{}, err
	}
	model.SortCanonical()
	return model, nil
}

func insertSchemaEntity(ctx context.Context, tx *sql.Tx, baseID, docID string, generation int64,
	entityType, entityID, title, content string, aliases []string, scope, tableID string,
	concepts []string, source SourceRef) error {
	aliasJSON, err := jsonString(aliases)
	if err != nil {
		return err
	}
	conceptJSON, err := jsonString(concepts)
	if err != nil {
		return err
	}
	sourceJSON, err := jsonString(source)
	if err != nil {
		return err
	}
	id := EntityID("schentity", baseID, generation, entityType+"\x00"+entityID)
	_, err = tx.ExecContext(ctx, `INSERT INTO schema_entities
		(id, base_id, doc_id, generation, entity_type, entity_id, title, content, aliases,
		 scope, table_id, concepts, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, baseID, docID, generation, entityType, entityID, title, content, aliasJSON,
		scope, nullString(tableID), conceptJSON, sourceJSON)
	return err
}

func tableJSON(table LogicalTable) (aliases, fieldIDs, scope, source, signals string, err error) {
	if aliases, err = jsonString(table.Aliases); err != nil {
		return
	}
	if fieldIDs, err = jsonString(table.FieldIDs); err != nil {
		return
	}
	if scope, err = jsonString(table.Scope); err != nil {
		return
	}
	if source, err = jsonString(table.Source); err != nil {
		return
	}
	if signals, err = jsonString(table.DetectionSignals); err != nil {
		return
	}
	return
}

func fieldJSON(field Field) (aliases, concepts, scope, source string, err error) {
	if aliases, err = jsonString(field.Aliases); err != nil {
		return
	}
	if concepts, err = jsonString(field.Concepts); err != nil {
		return
	}
	if scope, err = jsonString(field.Scope); err != nil {
		return
	}
	if source, err = jsonString(field.Source); err != nil {
		return
	}
	return
}

func fieldEnumJSON(field Field) (string, *Enum, error) {
	if field.Enum == nil {
		return "", nil, nil
	}
	return field.Enum.ID, field.Enum, nil
}

func enumJSON(enum Enum) (values, source string, err error) {
	if values, err = jsonString(enum.Values); err != nil {
		return
	}
	if source, err = jsonString(enum.Source); err != nil {
		return
	}
	return
}

func conceptJSON(concept BusinessConcept) (aliases, derivedFrom string, err error) {
	if aliases, err = jsonString(concept.Aliases); err != nil {
		return
	}
	if derivedFrom, err = jsonString(concept.DerivedFrom); err != nil {
		return
	}
	return
}

func tableRepresentation(table LogicalTable) string {
	parts := []string{"Table: " + table.Name, "Scope: " + table.Scope.String()}
	if table.Description != "" {
		parts = append(parts, "Description: "+table.Description)
	}
	if table.BusinessPurpose != "" {
		purpose := table.BusinessPurpose
		if table.PurposeInferred {
			purpose += " (inferred)"
		}
		parts = append(parts, "Purpose: "+purpose)
	}
	if len(table.FieldIDs) > 0 {
		parts = append(parts, fmt.Sprintf("Field count: %d", len(table.FieldIDs)))
	}
	return strings.Join(parts, "; ")
}

func fieldRepresentation(field Field) string {
	parts := []string{"Field: " + field.Name, "Table: " + field.TableName, "Scope: " + field.Scope.String()}
	if field.DataType != "" {
		parts = append(parts, "Type: "+field.DataType)
	}
	if field.Description != "" {
		parts = append(parts, "Meaning: "+field.Description)
	}
	if field.Enum != nil {
		values := make([]string, 0, len(field.Enum.Values))
		for _, value := range field.Enum.Values {
			if value.Label == "" {
				values = append(values, value.Value)
				continue
			}
			values = append(values, value.Value+"="+value.Label)
		}
		parts = append(parts, "Enum: "+strings.Join(values, ","))
	}
	if len(field.Concepts) > 0 {
		parts = append(parts, "Concepts: "+strings.Join(field.Concepts, ","))
	}
	return strings.Join(parts, "; ")
}

func jsonString(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
