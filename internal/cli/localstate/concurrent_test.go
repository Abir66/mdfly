package localstate

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestUpsert_concurrentNoLostRecord(t *testing.T) {
	s := New(t.TempDir())
	const n = 30

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := fmt.Sprintf("/f%d.md", i)
			if err := s.Upsert(sampleRecord(fmt.Sprintf("s%02d", i), &p)); err != nil {
				t.Errorf("Upsert %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	led, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(led.All()); got != n {
		t.Errorf("records=%d want %d (lost update under concurrency)", got, n)
	}
}

func TestWriteAtomic_priorStateIntactOnFailure(t *testing.T) {
	s := New(t.TempDir())
	p := "/a.md"
	if err := s.Upsert(sampleRecord("good", &p)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.statePath())
	if err != nil {
		t.Fatal(err)
	}

	// A temp+rename write must never truncate the destination: writing to a
	// nonexistent directory fails before any rename touches the good file.
	if err := writeAtomic(filepath.Join(s.dir, "missing", stateFileName), []byte("x")); err == nil {
		t.Fatal("expected error writing into missing dir")
	}

	after, err := os.ReadFile(s.statePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("prior state.json changed after failed write")
	}
}
