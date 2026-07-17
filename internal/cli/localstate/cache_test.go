package localstate

import (
	"os"
	"testing"
	"time"
)

func TestCache_saveLoadRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	want := UpdateCache{
		LastCheck:     time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC),
		LatestVersion: "1.4.2",
	}
	if err := s.SaveCache(want); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	got, err := s.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if got.LatestVersion != want.LatestVersion || !got.LastCheck.Equal(want.LastCheck) {
		t.Errorf("cache=%+v want %+v", got, want)
	}
}

func TestCache_corruptNeverClobbersState(t *testing.T) {
	s := New(t.TempDir())
	p := "/a.md"
	if err := s.Upsert(sampleRecord("keep", &p)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.cachePath(), []byte("{broken"), FileMode); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache on corrupt: %v", err)
	}
	if !got.LastCheck.IsZero() || got.LatestVersion != "" {
		t.Errorf("corrupt cache should yield zero value, got %+v", got)
	}

	led, _ := s.Load()
	if _, ok := led.Get("keep"); !ok {
		t.Error("corrupt cache clobbered state.json")
	}
}
