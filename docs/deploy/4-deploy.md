# 4. Deploy

Secrets on the box, migrations applied, stack running.

**You need**: everything from steps 1–3. **You produce**: a running backend.

## 4.1 Secrets

Three files live only on the VM: `.env` at the repo root, `deploy/redis.conf`, and
the certificate pair from step 3.3.

```sh
cd ~/mdfly
cp .env.example .env
cp deploy/redis.conf.example deploy/redis.conf
chmod 600 .env deploy/redis.conf
openssl rand -hex 32             # the Redis password — hex, so it is URL-safe
```

Put that password in **two** places — they must match:

- literally after `requirepass` in `deploy/redis.conf` (Redis config cannot read an
  environment variable)
- inside `REDIS_URL` in `.env`

Then fill in `.env`:

| Variable | Value |
|---|---|
| `DATABASE_URL` | the verified string from step 2.4 |
| `REDIS_URL` | `redis://:<the password>@redis:6379` |
| `BASE_URL` | `https://mdfly.dev` |
| `R2_ENDPOINT` | from step 3.5 |
| `R2_ACCESS_KEY_ID` / `R2_SECRET_ACCESS_KEY` | from step 3.5 |
| `R2_BUCKET` | `mdfly` |
| `CDN_BASE_URL` | `https://cdn.mdfly.dev` |
| `CLOUDFLARE_ZONE_ID` / `CLOUDFLARE_API_TOKEN` | from steps 3.1 and 3.9 |

`redis` is a compose service name, resolved by Docker's embedded DNS — not
`localhost`, which inside a container means that container.

`redis.conf` needs no tuning beyond the password. Its four settings, for when you
wonder later why they are what they are:

| Setting | Why |
|---|---|
| `bind 0.0.0.0` | All interfaces *this container has* — not the internet, since no host port is published |
| `maxmemory 512mb` | A ceiling on the **dataset**, not the process — Redis exceeds it through fragmentation and non-evictable buffers, so leave headroom rather than treating it as OOM protection. Roughly 25× what the limiter needs (~20 MB at 100k subjects/hour) |
| `maxmemory-policy volatile-lru` | Evict only TTL-bearing keys. Every limiter key has one, so pressure sheds counters and an evicted counter just grants a fresh allowance. `allkeys-lru` would shed durable data instead |
| `appendonly yes` + `appendfsync everysec` | A crash loses at most one second of counting |

Window sizes, limits, pool size, and timeouts are code constants in
`internal/server/ratelimit` and `internal/server/redis` — not config.

Leave the job intervals (`PURGE_DRAIN_INTERVAL`, `LIFECYCLE_GC_INTERVAL`,
`ABANDON_GRACE`, `BLOB_DELETE_GRACE`) at their commented defaults unless you have a
reason. `MDFLY_API` is a **CLI** variable and does not belong in this file.

There is one env file, not a prod/dev pair. The compose services read it as
`env_file: ../.env`, and nothing in `deploy/compose.yaml` uses `${…}` interpolation —
so no command needs an `--env-file` flag, and a `DATABASE_URL` exported in your shell
cannot leak into the containers. You still have to run `docker compose` from
`~/mdfly/deploy` (or pass `-f ~/mdfly/deploy/compose.yaml`) so Compose can find the
file; what no longer matters is which directory the *values* are resolved from.

## 4.2 Pre-flight

```sh
cd ~/mdfly/deploy
docker compose config >/dev/null && echo OK
```

A missing `.env` fails here with `env file ... not found` rather than at boot. The
full `docker compose config` output contains your secrets — do not paste it
anywhere.

## 4.3 Migrations

Run these **before** the first `up`. The lifecycle-GC ticker fires shortly after
boot and logs a loud `relation "documents" does not exist` if the schema is not
there yet — harmless, but it buries the real first-boot output.

```sh
docker compose run --rm migrate
# => 1/u documents ...
#    2/u purge_queue ...
```

If your provider gave you a separate direct (non-pooled) endpoint for DDL, use it
here without touching `.env`:

```sh
docker compose run --rm -e DATABASE_URL='<direct-url>' migrate
```

`migrate` sits behind a `tools` profile, so `docker compose up` never runs it. Its
DSN is expanded by the container's own shell from `env_file`, not by Compose, so a
`DATABASE_URL` exported in your host shell cannot silently win and migrate the
wrong database.

**How migrations behave**, since this is the part that makes people nervous:
`migrate` keeps a `schema_migrations` table holding one number — the highest
version applied. `up` runs only files above that number, in order, then updates it.
Running it twice prints `no change`. It never reads `.down.sql` files, so nothing
gets dropped. When you add `0003_*.up.sql` later, only `0003` runs and existing
rows keep their data.

If a migration fails halfway, the version is flagged **dirty** and the next run
refuses with `Dirty database version N. Fix and force version.` Recovery: fix the
SQL, `docker compose run --rm migrate force <N-1>`, then run `up` again.

## 4.4 Start

```sh
docker compose up -d --build     # builds the ARM image on the box, ~1 min
docker compose ps
```

Three services running: `caddy`, `app`, `redis`. Only `caddy` shows a published
port.

The image is built here rather than pulled, so there is no registry and no
cross-architecture build. `restart: unless-stopped` on every service is what keeps
the in-process tickers alive across a reboot (ADR-0003) — do not change it to
`on-failure`.

```sh
docker compose logs app
```

A healthy first boot:

```
{"level":"INFO","msg":"mdfly-server listening","addr":":8080"}
```

Expected warnings, both harmless: `redis unreachable at boot, rate limiter will
fail open` if Redis is still starting, and `cloudflare not configured, cdn purges
will stay queued` if you left the Cloudflare values empty.

`REDIS_URL` has three boot behaviours worth knowing apart: **unset** logs `redis not
configured, write paths are unthrottled` and runs without the limiter (a supported
local-dev mode, never production); **malformed** fails the boot deliberately, rather
than degrading to unthrottled behind your back; **set but unreachable** warns as above
and starts anyway.

Next: [5. Verify](5-verify.md).
