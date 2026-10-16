---
title: Quick start
order: 2
---

No account and no signup. Publish a file and the URL is the only thing printed to stdout.

```sh
mdfly publish notes.md
# https://mdfly.dev/abc12345
```

### Follow linked pages and folders

`-r` walks linked markdown files and folders into one document.

```sh
mdfly publish docs/index.md -r
```

### Publish text without a file

Pipe text in, or pass it with `-m`.

```sh
echo "# Quick note" | mdfly publish
mdfly publish -m "# Quick note"
```

### Open or copy the result

```sh
mdfly publish notes.md --open
mdfly publish notes.md | pbcopy
```

Progress lines such as `uploaded 3/3` go to stderr, so piping stdout never picks them up.

### Update and delete

```sh
mdfly update notes.md
mdfly delete abc12345
```
