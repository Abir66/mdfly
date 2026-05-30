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
