package markdown

import (
	"bytes"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

const srcSetAttr = "srcset"

// refAttrs lists, per raw-HTML tag, the attributes whose value names a file the
// bundle may need. A srcset value is a candidate list; every other one is a
// single URL.
var refAttrs = map[string][]string{
	"a":      {"href"},
	"img":    {"src", srcSetAttr},
	"source": {"src", srcSetAttr},
	"video":  {"src", "poster"},
	"audio":  {"src"},
}

var reURLScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)

// ImageRefs returns the raw destinations of every image (`![](dest)`) in the
// source markdown, in document order. Frontmatter is skipped. Destinations are
// returned verbatim (e.g. "./logo.png", "https://x/y.png").
func ImageRefs(md []byte) []string {
	_, body := parseFrontmatter(md)
	doc := mdParser.Parser().Parse(text.NewReader(body))

	var refs []string
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) { //nolint:errcheck — walk never errors
		if entering {
			if img, ok := n.(*ast.Image); ok {
				refs = append(refs, string(img.Destination))
			}
		}
		return ast.WalkContinue, nil
	})
	return refs
}

// LinkRefs returns the raw destinations of every markdown link (`[](dest)`) in
// the source markdown, in document order. Frontmatter is skipped. Destinations
// are returned verbatim (e.g. "./y.md", "https://x/y.md").
func LinkRefs(md []byte) []string {
	_, body := parseFrontmatter(md)
	doc := mdParser.Parser().Parse(text.NewReader(body))

	var refs []string
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) { //nolint:errcheck — walk never errors
		if entering {
			if link, ok := n.(*ast.Link); ok {
				refs = append(refs, string(link.Destination))
			}
		}
		return ast.WalkContinue, nil
	})
	return refs
}

// HTMLRefs returns the destinations of every raw-HTML reference in the source
// markdown, in document order: `<a href>`, the `src` of `<img>`, `<source>`,
// `<video>` and `<audio>`, `<video poster>`, and each candidate URL of an
// `<img>`/`<source>` srcset. Frontmatter and fenced code are skipped;
// destinations are returned verbatim. These references are invisible to
// ImageRefs/LinkRefs because the parser keeps raw HTML opaque.
func HTMLRefs(md []byte) []string {
	_, body := parseFrontmatter(md)

	var refs []string
	for _, sp := range htmlSpans(body) {
		for _, r := range htmlRefsIn(body[sp.start:sp.stop]) {
			refs = append(refs, r.ref)
		}
	}
	return refs
}

// htmlRef is one reference found in a raw-HTML chunk, paired with the byte
// range of its URL alone — the range a rewrite must replace.
type htmlRef struct {
	ref         string
	start, stop int
}

// htmlRefsIn returns every reference in a raw-HTML chunk, in document order.
// Tags are scanned rather than pattern-matched so that a ">" inside a quoted
// attribute value does not end the tag early, and so unquoted values are seen.
// Empty values are dropped.
func htmlRefsIn(chunk []byte) []htmlRef {
	var refs []htmlRef
	for pos := 0; pos < len(chunk); {
		i := bytes.IndexByte(chunk[pos:], '<')
		if i < 0 {
			break
		}
		name, attrs, next := scanTag(chunk, pos+i)
		pos = next
		for _, a := range attrs {
			refs = append(refs, attrRefs(chunk, a, refAttrs[name])...)
		}
	}
	return refs
}

// attrRefs extracts the references one attribute carries: none when the tag
// does not use it or the value is empty, a candidate list for srcset, a single
// URL otherwise.
func attrRefs(chunk []byte, a htmlAttr, wanted []string) []htmlRef {
	if a.stop <= a.start || !slices.Contains(wanted, a.name) {
		return nil
	}
	if a.name == srcSetAttr {
		return srcSetRefs(chunk[a.start:a.stop], a.start)
	}
	return []htmlRef{{string(chunk[a.start:a.stop]), a.start, a.stop}}
}

// srcSetRefs splits a srcset value into its candidate URLs. It follows the HTML
// rule that a candidate's URL runs to the next whitespace — so the commas in a
// `data:` URI stay part of it — and that trailing commas end the candidate,
// otherwise a descriptor ("2x", "640w") runs to the next comma. offset is the
// value's own start, so the returned ranges are relative to the enclosing chunk.
func srcSetRefs(value []byte, offset int) []htmlRef {
	var refs []htmlRef
	for p := 0; p < len(value); {
		for p < len(value) && (isHTMLSpace(value[p]) || value[p] == ',') {
			p++
		}
		start := p
		for p < len(value) && !isHTMLSpace(value[p]) {
			p++
		}
		stop := p
		for stop > start && value[stop-1] == ',' {
			stop--
		}
		if stop > start {
			refs = append(refs, htmlRef{string(value[start:stop]), offset + start, offset + stop})
		}
		if stop == p { // no trailing comma, so a descriptor may follow
			p = skipSrcSetDescriptor(value, p)
		}
	}
	return refs
}

// skipSrcSetDescriptor advances past a candidate's descriptor, to just after
// the comma that ends it.
func skipSrcSetDescriptor(value []byte, p int) int {
	for p < len(value) && value[p] != ',' {
		p++
	}
	if p < len(value) {
		p++
	}
	return p
}

// htmlAttr is one parsed attribute: its lowercased name and the byte range of
// its value within the chunk.
type htmlAttr struct {
	name        string
	start, stop int
}

// scanTag parses the tag opening at chunk[i] and returns its lowercased name,
// its attributes, and the index just past the tag. A non-tag "<" yields an
// empty name and advances by one so the caller always makes progress.
func scanTag(chunk []byte, i int) (string, []htmlAttr, int) {
	p := i + 1
	if p >= len(chunk) || !isTagNameStart(chunk[p]) {
		return "", nil, i + 1
	}
	nameStart := p
	for p < len(chunk) && isTagNameChar(chunk[p]) {
		p++
	}
	name := strings.ToLower(string(chunk[nameStart:p]))

	var attrs []htmlAttr
	for p < len(chunk) && chunk[p] != '>' {
		if isHTMLSpace(chunk[p]) || chunk[p] == '/' {
			p++
			continue
		}
		attr, next := scanAttr(chunk, p)
		if next <= p {
			p++
			continue
		}
		p = next
		if attr.name != "" {
			attrs = append(attrs, attr)
		}
	}
	return name, attrs, min(p+1, len(chunk))
}

// scanAttr parses one name[=value] pair at p and returns the index just past
// it. A quoted value may hold any character up to its closing quote, including
// ">"; an unquoted one ends at the next whitespace or ">".
func scanAttr(chunk []byte, p int) (htmlAttr, int) {
	nameStart := p
	for p < len(chunk) && !isHTMLSpace(chunk[p]) && chunk[p] != '=' && chunk[p] != '>' && chunk[p] != '/' {
		p++
	}
	name := strings.ToLower(string(chunk[nameStart:p]))
	if name == "" {
		return htmlAttr{}, p
	}

	v := skipHTMLSpace(chunk, p)
	if v >= len(chunk) || chunk[v] != '=' {
		return htmlAttr{name: name, start: p, stop: p}, p
	}
	v = skipHTMLSpace(chunk, v+1)
	if v >= len(chunk) {
		return htmlAttr{name: name, start: v, stop: v}, v
	}

	if quote := chunk[v]; quote == '"' || quote == '\'' {
		v++
		start := v
		for v < len(chunk) && chunk[v] != quote {
			v++
		}
		return htmlAttr{name, start, v}, min(v+1, len(chunk))
	}
	start := v
	for v < len(chunk) && !isHTMLSpace(chunk[v]) && chunk[v] != '>' {
		v++
	}
	return htmlAttr{name, start, v}, v
}

func skipHTMLSpace(chunk []byte, p int) int {
	for p < len(chunk) && isHTMLSpace(chunk[p]) {
		p++
	}
	return p
}

func isHTMLSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	}
	return false
}

func isTagNameStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isTagNameChar(c byte) bool {
	return isTagNameStart(c) || c >= '0' && c <= '9' || c == '-'
}

// htmlSpan is a half-open byte range of the markdown body.
type htmlSpan struct{ start, stop int }

// htmlSpans returns the byte ranges of body that the parser classified as raw
// HTML — HTML blocks (including their closing line) and inline raw HTML —
// merged and in ascending order. Everything else, notably fenced code, is
// excluded, and merging keeps a tag split across source lines in one range.
func htmlSpans(body []byte) []htmlSpan {
	doc := mdParser.Parser().Parse(text.NewReader(body))

	var spans []htmlSpan
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) { //nolint:errcheck — walk never errors
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := n.(type) {
		case *ast.HTMLBlock:
			sp, ok := segmentSpan(t.Lines())
			if t.HasClosure() {
				sp, ok = extend(sp, ok, htmlSpan{t.ClosureLine.Start, t.ClosureLine.Stop})
			}
			if ok {
				spans = append(spans, sp)
			}
		case *ast.RawHTML:
			if sp, ok := segmentSpan(t.Segments); ok {
				spans = append(spans, sp)
			}
		}
		return ast.WalkContinue, nil
	})
	return mergeSpans(spans)
}

// segmentSpan collapses a node's line segments into the single range they
// bracket. The gaps between segments — continuation-line indentation the parser
// strips — belong to the node's source text and must not be split away, or a
// tag written across several lines would never match as one tag.
func segmentSpan(segs *text.Segments) (htmlSpan, bool) {
	if segs == nil || segs.Len() == 0 {
		return htmlSpan{}, false
	}
	return htmlSpan{segs.At(0).Start, segs.At(segs.Len() - 1).Stop}, true
}

// extend widens sp to also cover next, or returns next when sp is unset.
func extend(sp htmlSpan, ok bool, next htmlSpan) (htmlSpan, bool) {
	if !ok {
		return next, true
	}
	if next.stop > sp.stop {
		sp.stop = next.stop
	}
	return sp, true
}

// mergeSpans sorts spans and folds overlapping or adjacent ones together.
func mergeSpans(spans []htmlSpan) []htmlSpan {
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	var out []htmlSpan
	for _, s := range spans {
		last := len(out) - 1
		if last >= 0 && s.start <= out[last].stop {
			if s.stop > out[last].stop {
				out[last].stop = s.stop
			}
			continue
		}
		out = append(out, s)
	}
	return out
}

// ResolveLogicalPath resolves a local reference against the referrer's logical
// directory into a cleaned logical path. referrerDir is the project-root-relative
// directory of the referring file ("." for files at the project root). The result
// keeps a leading "../" when the reference escapes the project root.
func ResolveLogicalPath(referrerDir, ref string) string {
	return path.Clean(path.Join(referrerDir, strings.TrimPrefix(ref, "./")))
}

// IsExternalRef reports whether a reference points outside the bundle and must
// not be downloaded: protocol-relative ("//host/x") or any scheme ("https:",
// "data:", "mailto:"). Relative paths ("./x", "imgs/x", "../x") are local.
func IsExternalRef(ref string) bool {
	return strings.HasPrefix(ref, "//") || reURLScheme.MatchString(ref)
}
