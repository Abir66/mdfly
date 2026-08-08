# 4. Deploy

Secrets on the box, a registry login, migrations applied, stack running.

**You need**: everything from steps 1–3, and one image published by CI (step 7).
**You produce**: a running backend.

Nothing is compiled on the box. CI publishes an ARM image per commit to GHCR and
`deploy/deploy.sh` pulls it (ADR-0015), so the box needs a registry credential
before its first `up`.

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
| `STORAGE_BASE_URL` | `https://storage.mdfly.dev` |
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
reason. They are read by the `jobs` container; the web slots register no tickers and
ignore them. `MDFLY_API` is a **CLI** variable and does not belong in this file.

**Do not put `SHUTDOWN_TIMEOUT` in `.env`.** It is set per service in
`deploy/compose.yaml` — 35s on each web slot, 115s on `jobs` — because the two
roles need different budgets, and a value here would override both.

> **The timeout chain is not enforced by anything.** Each web slot holds
> `serverWriteTimeout` 30s (a code constant) < `SHUTDOWN_TIMEOUT` 35s <
> `stop_grace_period` 45s; `jobs` holds 115s < 120s. No build fails and no
> container refuses to boot if you invert them. The symptom of a silent violation
> is work being SIGKILLed mid-flight — a dropped request, or a GC batch thrown
> away — while every setting still looks deliberate. If you change one of these
> numbers, change the ones above it in the same edit.

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

Before the first deploy the three application services render as
`image: mdfly-server:unset`. That is the deliberate fallback for
`${MDFLY_IMAGE}`: `deploy/.env` — a generated, secret-free file holding only the
tag — is written by `deploy.sh`, and a name that cannot exist makes an
un-deployed stack fail at the pull instead of starting something stale.

## 4.3 Migrations

Run these **before** the first deploy, for two reasons. The lifecycle-GC ticker
fires shortly after the `jobs` container boots and logs a loud
`relation "documents" does not exist` if the schema is not there yet — harmless,
but it buries the real first-boot output. More importantly, `deploy.sh` picks its
path by comparing `migrations/` against the applied version: with the schema
empty it takes the **schema path**, which recreates one web slot and assumes the
rest of the stack is already up. With migrations applied first, it takes the
**cold-start path** and brings up the whole default profile — Caddy included.

```sh
docker compose run --rm migrate
# => 1/u documents ...
#    2/u purge_queue ...
#    3/u job_runs ...
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
gets dropped. When you add `0004_*.up.sql` later, only `0004` runs and existing
rows keep their data.

If a migration fails halfway, the version is flagged **dirty** and the next run
refuses with `Dirty database version N. Fix and force version.` Recovery: fix the
SQL, `docker compose run --rm migrate force <N-1>`, then run `up` again.

## 4.4 Registry login

One-time, and the step everything after it depends on. The images are **private**
(ADR-0015), so `docker pull` fails with `denied` until the box holds a credential.

**Create the token** at <https://github.com/settings/tokens> → **Generate new
token (classic)**. Classic, not fine-grained: fine-grained tokens do not carry
package scopes, and GHCR still authenticates with classic PATs.

| Field | Value |
|---|---|
| Note | `mdfly-box-pull` |
| Expiration | your call — a token that expires is a deploy that fails at the pull |
| Scopes | **`read:packages` only** |

Nothing else. Not `write:packages`, not `repo`. The box only ever pulls; the
push and the prune are CI's job with its own workflow token (step 7). A
`read:packages` token that leaks lets someone read this image and nothing else.

**Log in on the box:**

```sh
read -rs CR_PAT                  # paste the token, press enter; it is not echoed
echo "$CR_PAT" | docker login ghcr.io -u Abir66 --password-stdin
unset CR_PAT
# => Login Succeeded
```

`read -rs` keeps the token out of your shell history and out of the process list,
which `docker login -p <token>` would not.

The credential is written to `~/.docker/config.json` (mode 0600) and **persists
across reboots** — this is not a per-session login and not something `deploy.sh`
redoes. You come back to it only when the token expires or is revoked, and the
symptom then is `deploy.sh` failing at `docker pull` with `denied` or
`unauthorized`.

Verify before continuing:

```sh
docker pull ghcr.io/abir66/mdfly:$(git -C ~/mdfly rev-parse HEAD)
```

A `manifest unknown` here means the login worked and CI has not published that
commit yet — check Actions. `denied` means the token or the scope is wrong.

## 4.5 First deploy

```sh
cd ~/mdfly/deploy
./deploy.sh --dry-run            # reads state, starts nothing, pulls nothing
./deploy.sh
docker compose ps
```

The dry run prints the path it would take. On a box where nothing is running and
4.3 is done, that is:

```
path:    cold start — no slot is serving
current: none
target:  <the HEAD sha>
serving: app_blue (was none)
```

Four containers running: `caddy`, `app_blue`, `jobs`, `redis`, and nothing else.
Only `caddy` shows a published port. `app_green` is the idle slot — it lives
behind a compose profile and stays absent until a deploy swaps onto it.

`restart: unless-stopped` on every service is what brings the stack back after a
reboot, and on the `jobs` container specifically it is what keeps the tickers
running (ADR-0003) — do not change it to `on-failure`.

```sh
docker compose logs app_blue
docker compose logs jobs
```

A healthy first boot — the web slot listens and ticks nothing:

```
{"level":"INFO","msg":"mdfly-server listening","addr":":8080"}
```

and the jobs container ticks and listens to nothing:

```
{"level":"INFO","msg":"mdfly-server jobs started","jobs":["lifecycle-gc","purge-drain"]}
```

Expected warnings, both harmless: `redis unreachable at boot, rate limiter will
fail open` from a web slot if Redis is still starting, and `cloudflare not
configured, cdn purges will stay queued` from either container if you left the
Cloudflare values empty — in which case `purge-drain` is absent from the `jobs`
list above, because a drain with no credentials would only fail.

Redis is a web-tier dependency. The `jobs` container opens no Redis connection and
registers no limiter, so `REDIS_URL` never affects it. On a **web slot**,
`REDIS_URL` has three boot behaviours worth knowing apart: **unset** logs `redis not
configured, write paths are unthrottled` and runs without the limiter (a supported
local-dev mode, never production); **malformed** fails the boot deliberately, rather
than degrading to unthrottled behind your back; **set but unreachable** warns as above
and starts anyway.

Next: [5. Verify](5-verify.md).
