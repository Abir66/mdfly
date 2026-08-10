package ssr_test

import (
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/server/filetree"
	"github.com/Abir66/mdfly/internal/server/ssr"
)

func chromeData() ssr.PageData {
	keys := []string{"index.md", "docs/api/auth.md", "assets/logo.png"}
	const current = "docs/api/auth.md"
	return ssr.PageData{
		Title:      "Auth",
		Body:       template.HTML("<h1>Auth</h1>"),
		Slug:       "abc12345",
		Sidebar:    true,
		Tree:       filetree.BuildTree(keys, current),
		Breadcrumb: filetree.Breadcrumb(current),
		CSSURL:     "/_static/app.deadbeef12.css",
		JSURL:      "/_static/app.cafebabe34.js",
	}
}

func TestRenderPage_SinglePageHidesSidebar(t *testing.T) {
	keys := []string{"foo.md"}
	data := ssr.PageData{
		Title:      "Foo",
		Body:       template.HTML("<h1>Foo</h1>"),
		Slug:       "abc12345",
		Sidebar:    false,
		Tree:       filetree.BuildTree(keys, "foo.md"),
		Breadcrumb: filetree.Breadcrumb("foo.md"),
		CSSURL:     "/_static/app.deadbeef12.css",
		JSURL:      "/_static/app.cafebabe34.js",
	}
	out, err := ssr.RenderPage(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `<aside class="sidebar">`) {
		t.Errorf("single-page must omit the sidebar:\n%s", out)
	}
	if strings.Contains(out, `<header class="topbar">`) {
		t.Errorf("single-page must omit the mobile drawer top bar:\n%s", out)
	}
	if !strings.Contains(out, "no-sidebar") {
		t.Errorf("single-page viewer must carry the no-sidebar class:\n%s", out)
	}
	// Content still renders.
	if !strings.Contains(out, "<h1>Foo</h1>") {
		t.Errorf("single-page body missing:\n%s", out)
	}
}

func TestRenderPage_SinglePageHidesBreadcrumb(t *testing.T) {
	data := chromeData()
	data.Sidebar = false
	out, err := ssr.RenderPage(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `class="breadcrumb"`) {
		t.Errorf("single-page must omit the breadcrumb:\n%s", out)
	}
}

func TestRenderPage_Footer(t *testing.T) {
	out, err := ssr.RenderPage(chromeData())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<footer class="site-footer">`) {
		t.Errorf("view chrome missing footer:\n%s", out)
	}
	if !strings.Contains(out, `href="https://www.mdfly.dev"`) {
		t.Errorf("footer missing mdFly attribution link:\n%s", out)
	}
	if !strings.Contains(out, "Published with") || !strings.Contains(out, "MdFly") {
		t.Errorf("footer missing attribution text:\n%s", out)
	}
}

func TestRenderPage_FooterUpdatedAt(t *testing.T) {
	data := chromeData()
	data.UpdatedAt = time.Date(2026, time.April, 20, 14, 45, 0, 0, time.UTC)
	out, err := ssr.RenderPage(data)
	if err != nil {
		t.Fatal(err)
	}
	// The UTC label is the no-JS fallback; the machine-readable instant lets the
	// client rewrite it in the reader's own zone.
	if !strings.Contains(out, "Last updated: ") || !strings.Contains(out, "Apr 20, 2026 at 2:45 PM UTC") {
		t.Errorf("footer missing formatted update time:\n%s", out)
	}
	if !strings.Contains(out, `datetime="2026-04-20T14:45:00Z"`) {
		t.Errorf("footer time missing machine-readable instant:\n%s", out)
	}
}

func TestRenderPage_FooterOmitsZeroUpdatedAt(t *testing.T) {
	out, err := ssr.RenderPage(chromeData())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Last updated") {
		t.Errorf("footer must omit the update line when the time is unknown:\n%s", out)
	}
}

func TestRenderPage_FooterOnSinglePage(t *testing.T) {
	data := chromeData()
	data.Sidebar = false
	out, err := ssr.RenderPage(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<footer class="site-footer">`) {
		t.Errorf("single-page must still render the footer:\n%s", out)
	}
}

func TestRenderPage_LogoGradientIDsUnique(t *testing.T) {
	// Sidebar and footer both render the logo; a shared SVG gradient id is
	// invalid and breaks the footer logo's fill when the sidebar collapses to a
	// rail (its def is display:none). Each instance must own a distinct id.
	out, err := ssr.RenderPage(chromeData())
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, `id="mdfly_logo_grad"`); n != 1 {
		t.Errorf("logo gradient id must be unique per instance, found %d of mdfly_logo_grad:\n%s", n, out)
	}
	if !strings.Contains(out, `class="footer-brand"`) {
		t.Errorf("footer brand logo missing:\n%s", out)
	}
	_, footerSVG, found := strings.Cut(out, `class="footer-brand"`)
	if !found {
		t.Fatalf("footer-brand not found:\n%s", out)
	}
	fillStart := strings.Index(footerSVG, `fill="url(#`)
	if fillStart == -1 {
		t.Fatalf("footer logo missing gradient fill reference:\n%s", footerSVG)
	}
	idStart := fillStart + len(`fill="url(#`)
	idEnd := strings.Index(footerSVG[idStart:], `)`)
	if idEnd == -1 {
		t.Fatalf("footer logo gradient fill reference malformed:\n%s", footerSVG)
	}
	footerGradID := footerSVG[idStart : idStart+idEnd]
	if footerGradID == "mdfly_logo_grad" {
		t.Errorf("footer logo must reference its own distinct gradient id, not the sidebar's:\n%s", footerSVG)
	}
	if !strings.Contains(footerSVG, `id="`+footerGradID+`"`) {
		t.Errorf("footer logo references gradient id %q that is never defined:\n%s", footerGradID, footerSVG)
	}
}

func TestRenderPage_CodeFileWide(t *testing.T) {
	data := chromeData()
	data.CodeFile = true
	data.Body = template.HTML(`<div><table><tr><td><pre>1</pre></td><td><pre>x</pre></td></tr></table></div>`)
	out, err := ssr.RenderPage(data)
	if err != nil {
		t.Fatal(err)
	}
	// A code file spans full width in a dedicated container, not the prose column.
	if !strings.Contains(out, `class="code-file"`) {
		t.Errorf("code file missing .code-file container:\n%s", out)
	}
	if !strings.Contains(out, "center center-wide") {
		t.Errorf("code file must widen the center column:\n%s", out)
	}
	if strings.Contains(out, `<article class="content">`) {
		t.Errorf("code file must not use the prose content article:\n%s", out)
	}
}

func TestRenderPage_MarkdownUsesProseColumn(t *testing.T) {
	data := chromeData()
	out, err := ssr.RenderPage(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<article class="content">`) {
		t.Errorf("markdown must render in the prose content article:\n%s", out)
	}
	if strings.Contains(out, "center-wide") {
		t.Errorf("markdown must keep the reading-width column:\n%s", out)
	}
}

func TestRenderPage_ChromeStaticLinks(t *testing.T) {
	out, err := ssr.RenderPage(chromeData())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `href="/_static/app.deadbeef12.css"`) {
		t.Errorf("missing hashed CSS link:\n%s", out)
	}
	if !strings.Contains(out, `src="/_static/app.cafebabe34.js"`) {
		t.Errorf("missing hashed JS script:\n%s", out)
	}
}

func TestRenderPage_ChromeTree(t *testing.T) {
	out, err := ssr.RenderPage(chromeData())
	if err != nil {
		t.Fatal(err)
	}
	// File links keep their extension and are addressed under the slug.
	if !strings.Contains(out, `href="/abc12345/docs/api/auth.md"`) {
		t.Errorf("tree missing extension-kept file link:\n%s", out)
	}
	if !strings.Contains(out, `href="/abc12345/index.md"`) {
		t.Errorf("tree missing root file link:\n%s", out)
	}
	// The current file's ancestor folders auto-expand via the .open class.
	if c := strings.Count(out, `class="tree-folder open"`); c < 2 {
		t.Errorf("expected ancestor dirs (docs, docs/api) open, got %d open folders:\n%s", c, out)
	}
	// Folders are navigable links, with a separate chevron toggle.
	if !strings.Contains(out, `data-action="toggle-folder"`) {
		t.Errorf("tree missing folder chevron toggle:\n%s", out)
	}
	if !strings.Contains(out, `class="folder-link" href="/abc12345/docs"`) {
		t.Errorf("tree missing navigable folder link:\n%s", out)
	}
	// The current node is highlighted.
	if !strings.Contains(out, `aria-current="page"`) {
		t.Errorf("current node missing aria-current:\n%s", out)
	}
}

func TestRenderPage_ChromeBreadcrumb(t *testing.T) {
	out, err := ssr.RenderPage(chromeData())
	if err != nil {
		t.Fatal(err)
	}
	// Home crumb first → bundle root.
	if !strings.Contains(out, `class="breadcrumb"`) {
		t.Errorf("missing breadcrumb:\n%s", out)
	}
	if !strings.Contains(out, `href="/abc12345"`) {
		t.Errorf("breadcrumb missing home crumb → /slug:\n%s", out)
	}
	if !strings.Contains(out, `href="/abc12345/docs"`) {
		t.Errorf("breadcrumb missing intermediate crumb:\n%s", out)
	}
}

func TestRenderPage_ChromeMobileAndRailSnippet(t *testing.T) {
	out, err := ssr.RenderPage(chromeData())
	if err != nil {
		t.Fatal(err)
	}
	// Mobile top bar carries the drawer toggle and the wordmark.
	if !strings.Contains(out, `<header class="topbar">`) {
		t.Errorf("missing mobile top bar:\n%s", out)
	}
	if !strings.Contains(out, `data-action="toggle-drawer"`) {
		t.Errorf("missing mobile drawer toggle:\n%s", out)
	}
	if !strings.Contains(out, `<span class="wordmark">MdFly</span>`) {
		t.Errorf("top bar missing wordmark:\n%s", out)
	}
	// Desktop sidebar header owns the rail-collapse control (no global top bar).
	if !strings.Contains(out, `data-action="toggle-rail"`) {
		t.Errorf("missing sidebar rail-collapse toggle:\n%s", out)
	}
	// Inline <head> snippet applies persisted rail state before paint.
	if !strings.Contains(out, "mdfly:sidebar-rail") {
		t.Errorf("missing inline rail-state snippet:\n%s", out)
	}
}

func dirListingData() ssr.PageData {
	keys := []string{"index.md", "docs/api/auth.md", "docs/guide.md", "assets/logo.png"}
	const current = "docs"
	return ssr.PageData{
		Slug:       "abc12345",
		Tree:       filetree.BuildTree(keys, current),
		Breadcrumb: filetree.Breadcrumb(current),
		Listing:    filetree.ListDir(map[string]int64{"docs/api/auth.md": 30, "docs/guide.md": 40}, current),
		CSSURL:     "/_static/app.deadbeef12.css",
		JSURL:      "/_static/app.cafebabe34.js",
	}
}

func TestRenderPage_DirectoryListing(t *testing.T) {
	out, err := ssr.RenderPage(dirListingData())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `class="listing"`) {
		t.Errorf("missing listing block:\n%s", out)
	}
	// Folder child is clickable and marked as a directory.
	if !strings.Contains(out, `href="/abc12345/docs/api"`) {
		t.Errorf("listing missing folder link:\n%s", out)
	}
	// File child is clickable and reports its size.
	if !strings.Contains(out, `href="/abc12345/docs/guide.md"`) {
		t.Errorf("listing missing file link:\n%s", out)
	}
	if !strings.Contains(out, "40") {
		t.Errorf("listing missing file size:\n%s", out)
	}
	// Folders sort before files: api/ precedes guide.md in the rendered list.
	if strings.Index(out, "docs/api") > strings.Index(out, "docs/guide.md") {
		t.Errorf("folders must list before files:\n%s", out)
	}
}

func TestRenderPage_DirectoryListingWithReadme(t *testing.T) {
	data := dirListingData()
	data.Body = template.HTML("<h1>Docs Readme</h1>")
	out, err := ssr.RenderPage(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `class="listing"`) {
		t.Errorf("missing listing block:\n%s", out)
	}
	if !strings.Contains(out, "<h1>Docs Readme</h1>") {
		t.Errorf("rendered README must appear below listing:\n%s", out)
	}
	if strings.Index(out, `class="listing"`) > strings.Index(out, "Docs Readme") {
		t.Errorf("listing must precede the README body:\n%s", out)
	}
}

func TestRenderPage_ImageCenter(t *testing.T) {
	data := chromeData()
	data.Image = &ssr.Asset{Name: "logo.png", URL: "https://storage.mdfly.dev/documents/abc12345/ph.png"}
	out, err := ssr.RenderPage(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<img class="asset-image" src="https://storage.mdfly.dev/documents/abc12345/ph.png" alt="logo.png">`) {
		t.Errorf("image center missing inline img:\n%s", out)
	}
}

func TestRenderPage_DownloadCard(t *testing.T) {
	data := chromeData()
	data.Download = &ssr.Asset{Name: "report.pdf", URL: "https://storage.mdfly.dev/documents/abc12345/dh.pdf", Size: 4096}
	out, err := ssr.RenderPage(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `class="download-card"`) {
		t.Errorf("download card missing:\n%s", out)
	}
	if !strings.Contains(out, "report.pdf") {
		t.Errorf("download card missing file name:\n%s", out)
	}
	if !strings.Contains(out, "4096 bytes") {
		t.Errorf("download card missing size:\n%s", out)
	}
	if !strings.Contains(out, `href="https://storage.mdfly.dev/documents/abc12345/dh.pdf" download`) {
		t.Errorf("download card missing CDN download link:\n%s", out)
	}
}

func TestRenderPage_TitleInHead(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{Title: "Hello World", Body: template.HTML("<p>body</p>")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<title>Hello World</title>") {
		t.Errorf("missing <title>: %s", out)
	}
	if !strings.Contains(out, `og:title" content="Hello World"`) {
		t.Errorf("missing og:title: %s", out)
	}
}

func TestRenderPage_DescriptionMeta(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{Excerpt: "A short excerpt.", Body: template.HTML("<p>body</p>")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `name="description" content="A short excerpt."`) {
		t.Errorf("missing description meta: %s", out)
	}
	if !strings.Contains(out, `og:description" content="A short excerpt."`) {
		t.Errorf("missing og:description: %s", out)
	}
}

func TestRenderPage_OGImage(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{OGImageURL: "https://storage.mdfly.dev/x.png", Body: template.HTML("<p>body</p>")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `og:image" content="https://storage.mdfly.dev/x.png"`) {
		t.Errorf("missing og:image: %s", out)
	}
	if !strings.Contains(out, `twitter:card" content="summary_large_image"`) {
		t.Errorf("missing twitter:card: %s", out)
	}
}

func TestRenderPage_BodyPresent(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{Body: template.HTML("<h1>Doc</h1>")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<h1>Doc</h1>") {
		t.Errorf("body missing from output: %s", out)
	}
}

func TestRenderPage_EmptyTitleFallback(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{Body: template.HTML("<p>no heading</p>")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<title>mdfly</title>") {
		t.Errorf("expected fallback title 'mdfly': %s", out)
	}
}

func TestRenderPage_MermaidBootSnippet(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{Body: template.HTML("<pre>g</pre>"), EnrichMermaid: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cdn.jsdelivr.net/npm/mermaid@") {
		t.Errorf("expected lazy-loaded mermaid bundle from jsdelivr:\n%s", out)
	}
	if strings.Contains(out, "katex@") {
		t.Errorf("katex must not load without math nodes:\n%s", out)
	}
	if !strings.Contains(out, "startOnLoad:false") || !strings.Contains(out, "mermaid.run()") {
		t.Errorf("mermaid must initialize with startOnLoad:false and call mermaid.run() after async load:\n%s", out)
	}
}

func TestContentSecurityPolicy_NoUnsafeInlineScript(t *testing.T) {
	_, scriptSrc, found := strings.Cut(ssr.ContentSecurityPolicy, "script-src")
	if !found {
		t.Fatalf("CSP missing script-src directive: %q", ssr.ContentSecurityPolicy)
	}
	if strings.Contains(scriptSrc, "'unsafe-inline'") {
		t.Errorf("script-src must not allow 'unsafe-inline': %q", scriptSrc)
	}
	if !strings.Contains(scriptSrc, "'sha256-") {
		t.Errorf("script-src must pin the boot snippet by sha256 hash: %q", scriptSrc)
	}
}

func TestRenderPage_MathBootSnippet(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{Body: template.HTML("<span>x</span>"), EnrichMath: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cdn.jsdelivr.net/npm/katex@") {
		t.Errorf("expected lazy-loaded katex bundle from jsdelivr:\n%s", out)
	}
	if strings.Contains(out, "mermaid@") {
		t.Errorf("mermaid must not load without mermaid nodes:\n%s", out)
	}
}

func TestRenderPage_BothBootSnippet(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{Body: template.HTML("<p>x</p>"), EnrichMermaid: true, EnrichMath: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mermaid@") || !strings.Contains(out, "katex@") {
		t.Errorf("expected both mermaid and katex to load:\n%s", out)
	}
}

func TestRenderPage_NoBootSnippetWhenPlain(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{Body: template.HTML(`<pre class="mermaid">not real</pre>`)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<script") {
		t.Errorf("plain document must carry no enrichment script:\n%s", out)
	}
	if strings.Contains(out, "jsdelivr") {
		t.Errorf("plain document must not reference the CDN:\n%s", out)
	}
}

func TestRenderPage_NoOGImageWhenEmpty(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{Title: "T", Body: template.HTML("<p>b</p>")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "og:image") {
		t.Errorf("og:image should be absent when OGImageURL empty: %s", out)
	}
}
