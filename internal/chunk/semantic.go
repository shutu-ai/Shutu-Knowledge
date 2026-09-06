package chunk

import "math"

// MergedSegment is one semantic chunk: the merged text with its derived
// vector (length-weighted mean of the segment vectors, renormalized), so
// semantic chunking costs no extra embedding pass.
type MergedSegment struct {
	Piece
	Vector []float64
}

// MergeSemanticSegments greedily merges adjacent embedded segments while
// (a) their cosine similarity stays at or above `threshold` (semantically
// coherent) and (b) the combined length remains within the token budget
// converted to characters via the document's measured chars-per-token ratio.
// Segments without vectors merge only while they fit the budget.
func MergeSemanticSegments(segments []Piece, vectors [][]float64, size int, threshold float64) []MergedSegment {
	if len(segments) == 0 {
		return nil
	}
	if size < 64 {
		size = 64
	}
	if threshold < 0 || threshold > 1 {
		threshold = 0.75
	}
	full := make([]string, 0, len(segments))
	for _, s := range segments {
		full = append(full, s.Text)
	}
	cpt := CharsPerToken(joinStrings(full, "\n"))
	safeSize := maxInt(64, int(math.Round(float64(size)*cpt)))

	out := make([]MergedSegment, 0, len(segments))
	current := MergedSegment{Piece: segments[0]}
	if len(vectors) > 0 {
		current.Vector = vectors[0]
	}
	weight := float64(len(current.Text))
	flush := func() {
		out = append(out, current)
	}
	for i := 1; i < len(segments); i++ {
		segment := segments[i]
		var vector []float64
		if i < len(vectors) {
			vector = vectors[i]
		}
		similar := true
		if current.Vector != nil && vector != nil {
			similar = cosineSimilarity(current.Vector, vector) >= threshold
		}
		if len(current.Text)+len(segment.Text)+1 <= safeSize && similar {
			current.Text += "\n" + segment.Text
			if current.Vector != nil && vector != nil {
				newWeight := weight + float64(len(segment.Text))
				current.Vector = weightedMean(current.Vector, weight, vector, float64(len(segment.Text)))
				weight = newWeight
			}
		} else {
			flush()
			current = MergedSegment{Piece: segment}
			current.Vector = vector
			weight = float64(len(segment.Text))
		}
	}
	flush()
	return out
}

// cosineSimilarity over equal-length vectors (true cosine; NaN-safe).
func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	norm := math.Sqrt(normA) * math.Sqrt(normB)
	if norm == 0 {
		return 0
	}
	cosine := dot / norm
	if math.IsNaN(cosine) {
		return 0
	}
	if cosine < 0 {
		return 0
	}
	if cosine > 1 {
		return 1
	}
	return cosine
}

// weightedMean = normalize(a*aw + b*bw); zero-norm inputs pass through.
func weightedMean(a []float64, aw float64, b []float64, bw float64) []float64 {
	out := make([]float64, len(a))
	var sum float64
	for i := range a {
		v := (a[i]*aw + b[i]*bw) / (aw + bw)
		out[i] = v
		sum += v * v
	}
	length := math.Sqrt(sum)
	if math.IsNaN(length) || length == 0 {
		return out
	}
	for i := range out {
		out[i] /= length
	}
	return out
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += sep
		}
		out += part
	}
	return out
}
