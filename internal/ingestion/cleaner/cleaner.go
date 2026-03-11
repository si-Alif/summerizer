package cleaner

import (
	"strings"

	"golang.org/x/net/html"
)

const MinContentLength = 30

// ContentBlock represents individual section to be chunked
type ContentBlock struct {
	SectionTitle string
	Text string
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
	"p": true,
	"blockquote": true,
	"code": true,
	"pre": true,
	// "li": true, removed <li> extraction upon facing issues with some websites where each line was wrapped in <li> tags which resulted in a large number of blocks and thus causing issues in chunking and embedding generation
	"td": true,
	"th": true,
	"dd" : true,
	"dt" : true,
}

var IgnoredTags = map[string]bool{
	"script": true,
	"nav": true,
	"menu": true,
	"sidebar": true,
	"toc": true,
	"breadcrumb": true,
	"footer": true,
	"header": true,
}

func ExtractBlocks(cleanedHTML string) ([]ContentBlock, error) {
	reader := strings.NewReader(cleanedHTML)

	parsedHTML, err := html.Parse(reader)

	if err != nil {
		return nil , err
	}

	var blocks []ContentBlock
	currentSection := ""
	seen := make(map[string]bool) // to track seen section titles and avoid duplicates

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode{
			tag := n.Data
			if IgnoredTags[tag] {
				return // skip this node and its children
			}

			// Note : n.data is tag name if it's element node else it's text content
			if headingTags[tag] { // checking if it's heading tag(element node)

				text := extractText(n) // text is raw title by now
				text = strings.TrimSpace(text)
				if text != "" {
					currentSection = text // update current section title
				}

				for c := n.NextSibling ; c != nil ; c = c.NextSibling {
					traverse(c)
				}
				return
			}

			if contentTags[tag] { // checking if it's content tag
				text := extractText(n)
				text = strings.TrimSpace(text)
				if len(text) < MinContentLength {
					// if the extracted text is too short , then it's likely not useful content and we can skip it
					for c := n.NextSibling ; c != nil ; c = c.NextSibling {
						traverse(c)
					}
					return
				}

				if seen[text]{
					for c := n.NextSibling ; c != nil ; c = c.NextSibling {
						traverse(c)
					}
					return
				}
				seen[text] = true

				blocks = append(blocks , ContentBlock{
					SectionTitle: currentSection,
					Text: text,
				})

				for c := n.NextSibling ; c != nil ; c = c.NextSibling {
					traverse(c)
				}
				return
			}
		}

		// every sub-tag under a parent tag is child node .Thus , every node under a parent in considered as siblings
		// suppose , cleanedHTML is <body>...<body> , thus every node under this will be child relative to it . That's why we extract the first child and then recursively to a depth first traversal to get all the nodes in under a child and then move to next sibling
		for c := n.FirstChild ; c != nil ;c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(parsedHTML)

	return blocks , nil
}

// extracts raw text content from the html node and its child nodes recursively
func extractText(n *html.Node) string{
	if n.Type == html.TextNode {
		return  n.Data
	}

	// inside a tag , we can have the text in multiple layered nodes such as <p> can have <b> and <i> inside it and the text can be in any of those nodes so we need to traverse all the child nodes to get the complete text content of the tag
	// it doesn't matter if it's a title or content , we have to extract the raw text
	var text strings.Builder
	for c := n.FirstChild ; c != nil ; c = c.NextSibling{
		text.WriteString(extractText(c)) // extract text from child nodes and append to the main text
	}

	return text.String()
}


// Fallback method to extract content blocks from plain text if HTML parsing fails or if the input is not in HTML format. It treats the entire plain text as a single content block with an empty section title.
func FromPlainText(plainText string) []ContentBlock {
	plainText = strings.TrimSpace(plainText)
	if plainText == "" {
		return  nil
	}

	paras := strings.Split(plainText , "\n\n")
	blocks := make([]ContentBlock , 0 , len(paras))

	for _, para := range paras {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		blocks = append(blocks , ContentBlock{
			SectionTitle: "",
			Text: para,
		})
	}

	return blocks
}