---
title: Raw links for agents
order: 5
---

Every document has two views from the same content: a rendered page at `https://mdfly.dev/<slug>`, and the source bytes at `https://mdfly.dev/raw/<slug>`.

```sh
curl https://mdfly.dev/raw/abc12345
```

### Files inside a bundle

Files in a bundle are addressed by path.

```sh
curl https://mdfly.dev/raw/abc12345/images/diagram.png
```

Responses are byte-exact with an appropriate content type: markdown stays markdown, code stays code, and other files come back as themselves.

### JSON output for scripts

`mdfly publish --json` prints one object with these fields.

| Field | Meaning |
|---|---|
| `url` | The rendered page URL. |
| `slug` | The document slug. |
| `manifest_hash` | Hash of the published file manifest. |
| `tier` | The limit tier the document was published under. |

```sh
mdfly publish notes.md --json
```
