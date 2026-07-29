# Deploying mdfly

Follow these in order. Each one needs something the previous one produced.

| | Step | What you get |
|---|---|---|
| 1 | [Oracle VM](1-oracle-vm.md) | An always-on ARM box with a fixed public IP, Docker, and the repo cloned |
| 2 | [Managed Postgres](2-managed-postgres.md) | A `DATABASE_URL` the VM can reach |
| 3 | [Cloudflare](3-cloudflare.md) | DNS, origin TLS cert, R2 bucket, purge token |
| 4 | [Deploy](4-deploy.md) | Secrets on the box, migrations applied, stack running |
| 5 | [Verify](5-verify.md) | Proof each piece works, with a checklist |
| 6 | [Operate](6-operate.md) | Redeploys, troubleshooting, runbook |

Six files, nothing else. The rate limiter's Redis is set up in step 4 and verified in
step 5 like everything else — ADR-0013 holds the reasoning behind it.

## What you are building

```
                    ┌─────────────── Cloudflare ───────────────┐
  browser / CLI ───▶│  proxy + cache + WAF + edge rate limit   │
                    └──────┬───────────────────────┬───────────┘
                           │ 443                   │
                           ▼                       ▼
                  ┌────────────────┐        cdn.mdfly.dev
                  │  Oracle VM     │        (R2 bucket, direct)
                  │  ┌──────────┐  │
                  │  │  Caddy   │  │  ← only published port
                  │  └────┬─────┘  │
                  │       │ :8080  │
                  │  ┌────▼─────┐  │
                  │  │   app    │──┼──▶ managed Postgres (off-box)
                  │  └────┬─────┘  │
                  │       │        │
                  │  ┌────▼─────┐  │
                  │  │  Redis   │  │  ← rate-limit counters, named volume
                  │  └──────────┘  │
                  └────────────────┘
```

Three long-lived containers on the VM, plus a one-shot `migrate`. **Postgres is
not on the box** — ADR-0005 fixes the `DATABASE_URL` contract and deliberately
leaves the host open, so the provider is swappable. Redis *is* on-box (ADR-0013),
reachable only on the Docker bridge network.

## Rules that hold everywhere

- **Only 443 is ever published, and only to Cloudflare's ranges.** The VCN ingress
  rule is what confines it — host iptables never sees Docker-published traffic.
  Redis has no `ports:` key; that omission, not a firewall, is what keeps it
  private.
- **One env file**: the repo-root `.env`. No `--env-file` flag on any command. Run
  `docker compose` from `~/mdfly/deploy` so it finds `compose.yaml`, or pass `-f`.
- **Never `docker compose down -v`** on the VM — it deletes the Redis volume.
- Secrets live only on the box: `.env`, `deploy/redis.conf`, `deploy/tls/*`. All
  gitignored; only their `.example` twins are tracked.
