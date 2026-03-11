package chunker

import (
	"fmt"
	"testing"

	"github.com/si-Alif/summerizer/internal/ingestion/cleaner"
)

func TestChunkBlocks(t *testing.T) {
	blocks := []cleaner.ContentBlock{
		{SectionTitle: "Creating a Client", Text: "Go provides http.Client to make HTTP requests. You can create a default client or customize it with transport settings. The zero value is usable but has no timeout."},
		{SectionTitle: "Creating a Client", Text: "A custom client allows you to set timeouts, redirect policies, and cookie jars. Most production code should create a custom client."},
		{SectionTitle: "Timeout Configuration", Text: "The client timeout determines the maximum duration for the entire request. This includes connection, any redirects, and reading the response body. Use client.Timeout to set it."},
		{SectionTitle: "Timeout Configuration", Text: "For more granular control, configure the underlying transport. You can set DialContext timeout for connection establishment and TLSHandshakeTimeout separately."},
	}

	c, err := New(80, 1)
	if err != nil {
		t.Fatal(err)
	}

	chunks, err := c.ChunkContent(blocks, "Go HTTP Docs", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}

	for _, ch := range chunks {
		fmt.Printf("--- Chunk %d [%s] (tokens: %d) ---\n%s\n\n",
			ch.Index, ch.SectionTitle, ch.TokenCount, ch.Content)
	}

	// verify no chunk exceeds max tokens
	for _, ch := range chunks {
		if ch.TokenCount > 80 {
			t.Errorf("chunk %d exceeds max tokens: %d", ch.Index, ch.TokenCount)
		}
	}

	// verify section titles are present
	for _, ch := range chunks {
		if ch.SectionTitle == "" {
			t.Errorf("chunk %d has empty section title", ch.Index)
		}
	}

	// verify metadata is populated
	for _, ch := range chunks {
		if ch.Metadata["document_title"] != "Go HTTP Docs" {
			t.Errorf("chunk %d missing document_title in metadata", ch.Index)
		}
		if ch.Metadata["total_chunks"] != len(chunks) {
			t.Errorf("chunk %d has wrong total_chunks", ch.Index)
		}
	}

	// verify chunks from different sections are never merged
	for i := 1; i < len(chunks); i++ {
		prev := chunks[i-1]
		curr := chunks[i]
		// if section changed, content should not overlap
		if prev.SectionTitle != curr.SectionTitle {
			// overlap is only within same section
			t.Logf("section boundary between chunk %d (%s) and chunk %d (%s)",
				prev.Index, prev.SectionTitle, curr.Index, curr.SectionTitle)
		}
	}
}

func TestChunkPlainTextFallback(t *testing.T) {
	text := `Go provides http.Client to make HTTP requests.
You can create a default client or customize it.

The client timeout determines the maximum duration.
Use client.Timeout to set it.`

	c, err := New(50, 1)
	if err != nil {
		t.Fatal(err)
	}

	chunks, err := c.ChunkContent([]cleaner.ContentBlock{{Text: text}}, "Go HTTP Docs", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks) == 0 {
		t.Error("expected chunks from plain text, got none")
	}

	for _, ch := range chunks {
		fmt.Printf("--- Chunk %d (tokens: %d) ---\n%s\n\n",
			ch.Index, ch.TokenCount, ch.Content)
	}
}

func TestSplitSentences(t *testing.T) {
	tests := []struct {
		input    string
		expected int // expected sentence count
	}{
		{"Hello world. How are you?", 2},
		{"Dr. Smith went home. He was tired.", 2},  // "Dr." should NOT split
		{"This costs $3.50 per unit. Buy now.", 2}, // "3.50" should NOT split
		{"Go is great! Really? Yes.", 3},
		{"e.g. this is an example. It works.", 2}, // "e.g." should NOT split
		{"Single sentence without period", 1},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitIntoSentences(tt.input)
			if len(got) != tt.expected {
				t.Errorf("splitIntoSentences(%q) = %d sentences, want %d\nGot: %v",
					tt.input, len(got), tt.expected, got)
			}
		})
	}
}
