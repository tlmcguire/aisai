// Package index implements hybrid retrieval over the chunks of one audited
// package.
//
// The corpus is the audited package only: an index is built per audit and
// discarded with it. Retrieval combines two retrievers:
//
//   - a keyword retriever backed by SQLite FTS5 (BM25 ranking), and
//   - a semantic retriever over stored embedding vectors (cosine similarity).
//
// The two rankings are fused with Reciprocal Rank Fusion, which needs no
// score calibration and degrades gracefully when embeddings are unavailable:
// in that case retrieval falls back to keyword-only and reports it.
//
// No vector database is used. An audited package holds 1k-20k chunks, and a
// brute-force cosine scan over that many vectors takes about 15 ms in Go.
package index

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"

	_ "modernc.org/sqlite"
)

// embedBatchSize is the number of chunks sent to the embedding provider in a
// single request.
const embedBatchSize = 32

// Index phases reported through Status.
const (
	// PhaseEmpty means no chunks have been indexed for the audit.
	PhaseEmpty = "empty"
	// PhaseKeyword means chunks are indexed but not yet embedded.
	PhaseKeyword = "keyword"
	// PhaseEmbedding means embedding is in progress.
	PhaseEmbedding = "embedding"
	// PhaseComplete means every chunk is embedded.
	PhaseComplete = "complete"
)

// Index is a persistent retrieval index over the chunks of one or more
// audits. It is safe for concurrent use.
type Index struct {
	db   *sql.DB
	path string

	mu       sync.RWMutex
	provider llm.Provider
	model    string
}

// Hit is one retrieval result.
type Hit struct {
	// Chunk is the retrieved chunk.
	Chunk model.Chunk
	// Score is the fused Reciprocal Rank Fusion score.
	Score float64
}

// Status reports the index build state of one audit.
type Status struct {
	// Ready reports whether the audit has chunks indexed.
	Ready bool
	// Phase is one of empty, keyword, embedding or complete.
	Phase string
	// Progress is the fraction of chunks that are embedded, 0..1.
	Progress float64
	// Chunks is the number of indexed chunks.
	Chunks int
	// Embedded is the number of chunks with a stored vector.
	Embedded int
	// KeywordOnly reports that no vector is available for this audit.
	KeywordOnly bool
	// EmbedModel is the embedding model used for the stored vectors.
	EmbedModel string
}

// Open opens or creates the index database at path, creating the parent
// directory when needed.
func Open(path string) (*Index, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("index: empty database path")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("index: create directory %s: %w", dir, err)
		}
	}

	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("index: open %s: %w", path, err)
	}
	// A single writer keeps SQLite happy and avoids SQLITE_BUSY storms; the
	// index is small enough that this is not a bottleneck.
	db.SetMaxOpenConns(1)

	ix := &Index{db: db, path: path}
	if err := ix.initSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return ix, nil
}

// initSchema creates the tables and the FTS5 virtual table.
func (ix *Index) initSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS chunks (
			chunk_id     TEXT PRIMARY KEY,
			audit_id     TEXT NOT NULL,
			file         TEXT NOT NULL DEFAULT '',
			start_line   INTEGER NOT NULL DEFAULT 0,
			end_line     INTEGER NOT NULL DEFAULT 0,
			chunk_type   TEXT NOT NULL DEFAULT '',
			symbol_path  TEXT NOT NULL DEFAULT '',
			docstring    TEXT NOT NULL DEFAULT '',
			content      TEXT NOT NULL DEFAULT '',
			parent_id    TEXT NOT NULL DEFAULT '',
			calls        TEXT NOT NULL DEFAULT '',
			imports      TEXT NOT NULL DEFAULT '',
			language     TEXT NOT NULL DEFAULT '',
			content_hash TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS chunks_audit_idx ON chunks(audit_id)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS chunk_fts USING fts5(
			chunk_id UNINDEXED,
			symbol_path,
			content,
			tokenize='porter unicode61'
		)`,
		`CREATE TABLE IF NOT EXISTS chunk_vec (
			chunk_id TEXT PRIMARY KEY,
			dim      INTEGER NOT NULL,
			vec      BLOB NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS embed_cache (
			content_hash TEXT NOT NULL,
			model        TEXT NOT NULL,
			dim          INTEGER NOT NULL,
			vec          BLOB NOT NULL,
			PRIMARY KEY (content_hash, model)
		)`,
		`CREATE TABLE IF NOT EXISTS index_state (
			audit_id    TEXT PRIMARY KEY,
			phase       TEXT NOT NULL DEFAULT '',
			progress    REAL NOT NULL DEFAULT 0,
			embed_model TEXT NOT NULL DEFAULT '',
			updated_at  TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, stmt := range stmts {
		if _, err := ix.db.Exec(stmt); err != nil {
			return fmt.Errorf("index: init schema: %w", err)
		}
	}
	return nil
}

// Close releases the database handle.
func (ix *Index) Close() error {
	if ix == nil || ix.db == nil {
		return nil
	}
	return ix.db.Close()
}

// secretFilePatterns are the file names that are never indexed. They hold
// credentials rather than code, and a package that ships them is reported
// through the findings pipeline instead.
var secretFilePatterns = []string{
	".env",
	".env.local",
	".env.production",
	"id_rsa",
	"id_dsa",
	"id_ecdsa",
	"id_ed25519",
}

// isSecretFile reports whether a workspace-relative path must not be indexed.
func isSecretFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	for _, p := range secretFilePatterns {
		if base == p {
			return true
		}
	}
	switch filepath.Ext(base) {
	case ".pem", ".key", ".p12", ".pfx", ".jks":
		return true
	}
	return false
}

// chunkID returns a stable identifier for a chunk.
func chunkID(c model.Chunk) string {
	if c.ID != "" {
		return c.ID
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d|%s", c.AuditID, c.File, c.StartLine, c.EndLine, c.SymbolPath)))
	return hex.EncodeToString(h[:16])
}

// contentHash returns the content hash of a chunk, used as the embedding
// cache key.
func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// AddChunks stores the chunks of one audit and populates the keyword index.
//
// Calling AddChunks again for the same audit replaces the previous chunks of
// that audit; embeddings of unchanged content are reused from the cache.
func (ix *Index) AddChunks(auditID string, chunks []model.Chunk) error {
	if strings.TrimSpace(auditID) == "" {
		return errors.New("index: empty audit id")
	}
	tx, err := ix.db.Begin()
	if err != nil {
		return fmt.Errorf("index: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Drop the previous state of this audit, FTS rows first.
	if _, err := tx.Exec(`DELETE FROM chunk_fts WHERE chunk_id IN (SELECT chunk_id FROM chunks WHERE audit_id = ?)`, auditID); err != nil {
		return fmt.Errorf("index: clear keyword index: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM chunk_vec WHERE chunk_id IN (SELECT chunk_id FROM chunks WHERE audit_id = ?)`, auditID); err != nil {
		return fmt.Errorf("index: clear vectors: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM chunks WHERE audit_id = ?`, auditID); err != nil {
		return fmt.Errorf("index: clear chunks: %w", err)
	}

	insertChunk, err := tx.Prepare(`INSERT OR REPLACE INTO chunks
		(chunk_id, audit_id, file, start_line, end_line, chunk_type, symbol_path,
		 docstring, content, parent_id, calls, imports, language, content_hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("index: prepare chunk insert: %w", err)
	}
	defer insertChunk.Close()

	insertFTS, err := tx.Prepare(`INSERT INTO chunk_fts (chunk_id, symbol_path, content) VALUES (?,?,?)`)
	if err != nil {
		return fmt.Errorf("index: prepare fts insert: %w", err)
	}
	defer insertFTS.Close()

	if err := insertChunks(tx, insertChunk, insertFTS, auditID, chunks); err != nil {
		return err
	}

	if err := ix.setStateTx(tx, auditID, PhaseKeyword, 0, ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index: commit chunks: %w", err)
	}
	return nil
}

// insertChunks writes chunks into the three tables that back the index.
func insertChunks(tx *sql.Tx, insertChunk, insertFTS *sql.Stmt, auditID string, chunks []model.Chunk) error {
	for _, c := range chunks {
		if isSecretFile(c.File) {
			continue
		}
		c.AuditID = auditID
		id := chunkID(c)
		calls, _ := json.Marshal(c.Calls)
		imports, _ := json.Marshal(c.Imports)
		if _, err := insertChunk.Exec(
			id, auditID, model.RelPath(c.File), c.StartLine, c.EndLine, c.Type,
			c.SymbolPath, c.Docstring, c.Content, c.ParentID, string(calls),
			string(imports), c.Language, contentHash(c.Content),
		); err != nil {
			return fmt.Errorf("index: insert chunk %s: %w", id, err)
		}
		if _, err := insertFTS.Exec(id, c.SymbolPath, c.Content); err != nil {
			return fmt.Errorf("index: insert fts row %s: %w", id, err)
		}
	}
	return nil
}

// ReplaceFileChunks swaps the indexed chunks of one file for a new set.
//
// It is used after a patch is applied: the old chunks of the patched file
// must be removed, or a search would keep returning the pre-patch content.
// Chunks of every other file in the audit are left alone.
func (ix *Index) ReplaceFileChunks(auditID, file string, chunks []model.Chunk) error {
	if strings.TrimSpace(auditID) == "" {
		return errors.New("index: empty audit id")
	}
	rel := model.RelPath(file)
	if rel == "" {
		return errors.New("index: empty file path")
	}

	tx, err := ix.db.Begin()
	if err != nil {
		return fmt.Errorf("index: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`DELETE FROM chunk_fts WHERE chunk_id IN (SELECT chunk_id FROM chunks WHERE audit_id = ? AND file = ?)`,
		`DELETE FROM chunk_vec WHERE chunk_id IN (SELECT chunk_id FROM chunks WHERE audit_id = ? AND file = ?)`,
		`DELETE FROM chunks WHERE audit_id = ? AND file = ?`,
	} {
		if _, err := tx.Exec(stmt, auditID, rel); err != nil {
			return fmt.Errorf("index: clear chunks of %s: %w", rel, err)
		}
	}

	insertChunk, err := tx.Prepare(`INSERT OR REPLACE INTO chunks
		(chunk_id, audit_id, file, start_line, end_line, chunk_type, symbol_path,
		 docstring, content, parent_id, calls, imports, language, content_hash)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("index: prepare chunk insert: %w", err)
	}
	defer insertChunk.Close()

	insertFTS, err := tx.Prepare(`INSERT INTO chunk_fts (chunk_id, symbol_path, content) VALUES (?,?,?)`)
	if err != nil {
		return fmt.Errorf("index: prepare fts insert: %w", err)
	}
	defer insertFTS.Close()

	if err := insertChunks(tx, insertChunk, insertFTS, auditID, chunks); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index: commit file chunks: %w", err)
	}
	return nil
}

// setStateTx records the build state of an audit inside a transaction. A nil
// transaction writes directly on the database handle.
func (ix *Index) setStateTx(tx *sql.Tx, auditID, phase string, progress float64, embedModel string) error {
	if embedModel == "" {
		embedModel = ix.currentModel()
	}
	var err error
	if tx != nil {
		_, err = tx.Exec(`INSERT INTO index_state (audit_id, phase, progress, embed_model, updated_at)
			VALUES (?,?,?,?,?)
			ON CONFLICT(audit_id) DO UPDATE SET phase=excluded.phase, progress=excluded.progress,
				embed_model=excluded.embed_model, updated_at=excluded.updated_at`,
			auditID, phase, progress, embedModel, time.Now().UTC().Format(time.RFC3339))
	} else {
		_, err = ix.db.Exec(`INSERT INTO index_state (audit_id, phase, progress, embed_model, updated_at)
			VALUES (?,?,?,?,?)
			ON CONFLICT(audit_id) DO UPDATE SET phase=excluded.phase, progress=excluded.progress,
				embed_model=excluded.embed_model, updated_at=excluded.updated_at`,
			auditID, phase, progress, embedModel, time.Now().UTC().Format(time.RFC3339))
	}
	if err != nil {
		return fmt.Errorf("index: update state: %w", err)
	}
	return nil
}

// setState records the build state of an audit.
func (ix *Index) setState(auditID, phase string, progress float64, embedModel string) error {
	return ix.setStateTx(nil, auditID, phase, progress, embedModel)
}

// currentModel returns the embedding model last used by Embed.
func (ix *Index) currentModel() string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.model
}

// currentProvider returns the provider last used by Embed.
func (ix *Index) currentProvider() llm.Provider {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.provider
}

// Embed computes and stores the vectors of the audit's chunks.
//
// The provider is called with batched input. Vectors are cached by
// (content hash, model), so re-indexing unchanged content costs no provider
// calls at all. The progress callback, when not nil, is called with
// (done, total) after every batch.
//
// When the provider fails, the error is returned and the index stays
// keyword-only; the failure is never swallowed.
func (ix *Index) Embed(ctx context.Context, auditID string, provider llm.Provider, modelName string, progress func(done, total int)) error {
	if strings.TrimSpace(auditID) == "" {
		return errors.New("index: empty audit id")
	}
	if provider == nil {
		return errors.New("index: no embedding provider configured")
	}
	if modelName == "" {
		return errors.New("index: no embedding model configured")
	}

	ix.mu.Lock()
	ix.provider = provider
	ix.model = modelName
	ix.mu.Unlock()

	type pending struct {
		id      string
		content string
		hash    string
	}

	rows, err := ix.db.Query(`SELECT chunk_id, content, content_hash FROM chunks WHERE audit_id = ?`, auditID)
	if err != nil {
		return fmt.Errorf("index: load chunks: %w", err)
	}
	var all []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.content, &p.hash); err != nil {
			rows.Close()
			return fmt.Errorf("index: scan chunk: %w", err)
		}
		all = append(all, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("index: load chunks: %w", err)
	}
	rows.Close()

	total := len(all)
	if total == 0 {
		if err := ix.setState(auditID, PhaseEmpty, 0, modelName); err != nil {
			return err
		}
		if progress != nil {
			progress(0, 0)
		}
		return nil
	}

	// Resolve cached vectors first; only the rest needs the provider.
	cached := map[string][]float32{}
	var todo []pending
	for _, p := range all {
		if vec, err := ix.cachedVector(p.hash, modelName); err != nil {
			log.Printf("index: embedding cache lookup failed for %s: %v", p.id, err)
		} else if vec != nil {
			cached[p.id] = vec
		} else {
			todo = append(todo, p)
		}
	}

	done := len(cached)
	if err := ix.storeVectors(auditID, cached); err != nil {
		return err
	}
	if progress != nil {
		progress(done, total)
	}
	if err := ix.setState(auditID, PhaseEmbedding, float64(done)/float64(total), modelName); err != nil {
		return err
	}

	for start := 0; start < len(todo); start += embedBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := start + embedBatchSize
		if end > len(todo) {
			end = len(todo)
		}
		batch := todo[start:end]
		texts := make([]string, 0, len(batch))
		for _, p := range batch {
			texts = append(texts, p.content)
		}
		vecs, err := provider.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("index: embed %d chunk(s) for audit %s: %w", len(texts), auditID, err)
		}
		if len(vecs) != len(texts) {
			return fmt.Errorf("index: embed returned %d vectors for %d chunks", len(vecs), len(texts))
		}
		stored := make(map[string][]float32, len(vecs))
		for i, p := range batch {
			if len(vecs[i]) == 0 {
				continue
			}
			stored[p.id] = vecs[i]
			if err := ix.putCachedVector(p.hash, modelName, vecs[i]); err != nil {
				log.Printf("index: cache embedding for %s failed: %v", p.id, err)
			}
		}
		if err := ix.storeVectors(auditID, stored); err != nil {
			return err
		}
		done += len(batch)
		if progress != nil {
			progress(done, total)
		}
		if err := ix.setState(auditID, PhaseEmbedding, float64(done)/float64(total), modelName); err != nil {
			return err
		}
	}

	phase := PhaseComplete
	if done < total {
		phase = PhaseEmbedding
	}
	if err := ix.setState(auditID, phase, float64(done)/float64(total), modelName); err != nil {
		return err
	}
	return nil
}

// cachedVector returns the cached vector of a content hash, or nil.
func (ix *Index) cachedVector(hash, modelName string) ([]float32, error) {
	var blob []byte
	err := ix.db.QueryRow(`SELECT vec FROM embed_cache WHERE content_hash = ? AND model = ?`, hash, modelName).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeVector(blob)
}

// putCachedVector stores a vector in the embedding cache.
func (ix *Index) putCachedVector(hash, modelName string, vec []float32) error {
	_, err := ix.db.Exec(`INSERT OR REPLACE INTO embed_cache (content_hash, model, dim, vec) VALUES (?,?,?,?)`,
		hash, modelName, len(vec), encodeVector(vec))
	return err
}

// storeVectors writes the vectors of the given chunks.
func (ix *Index) storeVectors(auditID string, vecs map[string][]float32) error {
	if len(vecs) == 0 {
		return nil
	}
	tx, err := ix.db.Begin()
	if err != nil {
		return fmt.Errorf("index: begin vector transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO chunk_vec (chunk_id, dim, vec) VALUES (?,?,?)`)
	if err != nil {
		return fmt.Errorf("index: prepare vector insert: %w", err)
	}
	defer stmt.Close()

	for id, vec := range vecs {
		if len(vec) == 0 {
			continue
		}
		if _, err := stmt.Exec(id, len(vec), encodeVector(vec)); err != nil {
			return fmt.Errorf("index: store vector %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index: commit vectors: %w", err)
	}
	return nil
}

// Status reports the index build state of one audit.
func (ix *Index) Status(auditID string) (Status, error) {
	var chunks int
	if err := ix.db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE audit_id = ?`, auditID).Scan(&chunks); err != nil {
		return Status{}, fmt.Errorf("index: count chunks: %w", err)
	}
	var embedded int
	err := ix.db.QueryRow(`SELECT COUNT(*) FROM chunk_vec v JOIN chunks c ON c.chunk_id = v.chunk_id WHERE c.audit_id = ?`, auditID).Scan(&embedded)
	if err != nil {
		return Status{}, fmt.Errorf("index: count vectors: %w", err)
	}

	var phase, embedModel string
	var progress float64
	err = ix.db.QueryRow(`SELECT phase, progress, embed_model FROM index_state WHERE audit_id = ?`, auditID).
		Scan(&phase, &progress, &embedModel)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Status{}, fmt.Errorf("index: read state: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		phase = PhaseEmpty
	}

	st := Status{
		Ready:       chunks > 0,
		Phase:       phase,
		Chunks:      chunks,
		Embedded:    embedded,
		KeywordOnly: embedded == 0,
		EmbedModel:  embedModel,
	}
	if chunks > 0 {
		st.Progress = float64(embedded) / float64(chunks)
	}
	if st.Phase == "" {
		switch {
		case chunks == 0:
			st.Phase = PhaseEmpty
		case embedded == 0:
			st.Phase = PhaseKeyword
		case embedded < chunks:
			st.Phase = PhaseEmbedding
		default:
			st.Phase = PhaseComplete
		}
	}
	return st, nil
}
