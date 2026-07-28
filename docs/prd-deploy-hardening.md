# PRD: Deployment & Production Hardening

**Triage:** `ready-for-agent`
**Governed by:** ADR-0028 (app rate limiting on Upstash), ADR-0029 (Oracle Always Free VM + in-process tickers — supersedes ADR-0007), ADR-0030 (blob deletion + lifecycle GC), ADR-0031 (durable CDN purge queue — supersedes the purge mechanism of ADR-0021).

> Supersedes the stale "Deployment & Production Hardening" sketch in `docs/issues/README.md` (which referenced a never-written Azure/`reclaim` design). Compute target is Oracle, not Azure; blobs are *deleted*, never "reclaimed".

## Problem Statement

mdFly's core publish / update / view / delete loop works, but the service is not safe to leave running in production on the free tier:

- **Nothing throttles writes at the application layer.** Cloudflare's edge rule (ADR-0021) limits by IP only and can't see who the caller is, so a scripted attacker with an Edit Token — or one rotating IPs — can hammer `publish` / `update` / `delete`, squat slugs, and run up R2 PUTs.
- **Storage and rows leak forever.** Anonymous Documents are supposed to expire after 30 days and deleted Documents are supposed to have their blobs removed, but no job actually does either. Pending rows from abandoned `init` calls also accumulate. R2 fills with bytes nobody can reach and the owner keeps paying for them.
- **Cache purges are best-effort and lost on restart.** A failed Cloudflare purge on update/delete currently strands stale content at the edge for up to 24h with no retry that survives a process restart — unacceptable for "I updated my Document but the old one still shows".
- **The deploy target assumed paid infrastructure.** ADR-0007 targeted DigitalOcean App Platform on student credit; the hard constraint is now free-tier-only with no spend, and every "run this periodically" design silently assumed a scheduler that a free host may not provide.

## Solution

Harden the backend for a genuinely-free, always-on deployment and close the abuse and leak vectors:

- Run on an **Oracle Cloud Always Free VM** (always-on, no scale-to-zero, no CPU-minute cap), so periodic work can run as **in-process tickers** with no external scheduler.
- Add an **identity-aware application rate limiter** on Upstash Redis in front of the write paths, beneath the existing Cloudflare edge limit.
- Add a **lifecycle GC** that marks Anonymous Documents `expired` at their TTL and never-committed rows `abandoned`, then **deletes the corresponding R2 blobs** by slug prefix after a safety grace.
- Make cache invalidation **durable**: enqueue a purge in the same transaction as the commit/delete, attempt it inline, and drain a retry queue on a ticker so a purge is never permanently lost.

To a publisher, editor, viewer, and the owner, the product behaves exactly as documented in CONTEXT.md — expired/deleted URLs return 410, never-published ones return 404, edits show fresh within moments, and abuse is throttled — while costing nothing to run.

## User Stories

1. As the **owner**, I want the backend to run on an always-on free-tier host, so that I pay nothing and periodic jobs can tick in-process without an external scheduler.
2. As the **owner**, I want the backend to retain its portability constraints (stateless, env-var config, single Dockerfile, `/healthz`), so that I can move off Oracle later without a rewrite.
3. As the **owner**, I want an app-level rate limit on `publish/init`, so that a scripted attacker can't mass-mint slugs and squat the global namespace.
4. As the **owner**, I want the rate limit keyed by **Edit Token** on `update` and `delete`, so that abuse is tied to the actual editor rather than a shared or rotating IP.
5. As an **anonymous publisher**, I want the anonymous rate limit keyed by my real client IP (via `CF-Connecting-IP`), so that one abuser behind Cloudflare doesn't get me throttled by proxy-IP collision.
6. As a **legitimate publisher**, I want generous-enough limits (10/min and 30/hr per subject), so that normal CLI use never trips the limiter.
7. As a **CI user**, I want a `429` to carry `X-RateLimit-*` and `Retry-After` headers, so that my script can back off correctly (CLI exit code 5).
8. As the **owner**, I want the limiter to **fail open** when Redis is unreachable, so that a Upstash outage degrades to "unthrottled" (edge limit still stands), never to "all writes blocked".
9. As a **viewer / AI agent**, I want read paths (SSR HTML, LLM twin, CDN assets) to never be rate-limited, so that reading a Document is always fast and served from edge cache.
10. As the **owner**, I want an hourly lifecycle GC job, so that expiry, abandonment, and blob deletion happen automatically without me running anything by hand.
11. As an **anonymous publisher**, I want my Document to auto-expire 30 days after my last publish/update, so that throwaway shares don't live forever.
12. As a **viewer**, I want an expired Document's URL to return **410 Gone**, so that I can tell it once existed and is now gone (distinct from a 404).
13. As the **owner**, I want a `pending` row whose `commit` never arrived to be marked `abandoned` after a 1-hour grace, so that stale init rows don't accumulate.
14. As a **viewer**, I want an abandoned slug to return **404**, so that a Document that was never published reads as "never existed".
15. As the **owner**, I want the blobs of a `deleted` or `expired` Document deleted after a 24-hour grace, so that a mistaken delete has a recovery window and in-flight reads can drain before bytes vanish.
16. As the **owner**, I want an `abandoned` row's blobs deleted immediately, so that bytes that were never served don't wait out a grace they don't need.
17. As the **owner**, I want blob deletion to remove the whole `documents/<slug>/` prefix, so that per-slug-namespaced blobs (ADR-0001) are cleaned with no per-file bookkeeping.
18. As the **owner**, I want blob deletion recorded via `blobs_deleted_at`, so that the job is idempotent and never re-lists a slug it already cleaned.
19. As the **owner**, I want blob deletion batched (LIST + `DeleteObjects`), so that a many-file Bundle is removed in few R2 calls.
20. As an **editor**, I want my update to purge the edge cache for both the human and LLM URL families, so that neither a browser nor an AI agent sees stale content.
21. As an **editor**, I want the purge attempted immediately on commit, so that in the common (Cloudflare-healthy) case my change is visible within moments.
22. As the **owner**, I want the purge intent enqueued in the same transaction as the row flip, so that a committed write can never be left without a pending purge.
23. As the **owner**, I want a failed or rate-limited purge retried by a ticker that survives process restarts, so that a purge is never permanently lost (the ADR-0021 goroutine's retry state died with the process).
24. As the **owner**, I want purges issued by **prefix** (`mdfly.dev/<slug>`, `mdfly.dev/llm/<slug>`), so that one operation per family covers the bare page and every sub-path within the Cloudflare free-plan 100-ops/request ceiling.
25. As the **owner**, I want the purge queue **deduplicated by slug**, so that a rapid sequence of updates to one slug collapses to a single pending purge.
26. As the **owner**, I want purge to remain **not a commit precondition**, so that a Cloudflare API outage never loses a user's write (worst case: stale ≤ 24h `s-maxage`).
27. As an **editor**, I want two updates in a row to both end with the edge showing the latest bytes, so that a late or duplicated purge is harmless because purge always re-reads the latest DB state.
28. As the **owner**, I want all periodic jobs (purge drain ~15 min, lifecycle GC hourly) started at boot and stopped cleanly on shutdown, so that they run reliably and don't block a graceful exit.
29. As a **developer**, I want the rate limiter, GC sweep, purge queue, and blob deleter each behind a small interface with fakes, so that I can test their logic in isolation without real Redis, R2, or Cloudflare.
30. As the **owner**, I want the Upstash and Cloudflare credentials and the job intervals/grace windows configured via env vars, so that config stays out of code and portable per ADR-0029.
31. As the **owner**, I want the lifecycle GC to skip Owned Documents for expiry, so that only Anonymous Documents (which have an `expires_at`) are ever expired.
32. As the **owner**, I want the GC to do bounded batches per pass, so that a backlog is worked down over successive ticks rather than in one long-running sweep.
33. As the **owner**, I want cross-Update orphaned-Asset cleanup to remain **out of scope** (ADR-0017's accepted leak), so that this work stays focused on delete/expiry/abandon.

## Implementation Decisions

**Modules** (approved with the user; deep modules behind interfaces, thin adapters isolated for fakeability):

- **`ratelimit`** (deep) — `Allow(ctx, key string) (allowed bool, err error)`. Implements dual fixed-window counting (10/min + 30/hr) via a single house operation `IncrementWithTTL(ops)` that pipelines every window's `INCR` + `EXPIRE` into one round trip, where each key embeds a time-bucket step `floor(now/window)`. Fails open on any backing-store error. Backed by a thin **`redis`** RESP/TCP-pool adapter behind an interface.
- **rate-limit middleware + client-IP resolver** (shallow, in `middleware`/`httpx`) — resolves the subject key (Edit Token from `Authorization` on update/delete; otherwise `CF-Connecting-IP`, trusted only from Cloudflare's published ranges since `RemoteAddr` is a Cloudflare edge IP), calls `ratelimit.Allow`, and on deny returns `429` with `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `Retry-After`. Wraps `publish/init`, `update/init`, and `DELETE /documents/:slug` only.
- **`jobs`** (deep-ish) — a ticker-driven runner: `Register(name string, interval time.Duration, fn func(ctx))` + `Start`/`Stop`, with an injectable clock so ticks can be driven in tests. Started at boot, stopped on graceful shutdown.
- **`service/gc`** (deep) — the hourly lifecycle sweep as pure logic over `db` and `storage` interfaces: (a) mark anon `published` rows past `expires_at` → `expired`; (b) mark `pending` rows older than the 1h grace → `abandoned`; (c) for rows needing blob deletion with `blobs_deleted_at IS NULL`, call `storage.DeletePrefix` then stamp `blobs_deleted_at`. Grace: 24h for `deleted`/`expired`, immediate for `abandoned`. Bounded batch per pass.
- **`storage.DeletePrefix(ctx, slug)`** (deep) — LIST `documents/<slug>/` + batched `DeleteObjects`; safe as a whole-prefix delete because blobs are per-slug namespaced (ADR-0001).
- **`service/purge`** (deep) — durable queue operations (`Enqueue(tx, slug)` in the commit/delete transaction; `Claim`/`MarkDone`/`Backoff`) + idempotent `Purge(ctx, slug)` that issues the two prefix purges and always re-reads latest DB state. Cloudflare prefix-purge is a thin **`cloudflare`** adapter behind an interface. The immediate post-commit attempt is a best-effort goroutine (safe to lose; the queue is the durable path). The ~15-min drain job lives here.
- **`db` additions** — see schema below; batch status-transition queries (`MarkExpired`, `MarkAbandoned`, `RowsNeedingBlobDelete`, `SetBlobsDeletedAt`) and `purge_queue` CRUD.

**Schema changes** (golang-migrate, `TEXT + CHECK` extension pattern per migration `0002`):

- Extend `documents.status` CHECK to `('pending','published','deleted','expired','abandoned')`.
- Add `documents.blobs_deleted_at TIMESTAMPTZ` (NULL = blobs still present).
- New table:

```sql
CREATE TABLE purge_queue (
    slug            TEXT PRIMARY KEY,       -- dedup by slug
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```
*(Shape encodes the ADR-0031 decision: slug PK gives dedup; `next_attempt_at` drives backoff; a row is deleted on purge success.)*

- Consider a partial index for the blob-delete scan (rows in a terminal state with `blobs_deleted_at IS NULL`), mirroring ADR-0019's partial-index posture.

**HTTP / behavior contracts:**

- `expired` and `deleted` → **410 Gone**; `abandoned` → **404**; matching CONTEXT.md and the existing `ErrGone`/`ErrNotFound` → 410/404 mapping in `db`.
- Rate-limit `429` envelope is the standard `{"error":{code,message}}` plus the three rate-limit headers; CLI maps to exit code 5.
- Purge covers exactly two prefixes per slug; success is not a commit precondition.

**Compute / ops (ADR-0029):** Oracle Always Free ARM VM, always-on; all jobs in-process on tickers; no Cloudflare Worker/Cron trigger and no internal job endpoint. Portability constraints from ADR-0007 retained. Provisioning the VM, Upstash instance, and Cloudflare API token are **manual HITL prerequisites**, not agent-run (see external-setup note below).

## Testing Decisions

**What makes a good test here:** exercise observable external behavior through the module's public interface, not its internals. Assert on outcomes — "a `pending` row older than 1h becomes `abandoned` and its prefix is deleted", "the 61st request in a minute is denied", "a failed purge leaves a `purge_queue` row that a later drain clears" — using fakes for Redis/R2/Cloudflare so tests are deterministic and infra-free. Don't assert on call counts to private helpers or on Redis key spellings.

**Modules to unit-test in isolation** (confirmed with the user — all four deep modules):

- **`ratelimit`** — window rollover across the time-bucket boundary, dual-window enforcement (per-minute vs per-hour tripping independently), and fail-open when the backing store errors. Fake counter (or miniredis).
- **`service/gc`** — which rows transition to `expired` vs `abandoned`, the 24h-vs-immediate grace math, that Owned Documents are never expired, and that blob deletion fires only when `blobs_deleted_at IS NULL`. Fake `db` + fake R2.
- **`service/purge`** — transactional enqueue + slug dedup, claim/backoff/`MarkDone` lifecycle, and that `Purge` is idempotent and re-reads latest state (a duplicate/late drain is a no-op). Fake `cloudflare` client.
- **`storage.DeletePrefix`** — LIST + batched `DeleteObjects`, empty prefix (no-op), and pagination past one batch. Fake object store.

**Prior art:** the existing end-to-end blackbox test + CI (S20) is the integration backstop and should be extended to cover a delete → 410 → blob-gone path. The stale README's own instinct — "verifiable via miniredis + fake Cloudflare client, no real infra" — is the right shape for the isolated tests; keep the fakes in-repo. Thin adapters (`redis`, `cloudflare`) get a light contract test each, not deep coverage.

## Out of Scope

- **Cross-Update orphaned-Asset GC** — deleting blobs whose refcount drops after an Update. Deferred per ADR-0017 (accepted leak); this PRD is delete/expiry/abandon only.
- **Provisioning and edge config** — standing up the Oracle VM, the Upstash instance, the Cloudflare API token/zone rules. These are manual HITL prerequisites (an AFK agent can't self-apply console/credential operations), tracked as separate setup steps, not code slices here.
- **CLI distribution, CI/CD pipeline, self-host packaging** — separate concerns.
- **Auth/session rate limiting** — v1 has no login on these paths; identity is the Edit Token or IP. Session-keyed limits arrive with OAuth (post-v1).
- **DB read caches** — ruled out this session; the edge cache already keeps the DB off the view hot path.

## Further Notes

- **Every slice that touches an external service must ship setup guidance** — exact Cloudflare/Upstash/Oracle/R2 dashboard steps, env vars, token scopes, and verification — because the owner runs all external setup by hand on the free tier. This is a standing project rule.
- **ADR bookkeeping:** ADR-0029 supersedes ADR-0007 (status line added); ADR-0031 supersedes the purge mechanism of ADR-0021 (amend note added). The `docs/issues/README.md` "Deployment & Production Hardening" section and its ADR-0028–0032 references are stale and will be rewritten when this PRD is cut into issues (`/to-issues`).
- **Job correctness is independent of always-on:** the durable `purge_queue` and DB-tracked `blobs_deleted_at` survive reboots regardless; Oracle's always-on property only removes the external-trigger tax and the "will my goroutine be killed" hazard of a scale-to-zero host.
