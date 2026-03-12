package search

import (
	"context"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion/embedder"
)


type Service struct {
	embedder *embedder.Embedder
	models data.Models
	// llm *llm.Client
}

func NewService(
		embedder *embedder.Embedder,
		models data.Models,
		// llm *llm.Client
	) *Service {
	return &Service{
		embedder: embedder,
		models: models,
		// llm: llm,
	}
}

func (s *Service) SearchService(ctx context.Context, collectionID int64, query string, topK int) ([]*data.ChunkSearchResult, error) {
	embedding, err := embedQuery(query, s.embedder)

	if err != nil {
		return nil, err
	}

	results, err := s.models.Chunks.SearchByVector(collectionID, embedding, topK)

	if err != nil {
		return nil, err
	}

	return results, nil
}
















func embedQuery(query string, embedder *embedder.Embedder) ([]float32, error) {
	ctx , cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	text := make([]string , 1)
	text[0] = query

	res , err := embedder.GetEmbeddings(ctx , text)
	if err != nil {
		return nil, err
	}

	return res[0], nil
}
