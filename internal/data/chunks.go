package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/si-Alif/summerizer/internal/validator"
)

// Batch size for bulk inserting chunks to prevent hitting PostgreSQL's parameter limit (65535)
const ChunkBulkInsertBatchSize = 500

type Chunk struct {
	ID           int64  `json:"id"`
	SourceID      int64  `json:"source_id"`
	ChunkIndex int    `json:"chunk_index"`
	Content 	 string `json:"content"`
	TokenCount int    `json:"token_count"`
	Embedding  []float64 `json:"-"`
	Metadata	 json.RawMessage `json:"metadata"`
	CreatedAt    string `json:"created_at"`
}

type ChunkSearchResult struct {
	ChunkID      int64   `json:"chunk_id"`
	Content 		string  `json:"content"`
	TokenCount 	int     `json:"token_count"`
	Distance 	 float64 `json:"distance"`
	SourceID      int64   `json:"source_id"`
	SourceTitle   string  `json:"source_title"`
	SourceURL     string  `json:"source_url"`
	ChunkIndex     int     `json:"chunk_index"`
	Metadata	 json.RawMessage `json:"metadata"`
}

func ValidateChunk(v *validator.Validator, chunk *Chunk) {
	v.Check(chunk.SourceID > 0, "source_id", "must be provided and greater than 0")
	v.Check(chunk.ChunkIndex >= 0, "chunk_index", "must be provided and non-negative")

	v.Check(validator.NotBlank(chunk.Content), "content", "must be provided")
	v.Check(len(chunk.Content) <= 50000, "content", "must not be more than 50000 characters")

	v.Check(chunk.TokenCount > 0 , "token_count", "must be greater than 0")
	v.Check(chunk.TokenCount <= 2000 , "token_count", "must not be more than 10000")
}

type ChunkModel struct {
	DB *sql.DB
}

func (m ChunkModel) BulkInsert(chunks []*Chunk) error {
	len := len(chunks)
	for i := 0; i < len; i += ChunkBulkInsertBatchSize {
		end := i + ChunkBulkInsertBatchSize
		if end > len {
			end = len
		}

		err := m.BulkInsertBatch(chunks[i:end])
		if err != nil {
			return fmt.Errorf("bulk insert chunks batch (start: %d, end: %d): %w", i, end, err)
		}
	}

	return nil
}

func (m ChunkModel) BulkInsertBatch(chunks []*Chunk) error {
	query := `INSERT INTO chunks (source_id , chunk_index , content , token_count , metadata) VALUES `

	chunks_len := len(chunks)

	valueString := make([]string , 0 , chunks_len)
	args := make([]any , 0 , chunks_len * 5)

	for i , chunk := range chunks {
		base := i * 5
		valueString = append(valueString,
			fmt.Sprintf("($%d, $%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4, base+5),
		)
		args = append(args, chunk.SourceID, chunk.ChunkIndex, chunk.Content, chunk.TokenCount, chunk.Metadata)
	}

	query += strings.Join(valueString , ", ")
	query += ` RETURNING id , created_at`

	ctx , cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows , err := m.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("bulk insert chunks: %w", err)
	}

	it := 0
	for rows.Next() {
		if it >= chunks_len {
			break
		}

		err := rows.Scan(&chunks[it].ID, &chunks[it].CreatedAt)
		if err != nil {
			return fmt.Errorf("scanning bulk insert chunks result: %w", err)
		}
		it++
	}

	return rows.Err()
}


func (m ChunkModel) DeleteBySourceID(sourceID int64) error {
	query := `DELETE FROM chunks WHERE source_id = $1`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := m.DB.ExecContext(ctx, query, sourceID)
	return err
}

func (m ChunkModel) GetBySourceID(sourceID int64) ([]*Chunk, error) {
	query := `
	SELECT id, source_id, chunk_index, content, token_count, metadata, created_at
	FROM chunks
	WHERE source_id = $1
	ORDER BY chunk_index ASC`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := m.DB.QueryContext(ctx, query, sourceID)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}
	}

	defer rows.Close()

	var chunks []*Chunk

	for rows.Next() {
		var chunk Chunk
		err := rows.Scan(
			&chunk.ID,
			&chunk.SourceID,
			&chunk.ChunkIndex,
			&chunk.Content,
			&chunk.TokenCount,
			&chunk.Metadata,
			&chunk.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning chunk row: %w", err)
		}
		chunks = append(chunks, &chunk)
	}

	return chunks, rows.Err()
}

func (m ChunkModel) CountBySourceID(sourceID int64) (int, error) {
	query := `SELECT count(*) FROM chunks WHERE source_id = $1`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var count int
	err := m.DB.QueryRowContext(ctx, query, sourceID).Scan(&count)
	if err != nil {
		switch {
			case errors.Is(err, sql.ErrNoRows):
				return 0, ErrRecordNotFound
			default:
				return 0, err
		}
	}

	return count, nil
}

func (m ChunkModel) SearchByVector(collectionID int64, queryVector []float32, limit int) ([]*ChunkSearchResult, error) {

	queryVectorString := float32SliceToPgVectorString(queryVector)

	query := `
		SELECT
			c.id ,
			c.content,
			c.token_count,
			c.embedding <=> $1::vector AS distance,
			s.id AS source_id,
			s.url,
			s.title,
			c.chunk_index,
			c.metadata
		FROM chunks c
		JOIN sources s ON c.source_id = s.id
		WHERE s.collection_id = $2
			AND s.status = 'completed'
			AND c.embedding IS NOT NULL
		ORDER BY c.embedding <=> $1::vector ASC
		LIMIT $3`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := m.DB.QueryContext(ctx, query, queryVectorString, collectionID, limit)
	if err != nil {
		return nil, fmt.Errorf("searching chunks by vector: %w", err)
	}
	defer rows.Close()

	var results []*ChunkSearchResult

	for rows.Next() {
		var result ChunkSearchResult
		err := rows.Scan(
			&result.ChunkID,
			&result.Content,
			&result.TokenCount,
			&result.Distance,
			&result.SourceID,
			&result.SourceURL,
			&result.SourceTitle,
			&result.ChunkIndex,
			&result.Metadata,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning chunk search result row: %w", err)
		}
		results = append(results, &result)
	}

	return results, rows.Err()
}


func (m ChunkModel) UpdateEmbedding(chunkID int64, embedding []float32) error {
	embeddingString := float32SliceToPgVectorString(embedding)

	query := `UPDATE chunks SET embedding = $1::vector WHERE id = $2`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := m.DB.ExecContext(ctx, query, embeddingString, chunkID)
	return err
}

func (m ChunkModel) BulkUpdateEmbedding(chunkIDs []int64, embeddings [][]float32) error {
	chunkIDsLen := len(chunkIDs)

	if chunkIDsLen == 0 {
		return nil
	}

	if chunkIDsLen != len(embeddings) {
		return fmt.Errorf("chunkIDs and embeddings length mismatch: %d vs %d",chunkIDsLen, len(embeddings))
	}

	// execute the updates in a transaction for better performance and atomicity
	ctx , cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction for bulk update embedding: %w", err)
	}

	defer tx.Rollback() // rollback the transaction if any error occurs

	query := `UPDATE chunks SET embedding = $1::vector WHERE id = $2`
	stmnt , err := tx.PrepareContext(ctx, query)

	if err != nil {
		return fmt.Errorf("preparing statement for bulk update embedding: %w", err)
	}

	defer stmnt.Close()

	for i , chunkID := range chunkIDs {
		embeddingString := float32SliceToPgVectorString(embeddings[i])
		_, err := stmnt.ExecContext(ctx, embeddingString, chunkID)
		if err != nil {
			return fmt.Errorf("executing statement for bulk update embedding (chunkID: %d): %w", chunkID, err)
		}
	}

	return  tx.Commit()
}

func float32SliceToPgVectorString(embedding []float32) string {
	if len(embedding) == 0 {
		return "[]"
	}

	var b strings.Builder
	b.WriteRune('[')

	for i, val := range embedding {
		if i > 0 {
			b.WriteRune(',')
		}
		fmt.Fprintf(&b , "%f" , val)
	}
	b.WriteRune(']')
	return b.String()
}