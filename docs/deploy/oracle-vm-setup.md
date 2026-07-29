# Provisioning the backend: Oracle Always Free VM + Redis + Caddy

Owner-run setup for the mdfly backend origin (ADR-0003). Everything below happens
once, by hand, in the Oracle Cloud console and over SSH. Nothing here is
automated and nothing is idempotent — read a step before running it.

**What you end up with.** One always-on ARM VM running three long-lived
containers from `deploy/compose.yaml` — Caddy (the only service with a published
host port), the Go server, and Redis — plus a one-shot `migrate`. **Postgres is
not on this box.** Metadata lives in a managed instance elsewhere, reached only
through `DATABASE_URL`; ADR-0005 fixes that contract and deliberately leaves the
host open, so any provider that speaks Postgres works with no code or compose
change. Redis *is* on-box (ADR-0013) as a compose service with a named volume,
and 6379 is never opened — it stays on the Docker bridge network.

**Before you start**, have: a managed Postgres with its connection string, a
Cloudflare account with `mdfly.dev` in it (ADR-0011), an R2 bucket plus an
S3-compatible access key pair, and an SSH keypair. The
Cloudflare zone ID and purge API token come later, with the edge configuration —
see [cloudflare-purge-setup.md](cloudflare-purge-setup.md). The stack boots fine
without them.

---

## 1. The Oracle Always Free VM

### 1a. Account

Sign up at <https://cloud.oracle.com>. Pick a **home region** close to your users
and near-permanently: home region cannot be changed, and Always Free A1 capacity
varies a lot by region. Sign-up asks for a card for identity verification; Always
Free resources are not billed against it.

### 1b. Create the instance

**Compute → Instances → Create instance.**

| Field | Value |
|---|---|
| Image | Canonical Ubuntu 24.04 (**aarch64** build) |
| Shape | **VM.Standard.A1.Flex** — Ampere ARM |
| OCPUs / memory | 1 OCPU / 6 GB (inside the 4 OCPU / 24 GB Always Free allowance) |
| Boot volume | 50 GB (Always Free gives 200 GB total block storage) |
| SSH keys | paste your public key |

Confirm the shape row reads **"Always Free-eligible"** before creating. An A1
shape that is *not* marked Always Free bills by the hour.

**A1 capacity is scarce.** `Out of host capacity` on create is normal, not a
misconfiguration. Retry across the availability domains in your region (AD-1,
AD-2, AD-3), and retry over hours rather than seconds. Do not "fix" it by
switching to an E-series shape that happens to provision — those are a different
Always Free allowance (2× micro, x86, 1 GB RAM each) and 1 GB will not hold this
stack.

### 1c. Reserve the public IP

A default instance gets an **ephemeral** public IP that changes on stop/start.
The apex is a proxied A record to a fixed address (ADR-0011), so make it static:

**Instance → Attached VNICs → the primary VNIC → IPv4 Addresses → the public IP →
Edit → Reserved public IP → Reserve new.**

Note the address. Every DNS record for the origin points at it.

### 1d. Open 443, and only 443

Two independent firewalls stand between the internet and Caddy. Both must allow
443/TCP, and neither may allow anything else.

**VCN security list** — **Networking → Virtual cloud networks → your VCN →
Security Lists → Default Security List → Add Ingress Rule:**

| Field | Value |
|---|---|
| Source CIDR | `0.0.0.0/0` |
| IP Protocol | TCP |
| Destination Port Range | `443` |

Leave the pre-existing SSH (22) rule. Add **nothing** for 6379 or 8080.
Consider narrowing the SSH rule's source to your own IP.

**OS firewall.** Oracle's Ubuntu images ship iptables rules that drop inbound
traffic other than SSH, persisted by `netfilter-persistent`:

```sh
sudo iptables -I INPUT 6 -m state --state NEW -p tcp --dport 443 -j ACCEPT
sudo netfilter-persistent save
```

Check the rule landed *before* the catch-all REJECT:

```sh
sudo iptables -L INPUT -n --line-numbers
```

> **Docker publishes ports by writing its own iptables rules in the `DOCKER`
> chain, which is consulted before `INPUT`.** A container port under `ports:` is
> reachable from the internet whether or not the OS firewall allows it. This is
> why Redis has **no `ports:` key at all** — that omission, not the firewall, is
> what keeps it private. Never add `6379:6379` "to debug"; use
> `docker compose exec redis redis-cli` instead.

### 1e. Verify

```sh
ssh ubuntu@<reserved-ip>
uname -m          # => aarch64
nproc; free -h    # => 1 (or your OCPU count); ~6 GB
uptime            # after a day, confirms no scale-to-zero
```

In the console, the instance detail page should still show the shape as Always
Free-eligible and **Billing → Cost analysis** should show zero.

---

## 2. Host prep

```sh
sudo apt-get update && sudo apt-get -y upgrade
# postgresql-client is for talking to the managed Postgres from this box.
sudo apt-get -y install ca-certificates curl git gnupg postgresql-client-16

# Docker Engine + Compose v2 from Docker's own apt repo (arm64).
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
  -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=arm64 signed-by=/etc/apt/keyrings/docker.asc] \
https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list
sudo apt-get update
sudo apt-get -y install docker-ce docker-ce-cli containerd.io \
  docker-buildx-plugin docker-compose-plugin
sudo usermod -aG docker ubuntu   # log out and back in
```

Then clone the repo — the image is **built on the box**, so no registry and no
cross-architecture build are needed:

```sh
git clone https://github.com/Abir66/mdfly.git ~/mdfly
cd ~/mdfly/deploy
```

`docker compose` commands below are written from `~/mdfly/deploy`, where the
compose file lives. They work from anywhere — every path in the file is relative
to the file itself — but a consistent working directory is one less thing to think
about.

---

## 3. Origin TLS certificate

Caddy terminates TLS at the origin with a **Cloudflare Origin CA** cert — trusted
only by Cloudflare, valid 15 years, no renewal cron (ADR-0011). This is what
makes SSL/TLS mode **Full (strict)** possible.

In Cloudflare: **SSL/TLS → Origin Server → Create Certificate.** Private key type
RSA (2048), hostnames `mdfly.dev` **and** `*.mdfly.dev` (the wildcard covers
`api.`), validity 15 years. Copy both PEM blocks before closing the dialog — the
private key is shown exactly once.

On the VM:

```sh
mkdir -p ~/mdfly/deploy/tls
chmod 700 ~/mdfly/deploy/tls
nano ~/mdfly/deploy/tls/origin.crt   # paste the certificate
nano ~/mdfly/deploy/tls/origin.key   # paste the private key
chmod 600 ~/mdfly/deploy/tls/origin.key
```

`deploy/tls/` is gitignored. Store a copy of the key in your password manager —
losing it means re-issuing the cert.

Also set **SSL/TLS → Overview → Full (strict)** and **Edge Certificates → Minimum
TLS Version → 1.2**. Leave HSTS off in v1 (ADR-0011). The rest of the zone configuration
(Worker routes, cache rules, WAF, edge rate limit) is a separate step.

---

## 4. Secrets and configuration

Four files live only on the VM: `.env` at the repo root, `deploy/redis.conf`, and
the two under `deploy/tls/`.

```sh
cd ~/mdfly
cp .env.example .env
cp deploy/redis.conf.example deploy/redis.conf
openssl rand -base64 32   # the Redis password
chmod 600 .env deploy/redis.conf
```

There is **one** env file, not a prod/dev pair: the compose services read it as
`env_file: ../.env` and a local `go run` sources the same file. Nothing in
`deploy/compose.yaml` uses `${…}` interpolation, so no `docker compose` command
needs an `--env-file` flag and the working directory never matters.

Edit `.env`. Every variable is documented in the file itself, which ships with the
production shape already in place (a commented local-dev block sits at the
bottom). The ones that must change:

| Variable | Value |
|---|---|
| `DATABASE_URL` | your provider's connection string, verbatim, **with `sslmode=require`** |
| `REDIS_URL` | `redis://:<generated secret>@redis:6379` |
| `BASE_URL` | `https://mdfly.dev` |
| `R2_ENDPOINT` | `https://<account-id>.r2.cloudflarestorage.com` |
| `R2_ACCESS_KEY_ID` / `R2_SECRET_ACCESS_KEY` | R2 API token pair, **Object Read & Write** on the `mdfly` bucket only |
| `R2_BUCKET` | `mdfly` |
| `CDN_BASE_URL` | `https://cdn.mdfly.dev` |
| `CLOUDFLARE_ZONE_ID` / `CLOUDFLARE_API_TOKEN` | leave empty until the edge configuration step |

`DATABASE_URL` is the only thing tying this box to the database, so paste the
provider's string whole, extra parameters included. `sslmode=require` is not
optional: unlike Redis, this connection leaves the machine. Check it before going
further:

```sh
psql "$DATABASE_URL" -Atc "select version();"
```

Three things to settle provider-side first:

- **Allowlist the VM's reserved IP** if the provider firewalls by source address.
  A hang rather than an error is almost always this.
- **Use a dedicated role** owning only the mdfly database, not an admin
  superuser. Migrations need DDL there and nothing beyond it.
- **Pick the endpoint** where more than one is offered: a pooled host suits the
  app's own pool, a direct host is safer for `migrate`'s DDL. If they differ, run
  `migrate` against the direct URL and leave `DATABASE_URL` pooled.

Then edit `deploy/redis.conf` and replace `requirepass CHANGE_ME_LONG_RANDOM` with
the generated secret. `requirepass` cannot read an environment variable, so it is
literal in that file — which is why the file is gitignored and only the
`.example` is tracked. Full rationale for every other line in that file:
[redis-rate-limit-setup.md](redis-rate-limit-setup.md).

`MDFLY_API` is a **CLI** variable, not a server one — it does not belong in this
file.

**Job intervals and grace windows** (`PURGE_DRAIN_INTERVAL`,
`LIFECYCLE_GC_INTERVAL`, `ABANDON_GRACE`, `BLOB_DELETE_GRACE`) carry their code
defaults in `.env.example`. Leave them unless you have a reason; setting one
overrides the default.

---

## 5. Migrations, then boot

Run migrations **before** the first `up`. The lifecycle-GC ticker fires shortly
after boot and logs a loud `relation "documents" does not exist` if the schema
isn't there yet — harmless, but it makes the first log unreadable.

```sh
cd ~/mdfly/deploy
docker compose run --rm migrate     # applies against DATABASE_URL, exits
docker compose up -d --build        # builds the image on the box, ~1 min
docker compose ps
```

`migrate` sits behind a `tools` profile so `up` never runs it. Its DSN is expanded
by the container's own shell from `env_file`, not by Compose, so a `DATABASE_URL`
already exported in your host shell cannot silently win. If the provider gave you
a separate direct (non-pooled) endpoint for DDL, use it here without touching
`.env`:

```sh
docker compose run --rm -e DATABASE_URL='<direct-url>' migrate
```

Verify the lifecycle and purge-queue schema landed — these run from the host
against the managed instance, so `psql` needs no container:

```sh
psql "$DATABASE_URL" -Atc \
  "SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname LIKE '%status%';"
# => CHECK ((status = ANY (ARRAY['pending','published','deleted','expired','abandoned'])))

psql "$DATABASE_URL" -Atc \
  "SELECT column_name FROM information_schema.columns
   WHERE table_name='documents' AND column_name='blobs_deleted_at';"
# => blobs_deleted_at

psql "$DATABASE_URL" -Atc "\dt"
# => documents, purge_queue, schema_migrations
```

`restart: unless-stopped` on every service is what keeps the in-process tickers
alive across reboots (ADR-0003). Do not change it to `on-failure`.

---

## 6. DNS

The full zone setup is a separate step, but the origin is not reachable until
the apex and `api.` records exist. Minimum to finish here — **DNS → Records:**

| Type | Name | Content | Proxy |
|---|---|---|---|
| A | `@` | reserved public IP | Proxied (orange) |
| A | `api` | reserved public IP | Proxied (orange) |

Both proxied: the origin IP must never be in public DNS (ADR-0011).

---

## 7. Verification

Each block maps to one acceptance criterion. Run them in order.

### 7a. Healthy through Cloudflare

```sh
curl -s -o /dev/null -w '%{http_code}\n' https://mdfly.dev/healthz          # => 200
curl -s -o /dev/null -w '%{http_code}\n' https://api.mdfly.dev/healthz      # => 200
```

From the VM itself, bypassing Cloudflare, to isolate origin from edge:

```sh
curl -sk --resolve mdfly.dev:443:127.0.0.1 -o /dev/null -w '%{http_code}\n' \
  https://mdfly.dev/healthz                                                 # => 200
```

If the first two fail and the third passes, the problem is DNS or SSL mode, not
the origin.

### 7b. Tickers logging

```sh
docker compose logs app | grep -E 'lifecycle gc pass|purge drain'
```

Expect `lifecycle gc pass` within `LIFECYCLE_GC_INTERVAL` of boot. **`purge-drain`
is absent until Cloudflare is configured** — with no credentials every pass
would fail, so
the job is left unregistered and you get
`cloudflare not configured, cdn purges will stay queued` at boot instead. Queued
rows are durable meanwhile. To see the drain sooner than an hour, temporarily set
`LIFECYCLE_GC_INTERVAL=30s` and `docker compose up -d app`.

### 7c. Rate limiting returns 429

```sh
for i in $(seq 1 11); do
  curl -s -o /dev/null -w '%{http_code} ' -X POST \
    https://api.mdfly.dev/v1/publish/init -H 'Content-Type: application/json' -d '{}'
done; echo
# => ten 400 (empty bundle; the limiter runs before validation) then 429
```

On the 429, headers should carry `X-RateLimit-Limit: 10`,
`X-RateLimit-Remaining: 0`, and a `Retry-After` in seconds.

### 7d. The limiter keys on the real client IP

This is the one that silently goes wrong. Caddy is the app's TCP peer, so if the
client IP is not carried through correctly **every anonymous caller shares one
rate-limit subject** and one abuser throttles everyone.

```sh
docker compose exec redis redis-cli -a "$REDIS_PASS" --scan --pattern 'rl:*'
```

After 7c you want `rl:ip:<your own public IP>:1m:<step>` — **not** a `172.*` or
`192.168.*` address. Every key must have a TTL (`ttl <key>` returns a positive
number, never `-1`); a key outside the `volatile-lru` eviction pool would be a
bug.

The chain: Cloudflare sets `CF-Connecting-IP`; Caddy strips that header when its
own peer is outside Cloudflare's published ranges (`deploy/Caddyfile`); the
backend trusts the header from a loopback or private peer, i.e. from Caddy
(`internal/server/middleware/clientip.go`). Confirm the strip works — a spoofed
header sent straight at the origin IP must be ignored:

```sh
curl -sk --resolve api.mdfly.dev:443:127.0.0.1 -X POST \
  https://api.mdfly.dev/v1/publish/init -H 'CF-Connecting-IP: 9.9.9.9' \
  -H 'Content-Type: application/json' -d '{}' -o /dev/null
docker compose exec redis redis-cli -a "$REDIS_PASS" --scan --pattern 'rl:ip:9.9.9.9*'
# => empty. If 9.9.9.9 appears, the Caddyfile's CF range list is stale.
```

Both IP lists — the Caddyfile's `@direct` matcher and `cloudflareRanges` in
`clientip.go` — come from <https://www.cloudflare.com/ips/> and must be updated
together.

### 7e. Redis persistence survives a recreate

A `docker compose restart` does **not** test this: the container keeps its
filesystem, so a missing named volume still looks durable. Use `down`/`up`:

```sh
docker compose exec redis redis-cli -a "$REDIS_PASS" set probe:persist ok
docker compose down && docker compose up -d
docker compose exec redis redis-cli -a "$REDIS_PASS" get probe:persist   # => "ok"
docker compose exec redis redis-cli -a "$REDIS_PASS" del probe:persist
```

`(nil)` means `redis-data:/data` is not attached. Fix that before believing
anything is durable. Use a key with **no TTL** — a TTL'd key is eviction-eligible
and proves less. The database is unaffected by this probe: it lives off-box, so
`psql "$DATABASE_URL" -Atc "\dt"` should list the same tables before and after.

`docker compose down -v` deletes the volumes. Never run it on this box.

### 7f. Redis is not internet-reachable

From your laptop:

```sh
nc -zv -w 3 <reserved-ip> 6379    # must time out or refuse
nc -zv -w 3 <reserved-ip> 8080    # must time out or refuse
nc -zv -w 3 <reserved-ip> 443     # must connect
```

And confirm no mapping exists at all:

```sh
docker compose ps --format '{{.Service}}\t{{.Ports}}'
# only caddy may show 0.0.0.0:443->443/tcp
```

### 7g. Origin TLS is Full (strict)

```sh
openssl s_client -connect <reserved-ip>:443 -servername mdfly.dev </dev/null 2>/dev/null \
  | openssl x509 -noout -issuer -subject -dates
# issuer => CloudFlare Origin SSL Certificate Authority; notAfter ~15 years out
```

A browser hitting the IP directly should show a certificate error — that is the
Origin CA working as intended. In Cloudflare, **SSL/TLS → Overview** must read
**Full (strict)**; "Flexible" sends plaintext to the origin and "Full" accepts
self-signed, so neither is acceptable.

### 7h. Fail-open

```sh
docker compose stop redis
curl -s -o /dev/null -w '%{http_code}\n' -X POST https://api.mdfly.dev/v1/publish/init \
  -H 'Content-Type: application/json' -d '{}'     # => 400, not 429 and not 5xx
docker compose logs app --tail 5 | grep 'rate limiter unavailable'
docker compose start redis
```

Writes must keep working with Redis down (ADR-0013) — the Cloudflare edge limit
(L1) still stands.

---

## 8. Backups

**The provider owns the primary backup.** Turn it on and note its retention
before going live — that is the restore path for "a migration went wrong an hour
ago".

What it does not cover is losing access to the provider itself. So keep a
provider-independent logical dump too, run from the VM against `DATABASE_URL` into
a **second, private** R2 bucket (`mdfly-backups`) — never the public `mdfly`
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

Put `DATABASE_URL`, `BACKUP_KEY_ID`, `BACKUP_SECRET`, and `BACKUP_ENDPOINT` in a
`chmod 600` file sourced by the cron entry — cron gets almost no environment, so
an unset `DATABASE_URL` is the usual reason a backup silently stops running:

```sh
crontab -e
# 17 3 * * * . $HOME/.backup-env && $HOME/bin/pg-backup.sh >> $HOME/backups/cron.log 2>&1
```

**Test the restore, not the dump.** An untested backup is a guess. Restore into a
scratch database on the provider (or a throwaway local container), never over the
live one:

```sh
gunzip -c ~/backups/mdfly-<stamp>.sql.gz | psql "<scratch-database-url>"
```

Redis needs no backup — the counters are disposable and a fresh keyspace just
grants everyone a new window (ADR-0013).

---

## 9. Idle reclaim

Oracle reclaims **idle Always Free compute**: roughly, an instance whose 95th
percentile CPU, network, and memory utilisation all sit under 20% across a
7-day window becomes eligible for reclamation.

Be clear-eyed about this: **the tickers do not clear that bar.** A purge drain
every 15 minutes and an hourly GC pass on an idle keyspace are microseconds of
CPU. At v1 traffic, real requests will not either.

The reliable guard is to **upgrade the tenancy to Pay As You Go**
(**Billing → Upgrade and Manage Payment**). Always Free resources stay free
under a paid account — you are billed only if you provision beyond the Always
Free allowances — and paid tenancies are not subject to idle reclamation. This
is the recommended action; treat everything below as fallback.

If you stay on a pure Always Free account, monitor and be ready to rebuild:

- **Compute → Instances → your instance → Metrics** — watch CPU and network
  utilisation. Oracle emails a reclamation warning before acting.
- A reclaimed instance is **stopped**, not deleted; the boot volume survives and
  restarting usually recovers it. Restart from the console, then
  `docker compose up -d` (it should already be up via `restart: unless-stopped`).
- If the instance is gone: re-run §1–§5, then restore from §8. The reserved IP
  is a separate resource and survives, so no DNS change is needed. Redis counters
  do not survive — accepted per ADR-0013.

---

## 10. Runbook

```sh
cd ~/mdfly/deploy

# Redeploy after a code change
git -C ~/mdfly pull
docker compose run --rm migrate       # only if migrations changed
docker compose up -d --build

# Logs
docker compose logs -f app
docker compose logs --since 1h app | grep -E '"level":"(WARN|ERROR)"'

# Restart one service
docker compose restart app

# Rotate the Redis password: edit deploy/redis.conf and REDIS_URL in .env
# together, then
docker compose up -d redis app

# Rotate the Postgres password: change it in the provider's console (or
# `ALTER ROLE mdfly PASSWORD …` over psql), update DATABASE_URL in ../.env, then
docker compose up -d app

# Move to a different Postgres provider: point DATABASE_URL at the new instance,
# apply the schema there, then
docker compose run --rm migrate && docker compose up -d app

# Disk and memory
df -h /; free -h; docker system df
docker system prune -f                # reclaims old build layers, not volumes

# Host patching
sudo apt-get update && sudo apt-get -y upgrade && sudo reboot
```

Watch four numbers over time: free disk on `/`, Redis `used_memory` against the
512 MB cap, `evicted_keys`, and — in the provider's console — the database's
connection count against its plan limit, which is the one ceiling that lives
outside this box.

```sh
docker compose exec redis redis-cli -a "$REDIS_PASS" info memory | \
  grep -E 'used_memory_human|maxmemory_human'
```

**Renewing the Origin CA certificate** is a 15-year problem, so it will need
re-learning: re-run §3 and `docker compose restart caddy`. Nothing else changes.
