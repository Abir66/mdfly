# Postgres v1 schema: five tables, BIGSERIAL PKs, soft-delete by `deleted_at`, JSONB manifest, partial indexes for hot scans

The v1 metadata store ships five tables and nothing else: `documents` (the entire publish/update/view lifecycle row), `users` (mdfly accounts), `identities` (the OAuth (provider, provider_id) join from ADR-0016), `sessions` (CLI + planned-v2 browser auth tokens), `exchange_codes` (the 60-second loopback-login indirection from ADR-0016). All primary keys are `BIGSERIAL` because the slug is the only externally-meaningful handle in the system — internal IDs never appear in URLs, response bodies, or logs, so unguessable IDs (UUIDv7) would buy nothing while costing 8 bytes per index entry and breaking foreign-key ergonomics. Soft-delete is a `deleted_at TIMESTAMPTZ` column on `documents` and `users` alongside the live row (slug stays reserved forever per ADR-0002; a manual user-delete leaves an audit tombstone) rather than a separate `_archive` table or a status enum value, because `WHERE deleted_at IS NULL` is a one-clause filter that composes with every other query without making the live-row scans implicitly conditional on a status check. Cascade policy on user delete is deliberately split: `identities`, `sessions`, and `exchange_codes` all `ON DELETE CASCADE` because their existence is meaningless without their user, while `documents.owner_user_id` is `ON DELETE SET NULL` so a user-delete orphans their content into Anonymous tier (subject to the 30-day expiry from then) rather than silently destroying public URLs that other people may have bookmarked. The `tier` axis (anon vs owned) is **derived** from `documents.owner_user_id IS NULL` rather than stored — single source of truth, impossible drift, Bundle Limits enforced in application code from that check. The `documents.status` column is `TEXT` with a `CHECK` constraint listing the two values (`'pending'`, `'published'`) rather than a native Postgres `ENUM`, because `ALTER TYPE ... ADD VALUE` is fiddly and `TEXT + CHECK` extends in one migration line. The Bundle Manifest is stored as `documents.manifest JSONB` on the same row as everything else, not in a child `document_files` table, because Bundle Limits cap manifest size well under the TOAST threshold and the manifest is only ever read whole (server-side at view time per ADR-0018) — no inner-key queries justify the relational split. A denormalized `documents.manifest_hash BYTEA` lives alongside the JSONB for ADR-0012 optimistic-concurrency lookup and ADR-0018 cache keying; it is intentionally **not** unique (two slugs can coincidentally share manifests) so no index is created on it. Three commit-time-cached columns — `title`, `excerpt`, `og_image_hash` — back the Preview Metadata so the SSR HTML render does not need to re-parse markdown to populate the `<head>`. Expiry timestamps are explicit (`now() + 30 days` for anon rows, `NULL` for owned) and refreshed on every anon Update, rather than `GENERATED` columns — `now()` cannot be referenced from `GENERATED`, and the explicit `INSERT`/`UPDATE` value is simpler than a trigger anyway. Hot-scan indexes are all **partial**: pending-row GC (`status='pending'`), anon expiry cron (`expires_at IS NOT NULL AND deleted_at IS NULL`), owner's `list --mine` (`owner_user_id IS NOT NULL AND deleted_at IS NULL`), active sessions enumeration (`revoked_at IS NULL`), exchange-code GC (`expires_at`). Each partial index is small because its `WHERE` clause filters to a fraction of the table. All timestamps are `TIMESTAMPTZ`; UTC at the storage boundary, conversion is app-side. Edit Tokens have no separate expiry: `documents.edit_token_hash` is set at first anon Publish, refreshed never (the same token survives every Update), and **NULLed in the same transaction as Claim** so once session auth takes over the secret surface disappears. The `users.display_name` denormalization rule is "first Identity wins" — locked at signup, stable across multi-provider linking, with a v2 `--update-name` flow deferred per ADR-0017.

We chose `BIGSERIAL` over **UUIDv7**: UUIDv7 would have been the right call if internal IDs were ever exposed (so consumers couldn't enumerate them), but they never are — slug is the public handle, session/edit tokens are random 32-byte strings, never the integer ID. BIGSERIAL's monotonic + 8-byte properties give tighter indexes, cheaper joins, and easier-to-eyeball debugging at zero cost. We chose **JSONB manifest on the document row** over a relational `document_files (document_id, path, hash, size)` table: the relational split would buy us nothing — we never query "which slugs reference asset hash X" (cross-doc dedup was rejected as a design direction; ADR-0017's deferred Asset GC is within-slug only, both manifests in hand at Update time) — and it would cost us a join per view-time render plus a multi-row write per commit. JSONB is the right normalization level when the document model is "atomic blob you read whole, version atomically, no inner-key queries." We chose **`deleted_at` over a `status='deleted'` enum value** because soft-delete is orthogonal to publish-state (a pending row can be GC'd as expired without ever being "deleted"; an anon row that hits expiry follows the same path as a manual delete; the lifecycle of *publication* and the lifecycle of *existence* are not the same axis). Collapsing them would force every status-related index to mean two things, and "active" would become `status IN ('published') AND deleted_at IS NULL` everywhere — at which point the enum was pretending. We chose **ON DELETE SET NULL for `documents.owner_user_id`** over **ON DELETE CASCADE** because public URLs are bookmarked, scraped, and embedded; silently destroying them when a user deletes their account is a worse default than orphaning into anon tier where the existing 30-day expiry will eventually GC them anyway. We chose **derived tier over a stored `tier` column** because the source-of-truth is `owner_user_id`; a stored `tier` column would have to be kept in sync on Claim (anon → owned) and on hypothetical un-Claim (not in v1), and any drift between `tier` and `owner_user_id IS NULL` is a bug that's hard to spot. The cost of derivation is one boolean comparison per Bundle-Limits check — negligible. We chose **explicit `expires_at` over `GENERATED`** because Postgres `GENERATED` columns cannot reference `now()` (only deterministic expressions of other columns), and a trigger-based approach is more complexity than a one-line value in the commit `INSERT`. We chose **`TEXT + CHECK` over native `ENUM` for `status`** because we want to be able to add `'archived'` or `'frozen'` or similar in a future v2 with one `ALTER TABLE` rather than a multi-step `ALTER TYPE` dance; the runtime cost difference is nil. We chose **partial indexes everywhere** rather than full indexes because every hot scan in the system has a natural `WHERE` predicate (pending-only, owned-only, active-only) — paying for index pages on rows that will never be scanned for that purpose is wasted IO and write amplification. We chose **`exchange_codes.code_hash` as PK** over a surrogate `id` because the hash is the natural and only lookup key (rows are short-lived, single-purpose, and never referenced from elsewhere except via the same hash). We chose **session pre-creation at OAuth callback time** (with `session_id` written into `exchange_codes`) over **session creation at exchange time** because pre-creation makes the callback-then-exchange transition a state-handoff rather than a state-machine: if CLI never exchanges, the orphan session row is harmless (`last_seen_at` never moves, no API requests are authorized against it, eventual cron sweep can prune sessions with no API hits older than N days if we want — but in v1 we tolerate them). The honest costs we accept: (a) we will eventually outgrow `JSONB manifest` if a Document grows to thousands of files — but Bundle Limits cap at 500 files for owned, 50 for anon, so this is a multi-version-out problem and the relational split is reversible by migration; (b) `deleted_at` rows live forever and accumulate — at our v1 scale (manual user-delete tickets per ADR-0017, 30-day anon expiry, low publish rate) this is fine, but a hot vacuum pattern may become necessary in v2; (c) `last_seen_at` lazy update means session-revocation observability is delayed by the throttle window — acceptable since `revoked_at` flips synchronously and the `WHERE revoked_at IS NULL` filter is exact.

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
    id              BIGSERIAL PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,
    idempotency_key UUID NOT NULL UNIQUE,
    status          TEXT NOT NULL CHECK (status IN ('pending', 'published')),
    owner_user_id   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    edit_token_hash BYTEA,
    manifest        JSONB NOT NULL,
    manifest_hash   BYTEA NOT NULL,
    title           TEXT,
    excerpt         TEXT,
    og_image_hash   BYTEA,
    bytes_total     BIGINT NOT NULL,
    file_count      INT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ
);
CREATE INDEX documents_pending_gc_idx
    ON documents (created_at)
    WHERE status = 'pending';
CREATE INDEX documents_anon_expiry_idx
    ON documents (expires_at)
    WHERE expires_at IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX documents_owner_idx
    ON documents (owner_user_id, updated_at DESC)
    WHERE owner_user_id IS NOT NULL AND deleted_at IS NULL;
```
