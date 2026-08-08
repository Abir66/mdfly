// Package ssr renders full HTML pages from pre-computed metadata and a rendered body.
package ssr

import (
	"bytes"
	_ "embed"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/Abir66/mdfly/internal/server/filetree"
)

//go:embed page.html.tmpl
var pageTemplateText string

var pageTmpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"node": func(slug string, depth int, n *filetree.TreeNode) nodePair { return nodePair{slug, depth, n} },
	"inc":  func(i int) int { return i + 1 },
	"href": nodeHref,
}).Parse(pageTemplateText))

// PageData holds the data needed to render a full HTML page. When Tree is nil the
// page renders bare (head + body), preserving the pre-chrome contract; when set,
// the full Viewer chrome (sidebar tree, breadcrumb, mobile drawer) wraps Body.
type PageData struct {
	Title      string
	Excerpt    string
	OGImageURL string
	Body       template.HTML
	// EnrichMermaid/EnrichMath gate the client-side boot snippet. The caller
	// sets these from the renderer's report of what placeholders it emitted —
	// the body is never re-scanned.
	EnrichMermaid bool
	EnrichMath    bool

	// Viewer chrome (ADR-0010). Slug addresses tree/breadcrumb links; Tree and
	// Breadcrumb come from the filetree package; CSSURL/JSURL are the hashed
	// /_static asset URLs.
	Slug string
	// Sidebar gates the file-tree sidebar, its mobile drawer top bar, and the
	// breadcrumb. It is false for a single-file bundle, where all three would
	// address a one-entry tree — the page then renders content plus footer only.
	Sidebar bool
	// CodeFile marks a whole-file source view (S24 text preview). It renders full
	// width in a dedicated .code-file container with a line-number gutter, rather
	// than in the reading-width prose column used for markdown.
	CodeFile   bool
	Tree       *filetree.TreeNode
	Breadcrumb []filetree.Crumb
	CSSURL     string
	JSURL      string
	// UpdatedAt is the bundle's last publish time, shown in the footer. A zero
	// value omits the line.
	UpdatedAt time.Time
	// Listing is the addressed directory's immediate children (S23). When set,
	// the center renders a Directory Listing above Body; Body then carries the
	// directory's rendered index document (README/index.md), or is empty.
	Listing []filetree.Entry

	// Image and Download are the non-markdown asset centers (S24); at most one is
	// set. Image renders an inline <img> from the CDN; Download renders a card
	// (name, size, Download link) for oversize/binary/other files.
	Image    *Asset
	Download *Asset
}

// Asset describes a non-markdown node's center: its display name, CDN blob URL,
// and byte size. Image centers ignore Size; download cards use all three.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// nodePair carries the slug alongside a tree node so the recursive tree template
// can build per-node hrefs (html/template can't pass two values to a sub-template).
type nodePair struct {
	Slug  string
	Depth int
	Node  *filetree.TreeNode
}

// pageView is PageData plus the computed boot + rail snippets and footer
// timestamp passed to the template. BootScript is empty for documents with no
// Mermaid/KaTeX placeholders; RailScript is empty when no chrome is rendered;
// UpdatedLabel is empty when PageData.UpdatedAt is unset.
type pageView struct {
	PageData
	BootScript   template.HTML
	RailScript   template.HTML
	UpdatedLabel string
}

// updatedLayout renders the footer timestamp in UTC, e.g. "Apr 20, 2026 at 2:45 PM UTC".
const updatedLayout = "Jan 2, 2006 at 3:04 PM MST"

// railStorageKey is the localStorage key holding the collapsed-rail boolean.
const railStorageKey = "mdfly:sidebar-rail"

// railSnippet applies the persisted collapsed-rail state to <html> before first
// paint (in <head>), so the rail doesn't re-expand on every full-reload nav.
const railSnippet = "(function(){try{if(localStorage.getItem('" + railStorageKey +
	"')==='1')document.documentElement.classList.add('rail');}catch(e){}})();"

// RenderPage executes the page template and returns the full HTML document.
func RenderPage(data PageData) (string, error) {
	var buf bytes.Buffer
	view := pageView{
		PageData:     data,
		BootScript:   bootScript(data.EnrichMermaid, data.EnrichMath),
		RailScript:   railScript(data.Tree != nil),
		UpdatedLabel: updatedLabel(data.UpdatedAt),
	}
	if err := pageTmpl.Execute(&buf, view); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// updatedLabel formats t for the footer, or returns "" when t is unset.
func updatedLabel(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(updatedLayout)
}

// railScript wraps railSnippet in a <script> tag when the chrome is rendered.
func railScript(chrome bool) template.HTML {
	if !chrome {
		return ""
	}
	return template.HTML("<script>" + railSnippet + "</script>")
}

// nodeHref builds the viewer URL for a tree node or breadcrumb crumb. An empty
// path is the bundle root (/{slug}); a path with leading "../" segments encodes
// its depth as ?up=N because proxies strip dot-segments (ADR-0010).
func nodeHref(slug, p string) string {
	up := 0
	for strings.HasPrefix(p, "../") {
		up++
		p = p[len("../"):]
	}
	url := "/" + slug
	if p != "" {
		url += "/" + p
	}
	if up > 0 {
		url += "?up=" + strconv.Itoa(up)
	}
	return url
}
