package knowledge

// ReconcileStorage repairs two startup-time drift cases: raw files no longer
// referenced by a document, and document chunk-count metadata that diverged
// from the actual chunk table.
func (s *Service) ReconcileStorage() (removedRaw int, fixedCounts int, err error) {
	docs, err := s.store.listAllDocuments()
	if err != nil {
		return 0, 0, err
	}
	referenced := make(map[string]bool, len(docs))
	for _, doc := range docs {
		if doc.RawFilePath != "" {
			referenced[doc.RawFilePath] = true
		}
	}
	raws, err := s.raw.ListAll()
	if err != nil {
		return 0, 0, err
	}
	for _, rel := range raws {
		if referenced[rel] {
			continue
		}
		if err := s.raw.Delete(rel); err != nil {
			return removedRaw, fixedCounts, err
		}
		removedRaw++
	}

	for _, doc := range docs {
		chunks, err := s.store.listChunksByDoc(doc.ID, 0, 0)
		if err != nil {
			return removedRaw, fixedCounts, err
		}
		if doc.ChunkCount == len(chunks) {
			continue
		}
		doc.ChunkCount = len(chunks)
		doc.UpdatedAt = now()
		if err := s.store.putDocument(doc); err != nil {
			return removedRaw, fixedCounts, err
		}
		fixedCounts++
	}
	return removedRaw, fixedCounts, nil
}
