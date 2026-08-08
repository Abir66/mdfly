package markdown_test

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/markdown"
)

var update = flag.Bool("update", false, "update golden files")

func TestRender_Golden(t *testing.T) {
	matches, err := filepath.Glob("testdata/*/input.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no testdata cases found")
	}
	for _, inputPath := range matches {
		name := filepath.Base(filepath.Dir(inputPath))
		t.Run(name, func(t *testing.T) {
			input, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := markdown.Render(input)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			goldPath := filepath.Join(filepath.Dir(inputPath), "expected.html")
			if *update {
				if err := os.WriteFile(goldPath, got, 0644); err != nil {
					t.Fatal(err)
				}
				return
			}
			expected, err := os.ReadFile(goldPath)
			if err != nil {
				t.Fatalf("golden file missing at %s; run: go test ./internal/markdown/... -update", goldPath)
			}
			if string(got) != string(expected) {
				t.Errorf("output mismatch for %s\ngot:\n%s\nwant:\n%s", name, got, expected)
			}
		})
	}
}

func TestRender_ScriptTagAbsent(t *testing.T) {
	input := []byte("# Title\n\n<script>alert(1)</script>\n")
	got, _, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "<script") {
		t.Errorf("output contains <script> tag:\n%s", got)
	}
	if strings.Contains(string(got), "alert(1)") {
		t.Errorf("output contains script content:\n%s", got)
	}
}

func TestRender_OnerrorAttrAbsent(t *testing.T) {
	input := []byte("# Title\n\n<img src=\"x\" onerror=\"alert(1)\">\n")
	got, _, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "onerror") {
		t.Errorf("output contains onerror attribute:\n%s", got)
	}
}

func TestRender_MermaidPlaceholder(t *testing.T) {
	input := []byte("```mermaid\ngraph TD\n    A --> B\n```\n")
	got, _, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `<pre class="mermaid">`) {
		t.Errorf("missing mermaid placeholder:\n%s", s)
	}
	if !strings.Contains(s, "graph TD") || !strings.Contains(s, "A --&gt; B") {
		t.Errorf("mermaid source not preserved/escaped:\n%s", s)
	}
	if strings.Contains(s, "language-mermaid") {
		t.Errorf("mermaid must not render as a highlighted code block:\n%s", s)
	}
}

func TestRender_MathInlinePlaceholder(t *testing.T) {
	got, _, err := markdown.Render([]byte("Euler: $x^2$ done.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(got); !strings.Contains(s, `<span class="math math-inline">x^2</span>`) {
		t.Errorf("missing inline math placeholder:\n%s", s)
	}
}

func TestRender_MathDisplayPlaceholder(t *testing.T) {
	got, _, err := markdown.Render([]byte("$$a+b=c$$\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(got); !strings.Contains(s, `<div class="math math-display">a+b=c</div>`) {
		t.Errorf("missing display math placeholder:\n%s", s)
	}
}

func TestRender_MathDisplayMultiline(t *testing.T) {
	got, _, err := markdown.Render([]byte("$$\na + b\n= c\n$$\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(got); !strings.Contains(s, `<div class="math math-display">a + b`) || !strings.Contains(s, "= c</div>") {
		t.Errorf("multiline display math not captured:\n%s", s)
	}
}

func TestRender_MetaEnrichFlags(t *testing.T) {
	cases := []struct {
		name          string
		md            string
		mermaid, math bool
	}{
		{"mermaid", "```mermaid\ngraph TD\n```\n", true, false},
		{"inline-math", "value $x^2$ here\n", false, true},
		{"display-math", "$$x^2$$\n", false, true},
		{"both", "$x$\n\n```mermaid\ng\n```\n", true, true},
		{"plain", "# Title\n\njust text\n", false, false},
		{"class-string-in-code", "```go\nx := `class=\"mermaid\"`\n```\n", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, meta, err := markdown.Render([]byte(c.md))
			if err != nil {
				t.Fatal(err)
			}
			if meta.HasMermaid != c.mermaid {
				t.Errorf("HasMermaid=%v, want %v", meta.HasMermaid, c.mermaid)
			}
			if meta.HasMath != c.math {
				t.Errorf("HasMath=%v, want %v", meta.HasMath, c.math)
			}
		})
	}
}

func TestRender_DollarNotMathLeftVerbatim(t *testing.T) {
	got, _, err := markdown.Render([]byte("It costs $5 today.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(got); strings.Contains(s, `class="math`) {
		t.Errorf("lone dollar must not become math:\n%s", s)
	}
}

func TestRenderRefs_rewritesImageAndLinkDestinations(t *testing.T) {
	resolve := func(ref string) (string, bool) {
		switch ref {
		case "./logo.png":
			return "https://storage.mdfly.dev/documents/s/abc.png", true
		case "./y.md":
			return "/s/y", true
		}
		return "", false
	}
	in := []byte("![logo](./logo.png)\n\n[y](./y.md)\n\n[ext](https://example.com)\n\n![miss](./missing.png)\n")
	got, _, err := markdown.RenderRefs(in, resolve)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `src="https://storage.mdfly.dev/documents/s/abc.png"`) {
		t.Errorf("image dest not rewritten:\n%s", s)
	}
	if !strings.Contains(s, `href="/s/y"`) {
		t.Errorf("link dest not rewritten:\n%s", s)
	}
	if !strings.Contains(s, `href="https://example.com"`) {
		t.Errorf("external link must be verbatim:\n%s", s)
	}
	if !strings.Contains(s, `src="./missing.png"`) {
		t.Errorf("unresolved image must be verbatim:\n%s", s)
	}
}

func TestRenderRefs_rewritesRawHTMLRefs(t *testing.T) {
	resolve := func(ref string) (string, bool) {
		switch ref {
		case "assets/logo.svg":
			return "https://storage.mdfly.dev/documents/s/abc.svg", true
		case "LICENSE":
			return "/s/LICENSE", true
		}
		return "", false
	}
	in := []byte("<div align=\"center\">\n<img src=\"assets/logo.svg\" width=\"72\" alt=\"logo\" />\n</div>\n\n" +
		"See <a href=\"LICENSE\">license</a> and <a href=\"missing.md\">missing</a>.\n\n" +
		"```html\n<img src=\"assets/logo.svg\" />\n```\n")
	got, meta, err := markdown.RenderRefs(in, resolve)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `src="https://storage.mdfly.dev/documents/s/abc.svg"`) {
		t.Errorf("raw HTML img src not rewritten:\n%s", s)
	}
	if !strings.Contains(s, `href="/s/LICENSE"`) {
		t.Errorf("raw HTML anchor href not rewritten:\n%s", s)
	}
	if !strings.Contains(s, `href="missing.md"`) {
		t.Errorf("unresolved raw HTML ref must be verbatim:\n%s", s)
	}
	if !strings.Contains(s, `<div align="center">`) {
		t.Errorf("align attribute must survive sanitizing:\n%s", s)
	}
	if !strings.Contains(s, `width="72"`) {
		t.Errorf("width attribute must survive sanitizing:\n%s", s)
	}
	if !strings.Contains(s, `&#34;assets/logo.svg&#34;`) {
		t.Errorf("ref inside a fenced code block must stay verbatim:\n%s", s)
	}
	if meta.OGImagePath != "https://storage.mdfly.dev/documents/s/abc.svg" {
		t.Errorf("OGImagePath=%q, want the rewritten URL", meta.OGImagePath)
	}
}

func TestRenderRefs_rewritesMediaRefs(t *testing.T) {
	resolve := func(ref string) (string, bool) { return "https://cdn/" + ref, true }
	in := []byte("<picture>\n" +
		"<source srcset=\"logo-dark.svg\" media=\"(prefers-color-scheme: dark)\" />\n" +
		"<source srcset=\"logo-1x.png 1x, logo-2x.png 2x\" />\n" +
		"<img src=\"logo.svg\" width=\"72\" alt=\"logo\" />\n" +
		"</picture>\n\n" +
		"<video src=\"demo.mp4\" poster=\"thumb.png\" controls muted loop></video>\n\n" +
		"<audio src=\"clip.mp3\" controls></audio>\n")
	got, _, err := markdown.RenderRefs(in, resolve)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	want := []string{
		`<source srcset="https://cdn/logo-dark.svg"`,
		`<source srcset="https://cdn/logo-1x.png 1x, https://cdn/logo-2x.png 2x"`,
		`<video src="https://cdn/demo.mp4"`,
		`poster="https://cdn/thumb.png"`,
		`<audio src="https://cdn/clip.mp3"`,
		`media="(prefers-color-scheme: dark)"`,
	}
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Errorf("missing %s in:\n%s", w, s)
		}
	}
}

// Media attributes bluemonday does not treat as URLs — srcset, poster, and src
// on <source> — must still reject non-http(s)/data:image schemes.
func TestRender_MediaSchemesSanitized(t *testing.T) {
	vectors := []string{
		`<video src="javascript:alert(1)" controls></video>`,
		`<video src="demo.mp4" onerror="alert(1)" controls></video>`,
		`<video src="demo.mp4" onloadstart="alert(1)"></video>`,
		`<source srcset="javascript:alert(1)" />`,
		`<audio src="data:text/html,<b>x</b>" controls></audio>`,
		`<video><source src="javascript:alert(1)"></video>`,
		`<video poster="javascript:alert(1)" src="a.mp4"></video>`,
		`<img srcset="javascript:alert(1) 2x" src="a.png" />`,
		`<img srcset="a.png 1x, javascript:alert(1) 2x" />`,
	}
	for _, v := range vectors {
		got, _, err := markdown.Render([]byte(v + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		s := string(got)
		for _, bad := range []string{"javascript:", "alert(1)", "onerror", "onloadstart", "data:text/html"} {
			if strings.Contains(s, bad) {
				t.Errorf("%q survived sanitizing of %s:\n%s", bad, v, s)
			}
		}
	}
}

func TestRender_MediaSafeRefsKept(t *testing.T) {
	in := []byte(`<video src="demo.mp4" poster="thumb.png" controls></video>` + "\n\n" +
		`<img srcset="a.png 1x, https://cdn/b.png 2x" src="/root.png" />` + "\n")
	got, _, err := markdown.Render(in)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, w := range []string{`src="demo.mp4"`, `poster="thumb.png"`, `srcset="a.png 1x, https://cdn/b.png 2x"`, `src="/root.png"`} {
		if !strings.Contains(s, w) {
			t.Errorf("safe attribute %s was dropped:\n%s", w, s)
		}
	}
}

// A ">" inside a quoted attribute value must not end the tag early, and a
// srcset data: URI's commas must stay inside the URL rather than splitting it —
// the URI stays one candidate, left verbatim, while its relative sibling is
// still rewritten.
func TestRenderRefs_awkwardAttributeValues(t *testing.T) {
	// Mirrors pageResolver: external refs are rejected before any lookup.
	resolve := func(ref string) (string, bool) {
		if ref == "" || markdown.IsExternalRef(ref) {
			return "", false
		}
		return "https://cdn/" + ref, true
	}
	tests := []struct{ name, in, want string }{
		{"gt in quoted value", `<img alt="a>b" src="logo.png" />`, `src="https://cdn/logo.png"`},
		{"gt in quoted href", `<a title="x>y" href="LICENSE">l</a>`, `href="https://cdn/LICENSE"`},
		{"unquoted value", `<img src=logo.png />`, `src="https://cdn/logo.png"`},
		{
			"srcset data uri",
			`<img srcset="data:image/png;base64,AAAA 1x, b.png 2x" />`,
			`srcset="data:image/png;base64,AAAA 1x, https://cdn/b.png 2x"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := markdown.RenderRefs([]byte(tt.in+"\n"), resolve)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), tt.want) {
				t.Errorf("missing %s in:\n%s", tt.want, got)
			}
		})
	}
}

func TestRenderRefs_nilResolverLeavesRefs(t *testing.T) {
	in := []byte("![logo](./logo.png)\n\n[y](./y.md)\n")
	got, _, err := markdown.RenderRefs(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `src="./logo.png"`) {
		t.Errorf("nil resolver must leave image dest:\n%s", s)
	}
	if !strings.Contains(s, `href="./y.md"`) {
		t.Errorf("nil resolver must leave link dest:\n%s", s)
	}
}

func TestRenderWithTimeout_TimesOut(t *testing.T) {
	// Large input + near-zero timeout → must return ErrTimeout.
	var sb strings.Builder
	for i := 0; i < 50000; i++ {
		sb.WriteString("# Heading\n\nParagraph with **bold** and _italic_ text.\n\n")
	}
	_, _, err := markdown.RenderWithTimeout([]byte(sb.String()), 1*time.Nanosecond)
	if !errors.Is(err, markdown.ErrTimeout) {
		t.Errorf("expected ErrTimeout, got %v", err)
	}
}
