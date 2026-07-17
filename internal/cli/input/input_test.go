package input

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestResolve_positionalFile(t *testing.T) {
	path := writeFile(t, "note.md", "# hello")
	src, err := Resolve(Request{File: path, StdinIsTTY: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Kind != KindFile {
		t.Errorf("Kind = %v, want KindFile", src.Kind)
	}
	if src.Path != path {
		t.Errorf("Path = %q, want %q", src.Path, path)
	}
}

func TestResolve_message(t *testing.T) {
	src, err := Resolve(Request{Message: "# note", MessageSet: true, StdinIsTTY: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Kind != KindText {
		t.Errorf("Kind = %v, want KindText", src.Kind)
	}
	if string(src.Content) != "# note" {
		t.Errorf("Content = %q, want %q", src.Content, "# note")
	}
}

func TestResolve_pipedStdin(t *testing.T) {
	src, err := Resolve(Request{Stdin: strings.NewReader("piped body"), StdinIsTTY: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Kind != KindText {
		t.Errorf("Kind = %v, want KindText", src.Kind)
	}
	if string(src.Content) != "piped body" {
		t.Errorf("Content = %q, want %q", src.Content, "piped body")
	}
}

// A positional file wins over piped stdin (a `<` redirect alongside an arg).
func TestResolve_fileWinsOverStdin(t *testing.T) {
	path := writeFile(t, "note.md", "from file")
	src, err := Resolve(Request{File: path, Stdin: strings.NewReader("from pipe"), StdinIsTTY: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Kind != KindFile || src.Path != path {
		t.Errorf("got %+v, want KindFile at %q", src, path)
	}
}

func TestResolve_messageAndFileMutuallyExclusive(t *testing.T) {
	path := writeFile(t, "note.md", "# hello")
	_, err := Resolve(Request{File: path, Message: "x", MessageSet: true, StdinIsTTY: true})
	assertContentError(t, err, "mutually exclusive")
}

func TestResolve_nothingToPublish(t *testing.T) {
	_, err := Resolve(Request{StdinIsTTY: true})
	assertContentError(t, err, "nothing to publish")
}

func TestResolve_emptyContent(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"empty file", Request{File: "", StdinIsTTY: true}, ""}, // set below
		{"empty message", Request{Message: "  ", MessageSet: true, StdinIsTTY: true}, "empty"},
		{"empty pipe", Request{Stdin: strings.NewReader("\n"), StdinIsTTY: false}, "empty"},
	}
	tests[0].req.File = writeFile(t, "empty.md", "")
	tests[0].want = "empty"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(tt.req)
			assertContentError(t, err, tt.want)
		})
	}
}

func TestResolve_nonexistentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.md")
	_, err := Resolve(Request{File: path, StdinIsTTY: true})

	var ce *ContentError
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v, want *ContentError", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error not fs.ErrNotExist: %v", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the path %q", err, path)
	}
}

func TestResolve_unreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	path := writeFile(t, "locked.md", "secret")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })

	_, err := Resolve(Request{File: path, StdinIsTTY: true})
	var ce *ContentError
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v, want *ContentError", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the path %q", err, path)
	}
}

func assertContentError(t *testing.T, err error, wantSubstr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", wantSubstr)
	}
	var ce *ContentError
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v (%T), want *ContentError", err, err)
	}
	if wantSubstr != "" && !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error %q does not contain %q", err, wantSubstr)
	}
}
