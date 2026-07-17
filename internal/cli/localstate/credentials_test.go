package localstate

import (
	"os"
	"strings"
	"testing"
)

func TestToken_saveLoadDelete(t *testing.T) {
	s := New(t.TempDir())
	const slug, tok = "abc123", "mftk_secret"

	if _, ok, err := s.LoadToken(slug); err != nil || ok {
		t.Fatalf("before save: ok=%v err=%v", ok, err)
	}
	if err := s.SaveToken(slug, tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	got, ok, err := s.LoadToken(slug)
	if err != nil || !ok {
		t.Fatalf("LoadToken: got ok=%v err=%v", ok, err)
	}
	if got != tok {
		t.Errorf("token=%q want %q", got, tok)
	}

	if err := s.DeleteToken(slug); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if _, ok, err := s.LoadToken(slug); err != nil || ok {
		t.Errorf("after delete: ok=%v err=%v", ok, err)
	}
}

func TestSaveToken_afterNullCredentialsFile(t *testing.T) {
	s := New(t.TempDir())
	if err := os.WriteFile(s.credentialsPath(), []byte("null"), FileMode); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveToken("abc", "mftk_x"); err != nil {
		t.Fatalf("SaveToken over null file: %v", err)
	}
	got, ok, err := s.LoadToken("abc")
	if err != nil || !ok || got != "mftk_x" {
		t.Errorf("got=%q ok=%v err=%v", got, ok, err)
	}
}

func TestToken_notInStateFile(t *testing.T) {
	s := New(t.TempDir())
	p := "/a.md"
	if err := s.SaveToken("abc123", "mftk_secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(sampleRecord("abc123", &p)); err != nil {
		t.Fatal(err)
	}

	state, err := os.ReadFile(s.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "mftk_secret") {
		t.Error("plaintext token leaked into state.json")
	}

	fi, err := os.Stat(s.credentialsPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != FileMode {
		t.Errorf("credentials mode=%o want %o", fi.Mode().Perm(), FileMode)
	}
}
