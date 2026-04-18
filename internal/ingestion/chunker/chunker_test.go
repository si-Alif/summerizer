package chunker

import (
	"strings"
	"testing"

	"github.com/si-Alif/summerizer/internal/ingestion/cleaner"
)

func TestChunkContentRespectsPrefixBudget(t *testing.T) {
	blocks := []cleaner.ContentBlock{
		{
			SectionTitle: "Retry Policy",
			SectionPath:  "Fetcher > Retry Policy > Exponential Backoff",
			HeadingLevel: 3,
			Type:         cleaner.ContentTypeParagraph,
			Text:         strings.Repeat("The system retries transient failures with jittered exponential backoff. ", 8),
		},
	}

	c, err := New(80, 1)
	if err != nil {
		t.Fatal(err)
	}

	chunks, err := c.ChunkContent(blocks, "Summerizer Ingestion Guide", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks) == 0 {
		t.Fatal("expected chunks, got none")
	}

	for _, ch := range chunks {
		embedTokens := c.countTokens(ch.EmbedText)
		if embedTokens > 80 {
			t.Fatalf("embed text exceeded max token budget: got=%d max=%d", embedTokens, 80)
		}
		if !strings.Contains(ch.EmbedText, "Document: Summerizer Ingestion Guide") {
			t.Fatalf("embed text missing document prefix: %q", ch.EmbedText)
		}
		if !strings.Contains(ch.EmbedText, "Section: Fetcher > Retry Policy > Exponential Backoff") {
			t.Fatalf("embed text missing section prefix: %q", ch.EmbedText)
		}
		if strings.Contains(ch.Content, "Document:") {
			t.Fatalf("stored content should not include context prefix: %q", ch.Content)
		}
	}
}

func TestChunkContentUsesSectionPathBoundary(t *testing.T) {
	blocks := []cleaner.ContentBlock{
		{
			SectionTitle: "Overview",
			SectionPath:  "Part A > Overview",
			HeadingLevel: 2,
			Type:         cleaner.ContentTypeParagraph,
			Text:         "Part A explains ingestion architecture and worker behavior.",
		},
		{
			SectionTitle: "Overview",
			SectionPath:  "Part B > Overview",
			HeadingLevel: 2,
			Type:         cleaner.ContentTypeParagraph,
			Text:         "Part B explains retrieval ranking and response synthesis.",
		},
	}

	c, err := New(200, 1)
	if err != nil {
		t.Fatal(err)
	}

	chunks, err := c.ChunkContent(blocks, "Architecture", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks split by section path, got %d", len(chunks))
	}

	leftPath, _ := chunks[0].Metadata["section_path"].(string)
	rightPath, _ := chunks[1].Metadata["section_path"].(string)
	if leftPath == rightPath {
		t.Fatalf("expected distinct section paths, got %q", leftPath)
	}
}

func TestChunkContentUsesTypeGatedOverlap(t *testing.T) {
	c, err := New(100, 2)
	if err != nil {
		t.Fatal(err)
	}

	previous := []chunkUnit{
		{text: "SELECT id FROM users", tokenCount: 5, blockType: cleaner.ContentTypeCode},
		{text: "The query returns active users.", tokenCount: 6, blockType: cleaner.ContentTypeParagraph},
		{text: "It sorts by recency.", tokenCount: 5, blockType: cleaner.ContentTypeParagraph},
	}

	overlap, overlapTokens := c.buildOverlap(previous, 100, cleaner.ContentTypeParagraph)
	if len(overlap) != 2 {
		t.Fatalf("expected 2 overlap units for paragraph continuation, got %d", len(overlap))
	}
	if overlap[0].blockType != cleaner.ContentTypeParagraph || overlap[1].blockType != cleaner.ContentTypeParagraph {
		t.Fatalf("expected overlap to contain only paragraph units, got %+v", overlap)
	}
	if overlapTokens <= 0 {
		t.Fatal("expected overlap token count to be positive")
	}

	none, noneTokens := c.buildOverlap(previous, 100, cleaner.ContentTypeCode)
	if len(none) != 0 || noneTokens != 0 {
		t.Fatalf("expected no overlap for code chunks, got len=%d tokens=%d", len(none), noneTokens)
	}
}

func TestBuildOverlapKeepsContiguousWindow(t *testing.T) {
	c, err := New(100, 3)
	if err != nil {
		t.Fatal(err)
	}

	previous := []chunkUnit{
		{text: "Sentence A", tokenCount: 50, blockType: cleaner.ContentTypeParagraph},
		{text: "Sentence B", tokenCount: 300, blockType: cleaner.ContentTypeParagraph},
		{text: "Sentence C", tokenCount: 50, blockType: cleaner.ContentTypeParagraph},
	}

	overlap, tokens := c.buildOverlap(previous, 100, cleaner.ContentTypeParagraph)
	if len(overlap) != 1 {
		t.Fatalf("expected 1 contiguous overlap unit, got %d", len(overlap))
	}
	if overlap[0].text != "Sentence A" {
		t.Fatalf("expected oldest contiguous unit to be kept, got %q", overlap[0].text)
	}
	if tokens != 50 {
		t.Fatalf("expected overlap token count=50, got %d", tokens)
	}
}

func TestDominantBlockTypeDeterministicTieBreak(t *testing.T) {
	counts := map[cleaner.ContentType]int{
		cleaner.ContentTypeParagraph: 2,
		cleaner.ContentTypeCode:      2,
		cleaner.ContentTypeList:      2,
	}

	got := dominantBlockType(counts)
	if got != cleaner.ContentTypeCode {
		t.Fatalf("expected deterministic tie-break to prefer code, got %q", got)
	}
}

func TestSplitListItemsHandlesCleanerAndMarkdownFormats(t *testing.T) {
	fromCleaner := splitListItems("Install Go; Configure GOPATH; Run go test")
	if len(fromCleaner) != 3 {
		t.Fatalf("expected 3 list items from cleaner format, got %d (%v)", len(fromCleaner), fromCleaner)
	}

	fromMarkdown := splitListItems("- first\n- second\n3. third")
	if len(fromMarkdown) != 3 {
		t.Fatalf("expected 3 list items from markdown format, got %d (%v)", len(fromMarkdown), fromMarkdown)
	}
}

func TestChunkMetadataIncludesRichFields(t *testing.T) {
	blocks := []cleaner.ContentBlock{
		{
			SectionTitle: "Install",
			SectionPath:  "Setup > Install",
			HeadingLevel: 2,
			Type:         cleaner.ContentTypeList,
			Text:         "Install dependencies; Configure environment; Run migration",
		},
	}

	c, err := New(150, 1)
	if err != nil {
		t.Fatal(err)
	}

	chunks, err := c.ChunkContent(blocks, "Setup Guide", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected exactly 1 chunk, got %d", len(chunks))
	}

	meta := chunks[0].Metadata
	if meta["section_title"] != "Install" {
		t.Fatalf("unexpected section_title metadata: %v", meta["section_title"])
	}
	if meta["section_path"] != "Setup > Install" {
		t.Fatalf("unexpected section_path metadata: %v", meta["section_path"])
	}
	if meta["heading_level"] != 2 {
		t.Fatalf("unexpected heading_level metadata: %v", meta["heading_level"])
	}
	if meta["block_type"] != "list" {
		t.Fatalf("unexpected block_type metadata: %v", meta["block_type"])
	}
	if chunks[0].TokenCount <= 0 {
		t.Fatalf("expected positive token count, got %d", chunks[0].TokenCount)
	}
}

func TestSplitSentences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{name: "basic", input: "Hello world. How are you?", expected: 2},
		{name: "title abbreviation", input: "Dr. Smith went home. He was tired.", expected: 2},
		{name: "decimal", input: "This costs $3.50 per unit. Buy now.", expected: 2},
		{name: "mixed punctuation", input: "Go is great! Really? Yes.", expected: 3},
		{name: "latin abbreviation", input: "e.g. this is an example. It works.", expected: 2},
		{name: "single sentence", input: "Single sentence without period", expected: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitIntoSentences(tt.input)
			if len(got) != tt.expected {
				t.Fatalf("splitIntoSentences(%q) = %d, want %d; got=%v", tt.input, len(got), tt.expected, got)
			}
		})
	}
}
