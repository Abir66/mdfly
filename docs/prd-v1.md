# PRD: mdfly v1 vertical slice — Anonymous Publish + View

**Status:** ready-for-agent
**Scope:** vertical tracer-bullet slice of the v1 milestone (ADR-0017). One verb (`mdfly publish`), one read path (`mdfly.dev/<slug>`). Login, claim, update, delete, and the LLM twin are explicitly deferred to follow-up PRDs.

---

## Problem Statement

A developer has a markdown file on disk and wants to share it with someone (a teammate, a friend, a recruiter) by sending a URL. Today their options are: paste into a GitHub Gist (loses linked images unless they're hosted elsewhere; requires a GitHub account on both ends), paste into a Notion page (rewrites the markdown, no real CLI flow), or zip-and-email (defeats the URL-share use case entirely). None of these honors the "the markdown file on disk is the source of truth, the URL is just a view of it" mental model that the developer already has for source code (git push → URL).

The developer also doesn't want to think about hosting. They don't want to spin up a static site, configure a domain, set up object storage for the linked PNG, or wire a CDN. They want `<command> file.md` → `<url>`. The file's linked assets (relative-path images, a sibling `.md`) must come along automatically — failure to handle relative links is what makes Gist a worse-than-paper option for any markdown more interesting than a single-file note.

## Solution

A single CLI verb — `mdfly publish hello.md` — uploads the markdown and every reachable relative asset to mdfly's backend and prints a shareable URL on stdout in under three seconds for a typical bundle. The URL renders the markdown as a fully server-rendered HTML page on `mdfly.dev/<slug>` (an 8-character random base62 slug). Linked images load from `cdn.mdfly.dev` automatically; sibling `.md` files become clickable cross-document links within the same slug.

For this PRD the only identity is "anonymous" — no login, no account, no `claim`. A successful publish writes a per-slug Edit Token to `~/.mdfly/credentials` (stored for use by later PRDs' `update`/`delete`/`claim` flows; not consumed by any verb in this slice). Anonymous Documents expire 30 days after publish (cron sweep, out of scope for this PRD's verbs but the column is set at commit time so the expiry infrastructure has data to act on later).

## User Stories

1. As a developer with a markdown file on my laptop, I want to run `mdfly publish notes.md` and get back a URL on stdout, so that I can paste the URL into Slack without leaving my terminal.
2. As a developer publishing a bare-filename invocation, I want `mdfly notes.md` (no verb) to dispatch to `publish` when the file has never been published before, so that I don't have to remember the verb in the common case.
3. As a developer with a markdown file that references `./diagram.png`, I want the image to upload alongside the markdown and render correctly at the public URL, so that I don't have to host images separately or rewrite paths.
4. As a developer with a markdown file that references `./appendix.md`, I want the appendix to be reachable at `mdfly.dev/<slug>/appendix` and the link in the rendered HTML to point there, so that multi-file documents work the same way they work on disk.
5. As a developer publishing the same file twice, I want each `mdfly publish` invocation to mint a fresh distinct slug, so that `publish` is unambiguously "create a new thing" (matches the `[[Publish]]` glossary term).
6. As a developer whose network blipped mid-upload, I want a retried `mdfly publish` to converge to a single document at a single URL (not two duplicate slugs), so that flaky networks don't cost me cleanup work.
7. As a developer publishing a tiny single-file note, I want the publish to complete in roughly one HTTP round trip rather than three, so that small ergonomic uses (`mdfly publish todo.md`) feel instant.
8. As a developer who hits Ctrl-C halfway through `mdfly publish`, I want the half-uploaded state on the server to be cleaned up automatically within an hour, so that retries don't pile up phantom pending rows.
9. As a developer publishing a bundle that exceeds 10 MB total or 50 files, I want the CLI to refuse before any network call with a clear error citing the Anonymous tier limit, so that I'm not surprised after a long upload.
10. As a developer publishing a markdown file containing raw HTML (`<script>alert(1)</script>`), I want the rendered page to strip the HTML entirely, so that I can't accidentally publish (and viewers can't be attacked by) XSS payloads.
11. As a developer publishing a markdown file with a YAML frontmatter block containing `title:`, `description:`, and `image:` keys, I want those values to drive the page's `<title>`, `<meta name="description">`, and OpenGraph `og:image`, so that the URL unfurls nicely when pasted into Slack/Discord/Twitter.
12. As a developer who didn't set frontmatter, I want the title to fall back to the first H1, the description to fall back to the first paragraph, and `og:image` to fall back to the first referenced image asset, so that link unfurls are reasonable by default.
13. As a viewer clicking a `mdfly.dev/<slug>` URL on a phone, I want the page to render in mobile-friendly width with readable typography, so that the share doesn't look broken when the recipient opens it on the device they actually have.
14. As a viewer of a Document containing a fenced code block tagged `go`, I want the block to be syntax-highlighted, so that code shares are legible.
15. As a viewer of a Document containing a Mermaid block, I want the diagram to render in the page (after a small JS hydration), so that diagram-driven docs work.
16. As a viewer of a Document containing inline `$E=mc^2$` or block `$$...$$` KaTeX math, I want the math to render, so that engineering/research notes work.
17. As a viewer hitting a slug that was never created, I want a `404 Not Found` response, so that mistyped URLs fail loudly.
18. As a viewer hitting a slug whose `mdfly.dev/<slug>/<missing-path>` does not exist in the bundle, I want a `404 Not Found` response, so that broken intra-bundle links fail the same way as bad slugs.
19. As a developer publishing a file, I want my local `~/.mdfly/state.json` to record `{<absolute-path>: [<slug>]}` so that future PRDs' `update`/`remove` verbs can resolve the file→slug mapping without re-asking the server.
20. As a developer publishing a file, I want a per-slug Edit Token written to `~/.mdfly/credentials` (mode 0600) at publish time, so that future PRDs' `update`/`delete`/`claim` can prove ownership of this anonymous Document.
21. As a developer running on a corporate workstation with `MDFLY_API` pointing at a staging environment, I want the CLI to honor the env var, so that I can test against staging without hardcoded prod URLs.
22. As a developer publishing in a CI script, I want progress narration to go to stderr and the canonical URL to go to stdout, so that `URL=$(mdfly publish docs/) | pbcopy` works without contamination.
23. As a developer running `mdfly publish --json hello.md`, I want stdout to be a single JSON object `{url, slug, manifest_hash, tier, expires_at}`, so that scripts can parse the result reliably.
24. As a developer running `mdfly publish` against a flaky backend, I want automatic retries with exponential backoff on 5xx/timeout (3 attempts) and the same idempotency key reused across attempts, so that transient failures don't surface as errors.
25. As an operator of mdfly, I want every blob in R2 to be verified server-side via HEAD before the row flips to `published`, so that a CLI claiming "I uploaded the bytes" cannot lie and publish a phantom document.
26. As an operator, I want presigned PUT URLs to pin `Content-Length` and `x-amz-checksum-sha256` so that R2 itself rejects mismatched bytes, so that I don't pay storage for junk a malicious CLI tried to inject.
27. As an operator, I want a Postgres row stuck in `status='pending'` for over an hour to be GC'd by a cron, so that abandoned publishes don't accumulate.
28. As an operator, I want every published-slug response to carry `Cache-Control: public, max-age=300, s-maxage=86400`, so that a popular Document is rendered once per Update and served from CDN thereafter.
29. As an operator, I want every published-slug response to carry `X-Robots-Tag: noindex, nofollow` and `/robots.txt` to serve `Disallow: /`, so that Documents do not leak into Google despite being technically reachable.

## Implementation Decisions

### Modules

The slice introduces the following packages. Italicized are deep modules (lots of logic, narrow interface, tested in isolation). Non-italicized are thin glue or wire types.

**Server-and-CLI shared (`internal/`):**

- ***`internal/manifest`*** — Bundle Manifest construction and resolution. Walks a root `.md` from disk, parses markdown for relative-path references (images via `![](...)`, sibling `.md` via `[](./x.md)`), follows them transitively into a Bundle, computes SHA256 + size per file, and returns a `Manifest{Root, Files[]}` struct. Also exposes `Resolve(manifest, slug, logicalPath) → {blobHash, contentType}` and `RewriteRefs(md []byte, manifest, slug, mode {Human|LLM}) → []byte` used by the SSR module. Pure: filesystem-in, struct-out; no network. The deepest module in the slice — most v1 ADR semantics (content-hash dedup, byte-identical storage, reachable-paths-not-sandbox) live here.
- ***`internal/markdown`*** — Goldmark + chroma + bluemonday wrapper. `Render(md []byte) → (html []byte, meta {Title, Excerpt, OGImagePath})` in one pass. Raw HTML is stripped (not rendered) per ADR. Frontmatter is parsed and its values, when present, override the inferred meta. Mermaid and KaTeX nodes are emitted as `<pre data-mermaid>` / math placeholders for client-side JS hydration; rendering itself is server-side and pure.
- ***`internal/slug`*** — Reserved-words constant slice + `Generate() → string` (8-char base62) + `Validate(s) → error`. The slug-collision retry loop lives here, not in `db`, because reserved-words check + retry-on-collision is one concern with one home.
- `internal/api` — Wire types only. Request/response structs for the three endpoints. Single source of truth for both binaries; future TypeScript codegen target.

**Server-only (`internal/server/`):**

- ***`internal/server/db`*** — Documents-row CRUD (`InsertPending`, `Publish`, `GetBySlug`, `GetByIdempotencyKey`) on a `db.Client` over pgxpool. All SQL inline; no ORM.
- ***`internal/server/storage`*** — R2 object storage on a `storage.Client`: presigned-PUT minting with `Content-Length` + `x-amz-checksum-sha256` pinned into the V4 signature, `HEAD` verification, raw blob fetch for SSR. Wraps AWS SDK v2 with mdfly-specific helpers. Shares no interface with `db` — promoted to its own top-level package.
- ***`internal/server/ssr`*** — Embeds the HTML template via `//go:embed`, composes the resolved markdown render + Preview Metadata + CDN asset URLs into the final response. Single function: `Render(doc, body) → http.Response`. The mobile-responsive CSS is a single inlined stylesheet (no Tailwind build pipeline in v1).
- `internal/server/handlers` — Thin HTTP handlers wiring requests through `slug` + `manifest` + `markdown` + `store` + `ssr`. Validation lives at the handler boundary; business logic does not.

**CLI-only (`internal/cli/`):**

- ***`internal/cli/publish`*** — The 3-phase orchestrator. `Publish(rootPath, opts) → (URL, error)`. Handles: bundle walk (delegates to `manifest`), init request with manifest + fresh UUIDv4 idempotency-key, parallel R2 PUTs of `missing_hashes`, commit, retry-with-backoff on 5xx/timeout. Also handles the 256-KB / 5-file inline fast-path: when the bundle is small enough, init's response `inline_accept: true` triggers a one-shot commit with `inline_blobs[]` populated, skipping the R2 round trip.
- `internal/cli/localstate` — Reads/writes `~/.mdfly/state.json` (Local State per [[Local State]]) and `~/.mdfly/credentials` (Edit Token map). Mode 0600 on write. Honors `MDFLY_CONFIG_DIR`.
- `internal/cli/cmd` — Cobra command tree. Only `publish` is wired in this PRD's slice; other verbs return "not yet implemented" with exit code 1 (to be replaced by their PRDs).

### Wire protocol — endpoints in this slice

Per ADR-0011 + ADR-0013, three endpoints. Hosted on the backend; final apex / API subdomain routing is out of scope (use direct DO App Platform hostname or local dev host until the edge PRD lands).

- `POST /v1/publish/init` — Body `{idempotency_key, manifest:{root, files:[{path, hash, size}]}, edit_token}`. No `requested_slug` in this slice (no login, no owned tier). Returns `{slug, missing_hashes:[hash...], presigned_urls:{hash:url}, inline_accept:bool}`. Server-side: `INSERT ... ON CONFLICT (idempotency_key) DO NOTHING`; collision-retries slug generation against `documents.slug UNIQUE`; rejects reserved-word slugs (none happen on random slugs in practice, but the check is the same code path); enforces Bundle Limits (10 MB / 50 files / 25 MB single-file for anon).
- `POST /v1/publish/commit` — Body `{idempotency_key, inline_blobs?:[{hash, bytes}]}`. Identifies row by `WHERE idempotency_key = $1`. HEAD-verifies all blobs (or, on inline fast-path, server-PUTs the inline bytes to R2 first), runs the markdown render once to compute `title` / `excerpt` / `og_image_hash`, atomically flips `status='pending' → 'published'`, sets `expires_at = now() + interval '30 days'`. Returns `{url, slug, manifest_hash}`.
- `GET /<slug>` and `GET /<slug>/<logical-path>` — The SSR view path. Looks up document by slug, fetches the requested markdown blob from R2, runs it through `markdown.Render` with reference rewriting, returns `text/html` with caching headers and robots headers.

Out of this PRD's wire scope: `documents/:slug` GET (used by future dashboard, not by viewer), DELETE, `claim`, all auth endpoints, the `/llm/<slug>` twin.

### Schema — columns touched in this slice

Full DDL is in ADR-0019. This slice creates the `documents` table with all its columns (everything is wired now; `edit_token_hash` is set by anonymous publish; `owner_user_id` stays `NULL`). The `users`, `identities`, `sessions`, `exchange_codes` tables are deferred to the login PRD — the slice's migration adds only `documents` and its indexes.

### URL routing scheme in this slice

- `GET /<slug>` → root markdown of bundle
- `GET /<slug>/<logical-path>` → nested markdown (logical-path is the bundle-relative path minus `.md`; e.g. bundle file `appendix.md` is served at `/<slug>/appendix`)
- `GET /<slug>/<logical-path>.md` is **not** served in this slice (that's the LLM twin's territory, deferred)
- `GET /robots.txt` → `User-agent: *\nDisallow: /\n`
- `GET /healthz` → `200 ok`
- Path `<slug>` that is a Reserved Word → handler does not match; chi router falls through to 404
- Path `<slug>` matching the slug pattern but not in DB (or soft-deleted) → 404 (this PRD does not yet need 410 since `delete` is deferred)

### CLI surface in this slice

```
mdfly publish <file>          # produces URL on stdout
mdfly <file>                  # bare-file shortcut: dispatches to publish if file not in Local State
                              # (if file IS in Local State, exits 2 — update is deferred)
mdfly --version
mdfly --help
```

Global flags wired now: `--json`, `--plain`, `-v`/`--verbose`, `-q`/`--quiet`, `--api`, `--help`, `--version`. `--no-update-check` is wired but the update check itself is no-op in this slice. Verb-local `--slug` is **not** wired (anon-only; user-chosen slugs require login).

### What the bare-file dispatch does in this slice

`mdfly <file>` (no verb) checks Local State for the file's absolute path:
- 0 slugs mapped → run `publish`, print `→ publish` to stderr first
- 1+ slugs mapped → exit code 2 with stderr `<file> is already published. mdfly update is not yet available in this build.` (update lands in a follow-up PRD; we don't want to silently re-publish)

### Error envelope

Per `[[API Surface]]`, every `4xx`/`5xx` carries `{"error":{"code":"<enum>","message":"<text>","details":?}}`. CLI surfaces backend `code` to the matching CLI exit code from the table in `[[CLI]]`. This slice exits: `0` success, `2` bad input / file too big, `5` quota / 413, `6` network / 5xx.

### Deployment shape

Backend is a single Go binary built from `cmd/mdfly-server` and deployed to DO App Platform per ADR-0007. Postgres is DO Managed Postgres. R2 is a single bucket; `cdn.mdfly.dev` custom-domain wiring is deferred to the edge PRD — assets in this slice are served via a stable R2 dev URL referenced from rendered HTML. Final `mdfly.dev` apex routing (Cloudflare Worker per ADR-0023) is deferred; the slice runs against the raw DO hostname (or local dev) until the edge PRD lands. `goreleaser` for the CLI binary is wired now (homebrew tap can come later; binary download from GH Releases is enough for this slice).

## Testing Decisions

### Principles

- **Test external behavior, not implementation.** A test that asserts "the `manifest.Build` function returned a `Manifest` struct with N entries" is fine; a test that mocks the chroma highlighter to verify it was called is not.
- **Round-trip the wire.** The most important tests in this slice are end-to-end: `publish <fixture.md> against in-process backend → GET <URL> → diff HTML against golden file`. If that test passes, the whole slice works.
- **Goldens, not assertions.** For markdown rendering and bundle resolution, store expected outputs as files in `testdata/` and diff on test. Goldens caught fewer bugs than assertions in unit-test-heavy codebases but catch more in render-pipeline codebases like this one.
- **No mocks for Postgres or R2.** Integration tests boot real Postgres (via testcontainers) and use a minio container as R2-compatible storage. The cost of slower tests is paid back in fewer "tests pass but prod doesn't" failures — the kind of bug that the design (ADR-0011's checksum pinning, ADR-0019's partial indexes) is specifically defending against. Mocks here would hide exactly the integration cracks worth testing.

### Modules to test in isolation

- ***`internal/manifest`*** — golden tests over `testdata/bundles/*/`: a directory of input fixtures (root `.md` + linked files + an `expected-manifest.json`). Cover: single-file bundle, bundle with one image, bundle with sibling `.md`, bundle with `.md` referencing `.md` referencing `.md` (transitive walk), bundle with an absolute URL (must NOT be walked or downloaded), bundle with a missing reference (must fail with a specific error), bundle that exceeds Bundle Limits.
- ***`internal/markdown`*** — golden HTML tests over `testdata/markdown/*.md → *.html`. Cover: GFM tables, task lists, strikethrough, autolinks, syntax-highlighted code blocks, frontmatter overrides title/description/image, frontmatter absent → fallback inference, raw `<script>` stripped, raw `<img onerror=...>` stripped, Mermaid block emits placeholder, KaTeX inline + block emit placeholders.
- ***`internal/slug`*** — unit tests for generator entropy (statistical: N=10k generations, no duplicates, all chars in base62 set), `Validate` accepts the format, `Validate` rejects every reserved word, `Validate` rejects each illegal character class.
- ***`internal/server/db`*** — integration tests against testcontainer Postgres + migrations applied. Cover: `InsertPending` idempotency under `ON CONFLICT (idempotency_key) DO NOTHING` with the same key (twice) returns the same row; `Publish` is a no-op idempotency when the row is already `published`; partial-index `documents_pending_gc_idx` exists and is used by an `EXPLAIN ANALYZE` of the GC query.
- ***`internal/server/storage`*** — integration tests against minio. Cover: presigned PUT with correct `Content-Length` + `x-amz-checksum-sha256` succeeds; presigned PUT with wrong checksum is rejected by minio; HEAD-verify on a present blob succeeds; HEAD-verify on a missing blob returns the expected `BlobMissing` error.
- ***`internal/cli/publish`*** — table-driven tests against an in-process `httptest.Server` running the real handlers + real Postgres + real minio. Cover: small bundle takes inline fast-path (single commit, no PUT); larger bundle takes 3-phase; commit retry after simulated network drop on the first commit returns the same URL; init retry with the same idempotency key returns the same slug; init retry with a different manifest under the same idempotency key returns 422.

### End-to-end test

One blackbox test that:
1. Boots Postgres + minio + the server binary on a free port
2. Runs the CLI binary via `exec.Cmd` with `MDFLY_API` pointing at the test server
3. `mdfly publish testdata/e2e/hello.md` → captures stdout (URL)
4. `curl <URL>` → diffs against `testdata/e2e/hello.expected.html`
5. `curl <URL>/appendix` → diffs against `testdata/e2e/appendix.expected.html`
6. Asserts `~/.mdfly/state.json` and `~/.mdfly/credentials` (under a temp HOME) have the expected entries

Runs in CI on every PR. Slow (Docker boot), but it is the single test that proves the wire protocol works end-to-end.

### Prior art

There is no prior art in this codebase — the project is pre-implementation. The pattern to follow is "[testcontainers-go](https://golang.testcontainers.org/) for Postgres and minio, `httptest` for in-process HTTP, `os.Exec` for the CLI binary against a separate-process server in the e2e test." This is the pattern used by `kubernetes/test-infra`, `argo-workflows`, and `golang-migrate` itself.

## Out of Scope

Explicitly deferred to follow-up PRDs:

- **All auth.** `mdfly login` (loopback + device flow per ADR-0016), `mdfly logout`, `mdfly whoami`, GitHub OAuth machinery, `sessions` / `identities` / `exchange_codes` tables, `Authorization: Bearer` middleware on state-changing endpoints.
- **Owned tier.** `--slug <name>` (user-chosen slugs), Owned-tier Bundle Limits, owned-tier endpoint shape variants.
- **`update`, `delete`, `claim`.** All three require the Edit Token verification path (or session auth for owned) that this slice intentionally does not wire any endpoints for. The Edit Token *is* generated and persisted by this slice's publish — it just isn't consumed by any verb yet.
- **`remove`, `list`.** Pure local-state verbs that this slice's CLI doesn't need to demonstrate the publish→view loop.
- **The `/llm/<slug>` LLM twin.** Per ADR-0018, same backend resolution as human routes minus the HTML wrap. Deferred to its own PRD; not needed to validate human view.
- **Final edge routing.** Cloudflare Worker at apex per ADR-0023, `api.mdfly.dev` subdomain, `cdn.mdfly.dev` custom-domain wiring, `www.mdfly.dev` redirect, `mdfly.dev/cli/login`, WAF/Bot Fight, rate-limit at the edge, Cloudflare Purge on commit, robots.txt at edge — all deferred to an edge PRD. The slice runs against raw DO + raw R2 dev URL until then.
- **Anonymous Document expiry cron.** The column `expires_at` is populated by this slice's commit. The cron that scans `documents_anon_expiry_idx` and soft-deletes expired rows is deferred — at v1 launch there will be no rows older than 30 days anyway.
- **Pending-row GC cron.** Same reasoning: column-population is here, cron loop is later.
- **Per-IP rate limit on `/v1/publish/init`.** Per ADR-0017 a crude token-bucket lives in v1; deferred to the abuse-protection PRD (it composes orthogonally with this slice's endpoints).
- **`mdfly preview`** — explicitly deferred to v2 per ADR-0017.
- **`apps/landing/`, `apps/dashboard/`, abuse page** — not part of the publish/view tracer.

## Further Notes

- This is a **tracer-bullet PRD** — the goal is the smallest end-to-end thing that proves the architecture (3-phase wire, content-hash storage, server-side manifest, SSR render, mobile-responsive viewer) is sound and shippable. Every deferred feature is additive on top of this slice without requiring a redesign — that property is what we're paying the tracer cost to verify.
- The slice's deepest risk is the `internal/manifest` reference-rewriting story: the same code resolves logical paths at publish-walk time (filesystem-relative) and at view-render time (URL-relative against the slug). Tests should prove that a single source of paths makes both pass.
- After this PRD ships, the natural next PRDs in order are: (1) login + claim (anonymous-to-owned conversion is the differentiating product loop per ADR-0017), (2) update + delete (closes the editable-document story), (3) LLM twin (cheap given SSR pipeline already in place), (4) edge / Cloudflare wiring (production hardening), (5) abuse / rate-limit / takedown page.
