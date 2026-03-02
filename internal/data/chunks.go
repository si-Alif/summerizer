package data

import (
	"database/sql"
	"time"

	"github.com/si-Alif/summerizer/internal/validator"
)


type Chunk struct {
	ID         int64     `json:"id"`
	SourceID   int64     `json:"source_id"`
	ChunkIndex int       `json:"chunk_index"`
	Content    string    `json:"content"`
	TokenCount int       `json:"token_count"`
	Embedding  []float32 `json:"-"`        // never returned in API responses
	Metadata   JsonMap   `json:"metadata"`
	CreatedAt  time.Time `json:"created_at"`
}

// ChunkSearchResult is the shape returned by vector similarity queries.
// It includes the distance score and denormalized source info so the
// handler doesn't need a second query.
type ChunkSearchResult struct {
	ChunkID     int64   `json:"chunk_id"`
	Content     string  `json:"content"`
	Distance    float64 `json:"distance"` // cosine distance — lower = more similar
	SourceURL   string  `json:"source_url"`
	SourceTitle string  `json:"source_title"`
	Metadata    JsonMap `json:"metadata"`
}

// ValidateChunk ensures a chunk produced by the pipeline meets basic
// integrity requirements before being bulk-inserted.
func ValidateChunk(v *validator.Validator, chunk *Chunk) {
	v.Check(chunk.SourceID > 0, "source_id", "must be a positive integer")
	v.Check(chunk.ChunkIndex >= 0, "chunk_index", "must be zero or positive")
	v.Check(chunk.Content != "", "content", "must not be empty")
	v.Check(len(chunk.Content) <= 50_000, "content", "must not exceed 50,000 characters")
	v.Check(chunk.TokenCount > 0, "token_count", "must be a positive integer")
	v.Check(chunk.TokenCount <= 2000, "token_count", "must not exceed 2000 tokens")
}

// ValidateChunkBatch runs validation across a slice of chunks.
// Returns a validator — check v.Valid() to see if the batch is acceptable.
func ValidateChunkBatch(chunks []*Chunk) *validator.Validator {
	v := validator.New()
	v.Check(len(chunks) > 0, "chunks", "must contain at least one chunk")
	v.Check(len(chunks) <= 5000, "chunks", "must not exceed 5000 chunks per source")

	for i, chunk := range chunks {
		if i >= 3 && !v.Valid() {
			break // stop early after seeing errors in the first few
		}
		ValidateChunk(v, chunk)
	}
	return v
}

// ChunkModel wraps the DB connection pool and provides
// all query methods for the chunks table.
type ChunkModel struct {
	DB *sql.DB
}
