# 5. Verify

Run these in order. Each proves one thing that can silently be wrong.

Export the Redis password once — `--no-auth-warning` keeps `-a` from printing a
warning on every call:

```sh
cd ~/mdfly/deploy
export PASS='<the Redis password>'
alias rcli='docker compose exec -T redis redis-cli -a "$PASS" --no-auth-warning'
```

## 5a. Healthy through Cloudflare

```sh
curl -s -o /dev/null -w '%{http_code}\n' https://mdfly.dev/healthz        # 200
curl -s -o /dev/null -w '%{http_code}\n' https://api.mdfly.dev/healthz    # 200
```

Then from the VM, bypassing Cloudflare, to tell origin problems from edge problems:

```sh
curl -sk --resolve mdfly.dev:443:127.0.0.1 -o /dev/null -w '%{http_code}\n' \
  https://mdfly.dev/healthz                                               # 200
```

If the first two fail and this one passes, the problem is DNS or the SSL mode — not
the origin.

## 5b. Origin TLS is Full (strict)

```sh
openssl s_client -connect <reserved-ip>:443 -servername mdfly.dev </dev/null 2>/dev/null \
  | openssl x509 -noout -issuer -dates
# issuer => CloudFlare Origin SSL Certificate Authority
# notAfter => ~15 years out
```

A browser hitting the IP directly **should** show a certificate error. That is the
Origin CA working: only Cloudflare trusts it, so bypassing the edge gets you
nothing. Confirm **SSL/TLS → Overview** reads Full (strict).

## 5c. Tickers are running

```sh
docker compose logs app | grep -E 'lifecycle gc pass|cdn purge drain'
```

`lifecycle gc pass` appears within `LIFECYCLE_GC_INTERVAL` (default 1h) of boot. To
see it now rather than in an hour, temporarily set `LIFECYCLE_GC_INTERVAL=30s` in
`.env` and `docker compose up -d app`, then put it back.

If you left the Cloudflare values empty in step 4, `purge-drain` is **not
registered** — with no credentials every pass would fail, so the job is skipped and
you get `cloudflare not configured, cdn purges will stay queued` at boot instead.
Slugs still enqueue transactionally, so nothing is lost; they drain once a
configured process runs.

## 5d. Rate limiting returns 429

```sh
for i in $(seq 1 11); do
  curl -s -o /dev/null -w '%{http_code} ' -X POST \
    https://api.mdfly.dev/v1/publish/init -H 'Content-Type: application/json' -d '{}'
done; echo
# => ten 400 then 429
```

The `400`s are the empty body — the limiter runs before validation, so any body
works. On the 429, headers carry `X-RateLimit-Limit: 10`,
`X-RateLimit-Remaining: 0`, and `Retry-After` in seconds.

## 5e. The limiter keys on the real client IP

**This is the one that goes wrong silently.** Caddy is the app's TCP peer, so if the
client IP is not carried through, every anonymous caller shares one rate-limit
subject and one abuser throttles everyone.

```sh
rcli --scan --pattern 'rl:*'
```

After 5d you want `rl:ip:<your own public IP>:1m:<step>` — **not** a `172.*` or
`192.168.*` address. Every key must have a TTL (`rcli ttl <key>` returns a positive
number, never `-1`); a key with no TTL would sit outside the `volatile-lru`
eviction pool.

The chain has two halves. Cloudflare sets `CF-Connecting-IP`. Caddy strips that
header when its own peer is outside Cloudflare's published ranges
(`deploy/Caddyfile`). The backend then trusts the header from a loopback or private
peer — i.e. from Caddy (`internal/server/middleware/clientip.go`). Confirm the strip
works by spoofing the header straight at the origin:

```sh
curl -sk --resolve api.mdfly.dev:443:127.0.0.1 -X POST \
  https://api.mdfly.dev/v1/publish/init -H 'CF-Connecting-IP: 9.9.9.9' \
  -H 'Content-Type: application/json' -d '{}' -o /dev/null
rcli --scan --pattern 'rl:ip:9.9.9.9*'
# => empty
```

If `9.9.9.9` appears, the Caddyfile's IP list is stale. Both lists — the Caddyfile's
`@direct` matcher and `cloudflareRanges` in `clientip.go` — come from
<https://www.cloudflare.com/ips/> and must be refreshed together.

## 5f. Fail-open

```sh
docker compose stop redis
curl -s -o /dev/null -w '%{http_code}\n' -X POST \
  https://api.mdfly.dev/v1/publish/init -H 'Content-Type: application/json' -d '{}'
# => 400 — not 429, not 5xx
docker compose logs app --tail 5 | grep 'rate limiter unavailable'
docker compose start redis
```

Writes must keep working with Redis down (ADR-0013). Rate limiting is abuse
mitigation, not a correctness invariant, and the edge limit still stands.

Stopping the container is the right test — the pool holds open sockets, so rotating
the password alone proves nothing.

## 5g. Redis persistence survives a recreate

`docker compose restart` does **not** test this: the container keeps its filesystem,
so a missing named volume still looks durable. Use `down`/`up`:

```sh
rcli set probe:persist ok
docker compose down && docker compose up -d
rcli get probe:persist          # => "ok"
rcli del probe:persist
```

`(nil)` means `redis-data:/data` is not attached — fix that before believing
anything is durable. Use a key with **no TTL**; a TTL'd key is eviction-eligible and
proves less.

**Never `docker compose down -v`** — that deletes the volume.

## 5h. Nothing but 443 is reachable

From your laptop:

```sh
nc -zv -w 3 <reserved-ip> 443     # must connect
nc -zv -w 3 <reserved-ip> 6379    # must time out or refuse
nc -zv -w 3 <reserved-ip> 8080    # must time out or refuse
nc -zv -w 3 <reserved-ip> 80      # must time out or refuse
```

And that no mapping exists at all:

```sh
docker compose ps --format '{{.Service}}\t{{.Ports}}'
# only caddy may show 0.0.0.0:443->443/tcp
```

## 5i. End to end

```sh
mdfly publish note.md                 # note the slug
curl -s -o /dev/null -w '%{http_code}\n' https://mdfly.dev/<slug>           # 200
curl -sD - -o /dev/null https://mdfly.dev/<slug> | grep -i 'cf-cache-status\|x-robots'
# second request => HIT; X-Robots-Tag: noindex, nofollow
```

The LLM twin (`/llm/<slug>`) is **not routed yet** — the purge path already
invalidates its prefix, but the route itself ships with ADR-0010's remaining work.
A request there 404s today; that is expected, not a deploy fault.

Then confirm the purge path, which is the piece most likely to be misconfigured:

```sh
# edit note.md first
mdfly update note.md
curl -sD - -o /dev/null https://mdfly.dev/<slug> | grep -i cf-cache-status
# => MISS or EXPIRED, and the new content is served
```

If it still serves the old content, check the purge queue:

```sh
psql "$DATABASE_URL" -c 'SELECT slug, attempts, next_attempt_at FROM purge_queue'
```

A healthy system keeps this table empty or near-empty. A climbing `attempts` means
Cloudflare is rejecting the purge — check the token, then the zone ID, then the
rejection message in `docker compose logs app`.

If an image in the document fails to load, or the Raw toggle errors in the browser
console, that is the R2 CORS rule from step 3.5.

## Checklist

- [ ] `/healthz` returns 200 through Cloudflare on both `mdfly.dev` and `api.mdfly.dev`
- [ ] Origin cert is Cloudflare Origin CA; SSL mode is Full (strict)
- [ ] `lifecycle gc pass` in the logs
- [ ] Eleventh rapid publish returns 429 with `X-RateLimit-*` and `Retry-After`
- [ ] Redis keys show real client IPs, all with TTLs
- [ ] A spoofed `CF-Connecting-IP` at the origin is ignored
- [ ] Writes still succeed with Redis stopped
- [ ] Redis probe key survives `down` + `up`
- [ ] 6379, 8080, and 80 all refuse from outside; only 443 connects
- [ ] A published document renders and its images load
- [ ] An update invalidates the edge cache and `purge_queue` drains to empty

Next: [6. Operate](6-operate.md).
