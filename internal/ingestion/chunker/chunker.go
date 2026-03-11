package chunker

import (
	"strings"
	"unicode"

	"github.com/pkoukk/tiktoken-go"
	"github.com/si-Alif/summerizer/internal/ingestion/cleaner"
)



const (
	DefaultMaxTokenLimitPerChunk = 400
	DefaultChunkOverlapSents = 1
)

var abbreviations = map[string]struct{}{
	// Titles
	"mr": {}, "mrs": {}, "ms": {}, "dr": {}, "prof": {}, "sr": {}, "jr": {},

	// Academic / professional
	"phd": {}, "md": {}, "ba": {}, "ma": {}, "msc": {}, "bsc": {},

	// Latin / common textual
	"etc": {}, "e.g": {}, "i.e": {}, "vs": {}, "cf": {}, "viz": {}, "al": {},

	// Time
	"a.m": {}, "p.m": {},

	// Locations
	"u.s": {}, "u.k": {}, "e.u": {}, "u.n": {},

	// Months
	"jan": {}, "feb": {}, "mar": {}, "apr": {}, "jun": {}, "jul": {},
	"aug": {}, "sep": {}, "sept": {}, "oct": {}, "nov": {}, "dec": {},

	// Days
	"mon": {}, "tue": {}, "tues": {}, "wed": {}, "thu": {}, "thur": {},
	"fri": {}, "sat": {}, "sun": {},

	// Measurements / units
	"sec": {}, "min": {}, "hr": {}, "hrs": {},  "kg": {}, "lb": {},

	// Document references
	"fig": {}, "eq": {}, "ref": {}, "refs": {}, "ch": {}, "chap": {},
	"vol": {}, "no": {}, "nos": {}, "pp": {}, "pg": {},

	// Addresses
	"st": {}, "ave": {}, "blvd": {}, "rd": {}, "ln": {},

	// Misc common
	"dept": {}, "est": {}, "misc": {}, "approx": {},

	// Tech / internet (often seen in docs)
	"cmd": {}, "pkg": {}, "lib": {}, "env": {}, "config": {},
}


type Chunk struct {
	Index int
	Content string
	SectionTitle string
	TokenCount int
	Metadata map[string]any
}

type Options struct {

}



type Chunker struct {
	maxTokenLimitPerChunk int
	chunkOverlapSents int
	tokenizer *tiktoken.Tiktoken
}

func New(maxTokenLimitPerChunk, chunkOverlapSents int) (*Chunker, error) {
	if maxTokenLimitPerChunk <= 0 {
		maxTokenLimitPerChunk = DefaultMaxTokenLimitPerChunk
	}

	if chunkOverlapSents < 0 {
		chunkOverlapSents = DefaultChunkOverlapSents
	}

	tokenizer, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		return nil, err
	}

	return &Chunker{
		maxTokenLimitPerChunk: maxTokenLimitPerChunk,
		chunkOverlapSents: chunkOverlapSents,
		tokenizer: tokenizer,
	}, nil
}

func (c *Chunker) countTokens(text string) int {
	tokens := c.tokenizer.Encode(text, nil, nil)
	return len(tokens)
}

func (c *Chunker) ChunkContent(blocks []cleaner.ContentBlock , docTitle,URL string ) ([]*Chunk, error) {
	if len(blocks) == 0 {
		return nil, nil
	}

	var (
		chunks []*Chunk
		currentChunkContent []string
		currentChunkTokenCount int
		chunkIndex int
		currentSectionTitle string
	)

	flush := func() {
		chunkText := strings.Join(currentChunkContent , " ")
		chunkText  = strings.TrimSpace(chunkText)

		chunks = append(chunks, &Chunk{
			Index: chunkIndex,
			Content: chunkText,
			TokenCount: c.countTokens(chunkText),
			SectionTitle: currentSectionTitle,
			Metadata: map[string]any{
				"section_title" : currentSectionTitle,
				"document_title" : docTitle,
				"url" : URL,
			},
		})
		chunkIndex++
	}

	for i, block := range blocks {
		if i > 0 && block.SectionTitle != currentSectionTitle && len(currentChunkContent) > 0 {
			flush()

			currentChunkContent = nil
			currentChunkTokenCount = 0
		}

		currentSectionTitle = block.SectionTitle

		sents := splitIntoSentences(block.Text)

		for _ , sent := range sents {
			if sent == "" {
				continue
			}

			sentTokenCount := c.countTokens(sent)

			if currentChunkTokenCount + sentTokenCount > c.maxTokenLimitPerChunk && len(currentChunkContent) > 0 {
				flush()

				// overlap
				overlapSt := len(currentChunkContent) - c.chunkOverlapSents
				if overlapSt < 0 {
					overlapSt = 0
				}

				currentChunkContent = currentChunkContent[overlapSt:]
				currentChunkTokenCount = 0

				for _, s := range currentChunkContent {
					currentChunkTokenCount += c.countTokens(s)
				}


			}

			currentChunkTokenCount += sentTokenCount
			currentChunkContent = append(currentChunkContent , sent)
		}

	}

	flush()

	for _, chunk := range chunks {
		chunk.Metadata["chunk_index"] = chunk.Index
		chunk.Metadata["chunk_token_count"] = chunk.TokenCount
		chunk.Metadata["chunk_content_length"] = len(chunk.Content)
		chunk.Metadata["total_chunks"] = len(chunks)
	}

	return  chunks, nil
}

func splitIntoSentences(text string) []string {
	var sentences []string
	var currentSent strings.Builder

	runes := []rune(text)
	rune_len := len(runes)
	for i , r := range runes {
		currentSent.WriteRune(r)

		if r == '.' || r == '!' || r == '?' {

			if r == '.' && i >0 && unicode.IsDigit(runes[i-1]) {
				continue
			}

			if i +1 < rune_len && unicode.IsSpace(runes[i+1]){
				word := lastWord(currentSent.String())
				if _, ok := abbreviations[strings.ToLower(word)] ; !ok {
					sent := strings.TrimSpace(currentSent.String())
					if sent != "" {
						sentences = append(sentences , sent)
					}
					currentSent.Reset()
				}
			}
		}
	}

	if remaining := strings.TrimSpace(currentSent.String()); remaining != "" {
    sentences = append(sentences, remaining)
  }

	return sentences
}

func lastWord(text string) string{
	if len(text) == 0 {
		return ""
	}

	// split the sentence by unicode.IsSpace()
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	word := words[len(words)-1]

	word = strings.TrimRight(word, ".!?,;:")
	word = strings.TrimLeft(word, `"'([`)
	return word
}