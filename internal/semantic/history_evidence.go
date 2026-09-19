package semantic

import "fmt"

func evidenceSourceKey(source EvidenceSource) string {
	return fmt.Sprintf("%s|%s|%s|%d|%d|%d", source.DocumentID, source.ChunkID, source.NodeID, source.IndexGeneration, source.SourceVersion, source.SourceOrder)
}
