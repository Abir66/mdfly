package markdown_test

import (
	"reflect"
	"strings"
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

func TestNormalizeAssetPath(t *testing.T) {
	tests := map[string]string{
		"./logo.png":       "logo.png",
		"logo.png":         "logo.png",
		"./imgs/logo.png":  "imgs/logo.png",
		"imgs/../logo.png": "logo.png",
		"./a/./b.png":      "a/b.png",
	}
	for in, want := range tests {
		if got := markdown.NormalizeAssetPath(in); got != want {
			t.Errorf("NormalizeAssetPath(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestRewriteImageRefs(t *testing.T) {
	resolve := func(src string) (string, bool) {
		if src == "./logo.png" {
			return "https://cdn.mdfly.dev/documents/slug/abc.png", true
		}
		return "", false
	}
	in := []byte(`<p><img src="./logo.png" alt="logo"><img src="https://ext.com/x.png"></p>`)
	got := string(markdown.RewriteImageRefs(in, resolve))

	if !strings.Contains(got, `src="https://cdn.mdfly.dev/documents/slug/abc.png"`) {
		t.Errorf("local img not rewritten: %s", got)
	}
	if !strings.Contains(got, `src="https://ext.com/x.png"`) {
		t.Errorf("external img must be left verbatim: %s", got)
	}
	if !strings.Contains(got, `alt="logo"`) {
		t.Errorf("other attrs must survive: %s", got)
	}
}
