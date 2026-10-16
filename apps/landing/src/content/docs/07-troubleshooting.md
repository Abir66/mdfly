---
title: Troubleshooting
order: 7
---

### Exit codes

Every failure maps to one exit code, so scripts can branch on it.

| Code | Meaning |
|---|---|
| 0 | Success. |
| 1 | Unrecognized failure. |
| 2 | Bad input: invalid flag or argument, no content, a missing file, a bundle over the limits, or a request the server rejects as malformed. |
| 3 | Missing or invalid edit token. |
| 4 | Conflict: the document changed since your last push. |
| 5 | Over quota: bundle too large for the server, or rate limited. |
| 6 | Server error, network failure, or an upload that kept failing after retries. |

### Rate limited

Exit code 5 with a rate-limit message means you passed 10 writes per minute or 30 per hour. Wait a minute and retry.

### Update conflict

Exit code 4 means the document changed since you last pushed it. If you intend to replace it, run `mdfly update --force`.

### Lost edit token

The token lives in `~/.mdfly/`. If it is gone, you cannot update or delete that document. Publish again for a new URL; the old one expires after 30 days.

### Delete refuses without confirmation

When stdin is not a terminal, `mdfly delete` exits with code 2 unless you pass `-y`.

### 410 Gone

The document was deleted or has expired. Publish it again to get a new URL.
