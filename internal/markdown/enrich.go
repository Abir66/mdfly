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

// Placeholder CSS classes emitted for client-side enrichment (ADR-0018, S12).
// The ssr boot snippet keys off these to lazy-load Mermaid/KaTeX; nothing renders
// these server-side. They must satisfy the bluemonday class allow-list.
const (
	ClassMermaid     = "mermaid"
	ClassMathInline  = "math math-inline"
	ClassMathDisplay = "math math-display"
)

const mathDelim = '$'

var (
	kindMermaid = ast.NewNodeKind("Mermaid")
	kindMath    = ast.NewNodeKind("Math")
)

// enrichExtension wires the Mermaid placeholder transformer, the math inline
// parser, and their renderers into a goldmark instance.
type enrichExtension struct{}

func (enrichExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithASTTransformers(util.Prioritized(mermaidTransformer{}, 90)),
		parser.WithInlineParsers(util.Prioritized(mathParser{}, 500)),
	)
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(enrichRenderer{}, 100)))
}

type enrichRenderer struct{}

func (enrichRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMermaid, renderMermaid)
	reg.Register(kindMath, renderMath)
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

func (mermaidTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
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

// mathNode holds the raw TeX of a `$...$` (inline) or `$$...$$` (display) span.
type mathNode struct {
	ast.BaseInline
	tex     []byte
	display bool
}

func (*mathNode) Kind() ast.NodeKind              { return kindMath }
func (n *mathNode) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// mathParser recognizes inline `$...$` and single-line display `$$...$$` spans,
// emitting placeholders for client-side KaTeX. Multi-line display blocks are not
// yet handled (see S12 notes).
type mathParser struct{}

func (mathParser) Trigger() []byte { return []byte{mathDelim} }

func (mathParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if len(line) < 2 || line[0] != mathDelim {
		return nil
	}
	if line[1] == mathDelim {
		idx := bytes.Index(line[2:], []byte{mathDelim, mathDelim})
		if idx <= 0 {
			return nil
		}
		block.Advance(2 + idx + 2)
		return newMath(line[2:2+idx], true)
	}
	idx := bytes.IndexByte(line[1:], mathDelim)
	if idx <= 0 || !isMathContent(line[1:1+idx]) {
		return nil
	}
	block.Advance(1 + idx + 1)
	return newMath(line[1:1+idx], false)
}

// isMathContent rejects spans padded with spaces (e.g. "$5 and $") so that lone
// currency dollars are left as plain text.
func isMathContent(c []byte) bool {
	return len(c) > 0 && c[0] != ' ' && c[len(c)-1] != ' '
}

func newMath(src []byte, display bool) *mathNode {
	tex := make([]byte, len(src))
	copy(tex, src)
	return &mathNode{tex: tex, display: display}
}

func renderMath(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*mathNode)
	tag, class := "span", ClassMathInline
	if n.display {
		tag, class = "div", ClassMathDisplay
	}
	w.WriteString("<" + tag + ` class="` + class + `">`)
	template.HTMLEscape(w, n.tex)
	w.WriteString("</" + tag + ">")
	return ast.WalkSkipChildren, nil
}
