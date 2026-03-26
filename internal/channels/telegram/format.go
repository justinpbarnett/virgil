package telegram

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

const maxMsgLen = 4096

var (
	codeBlockRe  = regexp.MustCompile("(?s)```(?:[a-zA-Z0-9]+\n)?(.*?)```")
	inlineCodeRe = regexp.MustCompile("`([^`\n]+)`")
	headingRe    = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	boldRe       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	hrRe         = regexp.MustCompile(`(?m)^---+\s*$`)
	tableAlignRe = regexp.MustCompile(`(?m)^\|[-| :]+\|\s*$`)
	bulletRe     = regexp.MustCompile(`(?m)^[ \t]*[-*]\s+`)
	linkRe       = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s)]+)\)`)
)

type savedChunk struct {
	marker  string
	content string
}

// mdToHTML converts Markdown to Telegram HTML parse mode.
// Handles code blocks, inline code, bold, headings, links, bullets, and horizontal rules.
// Italic (_text_) is intentionally skipped -- underscores in identifiers cause too many false positives.
func mdToHTML(md string) string {
	var saved []savedChunk
	idx := 0

	save := func(htmlContent string) string {
		marker := fmt.Sprintf("\x01%d\x01", idx)
		saved = append(saved, savedChunk{marker, htmlContent})
		idx++
		return marker
	}

	// Step 1: extract code blocks and inline code before HTML escaping.
	s := codeBlockRe.ReplaceAllStringFunc(md, func(match string) string {
		sub := codeBlockRe.FindStringSubmatch(match)
		body := strings.TrimRight(sub[1], "\n")
		return save("<pre>" + html.EscapeString(body) + "</pre>")
	})

	s = inlineCodeRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := inlineCodeRe.FindStringSubmatch(match)
		return save("<code>" + html.EscapeString(sub[1]) + "</code>")
	})

	// Step 2: escape remaining text so any stray < > & become safe.
	s = html.EscapeString(s)

	// Step 3: apply markdown-to-HTML conversions.
	s = hrRe.ReplaceAllString(s, "")
	s = tableAlignRe.ReplaceAllString(s, "")
	s = headingRe.ReplaceAllString(s, "<b>$1</b>")
	s = boldRe.ReplaceAllString(s, "<b>$1</b>")
	s = linkRe.ReplaceAllString(s, `<a href="$2">$1</a>`)
	s = bulletRe.ReplaceAllString(s, "• ")

	// Step 4: restore extracted code blocks.
	for _, c := range saved {
		s = strings.ReplaceAll(s, c.marker, c.content)
	}

	return strings.TrimSpace(s)
}

// splitHTML splits a Telegram HTML string into chunks of at most maxMsgLen runes.
// Splits at paragraph boundaries when possible. When splitting inside a <pre> block,
// it closes and reopens the tag so both chunks render correctly.
func splitHTML(text string) []string {
	runes := []rune(text)
	if len(runes) <= maxMsgLen {
		return []string{text}
	}

	var chunks []string
	remaining := text

	for {
		r := []rune(remaining)
		if len(r) <= maxMsgLen {
			if s := strings.TrimSpace(remaining); s != "" {
				chunks = append(chunks, s)
			}
			break
		}

		chunk := string(r[:maxMsgLen])

		// If we're inside an open <pre> block, close it cleanly and reopen in the next chunk.
		if openPre := strings.Count(chunk, "<pre>") - strings.Count(chunk, "</pre>"); openPre > 0 {
			// Split at the last newline so we don't cut mid-line.
			splitAt := strings.LastIndex(chunk, "\n")
			if splitAt < maxMsgLen/3 {
				splitAt = maxMsgLen // fall through to hard split, tag will be malformed but content survives
			}
			if splitAt < maxMsgLen {
				chunks = append(chunks, strings.TrimSpace(string(r[:splitAt]))+"</pre>")
				remaining = "<pre>" + strings.TrimLeft(string(r[splitAt:]), "\n")
				continue
			}
		}

		// Prefer paragraph boundary.
		if i := strings.LastIndex(chunk, "\n\n"); i > maxMsgLen/3 {
			chunks = append(chunks, strings.TrimSpace(string(r[:i])))
			remaining = strings.TrimLeft(string(r[i:]), "\n")
			continue
		}

		// Fall back to line boundary.
		if i := strings.LastIndex(chunk, "\n"); i > maxMsgLen/3 {
			chunks = append(chunks, strings.TrimSpace(string(r[:i])))
			remaining = strings.TrimLeft(string(r[i:]), "\n")
			continue
		}

		// Hard split as last resort.
		chunks = append(chunks, chunk)
		remaining = string(r[maxMsgLen:])
	}

	return chunks
}
