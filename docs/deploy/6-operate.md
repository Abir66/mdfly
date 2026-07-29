# 6. Operate

Redeploys, backups, troubleshooting, and the things worth watching.

All commands run from `~/mdfly/deploy`.

## 6.1 Redeploy

```sh
git -C ~/mdfly pull --ff-only
docker compose run --rm migrate      # only if migrations/ changed
docker compose up -d --build
docker compose ps
docker image prune -f                # reclaims the previous build's layers
```

Build the image, apply migrations as a one-off, then restart — in that order.
Migrations never run automatically at boot, which is why a bad one can never take
the site down on a restart loop.

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

## 6.2 Backups

The provider owns the primary backup (step 2.1). What it does not cover is losing
access to the provider itself, so keep a provider-independent logical dump.

Create a **second, private** R2 bucket — `mdfly-backups`, never the public `mdfly`
bucket, never attached to a custom domain, with its own scoped token.

```sh
sudo apt-get -y install awscli
mkdir -p ~/backups ~/bin

cat > ~/bin/pg-backup.sh <<'EOF'
#!/bin/sh
set -eu
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
OUT="$HOME/backups/mdfly-$STAMP.sql.gz"
pg_dump "$DATABASE_URL" | gzip > "$OUT"
AWS_ACCESS_KEY_ID=$BACKUP_KEY_ID AWS_SECRET_ACCESS_KEY=$BACKUP_SECRET \
  aws s3 cp "$OUT" "s3://mdfly-backups/" --endpoint-url "$BACKUP_ENDPOINT"
find "$HOME/backups" -name 'mdfly-*.sql.gz' -mtime +7 -delete
EOF
chmod +x ~/bin/pg-backup.sh
```

Put `DATABASE_URL`, `BACKUP_KEY_ID`, `BACKUP_SECRET`, and `BACKUP_ENDPOINT` in
`~/.backup-env` with mode 600, then:

```sh
crontab -e
# 17 3 * * * . $HOME/.backup-env && $HOME/bin/pg-backup.sh >> $HOME/backups/cron.log 2>&1
```

Cron gets almost no environment — an unset `DATABASE_URL` is the usual reason a
backup silently stops running, so check `cron.log` after the first night.

**Test the restore, not the dump.** An untested backup is a guess. Restore into a
scratch database, never over the live one:

```sh
gunzip -c ~/backups/mdfly-<stamp>.sql.gz | psql "<scratch-database-url>"
```

Redis needs no backup — the counters are disposable, and a fresh keyspace just
grants everyone a new window (ADR-0013).

## 6.3 Watch these four

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

## 6.4 Rotations

```sh
# Redis password — edit deploy/redis.conf and REDIS_URL in ../.env together
docker compose up -d redis app

# Postgres password — change it provider-side, update DATABASE_URL in ../.env
docker compose up -d app

# Cloudflare purge token — mint a new one, update CLOUDFLARE_API_TOKEN in ../.env
docker compose up -d app

# Move to a different Postgres provider
#   point DATABASE_URL at the new instance, then
docker compose run --rm migrate && docker compose up -d app
```

**Origin CA certificate** renewal is a 15-year problem, so the procedure will need
re-learning: re-run step 3.3 and `docker compose restart caddy`. Nothing else
changes.

## 6.5 Host patching

```sh
sudo apt-get update && sudo apt-get -y upgrade && sudo reboot
```

The stack comes back on its own via `restart: unless-stopped`.

## 6.6 Idle reclaim

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
- If the instance is gone: re-run steps 1 and 4, then restore from 6.2. The reserved
  IP is a separate resource and survives, so no DNS change is needed. Redis counters
  do not survive; accepted per ADR-0013.

## 6.7 Troubleshooting

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

## 6.8 Command reference

```sh
docker compose ps                       # what's running
docker compose restart app              # restart one service
docker compose down                     # stop everything, keep volumes
docker compose down -v                  # DELETES VOLUMES — never on this box
docker stats                            # live resource usage
docker compose exec redis redis-cli -a "$PASS" --no-auth-warning ping
psql "$DATABASE_URL" -Atc '\dt'         # tables on the managed instance
```
