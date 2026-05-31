# mdfly — Domain Context

Glossary of terms used in this project. Implementation details belong elsewhere.

## Terms

### Publish
Act of creating a **new** mdfly URL from a local markdown file. `mdfly publish a.md` always mints a fresh slug and appends it to the file's [[Local State]] mapping — repeated `publish` on the same file produces multiple independent Documents at distinct URLs. To modify an existing Document, use [[Update]] instead. Three-phase wire protocol:
- **init**: CLI sends `{idempotency_key, bundle, edit_token?, requested_slug?}` (where `bundle` is `{root_path, files:[{path,hash,size}]}`); server INSERTs a `documents` row with `status='pending'` (using `ON CONFLICT (idempotency_key) DO NOTHING` to be retry-safe), stores `SHA256(edit_token)` if present, returns `{slug, presigned_urls}` where `presigned_urls` maps a representative bundle path to its presigned PUT URL — server dedupes by R2 blobkey (`slug+hash+ext`), so paths sharing a blobkey with a representative are absent from the map and covered by the representative's upload.
- **R2 upload**: CLI PUTs each blob directly to R2 using its presigned URL, which pins `Content-Length` so R2 rejects mismatched-size bytes at upload.
- **commit**: CLI sends `{idempotency_key}`; server HEAD-verifies blobs and atomically flips the row to `status='published'`. Returns `{url, manifest_hash}`.

Inline fast-path: Bundles ≤256 KB total and ≤5 files skip the R2 PUT round trip and inline bytes into `commit`. Pending rows are GC'd by a cron after 1h. No Redis on the write path — idempotency lives entirely in Postgres via the UNIQUE constraint on `documents.idempotency_key`.

### Idempotency-Key
Client-minted UUIDv4 header sent on `/api/publish/init` and `/api/publish/commit`. Persisted as a UNIQUE column on the `documents` row: init uses `INSERT ... ON CONFLICT (idempotency_key) DO NOTHING` so a retried init lands on the same row, commit identifies the target row by `WHERE idempotency_key = $1`. Same key with a meaningfully different payload returns **422** so accidental collisions surface loudly. No Redis cache involved.

### Document
A published markdown file rendered at a mdfly URL. Has exactly one Root `.md` file plus zero or more Assets.

**Read surfaces:** every Document is reachable on two parallel URL families served by the same backend resolution pipeline (ADR-0018) — `mdfly.dev/<slug>` and `mdfly.dev/<slug>/<logical-path>` return fully SSR'd HTML for humans; `mdfly.dev/llm/<slug>` and `mdfly.dev/llm/<slug>/<logical-path>.md` return resolved raw markdown for AI agents (see [[LLM Path]]). Both share cache keys, the same `404`/`410` semantics, and the same Bundle Manifest lookup; they differ only in the final wrap step.

**Visibility model:** all Documents are public — there is no auth-gated view path in v1. Privacy is provided by the unguessability of the slug (62^8 ≈ 2 × 10^14 anonymous-slug space). Anyone with the URL can read; sharing the URL is sharing the Document.

**Search-engine indexing:** every slug response (HTML and `/llm/` twin) carries `X-Robots-Tag: noindex, nofollow`, and `/robots.txt` serves blanket `Disallow: /`. Users who want their Document indexed by Google must self-publish off mdfly. See [[Edge]] and ADR-0021.

### Asset
Any non-root file referenced by a Document — images, additional `.md` files, etc. — resolved and uploaded alongside the root. Manifest keys are **relative to the project root** (currently always the root file's dir; raising it to an ancestor via a flag is deferred). Files are uploaded **byte-identical** — no link rewriting, hashes stay stable (see [[Content Hash]]). The Document's project-root **absolute path** is stored in the [[Bundle Manifest]] so the server can resolve both relative and absolute references at render time. Which files the CLI uploads is a CLI-side concern: it recurses and uploads everything reachable that resolves **inside** the project root — a reference whose target lands outside the root (via `../` or an absolute path) is **skipped with a warning and left verbatim**, while one that climbs above the root but folds back inside is uploaded under its in-root key. The server still understands above-root keys (leading `../`) via the `?up=N` URL scheme (see [[Bundle Manifest]]); the CLI just no longer emits them in v1. Gated by a total size cap and a total file count cap per Bundle.

### Bundle
The complete set of files uploaded for one Publish: root `.md` + all transitively-reachable Assets. Subject to Bundle Limits.

### Bundle Limits
Hard caps applied at Publish time: max total bytes and max file count across all files in a Bundle, plus a single-file size cap. CLI computes before upload and aborts early; server re-validates. Tiered by identity:
- **Anonymous tier**: 25 MB total, 50 files, 10 MB single file
- **Owned tier**: 100 MB total, 500 files, 25 MB single file

### Slug
URL-safe identifier for a Document. All Documents — Anonymous, Owned, and Claimed — live in a **single global slug namespace** under `mdfly.dev/<slug>`. There is no per-user URL namespace; user identity does not appear in the URL. Two ways a slug is assigned:
- **Random slug**: 8-char base62 (`[a-zA-Z0-9]{8}`), server-generated. Used for all Anonymous Documents and as the fallback for Owned Documents that don't request a name. Unguessable; not enumerable.
- **User-chosen slug**: requested by a logged-in User at Publish time, `[a-z0-9-]{1,64}`. First-come-first-served against the global namespace — if `notes` is taken, the next user must pick a different name. Reserved words (`api`, `cdn`, `login`, `settings`, `_`, etc.) are blocklisted.

A random slug and a user-chosen slug share the same uniqueness index; the server retries random generation on the (vanishingly rare) collision. User-chosen slugs are additionally validated against the [[Reserved Words]] blocklist.

Slug length: 1–64 chars. **Single-char and 2-char slugs are legal** and FCFS in the owned tier (`a`, `fy`, etc.); short cute slugs are part of the product. Numeric-only slugs (`12345678`) are also legal.

### API Surface
HTTP surface mounted under `mdfly.devapi.mdfly.dev/v1/`. The `v1` prefix is cheap version insurance: lets a future `v2` endpoint cut without touching siblings. `Update` reuses the same `init` + `commit` pair as `publish`, distinguished only by `parent_manifest_hash` being present (and matching) at `commit` time — same 3-phase wire (ADR-0011), same idempotency-key (ADR-0013), same optimistic concurrency (ADR-0012). The inline-blob fast-path (ADR-0011) lives on the same `commit` endpoint via an optional `inline_blobs` field, gated by `inline_accept: true` in the `init` response. The view path's `GET api.mdfly.dev/v1/documents/:slug` is **unauthenticated** — visibility is public-only by ADR-0008; privacy is slug obscurity. State-changing endpoints always require the token in the `Authorization` header, never just a cookie — eliminates CSRF (ADR-0016).

**Error envelope** (all `4xx`/`5xx` bodies): `{"error": {"code": "<machine-enum>", "message": "<human readable>", "details": {...}?}}`. The `code` is the script-friendly enum (maps to CLI exit codes — see `CLI` term); `message` is human-facing. Chosen over RFC 9457 problem+json because the 12-endpoint surface doesn't earn the ceremony. Rate-limit responses (`429`) carry `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `Retry-After` headers.

**v1 endpoint table:**

| Endpoint | Method | Auth | Body | Success | Notable errors |
|---|---|---|---|---|---|
| `api.mdfly.dev/v1/publish/init` | POST | Bearer (session) OR Edit Token OR none (anon-new) | `{idempotency_key, bundle:{root_path, files:[{path,hash,size}]}, edit_token?, requested_slug?}` | `200 {slug, presigned_urls:{path:url}, inline_accept:bool}` (one entry per unique blobkey, keyed by a representative path) | `400` bad bundle, `401` bad token, `403` `--slug` w/o session, `409` slug taken or reserved, `413` bundle too big, `422` idempotency-key reused with different payload, `429` rate-limit |
| `api.mdfly.dev/v1/publish/commit` | POST | same as init | `{idempotency_key, parent_manifest_hash?, inline_blobs?:[{hash,bytes}]}` | `200 {url, slug, manifest_hash}` | `404` no pending row for key, `409` blob HEAD mismatch or `parent_manifest_hash` stale, `410` pending row GC'd |
| `api.mdfly.dev/v1/documents/:slug` | GET | none (public) | — | `200 {slug, owner_user_id?, tier, manifest_hash, title?, files:[path], created_at, updated_at, expires_at?}` | `404` never existed, `410` deleted/expired |
| `api.mdfly.dev/v1/documents/:slug` | DELETE | Bearer OR Edit Token | — | `204` | `401` bad token, `403` not yours, `404`, `410` |
| `api.mdfly.dev/v1/documents/:slug/claim` | POST | Bearer AND Edit Token (both) | — | `200 {url, slug, owner_user_id}` | `401` no session, `403` no/wrong Edit Token, `404`, `409` already owned by another user, `410` |
| `api.mdfly.dev/v1/documents` | GET | Bearer | `?limit=&cursor=` | `200 {documents:[...], next_cursor?}` | `401` |
| `api.mdfly.dev/v1/auth/cli/start` | GET | none | `?port=&state=` | `302` → provider OAuth URL (after setting `mf_cli_intent` cookie) | `400` bad port/state |
| `api.mdfly.dev/v1/auth/callback/:provider` | GET | OAuth state cookie | `?code=&state=` | `302` → `http://127.0.0.1:<port>/callback?code=mfxc_...&state=...` | `400` bad state, `403` provider denied, `502` provider error |
| `api.mdfly.dev/v1/auth/cli/exchange` | POST | none (presents `mfxc_`) | `{exchange_code}` | `200 {token: "mfst_...", user:{id,display_name}, session_id}` | `400`/`410` exchange code invalid/expired/consumed |
| `api.mdfly.dev/v1/auth/device/start` | POST | none | `{provider}` | `200 {device_code, user_code, verification_uri, interval, expires_in}` | provider device-flow errors forwarded |
| `api.mdfly.dev/v1/auth/device/poll` | POST | none | `{device_code}` | `200 {token, user, session_id}` or `400 {error:{code:"authorization_pending"}}` | RFC 8628 error codes |
| `api.mdfly.dev/v1/auth/logout` | POST | Bearer | `{all?: bool}` | `200 {revoked: N}` | `401` |
| `api.mdfly.dev/v1/me` | GET | Bearer | — | `200 {user_id, display_name, identities:[...]}` | `401` |

**Apex routing** (`mdfly.dev`): the apex is Worker-routed at the edge per [[Apex Routing]] / ADR-0023, dispatching between Cloudflare Pages (landing + legal pages: `/`, `/about`, `/pricing`, `/terms`, `/privacy`, `/contact`, `/abuse`, `/robots.txt`, `/_landing/*`) and the backend (everything else: `/<slug>[/<logical-path>]` for SSR Documents per ADR-0018, `/llm/<slug>[/<logical-path>.md]` for the LLM twin per [[LLM Path]], `/cli/login` for the OAuth chooser per ADR-0016, `/healthz` for the DO health probe per ADR-0007). The API lives at the separate subdomain `api.mdfly.dev/v1/*` (this surface, above). Assets live at `cdn.mdfly.dev/*` via R2 custom-domain per ADR-0021. The v2 React dashboard will live at `app.mdfly.dev` (DNS unprovisioned in v1).

### Reserved Words
Blocklist of strings that must never become a [[Slug]] because they collide with current or planned top-level route prefixes, or with brand / marketing / legal squat targets. Enforced at owned-slug `init` and applied to the random-slug retry loop (vanishingly improbable but the check is the same code path).

Initial v1 contents — kept as a Go slice constant in `internal/slug/reserved.go`:

| Category | Words |
|---|---|
| **Routes (live in v1)** | `api`, `cdn`, `healthz`, `cli`, `auth`, `abuse`, `_` |
| **Routes (planned v2)** | `dashboard`, `settings`, `login`, `logout`, `signup`, `account`, `admin`, `docs`, `app` |
| **Brand** | `mdfly` (exact match only — `mdfly-docs`, `mdfly-howto`, etc. are legal user-chosen slugs) |
| **DNS-confusion subdomains** | `www`, `mail`, `ftp`, `smtp` |
| **Marketing / legal pages** | `about`, `pricing`, `terms`, `privacy`, `contact` |

**Maintenance rule.** Additions are by code edit + redeploy. **No retroactive eviction**: a newly-reserved word stays held by any existing owner; it just stops being mintable for new owners. Removals from the list are allowed only after auditing that no live route uses the path. There is no DB table and no admin UI — the blocklist is implementation detail, not user-managed configuration.

### Claim
The act of transferring an Anonymous Document to a logged-in User via `mdfly claim <slug>`. Requires the local Edit Token plus an authenticated session. Sets `documents.owner_user_id`, NULLs `documents.edit_token_hash` (session auth fully supersedes — the secret surface is minimized), and clears `documents.expires_at` (Owned tier has no expiration); all in a single transaction. URL is unchanged — the original random `/<slug>` continues to identify the Document.

### Anonymous Document
Document published without a logged-in user. URL form: `mdfly.dev/<slug>` (random slug). Ownership proven by a secret Edit Token returned at publish time. Auto-expires 30 days after Publish (or last Update); expiry triggers the same path as a Delete. Can be promoted to Owned via [[Claim]].

### Owned Document
Document with an `owner_user_id` set. URL form is always `mdfly.dev/<slug>` (no `/<user>/` prefix — see ADR on flat global slug namespace). Two sub-shapes share this status:
- **Fresh-owned**: published while logged in. Slug is user-chosen against the global FCFS namespace (or a random fallback).
- **Claimed**: started as an Anonymous Document and was later transferred via `mdfly claim <slug>`. URL stays as the original random slug.

Either way, Owned status confers: Owned-tier Bundle Limits, no auto-expiration, ownership proven by account session rather than Edit Token.

### User
Authenticated identity in mdfly. Optional — only required for Owned Documents. A User is a single mdfly account that can have one or more [[Identity]] rows attached (GitHub at v1 launch, Google to follow, more OAuth providers addable). Stored as `users (id, display_name, primary_email?, created_at, deleted_at?)`; `display_name` is denormalized from the **first** linked Identity at signup time and stays stable thereafter — multi-provider users do not see their name flicker as they re-authenticate against different providers (a `--update-name` flow is deferred to v2). `primary_email` is nullable because some providers (GitHub with hidden email) return only a `noreply` alias or nothing useful, and it is never used as a unique key. There is **no mdfly-owned handle** in v1 — URLs are `mdfly.dev/<slug>` (flat namespace, see [[Slug]]) and there are no profile pages, so no need for a globally-unique user-chosen identifier. See ADR-0016 and [[Postgres Schema]].

### Identity
A single (provider, provider_id) pair attached to a [[User]]. Stored in `identities (user_id, provider, provider_id, email?, linked_at, primary key (provider, provider_id))`; `ON DELETE CASCADE` on `user_id` so a deleted User's identity rows go too (lets the same provider account sign up fresh later). Each successful OAuth login (whichever flow) upserts one Identity row. `provider` is the short string `github` | `google` | etc.; `provider_id` is the provider's stable numeric/string ID (GitHub user ID, Google `sub`), not the email or login — those rename, the ID is forever. v1 ships GitHub-only per ADR-0017 so each User has exactly one Identity; multi-Identity linking arrives with Google support post-v1 and will be **explicit only** (no auto-link-by-email, ever). Foundational rule baked in now: a login attempt landing on a `(provider, provider_id)` already attached to a different `user_id` is rejected with **409** — silent cross-user merging is destructive and unrecoverable.

### Login Flow
How `mdfly login` proves who the User is and produces a [[Session]] token. Two flows, same backend OAuth machinery, same `sessions` table outcome.

**Loopback (primary, default).** Modelled on `claude login` / `gh auth login --web` / `gcloud auth login`. UX: `mdfly login` → browser auto-opens → user clicks **Continue with GitHub** or **Continue with Google** on the mdfly-hosted chooser page at `mdfly.dev/cli/login` → user approves on the provider → browser tab shows "Signed in! You can close this tab." (auto-closes when permitted) → CLI prints `Logged in as <display_name>`. Under the hood: CLI binds `127.0.0.1:0` (OS-assigned ephemeral port), generates a `state` CSRF nonce, opens browser to `/cli/login?port=<p>&state=<csrf>`; backend stashes `{port, state}` in a short-lived signed `mf_cli_intent` cookie, runs OAuth code flow with the chosen provider (using its own `oauth_state` nonce), upserts Identity/User on success, mints a single-use 60s [[Exchange Code]] `mfxc_...`, redirects browser to `http://127.0.0.1:<p>/callback?code=mfxc_...&state=<csrf>`; CLI's local server validates `state`, then POSTs the exchange code to `api.mdfly.dev/v1/auth/cli/exchange` and receives the real `mfst_` token, which is persisted to `~/.mdfly/credentials`.

**Device flow (fallback, `--device`).** OAuth 2.0 device authorization grant. UX: `mdfly login --device` prints a `user_code` and a verification URI (`github.com/login/device` / `google.com/device`), the user opens the URI on any browser-capable device and enters the code, the CLI polls the backend until the provider confirms approval, then receives the same `mfst_` token. Used in headless environments — SSH sessions, Docker containers, CI runners — where opening a local browser isn't possible.

### Exchange Code
Single-use, short-lived (60-second TTL) intermediary token in the [[Login Flow]] loopback path, formatted `mfxc_` + base64url(32 random bytes). Carried in the URL when the backend redirects the browser to the CLI's localhost callback (`http://127.0.0.1:<p>/callback?code=mfxc_...`), then exchanged by the CLI via `POST api.mdfly.dev/v1/auth/cli/exchange` for the real [[Session]] token. Server stores `SHA256(mfxc_...)` in a transient `exchange_codes` table with `consumed_at` timestamp; the exchange endpoint rejects already-consumed or expired codes. The `mfxc_` token never authorizes anything other than its own exchange — its existence in URL/log surfaces is acceptable because of the 60s single-use window. This indirection avoids putting the real `mfst_` session token in any URL (which would leak it to browser history, system access logs, and the URL bar).

### Session
Authenticated handle bound to a [[User]], used to authorize CLI and browser requests. Stored as `sessions (id, token_hash, user_id, client_kind, device_label, created_at, last_seen_at, revoked_at)` where `token_hash = SHA256(mfst_<32B base64url>)` and `client_kind ∈ {'cli', 'browser'}`. The same row format and same middleware path serves both clients — only the wire transport differs: CLI sends `Authorization: Bearer mfst_...`, browser (planned v2 dashboard) sends a `HttpOnly` `Secure` `SameSite=Lax` cookie. State-changing API endpoints always require the token in the `Authorization` header even when a session cookie is present, eliminating CSRF surface. The `mfst_` prefix is visible-on-purpose for secret-scanning tools (consistent with `mftk_` Edit Token convention). Sessions persist until explicit revocation — `mdfly logout` revokes the current session, `mdfly logout --all` revokes every active session for the User. The plaintext `mfst_` token is returned exactly once at login (CLI: in the exchange response; browser: as a `Set-Cookie` header) and discarded server-side after persisting the hash.

**No separate API-key type in v1.** The Session token doubles as the credential for CI / scripted use cases: users `mdfly login --device --label ci-bot` once, lift the `mfst_` from `~/.mdfly/credentials`, and paste it into a CI secret. The CLI reads `MDFLY_TOKEN` env var first and falls back to the credentials file, so CI invocation is `MDFLY_TOKEN=mfst_... mdfly publish docs/`. Each login can carry an optional `--label <text>` that populates `device_label` for `mdfly sessions` listing — "abir-mbp", "github-actions", "render-deploy-hook", etc. Named/scoped API keys are explicitly deferred to v2 alongside the dashboard; when added they will live as additional columns on this same `sessions` row (or a sibling table) rather than as a parallel auth subsystem.

### Edit Token
Secret credential bound to an Anonymous Document. **Exactly one token per slug**, generated **client-side by the CLI** at first Publish (`crypto/rand`, 32 bytes formatted as `mftk_` + base64url, 256 bits of entropy) and sent to the server in the init request body as plaintext over TLS. The server stores only `SHA256(T)` in `documents.edit_token_hash` and discards the plaintext after persisting; the plaintext lives client-side in `~/.mdfly/credentials` (mode 600), keyed by slug. Required to update or delete an Anonymous Document, and required (alongside a logged-in session) to [[Claim]] one. Verification: server fetches the stored hash and runs `subtle.ConstantTimeCompare(SHA256(presented_token), stored_hash)`.

**Format:** `mftk_` prefix + base64url-encoded 32 random bytes, e.g. `mftk_5fK8...43chars`. The `mftk_` prefix is visible-on-purpose so secret-scanning tools (GitHub push protection, gitleaks, truffleHog) can block accidental commits. Wire transport: `Authorization: Bearer <token>` on `update` / `delete` / `claim` requests.

On successful [[Claim]] the `documents.edit_token_hash` column is set to `NULL` as part of the same transaction that sets `owner_user_id` — session auth fully supersedes and the now-redundant secret surface is removed. No separate token expiry: the token lives exactly as long as the Anonymous Document does (30 days from last Publish/Update, per [[Anonymous Document]]); when the doc expires or is deleted, the token dies by reference.

Multi-device anonymous editing is intentionally not a first-class flow in v1: users either log in and [[Claim]] the Document (session auth then supersedes the Edit Token entirely) or copy `~/.mdfly/credentials` between machines by hand. Dedicated `mdfly export-token` / `import-token` verbs are deferred to v2 per ADR-0017. A lost Edit Token on an unclaimed Anonymous Document is unrecoverable; the Document remains viewable but uneditable until its 30-day expiration.

### Local State
CLI-side mapping from absolute local file path → **list of slugs** (`{path: [slug1, slug2, ...]}`), stored in `~/.mdfly/state.json`. A file accumulates slugs over time: every `mdfly publish a.md` mints a new slug and appends it; every `mdfly update a.md` modifies an existing one (selected from the list). Moving or renaming the local file loses the mapping (recoverable via `mdfly update --slug <s>` or `mdfly list`). Trimmed by [[Remove]].

### Remove
CLI verb `mdfly remove <slug-or-file>` deletes entries from [[Local State]] only — touches `~/.mdfly/state.json`, makes **no server call**, the Document at the URL is unaffected. Distinct from [[Delete]], which hard-deletes server-side. Use when a file was renamed/moved/discarded and you want a clean `mdfly list`, or when you want to stop the bare-file shortcut from dispatching to `update` on a file you no longer track.

Resolution:
- **Slug arg** (`mdfly remove abc12345`) — removes that slug from whichever path maps it; errors with exit `2` if the slug isn't in Local State.
- **File arg** (`mdfly remove ./old.md`) — removes the path's entire slug list (could be 1, could be many). `--slug <s>` narrows to a single slug under that path.
- **`--all`** — wipes the entire state.json (credentials in `~/.mdfly/credentials` are untouched). TTY confirmation required (`Remove local tracking for N slug(s)? [y/N]`); non-TTY without `--yes` errors with exit `2`.

No confirmation on single-slug or single-file removes — they're cheap to undo (`mdfly update --slug <s>` re-maps; `mdfly list --mine` shows owned docs server-side so nothing is permanently lost as long as the slug still exists).

### CLI
The `mdfly` command-line tool users invoke to Publish, login, update, or delete Documents.

**Smart bare-command shortcut:** `mdfly <file>` (no verb) dispatches based on the file's [[Local State]] mapping — 0 slugs → [[Publish]]; 1 slug → [[Update]]; 2+ slugs → interactive picker on a TTY, error on non-TTY. Loud feedback every run (`Published as ...` or `Updating ...`) so the user sees which path ran. Detection rule: first positional arg is treated as a file path if it exists on disk; else as a verb. Explicit `mdfly publish <file>` and `mdfly update <file>` remain the canonical, scriptable forms.

**Exit codes** (categorical, `gh`-style — scripts branch on the code):
- `0` — success
- `1` — generic / unclassified error (fallback only; every site should pick a more specific code if possible)
- `2` — user / input error (bad flag, missing required arg, file not found, file too big for tier)
- `3` — auth error (no session, token revoked, 401, missing Edit Token for an Anonymous Document)
- `4` — conflict (409 — slug taken at publish time, optimistic-concurrency loss on Update, cross-user link collision)
- `5` — rate-limited or quota (429, Bundle Limits exceeded at server-side re-validation)
- `6` — network / server error (5xx, DNS failure, connection timeout, R2 PUT failure after retries)

CI scripts can `mdfly publish docs/ || case $? in 3) re-login;; 4) skip;; 6) retry;; *) fail;; esac`.

**Output streams** (UNIX-strict split):
- **stdout** carries the one canonical artifact of the command and nothing else. `publish` / `update` → the URL on a single line. `whoami` → `display_name`. `list` → newline-separated `<slug>\t<path>`. `claim` → URL of the now-Owned Document. Pipe-friendly by construction: `mdfly publish a.md | pbcopy` copies just the URL.
- **stderr** carries everything else: progress bars, upload counters, narration ("Uploading 3/12 files…"), warnings, prompts, errors. Color and spinner ANSI sequences are only emitted when `isatty(stderr)` is true; `NO_COLOR=1` env disables color globally; `--plain` forces no-color regardless of TTY.
- **`--json`** swaps the entire stdout payload to a single JSON object (verb-specific shape, documented per command). Stderr is unaffected by `--json` — progress and errors still go there, just without color/spinner so logs parse cleanly.
- **Interactive prompts require a TTY on stdin.** The 2+-slug picker, claim confirmation, and login browser-open confirmation each error out with exit code `2` (`user/input error`) if `!isatty(stdin)` rather than defaulting silently — silent defaults in scripts are a footgun (e.g. picking the wrong slug to update).

**Global flags** (accepted by every verb):
- `--json` — swap stdout to JSON (see Output streams above)
- `--plain` — disable color/spinner regardless of TTY
- `-v` / `--verbose` — extra stderr narration (request URLs, retries, timing)
- `-q` / `--quiet` — suppress non-error stderr; errors still print
- `--api <url>` — override backend base URL (dev / staging / self-host)
- `--no-update-check` — skip the once-per-24h release-check
- `--help` / `-h` — per-verb help
- `--version` — print `mdfly vX.Y.Z (<git-sha>, built <date>)` and exit `0`

Subcommand-local flags (NOT global): `--force` (`update`/`delete`), `--slug` (`update`/`delete`/`claim`), `--device`/`--label`/`--provider` (`login`), `--all`/`--wipe-local` (`logout`).

**Config precedence**, highest wins:
1. explicit flag (`--api https://...`)
2. env var (`MDFLY_API`, `MDFLY_TOKEN`, `NO_COLOR`, `MDFLY_CONFIG_DIR`)
3. config file `<config-dir>/config.toml` (opt-in; never auto-written)
4. compiled default (`MDFLY_API=https://api.mdfly.dev/v1`, color on if `isatty(stderr)`)

Env > config because env is the CI-friendly per-invocation override; flag > env because explicit beats implicit; config file is for power users who want a persistent dev/staging override without exporting env vars. `MDFLY_CONFIG_DIR` overrides the location of the entire CLI state directory (default `~/.mdfly/`) — affects where `credentials`, `state.json`, and `config.toml` are read and written. Primary use case: redirect off cloud-synced home directories (Dropbox / iCloud / OneDrive) per [[Shared-PC Hygiene]].

**Update check.** At most once per 24h, the CLI HEAD-s `api.github.com/repos/mdfly/mdfly/releases/latest`, caches the result in `~/.mdfly/state.json` alongside [[Local State]], and prints `New version vX.Y.Z available. brew upgrade mdfly` to stderr at exit if newer. Failures are silent. Disabled by `--no-update-check` or by setting the cache TTL to forever via config.

**v1 verb contract.** Stdout shapes are the canonical artifact per verb; `--json` swaps the same content to JSON. Stderr always carries narration/progress/errors regardless of mode.

| Verb | Positional | Verb-local flags | stdout on success | `--json` shape | Prompts (TTY-only) | Exit codes beyond `0` |
|---|---|---|---|---|---|---|
| `publish <file>` | file path | `--slug <s>` (owned only) | URL on one line | `{url, slug, manifest_hash, tier, expires_at?}` | none | `2` bad input / file too big / `--slug` w/o session, `4` slug taken, `5` quota, `6` net |
| `update <file>` | file path | `--slug <s>`, `--force` | URL on one line | `{url, slug, manifest_hash, changed_files}` | 2+-slug picker if mapped to multiple | `2` 0-mapped & no `--slug` or non-TTY & 2+ mapped, `3` Edit Token / session missing, `4` 409 w/o `--force`, `6` net |
| `delete <slug-or-file>` | slug or file | `--slug <s>`, `--yes` | `Deleted: <slug>` | `{slug, deleted_at}` | `Delete <slug>? [y/N]` unless `--yes` | `2` ambiguous / non-TTY w/o `--yes`, `3` not your token/session, `6` net |
| `remove <slug-or-file>` | slug or file | `--slug <s>`, `--all`, `--yes` | `Removed local tracking for <slug>.` (or `Removed N entries.` on `--all`) | `{slug, file, removed}` or `{removed: N}` | confirm on `--all` only | `2` slug/file not in Local State, non-TTY `--all` w/o `--yes` |
| `claim <slug>` | slug | `--yes` | URL on one line (now-owned) | `{url, slug, owner_user_id}` | `Claim <slug> for <display_name>? [y/N]` unless `--yes` | `2` no slug, `3` no session OR no Edit Token, `4` already owned by another user, `6` net |
| `login` | — | `--device`, `--label <s>`, `--provider <github\|google>` | `Logged in as <display_name>` | `{user_id, display_name, session_id, provider}` | none (browser opens) | `2` bad provider, `3` user denied OAuth, `6` browser-open or net failure |
| `logout` | — | `--all`, `--wipe-local` | `Logged out.` / `Logged out of N sessions.` / `Wiped local mdfly state at ~/.mdfly/.` (additive on `--wipe-local`) | `{revoked: N, wiped_local: bool}` | none | `0` even if not logged in (idempotent no-op; `--wipe-local` also idempotent) |
| `whoami` | — | — | `<display_name> (<provider>:<provider_id>)` | `{user_id, display_name, identities:[{provider, provider_id, email}]}` | none | `3` no session |
| `list` | — | `--mine`, `--all`, `--limit <n>` | `<slug>\t<path>\t<tier>\t<updated_at>` lines | `{documents: [...]}` | none | `3` `--mine` w/o session, `6` net |
| `<file>` (bare) | file path | (none — verb sugar, see [[CLI]] dispatch rules) | dispatched verb's stdout, preceded by `→ publish` or `→ update <slug>` to **stderr** | inherits dispatched verb | inherits | inherits |

`--mine` is the default on `list` when logged in; `--all` is the default when not (the local-state listing is the only thing available pre-auth).

### Root
The entry point of a Bundle, identified by `root_path` in the [[Bundle Manifest]]. In v1 it is always a single markdown **file**, rendered when the viewer hits the bare Document URL `mdfly.dev/<slug>`. `root_path` is **nullable** in the data model: an empty root means the Document is a folder (no entry file), and the bare URL serves a [[Directory Listing]] of the project root instead. The CLI cannot produce a folder-root in v1 (single-`.md` publish always sets `root_path`); the nullable path is server-side forward-compat for a future `mdfly publish <folder> --root <dir>`.

### Viewer
The human-facing read surface for a Document at `mdfly.dev/<slug>[/<key>]`: server-rendered chrome with a collapsible left **file-tree sidebar** (the whole Bundle; folders are native collapsible nodes, the current file is highlighted and its ancestors auto-expanded), a **center** that dispatches on the addressed node's type, and a **breadcrumb** of the logical path at the top of the center column. No client-side router — every navigation is a full page load (ADR-0018). The absolute `project_root` is never emitted; tree and breadcrumb are built only from project-root-relative keys (privacy). Center dispatch by node type:
- markdown → rendered HTML, with a **Raw toggle** (top-right) that lazy-fetches the byte-identical source blob from `cdn.mdfly.dev` once, caches it client-side, and swaps it in;
- image → shown inline;
- text/code ≤ `PreviewMaxBytes` (1 MB) and verified non-binary (valid UTF-8, no NUL) → server-side chroma-highlighted source; oversize or binary → a download card;
- any other type → a metadata card (name, size) + Download link to the CDN blob;
- a directory → a [[Directory Listing]].

Collapsing the sidebar leaves a thin rail (state persisted per-browser); under a mobile breakpoint the sidebar becomes a drawer behind a top bar. The Viewer's CSS/JS ship as content-hashed static assets under the reserved `/_static/*` path, not inlined — this supersedes S19's inline-stylesheet approach. See [[Directory Listing]], [[Root]], and ADR-0018.

### Directory Listing
The center view when a `mdfly.dev/<slug>/<key>` path resolves to a **directory** — `key` is a path prefix of one or more [[Bundle Manifest]] keys rather than an exact file key. Lists immediate child folders first, then files (each a link; files show size), derived **synthetically** from manifest keys: a folder "exists" only if it contains an uploaded file (the CLI uploads only reachable files in v1, so a listing shows manifest contents, not the publisher's on-disk directory). If the directory contains a `README.md` (else `index.md`), that file is rendered below the listing. The bare Document URL falls back to a root Directory Listing when [[Root]] is empty.

### Content Hash
SHA256 of a file's **raw bytes** — applied uniformly to markdown files and Assets alike. Used as the file's stored filename in R2: `<hash>.<ext>`. Hashes are computed over the file's untransformed user-authored content (no link rewriting), so the same bytes on disk always produce the same hash; this gives end-to-end content integrity (local file hash == stored object hash) and automatic CDN cache-immutability. See [[Bundle Manifest]] for how logical filenames are mapped to hashes.

### Bundle Manifest
Per-Document `files_by_path` keyed by each file's **project-root-relative logical path** (`index.md`, `sub/y.md`, `imgs/logo.png`), mapping to its [[Content Hash]] and size, plus a `root_path` field naming which entry is the Root markdown and a `project_root` field holding the publisher's **absolute** project-root path. The CLI sends the wire form (`bundle:{root_path, project_root, files:[{path,hash,size}]}`) — also path-keyed; the server stores it directly without re-indexing. `project_root` lets the server resolve references the same way the CLI walk computed keys: **every** reference (absolute or relative) is resolved into the publisher's absolute path space and made relative to `project_root`, so a `../`-chain that climbs above the root before folding back inside it still resolves to the right in-root key. Canonical copy lives in Postgres (`documents.manifest jsonb`). Markdown files are stored byte-identical to what the user wrote; the Manifest is what lets `![](./logo.png)` inside a markdown file resolve to `cdn.mdfly.dev/documents/<slug>/<hash>.png` at render time.

**`?up=N` URL scheme** — a key above the project root (`../x.md`) cannot be a path segment (browsers/Cloudflare/Go `ServeMux` strip `..` via RFC 3986 dot-segment removal before the server sees it). Such keys are addressed with a surviving query param: `/<slug>/<tail>?up=N` where `N` = count of leading `../` and `tail` = the rest **with its extension intact** (the strip-extension convention was reversed; see this term's routing note above). Server reconstructs `key = strings.Repeat("../", N) + tail`. No server-side cap on `N`. Assets above the root are immune (they render to `cdn.mdfly.dev/.../<hash>.<ext>`, no path in the URL). The v1 CLI skips outside-root files, so above-root keys are not produced in practice; the scheme is retained server-side for forward-compatibility (e.g. a future `--root` flag).

Manifest is consumed exclusively server-side. The Go backend reads it from Postgres at view time, fetches the requested markdown blob from R2, rewrites logical references (assets → `cdn.mdfly.dev/documents/<slug>/<hash>.<ext>`; cross-md links → `mdfly.dev/<slug>/<path>` for humans or `mdfly.dev/llm/<slug>/<path>.md` for agents), then either SSRs the result to HTML (human routes) or returns the resolved markdown bytes verbatim (LLM routes). Reference rewriting runs as a **Goldmark AST transformer**: it walks the parsed tree and rewrites every `ast.Image` and `ast.Link` destination through the manifest resolver **before** the HTML renderer runs — one pass covers both `![]()` images and `[]()` cross-md links, against the in-root key resolved relative to the rendered page's own directory. The bare URL `GET /<slug>` renders `root_path` (or a [[Directory Listing]] of the project root when `root_path` is empty); `GET /<slug>/<logical-path>` addresses any node by its **exact manifest key, extension included** (`docs/api/auth.md`, `assets/logo.png` — no `.md` stripping; reverses the original strip-extension convention). Resolution: exact key → serve that file in the [[Viewer]] chrome; else a path that is a directory prefix of some key → [[Directory Listing]]; else 404. Trailing slashes are not required and are 301-canonicalized. The Manifest is **never** sent to the client. See ADR-0018 (supersedes ADR-0009) and the amended ADR-0001.

### Update
Modifies the Bundle of an **existing** slug in place. CLI verb: `mdfly update <file>`. No version history; slug stable forever, bytes at the URL are not. Disambiguation by [[Local State]] mapping count:
- **0 slugs mapped**: error — `<file> has no published version. Use 'mdfly publish' or pass --slug <s>.` (`--slug` lets the user target a slug the local state forgot, e.g. after file rename.)
- **1 slug mapped**: update that slug.
- **2+ slugs mapped**: TTY → interactive picker showing each slug + URL; non-TTY → error, must pass `--slug <s>`.

Concurrency: server tracks a `manifest_hash` per slug; commit carries `parent_manifest_hash`, server returns **409 Conflict** if the slug moved underneath. CLI surfaces 409 as `Conflict: '<slug>' updated elsewhere since you started. Re-run to retry, or pass --force to overwrite.` `--force` skips the check.

(Asset garbage collection — deleting blobs whose refcount drops to 0 after an Update — is deferred to a later milestone. In v1, orphaned Assets remain in R2 and are paid for; this is an accepted leak.)

### Delete
Owner-triggered removal of a Document via `mdfly delete <slug>`. Soft-deletes the Postgres row (sets `deleted_at`, slug stays reserved and not reusable) and immediately hard-deletes the Bundle's blobs from R2. The URL responds **410 Gone** thereafter — distinct from 404 (never existed). Expiration of an Anonymous Document follows the same code path as a Delete.

### Preview Metadata
Server-extracted OpenGraph tags emitted in the SSR'd HTML at view time (ADR-0018). Resolution order: frontmatter `title` / `description` / `image` if present; otherwise inferred — title from first H1, description from first paragraph (truncated), image from first referenced image Asset. Enables link unfurl in chat clients.

**v1 (S07): computed at view time, no column cache.** The view path renders the body anyway, so it extracts Preview Metadata from that same render pass (`markdown.Render → (html, Meta, error)`) and fills the `<head>` directly — no separate parse. Per-page OG (`/slug/<path>`) therefore resolves for free from each page's render; per-page metadata is never stored. The `documents.title` / `excerpt` / `og_image_hash` columns exist in the schema (ADR-0019) but are **left NULL in v1** — nothing reads them (cache hits serve the full cached HTML head; the LLM twin is raw markdown).

**Deferred to v2 dashboard:** populate those columns at commit time so `list --mine` / `app.mdfly.dev` can render a document list (title + excerpt + thumbnail across N docs) from one SQL query instead of N renders. The commit-time extraction (root blob → render → resolve og image path to manifest hash) was built in S07 and reverted; re-introduce it with the dashboard. **Reword the column rationale in ADR-0019 + this entry at that time** — the original "computed at commit so the SSR render does not re-parse markdown" no longer matches v1.

### Markdown Flavor
The renderer accepted by mdfly. GitHub Flavored Markdown (CommonMark + tables, task lists, strikethrough, autolinks) plus YAML frontmatter, code-block syntax highlighting, Mermaid diagrams, and KaTeX math. Raw HTML embedded in markdown is **stripped**, not rendered — Documents are publicly viewable and raw HTML is an XSS surface. The Go backend runs the renderer + sanitizer server-side via Goldmark + chroma + bluemonday (ADR-0018); Mermaid + KaTeX render client-side via lazy-loaded JS shims because Go ports are heavy and the JS libs are mature. A matching CLI-side renderer (for `mdfly preview`) is deferred to v2 per ADR-0017.

### LLM Path
Parallel URL family at `mdfly.dev/llm/<slug>` and `mdfly.dev/llm/<slug>/<logical-path>.md` that returns the same resolved markdown as the human routes but skips the HTML wrap — `Content-Type: text/plain; charset=utf-8`, no Goldmark render, no sanitizer (markdown stays as markdown). Same backend resolution pipeline reads the [[Bundle Manifest]] from Postgres, fetches the requested markdown blob from R2, and rewrites asset references to `cdn.mdfly.dev/documents/<slug>/<hash>.<ext>` and cross-md links to absolute `mdfly.dev/llm/<slug>/<sibling-path>.md` URLs so an AI agent crawling one Document can follow the chain via `curl` alone, no JavaScript runtime required. Cacheable to the same degree as human routes (`Cache-Control: public, max-age=300, s-maxage=86400`). Returns `410 Gone` for deleted/expired Documents and `404` for never-existed slugs, matching `/<slug>`. See ADR-0018.

### Frontmatter
Optional YAML block at the top of the Root markdown. Recognized keys: `title`, `description`, `image` (path to a referenced image Asset). Used to populate Preview Metadata and the rendered page title. Unknown keys are preserved but ignored.

### Postgres Schema
The canonical relational layout backing every term in this glossary. v1 ships five tables: `documents`, `users`, `identities`, `sessions`, `exchange_codes`. The full DDL — column types, constraints, indexes, GC strategy — lives in ADR-0019; the rules below are the load-bearing decisions every other CONTEXT.md term assumes.

- **PK style: `BIGSERIAL`** across all tables. Slug is the public handle; internal IDs are never exposed in URLs or API responses, so unguessability of IDs is not a property we need. 8-byte integers keep indexes tight and joins cheap.
- **Soft-delete by `deleted_at TIMESTAMPTZ` column**, not by status enum or row removal. `documents.deleted_at IS NOT NULL` means the Bundle's R2 blobs are gone but the slug stays reserved forever per ADR-0002. `users.deleted_at IS NOT NULL` is the manual user-delete tombstone; under ADR-0017 this is a ticket-driven process, not self-service.
- **Cascade policy on user delete**: `identities`, `sessions`, `exchange_codes` all `ON DELETE CASCADE` (their existence is bound to the user); `documents.owner_user_id` is `ON DELETE SET NULL` so the user's docs orphan into Anonymous tier rather than getting silently swept.
- **Tier derived from `documents.owner_user_id IS NULL`**, not stored. Single source of truth; impossible to drift; Bundle Limits enforced in application code from this check.
- **`documents.status`** carries only `'pending'` and `'published'` — soft-delete is orthogonal via `deleted_at`. Stored as `TEXT` with `CHECK` constraint, not native `ENUM`, because extending `TEXT + CHECK` is a trivial migration whereas `ALTER TYPE ... ADD VALUE` on an enum is fiddly.
- **`documents.manifest JSONB`** stored on the row, not in a child `document_files` table. Bundle Limits cap manifest size well under TOAST threshold; the manifest is only ever read whole (at view time, server-side per [[Bundle Manifest]] / ADR-0018), never queried by inner key.
- **`documents.manifest_hash BYTEA`** denormalized alongside the JSONB for ADR-0012 optimistic concurrency lookup and for cache-keying ADR-0018's resolved-markdown render. Not unique (two slugs could coincidentally share a manifest); no index.
- **`documents.title`, `documents.excerpt`, `documents.og_image_hash`** computed at commit and cached on the row (per [[Preview Metadata]] / ADR-0018) so HTML render does not re-parse markdown to fill `<head>`.
- **`documents.expires_at TIMESTAMPTZ NULLABLE`** set explicitly at commit — anon row gets `now() + 30 days`, owned row gets `NULL`. Refreshed on every anon Update (commit transaction overwrites). Not a generated column because `now()` cannot be referenced from `GENERATED`.
- **`documents.edit_token_hash BYTEA NULLABLE`** holds `SHA256(mftk_...)` for Anonymous Documents only; `NULL` for Owned-from-birth and **NULLed at [[Claim]] time** so the secret surface disappears once session auth takes over.
- **`sessions.token_hash BYTEA UNIQUE`** is the auth lookup key (per [[Session]]); plain text is never persisted. `revoked_at TIMESTAMPTZ NULLABLE` is the revoke flag — middleware filters `WHERE revoked_at IS NULL`. `last_seen_at` is updated lazily (throttled in memory) to avoid write-heavy auth path.
- **`exchange_codes`** is the loopback-login indirection (per [[Exchange Code]] / ADR-0016): PK is `code_hash BYTEA` (the natural key), the row is created at OAuth callback time *together with* its `sessions` row (referenced by `session_id`), and either GC'd at `expires_at` (60 s after creation) or marked `consumed_at` on successful exchange.
- **Partial indexes for hot scans**: `documents (created_at) WHERE status='pending'` (1 h GC of stale init rows), `documents (expires_at) WHERE expires_at IS NOT NULL AND deleted_at IS NULL` (daily anon-expiry cron), `documents (owner_user_id, updated_at DESC) WHERE owner_user_id IS NOT NULL AND deleted_at IS NULL` (`list --mine`), `sessions (user_id, last_seen_at DESC) WHERE revoked_at IS NULL` (`sessions list` in v2), `exchange_codes (expires_at)` (60 s GC).
- **All timestamps `TIMESTAMPTZ`** (never `TIMESTAMP`). UTC at the database boundary, conversion is app-side.

### Apex Routing
The apex host `mdfly.dev` is **Worker-routed** at the edge: a Cloudflare Worker dispatches each incoming request between two origins based on the request path. **Cloudflare Pages** (`<project>.pages.dev`) serves landing + legal at the exact-match static paths `/`, `/about`, `/pricing`, `/terms`, `/privacy`, `/contact`, `/abuse`, `/robots.txt`, plus the prefix `/_landing/*` for the landing site's hashed static assets. The **Go backend** (`<app>.ondigitalocean.app` via DO App Platform) serves everything else: `/<slug>[/<logical-path>]` (Document SSR per ADR-0018), `/llm/<slug>[/<logical-path>.md]` (LLM twin), `/cli/login` (OAuth chooser), `/healthz`. The Worker's static-route table is code-generated from `internal/slug/reserved.go` so the landing path namespace and the [[Reserved Words]] blocklist cannot drift. Worker runs on Cloudflare's free tier (100k req/day at v1 scale, well under limit). Other hosts in the [[Edge]] family are **not** Worker-routed: `api.mdfly.dev/v1/*` flows directly to the backend (no Worker), `cdn.mdfly.dev/*` flows directly to R2, `www.mdfly.dev` 301-redirects to apex via Bulk Redirect, and `app.mdfly.dev` is reserved DNS for the v2 dashboard (unprovisioned in v1 — returns NXDOMAIN). Marketing copy and backend code can deploy independently — `apps/landing/` builds to Pages on git push without touching the backend; backend redeploys for code changes without rebuilding landing. See ADR-0023.

### Edge
Cloudflare sits in front of every user-facing URL mdfly serves. `mdfly.dev` is registered at Cloudflare Registrar with Cloudflare as authoritative DNS. Five hosts under the zone, each with its own origin and routing model:

- **`mdfly.dev` (apex)** — Worker-routed per [[Apex Routing]] / ADR-0023, dispatching between Cloudflare Pages (landing + legal at fixed paths) and the Go backend (Document SSR, LLM twin, `/cli/login`, `/healthz`). The apex DNS record is a CNAME-flattened entry pointing at the Worker's bound route.
- **`api.mdfly.dev`** — direct to backend, no Worker. CNAME-flattened to DO App Platform hostname.
- **`cdn.mdfly.dev`** — attached to the R2 bucket via Cloudflare's R2 custom-domain connector (not S3-compat presigned URLs); public GETs hit edge cache and never reach the backend.
- **`www.mdfly.dev`** — 301-redirects to apex via Cloudflare Bulk Redirect (no Worker).
- **`app.mdfly.dev`** — reserved for the v2 React dashboard; DNS unprovisioned in v1 (NXDOMAIN).

TLS terminates twice on backend-bound hosts: Universal SSL at the edge (browser-trusted), Cloudflare Origin CA cert at DO origin (Cloudflare-trusted only), under SSL/TLS mode **Full (strict)** — min TLS 1.2, HSTS deferred until v1 stable. See ADR-0021 + ADR-0023.

**Cache layer.** Edge cache rules are per-host post-ADR-0023. **`mdfly.dev` (apex)**: slug HTML (`/<slug>[/<path>]`) and LLM twin (`/llm/<slug>[/<path>.md]`) cache at `s-maxage=86400` per ADR-0018 — these flow through the Worker to the backend; Worker→Pages routes (landing/legal) inherit Pages' default cache (static assets versioned by content hash, HTML cached short with purge-on-deploy). **`api.mdfly.dev`**: blanket cache-bypass — no API response is ever edge-cached. **`cdn.mdfly.dev`**: forever-cache (`max-age=31536000, immutable`, set at R2 PUT). Tier-Cache is on for cacheable hosts. Active invalidation: every `commit` (publish, update) and every `delete` fires a Cloudflare Purge API call for four URL patterns — `mdfly.dev/<slug>`, `mdfly.dev/<slug>/*`, `mdfly.dev/llm/<slug>`, `mdfly.dev/llm/<slug>/*` — in a background goroutine *after* the row flip succeeds, with 3x backoff retry. Purge failure does **not** roll back the commit (Cloudflare API flakiness must not lose user writes); worst case is stale viewer up to the 24h `s-maxage` with manual dashboard purge as the escape hatch. The Cloudflare API token is stored in backend env vars.

**Defensive layer.** Bot Fight Mode on, Security Level Medium, Managed Challenge for borderline traffic, free WAF Managed Rules on, edge rate-limit rule of 10 req/min per IP on `api.mdfly.dev/v1/publish/*` (atop the backend's own crude per-IP limit from ADR-0017), Always-Use-HTTPS on, HTTP/3 + 0-RTT on. Known false-positive risk: Bot Fight Mode may block legitimate `curl` / agent traffic against `/llm/*`; v1 ships defaults, carve-out is a one-rule fix if telemetry warrants.

**Email DNS.** No transactional email in v1, so the domain advertises null-MX (`mdfly.dev. MX 0 "."`, RFC 7505) + `SPF v=spf1 -all` + `DMARC v=DMARC1; p=reject; rua=mailto:abir@shipday.com` — receivers reject any forgery of `@mdfly.dev`. Reversed in v2 when transactional email is added.

**Robots / indexing.** `/robots.txt` serves blanket `Disallow: /`; every slug response carries `X-Robots-Tag: noindex, nofollow`. See [[Document]] visibility note.

### Repo Layout
Single Go monorepo at `github.com/mdfly/mdfly`, single `go.mod`, two Go binaries (`cmd/mdfly` CLI + `cmd/mdfly-server` backend), shared Go code in compiler-private `internal/` packages. Reserves `apps/` for future non-Go deliverables (`apps/landing/` v1.5 marketing, `apps/dashboard/` v2 React SPA). Root-level: `migrations/` (golang-migrate SQL), `deploy/` (Cloudflare + DO + R2 IaC), `scripts/`, `docs/adr/`, `testdata/`, `Dockerfile` (server), `.goreleaser.yaml` (CLI), `Makefile`. Three-bucket internal layout — flat `internal/` for shared packages (`slug`, `manifest`, `markdown`, `api` — both binaries import); `internal/server/` for server-only, split into runtime wiring + infra + domain (`app.go`/`config.go`/`routes.go` assemble the server; `db`, `storage`, `manifest`, `httpx`, `handlers`, `middleware` are infra/glue; domain logic lives under `internal/server/service/<domain>/` — `publish` today, `auth` / `llm` / `purge` / `ssr` added as each ships); `internal/cli/` for CLI-only (`cmd`, `publish`, `localstate`). SSR templates `//go:embed`'d in `internal/server/ssr/` when that domain lands. The wire protocol's volatility at v1 (3-phase publish, idempotency-key, optimistic-concurrency) makes atomic cross-binary PRs valuable; polyrepo would force every wire change through a 3-PR shared-module-tag dance with real version-skew hazard. See ADR-0022.

### Shared-PC Hygiene
The trust boundary for the mdfly CLI is the **OS user account**, not the machine. The CLI persists secrets as plain-text mode-`0600` files under `~/.mdfly/` (`credentials` holds the `mfst_` session token + per-slug `mftk_` Edit Tokens; `state.json` holds the [[Local State]] mapping + update-check cache; `config.toml` is opt-in user-curated). Anything able to read your home directory — another process running under your account, a backup snapshot, a cloud-sync daemon — is treated as you. This matches the floor set by `gh`, `gcloud`, `claude`, `cargo`, and `git`; OS-keychain encryption was rejected for v1 (per ADR-0020) because the cost of three platform code paths plus a keychain-unlock UX surface that breaks `--device` headless flow does not pay for a marginal improvement over mode-`0600` against the threats we have.

**Cloud-sync footgun.** If your home directory is synced to Dropbox / iCloud / OneDrive / etc., `~/.mdfly/credentials` ships your session token and Edit Tokens to the cloud provider. Mitigation: set `MDFLY_CONFIG_DIR=<somewhere-not-synced>` (e.g. `~/.local/share/mdfly/`) before running `mdfly login`. The env var overrides the entire CLI state directory; everything below it relocates atomically. Documented in `mdfly login --help`.

**Leaving a shared machine.** `mdfly logout` by default wipes only the server session reference from `credentials` — slug list and Edit Tokens stay on disk because they are bound to anon Documents, not to your account (logging out of GitHub shouldn't disconnect you from your own anon shares). For the shared-PC case, `mdfly logout --wipe-local` additionally deletes `credentials` and `state.json` (leaves `config.toml` alone — it is user-curated and contains no secrets). `--wipe-local` works regardless of whether a session is present (the whole point is scrubbing whatever the previous user left). Composes with `--all`: `mdfly logout --all --wipe-local` is the full "I'm done with this machine forever" exit — every server-side session for this account is revoked AND every local trace is scrubbed. Exit `0` either way.

**Compromised-credentials response.** Suspect a leak? `mdfly logout --all` from a trusted machine revokes every session. Edit Tokens cannot be rotated in v1 (would need a `mdfly rotate-token` verb, deferred to v2); the durable response for anon Documents you care about is to `mdfly claim <slug>` from a logged-in trusted machine, after which the Edit Token is NULLed (per [[Claim]]) and session auth fully supersedes.
