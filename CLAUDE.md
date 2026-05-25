# mdFly

mdFly is a CLI-driven markdown sharing service. User runs `mdfly publish foo.md`, the CLI uploads the markdown + linked assets and returns a shareable URL.

## Components

- `cmd/mdfly` — CLI (Go)
- `cmd/mdfly-server` — backend (Go; SSRs human HTML, serves `/llm/<slug>` raw-markdown twin for AI agents)
- `apps/landing/` — static landing site, Cloudflare Pages (added v1.5)
- `apps/dashboard/` — React SPA (added v2)

## Where the design lives

1. [CONTEXT.md](CONTEXT.md) — authoritative glossary of every term + behavior. Read this first.
2. [docs/adr/](docs/adr/) — one ADR per locked decision. Read in numeric order; each is two paragraphs (decision + trade-off).

CONTEXT.md is the spec; ADRs are the why.

## Hosts

- `mdfly.dev` — apex; Worker-routed between Cloudflare Pages (landing/legal) and backend (slugs + LLM twin)
- `api.mdfly.dev/v1/*` — backend HTTP API
- `cdn.mdfly.dev` — R2 assets
- `app.mdfly.dev` — v2 dashboard (NXDOMAIN in v1)
- `www.mdfly.dev` — 301 → apex

## Don't write code unless asked

The project is pre-implementation. Do not start writing Go, SQL migrations, Dockerfiles, IaC, etc., until the user explicitly asks.

## Commiting

docs/issues folder are added in gitignore. They are only for local issue tracking. Don't add them in git
