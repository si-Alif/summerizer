package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/si-Alif/summerizer/internal/data"
	"github.com/si-Alif/summerizer/internal/ingestion/chunker"
	"github.com/si-Alif/summerizer/internal/ingestion/cleaner"
	"github.com/si-Alif/summerizer/internal/ingestion/fetcher"
)

type Pipeline struct {
	fetcher *fetcher.Fetcher
	chunker *chunker.Chunker
	models data.Models
	logger *slog.Logger
}

func NewPipeline(
	models data.Models,
	logger *slog.Logger,
	f *fetcher.Fetcher,
	c *chunker.Chunker,
) *Pipeline {
	return &Pipeline{
		fetcher: f,
		chunker: c,
		models: models,
		logger: logger,
	}
}

// Process runs the full ingestion pipeline for a single source.
//
// Flow:
//   1. fetch  — download HTML from source URL
//   2. clean  — extract section-aware content blocks from HTML
//   3. chunk  — split blocks into token-sized chunks with overlap
//   4. store  — delete old chunks (if re-ingesting), bulk insert new ones
//
// On error at any step:
//   - source.status = "failed"
//   - source.step_error = error message
//   - source.current_step = the step that failed
//   - retry_count is incremented (handled by caller/worker)
func (p *Pipeline) ProcessSource(ctx context.Context ,source *data.Source) error {
	log := p.logger.With("source_id", source.ID , "url" , source.URL)

	log.Info("Starting ingestion pipeline")

	// Step 1: Fetch
	log.Info("Fetching content")

	err := p.models.Sources.UpdateStatus(source.ID , "ingesting" , "fetch")

	rawContent, err := p.fetcher.Fetch(source.URL)
	if err != nil {
		p.failSource(source.ID , "fetch" , err)
		log.Error("Failed to fetch content", "error", err)
	}

	log.Info("pipeline: fetched", "title", rawContent.Title, "chars", len(rawContent.TextContent))

	// Step 2: Clean
	log.Info("Cleaning content")

	err = p.models.Sources.UpdateStatus(source.ID , "ingesting" , "clean")

	if err != nil {
		return fmt.Errorf("update step to clean %w" , err)
	}



	blocks, err := cleaner.ExtractBlocks(rawContent.HTMLContent)

	if err != nil || len(blocks) ==0 {
		switch {
			case err != nil:
				log.Warn("pipeline: HTML extraction failed, falling back to plain text", "error", err)
			default :
				log.Warn("pipeline: HTML extraction returned no blocks, falling back to plain text")
		}

		blocks = cleaner.FromPlainText(rawContent.TextContent)
	}

	if len(blocks) > 2000 {
		log.Warn("pipeline: extracted a large number of blocks, which may indicate an issue with the HTML structure or extraction logic . Falling back to plain text", "block_count", len(blocks))
		blocks = cleaner.FromPlainText(rawContent.TextContent)
	}

	if len(blocks) == 0{
		p.failSource(source.ID, "clean", fmt.Errorf("no content extracted"))
		return fmt.Errorf("clean failed: no content extracted")
	}

	log.Info("pipeline: cleaned", "blocks", len(blocks))

	// --- Step 3: CHUNK ---
  log.Info("pipeline: chunking")

	err = p.models.Sources.UpdateStatus(source.ID , "ingesting" , "chunk")

	if err != nil {
		return fmt.Errorf("update step to chunk %w" , err)
	}

	chunks  , err := p.chunker.ChunkContent(blocks , rawContent.Title , source.URL)

	if err != nil {
		p.failSource(source.ID , "chunk" , err)
		return fmt.Errorf("chunking failed: %w" , err)
	}

	if len(chunks) == 0 {
		p.failSource(source.ID, "chunk", fmt.Errorf("no chunks generated"))
		return fmt.Errorf("chunking failed: no chunks generated")
	}

	log.Info("pipeline: chunked", "chunks", len(chunks))

	// --- Step 4: STORE ---
	log.Info("pipeline: storing chunks")

	err = p.models.Sources.UpdateStatus(source.ID , "ingesting" , "store")

	if err != nil {
		return fmt.Errorf("update step to store %w" , err)
	}

	dataChunks := make([]*data.Chunk , len(chunks))

	for i , chunk := range chunks {
		metadata , err := json.Marshal(chunk.Metadata)

		if err != nil {
			metadata = []byte("{}")
		}

		dataChunks[i] = &data.Chunk{
			SourceID: source.ID,
			ChunkIndex: chunk.Index,
			Content: chunk.Content,
			TokenCount: chunk.TokenCount,
			Metadata: metadata,
		}
	}

	err = p.models.Chunks.DeleteBySourceID(source.ID)

	if err != nil {
		p.failSource(source.ID , "store" , err)
		return fmt.Errorf("deleting old chunks: %w" , err)
	}

	err = p.models.Chunks.BulkInsert(dataChunks)

	if err != nil {
		p.failSource(source.ID , "store" , err)
		return fmt.Errorf("bulk inserting chunks: %w" , err)
	}

	log.Info("pipeline: stored chunks successfully" , "chunks_stored" , len(chunks))

	// --- Step 5: EMBED --- (skipped for now — added when embedding layer is built)
	// When implemented:
  //   - embed each chunk via Ollama/OpenAI
  //   - call BulkUpdateEmbeddings()

	err = p.models.Sources.UpdateStatus(source.ID , "completed" , "")

	if err != nil {
		return fmt.Errorf("update final status to completed %w" , err)
	}

	log.Info("pipeline: completed",
    "source_id", source.ID,
    "chunks_stored", len(dataChunks),
  )

	return nil
}


func (p *Pipeline) failSource(sourceID int64 , step string , err error){
	updateErr := p.models.Sources.MarkAsFailed(sourceID , step , err.Error())

	if updateErr != nil {
		p.logger.Error("failed to update source status " ,
			"source_id" , sourceID ,
			"step" , step ,
			"error" , updateErr,
			"original_error" , err,
		)
	}

}