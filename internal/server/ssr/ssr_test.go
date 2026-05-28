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

func TestRenderPage_NoOGImageWhenEmpty(t *testing.T) {
	out, err := ssr.RenderPage(ssr.PageData{Title: "T", Body: template.HTML("<p>b</p>")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "og:image") {
		t.Errorf("og:image should be absent when OGImageURL empty: %s", out)
	}
}
