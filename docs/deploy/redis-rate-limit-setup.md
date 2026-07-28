# Redis setup for the write-path rate limiter (ADR-0028)

The backend rate-limits `POST /v1/publish/init`, `POST /v1/update/init`, and
`DELETE /v1/documents/{slug}` at 10/min and 30/hr per subject, counting in Redis
over a pooled TCP connection. Nothing is pre-provisioned — do this by hand once.

The server reads **one** env var, `REDIS_URL`, so either backend below works with
no code change:

- **Upstash** (default) — `rediss://…`, managed, free tier, no ops.
- **Self-hosted on the Oracle VM** — `redis://127.0.0.1:6379`, sub-millisecond,
  no vendor. See §5.

## 1. Create the Upstash database

1. Sign in at <https://console.upstash.com> (GitHub/Google login is fine).
2. **Redis** → **Create Database**.
   - **Name**: `mdfly-ratelimit`
   - **Type**: Regional (free tier). Global costs money and buys nothing here —
     the limiter is called from one VM.
   - **Region**: the region closest to the Oracle VM (ADR-0029). Every write
     request pays this round trip once.
   - **TLS**: enabled (default).
3. Create. The free tier gives 500k commands/month and 256 MB; the limiter uses
   4 commands per write request, so budget ~125k write requests/month.

## 2. Copy the TCP connection URL

On the database page, use the **Redis (TCP)** connection tab — *not* the REST
tab. Take the `rediss://` URL, which already embeds the password:

```
REDIS_URL=rediss://default:<password>@<name>-<id>.upstash.io:6379
```

The password is the read-write credential; the limiter runs `INCR` and `EXPIRE`,
so a read-only token will not work.

## 3. Set the env var on the backend

Add it to the backend's environment (the systemd unit / `docker run --env-file`
on the Oracle VM, per S44). No other config is needed: window sizes, limits, pool
size, and timeouts are code constants in `internal/server/ratelimit` and
`internal/server/redis`.

Leaving it unset is a supported mode: the server logs
`redis not configured, write paths are unthrottled` at boot and runs without the
limiter (local dev, tests). Production must set it — the Cloudflare edge rule
alone is IP-only. A **malformed** URL is not a supported mode: it fails the boot
rather than silently degrading to unthrottled.

If the URL parses but Redis is unreachable at boot, the server logs
`redis unreachable at boot, rate limiter will fail open` and starts anyway.

## 4. Verify

After deploying with the var set:

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
the next minute>`. In the Upstash console, **Data Browser** shows the two keys
one request creates, each with a TTL — for the anonymous curls above,
`rl:ip:<addr>:1m:<step>` and `rl:ip:<addr>:1h:<step>`; an authenticated request
writes `rl:token:<digest>:1m:<step>` and `rl:token:<digest>:1h:<step>` instead.
Edit Tokens appear only as a digest prefix — the credential itself is never
written to Redis.

To confirm fail-open, make Redis genuinely unreachable — the pool holds open
connections, so rotating the password alone leaves the live sockets working and
proves nothing. Drop the connections: block egress to the Redis port from the VM
(`sudo iptables -A OUTPUT -p tcp --dport 6379 -j REJECT`), or for a self-hosted
instance `sudo systemctl stop redis-server`. Writes must keep succeeding, with
`rate limiter unavailable, allowing request` in the logs. Restore Redis
afterwards (`sudo iptables -D OUTPUT …` / `systemctl start`) and re-run §4 to see
the limiter throttle again.

## 5. Alternative: self-host on the Oracle VM

Because compute is an always-on VM (ADR-0029), a local Redis is a valid store and
removes both the network hop and the vendor:

```sh
sudo apt-get install -y redis-server
sudo systemctl enable --now redis-server
```

Then set `REDIS_URL=redis://127.0.0.1:6379` and restart the backend. Bind Redis to
loopback only (the Ubuntu default) and leave it off the VM's public ingress rules.
Verification in §4 is unchanged except that `redis-cli --scan --pattern 'rl:*'`
replaces the Data Browser. The limiter's guarantee is unaffected: counters still
survive a backend restart, since Redis is a separate process. They do not survive
a VM rebuild: the counters reset, so a subject that had spent its allowance gets
a fresh one and the replacement VM serves traffic unthrottled until the windows
refill. Accepted — a rebuild is rare and operator-driven, and the Cloudflare edge
limit still stands throughout.
