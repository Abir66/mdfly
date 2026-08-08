package markdown_test

import (
	"reflect"
	"testing"

	"github.com/Abir66/mdfly/internal/markdown"
)

func TestImageRefs(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want []string
	}{
		{"single", "![alt](./logo.png)\n", []string{"./logo.png"}},
		{"multiple", "![a](./a.png)\n\n![b](b/c.jpg)\n", []string{"./a.png", "b/c.jpg"}},
		{"with title", "![alt](./logo.png \"a title\")\n", []string{"./logo.png"}},
		{"angle bracket space", "![alt](<./my image.png>)\n", []string{"./my image.png"}},
		{"external", "![x](https://example.com/x.png)\n", []string{"https://example.com/x.png"}},
		{"none", "# Heading\n\nNo images here.\n", nil},
		{"skips frontmatter", "---\nimage: ./fm.png\n---\n\n![body](./body.png)\n", []string{"./body.png"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := markdown.ImageRefs([]byte(tt.md))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ImageRefs()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestLinkRefs(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want []string
	}{
		{"single", "[a](./y.md)\n", []string{"./y.md"}},
		{"multiple", "[a](./a.md)\n\n[b](sub/b.md)\n", []string{"./a.md", "sub/b.md"}},
		{"external", "[x](https://example.com/x.md)\n", []string{"https://example.com/x.md"}},
		{"none", "# Heading\n\nNo links here.\n", nil},
		{"skips frontmatter", "---\nlink: ./fm.md\n---\n\n[body](./body.md)\n", []string{"./body.md"}},
		{"images not links", "![alt](./logo.png)\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := markdown.LinkRefs([]byte(tt.md))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LinkRefs()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestHTMLRefs(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want []string
	}{
		{"img block", "<div align=\"center\">\n<img src=\"./logo.svg\" width=\"72\" />\n</div>\n", []string{"./logo.svg"}},
		{"img attrs before src", "<img width=\"72\" src=\"logo.svg\" />\n", []string{"logo.svg"}},
		{"single quotes", "<img src='logo.svg' />\n", []string{"logo.svg"}},
		{"tag split across lines", "<img\n  src=\"logo.svg\"\n  width=\"72\" />\n", []string{"logo.svg"}},
		{"inline anchor", "See <a href=\"LICENSE\">license</a>.\n", []string{"LICENSE"}},
		{"external", "<img src=\"https://x/y.png\" />\n", []string{"https://x/y.png"}},
		{"multiple in order", "<img src=\"a.png\" />\n<a href=\"b.md\">b</a>\n", []string{"a.png", "b.md"}},
		{"skips fenced code", "```html\n<img src=\"logo.svg\" />\n```\n", nil},
		{"skips frontmatter", "---\nimage: <img src=\"fm.png\" />\n---\n\n<img src=\"body.png\" />\n", []string{"body.png"}},
		{"markdown image not html", "![alt](./logo.png)\n", nil},
		{"none", "# Heading\n\nNo HTML here.\n", nil},
		{"source src", "<video><source src=\"demo.mp4\" type=\"video/mp4\" /></video>\n", []string{"demo.mp4"}},
		{"video src", "<video src=\"demo.mp4\" controls></video>\n", []string{"demo.mp4"}},
		{"audio src", "<audio src=\"clip.mp3\" controls></audio>\n", []string{"clip.mp3"}},
		{"srcset single", "<source srcset=\"logo-dark.svg\" />\n", []string{"logo-dark.svg"}},
		{"srcset descriptors", "<img srcset=\"a.png 1x, b.png 2x\" />\n", []string{"a.png", "b.png"}},
		{"srcset trailing comma", "<img srcset=\"a.png 1x,\" />\n", []string{"a.png"}},
		{"srcset with src in order", "<img srcset=\"a.png 2x\" src=\"b.png\" />\n", []string{"a.png", "b.png"}},
		{"picture block", "<picture>\n<source srcset=\"d.svg\" media=\"(prefers-color-scheme: dark)\" />\n<img src=\"l.svg\" />\n</picture>\n", []string{"d.svg", "l.svg"}},
		{"srcset not confused with src", "<img srcset=\"a.png\" />\n", []string{"a.png"}},
		{"empty value dropped", "<img src=\"\" />\n", nil},
		{"video poster", "<video poster=\"thumb.png\" src=\"demo.mp4\"></video>\n", []string{"thumb.png", "demo.mp4"}},
		{"poster only on video", "<img poster=\"thumb.png\" src=\"a.png\" />\n", []string{"a.png"}},
		{"gt inside quoted attr", "<img alt=\"a>b\" src=\"logo.png\" />\n", []string{"logo.png"}},
		{"gt inside quoted href attr", "<a title=\"x>y\" href=\"LICENSE\">l</a>\n", []string{"LICENSE"}},
		{"unquoted value", "<img src=logo.png />\n", []string{"logo.png"}},
		{"srcset data uri keeps commas", "<img srcset=\"data:image/png;base64,AAAA 1x, b.png 2x\" />\n", []string{"data:image/png;base64,AAAA", "b.png"}},
		{"srcset data uri alone", "<img srcset=\"data:image/gif;base64,R0lGOD,x\" />\n", []string{"data:image/gif;base64,R0lGOD,x"}},
		{"uppercase tag and attr", "<IMG SRC=\"logo.png\" />\n", []string{"logo.png"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := markdown.HTMLRefs([]byte(tt.md))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("HTMLRefs()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveLogicalPath(t *testing.T) {
	tests := []struct {
		referrerDir string
		ref         string
		want        string
	}{
		{".", "./y.md", "y.md"},
		{".", "y.md", "y.md"},
		{"sub", "./sub2/a.md", "sub/sub2/a.md"},
		{"sub", "../x.md", "x.md"},
		{".", "../x.md", "../x.md"},
		{"sub2", "../sub/a.md", "sub/a.md"},
		{".", "imgs/logo.png", "imgs/logo.png"},
	}
	for _, tt := range tests {
		if got := markdown.ResolveLogicalPath(tt.referrerDir, tt.ref); got != tt.want {
			t.Errorf("ResolveLogicalPath(%q,%q)=%q, want %q", tt.referrerDir, tt.ref, got, tt.want)
		}
	}
}

func TestIsExternalRef(t *testing.T) {
	external := []string{"http://x/y.png", "https://x/y.png", "//x/y.png", "data:image/png;base64,AAAA", "mailto:a@b.c"}
	local := []string{"./logo.png", "logo.png", "imgs/logo.png", "../up.png", "my image.png"}
	for _, r := range external {
		if !markdown.IsExternalRef(r) {
			t.Errorf("IsExternalRef(%q)=false, want true", r)
		}
	}
	for _, r := range local {
		if markdown.IsExternalRef(r) {
			t.Errorf("IsExternalRef(%q)=true, want false", r)
		}
	}
}
