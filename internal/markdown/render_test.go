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
			return "https://cdn.mdfly.dev/documents/s/abc.png", true
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
	if !strings.Contains(s, `src="https://cdn.mdfly.dev/documents/s/abc.png"`) {
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
