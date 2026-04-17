package cleaner

import (
	"bufio"
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html"
)

const (
	MinContentLength            = 30
	maxMarkdownScannerTokenSize = 2 * 1024 * 1024
)

type ContentType string

const (
	ContentTypeParagraph ContentType = "paragraph"
	ContentTypeCode      ContentType = "code"
	ContentTypeTable     ContentType = "table"
	ContentTypeList      ContentType = "list"
)

type ExtractionMethod string

const (
	MethodMarkdown  ExtractionMethod = "markdown"
	MethodLegacy    ExtractionMethod = "legacy"
	MethodPlainText ExtractionMethod = "plain_text"
)

var (
	ErrNoContentFromMarkdown = errors.New("no content extracted from markdown")

	headingLineRe = regexp.MustCompile(`^(#{1,6})\s+(.+\S)\s*$`)
	mdLinkRe      = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdImageRe     = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
	fencedCodeRe  = regexp.MustCompile("^```")
	orderedListRe = regexp.MustCompile(`^\d+\.\s+`)

	tableLineRe = regexp.MustCompile(`^\|.*\|$`)
	tableSepRe  = regexp.MustCompile(`^\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?$`)

	noiseTagsForPreClean = map[string]bool{
		"script":   true,
		"style":    true,
		"noscript": true,
		"iframe":   true,
		"canvas":   true,
	}
)

// ContentBlock represents an extracted content unit to be chunked.
type ContentBlock struct {
	SectionTitle string
	SectionPath  string
	HeadingLevel int
	Text         string
	Type         ContentType
}

var headingTags = map[string]bool{
	"h1": true,
	"h2": true,
	"h3": true,
	"h4": true,
	"h5": true,
	"h6": true,
}

var contentTags = map[string]bool{
	"p":          true,
	"blockquote": true,
	"code":       true,
	"pre":        true,
	// "li": true, removed <li> extraction upon facing issues with some websites where each line was wrapped in <li> tags which resulted in a large number of blocks and thus causing issues in chunking and embedding generation
	"td": true,
	"th": true,
	"dd": true,
	"dt": true,
}

var IgnoredTags = map[string]bool{
	"script":     true,
	"nav":        true,
	"menu":       true,
	"sidebar":    true,
	"toc":        true,
	"breadcrumb": true,
	"footer":     true,
	"header":     true,
}

func newMarkdownConverter() *converter.Converter {
	return converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(
				table.WithHeaderPromotion(true),
				table.WithSkipEmptyRows(true),
				table.WithNewlineBehavior(table.NewlineBehaviorPreserve),
			),
		),
	)
}

func ExtractBlocks(ctx context.Context, cleanedHTML string) ([]ContentBlock, error) {
	blocks, _, err := ExtractBlocksWithMethod(ctx, cleanedHTML)
	return blocks, err
}

func ExtractBlocksWithMethod(ctx context.Context, cleanedHTML string) ([]ContentBlock, ExtractionMethod, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	blocks, err := ExtractBlocksMarkdown(ctx, cleanedHTML)
	if err == nil && len(blocks) > 0 {
		return blocks, MethodMarkdown, nil
	}

	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return nil, "", err
	}

	legacyBlocks, legacyErr := extractBlocksLegacy(ctx, cleanedHTML)
	if legacyErr != nil {
		return nil, "", legacyErr
	}

	if len(legacyBlocks) > 0 {
		return legacyBlocks, MethodLegacy, nil
	}

	if err != nil {
		return nil, "", err
	}

	return nil, "", ErrNoContentFromMarkdown
}

func ExtractBlocksMarkdown(ctx context.Context, cleanedHTML string) ([]ContentBlock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cleanedHTML = preCleanHTML(cleanedHTML)

	markdown, err := newMarkdownConverter().ConvertString(cleanedHTML, converter.WithContext(ctx))
	if err != nil {
		return nil, err
	}

	blocks, err := blocksFromMarkdown(ctx, markdown)
	if err != nil {
		return nil, err
	}

	if len(blocks) == 0 {
		return nil, ErrNoContentFromMarkdown
	}

	return blocks, nil
}

func preCleanHTML(input string) string {
	if strings.TrimSpace(input) == "" {
		return input
	}

	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return input
	}

	removeNoiseNodes(doc)

	var b strings.Builder
	if err := html.Render(&b, doc); err != nil {
		return input
	}

	return b.String()
}

func removeNoiseNodes(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling

		if c.Type == html.ElementNode && noiseTagsForPreClean[c.Data] {
			n.RemoveChild(c)
			c = next
			continue
		}

		removeNoiseNodes(c)
		c = next
	}
}

type parserState struct {
	blocks       []ContentBlock
	seen         map[string]struct{}
	sectionStack []string
	currentTitle string
	currentLevel int

	paragraph []string
	listItems []string

	inCode    bool
	codeLang  string
	codeLines []string

	inTable    bool
	tableLines []string
}

func newParserState() *parserState {
	return &parserState{
		seen:         make(map[string]struct{}),
		sectionStack: make([]string, 0, 6),
		paragraph:    make([]string, 0, 16),
		listItems:    make([]string, 0, 16),
		codeLines:    make([]string, 0, 32),
		tableLines:   make([]string, 0, 16),
	}
}

func (s *parserState) sectionPath() string {
	parts := make([]string, 0, len(s.sectionStack))
	for _, p := range s.sectionStack {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}

	return strings.Join(parts, " > ")
}

func (s *parserState) emit(text string, t ContentType) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	minLen := MinContentLength
	if t == ContentTypeCode || t == ContentTypeList {
		minLen = 20
	}

	if len(text) < minLen {
		return
	}

	key := s.sectionPath() + "|" + string(t) + "|" + text
	if _, ok := s.seen[key]; ok {
		return
	}

	s.seen[key] = struct{}{}
	s.blocks = append(s.blocks, ContentBlock{
		SectionTitle: s.currentTitle,
		SectionPath:  s.sectionPath(),
		HeadingLevel: s.currentLevel,
		Text:         text,
		Type:         t,
	})
}

func (s *parserState) flushParagraph() {
	if len(s.paragraph) == 0 {
		return
	}

	text := normalizeParagraph(strings.Join(s.paragraph, " "))
	s.paragraph = s.paragraph[:0]
	s.emit(text, ContentTypeParagraph)
}

func (s *parserState) flushList() {
	if len(s.listItems) == 0 {
		return
	}

	text := normalizeParagraph(strings.Join(s.listItems, "; "))
	s.listItems = s.listItems[:0]
	s.emit(text, ContentTypeList)
}

func (s *parserState) flushCode() {
	if len(s.codeLines) == 0 {
		s.inCode = false
		s.codeLang = ""
		return
	}

	text := normalizeCodeBlock(s.codeLines, s.codeLang)
	s.codeLines = s.codeLines[:0]
	s.inCode = false
	s.codeLang = ""
	s.emit(text, ContentTypeCode)
}

func (s *parserState) flushTable() {
	if len(s.tableLines) == 0 {
		s.inTable = false
		return
	}

	lines := s.tableLines
	s.tableLines = s.tableLines[:0]
	s.inTable = false

	if len(lines) < 2 {
		s.emit(normalizeParagraph(strings.Join(lines, " ")), ContentTypeParagraph)
		return
	}

	hasHeader := false
	var headers []string
	dataStart := 0

	if len(lines) >= 2 && tableSepRe.MatchString(strings.TrimSpace(lines[1])) {
		hasHeader = true
		headers = splitTableRow(lines[0])
		dataStart = 2
	}

	fragments := make([]string, 0, len(lines))

	for i := dataStart; i < len(lines); i++ {
		ln := strings.TrimSpace(lines[i])
		if ln == "" || tableSepRe.MatchString(ln) {
			continue
		}

		cells := splitTableRow(ln)
		if len(cells) == 0 {
			continue
		}

		rowParts := make([]string, 0, len(cells))
		for j, cell := range cells {
			cell = collapseWhitespace(cell)
			if cell == "" {
				continue
			}

			if hasHeader && j < len(headers) {
				h := collapseWhitespace(headers[j])
				if h != "" {
					rowParts = append(rowParts, h+": "+cell)
					continue
				}
			}

			rowParts = append(rowParts, cell)
		}

		if len(rowParts) > 0 {
			fragments = append(fragments, strings.Join(rowParts, ", "))
		}
	}

	s.emit(strings.Join(fragments, ". "), ContentTypeTable)
}

func blocksFromMarkdown(ctx context.Context, markdown string) ([]ContentBlock, error) {
	scanner := bufio.NewScanner(strings.NewReader(markdown))
	scanner.Buffer(make([]byte, 0, 64*1024), maxMarkdownScannerTokenSize)

	st := newParserState()

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		raw := scanner.Text()
		line := strings.TrimSpace(raw)

		if fencedCodeRe.MatchString(line) {
			if st.inCode {
				st.flushCode()
			} else {
				st.flushParagraph()
				st.flushList()
				st.inCode = true
				st.codeLang = strings.TrimSpace(strings.TrimPrefix(line, "```"))
			}
			continue
		}

		if st.inCode {
			st.codeLines = append(st.codeLines, raw)
			continue
		}

		if looksLikeTableLine(line) {
			st.flushParagraph()
			st.flushList()
			st.inTable = true
			st.tableLines = append(st.tableLines, line)
			continue
		}

		if st.inTable {
			st.flushTable()
		}

		if line == "" {
			st.flushParagraph()
			st.flushList()
			continue
		}

		if m := headingLineRe.FindStringSubmatch(line); len(m) == 3 {
			st.flushParagraph()
			st.flushList()

			level := len(m[1])
			title := normalizeHeading(m[2])
			if title == "" {
				continue
			}

			for len(st.sectionStack) < level-1 {
				st.sectionStack = append(st.sectionStack, "")
			}
			if len(st.sectionStack) >= level {
				st.sectionStack = st.sectionStack[:level-1]
			}
			st.sectionStack = append(st.sectionStack, title)

			st.currentTitle = title
			st.currentLevel = level
			continue
		}

		if line == "---" || line == "***" || line == "___" {
			st.flushParagraph()
			st.flushList()
			continue
		}

		if item, ok := parseListItem(line); ok {
			st.flushParagraph()
			st.listItems = append(st.listItems, item)
			continue
		}

		if len(st.listItems) > 0 {
			st.flushList()
		}

		st.paragraph = append(st.paragraph, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	st.flushParagraph()
	st.flushList()
	if st.inCode {
		st.flushCode()
	}
	if st.inTable {
		st.flushTable()
	}

	return st.blocks, nil
}

func parseListItem(line string) (string, bool) {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return normalizeParagraph(strings.TrimSpace(line[2:])), true
	}

	if orderedListRe.MatchString(line) {
		idx := strings.Index(line, ". ")
		if idx > -1 && idx+2 < len(line) {
			return normalizeParagraph(strings.TrimSpace(line[idx+2:])), true
		}
	}

	return "", false
}

func looksLikeTableLine(line string) bool {
	if line == "" {
		return false
	}

	if tableSepRe.MatchString(line) {
		return true
	}

	if !tableLineRe.MatchString(line) {
		return false
	}

	cells := splitTableRow(line)
	return len(cells) >= 2
}

func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "|")
	if line == "" {
		return nil
	}

	cells := strings.Split(line, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}

	return cells
}

func normalizeHeading(s string) string {
	s = mdImageRe.ReplaceAllString(s, "")
	s = mdLinkRe.ReplaceAllString(s, "$1")
	return collapseWhitespace(s)
}

func normalizeParagraph(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = mdImageRe.ReplaceAllString(s, "")
	s = mdLinkRe.ReplaceAllString(s, "$1")
	return collapseWhitespace(s)
}

func normalizeCodeBlock(lines []string, lang string) string {
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}

	body := strings.TrimSpace(strings.Join(lines, "\n"))
	if body == "" {
		return ""
	}

	if lang != "" {
		return "language: " + lang + "\n" + body
	}

	return body
}

func collapseWhitespace(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

func extractBlocksLegacy(ctx context.Context, cleanedHTML string) ([]ContentBlock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	parsedHTML, err := html.Parse(strings.NewReader(cleanedHTML))
	if err != nil {
		return nil, err
	}

	var blocks []ContentBlock
	currentSection := ""
	currentLevel := 0
	seen := make(map[string]struct{})

	var traverse func(*html.Node) error
	traverse = func(n *html.Node) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if n.Type == html.ElementNode {
			tag := n.Data
			if IgnoredTags[tag] {
				return nil
			}

			if headingTags[tag] {
				text, err := extractText(ctx, n)
				if err != nil {
					return err
				}
				text = strings.TrimSpace(text)
				if text != "" {
					currentSection = text
					if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
						currentLevel = int(tag[1] - '0')
					}
				}
				return nil
			}

			if contentTags[tag] {
				text, err := extractText(ctx, n)
				if err != nil {
					return err
				}
				text = strings.TrimSpace(text)
				if len(text) >= MinContentLength {
					key := currentSection + "|" + tag + "|" + text
					if _, ok := seen[key]; !ok {
						seen[key] = struct{}{}
						blocks = append(blocks, ContentBlock{
							SectionTitle: currentSection,
							SectionPath:  currentSection,
							HeadingLevel: currentLevel,
							Text:         text,
							Type:         legacyTypeForTag(tag),
						})
					}
				}
				return nil
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if err := traverse(c); err != nil {
				return err
			}
		}
		return nil
	}

	if err := traverse(parsedHTML); err != nil {
		return nil, err
	}

	return blocks, nil
}

func legacyTypeForTag(tag string) ContentType {
	switch tag {
	case "code", "pre":
		return ContentTypeCode
	case "td", "th":
		return ContentTypeTable
	default:
		return ContentTypeParagraph
	}
}

func extractText(ctx context.Context, n *html.Node) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if n.Type == html.TextNode {
		return n.Data, nil
	}

	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text, err := extractText(ctx, c)
		if err != nil {
			return "", err
		}
		b.WriteString(text)
	}
	return b.String(), nil
}

// FromPlainText extracts blocks from plain text when HTML extraction yields no usable blocks.
func FromPlainText(plainText string) []ContentBlock {
	plainText = strings.TrimSpace(plainText)
	if plainText == "" {
		return nil
	}

	sep := "\n\n"
	if !strings.Contains(plainText, "\n\n") {
		sep = "\n"
	}

	paras := strings.Split(plainText, sep)
	blocks := make([]ContentBlock, 0, len(paras))

	for _, para := range paras {
		para = collapseWhitespace(para)
		if len(para) < MinContentLength {
			continue
		}

		blocks = append(blocks, ContentBlock{
			SectionTitle: "",
			SectionPath:  "",
			HeadingLevel: 0,
			Text:         para,
			Type:         ContentTypeParagraph,
		})
	}

	return blocks
}
