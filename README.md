<div align="center">

<img src="internal/server/static/assets/mdFly-logo.svg" width="72" alt="mdFly logo" />

# mdFly

**Publish a markdown file, get a shareable URL — from your terminal.**

`mdfly publish notes.md` uploads your markdown plus every linked image and file,
and hands back a link anyone can open — rendered for people, raw for machines.

</div>

---

## What is mdFly?

mdFly turns any local markdown file into a hosted, shareable document in one
command. It follows the links inside your file — images, other markdown pages,
whole folders — and uploads them alongside the root, so relative links just work
at the URL.

Every document is served two ways from the same content:

- **`mdfly.dev/<slug>`** — a rendered HTML page: file-tree sidebar,
  syntax-highlighted code, inline images, breadcrumbs.
- **`mdfly.dev/raw/<slug>`** — the source bytes, byte-for-byte, with the right
  content type. Raw markdown for a `.md` root, raw source for code, the file
  itself for anything else. Ideal for `curl`, scripts, and AI agents.

No account, no signup. Documents are protected by an unguessable slug and expire
30 days after publish.

## Install

**Shell installer** (macOS, Linux) — verifies the download against release checksums:

```sh
curl -fsSL https://mdfly.dev/install.sh | sh
```

**Homebrew** (macOS, Linux):

```sh
brew install Abir66/mdfly/mdfly
```

## Quick start

```sh
# Publish a single file
mdfly publish notes.md
# → https://mdfly.dev/abc12345

# Follow linked markdown pages and folders too
mdfly publish docs/index.md -r

# Publish inline text without a file
echo "# Quick note" | mdfly publish
mdfly publish -m "# Quick note"

# Open the result in your browser
mdfly publish notes.md --open

# The URL is the only thing on stdout — pipe it anywhere
mdfly publish notes.md | pbcopy
```

Updating and deleting use the edit token mdFly stored for you at publish time
(kept in `~/.mdfly/`):

```sh
mdfly update notes.md    # push changes to the same URL
mdfly delete abc12345    # take it down (serves 410 afterward)
```

## Commands

| Command | What it does |
|---|---|
| `mdfly publish <file>` | Mint a new URL from a file (or inline text via `-m`/stdin). |
| `mdfly update <file>` | Push changes to an existing document at the same URL. |
| `mdfly delete <slug\|file>` | Take a document down server-side. |
| `mdfly remove <slug\|file>` | Drop a document from local tracking only (no server call). |
| `mdfly open <slug\|file>` | Open a document's URL in the browser. |
| `mdfly list` | List the documents you've published from this machine. |

Every command takes `--json` for machine-readable output, `--plain` /
`NO_COLOR=1` to disable color, and `-v` / `-q` to tune noise. Run
`mdfly <command> --help` for the full flag list.

**Useful flags:** `-r` follow linked markdown/folders · `-m <text>` publish inline
text · `--open` open in browser after success · `--force` override an update conflict.

## Limits

Each published document is capped at:

| Limit | Value |
|---|---|
| Total bundle size | 25 MB |
| File count | 50 files |
| Single file size | 10 MB |
| Lifetime | 30 days|

The CLI checks these before uploading and aborts early; the server re-validates.

## Features

- **One-command publish** — root file plus every reachable image, page, and asset,
  uploaded byte-for-byte. Relative links keep working at the URL.
- **Dual read surface** — a rendered page for humans, a byte-exact raw view at
  `/raw/<slug>` for `curl`, scripts, and AI agents.
- **Recursive bundles** — `-r` walks linked markdown and folders transitively into
  one document with a browsable file tree.
- **Anything, not just markdown** — text, code (syntax-highlighted), images, and
  binaries all publish; the viewer renders each by type.
- **No account needed** — publish out of the box; documents are protected by an
  unguessable slug.
- **Content-addressed & immutable** — files are stored by SHA-256 of their bytes,
  giving end-to-end integrity and CDN-cacheable assets.
- **Scriptable by design** — clean stdout/stderr split, categorical exit codes, and
  `--json` on every command make mdFly safe to drop into CI.
- **Publish inline text** — pipe or `-m` markdown straight from the shell, no file
  on disk required.

## Roadmap

- **Web editor** — create and edit documents in the browser, not just the CLI.
- **Password-protected docs** *(exploring)* — a shared-secret gate for documents you
  don't want readable by anyone with the link.
- **LLM Twin** - `/llm/slug` return raw markdown with internal links rewritten for better navigation
- **Skill** - Create a skill for AI agents
- **Dark Mode** - Add dark mode to published docs


## License

Licensed under the [Apache License, Version 2.0](LICENSE).
