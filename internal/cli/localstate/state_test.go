package localstate

import (
	"testing"
	"time"
)

func sampleRecord(slug string, path *string) Record {
	ts := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	return Record{
		Slug:         slug,
		URL:          "https://mdfly.dev/" + slug,
		ManifestHash: "hash-" + slug,
		Tier:         "anon",
		Size:         1234,
		FileCount:    3,
		CreatedAt:    ts,
		UpdatedAt:    ts,
		Source:       SourceFile,
		Path:         path,
		HasToken:     true,
	}
}

func TestUpsertLoad_roundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	p := "/home/u/notes.md"
	want := sampleRecord("abc123", &p)
	if err := s.Upsert(want); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	led, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := led.Get("abc123")
	if !ok {
		t.Fatal("Get(abc123): not found")
	}
	if got.URL != want.URL || got.ManifestHash != want.ManifestHash || got.FileCount != want.FileCount {
		t.Errorf("record mismatch: got %+v want %+v", got, want)
	}
	if got.Path == nil || *got.Path != p {
		t.Errorf("path=%v want %q", got.Path, p)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("created_at=%v want %v", got.CreatedAt, want.CreatedAt)
	}
}

func TestSlugsForPath_resolution(t *testing.T) {
	s := New(t.TempDir())
	shared := "/home/u/notes.md"
	lone := "/home/u/solo.md"

	for _, r := range []Record{
		sampleRecord("s1", &shared),
		sampleRecord("s2", &shared),
		sampleRecord("only", &lone),
		{Slug: "text1", Source: SourceText, Path: nil},
	} {
		if err := s.Upsert(r); err != nil {
			t.Fatalf("Upsert %s: %v", r.Slug, err)
		}
	}

	led, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := led.SlugsForPath("/nope.md"); len(got) != 0 {
		t.Errorf("zero: got %v want none", got)
	}
	if got := led.SlugsForPath(lone); len(got) != 1 || got[0] != "only" {
		t.Errorf("one: got %v want [only]", got)
	}
	got := led.SlugsForPath(shared)
	if len(got) != 2 || got[0] != "s1" || got[1] != "s2" {
		t.Errorf("many: got %v want [s1 s2]", got)
	}
}

func TestLedger_returnedPathIsCopy(t *testing.T) {
	s := New(t.TempDir())
	p := "/home/u/notes.md"
	if err := s.Upsert(sampleRecord("s1", &p)); err != nil {
		t.Fatal(err)
	}
	led, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	rec, _ := led.Get("s1")
	*rec.Path = "/tampered.md"

	again, _ := led.Get("s1")
	if *again.Path != p {
		t.Errorf("ledger mutated via returned Path: got %q", *again.Path)
	}
	if got := led.SlugsForPath(p); len(got) != 1 {
		t.Errorf("index desynced: SlugsForPath(%q)=%v", p, got)
	}
}
