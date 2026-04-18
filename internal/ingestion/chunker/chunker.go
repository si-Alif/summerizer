package chunker

import (
	"strings"
	"unicode"

	"github.com/pkoukk/tiktoken-go"
	"github.com/si-Alif/summerizer/internal/ingestion/cleaner"
)

const (
	DefaultMaxTokenLimitPerChunk = 400
	DefaultChunkOverlapSents     = 1
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
	"sec": {}, "min": {}, "hr": {}, "hrs": {}, "kg": {}, "lb": {},

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
	Index        int
	Content      string
	EmbedText    string
	SectionTitle string
	SectionPath  string
	TokenCount   int
	Metadata     map[string]any
}

type Options struct{}

type Chunker struct {
	maxTokenLimitPerChunk int
	chunkOverlapSents     int
	tokenizer             *tiktoken.Tiktoken
}

type chunkUnit struct {
	text         string
	tokenCount   int
	blockType    cleaner.ContentType
	headingLevel int
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
		chunkOverlapSents:     chunkOverlapSents,
		tokenizer:             tokenizer,
	}, nil
}

func (c *Chunker) countTokens(text string) int {
	tokens := c.tokenizer.Encode(text, nil, nil)
	return len(tokens)
}

func (c *Chunker) ChunkContent(blocks []cleaner.ContentBlock, docTitle, URL string) ([]*Chunk, error) {
	if len(blocks) == 0 {
		return nil, nil
	}

	var (
		chunks                 []*Chunk
		currentChunkContent    []chunkUnit
		currentChunkTokenCount int
		chunkIndex             int
		currentSectionTitle    string
		currentSectionPath     string
		cachedSectionPath      string
		cachedBodyBudget       int
		hasCachedBodyBudget    bool
	)

	flush := func() {
		if len(currentChunkContent) == 0 {
			return
		}

		chunkText := joinUnitText(currentChunkContent)
		chunkText = strings.TrimSpace(chunkText)
		if chunkText == "" {
			currentChunkContent = nil
			currentChunkTokenCount = 0
			return
		}

		prefix := makeContextPrefix(docTitle, currentSectionPath)
		embedText := chunkText
		if prefix != "" {
			embedText = prefix + chunkText
		}

		headingLevel := 0
		typeCounts := make(map[cleaner.ContentType]int, 4)
		for _, u := range currentChunkContent {
			typeCounts[u.blockType]++
			if u.headingLevel > headingLevel {
				headingLevel = u.headingLevel
			}
		}

		blockType := dominantBlockType(typeCounts)
		blockTypes := orderedBlockTypes(typeCounts)

		chunks = append(chunks, &Chunk{
			Index:        chunkIndex,
			Content:      chunkText,
			EmbedText:    embedText,
			TokenCount:   c.countTokens(chunkText),
			SectionTitle: currentSectionTitle,
			SectionPath:  currentSectionPath,
			Metadata: map[string]any{
				"section_title":     currentSectionTitle,
				"section_path":      currentSectionPath,
				"heading_level":     headingLevel,
				"document_title":    docTitle,
				"url":               URL,
				"block_type":        string(blockType),
				"block_types":       blockTypes,
				"embed_text_tokens": c.countTokens(embedText),
			},
		})
		chunkIndex++

		currentChunkContent = nil
		currentChunkTokenCount = 0
	}

	for _, block := range blocks {
		sectionPath := normalizeSectionPath(block)
		sectionTitle := normalizeSectionTitle(block, sectionPath)

		if len(currentChunkContent) > 0 && sectionPath != currentSectionPath {
			flush()
		}

		currentSectionPath = sectionPath
		currentSectionTitle = sectionTitle

		if !hasCachedBodyBudget || sectionPath != cachedSectionPath {
			cachedSectionPath = sectionPath
			cachedBodyBudget = c.bodyTokenBudget(docTitle, sectionPath)
			hasCachedBodyBudget = true
		}

		bodyBudget := cachedBodyBudget
		units := c.unitsFromBlock(block)

		for _, unit := range units {
			unitParts := c.splitOversizedUnit(unit, bodyBudget)
			if len(unitParts) == 0 {
				continue
			}

			for _, part := range unitParts {
				if part.tokenCount == 0 {
					continue
				}

				if currentChunkTokenCount+part.tokenCount > bodyBudget && len(currentChunkContent) > 0 {
					previous := append([]chunkUnit(nil), currentChunkContent...)
					flush()
					currentChunkContent, currentChunkTokenCount = c.buildOverlap(previous, bodyBudget, part.blockType)
				}

				if part.tokenCount > bodyBudget && len(currentChunkContent) > 0 {
					flush()
				}

				currentChunkContent = append(currentChunkContent, part)
				currentChunkTokenCount += part.tokenCount

				if currentChunkTokenCount > bodyBudget {
					// splitOversizedUnit may still return a single over-budget unit (e.g. long token); keep it isolated.
					flush()
				}
			}
		}
	}

	flush()

	totalChunks := len(chunks)
	for _, chunk := range chunks {
		chunk.Metadata["chunk_index"] = chunk.Index
		chunk.Metadata["chunk_token_count"] = chunk.TokenCount
		chunk.Metadata["chunk_content_length"] = len(chunk.Content)
		chunk.Metadata["total_chunks"] = totalChunks
	}

	return chunks, nil
}

func normalizeSectionPath(block cleaner.ContentBlock) string {
	if p := strings.TrimSpace(block.SectionPath); p != "" {
		return p
	}

	return strings.TrimSpace(block.SectionTitle)
}

func normalizeSectionTitle(block cleaner.ContentBlock, sectionPath string) string {
	if t := strings.TrimSpace(block.SectionTitle); t != "" {
		return t
	}

	return sectionPath
}

func makeContextPrefix(docTitle, sectionPath string) string {
	var b strings.Builder

	if title := strings.TrimSpace(docTitle); title != "" {
		b.WriteString("Document: ")
		b.WriteString(title)
		b.WriteByte('\n')
	}

	if path := strings.TrimSpace(sectionPath); path != "" {
		b.WriteString("Section: ")
		b.WriteString(path)
		b.WriteByte('\n')
	}

	if b.Len() == 0 {
		return ""
	}

	b.WriteByte('\n')
	return b.String()
}

func (c *Chunker) bodyTokenBudget(docTitle, sectionPath string) int {
	prefixTokens := c.countTokens(makeContextPrefix(docTitle, sectionPath))
	budget := c.maxTokenLimitPerChunk - prefixTokens
	if budget < 1 {
		return 1
	}

	return budget
}

func (c *Chunker) unitsFromBlock(block cleaner.ContentBlock) []chunkUnit {
	text := strings.TrimSpace(block.Text)
	if text == "" {
		return nil
	}

	build := func(parts []string, blockType cleaner.ContentType, headingLevel int) []chunkUnit {
		units := make([]chunkUnit, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}

			units = append(units, chunkUnit{
				text:         part,
				tokenCount:   c.countTokens(part),
				blockType:    blockType,
				headingLevel: headingLevel,
			})
		}

		return units
	}

	switch block.Type {
	case cleaner.ContentTypeParagraph:
		return build(splitIntoSentences(text), block.Type, block.HeadingLevel)
	case cleaner.ContentTypeList:
		return build(splitListItems(text), block.Type, block.HeadingLevel)
	case cleaner.ContentTypeCode, cleaner.ContentTypeTable:
		return build([]string{text}, block.Type, block.HeadingLevel)
	default:
		return build(splitIntoSentences(text), cleaner.ContentTypeParagraph, block.HeadingLevel)
	}
}

func splitListItems(text string) []string {
	// Cleaner currently serializes list blocks as "item one; item two; item three".
	// Keep supporting markdown list markers as a forward-compatible fallback.
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == ';' || r == '\n'
	})

	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := trimListPrefix(part)
		if item != "" {
			items = append(items, item)
		}
	}

	if len(items) == 0 {
		fallback := trimListPrefix(text)
		if fallback != "" {
			return []string{fallback}
		}
	}

	return items
}

func trimListPrefix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	for _, prefix := range []string{"- ", "* ", "+ "} {
		s = strings.TrimPrefix(s, prefix)
	}

	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}

	if i > 0 && i+1 < len(s) && s[i] == '.' && s[i+1] == ' ' {
		s = s[i+2:]
	}

	return strings.TrimSpace(s)
}

func (c *Chunker) splitOversizedUnit(unit chunkUnit, budget int) []chunkUnit {
	if budget <= 0 || unit.tokenCount <= budget {
		return []chunkUnit{unit}
	}

	if unit.blockType == cleaner.ContentTypeCode || unit.blockType == cleaner.ContentTypeTable {
		return c.splitByLines(unit, budget)
	}

	return c.splitByWords(unit, budget)
}

func (c *Chunker) splitByLines(unit chunkUnit, budget int) []chunkUnit {
	lines := strings.Split(unit.text, "\n")
	if len(lines) <= 1 {
		return c.splitByWords(unit, budget)
	}

	parts := make([]chunkUnit, 0, len(lines))
	current := make([]string, 0, len(lines))
	currentTokenCount := 0

	emit := func() {
		if len(current) == 0 {
			return
		}

		text := strings.TrimSpace(strings.Join(current, "\n"))
		if text == "" {
			current = current[:0]
			currentTokenCount = 0
			return
		}

		part := unit
		part.text = text
		part.tokenCount = c.countTokens(text)
		parts = append(parts, part)
		current = current[:0]
		currentTokenCount = 0
	}

	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			continue
		}

		lineTokenCount := c.countTokens(line)
		candidateTokenCount := currentTokenCount + lineTokenCount

		if candidateTokenCount > budget {
			if len(current) > 0 {
				emit()
				current = append(current, line)
				currentTokenCount = lineTokenCount
				continue
			}

			lineUnit := unit
			lineUnit.text = line
			lineUnit.tokenCount = lineTokenCount
			parts = append(parts, c.splitByWords(lineUnit, budget)...)
			continue
		}

		current = append(current, line)
		currentTokenCount = candidateTokenCount
	}

	emit()

	if len(parts) == 0 {
		return c.splitByWords(unit, budget)
	}

	return parts
}

func (c *Chunker) splitByWords(unit chunkUnit, budget int) []chunkUnit {
	words := strings.Fields(unit.text)
	if len(words) == 0 {
		return nil
	}

	parts := make([]chunkUnit, 0, len(words))
	current := make([]string, 0, len(words))
	currentTokenCount := 0

	emit := func() {
		if len(current) == 0 {
			return
		}

		text := strings.TrimSpace(strings.Join(current, " "))
		if text == "" {
			current = current[:0]
			currentTokenCount = 0
			return
		}

		part := unit
		part.text = text
		part.tokenCount = c.countTokens(text)
		parts = append(parts, part)
		current = current[:0]
		currentTokenCount = 0
	}

	for _, word := range words {
		wordTokenCount := c.countTokens(word)
		candidateTokenCount := currentTokenCount + wordTokenCount

		if candidateTokenCount > budget {
			if len(current) > 0 {
				emit()
				current = append(current, word)
				currentTokenCount = wordTokenCount
				continue
			}

			part := unit
			part.text = word
			part.tokenCount = wordTokenCount
			parts = append(parts, part)
			continue
		}

		current = append(current, word)
		currentTokenCount = candidateTokenCount
	}

	emit()
	return parts
}

func (c *Chunker) buildOverlap(previous []chunkUnit, bodyBudget int, nextType cleaner.ContentType) ([]chunkUnit, int) {
	if c.chunkOverlapSents <= 0 || !isOverlapType(nextType) {
		return nil, 0
	}

	// Walk backward and stop at the first non-overlap unit so overlap remains contiguous.
	overlap := make([]chunkUnit, 0, c.chunkOverlapSents)
	for i := len(previous) - 1; i >= 0 && len(overlap) < c.chunkOverlapSents; i-- {
		if !isOverlapType(previous[i].blockType) {
			break
		}
		overlap = append(overlap, previous[i])
	}

	for i, j := 0, len(overlap)-1; i < j; i, j = i+1, j-1 {
		overlap[i], overlap[j] = overlap[j], overlap[i]
	}

	kept := make([]chunkUnit, 0, len(overlap))
	tokens := 0
	for _, u := range overlap {
		if tokens+u.tokenCount > bodyBudget {
			break
		}

		kept = append(kept, u)
		tokens += u.tokenCount
	}

	return kept, tokens
}

func isOverlapType(t cleaner.ContentType) bool {
	return t == cleaner.ContentTypeParagraph || t == cleaner.ContentTypeList
}

func joinUnitText(units []chunkUnit) string {
	if len(units) == 0 {
		return ""
	}

	var b strings.Builder
	wroteAny := false

	for i, u := range units {
		text := strings.TrimSpace(u.text)
		if text == "" {
			continue
		}

		if wroteAny {
			prevType := units[i-1].blockType
			if prevType == cleaner.ContentTypeCode || prevType == cleaner.ContentTypeTable || u.blockType == cleaner.ContentTypeCode || u.blockType == cleaner.ContentTypeTable {
				b.WriteString("\n\n")
			} else {
				b.WriteByte(' ')
			}
		}

		b.WriteString(text)
		wroteAny = true
	}

	return strings.TrimSpace(b.String())
}

func dominantBlockType(typeCounts map[cleaner.ContentType]int) cleaner.ContentType {
	if len(typeCounts) == 0 {
		return cleaner.ContentTypeParagraph
	}

	bestType := cleaner.ContentTypeParagraph
	bestCount := -1
	bestRank := blockTypeRank(cleaner.ContentTypeParagraph)

	for t, c := range typeCounts {
		rank := blockTypeRank(t)
		if c > bestCount || (c == bestCount && rank < bestRank) {
			bestType = t
			bestCount = c
			bestRank = rank
		}
	}

	return bestType
}

func orderedBlockTypes(typeCounts map[cleaner.ContentType]int) []string {
	types := make([]string, 0, len(typeCounts))
	for _, t := range []cleaner.ContentType{
		cleaner.ContentTypeCode,
		cleaner.ContentTypeTable,
		cleaner.ContentTypeList,
		cleaner.ContentTypeParagraph,
	} {
		if typeCounts[t] > 0 {
			types = append(types, string(t))
		}
	}

	return types
}

func blockTypeRank(t cleaner.ContentType) int {
	switch t {
	case cleaner.ContentTypeCode:
		return 0
	case cleaner.ContentTypeTable:
		return 1
	case cleaner.ContentTypeList:
		return 2
	case cleaner.ContentTypeParagraph:
		return 3
	default:
		return 4
	}
}

func splitIntoSentences(text string) []string {
	var sentences []string
	var currentSent strings.Builder

	runes := []rune(text)
	runeLen := len(runes)
	for i, r := range runes {
		currentSent.WriteRune(r)

		if r == '.' || r == '!' || r == '?' {
			if r == '.' && i > 0 && unicode.IsDigit(runes[i-1]) {
				continue
			}

			if i+1 < runeLen && unicode.IsSpace(runes[i+1]) {
				word := lastWord(currentSent.String())
				if _, ok := abbreviations[strings.ToLower(word)]; !ok {
					sent := strings.TrimSpace(currentSent.String())
					if sent != "" {
						sentences = append(sentences, sent)
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

func lastWord(text string) string {
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
