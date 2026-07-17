package localstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_corruptStateRecovered(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := os.WriteFile(s.statePath(), []byte("{not json"), FileMode); err != nil {
		t.Fatal(err)
	}

	led, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n := len(led.All()); n != 0 {
		t.Errorf("ledger has %d records, want empty", n)
	}

	bak, err := os.ReadFile(s.statePath() + bakSuffix)
	if err != nil {
		t.Fatalf("read .bak: %v", err)
	}
	if string(bak) != "{not json" {
		t.Errorf("bak=%q want original corrupt bytes", bak)
	}
}

func TestPrune_removesOnlyTarget(t *testing.T) {
	s := New(t.TempDir())
	p := "/a.md"
	for _, slug := range []string{"keep", "drop"} {
		if err := s.Upsert(sampleRecord(slug, &p)); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Prune("drop"); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	led, _ := s.Load()
	if _, ok := led.Get("drop"); ok {
		t.Error("drop still present")
	}
	if _, ok := led.Get("keep"); !ok {
		t.Error("keep was removed")
	}
	if got := led.SlugsForPath(p); len(got) != 1 || got[0] != "keep" {
		t.Errorf("index=%v want [keep]", got)
	}
}

func TestFileAndDirModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".mdfly")
	s := New(dir)
	p := "/a.md"
	if err := s.Upsert(sampleRecord("s1", &p)); err != nil {
		t.Fatal(err)
	}

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != DirMode {
		t.Errorf("dir mode=%o want %o", di.Mode().Perm(), DirMode)
	}
	fi, err := os.Stat(s.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != FileMode {
		t.Errorf("state.json mode=%o want %o", fi.Mode().Perm(), FileMode)
	}
}
