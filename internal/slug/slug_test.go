package slug_test

import (
	"testing"

	"github.com/Abir66/mdfly/internal/slug"
)

func TestGenerate_length(t *testing.T) {
	s, err := slug.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(s) != 8 {
		t.Errorf("len=%d, want 8", len(s))
	}
}

func TestGenerate_alphabet(t *testing.T) {
	const allowed = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	s, _ := slug.Generate()
	for _, c := range s {
		found := false
		for _, a := range allowed {
			if c == a {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("char %q not in base62 alphabet", c)
		}
	}
}

func TestGenerate_unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s, err := slug.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if seen[s] {
			t.Errorf("duplicate slug %q", s)
		}
		seen[s] = true
	}
}

func TestIsReserved(t *testing.T) {
	reserved := []string{"api", "cdn", "healthz", "llm", "mdfly", "login", "www"}
	for _, w := range reserved {
		if !slug.IsReserved(w) {
			t.Errorf("IsReserved(%q) = false, want true", w)
		}
	}
	notReserved := []string{"hello", "mdfly-docs", "abc12345", "notes"}
	for _, w := range notReserved {
		if slug.IsReserved(w) {
			t.Errorf("IsReserved(%q) = true, want false", w)
		}
	}
}
