# Architecture Decision Records

One ADR per locked decision, each two paragraphs: what was decided, and the trade-off. Read in numeric order — later ADRs assume earlier ones. [CONTEXT.md](../../CONTEXT.md) is the spec; these are the why.

There are no superseded ADRs in this directory. When a decision is reversed, the ADR it belongs to is **rewritten in place** and the reversal is recorded inside its trade-off paragraph — no amendment headers, no supersession chains, no archive. Git history holds every prior version.

| # | Decision |
|---|---|
| [0001](0001-document-shape-slug-visibility-namespace.md) | One mutable public Document per slug, flat global namespace |
| [0002](0002-go-stack-and-monorepo-layout.md) | Go for both binaries, single-module monorepo layout |
| [0003](0003-compute-always-free-vm-and-in-process-tickers.md) | Always-on free-tier VM, in-process tickers in a dedicated jobs process |
| [0004](0004-content-hash-flat-storage-on-r2.md) | Content-hash flat storage on R2 with manifest indirection |
| [0005](0005-postgres-metadata-store-schema-and-lifecycle.md) | Postgres metadata store: six tables, lifecycle GC, full DDL |
| [0006](0006-publish-and-update-wire-protocols.md) | Publish and Update wire protocols, idempotency, concurrency |
| [0007](0007-multi-provider-identity-no-handle.md) | Multi-provider OAuth identity, no mdfly-owned handle |
| [0008](0008-edit-tokens-and-local-secret-hygiene.md) | Anonymous Edit Tokens and local secret hygiene |
| [0009](0009-explicit-cli-verbs-and-slug-keyed-local-state.md) | Explicit CLI verbs over slug-keyed local state |
| [0010](0010-ssr-view-path-viewer-chrome-and-llm-twin.md) | SSR view path: Viewer chrome, LLM twin, any-file Root |
| [0011](0011-cloudflare-edge-dns-tls-and-apex-routing.md) | Cloudflare edge: DNS, TLS, subdomain split, apex routing |
| [0012](0012-durable-cdn-purge-queue.md) | Durable CDN purge queue with retry |
| [0013](0013-two-layer-identity-aware-rate-limiting.md) | Two-layer identity-aware rate limiting |
| [0014](0014-v1-scope.md) | v1 milestone scope |
| [0015](0015-prebuilt-images-and-blue-green-deploy.md) | Prebuilt images, operator-triggered blue-green deploy |
