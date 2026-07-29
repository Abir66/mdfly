# Redis setup for the write-path rate limiter (ADR-0013)

The backend rate-limits `POST /v1/publish/init`, `POST /v1/update/init`, and
`DELETE /v1/documents/{slug}` at 10/min and 30/hr per subject, counting in Redis
over a pooled TCP connection.

Redis is **self-hosted on the backend's own VM** (ADR-0003) as a sibling
container — no vendor, no command quota, and a sub-millisecond hop instead of a
10–50 ms TLS round trip on every write request. It is configured for durability
so it can back state beyond the limiter later, even though the counters
themselves are disposable.

The server reads **one** env var, `REDIS_URL`, so a managed endpoint remains a
URL swap with no code change if this ever needs to move off-box (§6).

## 1. The Redis config file

Copy the tracked example and edit the password into it:

```sh
cp deploy/redis.conf.example deploy/redis.conf
chmod 600 deploy/redis.conf
```

The example carries four decisions worth understanding:

| Setting | Why |
|---|---|
| `bind 0.0.0.0` | All interfaces *this container has* — not the internet, because no host port is published (§3) |
| `maxmemory 512mb` | A ceiling, so the Linux OOM killer never gets to choose between Redis and the Go server |
| `maxmemory-policy volatile-lru` | Evict only TTL-bearing keys. Every limiter key has one; durable data added later would not, so pressure sheds counters first. `allkeys-lru` would do the opposite |
| `appendonly yes` + `appendfsync everysec` | A crash loses at most one second of counting. `save 900 1` adds RDB snapshots as a copyable backup artifact |

Generate the password with `openssl rand -base64 32`. It goes in **two** places
and must match: literally after `requirepass` here (Redis config cannot read an
environment variable), and inside `REDIS_URL` in the repo-root `.env`. That is why
`deploy/redis.conf` is gitignored and only `redis.conf.example` is tracked.

Nothing else needs tuning. Window sizes, limits, pool size, and timeouts are
code constants in `internal/server/ratelimit` and `internal/server/redis`.

## 2. The compose service

Already in `deploy/compose.yaml` — nothing to add. It mounts `redis.conf`
read-only, attaches the `redis-data` named volume at `/data`, sets
`restart: unless-stopped`, and deliberately declares no `ports:` (§3).

The one value you supply is in the repo-root `.env`:

```
REDIS_URL=redis://:<the password from §1>@redis:6379
```

`redis` is the compose service name; Docker's embedded DNS resolves it on the
project network.

**The named volume is the load-bearing detail.** A container's own filesystem is
destroyed when the container is recreated, so without `redis-data:/data` Redis
writes its AOF faithfully and the file disappears on every redeploy — you get
the appearance of persistence with none of the substance. Named volume, not an
anonymous one, not a bind mount into the container's ephemeral layer.

## 3. Why no published port

Docker only opens a host firewall path for ports listed under `ports:`. Omitting
the key leaves Redis addressable on the compose bridge network — by the `app`
container, the only thing that needs it — and unreachable from the internet, so
`bind 0.0.0.0`
here means "all interfaces *this container has*", not "the public internet".
`requirepass` is defence in depth for the case where something else later joins
that network. Never add a `6379:6379` mapping "to debug" — use
`docker compose exec redis redis-cli` instead, which needs no exposure.

## 4. Behaviour under memory pressure

512 MB is roughly 25× what the limiter needs: two keys per subject per window,
both TTL'd, so ~100k distinct subjects in an hour lands near 20 MB. The headroom
exists so the cases below stay theoretical.

At `maxmemory` with `volatile-lru`, Redis evicts the least-recently-used
TTL-bearing key to make room. Limiter keys are the entire eviction pool. An
evicted counter means a subject silently gets a fresh allowance — the same
outcome as fail-open, and acceptable.

If the cap is reached with **no** volatile keys left to shed, writes get
`OOM command not allowed when used memory > 'maxmemory'.` Reads keep working and
Redis does not crash. The limiter treats that like any other Redis error and
**fails open**, so the write path stays up and the Cloudflare edge limit (L1)
still throttles floods. A future durable consumer would see the error
explicitly, which is the point of choosing `volatile-lru` — better an error it
can handle than an eviction it discovers later.

Watch three numbers: `used_memory` against `maxmemory`, `evicted_keys`, and free
disk on the volume. `docker compose exec redis redis-cli -a "$PASS" info memory`
covers the first two.

## 5. Verify

The `redis-cli` commands below need the §1 password. Export it once, and add
`--no-auth-warning` to keep `-a` from printing a warning on every call:

```sh
export PASS='<the password from §1>'
```

### 5a. The limiter throttles

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
the next minute>`.

### 5b. The keys look right

```sh
docker compose exec redis redis-cli -a "$PASS" --scan --pattern 'rl:*'
```

Each request creates two keys, each with a TTL — for the anonymous curls above,
`rl:ip:<addr>:1m:<step>` and `rl:ip:<addr>:1h:<step>`; an authenticated request
writes `rl:token:<digest>:1m:<step>` and `rl:token:<digest>:1h:<step>` instead.
Edit Tokens appear only as a digest prefix — the credential itself is never
written to Redis. An IPv6 caller has its colons flattened to dots, so `::1`
appears as `rl:ip:..1:1m:<step>` rather than splitting the key into more
segments. Confirm every key has a TTL (`redis-cli -a "$PASS" ttl <key>` returns
a positive number, never `-1`) — a limiter key without one would be outside the
`volatile-lru` eviction pool.

### 5c. Persistence survives a restart

```sh
docker compose exec redis redis-cli -a "$PASS" set probe:persist ok
docker compose restart redis
docker compose exec redis redis-cli -a "$PASS" get probe:persist   # => "ok"
docker compose exec redis redis-cli -a "$PASS" del probe:persist
```

Then the stronger test — the one that catches a missing named volume:

```sh
docker compose exec redis redis-cli -a "$PASS" set probe:persist ok
docker compose down && docker compose up -d
docker compose exec redis redis-cli -a "$PASS" get probe:persist   # => "ok"
```

If the second returns `(nil)`, the volume is not attached. Fix that before
believing anything is durable. Use a key with no TTL for this probe: a TTL'd key
is eviction-eligible and proves less.

### 5d. Fail-open

Make Redis genuinely unreachable — the pool holds open connections, so rotating
the password alone leaves live sockets working and proves nothing. Stop the
container outright:

```sh
docker compose stop redis
```

Writes must keep succeeding, with `rate limiter unavailable, allowing request`
in the logs. Restart (`docker compose start redis`) and re-run §5a to see the
limiter throttle again.

## 6. Boot-time modes

Leaving `REDIS_URL` unset is a supported mode: the server logs
`redis not configured, write paths are unthrottled` at boot and runs without the
limiter (local dev, tests). Production must set it — the Cloudflare edge rule
alone is IP-only.

A **malformed** URL is not a supported mode: it fails the boot rather than
silently degrading to unthrottled.

If the URL parses but Redis is unreachable at boot, the server logs
`redis unreachable at boot, rate limiter will fail open` and starts anyway.

## 7. If this ever moves off-box

A managed instance works with no code change — take the provider's **RESP/TCP**
`rediss://` URL, not a REST endpoint, since the adapter speaks RESP over a pooled
connection, and set it as `REDIS_URL`. Sections 1–3 stop applying (the provider
owns persistence and memory policy) and §5b/§5c move to the provider's own console
and tooling.

Two things to check before making that trade: the per-request latency cost of
leaving the box, and whether the free tier's monthly command quota covers your
write volume at ~1 command per write request.
