---
title: Limits
order: 6
---

Each published document is capped at:

| Limit | Value |
|---|---|
| Total bundle size | 25 MB |
| File count | 50 files |
| Single file size | 10 MB |
| Lifetime | 30 days |

The CLI checks size and file count before uploading and stops early with exit code 2. The server checks again.

### Expiry and removal

Documents expire 30 days after they are published or last updated. An expired or deleted document answers 410 Gone at both its page and raw URLs.

### Rate limits

Publishing, updating and deleting are limited to 10 requests per minute and 30 per hour. Reading documents is not limited. Hitting a limit exits with code 5.
