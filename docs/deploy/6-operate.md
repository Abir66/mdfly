# 6. Operate

Redeploys, troubleshooting, and the things worth watching.

All commands run from `~/mdfly/deploy` — `docker compose` finds `compose.yaml` by
looking in the working directory, so from anywhere else you need
`docker compose -f ~/mdfly/deploy/compose.yaml …`.

Several commands below need the Redis password or the database URL. In a fresh shell:

```sh
cd ~/mdfly/deploy
export REDISCLI_AUTH='<the Redis password>'
alias rcli='docker compose exec -T -e REDISCLI_AUTH redis redis-cli'
export DATABASE_URL=$(grep -m1 '^DATABASE_URL=' ../.env | cut -d= -f2-)

export SLOT=$(docker ps --filter label=com.docker.compose.project=mdfly \
  --format '{{.Label "com.docker.compose.service"}}' | grep -m1 '^app_')
slotc() { case "$SLOT" in app_green) docker compose --profile green "$@" ;;
                         *) docker compose "$@" ;; esac; }
```

`$SLOT` is whichever web slot is serving — `app_blue` or `app_green`. Re-run that
line after a deploy; a swap moves it. `app_green` is behind a compose profile, so
naming it needs `--profile green`, which is all `slotc` adds. **Never put the
profile on a bare `up`** — that starts both slots, Caddy then prefers blue, and
`deploy.sh` refuses to run at all until one is stopped.

## 6.1 Redeploy

One command, from a checkout on the box:

```sh
cd ~/mdfly/deploy
./deploy.sh --dry-run     # what would happen; starts nothing, pulls nothing
./deploy.sh               # deploy the pulled HEAD
./deploy.sh <sha>         # or a specific published tag
```

`deploy.sh` pulls the repo (for `compose.yaml`, the Caddyfile and `migrations/` —
the things not baked into the image), pulls `ghcr.io/abir66/mdfly:<tag>`, writes
that tag into `deploy/.env`, and then takes one of four paths **it picks itself**.
There is no flag for the path, deliberately: a flag is forgotten exactly when you
are tired, and the failure it prevents is old code meeting a schema it does not
understand while visitors are on the site.

| Path | When | What happens |
|---|---|---|
| code-only | `migrations/` matches the applied version | start the idle slot, wait for its `/healthz`, stop the live one — no dropped requests |
| schema change | `migrations/` has outrun it | run `migrate`, then recreate the single live slot in place — a few seconds of downtime (ADR-0015) |
| cold start | nothing is serving | bring the whole default profile up |
| cold start with a schema change | both of the above — a first deploy | migrate, then bring the whole default profile up |

Read `--dry-run`'s first line before every deploy; it is the only place the path is
announced.

If the idle slot never answers `/healthz`, the deploy aborts, stops it, restores
the previous tag, and leaves the live slot serving. Nothing to undo.

The image prune is part of the deploy and keeps exactly two tags: what is serving
and what rollback would return to.

### Rollback

```sh
./deploy.sh rollback --dry-run
./deploy.sh rollback
```

The outgoing tag is bookmarked in `deploy/.deploy-state` before every swap, so
rollback is "start the previous image" rather than "find the old commit". It takes
no tag argument — it returns to the bookmarked one.

> **Rollback is a code-only tool.** A tag cannot un-apply a migration, so returning
> across one puts the old binary in front of a schema it has never seen. The script
> warns and then refuses unless you pass `--force`. **Schema deploys are fixed
> forward**: write the next migration, deploy that.

### Why the schema path has no overlap

Migrations run before any new code starts, and on that path only one version is
ever running. The alternative — expand/contract, where every migration must be
readable by both the outgoing and incoming code — turns a rename into two deploys
days apart and punishes a lapse of memory with live errors. Seconds of downtime on
a rare deploy is the cheaper trade for one maintainer.

Migrations never run automatically at boot, which is why a bad one can never take the
site down on a restart loop. `migrate` uses its own upstream image, not the app
image, so nothing has to be pulled before it runs.

> **The timeout chain is not enforced by anything.** Web slots hold
> `serverWriteTimeout` 30s < `SHUTDOWN_TIMEOUT` 35s < `stop_grace_period` 45s;
> `jobs` holds 115s < 120s. Nothing fails a build or refuses a boot if you invert
> them — the symptom is work being SIGKILLed mid-flight, a request dropped during a
> swap or a GC batch thrown away, while every setting still looks deliberate. The
> values live in `deploy/compose.yaml` beside a comment saying so.

## 6.2 Watch these five

| Number | How |
|---|---|
| Free disk on `/` | `df -h /` and `docker system df` |
| Redis `used_memory` vs the 512 MB cap | `rcli info memory \| grep -E 'used_memory_human\|maxmemory_human'` |
| Redis `evicted_keys` | `rcli info stats \| grep evicted` |
| Database connections vs the plan limit | the provider's console — the one ceiling outside this box |
| The jobs process is still ticking | `psql "$DATABASE_URL" -Atc 'SELECT name, last_success_at, consecutive_failures FROM job_runs'` |

That last one is the whole liveness story for the `jobs` container. It has no HTTP
surface, so `/healthz` cannot see it and Caddy would not notice it dying; each pass
upserts its outcome instead. A `last_success_at` older than the job's interval, or
a climbing `consecutive_failures`, is the signal — `last_error` says why. Nothing
serves this and nothing pages on it; it is a `psql` line you run.

Logs:

```sh
slotc logs -f "$SLOT"
slotc logs --since 1h "$SLOT" | grep -E '"level":"(WARN|ERROR)"'
docker compose logs --since 1h jobs | grep -E '"level":"(WARN|ERROR)"'
```

Requests are in the slot's logs, ticks are in the jobs container's, and they never
overlap.

Redis at its cap is not an outage. `volatile-lru` evicts a limiter key, which grants
that subject a fresh allowance — the same outcome as fail-open. If the cap is somehow
reached with nothing volatile left to shed, writes get `OOM command not allowed when
used memory > 'maxmemory'`, the limiter treats it as any other Redis error and fails
open, and the edge rule still throttles floods. Rising `evicted_keys` at v1 traffic
means something is wrong with key TTLs, not that you need more memory.

## 6.3 Rotations

Every service reads the same `../.env`, so a rotated value reaches a container only
when that container is recreated. Recreating the live slot is a few seconds of
downtime — a rotation is not a deploy and does not get the overlap.

```sh
# Redis password — edit deploy/redis.conf and REDIS_URL in ../.env together, then
docker compose up -d redis
rcli ping                            # => PONG, with the new REDISCLI_AUTH
slotc up -d "$SLOT"                  # the web tier holds the pool

# Postgres password — change it provider-side, update DATABASE_URL in ../.env
slotc up -d "$SLOT"
docker compose up -d jobs            # jobs talks to Postgres too

# Cloudflare purge token — mint a new one, update CLOUDFLARE_API_TOKEN in ../.env
slotc up -d "$SLOT"                  # inline best-effort purge (ADR-0012)
docker compose up -d jobs            # the durable drain

# GHCR pull token — mint a new read:packages token (step 4.4), then
docker login ghcr.io -u Abir66       # no container restart; deploy.sh pulls next time

# Move to a different Postgres provider
#   migrate creates an EMPTY schema — it does not move data. Copy the data across
#   with the providers' own tooling and confirm it landed before cutting over.
docker compose run --rm migrate && slotc up -d "$SLOT" && docker compose up -d jobs
```

Redis is web-tier only — the `jobs` container opens no Redis connection, so a
password rotation never touches it. Postgres and Cloudflare credentials reach both.

Redis needs no such care on a move — the counters are disposable, and a fresh
keyspace grants everyone a new window (ADR-0013).

**Moving Redis off-box**, if it ever comes to that: take the provider's RESP/TCP
`rediss://` URL — not a REST endpoint, since the adapter speaks RESP over a pooled
connection — put it in `REDIS_URL`, drop the `redis` service, and `redis.conf` and
the volume stop mattering (the provider owns persistence and memory policy). Weigh
two things first: the per-request latency of leaving the box, and whether the free
tier's command quota covers your write volume at ~1 command per write.

**Origin CA certificate** renewal is a 15-year problem, so the procedure will need
re-learning: re-run step 3.3 and `docker compose restart caddy`. Nothing else
changes.

## 6.4 Host patching

```sh
sudo apt-get update && sudo apt-get -y upgrade && sudo reboot
```

The stack comes back on its own via `restart: unless-stopped`.

## 6.5 Idle reclaim

Oracle reclaims **idle Always Free compute** — roughly, an instance whose 95th
percentile CPU, network, and memory utilisation all sit under 20% across a 7-day
window.

Be clear-eyed: **the tickers do not clear that bar.** A purge drain every 15 minutes
and an hourly GC pass on a small keyspace are microseconds of CPU, and at v1 traffic
real requests will not either.

The reliable guard is to **upgrade the tenancy to Pay As You Go**
(**Billing → Upgrade and Manage Payment**). Always Free resources stay free under a
paid account — you are billed only if you provision beyond the Always Free
allowances — and paid tenancies are not subject to idle reclamation. Recommended;
treat the rest as fallback.

Staying on a pure Always Free account, monitor and be ready to rebuild:

- **Compute → Instances → your instance → Metrics** for CPU and network. Oracle
  emails a warning before reclaiming.
- A reclaimed instance is **stopped**, not deleted. The boot volume survives, so
  restarting from the console usually recovers it — `restart: unless-stopped`
  brings the containers back.
- If the instance is gone: re-run steps 1 and 4, registry login included. Nothing on
  the box is the only copy of anything — the database is off-box and every image CI
  has published is still in GHCR, so a rebuild is a `docker login` and a pull. The
  reserved IP is a separate resource and survives, so no DNS change is needed. Redis
  counters do not survive; accepted per ADR-0013.

## 6.6 Troubleshooting

**`502 Bad Gateway` from Caddy**

```sh
docker compose ps                 # is a web slot running at all?
slotc logs "$SLOT" --tail 50
docker compose exec caddy wget -qO- http://app_blue:8080/healthz
docker compose exec caddy wget -qO- http://app_green:8080/healthz
```

Caddy names **both** slots permanently and health-checks them every 5s, so a 502
means neither answered. One of the two hosts failing to resolve is normal — the
idle slot does not exist — and shows up as a DNS error in Caddy's log, not an
outage. `localhost` inside the Caddy container means Caddy itself.

**A web slot boots then exits**

Almost always config. The server requires `DATABASE_URL`, `BASE_URL`, all four
`R2_*`, and `STORAGE_BASE_URL`, and refuses to start without any one of them:

```sh
slotc logs "$SLOT" | tail -20
# "required env var not set: X"          → add it to ../.env
# "ping db: ..."                         → step 2.4, the DSN or the allowlist
# "parse redis url: ..."                 → malformed REDIS_URL (this one is fatal by design)
```

The `jobs` container fails the same way on the same file — check it separately with
`docker compose logs jobs`, because the site stays up while the tickers are dead.

**`deploy.sh` fails at the pull**

```
Error response from daemon: denied
```

The GHCR credential, not the image. Re-do the login in step 4.4; tokens expire.
`manifest unknown` instead means the login is fine and CI has not published that
commit — check Actions.

**`deploy.sh: both app_blue and app_green are running`**

A previous deploy was interrupted, or a bare `up` ran with the green profile
enabled. Caddy is serving whichever it prefers (blue), so there is no live slot to
swap away from. Stop the one that is *not* on the current tag —
the `docker ps` line in 5j says which — and retry.

**`/healthz` works from the VM but not through Cloudflare**

DNS or SSL mode, not the origin. Check the record is **proxied** (orange), and that
SSL/TLS is **Full (strict)** — "Flexible" would try plain HTTP to a port that is
closed.

**Everything 429s**

Check 5e. If the Redis keys show a `172.*` address, the client IP is not reaching the
app and every caller shares one bucket.

**Images 404 or the Raw toggle errors in the console**

R2. Check `STORAGE_BASE_URL` matches the custom domain, the custom domain is connected,
and the CORS rule from step 3.5 exists.

**Update serves stale content**

The purge path. `SELECT * FROM purge_queue` — a climbing `attempts` means Cloudflare
is rejecting; re-run the token check in step 3.9.

**Env var seems to be ignored**

```sh
docker compose config | grep -A2 "$SLOT:" | head
```

Compose reads `../.env` relative to the compose file. Output contains secrets — do
not paste it anywhere. A container only sees a changed `.env` after it is
recreated (6.3), so "the value is right in `config` and wrong in the process"
means you restarted nothing.

## 6.7 Command reference

Needs the shell setup at the top of this file for `$SLOT`, `slotc` and `rcli`.

```sh
./deploy.sh --dry-run                   # the path, the current tag, the target
./deploy.sh                             # deploy pulled HEAD
./deploy.sh <sha>                       # deploy a specific published tag
./deploy.sh rollback                    # back to the bookmarked tag (code-only)
./deploy.sh rollback --force            # ... even across a migration. Read 6.1 first

docker compose ps                       # what's running
slotc logs -f "$SLOT"                   # the serving web slot
docker compose logs -f jobs             # the tickers
slotc restart "$SLOT"                   # restart the serving slot (brief 502s)
docker compose restart jobs             # restart the tickers; the site is unaffected
docker compose run --rm migrate         # apply migrations by hand
docker compose down                     # stop everything, keep volumes
docker compose down -v                  # DELETES VOLUMES — never on this box
docker stats                            # live resource usage
rcli ping                               # Redis, on the private bridge network
psql "$DATABASE_URL" -Atc '\dt'         # tables on the managed instance
psql "$DATABASE_URL" -c 'SELECT * FROM job_runs'   # are the tickers alive
```

`docker compose up -d` with no arguments starts the default profile — Caddy,
Redis, `jobs` and **`app_blue`**. After a swap onto green that pulls traffic back
to blue's image, because Caddy prefers blue whenever it is healthy. Use
`deploy.sh`; reach for a bare `up -d` only on a box where nothing is running.
