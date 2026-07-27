// Package gc runs the periodic document lifecycle sweep (ADR-0030): anonymous
// published Documents past their expires_at become 'expired' (410), and pending
// rows that never committed within the abandon grace become 'abandoned' (404).
// Blob deletion is a separate pass. The sweep is driven by the jobs runner
// (ADR-0029) and is safe to repeat — each transition selects only rows still in
// the source status.
package gc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// DefaultBatchSize bounds how many rows one pass moves per transition, so a
// backlog is worked down over successive ticks instead of in one long
// transaction.
const DefaultBatchSize = 500

// Store is the subset of db.Client the sweep needs.
type Store interface {
	MarkExpired(ctx context.Context, now time.Time, limit int) (int64, error)
	MarkAbandoned(ctx context.Context, olderThan time.Time, limit int) (int64, error)
}

// Service executes lifecycle sweeps. AbandonGrace is required and must be
// positive — wiring supplies it from JobsConfig. BatchSize defaults to
// DefaultBatchSize and Now to time.Now.
type Service struct {
	Db           Store
	AbandonGrace time.Duration
	BatchSize    int
	Now          func() time.Time
}

// Result counts the rows each transition moved in one pass.
type Result struct {
	Expired   int64
	Abandoned int64
}

// Sweep runs one pass of both transitions and returns what moved. Both are
// attempted even if the first fails, so a broken expiry cannot stall
// abandonment; the returned error joins the failures.
func (s *Service) Sweep(ctx context.Context) (Result, error) {
	now := s.now()
	limit := s.batchSize()

	var res Result
	var errs []error

	expired, err := s.Db.MarkExpired(ctx, now, limit)
	if err != nil {
		errs = append(errs, fmt.Errorf("mark expired: %w", err))
	}
	res.Expired = expired

	abandoned, err := s.Db.MarkAbandoned(ctx, now.Add(-s.AbandonGrace), limit)
	if err != nil {
		errs = append(errs, fmt.Errorf("mark abandoned: %w", err))
	}
	res.Abandoned = abandoned

	return res, errors.Join(errs...)
}

// Job adapts Sweep to the jobs.Runner signature, logging per-pass counts.
func (s *Service) Job(ctx context.Context) {
	res, err := s.Sweep(ctx)
	if err != nil {
		slog.Error("lifecycle gc pass failed",
			"err", err, "expired", res.Expired, "abandoned", res.Abandoned)
		return
	}
	slog.Info("lifecycle gc pass", "expired", res.Expired, "abandoned", res.Abandoned)
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (s *Service) batchSize() int {
	if s.BatchSize <= 0 {
		return DefaultBatchSize
	}
	return s.BatchSize
}
