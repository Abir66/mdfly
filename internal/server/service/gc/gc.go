// Package gc runs the periodic document lifecycle sweep (ADR-0005): anonymous
// published Documents past their expires_at become 'expired' (410), pending rows
// that never committed within the abandon grace become 'abandoned' (404), and
// terminal rows past their blob-delete grace lose their R2 blobs. The sweep is
// driven by the jobs runner (ADR-0003) and is safe to repeat — each transition
// selects only rows still in the source status, and a re-deleted blob prefix is
// already empty.
package gc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Abir66/mdfly/internal/server/db"
)

// DefaultBatchSize bounds how many rows one pass moves per transition, so a
// backlog is worked down over successive ticks instead of in one long
// transaction.
const DefaultBatchSize = 500

// Store is the subset of db.Client the sweep needs.
type Store interface {
	MarkExpired(ctx context.Context, now time.Time, limit int) (int64, error)
	MarkAbandoned(ctx context.Context, olderThan time.Time, limit int) (int64, error)
	ListBlobGCCandidates(ctx context.Context, gracedBefore time.Time, limit int) ([]db.BlobGCCandidate, error)
	SetBlobsDeletedAt(ctx context.Context, id int64) error
}

// Blobs is the subset of storage.Client the blob step needs.
type Blobs interface {
	DeletePrefix(ctx context.Context, slug string) error
}

// ErrInvalidAbandonGrace is returned by Sweep when AbandonGrace is not
// positive. A zero or negative grace would put the abandon cutoff at or after
// now and flip in-flight pending rows, so the pass refuses to run.
var ErrInvalidAbandonGrace = errors.New("abandon grace must be positive")

// ErrInvalidBlobDeleteGrace is returned by Sweep when BlobDeleteGrace is not
// positive. That would drop a Document's blobs the moment it goes terminal,
// leaving no window for an in-flight edge read to drain or for a mistaken delete
// to be caught.
var ErrInvalidBlobDeleteGrace = errors.New("blob delete grace must be positive")

// ErrMissingBlobs is returned by Sweep when Blobs is nil. The blob step only
// touches it once a candidate turns up, so an unwired deleter would otherwise
// sweep quietly for hours and then panic on the first terminal row.
var ErrMissingBlobs = errors.New("blob deleter is required")

// Service executes lifecycle sweeps. Db and Blobs are required, and AbandonGrace
// and BlobDeleteGrace must be positive — wiring supplies the graces from
// JobsConfig. BatchSize defaults to DefaultBatchSize and Now to time.Now.
type Service struct {
	Db              Store
	Blobs           Blobs
	AbandonGrace    time.Duration
	BlobDeleteGrace time.Duration
	BatchSize       int
	Now             func() time.Time
}

// Result counts what each step of one pass moved.
type Result struct {
	Expired      int64
	Abandoned    int64
	BlobsDeleted int64
}

// Sweep runs one pass of the two status transitions and the blob deletion, and
// returns what moved. Every step is attempted even if an earlier one fails, so a
// broken expiry cannot stall abandonment or blob cleanup; the returned error
// joins the failures. An invalid grace or a missing dependency fails the whole
// pass before any step runs.
func (s *Service) Sweep(ctx context.Context) (Result, error) {
	if err := s.validateConfig(); err != nil {
		return Result{}, err
	}

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

	deleted, err := s.deleteBlobs(ctx, now, limit)
	if err != nil {
		errs = append(errs, fmt.Errorf("delete blobs: %w", err))
	}
	res.BlobsDeleted = deleted

	return res, errors.Join(errs...)
}

// deleteBlobs drops the R2 prefix of every terminal row past its grace and
// stamps blobs_deleted_at so the row leaves the work-list. The stamp follows the
// delete: a crash in between leaves the row listed, and the next pass deletes an
// already-empty prefix. A row whose delete or stamp fails is left for the next
// pass rather than aborting the batch.
func (s *Service) deleteBlobs(ctx context.Context, now time.Time, limit int) (int64, error) {
	candidates, err := s.Db.ListBlobGCCandidates(ctx, now.Add(-s.BlobDeleteGrace), limit)
	if err != nil {
		return 0, err
	}

	var deleted int64
	var errs []error
	for _, c := range candidates {
		if err := s.Blobs.DeletePrefix(ctx, c.Slug); err != nil {
			errs = append(errs, fmt.Errorf("delete prefix %s: %w", c.Slug, err))
			continue
		}
		if err := s.Db.SetBlobsDeletedAt(ctx, c.ID); err != nil {
			errs = append(errs, fmt.Errorf("stamp %s: %w", c.Slug, err))
			continue
		}
		deleted++
	}
	return deleted, errors.Join(errs...)
}

// Job adapts Sweep to the jobs.Runner signature, logging per-pass counts and
// returning the failure so the runner's recorder can stamp it.
func (s *Service) Job(ctx context.Context) error {
	res, err := s.Sweep(ctx)
	if err != nil {
		slog.Error("lifecycle gc pass failed", "err", err,
			"expired", res.Expired, "abandoned", res.Abandoned, "blobs_deleted", res.BlobsDeleted)
		return err
	}
	slog.Info("lifecycle gc pass",
		"expired", res.Expired, "abandoned", res.Abandoned, "blobs_deleted", res.BlobsDeleted)
	return nil
}

func (s *Service) validateConfig() error {
	if s.AbandonGrace <= 0 {
		return fmt.Errorf("%w, got %s", ErrInvalidAbandonGrace, s.AbandonGrace)
	}
	if s.BlobDeleteGrace <= 0 {
		return fmt.Errorf("%w, got %s", ErrInvalidBlobDeleteGrace, s.BlobDeleteGrace)
	}
	if s.Blobs == nil {
		return ErrMissingBlobs
	}
	return nil
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
