# PRD: Viewer Chrome — sidebar file-tree + directory navigation

**Status:** ready-for-agent
**Scope:** the human-facing read surface at `mdfly.dev/<slug>[/<key>]` graduates from a bare single-document SSR page to a full [[Viewer]] chrome — collapsible file-tree sidebar, type-dispatched center, breadcrumb, directory navigation, raw-markdown toggle, mobile drawer. Governed by ADR-0024 (which amends ADR-0018 + ADR-0021 and supersedes S19). The CLI publish/walk path is **unchanged**; this is a backend-render + frontend-asset slice. The `/llm/<slug>` twin is untouched (it is not implemented and is out of scope).

---

## Problem Statement

A viewer who opens a shared `mdfly.dev/<slug>` URL today lands on a bare server-rendered markdown page with no navigation. If the Document is a multi-file Bundle, there is no way to see the other files, no sense of the project's shape, and no way to reach a sibling `.md` except by clicking an inline link the author happened to write. Linked **directories** don't work at all — the view path only resolves markdown files, so a markdown link to a local folder is a dead end. Non-markdown files (images opened directly, PDFs, source files) have no in-product view: they either render as raw CDN bytes with no context or aren't reachable from the page at all. The result feels less like browsing a shared project and more like reading one stranded file.

The author, meanwhile, has a privacy concern: their absolute project path (`/Users/abir/work/secret-project/...`) must never leak to the viewer, even though the viewer needs to see the project's relative folder structure to navigate it. And the whole thing must stay fast and cheap — initial render must not regress, and the backend must not take on per-view load, because the read path is the most-trafficked surface mdfly has.

## Solution

Every Document URL renders a documentation-site-style **Viewer chrome**: a collapsible left **file-tree sidebar** showing the whole Bundle (folders collapse/expand like a GitHub or VS Code tree), a **center** column that shows the addressed node, and a **breadcrumb** of the logical path across the top of the center. Click any file or folder in the sidebar to navigate to it (full-page reload, no SPA). The center dispatches on file type: markdown renders to HTML (with a **Raw** toggle that swaps in the byte-identical source); an image displays inline; a text or code file shows server-side syntax-highlighted source; anything else (PDF, archive, binary) shows a metadata card with a Download link; and a **directory** shows a GitHub-style listing of its folders and files, auto-rendering a `README.md`/`index.md` below it.

Privacy is preserved by construction: the sidebar tree and breadcrumb are built **only** from project-root-relative [[Bundle Manifest]] keys; the absolute `project_root` is never emitted to the client. Speed is preserved by keeping the page a pure SSR function of `(slug, key, manifest_hash)` — fully CDN-cacheable per ADR-0018 — with the chrome's CSS/JS served as content-hashed `/_static/*` assets fetched once and reused across every navigation, and the Raw toggle fetching the source blob directly from `cdn.mdfly.dev` (zero backend involvement). Folder collapse uses native `<details>` (no JS); the only client JS is the mobile drawer, the Raw toggle, and persisting the sidebar's collapsed-rail state.

## User Stories

1. As a viewer of a multi-file Document, I want a sidebar listing every file and folder in the Bundle, so that I can see the project's shape at a glance instead of reading one stranded file.
2. As a viewer, I want folders in the sidebar to collapse and expand like a GitHub/VS Code tree, so that I can focus on the part of the project I care about.
3. As a viewer, I want the sidebar to auto-expand to and highlight the file I'm currently viewing, so that I always know where I am in the project.
4. As a viewer, I want clicking a file in the sidebar to navigate to that file's page, so that I can browse the Bundle without hunting for inline links.
5. As a viewer, I want clicking a folder in the sidebar to open a listing of that folder, so that I can drill into directories like I would on GitHub.
6. As a viewer following a markdown link that points to a local directory, I want to land on a listing of that directory's files and folders, so that directory links are no longer dead ends.
7. As a viewer of a directory listing, I want folders shown first, then files with their sizes, each clickable, so that the listing reads like a familiar file browser.
8. As a viewer of a directory that contains a `README.md` (or `index.md`), I want that file rendered below the listing, so that folders can have a real landing page like GitHub repos do.
9. As a viewer, I want a breadcrumb of the logical path across the top of the content, with each segment clickable, so that I can jump back up to any ancestor folder.
10. As a viewer, I want the breadcrumb to start with a home icon linking to the Document root, so that I can always return to the entry point.
11. As an author, I want my absolute project path never sent to the viewer, so that publishing a Document doesn't leak where it lived on my machine.
12. As a viewer of a markdown page, I want a Raw toggle that swaps the rendered HTML for the source markdown, so that I can read or copy the original text.
13. As a viewer who toggles Raw, I want the source fetched only once and reused on subsequent toggles, so that flipping back and forth is instant and doesn't re-hit the network.
14. As a viewer who never clicks Raw, I want the source never fetched at all, so that the common (rendered) path stays as light as possible.
15. As a viewer opening an image file, I want it displayed inline in the center, so that I can see it in the context of the project rather than as raw bytes.
16. As a viewer opening a text or source-code file, I want it shown syntax-highlighted in the center, so that shared code is legible.
17. As a viewer opening a very large text file, I want a download card instead of a multi-megabyte inline render, so that the page stays responsive.
18. As a viewer opening a file whose extension lies (named `.cpp` but actually binary), I want a download card instead of garbled text, so that the page never renders binary junk.
19. As a viewer opening a PDF, archive, or other non-previewable file, I want a metadata card (name, size) with a Download button, so that I can grab the file without the page trying to render it.
20. As a viewer on a phone, I want the sidebar collapsed behind a menu button with the content full-width, so that the share is readable on the device I actually have.
21. As a viewer on a phone, I want a top bar with the mdfly wordmark and a menu button that opens the file tree as a drawer, so that I can still navigate the project on mobile.
22. As a viewer on desktop, I want to collapse the sidebar to a thin rail to maximize reading width, so that long documents aren't cramped.
23. As a viewer who collapsed the sidebar, I want it to stay collapsed as I navigate between pages, so that I don't have to re-collapse it on every click.
24. As a viewer with JavaScript disabled, I want the file tree, folder collapse, navigation, and content to all still work, so that the core experience doesn't depend on JS.
25. As a viewer, I want navigating between pages to feel fast (sub-100ms when cached), so that browsing a project doesn't feel like loading a new website each click.
26. As a viewer hitting a path that is neither a file nor a directory in the Bundle, I want a 404, so that broken links fail loudly.
27. As a viewer of a Document whose root is a single markdown file, I want the bare `mdfly.dev/<slug>` URL to render that file, so that the common case is unchanged.
28. As an operator, I want the Viewer's CSS and JS served as content-hashed immutable assets fetched once and reused across navigations, so that browsing many pages doesn't re-download the chrome each time.
29. As an operator, I want every Viewer page to remain a pure cacheable function of `(slug, key, manifest_hash)`, so that the CDN absorbs read load and the backend renders once per Update.
30. As an operator, I want directory-listing and asset-chrome pages to carry the same cache and robots headers as markdown pages, so that the new surfaces inherit the existing caching and noindex posture.
31. As an operator, I want the Raw toggle to fetch the source blob from the CDN (not the backend), so that the raw view adds zero backend load.
32. As a future maintainer, I want the backend to already tolerate a Document with an empty root (a folder-Document), so that recursive folder-publishing can ship later without a schema change.
33. As a future maintainer, I want `/_static`, `landing`, and `static` reserved so that the static-asset path and marketing words can't be claimed as slugs.

## Implementation Decisions

Governed by **ADR-0024** (Viewer chrome) which amends **ADR-0018** (SSR view path) and **ADR-0021** (R2 CORS) and supersedes **S19** (inline viewer CSS). Vocabulary per CONTEXT.md [[Viewer]], [[Directory Listing]], [[Root]], [[Bundle Manifest]].

### URL convention (reverses ADR-0018)

- Every node is addressed by its **exact manifest key, extension included**: `/<slug>/docs/api/auth.md`, `/<slug>/assets/logo.png`. The previous strip-`.md` convention (S11) is **reversed** — the cross-md link rewrite and `slugPageURL`/`nestedKey` stop stripping `.md`.
- Resolution order for `/<slug>/<rest>`: (1) `<rest>` is an exact manifest key → serve that file in chrome; (2) `<rest>` is a directory prefix of some key → [[Directory Listing]]; (3) else 404.
- Trailing slashes are optional and 301-canonicalized to one form.
- Bare `/<slug>` renders [[Root]] (`root_path`); when `root_path` is empty, it renders a root Directory Listing.
- `?up=N` (above-root keys) now carries the tail **with extension intact**.

### Modules

Italicized = deep modules (logic-heavy, narrow interface, isolation-testable).

- ***`internal/server/filetree`*** (NEW, pure, no I/O) — the slice's deepest module. Consumes the set of manifest keys (+ the current key) and exposes: `BuildTree(keys, currentKey) → TreeNode` (nested folders/files, current node + ancestors flagged open); `Classify(keys, reqPath) → File{key} | Dir{prefix} | NotFound`; `ListDir(keys, prefix) → []Entry{name, isDir, size}` (folders first, then files, alphabetical); `Breadcrumb(key) → []Crumb`; `IndexFile(keys, prefix) → (key, ok)` (`README.md` then `index.md`, case-insensitive, first match). All inputs are project-root-relative keys; `project_root` never enters this module.
- ***`internal/markdown`*** (extend, pure) — `HighlightFile(filename string, content []byte) → (html template.HTML, err)` runs a standalone file through chroma, selecting the lexer by filename/extension and falling back to plaintext; `IsBinary(content []byte) → bool` reports true on invalid UTF-8 or a NUL byte. No client JS — highlighting is server-side, reusing the existing chroma stylesheet.
- `internal/server/service/view` (heavy extend) — the workflow. `RenderRoot`/`RenderPath` become: load manifest → `filetree.Classify` → dispatch. Dispatch helpers: `renderMarkdown` (existing path + embeds the file's CDN raw URL for the toggle + tree/breadcrumb), `renderImage`, `renderTextPreview` (guard: `f.Size > PreviewMaxBytes` → download card with no fetch; else fetch → `markdown.IsBinary` → download card, else `markdown.HighlightFile`), `renderDownload` (metadata card), `renderDirectory` (`filetree.ListDir` + optional `IndexFile` render). Builds a single view model passed to ssr. `PreviewMaxBytes = 1 << 20` (1 MB), a declared constant.
- `internal/server/ssr` (extend) — `PageData` grows to carry the tree, breadcrumb, center variant + payload, raw-URL, and enrichment flags. The template becomes the chrome: sidebar `<details>`/`<summary>` tree (server emits `open` on current ancestors, `aria-current` on the current node), breadcrumb, center, mobile top bar, Raw toggle, and `<link>`/`<script>` to the `/_static/*` assets plus the inline `<head>` collapse-state snippet.
- `internal/server/static` (NEW) — `go:embed`s the built `app.css` + `app.js`, serves them at the reserved `/_static/<name>.<hash>.<ext>` with `Cache-Control: public, max-age=31536000, immutable`. Exposes the hashed URLs to ssr so the template can reference them.
- `internal/server/manifest` (small) — `FromDTO` tolerates an empty `root_path` (returns no `ErrRootPathNotFound` when `root_path == ""`); only errors when a non-empty `root_path` is absent from files. `RootFile()` already returns `(_, false)` for the empty case.
- `internal/slug/reserved.go` (small) — add `landing` and `static` to the blocklist. (`_static` is outside the slug grammar `[a-z0-9-]`, so it needs no entry — only the Worker route.)

### Frontend assets (`apps/` or embedded source → built into `/_static`)

- `app.css` — three-region-collapsing-to-two layout (sidebar + center; no right column in v1), file-tree styling over native `<details>`, breadcrumb, directory listing, code/`<pre>` + chroma classes, download/metadata card, image centering, mobile breakpoint at **768px** (sidebar → drawer), collapsed-rail styling. Replaces S19's inline `<style>`.
- `app.js` — vanilla, no framework: mobile drawer open/close, Raw toggle (fetch the embedded CDN raw URL once, cache in memory, swap center via a CSS class), and sidebar collapse toggle persisting a single boolean to `localStorage`. An additional tiny inline `<head>` snippet (not in `app.js`) reads that boolean and sets the collapsed class before first paint to avoid flash. Mermaid/KaTeX/copy-button enrichment from the existing boot snippet are preserved.

### Center dispatch table (by node type)

| Node | Center |
|---|---|
| markdown | rendered HTML + Raw toggle (CDN-blob fetch, lazy, once) |
| image (png/jpg/jpeg/gif/webp/svg) | inline `<img src=cdn>` (no server fetch; SVG safe via `<img>`) |
| text/code, `size ≤ 1 MB`, non-binary | server chroma-highlighted `<pre>` |
| text/code, `size > 1 MB` or binary | download card (oversize: no fetch; binary: detected post-fetch) |
| other (pdf/zip/bin/…) | metadata card (name, size) + Download → CDN blob |
| directory | listing (folders first, then files+sizes) + README/index render |

### Edge / IaC

- Worker dispatch table gains `/_static/*` → backend origin (alongside the existing `/_landing/*` → Pages). Per ADR-0023 the table is code-generated from the reserved-words source, so adding the prefix there keeps it from drifting.
- R2 bucket (`cdn.mdfly.dev`) gains a CORS rule allowing `Access-Control-Allow-Origin: https://mdfly.dev` so the Raw toggle's cross-origin `fetch()` of the source blob succeeds (ADR-0021 amendment).

### Caching / headers

Directory-listing and asset-chrome responses carry the same `Cache-Control: public, max-age=300, s-maxage=86400` and `X-Robots-Tag: noindex, nofollow` as markdown pages. The existing `/<slug>/*` purge-on-commit already covers the new paths. `/_static/*` is `immutable`.

## Testing Decisions

### Principles

- **Test external behavior, not implementation.** Assert on the classification result, the listing order, the rendered HTML — not on which internal function was called.
- **Goldens for render output.** Markdown chrome, directory listings, and highlighted files are diffed against `testdata/` golden HTML, consistent with the existing `internal/markdown` and `internal/server/ssr` golden tests.
- **Pure modules get table tests; I/O modules get integration + e2e.** `filetree` and the `markdown` extensions are pure and exhaustively table-tested; the `view` workflow is exercised through real handlers against testcontainer Postgres + minio, consistent with the existing `internal/server/handlers` and `internal/cli/publish` integration tests.

### Modules to test

- ***`internal/server/filetree`*** — table tests over hand-written key sets. Cover: classify exact file key, classify directory prefix, classify miss → NotFound, classify where a file and a same-stem folder coexist (file wins by exact-match), tree build marks current node + opens its ancestors only, listing orders folders-before-files alphabetically and reports sizes, `IndexFile` precedence (`README.md` over `index.md`, case-insensitive), breadcrumb segments for nested + root + above-root (`?up`) keys, deeply nested keys, and a single-file Bundle.
- ***`internal/markdown`*** — golden HTML for `HighlightFile` across a few languages (go, python, json, plain `.txt`) and an unknown extension (plaintext fallback); table tests for `IsBinary` (valid UTF-8 text → false, content with a NUL byte → true, invalid UTF-8 → true, empty → false).
- ***`internal/server/ssr`*** — golden HTML of the full chrome for: a markdown page (sidebar tree with current-open + aria-current, breadcrumb, Raw toggle present, `/_static` links present), an image page, a directory listing (with and without a rendered README), and a download card. Spot-check mobile markup (top bar + drawer container) and the inline collapse snippet are present.
- `internal/server/service/view` + handlers — integration tests against testcontainer Postgres + minio for each dispatch tier and both guards: markdown renders + embeds a CDN raw URL; an image yields an `<img>` not a fetch; a small text file is highlighted; a `>1 MB` text file yields a download card with **no blob fetch**; a binary file with a text extension yields a download card after sniff; a directory prefix yields a listing; a directory with a README renders it below; an empty-`root_path` manifest fixture yields a root listing at `/<slug>`; an unknown path yields 404; new-surface responses carry the cache + robots headers.
- **End-to-end (extend the existing blackbox test).** After publishing a multi-file fixture Bundle: `curl /<slug>` → root markdown chrome golden; `curl /<slug>/<dir>` → directory listing golden; `curl /<slug>/<image-key>` → image chrome; `curl /<slug>/<code-key>` → highlighted source; assert the CDN raw URL embedded in a markdown page resolves; assert `/_static/*` assets are reachable with immutable cache headers.

## Out of Scope

- **The "On this page" / table-of-contents right column.** Deferred (server-extract was designed but the user deferred it); the layout is sidebar + center only in v1.
- **Per-folder collapse-state persistence.** Only the whole-sidebar collapsed/expanded boolean persists; individual `<details>` reset to the server default (current path auto-opened) on each load.
- **Syntax-highlight theme switching / dark mode.** Out of scope per ADR-0017 (dark mode deferred to v2). One chroma theme.
- **Forcing `Content-Disposition: attachment` on downloads.** The Download link points at the CDN blob; the browser decides whether to render (e.g. PDF) or save. Forced-attachment is deferred.
- **The recursive folder-publish CLI walk** (`mdfly publish <folder> --root <dir>`). The backend ships the nullable-`root_path` tolerance + a fixture test, but the CLI cannot produce a folder-Document in this slice.
- **The `/llm/<slug>` twin.** Not implemented and explicitly off-scope — no directory or folder-root behavior is added to it.
- **PDF inline rendering** (`<embed>`/`<iframe>`). PDFs are download cards in v1.
- **Search within the sidebar tree / file filtering.** Plain tree only.

## Further Notes

- This slice **supersedes S19** — delete that issue; its mobile-responsive requirement is absorbed here as the `app.css` mobile breakpoint, served externally rather than inlined.
- The deepest correctness risk is the URL-convention reversal: `filetree.Classify`, the cross-md link rewrite in `service/view`, and the `?up=N` reconstruction must all agree that keys carry their extension. A single set of golden tests over one fixture Bundle that exercises navigation across markdown ↔ directory ↔ asset is the cheapest proof they agree.
- `filetree` is intentionally pure and `project_root`-free; that is the structural guarantee behind the privacy user stories (11) — there is no code path by which the absolute root can reach the template.
- Natural follow-ups after this slice: the deferred TOC column, the recursive folder-publish CLI walk (which makes folder-Documents real and directory listings reflect on-disk contents), and forced-download `Content-Disposition`.
