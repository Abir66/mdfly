package markdown

import (
	"bytes"
	"errors"
	stdhtml "html"
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

// refTransformer rewrites every markdown image and link destination through the
// RefResolver found in the parser context, before HTML generation. With no
// resolver in context it is a no-op (e.g. ImageRefs/LinkRefs raw parses).
// Raw-HTML references are out of its reach and handled by rewriteHTMLRefs.
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
	reAlignValue  = regexp.MustCompile(`^(?i)(left|right|center|justify)$`)
)

// alignElements are the elements allowed to keep an `align` attribute.
var alignElements = []string{"div", "p", "h1", "h2", "h3", "h4", "h5", "h6"}

// URL-shaped attribute values the sanitizer may keep: an http(s) URL, a
// data:image URI, or a scheme-less relative/root-absolute path. Any other
// scheme — javascript:, vbscript:, data:text/html — fails to match and the
// attribute is dropped. bluemonday applies its own URL policy to `src` and
// `href` but not to `srcset`, `poster`, or `src` on `<source>`, so these carry
// the check for us.
const (
	urlScheme     = `(?:https?://|data:image/)`
	urlTail       = `[^\s"'<>]*`
	urlTailNC     = `[^\s"'<>,]*` // no comma: srcset candidates are comma-separated
	srcSetDescr   = `(?:\s+[0-9.]+[wx])?`
	safeURL       = `(?:` + urlScheme + urlTail + `|[^:\s"'<>/?#]*[/?#]` + urlTail + `|[^:\s"'<>]*)`
	safeSrcSetURL = `(?:` + urlScheme + urlTailNC + `|[^:\s"'<>,/?#]*[/?#]` + urlTailNC + `|[^:\s"'<>,]*)`
)

var (
	reSafeURL    = regexp.MustCompile(`^(?i)\s*` + safeURL + `\s*$`)
	reSafeSrcSet = regexp.MustCompile(`^(?i)\s*` + safeSrcSetURL + srcSetDescr +
		`(?:\s*,\s*` + safeSrcSetURL + srcSetDescr + `)*\s*$`)
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
	// READMEs centre logos and badges with <div align="center"> / <p align="center">.
	policy.AllowAttrs("align").Matching(reAlignValue).OnElements(alignElements...)
	// Raw-HTML media: <picture>/<source> for theme-aware logos, <video>/<audio>
	// for embedded demos. Every attribute is allowlisted, so no event handler
	// survives, and the standard URL policy still gates the schemes.
	policy.AllowElements("picture", "source", "video", "audio")
	policy.AllowAttrs("src").Matching(reSafeURL).OnElements("source", "video", "audio")
	policy.AllowAttrs("poster").Matching(reSafeURL).OnElements("video")
	policy.AllowAttrs("srcset").Matching(reSafeSrcSet).OnElements("source", "img")
	policy.AllowAttrs("sizes", "media", "type").OnElements("source", "img")
	policy.AllowAttrs("controls", "loop", "muted", "preload").OnElements("video", "audio")
	policy.AllowAttrs("autoplay", "playsinline", "width", "height").OnElements("video")
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
		body = rewriteHTMLRefs(body, resolve)
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

// rewriteHTMLRefs rewrites every raw-HTML reference inside the body's HTML
// spans through resolve, returning the amended source: `<a href>`, the `src` of
// `<img>`, `<source>`, `<video>` and `<audio>`, `<video poster>`, and each
// candidate of an `<img>`/`<source>` srcset. The parser cannot rewrite these in
// the AST because raw HTML is opaque to it, so they are patched in the source
// before rendering; only parser-identified HTML ranges are touched, leaving
// refs inside fenced code verbatim.
func rewriteHTMLRefs(body []byte, resolve RefResolver) []byte {
	spans := htmlSpans(body)
	if len(spans) == 0 {
		return body
	}

	var out bytes.Buffer
	prev := 0
	for _, sp := range spans {
		out.Write(body[prev:sp.start])
		out.Write(rewriteHTMLSpan(body[sp.start:sp.stop], resolve))
		prev = sp.stop
	}
	out.Write(body[prev:])
	return out.Bytes()
}

// rewriteHTMLSpan replaces every reference in one raw-HTML span with its
// resolved URL. References the resolver rejects are left verbatim. srcset
// candidates are rewritten individually, so their descriptors survive.
func rewriteHTMLSpan(span []byte, resolve RefResolver) []byte {
	refs := htmlRefsIn(span)
	if len(refs) == 0 {
		return span
	}

	var out bytes.Buffer
	prev := 0
	for _, r := range refs {
		url, ok := resolve(r.ref)
		if !ok {
			continue
		}
		out.Write(span[prev:r.start])
		out.WriteString(stdhtml.EscapeString(url))
		prev = r.stop
	}
	out.Write(span[prev:])
	return out.Bytes()
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
