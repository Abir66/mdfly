# Upstash setup for the write-path rate limiter (ADR-0028)

The backend rate-limits `POST /v1/publish/init`, `POST /v1/update/init`, and
`DELETE /v1/documents/{slug}` at 10/min and 30/hr per subject, counting in
Upstash Redis over its HTTPS REST API. Nothing is pre-provisioned — do this by
hand once, in the Upstash console.

## 1. Create the database

1. Sign in at <https://console.upstash.com> (GitHub/Google login is fine).
2. **Redis** → **Create Database**.
   - **Name**: `mdfly-ratelimit`
   - **Type**: Regional (free tier). Global costs money and buys nothing here —
     the limiter is called from one VM.
   - **Region**: the region closest to the Oracle VM (ADR-0029). Every write
     request pays this round trip twice (one per window).
   - **TLS**: enabled (default).
3. Create. The free tier gives 500k commands/month and 256 MB; the limiter uses
   4 commands per write request, so budget ~125k write requests/month.

## 2. Copy the REST credentials

On the database page, **REST API** section → **.env** tab. It shows exactly the
two values the server reads:

```
UPSTASH_REDIS_REST_URL=https://<name>-<id>.upstash.io
UPSTASH_REDIS_REST_TOKEN=<long token>
```

Use the **read-write** token (the default one shown), not the read-only token —
the limiter runs `INCR` and `EXPIRE`.

## 3. Set the env vars on the backend

Add both to the backend's environment (the systemd unit / `docker run --env-file`
on the Oracle VM, per S44). No other config is needed: window sizes and limits
are code constants in `internal/server/ratelimit`.

Leaving both unset is a supported mode: the server logs
`upstash not configured, write paths are unthrottled` at boot and runs without
the limiter (local dev, tests). Production must set them — the Cloudflare edge
rule alone is IP-only.

## 4. Verify

After deploying with the vars set:

```sh
# 11 rapid publishes from one IP — the 11th must come back 429.
for i in $(seq 1 11); do
  curl -s -o /dev/null -w '%{http_code}\n' \
    -X POST https://api.mdfly.dev/v1/publish/init \
    -H 'Content-Type: application/json' -d '{}'
done
```

Expect ten `400` (empty bundle — the limiter runs before validation, so any body
works) then `429`. On the 429, check the headers:

```sh
curl -sD - -o /dev/null -X POST https://api.mdfly.dev/v1/publish/init \
  -H 'Content-Type: application/json' -d '{}' | grep -i 'ratelimit\|retry-after'
```

`X-RateLimit-Limit: 10`, `X-RateLimit-Remaining: 0`, `Retry-After: <seconds to
the next minute>`. In the Upstash console, **Data Browser** shows the counter
keys as `rl:ip:<addr>:1m:<step>` and `rl:token:<digest>:1h:<step>`, each with a
TTL. Edit Tokens appear only as a digest prefix — the credential itself is never
written to Upstash.

To confirm fail-open, rotate the token in the console without updating the env
var: writes must keep succeeding, with `rate limiter unavailable, allowing
request` in the logs. Restore the token afterwards.
