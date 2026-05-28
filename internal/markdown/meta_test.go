package markdown_test

import (
	"testing"

	"github.com/Abir66/mdfly/internal/markdown"
)

func TestRender_FrontmatterTitle(t *testing.T) {
	input := []byte("---\ntitle: Hello World\n---\n\n# Other Heading\n\nSome paragraph.\n")
	_, meta, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Hello World" {
		t.Errorf("title: got %q, want %q", meta.Title, "Hello World")
	}
}

func TestRender_H1FallbackTitle(t *testing.T) {
	input := []byte("# My Document\n\nFirst paragraph text.\n")
	_, meta, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "My Document" {
		t.Errorf("title: got %q, want %q", meta.Title, "My Document")
	}
}

func TestRender_NoTitleFallback(t *testing.T) {
	input := []byte("Just a paragraph, no heading.\n")
	_, meta, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "" {
		t.Errorf("title: got %q, want empty", meta.Title)
	}
}

func TestRender_FrontmatterDescription(t *testing.T) {
	input := []byte("---\ndescription: A short desc.\n---\n\nSome body text.\n")
	_, meta, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Excerpt != "A short desc." {
		t.Errorf("excerpt: got %q, want %q", meta.Excerpt, "A short desc.")
	}
}

func TestRender_FirstParagraphExcerpt(t *testing.T) {
	input := []byte("First paragraph content here.\n\nSecond paragraph.\n")
	_, meta, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Excerpt != "First paragraph content here." {
		t.Errorf("excerpt: got %q, want %q", meta.Excerpt, "First paragraph content here.")
	}
}

func TestRender_ExcerptTruncated(t *testing.T) {
	// paragraph longer than 200 chars must be truncated
	long := "abcdefghij" // 10 chars
	paragraph := ""
	for i := 0; i < 25; i++ { // 250 chars
		paragraph += long
	}
	input := []byte(paragraph + "\n")
	_, meta, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(meta.Excerpt)) > 200 {
		t.Errorf("excerpt length %d > 200", len([]rune(meta.Excerpt)))
	}
}

func TestRender_FrontmatterImagePath(t *testing.T) {
	input := []byte("---\nimage: ./hero.png\n---\n\n# Title\n\nBody.\n")
	_, meta, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if meta.OGImagePath != "./hero.png" {
		t.Errorf("OGImagePath: got %q, want %q", meta.OGImagePath, "./hero.png")
	}
}

func TestRender_FirstImgFallbackImagePath(t *testing.T) {
	input := []byte("# Title\n\n![alt](./logo.png)\n\nBody.\n")
	_, meta, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if meta.OGImagePath != "./logo.png" {
		t.Errorf("OGImagePath: got %q, want %q", meta.OGImagePath, "./logo.png")
	}
}

func TestRender_NoImagePath(t *testing.T) {
	input := []byte("# Title\n\nNo images here.\n")
	_, meta, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if meta.OGImagePath != "" {
		t.Errorf("OGImagePath: got %q, want empty", meta.OGImagePath)
	}
}

func TestRender_FrontmatterStrippedFromHTML(t *testing.T) {
	input := []byte("---\ntitle: Hello\n---\n\n# Heading\n")
	html, _, err := markdown.Render(input)
	if err != nil {
		t.Fatal(err)
	}
	body := string(html)
	if contains(body, "---") {
		t.Errorf("rendered HTML contains frontmatter delimiter: %s", body)
	}
	if contains(body, "title: Hello") {
		t.Errorf("rendered HTML contains frontmatter content: %s", body)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
