package index

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/tlmcguire/aisai/internal/model"
)

// rrfK is the constant of the Reciprocal Rank Fusion formula
// RRF(d) = sum over rankings of 1 / (k + rank(d)).
const rrfK = 60.0

// candidatePoolFactor multiplies the requested limit to build the candidate
// pool that is fused and then trimmed.
const candidatePoolFactor = 5

// ftsQuery converts a natural-language query into a safe FTS5 MATCH
// expression. Only alphanumeric tokens are kept, so that punctuation in the
// question can never be interpreted as FTS5 syntax.
func ftsQuery(query string) string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	seen := map[string]bool{}
	var parts []string
	for _, f := range fields {
		f = strings.ToLower(f)
		if len(f) < 2 || seen[f] {
			continue
		}
		seen[f] = true
		parts = append(parts, `"`+f+`"`)
	}
	return strings.Join(parts, " OR ")
}

// keywordSearch returns the chunk identifiers matching the query, best first.
func (ix *Index) keywordSearch(auditID, query string, limit int) ([]string, error) {
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}
	rows, err := ix.db.Query(`SELECT f.chunk_id
		FROM chunk_fts f
		JOIN chunks c ON c.chunk_id = f.chunk_id
		WHERE chunk_fts MATCH ? AND c.audit_id = ?
		ORDER BY bm25(chunk_fts)
		LIMIT ?`, match, auditID, limit)
	if err != nil {
		return nil, fmt.Errorf("index: keyword search: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("index: scan keyword hit: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index: keyword search: %w", err)
	}
	return ids, nil
}

// vectorSearch returns the chunk identifiers closest to the query vector,
// best first.
func (ix *Index) vectorSearch(auditID string, queryVec []float32, limit int) ([]string, error) {
	rows, err := ix.db.Query(`SELECT v.chunk_id, v.dim, v.vec
		FROM chunk_vec v
		JOIN chunks c ON c.chunk_id = v.chunk_id
		WHERE c.audit_id = ?`, auditID)
	if err != nil {
		return nil, fmt.Errorf("index: load vectors: %w", err)
	}
	defer rows.Close()

	type scored struct {
		id    string
		score float64
	}
	var list []scored
	for rows.Next() {
		var id string
		var dim int
		var blob []byte
		if err := rows.Scan(&id, &dim, &blob); err != nil {
			return nil, fmt.Errorf("index: scan vector: %w", err)
		}
		vec, err := decodeVector(blob)
		if err != nil {
			log.Printf("index: skipping malformed vector for %s: %v", id, err)
			continue
		}
		list = append(list, scored{id: id, score: cosine(queryVec, vec)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index: load vectors: %w", err)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	if len(list) > limit {
		list = list[:limit]
	}
	ids := make([]string, 0, len(list))
	for _, s := range list {
		ids = append(ids, s.id)
	}
	return ids, nil
}

// vectorCount reports how many chunks of the audit have a stored vector.
func (ix *Index) vectorCount(auditID string) (int, error) {
	var n int
	err := ix.db.QueryRow(`SELECT COUNT(*) FROM chunk_vec v JOIN chunks c ON c.chunk_id = v.chunk_id WHERE c.audit_id = ?`, auditID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("index: count vectors: %w", err)
	}
	return n, nil
}

// embedQuery embeds the query text with the provider used for the stored
// vectors. It returns nil when no provider is available.
func (ix *Index) embedQuery(ctx context.Context, query string) ([]float32, error) {
	provider := ix.currentProvider()
	if provider == nil {
		return nil, nil
	}
	vecs, err := provider.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, errors.New("index: empty query embedding")
	}
	return vecs[0], nil
}

// Search performs hybrid retrieval over the chunks of one audit.
//
// The BM25 ranking and the cosine ranking are fused with Reciprocal Rank
// Fusion (k=60). When the audit has no vectors, or the query cannot be
// embedded, retrieval falls back to keyword-only and keywordOnly is true.
func (ix *Index) Search(auditID, query string, limit int) ([]Hit, bool, error) {
	if strings.TrimSpace(auditID) == "" {
		return nil, false, errors.New("index: empty audit id")
	}
	if limit <= 0 {
		limit = 10
	}
	pool := limit * candidatePoolFactor
	if pool < 50 {
		pool = 50
	}

	keywordIDs, err := ix.keywordSearch(auditID, query, pool)
	if err != nil {
		return nil, false, err
	}

	vectorCount, err := ix.vectorCount(auditID)
	if err != nil {
		return nil, false, err
	}
	keywordOnly := vectorCount == 0

	var vectorIDs []string
	if !keywordOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		queryVec, err := ix.embedQuery(ctx, query)
		cancel()
		if err != nil {
			// The semantic side degrades to keyword-only; the failure is
			// logged because it is a provider problem, not a user error.
			log.Printf("index: query embedding failed for audit %s, using keyword-only search: %v", auditID, err)
			keywordOnly = true
		} else {
			vectorIDs, err = ix.vectorSearch(auditID, queryVec, pool)
			if err != nil {
				return nil, false, err
			}
		}
	}

	// Reciprocal Rank Fusion over the two rankings.
	scores := map[string]float64{}
	addRanking := func(ids []string) {
		for i, id := range ids {
			scores[id] += 1 / (rrfK + float64(i+1))
		}
	}
	addRanking(keywordIDs)
	addRanking(vectorIDs)

	if len(scores) == 0 {
		return nil, keywordOnly, nil
	}

	type fused = fusedChunk
	fusedList := make([]fused, 0, len(scores))
	for id, s := range scores {
		fusedList = append(fusedList, fused{id: id, score: s})
	}
	sort.SliceStable(fusedList, func(i, j int) bool {
		if fusedList[i].score == fusedList[j].score {
			return fusedList[i].id < fusedList[j].id
		}
		return fusedList[i].score > fusedList[j].score
	})
	if len(fusedList) > limit {
		fusedList = fusedList[:limit]
	}

	chunks, err := ix.loadChunks(fusedList)
	if err != nil {
		return nil, keywordOnly, err
	}
	hits := make([]Hit, 0, len(fusedList))
	for _, f := range fusedList {
		c, ok := chunks[f.id]
		if !ok {
			continue
		}
		hits = append(hits, Hit{Chunk: c, Score: f.score})
	}
	return hits, keywordOnly, nil
}

// fusedChunk is an identifier paired with its fused score.
type fusedChunk struct {
	id    string
	score float64
}

// loadChunks loads the chunks of the given identifiers.
func (ix *Index) loadChunks(items []fusedChunk) (map[string]model.Chunk, error) {
	if len(items) == 0 {
		return map[string]model.Chunk{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(items)), ",")
	args := make([]any, 0, len(items))
	for _, it := range items {
		args = append(args, it.id)
	}
	rows, err := ix.db.Query(`SELECT chunk_id, audit_id, file, start_line, end_line, chunk_type,
		symbol_path, docstring, content, parent_id, calls, imports, language
		FROM chunks WHERE chunk_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("index: load chunks: %w", err)
	}
	defer rows.Close()

	out := map[string]model.Chunk{}
	for rows.Next() {
		var c model.Chunk
		var calls, imports string
		if err := rows.Scan(&c.ID, &c.AuditID, &c.File, &c.StartLine, &c.EndLine, &c.Type,
			&c.SymbolPath, &c.Docstring, &c.Content, &c.ParentID, &calls, &imports, &c.Language); err != nil {
			return nil, fmt.Errorf("index: scan chunk: %w", err)
		}
		if calls != "" {
			_ = json.Unmarshal([]byte(calls), &c.Calls)
		}
		if imports != "" {
			_ = json.Unmarshal([]byte(imports), &c.Imports)
		}
		out[c.ID] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index: load chunks: %w", err)
	}
	return out, nil
}
