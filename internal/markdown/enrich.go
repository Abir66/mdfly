package markdown

import (
	"bytes"
	"html/template"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Placeholder CSS classes emitted for client-side enrichment (ADR-0010, S12).
// The ssr boot snippet keys off the per-render enrichFlags (not these strings)
// to lazy-load Mermaid/KaTeX; nothing renders these server-side. They must
// satisfy the bluemonday class allow-list.
const (
	ClassMermaid     = "mermaid"
	ClassMathInline  = "math math-inline"
	ClassMathDisplay = "math math-display"
)

const mathDelim = '$'

var mathFence = []byte{mathDelim, mathDelim}

var (
	kindMermaid    = ast.NewNodeKind("Mermaid")
	kindMathInline = ast.NewNodeKind("MathInline")
	kindMathBlock  = ast.NewNodeKind("MathBlock")
)

// enrichFlags records which enrichment placeholders a single render emitted, so
// callers can gate the client-side boot snippet without re-scanning the HTML.
type enrichFlags struct {
	mermaid bool
	math    bool
}

var enrichFlagsKey = parser.NewContextKey()

func enrichFlagsFrom(pc parser.Context) *enrichFlags {
	f, _ := pc.Get(enrichFlagsKey).(*enrichFlags)
	return f
}

// enrichExtension wires the Mermaid placeholder transformer, the math parsers,
// and their renderers into a goldmark instance.
type enrichExtension struct{}

func (enrichExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithASTTransformers(util.Prioritized(mermaidTransformer{}, 90)),
		parser.WithBlockParsers(util.Prioritized(mathBlockParser{}, 100)),
		parser.WithInlineParsers(util.Prioritized(mathInlineParser{}, 500)),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(enrichRenderer{}, 100)))
}

type enrichRenderer struct{}

func (enrichRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMermaid, renderMermaid)
	reg.Register(kindMathInline, renderMathInline)
	reg.Register(kindMathBlock, renderMathBlock)
}

// mermaidBlock holds the raw source of a ```mermaid fenced block.
type mermaidBlock struct {
	ast.BaseBlock
	source []byte
}

func (*mermaidBlock) Kind() ast.NodeKind              { return kindMermaid }
func (n *mermaidBlock) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// mermaidTransformer replaces ```mermaid fenced code blocks with a mermaidBlock
// so they bypass chroma highlighting and render as a client-side placeholder.
type mermaidTransformer struct{}

func (mermaidTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	source := reader.Source()
	var targets []*ast.FencedCodeBlock
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) { //nolint:errcheck — walk never errors
		if entering {
			if fc, ok := n.(*ast.FencedCodeBlock); ok && string(fc.Language(source)) == ClassMermaid {
				targets = append(targets, fc)
			}
		}
		return ast.WalkContinue, nil
	})
	if len(targets) == 0 {
		return
	}
	if f := enrichFlagsFrom(pc); f != nil {
		f.mermaid = true
	}
	for _, fc := range targets {
		mb := &mermaidBlock{source: blockText(fc, source)}
		fc.Parent().ReplaceChild(fc.Parent(), fc, mb)
	}
}

func blockText(n ast.Node, source []byte) []byte {
	var buf bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		buf.Write(seg.Value(source))
	}
	return buf.Bytes()
}

func renderMermaid(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	w.WriteString(`<pre class="` + ClassMermaid + `">`)
	template.HTMLEscape(w, node.(*mermaidBlock).source)
	w.WriteString("</pre>\n")
	return ast.WalkSkipChildren, nil
}

// mathInline holds the raw TeX of an inline `$...$` span.
type mathInline struct {
	ast.BaseInline
	tex []byte
}

func (*mathInline) Kind() ast.NodeKind          { return kindMathInline }
func (n *mathInline) Dump(source []byte, l int) { ast.DumpHelper(n, source, l, nil, nil) }

// mathBlock holds the raw TeX of a display `$$...$$` span (one or more lines).
// The TeX is kept as Lines() segments, reconstructed from source at render —
// the idiom goldmark's own fenced-code block uses for multi-line raw content.
type mathBlock struct {
	ast.BaseBlock
}

func (*mathBlock) Kind() ast.NodeKind          { return kindMathBlock }
func (n *mathBlock) Dump(source []byte, l int) { ast.DumpHelper(n, source, l, nil, nil) }

// mathInlineParser recognizes inline `$...$` spans. Display `$$...$$` is handled
// by mathBlockParser, so a `$$` start here is left for it.
type mathInlineParser struct{}

func (mathInlineParser) Trigger() []byte { return []byte{mathDelim} }

func (mathInlineParser) Parse(_ ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 2 || line[0] != mathDelim || line[1] == mathDelim {
		return nil
	}
	idx := bytes.IndexByte(line[1:], mathDelim)
	if idx <= 0 || !isMathContent(line[1:1+idx]) {
		return nil
	}
	block.Advance(1 + idx + 1)
	flagMath(pc)
	return &mathInline{tex: clone(line[1 : 1+idx])}
}

// isMathContent rejects spans padded with spaces (e.g. "$5 and $") so that lone
// currency dollars are left as plain text.
func isMathContent(c []byte) bool {
	return len(c) > 0 && c[0] != ' ' && c[len(c)-1] != ' '
}

// mathBlockParser recognizes display math fenced by `$$`, on one line
// (`$$a+b$$`) or spanning several lines.
type mathBlockParser struct{}

func (mathBlockParser) Trigger() []byte { return []byte{mathDelim} }

// Open/Continue follow goldmark's fenced-code idiom: never advance the opening
// line (goldmark advances whole lines between calls); within a line use
// AdvanceToEOL. Manual byte-advancing the full line double-advances and swallows
// content lines.
func (mathBlockParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, seg := reader.PeekLine()
	if len(line) < 2 || line[0] != mathDelim || line[1] != mathDelim {
		return nil, parser.NoChildren
	}
	flagMath(pc)
	node := &mathBlock{}
	rest := line[2:]
	if idx := bytes.Index(rest, mathFence); idx >= 0 {
		node.Lines().Append(text.NewSegment(seg.Start+2, seg.Start+2+idx))
		return node, parser.Close | parser.NoChildren
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		node.Lines().Append(text.NewSegment(seg.Start+2, seg.Stop))
	}
	return node, parser.NoChildren
}

func (mathBlockParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	line, seg := reader.PeekLine()
	if idx := bytes.Index(line, mathFence); idx >= 0 {
		if idx > 0 {
			node.Lines().Append(text.NewSegment(seg.Start, seg.Start+idx))
		}
		reader.AdvanceToEOL()
		return parser.Close
	}
	node.Lines().Append(seg)
	reader.AdvanceToEOL()
	return parser.Continue | parser.NoChildren
}

func (mathBlockParser) Close(_ ast.Node, _ text.Reader, _ parser.Context) {}
func (mathBlockParser) CanInterruptParagraph() bool                       { return true }
func (mathBlockParser) CanAcceptIndentedLine() bool                       { return false }

func flagMath(pc parser.Context) {
	if f := enrichFlagsFrom(pc); f != nil {
		f.math = true
	}
}

func clone(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

func renderMathInline(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	return writeMath(w, "span", ClassMathInline, node.(*mathInline).tex)
}

func renderMathBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	return writeMath(w, "div", ClassMathDisplay, blockText(node, source))
}

func writeMath(w util.BufWriter, tag, class string, tex []byte) (ast.WalkStatus, error) {
	w.WriteString("<" + tag + ` class="` + class + `">`)
	template.HTMLEscape(w, bytes.TrimSpace(tex))
	w.WriteString("</" + tag + ">")
	return ast.WalkSkipChildren, nil
}
