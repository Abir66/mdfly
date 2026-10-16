---
title: Global flags
order: 4
---

Every command accepts these flags.

| Flag | Effect |
|---|---|
| `--json` | Print machine-readable JSON instead of text. |
| `-q`, `--quiet` | Suppress non-error output. |
| `-y`, `--yes` | Skip the confirmation prompt of `delete` and `remove --all`. |
| `--api <url>` | Override the API base URL. |

### Output streams

stdout carries only the result: the URL, or the `--json` object. Everything else, including progress and errors, goes to stderr.

### API base URL

The API base resolves in this order: `--api`, the `MDFLY_API` environment variable, `config.toml` in the state directory, then the built-in default.
