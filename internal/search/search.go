package search

// import (
// 	"context"
// 	"time"

// 	"github.com/si-Alif/summerizer/internal/data"
// 	"github.com/si-Alif/summerizer/internal/ingestion/embedder"
// )


// type SearchResult struct {
// 	chunk data.ChunkSearchResult
// }

// type SearchService struct {
// 	Query string
// 	TopK  int
// 	CollectionID int64
// 	embedder *embedder.Embedder
// }

// func NewSearchService(query string, topK int, collectionID int64, embedder *embedder.Embedder) *SearchService {
// 	return &SearchService{
// 		Query: query,
// 		TopK: topK,
// 		CollectionID: collectionID,
// 		embedder: embedder,
// 	}
// }

// func (s *SearchService) Embed() ([]float32, error) {
// 	ctx , cancel := context.WithTimeout(context.Background(), 5*time.Second)
// 	defer cancel()

// 	text := make([]string , 1)
// 	text[0] = s.Query

// 	res , err := s.embedder.GetEmbeddings(ctx , text)
// 	if err != nil {
// 		return nil, err
// 	}

// 	return res[0], nil
// }


// func (s *SearchService) SearchByEmbedding(embedding []float32) ([]SearchResult, error) {

// }
