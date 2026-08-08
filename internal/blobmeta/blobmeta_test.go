package blobmeta_test

import (
	"testing"

	"github.com/Abir66/mdfly/internal/blobmeta"
)

func TestContentType(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"svg needs exact type", "imgs/logo.svg", "image/svg+xml"},
		{"png", "a.png", "image/png"},
		{"jpg", "a.jpg", "image/jpeg"},
		{"jpeg", "a.jpeg", "image/jpeg"},
		{"gif", "a.gif", "image/gif"},
		{"webp", "a.webp", "image/webp"},
		{"mp4", "a.mp4", "video/mp4"},
		{"webm", "a.webm", "video/webm"},
		{"mp3", "a.mp3", "audio/mpeg"},
		{"wav", "a.wav", "audio/wav"},
		{"uppercase ext", "LOGO.SVG", "image/svg+xml"},
		{"blob key resolves like its path", "documents/abc123/deadbeef.svg", "image/svg+xml"},
		{"markdown downloads", "index.md", blobmeta.DefaultContentType},
		{"no extension", "LICENSE", blobmeta.DefaultContentType},
		{"unknown extension", "a.zip", blobmeta.DefaultContentType},
		{"html never renders", "a.html", blobmeta.DefaultContentType},
		{"htm never renders", "a.htm", blobmeta.DefaultContentType},
		{"xml never renders", "a.xml", blobmeta.DefaultContentType},
		{"dotfile is not an extension", ".gitignore", blobmeta.DefaultContentType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := blobmeta.ContentType(tt.path); got != tt.want {
				t.Errorf("ContentType(%q)=%q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
