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
```

## 6.1 Redeploy

```sh
git -C ~/mdfly pull --ff-only
docker compose run --rm migrate      # only if migrations/ changed
docker compose up -d --build
docker compose ps
docker image prune -f                # reclaims the previous build's layers
```

Migrations first, then the new image — in that order. Migrations are written to be
readable by the old code as well as the new, so the schema can move ahead of the
containers; the reverse would run new code against an old schema. `migrate` uses its
own upstream image, not the app image, so nothing has to be built before it runs.

Migrations never run automatically at boot, which is why a bad one can never take the
site down on a restart loop.

Optional, if you do this often:

```sh
cat > ~/mdfly/deploy.sh <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
cd ~/mdfly
git pull --ff-only
cd deploy
docker compose run --rm migrate
docker compose up -d --build
docker compose ps
docker image prune -f
EOF
chmod +x ~/mdfly/deploy.sh
```

`deploy.sh` is gitignored territory — keep it on the box or add it to the repo
deliberately.

## 6.2 Watch these four

| Number | How |
|---|---|
| Free disk on `/` | `df -h /` and `docker system df` |
| Redis `used_memory` vs the 512 MB cap | `rcli info memory \| grep -E 'used_memory_human\|maxmemory_human'` |
| Redis `evicted_keys` | `rcli info stats \| grep evicted` |
| Database connections vs the plan limit | the provider's console — the one ceiling outside this box |

Logs:

```sh
docker compose logs -f app
docker compose logs --since 1h app | grep -E '"level":"(WARN|ERROR)"'
```

## 6.3 Rotations

```sh
# Redis password — edit deploy/redis.conf and REDIS_URL in ../.env together, then
docker compose up -d redis
rcli ping                            # => PONG, with the new REDISCLI_AUTH
docker compose up -d app

# Postgres password — change it provider-side, update DATABASE_URL in ../.env
docker compose up -d app

# Cloudflare purge token — mint a new one, update CLOUDFLARE_API_TOKEN in ../.env
docker compose up -d app

# Move to a different Postgres provider
#   migrate creates an EMPTY schema — it does not move data. Copy the data across
#   with the providers' own tooling and confirm it landed before cutting over.
docker compose run --rm migrate && docker compose up -d app
```

Redis needs no such care on a move — the counters are disposable, and a fresh
keyspace grants everyone a new window (ADR-0013).

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
- If the instance is gone: re-run steps 1 and 4. Nothing on the box is the only copy
  of anything — the database is off-box and the image rebuilds from the repo. The
  reserved IP is a separate resource and survives, so no DNS change is needed. Redis
  counters do not survive; accepted per ADR-0013.

## 6.6 Troubleshooting

**`502 Bad Gateway` from Caddy**

```sh
docker compose ps                 # is app running?
docker compose logs app --tail 50
docker compose exec caddy wget -qO- http://app:8080/healthz
```

Caddy proxies to `app:8080` — the compose service name. `localhost` inside the Caddy
container means Caddy itself.

**App container boots then exits**

Almost always config. The server requires `DATABASE_URL`, `BASE_URL`, all four
`R2_*`, and `CDN_BASE_URL`, and refuses to start without any one of them:

```sh
docker compose logs app | tail -20
# "required env var not set: X"          → add it to ../.env
# "ping db: ..."                         → step 2.4, the DSN or the allowlist
# "parse redis url: ..."                 → malformed REDIS_URL (this one is fatal by design)
```

**`/healthz` works from the VM but not through Cloudflare**

DNS or SSL mode, not the origin. Check the record is **proxied** (orange), and that
SSL/TLS is **Full (strict)** — "Flexible" would try plain HTTP to a port that is
closed.

**Everything 429s**

Check 5e. If the Redis keys show a `172.*` address, the client IP is not reaching the
app and every caller shares one bucket.

**Images 404 or the Raw toggle errors in the console**

R2. Check `CDN_BASE_URL` matches the custom domain, the custom domain is connected,
and the CORS rule from step 3.5 exists.

**Update serves stale content**

The purge path. `SELECT * FROM purge_queue` — a climbing `attempts` means Cloudflare
is rejecting; re-run the token check in step 3.9.

**Env var seems to be ignored**

```sh
docker compose config | grep -A2 'app:' | head
```

Compose reads `../.env` relative to the compose file. Output contains secrets — do
not paste it anywhere.

## 6.7 Command reference

```sh
docker compose ps                       # what's running
docker compose restart app              # restart one service
docker compose down                     # stop everything, keep volumes
docker compose down -v                  # DELETES VOLUMES — never on this box
docker stats                            # live resource usage
rcli ping                               # needs the shell setup at the top of this file
psql "$DATABASE_URL" -Atc '\dt'         # tables on the managed instance
```
