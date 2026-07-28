// Package purge keeps the Cloudflare edge cache in step with Postgres
// (ADR-0031). Every commit and delete enqueues its slug to the durable
// purge_queue inside the same transaction as the row flip; this package turns a
// queued slug into the two prefix purges that cover it, attempts one inline right
// after the write, and drains the queue with backoff on a ticker until it
// succeeds.
package purge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/Abir66/mdfly/internal/server/db"
)

const (
	// DefaultBatchSize bounds how many slugs one drain pass purges. Two prefixes
	// per slug keeps a full batch inside Cloudflare's 100-operations-per-request
	// free-plan ceiling even if the calls were ever coalesced.
	DefaultBatchSize = 50

	// DefaultBaseBackoff is the wait after a first failed attempt; each further
	// attempt doubles it.
	DefaultBaseBackoff = time.Minute

	// DefaultMaxBackoff caps the doubling, so a long Cloudflare outage cannot
	// push a pending purge days into the future.
	DefaultMaxBackoff = 6 * time.Hour

	// inlineTimeout bounds the best-effort purge that follows a write. It is
	// detached from the request, so it needs its own deadline.
	inlineTimeout = 30 * time.Second

	// llmPathPrefix is the LLM twin's path segment (ADR-0016).
	llmPathPrefix = "llm"
)

// ErrMissingCDN is returned when a purge is attempted with no Cloudflare client
// wired. Failing loudly keeps the queue row in place instead of reporting a purge
// that never happened.
var ErrMissingCDN = errors.New("cloudflare client is required")

// ErrMissingHost is returned when Host is empty. A prefix without a host purges
// nothing at the edge, so the pass refuses to send it.
var ErrMissingHost = errors.New("purge host is required")

// ErrMissingStore is returned when Db is nil. Without the queue a purge cannot be
// cleared or backed off, so the pass refuses before it calls Cloudflare.
var ErrMissingStore = errors.New("purge store is required")

// Store is the subset of db.Client the purge queue needs.
type Store interface {
	ClaimPurgeDue(ctx context.Context, now time.Time, limit int) ([]db.PurgeTask, error)
	MarkPurgeDone(ctx context.Context, slug string, attempts int) error
	BackoffPurge(ctx context.Context, slug string, attempts int, nextAttemptAt time.Time) error
}

// CDN is the edge cache a purge targets.
type CDN interface {
	PurgePrefixes(ctx context.Context, prefixes []string) error
}

// Service invalidates the edge cache for queued slugs. Db, CDN and Host are
// required; BatchSize, the backoff bounds and Now fall back to their defaults.
type Service struct {
	Db          Store
	CDN         CDN
	Host        string // e.g. "mdfly.dev" — no scheme, this is a purge prefix
	BatchSize   int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	Now         func() time.Time
}

// Result counts what one drain pass moved.
type Result struct {
	Purged int
	Failed int
}

// Purge invalidates task's slug at the edge and drops its queue row. It is
// idempotent: the two prefix purges carry no document state, so a late or
// duplicated purge only makes the edge re-read whatever Postgres holds now, and
// clearing an already-cleared queue row is a no-op. task.Attempts fences the
// clear, so a write that landed mid-purge keeps its own queued purge. A failed
// purge leaves the row in place for the drain to retry.
func (s *Service) Purge(ctx context.Context, task db.PurgeTask) error {
	if err := s.validateConfig(); err != nil {
		return err
	}
	if err := s.CDN.PurgePrefixes(ctx, s.prefixes(task.Slug)); err != nil {
		return fmt.Errorf("purge %s: %w", task.Slug, err)
	}
	if err := s.Db.MarkPurgeDone(ctx, task.Slug, task.Attempts); err != nil {
		return fmt.Errorf("clear purge queue for %s: %w", task.Slug, err)
	}
	return nil
}

// AttemptInline purges slug in the background right after a write, as a latency
// optimization over waiting for the next drain tick. It is safe to lose: the
// queue row is the durable path, so a failure is logged and left to the drain.
// The goroutine runs on a context detached from the caller's, which is cancelled
// as soon as the response is written.
func (s *Service) AttemptInline(ctx context.Context, slug string) {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), inlineTimeout)
	go func() {
		defer cancel()
		// The write that just committed enqueued the slug with attempts reset to
		// zero, so that is the value this attempt is fenced against.
		if err := s.Purge(detached, db.PurgeTask{Slug: slug}); err != nil {
			slog.Warn("inline cdn purge failed, left queued", "slug", slug, "err", err)
		}
	}()
}

// Drain purges every slug whose next attempt is due, backing off the ones that
// fail. One failure neither stalls the batch nor loses its row; the returned
// error joins the failures.
func (s *Service) Drain(ctx context.Context) (Result, error) {
	if err := s.validateConfig(); err != nil {
		return Result{}, err
	}

	now := s.now()
	tasks, err := s.Db.ClaimPurgeDue(ctx, now, s.batchSize())
	if err != nil {
		return Result{}, fmt.Errorf("claim due purges: %w", err)
	}

	var res Result
	var errs []error
	for _, task := range tasks {
		if err := s.Purge(ctx, task); err != nil {
			res.Failed++
			errs = append(errs, err)
			if err := s.Db.BackoffPurge(ctx, task.Slug, task.Attempts, now.Add(s.backoffFor(task.Attempts))); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		res.Purged++
	}
	return res, errors.Join(errs...)
}

// Job adapts Drain to the jobs.Runner signature, logging per-pass counts.
func (s *Service) Job(ctx context.Context) {
	res, err := s.Drain(ctx)
	if err != nil {
		slog.Error("cdn purge drain failed", "err", err, "purged", res.Purged, "failed", res.Failed)
		return
	}
	if res.Purged > 0 {
		slog.Info("cdn purge drain", "purged", res.Purged)
	}
}

// prefixes returns the two prefixes covering slug: the human page and the LLM
// twin. Each covers the bare page plus every sub-path under it, so one pair
// invalidates a whole document (ADR-0031).
func (s *Service) prefixes(slug string) []string {
	return []string{
		s.Host + "/" + slug,
		s.Host + "/" + llmPathPrefix + "/" + slug,
	}
}

// backoffFor doubles the base wait per attempt already made, capped at
// MaxBackoff. The shift is bounded before it can overflow.
func (s *Service) backoffFor(attempts int) time.Duration {
	base, ceiling := s.BaseBackoff, s.MaxBackoff
	if base <= 0 {
		base = DefaultBaseBackoff
	}
	if ceiling <= 0 {
		ceiling = DefaultMaxBackoff
	}
	delay := base
	for i := 0; i < attempts && delay < ceiling; i++ {
		delay *= 2
	}
	if delay > ceiling {
		return ceiling
	}
	return delay
}

func (s *Service) validateConfig() error {
	if s.Db == nil {
		return ErrMissingStore
	}
	if s.CDN == nil {
		return ErrMissingCDN
	}
	if s.Host == "" {
		return ErrMissingHost
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

// HostFromBaseURL reduces a base URL to the host (with port, if any) that
// Cloudflare purge prefixes are built from, dropping the scheme plus any path,
// trailing slash or query. A schemeless value is parsed as if it carried one; an
// unparseable value falls back to itself, which validateConfig then rejects or
// the edge treats as a prefix that purges nothing.
func HostFromBaseURL(baseURL string) string {
	raw := baseURL
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimSuffix(baseURL, "/")
	}
	return u.Host
}
