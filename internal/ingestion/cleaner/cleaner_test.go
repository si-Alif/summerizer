package cleaner

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExtractBlocksWithMethod_MarkdownPreferred(t *testing.T) {
	htmlInput := `<h1>Introduction</h1><p>This is a sufficiently long paragraph to verify markdown-first extraction behavior in cleaner.</p>`

	blocks, method, err := ExtractBlocksWithMethod(context.Background(), htmlInput)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if method != MethodMarkdown {
		t.Fatalf("expected method %q, got %q", MethodMarkdown, method)
	}

	if len(blocks) == 0 {
		t.Fatalf("expected non-empty blocks")
	}

	if blocks[0].Type != ContentTypeParagraph {
		t.Fatalf("expected paragraph type, got %q", blocks[0].Type)
	}
}

func TestExtractBlocksWithMethod_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := ExtractBlocksWithMethod(ctx, `<p>irrelevant</p>`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestFromPlainText_SingleNewlineFallback(t *testing.T) {
	plainText := strings.Join([]string{
		"This first paragraph line is long enough to pass minimum threshold.",
		"This second paragraph line is also long enough to become a block.",
	}, "\n")

	blocks := FromPlainText(plainText)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks from single-newline split, got %d", len(blocks))
	}

	for i := range blocks {
		if blocks[i].Type != ContentTypeParagraph {
			t.Fatalf("expected paragraph block type at index %d, got %q", i, blocks[i].Type)
		}
	}
}

func TestLooksLikeTableLine_ReducesFalsePositives(t *testing.T) {
	if looksLikeTableLine("| grep main") {
		t.Fatalf("expected non-table pipe text to be false")
	}

	if !looksLikeTableLine("| name | value |") {
		t.Fatalf("expected markdown table row to be true")
	}

	if !looksLikeTableLine("| ---- | ---- |") {
		t.Fatalf("expected markdown table separator to be true")
	}
}

func TestBlocksFromMarkdown_CodeFencePreserved(t *testing.T) {
	markdown := strings.Join([]string{
		"```go",
		"fmt.Println(\"hello\")",
		"```",
	}, "\n")

	blocks, err := blocksFromMarkdown(context.Background(), markdown)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(blocks) != 1 {
		t.Fatalf("expected 1 code block, got %d", len(blocks))
	}

	if blocks[0].Type != ContentTypeCode {
		t.Fatalf("expected code type, got %q", blocks[0].Type)
	}

	if !strings.Contains(blocks[0].Text, "language: go") {
		t.Fatalf("expected language tag in code block text, got %q", blocks[0].Text)
	}
}

func TestParseListItem_MultiDigitOrderedList(t *testing.T) {
	item, ok := parseListItem("10. install dependencies")
	if !ok {
		t.Fatalf("expected ordered list item to parse")
	}

	if item != "install dependencies" {
		t.Fatalf("expected parsed list content, got %q", item)
	}
}
