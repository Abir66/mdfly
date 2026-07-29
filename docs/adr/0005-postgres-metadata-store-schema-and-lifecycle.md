# Metadata store: Postgres, six tables, soft-delete by status, lifecycle GC

All non-blob state lives in **Postgres**, reached over a single `DATABASE_URL` env var per the ADR-0003 portability constraints — the host is deliberately not part of this decision and may change without an ADR. R2 holds the bytes; Postgres holds the index. v1 ships **six tables and nothing else**: `documents` (the whole publish/update/view lifecycle row), `users`, `identities` (the OAuth `(provider, provider_id)` join, ADR-0007), `sessions` (CLI and planned browser auth), `exchange_codes` (the 60-second loopback-login indirection, ADR-0007), and `purge_queue` (durable CDN invalidation, ADR-0012). Load-bearing choices: all PKs are **`BIGSERIAL`** because internal IDs never appear in URLs, responses, or logs — the slug is the only public handle and tokens are random 32-byte strings — so unguessable IDs would cost 8 bytes per index entry and buy nothing. The **Bundle Manifest is `documents.manifest JSONB`** on the same row, not a child `document_files` table, because Bundle Limits (50 files anon, 500 owned) keep it well under the TOAST threshold and it is only ever read whole; a denormalized **`manifest_hash BYTEA`** sits alongside it for ADR-0006 optimistic concurrency and ADR-0010 cache keying, intentionally not unique and not indexed. **Tier is derived** from `owner_user_id IS NULL`, never stored, so it cannot drift. Cascade policy on user delete is split: `identities`, `sessions`, `exchange_codes` all `ON DELETE CASCADE`, while `documents.owner_user_id` is `ON DELETE SET NULL` so a user delete orphans content into Anonymous tier (subject to the 30-day expiry from then) rather than silently destroying bookmarked public URLs. `documents.status` is `TEXT + CHECK` (not a native `ENUM`, which needs a fiddly `ALTER TYPE` dance) over five values: **`pending`** (init written, not committed), **`published`**, **`deleted`** (user-triggered, 410), **`expired`** (anon row past `expires_at`, 410), **`abandoned`** (a `pending` row past its 1-hour grace, **404** — it was never published and no URL ever resolved it). `deleted_at` stamps the user-delete moment alongside the status flip, and on `users` it is the manual-delete tombstone. Anon `expires_at` is an **explicit** `now() + 30 days` refreshed on every anon Update, not a `GENERATED` column (Postgres `GENERATED` cannot reference `now()`) and not a trigger. `edit_token_hash` is set at first anon Publish, never refreshed, and **NULLed in the same transaction as Claim** so the secret surface disappears once session auth takes over. The three Preview Metadata columns (`title`, `excerpt`, `og_image_hash`) exist but are **left NULL in v1** — the view path extracts metadata from the render pass it was doing anyway (ADR-0010); they are reserved for the v2 dashboard's list view, which needs N documents' metadata from one query. A single **hourly in-process GC** (ADR-0003 ticker) drives the terminal transitions and blob deletion: flip anon `published` rows past `expires_at` → `expired`; flip `pending` rows older than 1 hour → `abandoned`; then for any row whose state calls for blob removal and whose **`blobs_deleted_at IS NULL`**, LIST the `documents/<slug>/` prefix, issue batched `DeleteObjects`, and stamp `blobs_deleted_at`. Blob removal waits a **24-hour grace** for `deleted` and `expired` and is **immediate** for `abandoned`. Cross-Update orphaned-asset cleanup stays deferred (ADR-0014's accepted leak). Every hot scan is served by a **partial index** whose `WHERE` clause matches the job that scans it. All timestamps are `TIMESTAMPTZ`, UTC at the storage boundary.

We chose Postgres over a KV store (Cloudflare KV, DynamoDB) because the natural queries are relational — "list this user's documents", "find rows past expiry whose blobs are still present", "drain the purge queue by `next_attempt_at`" — and over SQLite on a backend disk because ADR-0003's stateless-process constraint means the DB must not live in the app's filesystem. We chose **JSONB manifest** over a relational `document_files` table because the relational split would buy nothing (we never query "which slugs reference hash X" — cross-document dedup was rejected in ADR-0004, and the deferred asset GC is within-slug with both manifests in hand) while costing a join per render and a multi-row write per commit; the reversal is a migration if a Document ever grows to thousands of files, which Bundle Limits make a multi-version-out problem. We chose **`deleted_at` alongside a status flip** rather than collapsing existence into publication state, and we chose **distinct `expired` / `abandoned` statuses** over reusing `deleted`, because the states are observably different (410 versus 404) and an operator reading rows must be able to tell "user deleted it" from "anon TTL lapsed" from "init never finished" — collapsing them throws that signal away and makes every status index mean several things. We chose **`SET NULL` over `CASCADE`** for document ownership because public URLs get bookmarked, scraped, and embedded, and destroying them on account deletion is a worse default than orphaning into a tier that will expire them anyway. We chose a **DB-tracked `blobs_deleted_at` flag driven off `documents`** over a work-queue in Redis because the table already *is* the work list, and a second store would add a sync burden and drift against the source of truth; we chose **LIST + batched delete on the slug prefix** over tracking individual keys because ADR-0004 guarantees no cross-document blob sharing, so the prefix is authoritative. The 24-hour grace bounds the blast radius of a mistaken delete and lets in-flight edge reads drain; `abandoned` skips it because those bytes were never served by any URL. The honest costs: soft-deleted rows accumulate forever (fine at this scale, may need a vacuum pattern later), `last_seen_at` is lazily updated so session-activity observability lags the throttle window (revocation itself is synchronous via `revoked_at`), and the GC's correctness depends on the prefix-delete assumption holding — if cross-document blob sharing is ever introduced, this ADR must be revisited before it.

## Full DDL

```sql
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    display_name  TEXT NOT NULL,
    primary_email TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);

CREATE TABLE identities (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider    TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    email       TEXT,
    linked_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_id)
);
CREATE INDEX identities_user_idx ON identities (user_id);

CREATE TABLE sessions (
    id           BIGSERIAL PRIMARY KEY,
    token_hash   BYTEA NOT NULL UNIQUE,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_kind  TEXT NOT NULL CHECK (client_kind IN ('cli', 'browser')),
    device_label TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at   TIMESTAMPTZ
);
CREATE INDEX sessions_user_active_idx
    ON sessions (user_id, last_seen_at DESC)
    WHERE revoked_at IS NULL;

CREATE TABLE exchange_codes (
    code_hash   BYTEA PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id  BIGINT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);
CREATE INDEX exchange_codes_gc_idx ON exchange_codes (expires_at);

CREATE TABLE documents (
    id               BIGSERIAL PRIMARY KEY,
    slug             TEXT NOT NULL UNIQUE,
    idempotency_key  UUID NOT NULL UNIQUE,
    status           TEXT NOT NULL CHECK (status IN
                         ('pending', 'published', 'deleted', 'expired', 'abandoned')),
    owner_user_id    BIGINT REFERENCES users(id) ON DELETE SET NULL,
    edit_token_hash  BYTEA,
    manifest         JSONB NOT NULL,
    manifest_hash    BYTEA NOT NULL,
    title            TEXT,
    excerpt          TEXT,
    og_image_hash    BYTEA,
    bytes_total      BIGINT NOT NULL,
    file_count       INT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ,
    deleted_at       TIMESTAMPTZ,
    blobs_deleted_at TIMESTAMPTZ
);
CREATE INDEX documents_pending_gc_idx
    ON documents (created_at)
    WHERE status = 'pending';
CREATE INDEX documents_anon_expiry_idx
    ON documents (expires_at)
    WHERE status = 'published' AND expires_at IS NOT NULL;
CREATE INDEX documents_blob_gc_idx
    ON documents (updated_at)
    WHERE blobs_deleted_at IS NULL
      AND status IN ('deleted', 'expired', 'abandoned');
CREATE INDEX documents_owner_idx
    ON documents (owner_user_id, updated_at DESC)
    WHERE owner_user_id IS NOT NULL AND status = 'published';

CREATE TABLE purge_queue (
    slug            TEXT PRIMARY KEY,
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX purge_queue_due_idx ON purge_queue (next_attempt_at);
```
