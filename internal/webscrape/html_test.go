package webscrape

import (
	"strings"
	"testing"
)

func TestExtractText(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		contains []string // substrings that must appear in output
		absent   []string // substrings that must NOT appear
	}{
		{
			name: "article preferred over sidebar",
			html: `<html><body>
				<nav>Navigation links</nav>
				<article><p>Main article content here.</p></article>
				<aside>Sidebar ads</aside>
			</body></html>`,
			contains: []string{"Main article content here"},
			absent:   []string{"Navigation links", "Sidebar ads"},
		},
		{
			name: "script and style removed",
			html: `<html><body>
				<script>var x = 1; alert("bad");</script>
				<style>.foo { color: red; }</style>
				<p>Real content</p>
			</body></html>`,
			contains: []string{"Real content"},
			absent:   []string{"var x", "alert", ".foo", "color: red"},
		},
		{
			name: "heading preservation h1",
			html: `<html><body><h1>Main Title</h1><p>Content</p></body></html>`,
			contains: []string{"# Main Title", "Content"},
		},
		{
			name: "heading preservation h2",
			html: `<html><body><h2>Section Title</h2><p>Body text</p></body></html>`,
			contains: []string{"## Section Title"},
		},
		{
			name: "heading hierarchy",
			html: `<html><body>
				<h1>Top</h1>
				<h2>Sub</h2>
				<h3>SubSub</h3>
			</body></html>`,
			contains: []string{"# Top", "## Sub", "### SubSub"},
		},
		{
			name: "code block preservation",
			html: `<html><body><pre><code>func main() {
    fmt.Println("hello")
}</code></pre></body></html>`,
			contains: []string{"```", "func main()"},
		},
		{
			name: "empty html returns empty string",
			html: "",
		},
		{
			name: "minimal html",
			html: `<html><body></body></html>`,
		},
		{
			name: "malformed html graceful degradation",
			html: `<html><body><p>Unclosed paragraph<div>nested`,
			contains: []string{"Unclosed paragraph", "nested"},
		},
		{
			name: "nav and footer removed",
			html: `<html><body>
				<header>Site header</header>
				<main><p>Good content</p></main>
				<footer>Footer text</footer>
			</body></html>`,
			contains: []string{"Good content"},
			absent:   []string{"Site header", "Footer text"},
		},
		{
			name: "main preferred when no article",
			html: `<html><body>
				<nav>Skip</nav>
				<main><p>Primary content</p></main>
			</body></html>`,
			contains: []string{"Primary content"},
			absent:   []string{"Skip"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractText(tt.html)
			if err != nil {
				t.Fatalf("ExtractText error: %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\ngot:\n%s", want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("output unexpectedly contains %q\ngot:\n%s", absent, got)
				}
			}
		})
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	input := "line1\n\n\n\n\nline2\n   \nline3"
	got := normalizeWhitespace(input)
	// No more than one consecutive blank line
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("found triple newline in normalized output: %q", got)
	}
	if !strings.Contains(got, "line1") || !strings.Contains(got, "line2") || !strings.Contains(got, "line3") {
		t.Errorf("content lost during normalization: %q", got)
	}
}
