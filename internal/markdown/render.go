package markdown

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"gopkg.in/yaml.v3"
)

const RenderTimeout = 2 * time.Second
const excerptMaxRunes = 200

var ErrTimeout = errors.New("markdown: render timed out")

var (
	mdParser goldmark.Markdown
	policy   *bluemonday.Policy
)

// RefResolver maps a raw markdown reference to its final URL. It returns
// ok=false to leave the reference verbatim (external, empty, or unknown).
type RefResolver func(ref string) (string, bool)

// refResolverKey carries a RefResolver through the parser context so the AST
// transformer can rewrite references without rebuilding the parser per render.
var refResolverKey = parser.NewContextKey()

// refTransformer rewrites every image and link destination through the
// RefResolver found in the parser context, before HTML generation. With no
// resolver in context it is a no-op (e.g. ImageRefs/LinkRefs raw parses).
type refTransformer struct{}

func (refTransformer) Transform(node *ast.Document, _ text.Reader, pc parser.Context) {
	resolve, ok := pc.Get(refResolverKey).(RefResolver)
	if !ok || resolve == nil {
		return
	}
	ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) { //nolint:errcheck — walk never errors
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.Image:
			if u, ok := resolve(string(t.Destination)); ok {
				t.Destination = []byte(u)
			}
		case *ast.Link:
			if u, ok := resolve(string(t.Destination)); ok {
				t.Destination = []byte(u)
			}
		}
		return ast.WalkContinue, nil
	})
}

var (
	reH1          = regexp.MustCompile(`(?i)<h1[^>]*>(.*?)</h1>`)
	rePara        = regexp.MustCompile(`(?s)<p>(.*?)</p>`)
	reImgSrc      = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)
	reTags        = regexp.MustCompile(`<[^>]+>`)
	reFrontmatter = regexp.MustCompile(`(?s)^\s*---\n(.*?)\n---\n?`)
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
			enrichExtension{},
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(refTransformer{}, 100)),
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

// Meta holds extracted metadata from a markdown document.
type Meta struct {
	Title       string
	Excerpt     string
	OGImagePath string // raw relative path, empty if none found
	HasMermaid  bool   // body emitted a Mermaid placeholder (needs client shim)
	HasMath     bool   // body emitted a KaTeX placeholder (needs client shim)
}

type frontmatterFields struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Image       string `yaml:"image"`
}

// Render converts markdown to sanitized HTML and extracts document metadata.
func Render(md []byte) ([]byte, Meta, error) {
	return RenderWithTimeout(md, RenderTimeout)
}

// RenderWithTimeout is like Render but aborts after the given duration.
func RenderWithTimeout(md []byte, timeout time.Duration) ([]byte, Meta, error) {
	return RenderRefsWithTimeout(md, nil, timeout)
}

// RenderRefs is like Render but rewrites every image and link destination
// through resolve before HTML generation. A nil resolver leaves refs verbatim.
func RenderRefs(md []byte, resolve RefResolver) ([]byte, Meta, error) {
	return RenderRefsWithTimeout(md, resolve, RenderTimeout)
}

// RenderRefsWithTimeout is like RenderRefs but aborts after the given duration.
func RenderRefsWithTimeout(md []byte, resolve RefResolver, timeout time.Duration) ([]byte, Meta, error) {
	type result struct {
		html []byte
		meta Meta
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		h, m, err := renderCore(md, resolve)
		ch <- result{h, m, err}
	}()
	timer := time.NewTimer(timeout)
	select {
	case r := <-ch:
		if !timer.Stop() {
			<-timer.C
		}
		return r.html, r.meta, r.err
	case <-timer.C:
		return nil, Meta{}, ErrTimeout
	}
}

func renderCore(md []byte, resolve RefResolver) ([]byte, Meta, error) {
	fm, body := parseFrontmatter(md)

	var buf bytes.Buffer
	ctx := parser.NewContext()
	flags := &enrichFlags{}
	ctx.Set(enrichFlagsKey, flags)
	if resolve != nil {
		ctx.Set(refResolverKey, resolve)
	}
	if err := mdParser.Convert(body, &buf, parser.WithContext(ctx)); err != nil {
		return nil, Meta{}, err
	}
	sanitized := policy.SanitizeBytes(buf.Bytes())

	meta := extractMeta(string(sanitized), fm)
	meta.HasMermaid = flags.mermaid
	meta.HasMath = flags.math
	return sanitized, meta, nil
}

func parseFrontmatter(md []byte) (frontmatterFields, []byte) {
	loc := reFrontmatter.FindSubmatchIndex(md)
	if loc == nil {
		return frontmatterFields{}, md
	}
	var fm frontmatterFields
	yaml.Unmarshal(md[loc[2]:loc[3]], &fm) //nolint:errcheck — best-effort
	return fm, md[loc[1]:]
}

func extractMeta(htmlBody string, fm frontmatterFields) Meta {
	return Meta{
		Title:       extractTitle(htmlBody, fm.Title),
		Excerpt:     extractExcerpt(htmlBody, fm.Description),
		OGImagePath: extractOGImagePath(htmlBody, fm.Image),
	}
}

func extractTitle(htmlBody, fmTitle string) string {
	if fmTitle != "" {
		return fmTitle
	}
	if m := reH1.FindStringSubmatch(htmlBody); m != nil {
		return strings.TrimSpace(reTags.ReplaceAllString(m[1], ""))
	}
	return ""
}

func extractExcerpt(htmlBody, fmDesc string) string {
	if fmDesc != "" {
		return truncate(fmDesc, excerptMaxRunes)
	}
	if m := rePara.FindStringSubmatch(htmlBody); m != nil {
		text := strings.TrimSpace(reTags.ReplaceAllString(m[1], ""))
		return truncate(text, excerptMaxRunes)
	}
	return ""
}

func extractOGImagePath(htmlBody, fmImage string) string {
	if fmImage != "" {
		return fmImage
	}
	if m := reImgSrc.FindStringSubmatch(htmlBody); m != nil {
		return m[1]
	}
	return ""
}

func truncate(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}
