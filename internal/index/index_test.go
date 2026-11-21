package index

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tlmcguire/aisai/internal/llm"
	"github.com/tlmcguire/aisai/internal/model"
)

// fakeProvider is an llm.Provider that returns fixed embeddings.
type fakeProvider struct {
	mu      sync.Mutex
	calls   int
	batches [][]string
	err     error
	vectors map[string][]float32
}

func (f *fakeProvider) ID() string   { return "fake" }
func (f *fakeProvider) Kind() string { return "fake" }

func (f *fakeProvider) Chat(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{Delta: ""}
	close(ch)
	return ch, nil
}

func (f *fakeProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.Completion, error) {
	return &llm.Completion{Text: "{}"}, nil
}

func (f *fakeProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls++
	f.batches = append(f.batches, append([]string(nil), texts...))
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		if v, ok := f.vectors[t]; ok {
			out = append(out, v)
			continue
		}
		out = append(out, []float32{0, 1})
	}
	return out, nil
}

func (f *fakeProvider) Models(ctx context.Context) ([]string, error) { return nil, nil }

func (f *fakeProvider) Health(ctx context.Context) error { return nil }

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// openTestIndex opens an index in a temporary directory.
func openTestIndex(t *testing.T) *Index {
	t.Helper()
	ix, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

// testChunks returns two chunks with distinct vocabulary.
func testChunks() []model.Chunk {
	return []model.Chunk{
		{
			ID:         "c1",
			File:       "pkg/auth.py",
			StartLine:  10,
			EndLine:    20,
			Type:       model.ChunkFunc,
			SymbolPath: "pkg.auth.login",
			Content:    "def login(user):\n    return check_password_hash(user, 'alpha beta')",
		},
		{
			ID:         "c2",
			File:       "pkg/routes.py",
			StartLine:  1,
			EndLine:    5,
			Type:       model.ChunkFunc,
			SymbolPath: "pkg.routes.index",
			Content:    "def index():\n    return 'gamma'",
		},
	}
}

func TestFTS5IsAvailable(t *testing.T) {
	ix := openTestIndex(t)
	var name string
	err := ix.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='chunk_fts'`).Scan(&name)
	if err != nil {
		t.Fatalf("FTS5 table missing: %v", err)
	}
	if name != "chunk_fts" {
		t.Fatalf("table = %q", name)
	}
}

func TestAddChunksAndKeywordSearch(t *testing.T) {
	ix := openTestIndex(t)
	if err := ix.AddChunks("audit-1", testChunks()); err != nil {
		t.Fatalf("AddChunks: %v", err)
	}

	hits, keywordOnly, err := ix.Search("audit-1", "password hash", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !keywordOnly {
		t.Fatal("expected keyword-only search before embedding")
	}
	if len(hits) == 0 {
		t.Fatal("expected a hit")
	}
	if hits[0].Chunk.ID != "c1" {
		t.Fatalf("top hit = %s, want c1", hits[0].Chunk.ID)
	}
	if hits[0].Chunk.SymbolPath != "pkg.auth.login" {
		t.Fatalf("symbol = %q", hits[0].Chunk.SymbolPath)
	}
}

func TestEmbedBatchesAndCachesVectors(t *testing.T) {
	ix := openTestIndex(t)
	if err := ix.AddChunks("audit-1", testChunks()); err != nil {
		t.Fatalf("AddChunks: %v", err)
	}
	provider := &fakeProvider{vectors: map[string][]float32{
		"def login(user):\n    return check_password_hash(user, 'alpha beta')": {1, 0},
		"def index():\n    return 'gamma'":                                     {0, 1},
	}}

	var lastDone, lastTotal int
	err := ix.Embed(context.Background(), "audit-1", provider, "embed-model", func(done, total int) {
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1 batched call", provider.callCount())
	}
	if lastDone != 2 || lastTotal != 2 {
		t.Fatalf("progress = (%d,%d), want (2,2)", lastDone, lastTotal)
	}

	// Re-indexing unchanged content must not call the provider again.
	if err := ix.AddChunks("audit-1", testChunks()); err != nil {
		t.Fatalf("AddChunks: %v", err)
	}
	if err := ix.Embed(context.Background(), "audit-1", provider, "embed-model", nil); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want the cached vectors to be reused", provider.callCount())
	}

	st, err := ix.Status("audit-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Ready || st.Chunks != 2 || st.Embedded != 2 {
		t.Fatalf("status = %+v", st)
	}
	if st.KeywordOnly {
		t.Fatal("expected embedded status")
	}
	if st.Phase != PhaseComplete {
		t.Fatalf("phase = %q", st.Phase)
	}
}

func TestSearchFusesKeywordAndVectorRankings(t *testing.T) {
	ix := openTestIndex(t)
	if err := ix.AddChunks("audit-1", testChunks()); err != nil {
		t.Fatalf("AddChunks: %v", err)
	}
	provider := &fakeProvider{vectors: map[string][]float32{
		"def login(user):\n    return check_password_hash(user, 'alpha beta')": {1, 0},
		"def index():\n    return 'gamma'":                                     {0, 1},
		"synonym":                                                              {1, 0},
	}}
	if err := ix.Embed(context.Background(), "audit-1", provider, "embed-model", nil); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	// "synonym" appears in no chunk: only the vector ranking can find it.
	hits, keywordOnly, err := ix.Search("audit-1", "synonym", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if keywordOnly {
		t.Fatal("expected a hybrid search")
	}
	if len(hits) == 0 {
		t.Fatal("expected a hit from the vector ranking")
	}
	if hits[0].Chunk.ID != "c1" {
		t.Fatalf("top hit = %s, want c1", hits[0].Chunk.ID)
	}
	if hits[0].Score <= 0 {
		t.Fatalf("score = %v", hits[0].Score)
	}
}

func TestEmbedFailureIsReportedAndLeavesKeywordSearchUsable(t *testing.T) {
	ix := openTestIndex(t)
	if err := ix.AddChunks("audit-1", testChunks()); err != nil {
		t.Fatalf("AddChunks: %v", err)
	}
	provider := &fakeProvider{err: errors.New("provider down")}

	err := ix.Embed(context.Background(), "audit-1", provider, "embed-model", nil)
	if err == nil {
		t.Fatal("expected the embedding failure to be returned")
	}

	st, err := ix.Status("audit-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.KeywordOnly {
		t.Fatal("expected keyword-only status after the failure")
	}
	if st.Embedded != 0 {
		t.Fatalf("embedded = %d", st.Embedded)
	}

	hits, keywordOnly, err := ix.Search("audit-1", "password", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !keywordOnly || len(hits) == 0 {
		t.Fatalf("keyword search broke: keywordOnly=%v hits=%d", keywordOnly, len(hits))
	}
}

func TestSecretFilesAreNotIndexed(t *testing.T) {
	ix := openTestIndex(t)
	chunks := []model.Chunk{
		{ID: "s1", File: "pkg/.env", Content: "SECRET=1"},
		{ID: "s2", File: "pkg/key.pem", Content: "-----BEGIN-----"},
		{ID: "s3", File: "pkg/app.py", Content: "print('hello')"},
	}
	if err := ix.AddChunks("audit-1", chunks); err != nil {
		t.Fatalf("AddChunks: %v", err)
	}
	st, err := ix.Status("audit-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Chunks != 1 {
		t.Fatalf("chunks = %d, want 1", st.Chunks)
	}
}

func TestCosineAndVectorRoundTrip(t *testing.T) {
	vec := []float32{1, 2, 3}
	got, err := decodeVector(encodeVector(vec))
	if err != nil {
		t.Fatalf("decodeVector: %v", err)
	}
	if len(got) != 3 || got[2] != 3 {
		t.Fatalf("round trip = %v", got)
	}
	if c := cosine([]float32{1, 0}, []float32{1, 0}); c < 0.999 {
		t.Fatalf("cosine = %v", c)
	}
	if c := cosine([]float32{1, 0}, []float32{0, 1}); c != 0 {
		t.Fatalf("cosine = %v", c)
	}
	if c := cosine([]float32{0, 0}, []float32{1, 0}); c != 0 {
		t.Fatalf("cosine = %v", c)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("expected an error")
	}
}

var _ llm.Provider = (*fakeProvider)(nil)
