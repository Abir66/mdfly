package storage_test

import (
	"testing"

	"github.com/Abir66/mdfly/internal/server/storage"
)

func TestBlobKey(t *testing.T) {
	const (
		slug = "abc123"
		hash = "deadbeef"
	)
	tests := []struct {
		name string
		ext  string
		want string
	}{
		{"with dot ext", ".md", "documents/abc123/deadbeef.md"},
		{"without dot ext", "png", "documents/abc123/deadbeef.png"},
		{"empty ext", "", "documents/abc123/deadbeef"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := storage.BlobKey(slug, hash, tt.ext); got != tt.want {
				t.Errorf("BlobKey(%q, %q, %q)=%q, want %q", slug, hash, tt.ext, got, tt.want)
			}
		})
	}
}
