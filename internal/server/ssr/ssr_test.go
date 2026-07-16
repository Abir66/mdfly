package ssr_test

import (
	"html/template"
	"strings"
	"testing"

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
		Tree:       filetree.BuildTree(keys, current),
		Breadcrumb: filetree.Breadcrumb(current),
		CSSURL:     "/_static/app.deadbeef12.css",
		JSURL:      "/_static/app.cafebabe34.js",
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
	data.Image = &ssr.Asset{Name: "logo.png", URL: "https://cdn.mdfly.dev/documents/abc12345/ph.png"}
	out, err := ssr.RenderPage(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<img class="asset-image" src="https://cdn.mdfly.dev/documents/abc12345/ph.png" alt="logo.png">`) {
		t.Errorf("image center missing inline img:\n%s", out)
	}
}

func TestRenderPage_DownloadCard(t *testing.T) {
	data := chromeData()
	data.Download = &ssr.Asset{Name: "report.pdf", URL: "https://cdn.mdfly.dev/documents/abc12345/dh.pdf", Size: 4096}
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
	if !strings.Contains(out, `href="https://cdn.mdfly.dev/documents/abc12345/dh.pdf" download`) {
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
	out, err := ssr.RenderPage(ssr.PageData{OGImageURL: "https://cdn.mdfly.dev/x.png", Body: template.HTML("<p>body</p>")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `og:image" content="https://cdn.mdfly.dev/x.png"`) {
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
