# Deploying mdfly

Follow these in order. Each one needs something the previous one produced.

| | Step | What you get |
|---|---|---|
| 1 | [Oracle VM](1-oracle-vm.md) | An always-on ARM box with a fixed public IP, Docker, and the repo cloned |
| 2 | [Managed Postgres](2-managed-postgres.md) | A `DATABASE_URL` the VM can reach |
| 3 | [Cloudflare](3-cloudflare.md) | DNS, origin TLS cert, R2 bucket, purge token |
| 4 | [Deploy](4-deploy.md) | Secrets on the box, a registry login, migrations applied, stack running |
| 5 | [Verify](5-verify.md) | Proof each piece works, with a checklist |
| 6 | [Operate](6-operate.md) | Redeploys, rollback, troubleshooting, runbook |
| 7 | [Container registry](7-container-registry.md) | The private ARM image CI publishes, and the GitHub settings it needs |
| 8 | [Cutover](8-cutover.md) | The box on the pipeline, with the swap, the rollback and the drain each observed once |

Steps 1–6 build the box; step 7 is entirely GitHub-side. Do it **before step 4** —
that step pulls a published image and there is nothing to compile on the box. The
rate limiter's Redis is set up in step 4 and verified in step 5 like everything
else — ADR-0013 holds the reasoning behind it.

Step 8 is the one-time cutover: it is where the registry token is created, where a
box still running a locally built image is retired, and where the deploy paths are
rehearsed deliberately rather than met during an incident.

## What you are building

```
                    ┌─────────────── Cloudflare ───────────────┐
  browser / CLI ───▶│  proxy + cache + WAF + edge rate limit   │
                    └──────┬───────────────────────┬───────────┘
                           │ 443                   │
                           ▼                       ▼
                  ┌──────────────────────┐  storage.mdfly.dev
                  │  Oracle VM           │  (R2 bucket, direct)
                  │  ┌────────────────┐  │
                  │  │     Caddy      │  │  ← only published port
                  │  └───┬────────┬───┘  │
                  │ :8080│        │:8080 │
                  │  ┌───▼───┐ ┌──▼────┐ │
                  │  │app_   │ │app_   │ │──▶ managed Postgres
                  │  │blue   │ │green  │ │        (off-box)
                  │  └───┬───┘ └──┬────┘ │           ▲
                  │      │        │      │  ┌──────┐ │
                  │  ┌───▼────────▼───┐  │  │ jobs │─┘ ← tickers only,
                  │  │     Redis      │  │  └──────┘     no listener
                  │  └────────────────┘  │
                  └──────────────────────┘
```

Four long-lived containers on the VM — Caddy, one web slot, `jobs`, Redis — plus a
one-shot `migrate`. The web tier is **two fixed slots** and only one serves at a
time; a deploy starts the idle one and stops the live one (ADR-0015), so both are
up for a few seconds per deploy and never longer. `jobs` runs the same image under
a different subcommand, carries all the periodic work, and answers no HTTP at all
(ADR-0003) — its liveness is the `job_runs` table, not `/healthz`.

**Postgres is not on the box** — ADR-0005 fixes the `DATABASE_URL` contract and
deliberately leaves the host open, so the provider is swappable. Redis *is* on-box
(ADR-0013), reachable only on the Docker bridge network, and only by the web tier.

## Rules that hold everywhere

- **Only 443 is ever published, and only to Cloudflare's ranges.** The VCN ingress
  rule is what confines it — host iptables never sees Docker-published traffic.
  Redis has no `ports:` key; that omission, not a firewall, is what keeps it
  private.
- **One env file**: the repo-root `.env`. No `--env-file` flag on any command. Run
  `docker compose` from `~/mdfly/deploy` so it finds `compose.yaml`, or pass `-f`.
- **Never `docker compose down -v`** on the VM — it deletes the Redis volume.
- Secrets live only on the box: `.env`, `deploy/redis.conf`, `deploy/tls/*`. All
  gitignored; only their `.example` twins are tracked. `deploy/.env` is *not* a
  secret — `deploy.sh` generates it and it holds one image tag.
- **Nothing is compiled on the box.** CI publishes an image per commit and
  `deploy/deploy.sh` — tracked in this repo, not typed on the box — pulls it.
- **Rollback moves code only.** Schema deploys are fixed forward (ADR-0015).
