package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion/embedder"
	"github.com/si-Alif/summerizer/internal/llm"
)

type Service struct {
	embedder *embedder.Embedder
	models   data.Models
	llm      llm.Client
}

type AskResult struct {
	Answer  string                    `json:"answer"`
	Sources []*data.ChunkSearchResult `json:"sources"`
}

type chunkMetadata struct {
	SectionTitle  string `json:"section_title"`
	DocumentTitle string `json:"document_title"`
}

func NewService(embedder *embedder.Embedder, models data.Models, llmClient llm.Client) *Service {
	return &Service{
		embedder: embedder,
		models:   models,
		llm:      llmClient,
	}
}

func (s *Service) SearchService(ctx context.Context, collectionID int64, query string, topK int) ([]*data.ChunkSearchResult, error) {
	embedding, err := embedQuery(ctx, query, s.embedder)
	if err != nil {
		return nil, err
	}

	results, err := s.models.Chunks.SearchByVector(collectionID, embedding, topK)
	if err != nil {
		return nil, err
	}

	return results, nil
}

func (s *Service) AskService(ctx context.Context, collectionID int64, question string, topK int) (*AskResult, error) {
	results, err := s.SearchService(ctx, collectionID, question, topK)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return &AskResult{
			Answer:  "I don't have enough information in the provided sources to answer that.",
			Sources: results,
		}, nil
	}

	req := buildGenerateRequest(question, results)

	resp, err := s.llm.Generate(ctx, req)
	if err != nil {
		return nil, err
	}

	return &AskResult{
		Answer:  resp.Text,
		Sources: results,
	}, nil
}

func buildGenerateRequest(question string, results []*data.ChunkSearchResult) llm.GenerateRequest {
	systemPrompt := `You are a helpful assistant.
Answer ONLY from the provided context.
If the context does not contain enough information, say:
"I don't have enough information in the provided sources."
When possible, cite source URLs used.`

	var b strings.Builder
	b.WriteString("Question:\n")
	b.WriteString(question)
	b.WriteString("\n\nContext:\n")

	for i, r := range results {
		meta := chunkMetadata{}
		if len(r.Metadata) > 0 {
			_ = json.Unmarshal(r.Metadata, &meta)
		}

		section := meta.SectionTitle
		if section == "" {
			section = "(none)"
		}
		title := r.SourceTitle
		if title == "" {
			title = meta.DocumentTitle
		}
		if title == "" {
			title = "(untitled)"
		}

		b.WriteString(fmt.Sprintf("[%d]\n", i+1))
		b.WriteString(fmt.Sprintf("Title: %s\n", title))
		b.WriteString(fmt.Sprintf("URL: %s\n", r.SourceURL))
		b.WriteString(fmt.Sprintf("Section: %s\n", section))
		b.WriteString(fmt.Sprintf("ChunkIndex: %d\n", r.ChunkIndex))
		b.WriteString(fmt.Sprintf("Distance: %.4f\n", r.Distance))
		b.WriteString("Content:\n")
		b.WriteString(r.Content)
		b.WriteString("\n\n")
	}

	return llm.GenerateRequest{
		Messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: b.String()},
		},
		Temperature: 0.2,
		MaxTokens:   700,
	}
}

func embedQuery(parentCtx context.Context, query string, emb *embedder.Embedder) ([]float32, error) {
	ctx, cancel := context.WithTimeout(parentCtx, 15*time.Second)
	defer cancel()

	res, err := emb.GetSearchQueryEmbedding(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("embed query: no embedding returned")
	}

	return res, nil
}
