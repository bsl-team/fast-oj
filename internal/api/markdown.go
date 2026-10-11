package api

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

// Statement Markdown comes from problem authors, but it is still rendered with
// raw HTML skipped and dangerous URL schemes refused: one stray <script> in a
// statement would otherwise run on every contestant's page.
func newMarkdownParser() *parser.Parser {
	return parser.NewWithExtensions(
		parser.CommonExtensions | // tables, fenced code, headings, lists
			parser.MathJax) // keep $...$ / $$...$$ verbatim for KaTeX
}

// safeURL blocks schemes a click could execute. Relative links and http(s),
// which the renderer allows by default, stay usable.
func safeURL(raw []byte) bool {
	s := strings.ToLower(strings.TrimSpace(string(raw)))
	for _, bad := range []string{"javascript:", "data:", "vbscript:", "file:", "blob:", "about:"} {
		if strings.HasPrefix(s, bad) {
			return false
		}
	}
	return true
}

func renderMarkdownHTML(src []byte) template.HTML {
	if len(bytes.TrimSpace(src)) == 0 {
		return ""
	}
	// Safelink is what makes IsSafeURLOverride actually consulted; without it the
	// predicate is dead code and javascript: hrefs sail through.
	r := html.NewRenderer(html.RendererOptions{
		Flags: html.SkipHTML | html.HrefTargetBlank | html.Safelink | html.NofollowLinks,
	})
	r.IsSafeURLOverride = safeURL
	body := markdown.ToHTML(src, newMarkdownParser(), r)
	return template.HTML(body) //nolint:gosec // raw HTML is skipped and URLs filtered above
}
