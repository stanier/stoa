package knowledgebase

import (
	"math"
	"sort"
	"strings"
)

// SearchResult is a single ranked result from a search query.
type SearchResult struct {
	Chunk       *Chunk
	Score       float64
	BM25Score   float64
	CosineScore float64
}

// searcher performs hybrid BM25 + cosine similarity search over the index.
type searcher struct {
	index *Index
}

func newSearcher(index *Index) *searcher {
	return &searcher{index: index}
}

// Search returns the top-k chunks for query using hybrid BM25 + cosine scoring.
// bm25Weight controls the blend: 0.0 = pure semantic, 1.0 = pure BM25.
// If no embeddings are stored, falls back to BM25-only.
func (s *searcher) Search(query string, queryVec []float32, topK int, bm25Weight float64) []SearchResult {
	chunks := s.index.chunks()
	if len(chunks) == 0 {
		return nil
	}

	tokens := tokenize(query)

	// Build per-document term frequencies and corpus stats for BM25
	df := computeDF(chunks)
	avgLen := avgChunkLength(chunks)

	results := make([]SearchResult, 0, len(chunks))
	hasEmbeddings := queryVec != nil && len(queryVec) > 0

	for _, c := range chunks {
		bm25 := bm25Score(tokens, c.Content, df, len(chunks), avgLen)
		var cos float64
		if hasEmbeddings && len(c.Embedding) > 0 {
			cos = cosineSimilarity(queryVec, c.Embedding)
		}

		var score float64
		if hasEmbeddings && len(c.Embedding) > 0 {
			score = bm25Weight*normBM25(bm25) + (1-bm25Weight)*((cos+1)/2)
		} else {
			// No embeddings: BM25 only
			score = normBM25(bm25)
		}

		results = append(results, SearchResult{
			Chunk:       c,
			Score:       score,
			BM25Score:   bm25,
			CosineScore: cos,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if topK > 0 && topK < len(results) {
		results = results[:topK]
	}
	return results
}

// --- BM25 (Okapi BM25) ---

const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// termFreq counts occurrences of each token in text.
func termFreq(tokens []string, text string) map[string]int {
	docTokens := tokenize(text)
	freq := make(map[string]int, len(docTokens))
	for _, t := range docTokens {
		freq[t]++
	}
	// Return counts only for query tokens
	result := make(map[string]int, len(tokens))
	for _, t := range tokens {
		result[t] = freq[t]
	}
	return result
}

// computeDF returns document frequency (number of chunks containing each token).
func computeDF(chunks []*Chunk) map[string]int {
	df := make(map[string]int)
	for _, c := range chunks {
		seen := make(map[string]bool)
		for _, t := range tokenize(c.Content + " " + c.Heading) {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}
	return df
}

// avgChunkLength returns the average token count across all chunks.
func avgChunkLength(chunks []*Chunk) float64 {
	if len(chunks) == 0 {
		return 0
	}
	total := 0
	for _, c := range chunks {
		total += len(tokenize(c.Content))
	}
	return float64(total) / float64(len(chunks))
}

// bm25Score computes the BM25 score for a single document.
func bm25Score(queryTokens []string, docText string, df map[string]int, N int, avgLen float64) float64 {
	docTokens := tokenize(docText)
	docLen := float64(len(docTokens))

	tf := make(map[string]int, len(docTokens))
	for _, t := range docTokens {
		tf[t]++
	}

	var score float64
	for _, t := range queryTokens {
		f := float64(tf[t])
		if f == 0 {
			continue
		}
		n := float64(df[t])
		if n == 0 {
			continue
		}
		idf := math.Log((float64(N)-n+0.5)/(n+0.5) + 1)
		tfNorm := (f * (bm25K1 + 1)) / (f + bm25K1*(1-bm25B+bm25B*docLen/avgLen))
		score += idf * tfNorm
	}
	return score
}

// normBM25 maps a raw BM25 score to [0, 1] using a sigmoid-like transform.
// BM25 scores are unbounded positive; this keeps blending sensible.
func normBM25(score float64) float64 {
	if score <= 0 {
		return 0
	}
	return score / (score + 5.0)
}

// --- Cosine similarity ---

// cosineSimilarity returns the cosine similarity in [-1, 1].
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// --- Tokenizer ---

// tokenize lowercases text and splits on non-alphanumeric characters.
func tokenize(text string) []string {
	lower := strings.ToLower(text)
	var tokens []string
	var cur strings.Builder
	for _, r := range lower {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			cur.WriteRune(r)
		} else {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}
