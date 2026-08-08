# 5. Verify

Run these in order. Each proves one thing that can silently be wrong.

Set up the shell once. `redis-cli` reads the password from `REDISCLI_AUTH`, which
keeps it out of the process list, and `-e REDISCLI_AUTH` is what carries it into the
container — without it every command returns `NOAUTH Authentication required.`

```sh
cd ~/mdfly/deploy
export REDISCLI_AUTH='<the Redis password>'
alias rcli='docker compose exec -T -e REDISCLI_AUTH redis redis-cli'
export DATABASE_URL=$(grep -m1 '^DATABASE_URL=' ../.env | cut -d= -f2-)
```

The web tier is two slots and only one of them is serving, so name it once rather
than guessing per command:

```sh
export SLOT=$(docker ps --filter label=com.docker.compose.project=mdfly \
  --format '{{.Label "com.docker.compose.service"}}' | grep -m1 '^app_')
echo "$SLOT"                      # app_blue, or app_green after a swap
slotc() { case "$SLOT" in app_green) docker compose --profile green "$@" ;;
                         *) docker compose "$@" ;; esac; }
```

`app_green` sits behind a compose profile, so any `docker compose` command that
*names* it needs `--profile green` — that is all `slotc` does. Never add the
profile to a bare `up`: it would start both slots at once, which Caddy resolves by
preferring blue and `deploy.sh` refuses to reason about at all.

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

443 is open only to Cloudflare's ranges (step 1.3), so your laptop cannot reach the
origin socket at all. Read the certificate from the VM instead:

```sh
openssl s_client -connect 127.0.0.1:443 -servername mdfly.dev </dev/null 2>/dev/null \
  | openssl x509 -noout -issuer -dates
# issuer => CloudFlare Origin SSL Certificate Authority
# notAfter => ~15 years out
```

Two independent things stop an edge bypass, and both should hold. The VCN rule
means a non-Cloudflare client never completes a TCP handshake. Even if it did, the
Origin CA cert is trusted only by Cloudflare, so a browser hitting the IP directly
would show a certificate error. Confirm **SSL/TLS → Overview** reads Full (strict).

## 5c. Tickers are running

The tickers are in the **`jobs` container**, not the web slots (ADR-0003). A web
slot logs nothing periodic at all, so grepping it proves nothing.

```sh
docker compose logs jobs | grep -E 'jobs started|lifecycle gc pass|cdn purge drain'
```

`lifecycle gc pass` appears within `LIFECYCLE_GC_INTERVAL` (default 1h) of boot. To
see it now rather than in an hour, temporarily set `LIFECYCLE_GC_INTERVAL=30s` in
`.env` and `docker compose up -d jobs`, then put it back.

If you left the Cloudflare values empty in step 4, `purge-drain` is **not
registered** — with no credentials every pass would fail, so the job is skipped and
you get `cloudflare not configured, cdn purges will stay queued` at boot instead.
Slugs still enqueue transactionally, so nothing is lost; they drain once a
configured process runs.

### The `job_runs` stamps, which outlive the logs

Logs are the wrong instrument for "is the GC still alive next month?" — they roll
over, and a container that died an hour ago still has yesterday's happy lines.
Every pass upserts its outcome into `job_runs` (one row per job, not a history),
and that row is the only answer:

```sh
psql "$DATABASE_URL" -c 'SELECT name, last_success_at, last_failure_at, consecutive_failures FROM job_runs'
```

What each column means:

- **No row for a job** — it has not completed a pass yet. Expected in the first
  hour; not expected after `LIFECYCLE_GC_INTERVAL` has elapsed.
- **`last_success_at` older than the interval** — the pass is failing or the
  container is not running. `last_error` holds the reason.
- **`consecutive_failures` climbing** — the pass fails every time. Zero resets on
  the next success.

Exactly one `jobs` container may run (ADR-0003) — the purge drain's claim is
lease-free and assumes a single writer. `docker compose ps jobs` showing more than
one is a bug, not headroom.

## 5d. Rate limiting returns 429

Start from an empty bucket, or the count will be off. The per-minute window is a
fresh key each minute, so `rcli --scan --pattern 'rl:*'` returning nothing means you
are clear; otherwise wait out the minute. The **hourly** window is the one that
bites — 30/hr per subject means this loop is only good for two more runs in the same
hour before every response is a 429.

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
slotc logs "$SLOT" --tail 5 | grep 'rate limiter unavailable'
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

`down` removes every container in the project, green included, and the following
`up -d` starts only the default profile — so if green was the live slot you come
back on blue. Same image either way, since both slots read `MDFLY_IMAGE`; it is
the *slot* that moves, and `deploy.sh` will detect blue as live next time.

**Never `docker compose down -v`** — that deletes the volume.

## 5h. Nothing is reachable except 443, from Cloudflare only

From your laptop — which is not a Cloudflare address, so **every** port including
443 must fail:

```sh
nc -zv -w 3 <reserved-ip> 443     # must time out  <- the VCN allowlist working
nc -zv -w 3 <reserved-ip> 6379    # must time out or refuse
nc -zv -w 3 <reserved-ip> 8080    # must time out or refuse
nc -zv -w 3 <reserved-ip> 80      # must time out or refuse
```

443 connecting from here means the ingress rule is still `0.0.0.0/0` — the edge
is bypassable and the WAF, edge rate limit, and cache are all optional to an
attacker. Fix step 1.3 before going further.

That 443 works *through* the edge is what 5a already proved. Listening locally is
separately checkable on the VM with `nc -zv -w 3 127.0.0.1 443`.

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
rejection message in `docker compose logs jobs` — the drain runs there.

If an image in the document fails to load, or the Raw toggle errors in the browser
console, that is the R2 CORS rule from step 3.5.

## 5j. A code-only deploy drops nothing

The claim being tested is narrow and worth stating: on the **swap path** — no new
migration — the idle slot is started and health-checked *before* the live one is
stopped, and Caddy's `lb_policy first` moves traffic the instant the live slot goes
away. No request should see a 5xx.

Two terminals. The first hammers the site through Cloudflare for the duration:

```sh
while true; do
  curl -s -o /dev/null -w '%{http_code}\n' https://mdfly.dev/healthz
  sleep 0.1
done | sort | uniq -c
```

The second runs a deploy of a commit that changes no migration — redeploying the
current tag is enough:

```sh
cd ~/mdfly/deploy
./deploy.sh --dry-run             # must say "code-only", not "schema change"
./deploy.sh
```

Stop the loop after `deploy.sh` returns. The count must be **one line, `200`**. A
handful of `502`s means the fall-through is not working: check that both slots are
in `deploy/Caddyfile` and that `health_uri /healthz` is present.

A single `502` at the moment of the swap is a different fault, and a subtle one.
Caddy keeps an upstream **out of the pool** until one of its own active probes
succeeds, and the idle slot fails DNS for as long as it does not exist — so it
enters the swap already marked unhealthy. If the live slot stops before Caddy's
next probe the pool is empty, and an empty pool is a 502 that `lb_retries` cannot
rescue. `deploy.sh` waits this out (`await_caddy_upstream`, one `MDFLY_CADDY_SETTLE`
of 4s against a 2s `health_interval`); if you see one anyway, those two numbers
have drifted apart.

Confirm the slot actually moved, rather than the deploy having done nothing:

```sh
docker ps --filter label=com.docker.compose.project=mdfly \
  --format '{{.Label "com.docker.compose.service"}}\t{{.Image}}'
# the serving slot is the other colour, on the new tag; the old slot is gone
```

An in-flight request survives its slot being stopped for a separate reason — the
drain — and that is the timeout chain in step 4.1, not the swap.

## 5k. A schema deploy is a short, visible outage

On the **schema path** there is deliberately no overlap: a migration would
otherwise have to be readable by both versions at once, and ADR-0015 buys ~5
seconds of downtime rather than a permanent expand/contract rule. Verify the shape
of it so it is not a surprise at 1am.

With the same request loop running, deploy a commit that adds a migration:

```sh
./deploy.sh --dry-run
# path:    schema change — migrate, then recreate the live slot
./deploy.sh
```

Expected: a **short run of `502`s** — seconds, not minutes — while the single slot
is recreated, then `200` again. That is the documented cost, not a fault. What must
*not* happen is the migration failing and code starting anyway; `deploy.sh` runs
`migrate` first and aborts before touching a container if it fails.

**Rollback cannot undo this.** The bookmark holds a tag, and a tag cannot un-apply
a migration, so `deploy.sh rollback` refuses to cross one without `--force`. A bad
schema deploy is fixed forward.

## Checklist

- [ ] `/healthz` returns 200 through Cloudflare on both `mdfly.dev` and `api.mdfly.dev`
- [ ] Origin cert is Cloudflare Origin CA; SSL mode is Full (strict)
- [ ] `lifecycle gc pass` in the **jobs** container's logs
- [ ] `job_runs` holds a row per registered job with a recent `last_success_at`
- [ ] Eleventh rapid publish returns 429 with `X-RateLimit-*` and `Retry-After`
- [ ] Redis keys show real client IPs, all with TTLs
- [ ] A spoofed `CF-Connecting-IP` at the origin is ignored
- [ ] Writes still succeed with Redis stopped
- [ ] Redis probe key survives `down` + `up`
- [ ] 6379, 8080, and 80 all refuse from outside; only 443 connects
- [ ] A published document renders and its images load
- [ ] An update invalidates the edge cache and `purge_queue` drains to empty
- [ ] A code-only `./deploy.sh` under a request loop returns `200` and nothing else
- [ ] A schema `./deploy.sh` returns to `200` within seconds, and rollback refuses
      to cross the migration

Next: [6. Operate](6-operate.md).
