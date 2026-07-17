package markdown_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/markdown"
)

func TestHighlightFile_Golden(t *testing.T) {
	cases := []struct {
		name, filename, content string
	}{
		{"go", "main.go", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"},
		{"python", "app.py", "def greet(name):\n    return f\"hi {name}\"\n"},
		{"json", "data.json", "{\n  \"key\": \"value\",\n  \"n\": 42\n}\n"},
		{"plain", "notes.txt", "just some plain text\nwith two lines\n"},
		{"unknown", "file.zzz", "fallback to plaintext lexer\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := markdown.HighlightFile(tc.filename, []byte(tc.content))
			if err != nil {
				t.Fatalf("HighlightFile: %v", err)
			}
			goldPath := filepath.Join("testdata", "highlight", tc.name+".html")
			if *update {
				if err := os.WriteFile(goldPath, []byte(got), 0644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(goldPath)
			if err != nil {
				t.Fatalf("golden missing at %s; run: go test ./internal/markdown/... -update", goldPath)
			}
			if string(got) != string(want) {
				t.Errorf("output mismatch for %s\ngot:\n%s\nwant:\n%s", tc.name, got, want)
			}
		})
	}
}

func TestHighlightFile_LineNumbers(t *testing.T) {
	content := []byte("package main\n\nfunc main() {}\n")
	got, err := markdown.HighlightFile("main.go", content)
	if err != nil {
		t.Fatalf("HighlightFile: %v", err)
	}
	// File view carries a line-number gutter: one non-selectable cell per line.
	const perLine = "white-space:pre;-webkit-user-select:none"
	if n := strings.Count(string(got), perLine); n != 3 {
		t.Errorf("expected 3 non-selectable line-number cells, got %d:\n%s", n, got)
	}
	if !strings.Contains(string(got), "<table") {
		t.Errorf("file view must render line numbers in a table:\n%s", got)
	}
}

func TestIsBinary(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    bool
	}{
		{"valid utf8", []byte("hello, world\nλ"), false},
		{"nul byte", []byte("hello\x00world"), true},
		{"invalid utf8", []byte{0xff, 0xfe, 0xfd}, true},
		{"empty", []byte{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := markdown.IsBinary(tc.content); got != tc.want {
				t.Errorf("IsBinary(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}
