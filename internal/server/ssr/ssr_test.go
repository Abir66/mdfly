package ssr_test

import (
	"html/template"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/server/ssr"
)

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
