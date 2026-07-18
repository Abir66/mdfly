# PRD: CLI Anon-Tier Overhaul (MVP)

**Status:** ready-for-agent
**Scope:** anonymous-tier CLI only — no `login`/`logout`/`whoami`/`claim`/session work.
**Domain:** see [CONTEXT.md](CONTEXT.md). Governed by ADR-0011 (wire protocol), ADR-0012 (optimistic concurrency), ADR-0013 (idempotency), ADR-0014 (publish/update verb separation, amended), ADR-0015 (Edit Token), ADR-0025 (any-file Root), ADR-0026 (slug-keyed Local State), ADR-0027 (separate stateless update wire).

---

## Problem Statement

The `mdfly` CLI barely works. Only `mdfly publish <file>` runs, and even that is thin: it uploads a Bundle's blobs **serially** (a 20-image doc is 20 sequential round-trips), never persists the Edit Token or any [[Local State]], so nothing downstream — `update`, `delete`, `list` — has anything to read. There is no way to publish text typed on the command line (only files on disk). Error handling collapses every failure into exit code 1 or 2, throwing away the server's categorical error envelope, and leaks machine noise (`status 409 (slug_taken): ...`) at the user. There is no cobra command tree, no global-flag policy, no config precedence, no misuse guards (a symlink pointing outside the project root would have its target's bytes read and uploaded). A user cannot reliably track what they published, re-open it, update it, or clean up — the CLI is not shippable as an MVP.

## Solution

A coherent anon-tier CLI built on a cobra command tree with a consistent global-flag and exit-code contract, backed by durable, slug-keyed [[Local State]]. Users can:

- **Publish** any file (markdown, code, or binary — ADR-0025), or **text typed inline** (`-m`, a pipe, or `<` redirect — a [[Text Publish]]).
- **Update**, **delete**, **list**, **remove** (local-only), and **open** their Documents, with the CLI resolving slugs from durable local state that survives file renames (ADR-0026).
- Get **fast** publishes — blob uploads run in a bounded parallel pool instead of serially.
- Get **legible errors** — the server's error envelope maps to categorical exit codes (0–6) with clean, hint-bearing messages; machine detail hides behind `-v`.
- Trust **misuse guards** — the walk refuses to exfiltrate files outside the project root (including via symlinks), enforces size/count caps client-side, and rejects empty or unresolvable input early.

## User Stories

1. As a CLI user, I want `mdfly publish notes.md` to upload my markdown and print a shareable URL, so that I can share a document.
2. As a CLI user, I want repeated `mdfly publish notes.md` to each mint a fresh independent URL, so that I can share distinct copies without clobbering earlier ones.
3. As a CLI user, I want `mdfly publish report.pdf` (or any binary) to publish and return a URL that shows a download page, so that I can share non-markdown files through the same tool (ADR-0025).
4. As a CLI user, I want `mdfly publish main.go` to publish a code file that renders as highlighted source, so that I can share code snippets.
5. As a CLI user, I want `mdfly publish -m "# Quick note"` to publish inline text as markdown, so that I don't have to create a throwaway file.
6. As a CLI user, I want `echo "# Note" | mdfly publish` and `mdfly publish < note.md` to publish piped/redirected content, so that the CLI composes with shell pipelines.
7. As a CLI user, I want inline text with a relative link (e.g. `![](./logo.png)`) to resolve assets against my current working directory, so that Text Publish behaves like a file publish rooted at the cwd.
8. As a CLI user, I want the CLI to auto-name the synthetic Text Publish root `index.md`, and auto-rename it if a real `index.md` collides, so that publishing text never fails on a name clash and never needs a flag for it.
9. As a CLI user, I want images referenced by my markdown to be uploaded automatically, so that my rendered document is complete.
10. As a CLI user, I want `-r`/`--recursive` to also pull in transitively-linked `.md` files as part of the Bundle, so that a multi-page document publishes as one Document.
11. As a CLI user, I want publishing **without** `-r` to include only the Root plus its directly-referenced non-`.md` assets (leaving linked `.md` as plain links), so that a single-file share stays single-file by default.
12. As a CLI user publishing a large image-heavy Bundle, I want blob uploads to run in parallel, so that publishing is fast instead of one-round-trip-at-a-time.
13. As a CLI user, I want each blob upload to retry independently on a transient failure, so that one flaky blob doesn't fail or stall the whole publish.
14. As a CLI user, I want the Edit Token minted at first publish to be saved locally, so that I can later update or delete the Document without re-authenticating.
15. As a CLI user, I want every successful publish recorded in local state (slug, URL, size, file count, timestamps, source), so that `list`/`update`/`delete`/`open` know about it.
16. As a CLI user, I want `mdfly update notes.md` to modify the existing Document in place at the same URL, so that iterating on a draft doesn't spawn new URLs.
17. As a CLI user whose file maps to exactly one slug, I want `mdfly update notes.md` to target that slug automatically, so that the common case needs no flags.
18. As a CLI user whose file maps to multiple slugs, I want an interactive picker (on a TTY) listing slug + URL, so that I choose which Document to update.
19. As a CLI user running in CI (non-TTY) with an ambiguous file, I want the command to error and demand `--slug`, so that a script never silently updates the wrong Document.
20. As a CLI user, I want `mdfly update --slug <s> -m "..."` (or piped stdin) to replace a Document's content with inline text, so that I can update without a source file.
21. As a CLI user who renamed my source file, I want `mdfly update --slug <s>` to still work and re-attach the record's path, so that a rename doesn't orphan my Document (ADR-0026).
22. As a CLI user, I want a concurrency conflict (the slug changed elsewhere since I started) to surface as a clear message telling me to re-run or pass `--force`, so that I don't silently overwrite someone else's change (ADR-0012).
23. As a CLI user, I want `--force` on `update`/`delete` to bypass the optimistic-concurrency check, so that I can deliberately overwrite when I mean to.
24. As a CLI user, I want `mdfly delete <slug>` to remove the Document server-side (410 Gone thereafter) and prune my local record, so that a deleted Document leaves no dangling local entry.
25. As a CLI user, I want `mdfly delete <file>` to resolve the slug from local state (same disambiguation as update), so that I can delete by the file I remember rather than the slug I don't.
26. As a CLI user, I want a delete confirmation prompt on a TTY unless I pass `-y`/`--yes`, so that I don't accidentally destroy a Document.
27. As a CLI user, I want `mdfly list` to print all my tracked Documents (slug, path, tier, updated-at), newest first, from local state with no network call, so that I can see what I've published instantly.
28. As a CLI user, I want `mdfly remove <slug-or-file>` to drop a local tracking entry **without** touching the server, so that I can clean up `list` for files I no longer care about while leaving the live URL alone.
29. As a CLI user, I want `mdfly remove --all` to wipe local tracking (with confirmation), so that I can reset state without deleting Documents server-side.
30. As a CLI user, I want `mdfly open <slug-or-file>` to open the Document's URL in my browser, so that I can view what I published without copy-pasting.
31. As a CLI user, I want a stale local record to self-heal — if the server returns 404/410 on update/delete, the CLI prunes the entry and tells me — so that `list` doesn't accumulate dead slugs.
32. As a CLI user, I want every command to accept the same global flags (`--json`, `-v`, `-q`, `-y`, `--api`, `--no-update-check`), so that the tool behaves consistently.
33. As a scripting user, I want `--json` to swap stdout to a single machine-readable object per verb, so that I can pipe results into other tools.
34. As a scripting user, I want categorical exit codes (0 success, 2 input, 3 auth, 4 conflict, 5 rate/quota, 6 network/server), so that my script can branch on failure type.
35. As a CLI user, I want error messages to read cleanly with an actionable hint (e.g. "slug 'notes' is already taken. Hint: pick another with --slug"), so that I know what to do next.
36. As a CLI user debugging, I want `-v` to reveal the machine error code + HTTP status behind the friendly message, so that I can diagnose or report a problem precisely.
37. As a CLI user, I want the stdout/stderr split respected — the URL (or JSON) on stdout, all progress/prompts/errors on stderr — so that `mdfly publish a.md | pbcopy` copies just the URL.
38. As a CLI user, I want the config directory and API base resolved by precedence (flag > env > `config.toml` > default), so that I can override per-invocation or persistently as needed.
39. As a self-hoster, I want `--api <url>` / `MDFLY_API` to point the CLI at my own backend, so that I can use mdfly against a non-default server.
40. As a security-conscious user, I want the CLI to refuse to upload a file whose symlink target escapes the project root, so that publishing can't exfiltrate arbitrary files like `/etc/passwd` (unlike a naive read-follow).
41. As a CLI user, I want references with absolute paths or `file://` URLs skipped with a warning (not uploaded), so that only in-root relative assets ship.
42. As a CLI user, I want `http`/`https`/`data:` references left untouched (never fetched), so that the CLI has no SSRF surface.
43. As a CLI user, I want the CLI to reject empty content (empty file, empty `-m`, empty pipe) with a clear message, so that I don't publish nothing by accident.
44. As a CLI user, I want a Bundle that exceeds the anon size or file-count cap to fail client-side before any upload, so that I get an immediate, cheap error instead of a wasted round-trip.
45. As a CLI user, I want a non-existent or unreadable file argument to fail fast with exit 2 and the offending path, so that I can fix the typo.
46. As a CLI user, I want `-m` and a positional file argument to be mutually exclusive (a clear error if I pass both), so that the content source is never ambiguous.
47. As a CLI user, I want `--open` on `publish`/`update` to open the resulting URL after success, so that publish-and-view is one command.
48. As a CLI user on an interactive terminal, I want color/spinner narration on stderr, and none when piped or when `NO_COLOR` is set, so that logs stay clean in non-interactive contexts.

## Implementation Decisions

### Command tree & entry point (`cmd`, `main`)
- Adopt **cobra** as a dependency; build the command tree under `internal/cli/cmd` (root + one file per verb: `publish`, `update`, `list`, `delete`, `remove`, `open`).
- `cmd/mdfly/main.go` is thin and is the **only** site that calls `os.Exit`. It builds the root command, runs it, and maps any returned error through the `output` package's exit-code mapper.
- All verbs use cobra `RunE` (never `Run`) so errors bubble to `main` for central mapping. Set `SilenceUsage` + `SilenceErrors` on root — the CLI prints its own errors; cobra only shows usage for actual flag-parse errors.
- The root command owns the **persistent (global) flags** and resolves config precedence once in `PersistentPreRun`, producing a context object passed to each verb.
- **No bare-command shortcut** (`mdfly <file>` with no verb is removed — ADR-0014 amendment). Verbs are always explicit.

### Global flags (all verbs)
`--json`, `-v`/`--verbose`, `-q`/`--quiet`, `-y`/`--yes`, `--api <url>`, `--no-update-check`, `--help`/`-h`, `--version`.
- `-y`/`--yes` is **global** (promoted from per-verb) and distinct from `--force` (which is `update`/`delete`-local and overrides optimistic-concurrency, not prompts).

### Subcommand-local flags
`-r`/`--recursive` and `-m`/`--message <text>` (`publish`/`update`), `--open` (`publish`/`update`), `--force` (`update`/`delete`), `--slug <s>` (`update`/`delete`), `--all` (`remove`).

### `config` module (deep)
- Resolves settings by precedence: **flag > env > `config.toml` > compiled default**.
- Config file is `<config-dir>/config.toml`, holding only `api_base` in v1 (never auto-written; opt-in).
- `MDFLY_CONFIG_DIR` overrides the entire CLI state directory (default `~/.mdfly/`). Env vars: `MDFLY_API`, `NO_COLOR`.
- **Correction from grilling:** config file is `config.toml`, not `config.json` (CONTEXT/existing lock wins).

### `input` module (deep)
- Resolves the content source: positional **file** vs `-m` **text** vs **stdin** (pipe or `<` redirect).
- `-m` and a positional file are mutually exclusive → error if both given.
- Detects piped/redirected stdin by testing `isatty(stdin)`; if neither a file, `-m`, nor a non-TTY stdin is present, error ("nothing to publish").
- Rejects empty content (exit 2).

### `walk` module (deep — refactor of existing `publish` walk)
- Reference walk producing project-root-relative logical keys, resolving the project root as the Root file's directory (file publish) or the **cwd** (Text Publish).
- **`-r` gating:** non-`.md` assets directly referenced by an included file are always uploaded; linked `.md` files are followed transitively **only under `-r`**.
- **Any-file Root (ADR-0025):** only a **markdown** Root is walked for refs; every non-markdown Root (text, code, binary) → **no walk**, single-blob Bundle.
- **Symlink safety:** resolve symlinks (`filepath.EvalSymlinks`) **before** the inside-root boundary check; a symlink whose resolved target escapes the root is skipped with a warning (never read/uploaded).
- **Boundary rules:** targets that escape the root (`../` beyond root, absolute paths, `file://`) are skipped with a warning and left verbatim; `http`/`https`/`data:` references are left untouched and never fetched.
- **Text mode:** synthesize the Root as `index.md`; if the walk finds an existing `index.md` in the cwd (or linked), auto-rename the synthetic root to the first free name the walk does not already contain.
- No `root_kind` signal is emitted on the wire — the server resolves render type from the key's extension + a binary sniff.

### `localstate` module (deep — build the stub)
- Two files under the CLI state directory (dir mode `0700`): `state.json` (mode `0600`) and `cache.json` (mode `0600`); Edit Tokens live in the separate `credentials` file (mode `0600`) per ADR-0015.
- **Slug-keyed records (ADR-0026):** one record per slug holding `slug`, `url`, `manifest_hash`, `tier`, `size`, `file_count`, `created_at`, `updated_at`, `source` (`file`|`text`), `path` (absolute, or null for Text Publish), and a `has_token` marker (the token itself is in `credentials`, not here).
- A **path → slugs secondary index** is derived at load time for `update <file>` / `delete <file>` / `open <file>` resolution.
- **Atomic writes:** write-temp-then-rename; hold a file lock (`flock`) during read-modify-write to prevent concurrent-publish corruption.
- **Corruption recovery:** on parse failure, back up to `state.json.bak`, warn, and continue with an empty ledger (publish still works; `list` is just empty).
- **Write timing:** persist after a successful `commit` (publish/update) and after `delete`; never on failure.
- **Self-heal:** a server 404/410 on update/delete prunes the stale record and informs the user.

### `publish` workflow module (thin — refactor of existing `Run`)
- Widen the entry point from `Run(apiBase, filePath)` to `Run(Options{...})` (API base, Root, Text, Recursive, Force, Yes, JSON, Open, Slug, …) — one options struct, not a growing positional list.
- Workflow: build Bundle → `init` → **parallel blob upload** → `commit` → persist Local State (+ credentials) → optional `--open`.
- **Parallel PUT pool:** bounded worker pool over the presigned-URL upload phase (`errgroup.WithContext` + semaphore, concurrency cap = a constant `6`); `init` and `commit` stay sequential; the reference walk stays sequential.
  - Each blob keeps its own retry (`withRetry`), so a flaky blob retries without blocking the pool.
  - **First-error-wins** aggregation (errgroup semantics): the first upload error cancels the group and maps to its exit code; `-v` may note "N of M blobs failed."
  - Progress under not-`-q` is an "uploaded N/M" counter (upload order is nondeterministic; final URL + exit code must not depend on order).
- Idempotency key reused across all attempts of one publish (ADR-0013), unchanged.
- **Inline-blob fast-path is deferred** (ADR-0011) — the CLI always uploads via presigned PUT and never emits `inline_blobs`.

### `client` module (refactor of existing)
- Parse the server error envelope `{"error":{"code","message","details?}}` into typed errors keyed by the **machine `code` enum** (the enum lives in the shared `internal/api` package so CLI and server can't drift).
- Distinguish transient (network) errors, retryable 5xx, and terminal 4xx.

### `output` module (deep — new) + error taxonomy
- **Central error→exit-code mapping** (mapping on the machine `code` first, falling back to HTTP status class):

  | Source | Exit |
  |---|---|
  | success | 0 |
  | bad flag / missing arg / file not found / no content / client-side bundle too big / 400 bad bundle / 422 idempotency mismatch | 2 |
  | 401 / 403 (bad or missing Edit Token) | 3 |
  | 409 (slug taken, update conflict) | 4 |
  | 413 (server says too big) / 429 (rate limit) | 5 |
  | 5xx / DNS/timeout/connection / PUT-fail-after-retries | 6 |
  | unclassified | 1 |

  Deliberate split: **client-side** "too big" = 2 (input error); **server-side 413** = 5 (quota).
- **429 is not auto-retried** in v1 — it fails with exit 5 (honoring `Retry-After` is deferred).
- **Message rendering:** print the server's human `message` as the main line + a hint for common cases; hide the machine `code`/HTTP status behind `-v`. `--json` emits `{error:{code, message, exit}}`.
- **Output streams:** stdout carries only the canonical artifact (URL, or `--json` object); stderr carries progress/prompts/warnings/errors. Color/spinner only when `isatty(stderr)` and `NO_COLOR` unset.

### Verb services (thin)
- `delete`: resolve slug (local state) → server `DELETE` (Edit Token auth) → prune local record; 404/410 also prunes; TTY confirm unless `-y`.
- `list`: read local state, print all records newest-first; no network.
- `remove`: local-state only, never calls the server; `--all` wipes with confirmation.
- `open`: resolve slug/URL (local state) → open in browser (`open`/`xdg-open`).

## Testing Decisions

**What makes a good test here:** assert **external behavior**, not internals. For `walk`, assert the resulting Bundle's logical keys / RootPath / file set given an on-disk fixture — not the traversal order or internal queue. For `localstate`, assert what a subsequent load observes after a save/prune — not the JSON byte layout. For `output`, assert the exit code and the rendered message/hint given an input error — not the mapping's control flow. Drive I/O modules with real temp dirs and real files (`t.TempDir()`), and network with `httptest.NewServer`, exactly as the existing suite does.

**Modules to test in isolation (confirmed with developer):**

1. **`localstate`** — save→load round-trips; slug-keyed record retrieval; derived path→slugs index resolution (0/1/many slugs per path); atomic write survives; **corrupt `state.json` → backup + empty-ledger recovery**; concurrent-write safety (lock); self-heal prune on simulated 404/410; credentials kept separate from state.
2. **`walk`** — no-asset single file; image asset pulled in; **`-r` on/off gating of linked `.md`**; non-markdown Root (text/code/binary) → no walk / single-file Bundle; **symlink escaping root → skipped, target bytes never read**; absolute/`file://` skipped; `http`/`data:` left untouched; **Text mode cwd anchoring + `index.md` auto-rename on collision**; escape-root boundary (`../`).
3. **`output` / error-mapping** — table-driven: each error kind (typed local error, each server `code`/status) → expected exit code; message + hint rendering; `-v` reveals machine detail; `--json` error shape; client-side-too-big=2 vs server-413=5 split; 429=5 (no retry).
4. **`config` + `input`** — precedence resolution (flag > env > `config.toml` > default); `MDFLY_CONFIG_DIR` relocation; content-source selection across file/`-m`/piped-stdin/`<`; `-m`+file mutual-exclusion error; empty-content rejection; stdin-TTY detection.

**Prior art** (same package, mirror these patterns):
- `internal/cli/publish/walk_test.go` — `t.TempDir()` + `writeFile`/`hashOf` helpers, external assertions on `Bundle.RootPath` / `FilesByPath`. Model for `walk` and `localstate` fs tests.
- `internal/cli/publish/publish_test.go` + `cli_e2e_test.go` — `httptest.NewServer` mocking the 3-phase init/upload/commit flow, including transient-503 retry and missing-asset failure. Model for the parallel-upload workflow and `client` error-envelope tests.
- `internal/cli/publish/limits_test.go`, `retry_internal_test.go` — table-driven pure-logic tests. Model for `output`/`config`/`input`.
- `testdata/bundles/` fixtures (`multi-md`, `with-image`) — extend with `-r`, binary-root, and symlink-escape fixtures.

## Out of Scope

- **All account/session functionality:** `login`, `logout`, `whoami`, `claim`, device flow, OAuth, `MDFLY_TOKEN`/session auth. This PRD is anon-tier only.
- **Owned-tier behavior:** user-chosen `--slug` at publish, owned Bundle Limits, `list --mine` (server-side listing). `list` here is local-state only.
- **Inline-blob fast-path** (ADR-0011) — deferred; the CLI always uploads via presigned PUT.
- **429 auto-retry / `Retry-After` honoring** — deferred; 429 fails with exit 5.
- **Folder publish** (`mdfly publish <folder> --root <dir>`) and above-root `?up=N` key emission — server-side forward-compat exists; the CLI does not produce these in v1.
- **Resume/retry of a partially-failed publish** — deferred (small bundles; server GCs pending rows in 1h).
- **Asset garbage collection** on update (orphaned R2 blobs) — server-side concern, deferred per [[Update]].
- **`mdfly preview`** (local render) — deferred to v2 per ADR-0017.
- **OS-keychain credential storage** — rejected for v1 per ADR-0020; mode-`0600` files stand.
- **Server/backend changes** are out of scope **for `publish`** — the any-file-Root render path (ADR-0025) already exists server-side (`internal/server/service/view`), no wire change required. **`update` and `delete` are the exception:** the server has no overwrite path (`Init` always mints a fresh slug), no mutation token-check, no `parent_manifest_hash`, no DELETE route, and no `gone` status — so those two slices (S33/S34) are full-stack and build the server endpoint + auth + 409/410 alongside the CLI verb. **`update` (S33, ADR-0027)** adds its own stateless `/v1/update/init` + `/v1/update/commit` endpoints (Edit-Token auth, manifest diff, atomic optimistic overwrite) and needs **no migration** — content-addressing stages new blobs in R2 and the manifest hash is the idempotency token, so no schema change, no staging state, no `idempotency_key`. **`delete` (S34)** added the `'deleted'` status via migration `0002`.

## Further Notes

- **ADRs written alongside this PRD:** ADR-0025 (any-file Root), ADR-0026 (slug-keyed Local State), and the ADR-0014 amendment (slug-keyed model + bare-command removal). CONTEXT.md terms updated: [[Publish]], [[Document]], [[Root]], [[Asset]], [[Bundle]], [[Text Publish]] (new, replacing the [[Inline Publish]] stub), [[Local State]], [[CLI]], [[Update]], [[Delete]], [[Remove]], [[API Surface]].
- **Two grilling decisions were overridden by existing locks and are reflected above:** config file is `config.toml` (not `config.json`); `-y`/`--yes` promoted to a global flag.
- The machine error `code` enum must be defined in the **shared** `internal/api` package (only `CodeIdempotencyPayloadMismatch` exists today) so the CLI's exit-code mapping and the server's envelope cannot drift.
- Suggested slicing for `/to-issues`: (1) cobra tree + global flags + config + `main` exit mapping; (2) `output` + error taxonomy; (3) `localstate`; (4) `input`; (5) `walk` refactor (`-r`, any-file, symlink, text mode); (6) `publish` parallel-upload + state persist; (7) `update`; (8) `delete`; (9) `list`; (10) `remove`; (11) `open`. Order roughly by dependency (1–5 are foundational; 6 depends on 3+5; verbs depend on 3).
