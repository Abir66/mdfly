---
title: Commands
order: 3
---

Run `mdfly <command> --help` for the full flag list. `delete`, `open` and `remove` take a slug or the path of a file you published from this machine. `update` takes the file; use `--slug` to target by slug.

### Edit tokens

Publishing mints an edit token. mdFly stores it locally under `~/.mdfly/` and sends it when you `update` or `delete` that document. Set `MDFLY_CONFIG_DIR` to keep that state somewhere else. The token is never shown on stdout.

### publish [file]

Upload a file, or inline text, and print its URL.

| Flag | Effect |
|---|---|
| `-r`, `--recursive` | Follow linked `.md` files and linked folders transitively. |
| `-m`, `--message <text>` | Publish inline text instead of a file. |
| `--open` | Open the URL in your browser after publishing. |

With no file and no `-m`, `publish` reads stdin.

### update [file]

Push changes to an existing document. The URL stays the same, and the 30-day lifetime restarts.

| Flag | Effect |
|---|---|
| `--slug <slug>` | Target a document by slug, for text updates, recovery or when a file maps to several documents. |
| `-m`, `--message <text>` | Update with inline text instead of a file. |
| `-r`, `--recursive` | Follow linked `.md` files and linked folders. |
| `--force` | Overwrite without the concurrency check against the last version you pushed. |
| `--open` | Open the URL after updating. |

Without `--force`, an update fails with a conflict if the document changed since you last pushed it.

### delete [slug-or-file]

Take a document down on the server. Its URL then answers 410. It asks for confirmation; pass `-y` to skip the prompt, which is required when stdin is not a terminal.

| Flag | Effect |
|---|---|
| `--slug <slug>` | Target a document by slug when a file maps to several. |

### list

Print the documents you published from this machine, newest first. It reads local state and makes no network call. Columns are slug, tier, updated time, path and URL. With `--json` it prints an array.

### open [slug-or-file]

Open a published document in your browser.

| Flag | Effect |
|---|---|
| `--slug <slug>` | Target a document by slug when a file maps to several. |

### remove [slug-or-file]

Forget a document locally. This touches local state only: the document stays online, and you lose the edit token, so you can no longer update or delete it.

| Flag | Effect |
|---|---|
| `--slug <slug>` | Target a document by slug when a file maps to several. |
| `--all` | Remove every tracked document from local state. Cannot be combined with a target or `--slug`. |
