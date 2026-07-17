package markdown

import (
	"bytes"
	"fmt"
	"html/template"
	"unicode/utf8"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// highlightStyle is the chroma style shared with the markdown code-block path
// (see init in render.go). fileFormatter emits inline styles (WithClasses(false))
// so a highlighted file needs no external stylesheet — zero client assets.
const highlightStyle = "github"

// fileFormatter renders a whole-file view (unlike a markdown code block): it adds
// a line-number gutter in a two-column table, which the .code-file CSS styles
// into the GitHub blob look. Numbers are non-selectable via inline styles chroma
// emits, so copying the code skips them.
var fileFormatter = chromahtml.New(
	chromahtml.WithClasses(false),
	chromahtml.WithLineNumbers(true),
	chromahtml.LineNumbersInTable(true),
)

// IsBinary reports whether content looks binary: it holds a NUL byte or is not
// valid UTF-8. Empty content is treated as text (false).
func IsBinary(content []byte) bool {
	if bytes.IndexByte(content, 0) >= 0 {
		return true
	}
	return !utf8.Valid(content)
}

// HighlightFile renders a standalone file's content to syntax-highlighted HTML.
// The lexer is chosen from filename (extension/name), falling back to plaintext
// for unknown types. Output carries inline chroma styles and is sanitized with
// the shared policy, so it drops into the page like a markdown code block.
func HighlightFile(filename string, content []byte) (template.HTML, error) {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	style := styles.Get(highlightStyle)
	iterator, err := lexer.Tokenise(nil, string(content))
	if err != nil {
		return "", fmt.Errorf("tokenise %s: %w", filename, err)
	}
	var buf bytes.Buffer
	if err := fileFormatter.Format(&buf, style, iterator); err != nil {
		return "", fmt.Errorf("format %s: %w", filename, err)
	}
	return template.HTML(policy.SanitizeBytes(buf.Bytes())), nil
}
