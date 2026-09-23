package knowledge

import (
	"context"

	"github.com/shutu-ai/shutu-knowledge/internal/schemamodel"
)

// SearchSchema activates generic schema entities independently of evidence
// chunks. It is the stable facade used by the HTTP and Extension API layers.
func (s *Service) SearchSchema(ctx context.Context, baseID, query string, topK int) (schemamodel.SchemaSearchResponse, error) {
	models, err := schemamodel.NewStore(s.store.db).ActiveModels(ctx, baseID)
	if err != nil {
		return schemamodel.SchemaSearchResponse{}, err
	}
	return schemamodel.SearchModels(models, query, topK), nil
}

// ResolveDataRequirement resolves business language to schema candidates and
// explicitly stops before SQL generation.
func (s *Service) ResolveDataRequirement(ctx context.Context, baseID, query string, maxPerConcept, maxTables int) (schemamodel.ResolvedRequirement, error) {
	models, err := schemamodel.NewStore(s.store.db).ActiveModels(ctx, baseID)
	if err != nil {
		return schemamodel.ResolvedRequirement{}, err
	}
	return schemamodel.ResolveDataRequirement(models, query, maxPerConcept, maxTables), nil
}

// GetSchemaTable returns one logical table and its owned fields.
func (s *Service) GetSchemaTable(ctx context.Context, baseID, tableID string) (schemamodel.LogicalTable, []schemamodel.Field, error) {
	models, err := schemamodel.NewStore(s.store.db).ActiveModels(ctx, baseID)
	if err != nil {
		return schemamodel.LogicalTable{}, nil, err
	}
	for _, model := range models {
		if table, ok := model.TableByID(tableID); ok {
			return table, model.FieldsByID(tableID), nil
		}
	}
	return schemamodel.LogicalTable{}, nil, ErrNotFound
}

// GetSchemaField returns one scoped field; field ID is never just a name.
func (s *Service) GetSchemaField(ctx context.Context, baseID, fieldID string) (schemamodel.Field, error) {
	models, err := schemamodel.NewStore(s.store.db).ActiveModels(ctx, baseID)
	if err != nil {
		return schemamodel.Field{}, err
	}
	for _, model := range models {
		for _, field := range model.Fields {
			if field.ID == fieldID {
				return field, nil
			}
		}
	}
	return schemamodel.Field{}, ErrNotFound
}
