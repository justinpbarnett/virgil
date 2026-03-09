package webscrape

import (
	"strings"

	"golang.org/x/net/html"
)

// noiseElements are removed entirely during extraction (content + children).
var noiseElements = map[string]bool{
	"script": true, "style": true, "nav": true,
	"header": true, "footer": true, "aside": true,
	"noscript": true, "iframe": true, "svg": true,
	"form": true, "button": true, "input": true, "select": true,
}

// headingLevel maps tag names to their Markdown heading prefix.
var headingLevel = map[string]string{
	"h1": "# ", "h2": "## ", "h3": "### ",
	"h4": "#### ", "h5": "##### ", "h6": "###### ",
}

// ExtractText extracts readable text from HTML content using readability-style
// heuristics. It prefers content in <article> or <main> elements, removes noise
// (scripts, nav, footer), and preserves heading hierarchy and code blocks.
func ExtractText(htmlContent string) (string, error) {
	if strings.TrimSpace(htmlContent) == "" {
		return "", nil
	}

	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// Graceful degradation — return empty rather than error
		return "", nil
	}

	// Prefer article or main, fall back to body, then full document
	root := findElement(doc, "article")
	if root == nil {
		root = findElement(doc, "main")
	}
	if root == nil {
		root = findElement(doc, "body")
	}
	if root == nil {
		root = doc
	}

	var sb strings.Builder
	extractNode(root, &sb, false)

	return normalizeWhitespace(sb.String()), nil
}

// extractNode recursively extracts text from an HTML node tree.
func extractNode(n *html.Node, sb *strings.Builder, inCode bool) {
	if n == nil {
		return
	}

	switch n.Type {
	case html.TextNode:
		sb.WriteString(n.Data)
		return

	case html.ElementNode:
		tag := strings.ToLower(n.Data)

		// Skip noise elements entirely
		if noiseElements[tag] {
			return
		}

		// Handle headings
		if prefix, ok := headingLevel[tag]; ok {
			sb.WriteString("\n")
			sb.WriteString(prefix)
			extractChildren(n, sb, false)
			sb.WriteString("\n")
			return
		}

		// Handle code blocks — wrap in fenced code
		if tag == "pre" {
			sb.WriteString("\n```\n")
			extractChildren(n, sb, true)
			sb.WriteString("\n```\n")
			return
		}

		// Inline code
		if tag == "code" && !inCode {
			sb.WriteString("`")
			extractChildren(n, sb, true)
			sb.WriteString("`")
			return
		}

		// Block elements that get a newline before/after
		if isBlockElement(tag) {
			sb.WriteString("\n")
			extractChildren(n, sb, inCode)
			sb.WriteString("\n")
			return
		}

		extractChildren(n, sb, inCode)

	case html.DocumentNode:
		extractChildren(n, sb, inCode)
	}
}

func extractChildren(n *html.Node, sb *strings.Builder, inCode bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractNode(c, sb, inCode)
	}
}

// isBlockElement returns true for HTML elements that represent block-level content.
func isBlockElement(tag string) bool {
	switch tag {
	case "p", "div", "section", "article", "main",
		"li", "dd", "dt", "blockquote",
		"td", "th", "tr", "table",
		"br", "hr":
		return true
	}
	return false
}

// findElement performs a depth-first search for the first element with the given tag.
func findElement(n *html.Node, tag string) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && strings.ToLower(n.Data) == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, tag); found != nil {
			return found
		}
	}
	return nil
}

// normalizeWhitespace collapses multiple blank lines into a single blank line
// and trims leading/trailing whitespace.
func normalizeWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	result := make([]string, 0, len(lines))
	blankCount := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			blankCount++
			if blankCount <= 1 {
				result = append(result, "")
			}
		} else {
			blankCount = 0
			result = append(result, trimmed)
		}
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}
