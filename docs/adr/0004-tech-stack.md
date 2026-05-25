# Tech stack: Go CLI, Go backend, React frontend

**Amended by ADR-0018:** the v1 viewer is no longer a React SPA. The Go backend SSRs HTML directly (Goldmark + chroma + bluemonday) with a tiny progressive-enhancement JS bundle for Mermaid/KaTeX/clipboard, and a parallel `/llm/<slug>` raw-markdown twin for AI agents. React is deferred to the v2 dashboard if/when it ships. The reasoning below is retained for the original framing.

The `mdfly` CLI and the publishing backend are both written in Go; the viewer web app is a React SPA that fetches Bundle files from object storage and renders markdown client-side.

We chose Go for the CLI to ship a single static cross-compiled binary with no runtime dependency on the user's machine, and Go for the backend so the markdown parser, link-resolver, and validation code can be shared between CLI (pre-flight checks) and server (re-validation). We chose React over plain HTML for the viewer so a richer dashboard (owned-document list, account settings, future versioned-mode UI) can grow on the same codebase without a frontend rewrite — accepting the upfront build pipeline and ~100KB framework cost as the price of that headroom.
