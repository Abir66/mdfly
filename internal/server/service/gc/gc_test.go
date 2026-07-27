package gc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/server/service/gc"
)

// fakeStore records the arguments each transition was called with and replays
// canned counts and errors.
type fakeStore struct {
	expiredNow      time.Time
	expiredLimit    int
	expiredCount    int64
	expiredErr      error
	abandonedBefore time.Time
	abandonedLimit  int
	abandonedCount  int64
	abandonedErr    error
}

func (f *fakeStore) MarkExpired(_ context.Context, now time.Time, limit int) (int64, error) {
	f.expiredNow, f.expiredLimit = now, limit
	return f.expiredCount, f.expiredErr
}

func (f *fakeStore) MarkAbandoned(_ context.Context, olderThan time.Time, limit int) (int64, error) {
	f.abandonedBefore, f.abandonedLimit = olderThan, limit
	return f.abandonedCount, f.abandonedErr
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestSweep_appliesClockAndGrace(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{expiredCount: 3, abandonedCount: 2}
	svc := &gc.Service{Db: store, AbandonGrace: time.Hour, BatchSize: 50, Now: fixedClock(now)}

	res, err := svc.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if !store.expiredNow.Equal(now) {
		t.Errorf("MarkExpired now=%s, want %s", store.expiredNow, now)
	}
	if want := now.Add(-time.Hour); !store.abandonedBefore.Equal(want) {
		t.Errorf("MarkAbandoned olderThan=%s, want %s", store.abandonedBefore, want)
	}
	if res.Expired != 3 || res.Abandoned != 2 {
		t.Errorf("result = %+v, want {Expired:3 Abandoned:2}", res)
	}
}

func TestSweep_boundsBatchSize(t *testing.T) {
	cases := []struct {
		name      string
		batchSize int
		want      int
	}{
		{"explicit", 7, 7},
		{"zero falls back to default", 0, gc.DefaultBatchSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			svc := &gc.Service{Db: store, AbandonGrace: time.Hour, BatchSize: tc.batchSize}

			if _, err := svc.Sweep(context.Background()); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if store.expiredLimit != tc.want || store.abandonedLimit != tc.want {
				t.Errorf("limits = (%d, %d), want %d", store.expiredLimit, store.abandonedLimit, tc.want)
			}
		})
	}
}

func TestSweep_defaultClockIsWallClock(t *testing.T) {
	store := &fakeStore{}
	svc := &gc.Service{Db: store, AbandonGrace: time.Hour}

	before := time.Now()
	if _, err := svc.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if store.expiredNow.Before(before) || store.expiredNow.After(time.Now()) {
		t.Errorf("MarkExpired now=%s, want between %s and now", store.expiredNow, before)
	}
}

// A non-positive grace would abandon rows still mid-publish, so the pass must
// fail before touching either transition.
func TestSweep_rejectsNonPositiveAbandonGrace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		grace time.Duration
	}{
		{"zero", 0},
		{"negative", -time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{expiredCount: 3, abandonedCount: 2}
			svc := &gc.Service{Db: store, AbandonGrace: tc.grace}

			res, err := svc.Sweep(context.Background())
			if !errors.Is(err, gc.ErrInvalidAbandonGrace) {
				t.Fatalf("err = %v, want ErrInvalidAbandonGrace", err)
			}
			if res != (gc.Result{}) {
				t.Errorf("result = %+v, want zero", res)
			}
			if store.expiredLimit != 0 || store.abandonedLimit != 0 {
				t.Error("a transition ran despite the invalid grace")
			}
		})
	}
}

// A failing transition must not skip the other one: the pass reports both the
// error and whatever the surviving transition moved.
func TestSweep_bothTransitionsRunWhenOneFails(t *testing.T) {
	boom := errors.New("boom")
	store := &fakeStore{expiredErr: boom, abandonedCount: 4}
	svc := &gc.Service{Db: store, AbandonGrace: time.Hour}

	res, err := svc.Sweep(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap boom", err)
	}
	if store.abandonedLimit == 0 {
		t.Error("MarkAbandoned was not called after MarkExpired failed")
	}
	if res.Abandoned != 4 {
		t.Errorf("res.Abandoned = %d, want 4", res.Abandoned)
	}
}
