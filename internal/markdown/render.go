package markdown

import (
	"bytes"
	"errors"
	"regexp"
	"time"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

const RenderTimeout = 2 * time.Second

var ErrTimeout = errors.New("markdown: render timed out")

var (
	mdParser goldmark.Markdown
	policy   *bluemonday.Policy
)

func init() {
	mdParser = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github"),
				highlighting.WithFormatOptions(
					chromahtml.WithClasses(false),
				),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
		),
	)

	policy = bluemonday.UGCPolicy()
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`^[\w\s-]+$`)).OnElements("code", "pre", "span", "div")
	policy.AllowAttrs("id").Matching(regexp.MustCompile(`^[a-zA-Z0-9\-_:]+$`)).OnElements("h1", "h2", "h3", "h4", "h5", "h6")
	policy.AllowAttrs("tabindex").Matching(regexp.MustCompile(`^\d+$`)).OnElements("pre")
	policy.AllowAttrs("style").Matching(regexp.MustCompile(`^[\w\s:;#()\.,%-]+$`)).OnElements("span", "pre", "code")
	// Allow task-list checkboxes rendered by GFM extension.
	policy.AllowElements("input")
	policy.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	policy.AllowAttrs("checked", "disabled").OnElements("input")
}

// Render converts markdown to sanitized HTML using Goldmark (GFM + chroma) and bluemonday.
func Render(md []byte) ([]byte, error) {
	return RenderWithTimeout(md, RenderTimeout)
}

// RenderWithTimeout is like Render but aborts after the given duration.
func RenderWithTimeout(md []byte, timeout time.Duration) ([]byte, error) {
	type result struct {
		html []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var buf bytes.Buffer
		if err := mdParser.Convert(md, &buf); err != nil {
			ch <- result{nil, err}
			return
		}
		ch <- result{policy.SanitizeBytes(buf.Bytes()), nil}
	}()
	select {
	case r := <-ch:
		return r.html, r.err
	case <-time.After(timeout):
		return nil, ErrTimeout
	}
}
