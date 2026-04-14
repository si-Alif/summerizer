package cleaner

import (
	"context"
	"strings"

	"golang.org/x/net/html"
)

const MinContentLength = 30

// ContentBlock represents individual section to be chunked
type ContentBlock struct {
	SectionTitle string
	Text         string
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

func ExtractBlocks(ctx context.Context, cleanedHTML string) ([]ContentBlock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	parsedHTML, err := html.Parse(strings.NewReader(cleanedHTML))
	if err != nil {
		return nil, err
	}

	var blocks []ContentBlock
	currentSection := ""
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
				}
			}

			if contentTags[tag] {
				text, err := extractText(ctx, n)
				if err != nil {
					return err
				}
				text = strings.TrimSpace(text)
				if len(text) >= MinContentLength {
					if _, ok := seen[text]; !ok {
						seen[text] = struct{}{}
						blocks = append(blocks, ContentBlock{
							SectionTitle: currentSection,
							Text:         text,
						})
					}
				}
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

// Fallback method to extract content blocks from plain text if HTML parsing fails or if the input is not in HTML format. It treats the entire plain text as a single content block with an empty section title.
func FromPlainText(plainText string) []ContentBlock {
	plainText = strings.TrimSpace(plainText)
	if plainText == "" {
		return nil
	}

	paras := strings.Split(plainText, "\n\n")
	blocks := make([]ContentBlock, 0, len(paras))

	for _, para := range paras {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		blocks = append(blocks, ContentBlock{
			SectionTitle: "",
			Text:         para,
		})
	}

	return blocks
}
