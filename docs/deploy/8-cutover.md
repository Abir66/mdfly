# 8. Cutover

Move the box from "builds its own code" to the pipeline, then prove each property
the pipeline claims.

**You need**: steps 1–7 done, and at least one image published by CI.
**You produce**: a box that pulls, swaps, and rolls back — with each of those
observed once, calmly, rather than learned during an incident.

> **The pipeline must be on `main` before any of this works.** The `publish` job
> is gated on `push` to `main` (step 7), so until the blue/green slots, the
> `Dockerfile` and `deploy/deploy.sh` are merged, **no image exists** and §8.2's
> pull fails with `manifest unknown` no matter how good the token is. Confirm
> before starting:
>
> ```sh
> git ls-tree origin/main deploy/deploy.sh          # must print a line
> git show origin/main:deploy/compose.yaml | grep -c app_blue   # must be > 0
> ```
>
> If either comes back empty, merge that work first, wait for **Actions → CI →
> Publish image** to go green, and confirm the version on the package's
> **Versions** tab. Only then continue.

Every command here runs as you, on the box, from `~/mdfly/deploy` unless it says
otherwise. Two commands are run in a **second terminal** and say so.

This page is also the test of steps 1–7. If a command here does not behave as
written, that is a bug in the page it came from — fix the page (§8.10), do not
work around it on the box.

## 8.0 Where the box is starting from

```sh
cd ~/mdfly/deploy
docker ps -a --filter label=com.docker.compose.project=mdfly \
  --format '{{.Names}}\t{{.Label "com.docker.compose.service"}}\t{{.Image}}\t{{.Status}}'
```

| What you see | State | Where to start |
|---|---|---|
| Nothing | **A** — never deployed | §8.1, then skip §8.3 |
| A service named `app`, image `mdfly-server:latest` or similar | **B** — the pre-pipeline stack | §8.1, and §8.3 is mandatory |
| `app_blue` / `app_green` on a `ghcr.io/…` image | already cut over | you are past this page |

State **B** is the interesting one. That container is pre-ADR-0003 code: it serves
HTTP **and** runs the tickers in the same process. The new `jobs` container also
runs them, and exactly one process may (ADR-0003) — the purge drain's claim is
lease-free and assumes a single writer. So the old container has to be gone before
the new stack starts, not after.

Also confirm the checkout is clean and on `main`, because `deploy.sh` starts with
`git pull --ff-only` and a locally edited tracked file will stop it:

```sh
git -C ~/mdfly rev-parse --abbrev-ref HEAD    # => main
git -C ~/mdfly status --porcelain             # => empty
```

`.env`, `deploy/redis.conf` and `deploy/tls/*` are gitignored, so they do not
appear there and nothing on this page edits them.

## 8.1 The registry read token

The images are private (ADR-0015), so the box holds a credential of its own. This
is the same procedure as [4.4](4-deploy.md#44-registry-login), repeated here
because the cutover is when it is actually done.

**Navigate**: github.com → your avatar (top right) → **Settings** → **Developer
settings** (bottom of the left sidebar) → **Personal access tokens** → **Tokens
(classic)** → **Generate new token** → **Generate new token (classic)**.
Direct: <https://github.com/settings/tokens/new>.

Classic, not fine-grained: fine-grained tokens carry no package scopes, and GHCR
still authenticates with classic PATs.

| Field | Value |
|---|---|
| Note | `mdfly-box-pull` |
| Expiration | **90 days** |
| Scopes | **`read:packages` only** |

In the scope list `read:packages` sits under the `write:packages` heading. Tick the
child, not the parent — a ticked parent grants push. Nothing else: not `repo`, not
`delete:packages`. The box only ever pulls; the push and the prune are CI's, with
its own workflow token (step 7). A `read:packages` token that leaks reads this
image and nothing else.

**Make the expiry one you will notice.** GitHub emails the account address about a
week before, and the mail names the token — which is why the note is
`mdfly-box-pull` and not `token`. Add a calendar entry on the expiry date as well,
with the rotation command in the body:

```
docker login ghcr.io -u Abir66      # mdfly box pull token, read:packages, 90d
```

An expired token is not a warning anywhere. It is a deploy that dies at
`docker pull` with `denied`, at whatever hour you happened to be deploying.

Copy the token now — GitHub shows it once.

## 8.2 Log the box in, and prove it survives a reboot

```sh
read -rs CR_PAT                  # paste, press enter; nothing is echoed
echo "$CR_PAT" | docker login ghcr.io -u Abir66 --password-stdin
unset CR_PAT
# => Login Succeeded
```

`read -rs` keeps the token out of the shell history and out of the process list,
which `docker login -p <token>` would not.

Where it landed, and that it is not world-readable:

```sh
ls -l ~/.docker/config.json      # -rw------- 
grep -c ghcr.io ~/.docker/config.json    # => 1
```

Pull the current tag. This is the one command that proves token, scope, package
visibility and the box's egress in a single shot:

```sh
docker pull ghcr.io/abir66/mdfly:$(git -C ~/mdfly rev-parse HEAD)
# => ... Status: Downloaded newer image for ghcr.io/abir66/mdfly:<sha>
```

| Failure | Meaning |
|---|---|
| `denied` / `unauthorized` | The token or its scope. Redo §8.1 |
| `manifest unknown` | Login is fine; CI has not published that commit. Check Actions |
| `no such host` | The box cannot reach ghcr.io at all — egress, not credentials |

**Then reboot**, because "it works until the box restarts" is a failure that waits
for the worst moment:

```sh
sudo reboot
# wait ~30s, ssh back in
docker pull ghcr.io/abir66/mdfly:$(git -C ~/mdfly rev-parse HEAD)
# => Status: Image is up to date for ghcr.io/abir66/mdfly:<sha>
```

No password prompt, no re-login. The credential lives in `~/.docker/config.json`
and is not something `deploy.sh` redoes. You come back to it only when the token
expires or is revoked.

While you are here, confirm the containers came back on their own —
`restart: unless-stopped` is what does that, and a reboot is the only honest test:

```sh
docker ps --format '{{.Names}}\t{{.Status}}'
```

## 8.3 Retire the pre-pipeline stack — state B only

Skip this on a box that has never deployed.

The old `app` service no longer exists in `deploy/compose.yaml`, so Compose will
treat its container as an orphan and leave it running. It has to go first, for the
single-ticker reason in §8.0.

First, prove there is something to move *to*. This removal takes the site down,
and a box whose registry holds no image has no way forward:

```sh
docker pull ghcr.io/abir66/mdfly:$(git -C ~/mdfly rev-parse HEAD)
```

If that does not end in `Downloaded newer image` or `Image is up to date`, stop —
re-read the prerequisite at the top of this page. Do not run the next command.

```sh
docker logs mdfly-app-1 --tail 20        # a last look, if you want one
docker rm -f $(docker ps -q --filter label=com.docker.compose.project=mdfly \
                            --filter label=com.docker.compose.service=app)
```

`docker logs`, not `docker compose logs`: `app` is gone from `compose.yaml`, which
is exactly why the container is an orphan — Compose no longer knows the name and
answers `no such service: app`.

**The site is down from this moment until §8.4 finishes** — a few minutes, and the
one unavoidable outage of the cutover. Caddy and Redis keep running and are not
touched; Caddy answers 502 meanwhile, which is correct — there is no backend.

Now pull the repo by hand, before deploying, because one file needs a step
`deploy.sh` does not perform:

```sh
git -C ~/mdfly pull --ff-only
docker compose exec caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
docker compose restart caddy
```

> **A running Caddy does not notice a changed `Caddyfile`.** The file is a
> read-only bind mount; Caddy read it once at start. The pull you just did
> replaced a single-upstream config (`reverse_proxy app:8080`) with the
> two-upstream one the slots need, and `deploy.sh` never touches the `caddy`
> service — `docker compose up` does not recreate a container because a mounted
> file's contents changed. Without the restart above you get a permanent 502 with
> every container healthy, which is the hardest shape of failure to read. This
> applies to every future pull that changes `deploy/Caddyfile`, not just this one
> (see [6.1](6-operate.md#61-redeploy)).

Pre-flight the rest:

```sh
docker compose config >/dev/null && echo OK
ls -l ~/mdfly/.env                       # -rw-------, and an mtime you recognise
```

The full `docker compose config` output holds your secrets — do not paste it
anywhere. Before the first deploy the three application services render as
`image: mdfly-server:unset`; that is the deliberate `${MDFLY_IMAGE}` fallback and
means nothing has been deployed yet.

## 8.4 The first pipeline deploy

Read the plan before running anything:

```sh
cd ~/mdfly/deploy
./deploy.sh --dry-run       # reads state, starts nothing, pulls nothing
```

On a state-B box the `job_runs` migration (`0003`) is usually not applied yet, and
nothing is serving after §8.3, so both conditions hold:

```
path:    cold start with a schema change — migrate, then start the stack
current: none
target:  <the 40-char HEAD sha>
serving: app_blue (was none)
```

If `0003` is already applied — a state-A box that followed
[4.3](4-deploy.md#43-migrations) — the first line reads `cold start — no slot is
serving` instead. Either is fine here. What must **not** appear is `code-only` or
`schema change — migrate, then recreate the live slot`: both of those mean
`deploy.sh` found a live slot, so something from the old stack is still up.

Read that first line every time. It is the only place the path is announced, and
there is no flag to override it — deliberately (ADR-0015).

Then deploy:

```sh
./deploy.sh
```

Expected, in order: the pull, then the migration (`3/u job_runs (…ms)`), then
Compose creating `caddy`, `redis`, `app_blue` and `jobs`, then the script exits
silently. The order matters and is the property being kept: the schema moves
first, and a migration that fails has started no new code.

```sh
docker compose ps
docker compose ps --format '{{.Service}}\t{{.Ports}}'
```

Four containers — `caddy`, `app_blue`, `jobs`, `redis` — and only `caddy` shows
`0.0.0.0:443->443/tcp`. `app_green` is absent by design: it lives behind a compose
profile until a deploy swaps onto it.

Name the live slot for everything below (same two lines as
[5](5-verify.md) and [6](6-operate.md)):

```sh
export SLOT=$(docker ps --filter label=com.docker.compose.project=mdfly \
  --format '{{.Label "com.docker.compose.service"}}' | grep -m1 '^app_')
echo "$SLOT"                      # => app_blue
slotc() { case "$SLOT" in app_green) docker compose --profile green "$@" ;;
                         *) docker compose "$@" ;; esac; }
```

Re-run that `export` after every swap on this page — it moves.

And that the site is actually back:

```sh
curl -s -o /dev/null -w '%{http_code}\n' https://mdfly.dev/healthz       # 200
curl -s -o /dev/null -w '%{http_code}\n' https://api.mdfly.dev/healthz   # 200
```

If the first one 502s and every container is up, it is the Caddy reload from §8.3.

Publish a document now if there is not one already — §8.5 wants a real slug:

```sh
mdfly publish note.md            # note the slug
```

## 8.5 A code-only deploy drops nothing

The claim: on the swap path the idle slot is started and health-checked *before*
the live one is stopped, and Caddy's `lb_policy first` moves traffic the instant
the live slot goes away. **Zero non-2xx across the swap.**

This needs a **second, different tag** — redeploying the same sha would swap slots
but leave nothing for §8.6 to roll back to. Push a trivial commit to `main` (a
docs typo is enough), wait for **Actions → CI → Publish image** to go green, and
confirm the version exists on the package's **Versions** tab.

**Terminal 1** — the request loop. Two of them, because they prove different
things:

```sh
# always reaches the origin: api.mdfly.dev is Bypass cache (step 3.6)
while true; do
  curl -s -o /dev/null -w '%{http_code}\n' https://api.mdfly.dev/healthz
  sleep 0.1
done | sort | uniq -c
```

```sh
# a real slug, with a cache-buster so the edge cannot answer for the origin
i=0; while true; do
  i=$((i+1))
  curl -s -o /dev/null -w '%{http_code}\n' "https://mdfly.dev/<slug>?cb=$i"
  sleep 0.1
done | sort | uniq -c
```

The `?cb=` is not decoration. `mdfly.dev` is cacheable (step 3.6), so a plain loop
against a slug would be answered by Cloudflare and would stay green with the
origin on fire. A changing query string changes the cache key.

**Terminal 2** — the deploy:

```sh
cd ~/mdfly/deploy
./deploy.sh --dry-run
# path:    code-only — start the idle slot, then stop the live one
# current: <first sha>
# target:  <second sha>
# serving: app_green (was app_blue)
./deploy.sh
```

Stop the loop (Ctrl-C) once `deploy.sh` returns. The result must be **one line**:

```
   412 200
```

Any `502`s mean the fall-through is not working: check that both slots are named
in `deploy/Caddyfile` and that `health_uri /healthz` is present. Any `000` is curl
failing to connect — Caddy itself, not a slot.

Then confirm the swap really happened, and that exactly one web slot remains:

```sh
docker ps --filter label=com.docker.compose.project=mdfly \
  --format '{{.Label "com.docker.compose.service"}}\t{{.Image}}'
# app_green   ghcr.io/abir66/mdfly:<second sha>
# caddy / jobs / redis — and no app_blue line at all
export SLOT=app_green
```

Two `app_*` lines is a fault, not headroom: Caddy would serve whichever it prefers
(blue) and `deploy.sh` refuses to run at all until one is stopped.

## 8.6 Rollback rehearsal

Do this now, while the bookmark is from a code-only deploy. After §8.7 the script
will refuse — correctly — and rehearsing the refusal is not the same as rehearsing
the rollback.

```sh
cat deploy/.deploy-state
# PREVIOUS_TAG=<first sha>
# CROSSED_MIGRATION=no

./deploy.sh rollback --dry-run
# path:    code-only — start the idle slot, then stop the live one
# current: <second sha>
# target:  <first sha>
```

Rollback takes no tag argument — it returns to the bookmarked one — and it does
not `git pull`, because it moves the image and nothing else. Run it with the
request loop from §8.5 still going: a rollback is a swap, so it should also cost
zero non-2xx.

```sh
./deploy.sh rollback
docker ps --filter label=com.docker.compose.project=mdfly \
  --format '{{.Label "com.docker.compose.service"}}\t{{.Image}}'
# app_blue   ghcr.io/abir66/mdfly:<first sha>
```

The image tag on the running slot **is** the proof that old code is serving —
there is no build-info endpoint, deliberately.

Roll forward again:

```sh
export SLOT=app_blue
./deploy.sh                 # pulls the repo, deploys HEAD = <second sha>
export SLOT=app_green
```

Rollback re-bookmarks the tag it left, so a rollback of a rollback works; that is
why rolling forward with a plain `./deploy.sh` and with `./deploy.sh rollback`
both land in the same place here.

## 8.7 The schema path, and its cost

On the schema path there is deliberately **no overlap** — a migration would
otherwise have to be readable by both versions at once, and ADR-0015 buys a few
seconds of downtime rather than a permanent expand/contract rule. Verify the shape
of it once, so it is not a surprise at 1am.

If you have a commit carrying a new migration, deploy that and skip the next
paragraph. Otherwise rehearse with `job_runs` itself, which is the one table in
the schema that can be dropped without losing anything durable — it holds one
liveness stamp per job and both are re-written on the next pass:

```sh
docker compose run --rm -T migrate \
  'migrate -path=/migrations -database="$DATABASE_URL" down 1'
# => 3/d job_runs (…ms)
```

Between here and the deploy the `jobs` container's recorder has no table to write
to. It logs `record job run … err=…` and keeps ticking — the recorder observes
jobs and is not allowed to stop one. That is expected for the next minute only.

With a request loop running (§8.5):

```sh
./deploy.sh --dry-run
# path:    schema change — migrate, then recreate the live slot
# current: <sha>
# target:  <sha>
# serving: app_green (recreated in place)
./deploy.sh
```

Expected:

- The migration line (`3/u job_runs`) in the output **before** any container is
  touched. That ordering is the property — one version of the code ever meets a
  new schema.
- In the loop: a **short run of `502`s — seconds, not minutes** — then `200`
  again. That is the documented cost of this path, not a fault. The full boot
  window would be tens of seconds; recreating one slot is not that.
- The same slot serving afterwards, on the new tag. This path does not swap.

```sh
psql "$DATABASE_URL" -Atc "SELECT to_regclass('public.job_runs')"   # => job_runs
```

Now confirm the refusal, which is the other half of this path:

```sh
./deploy.sh rollback
# WARNING: the deploy being reverted applied a migration. <sha> predates the
# current schema and may fail against it. ...
# deploy: refusing to roll back across a migration without --force
```

**Do not pass `--force`.** A tag cannot un-apply a migration, so returning across
one puts the old binary in front of a schema it has never seen. Schema deploys are
fixed forward: write the next migration and deploy that.

## 8.8 The jobs process

Ticks belong to the `jobs` container and to nothing else (ADR-0003). To see one
now rather than in an hour, shorten both intervals temporarily:

```sh
# in ~/mdfly/.env
LIFECYCLE_GC_INTERVAL=30s
PURGE_DRAIN_INTERVAL=30s
```

```sh
docker compose up -d jobs        # a rotation of .env only reaches a recreated container
sleep 90
docker compose logs jobs | grep -E 'jobs started|lifecycle gc pass'
# {"level":"INFO","msg":"mdfly-server jobs started","jobs":["lifecycle-gc","purge-drain"]}
# {"level":"INFO","msg":"lifecycle gc pass","expired":0,"abandoned":0,"blobs_deleted":0}

slotc logs "$SLOT" | grep -c 'lifecycle gc pass'     # => 0
```

That `0` is the point of the split: a web slot registers no tickers, so grepping
it can only ever prove the split held.

`purge-drain` logs only when it actually purged something, so its liveness is the
table, not the log — which is exactly why the table exists:

```sh
psql "$DATABASE_URL" -c \
  'SELECT name, last_success_at, last_failure_at, consecutive_failures FROM job_runs'
```

Both `lifecycle-gc` and `purge-drain` must have a `last_success_at` within the
last minute and `consecutive_failures = 0`. A missing `purge-drain` row means the
job was never registered — check for `cloudflare not configured, cdn purges will
stay queued` at boot, i.e. empty Cloudflare values in `.env`.

And exactly one of these containers may run:

```sh
docker compose ps jobs           # exactly one row
```

**Drain, not kill.** Stop the container while a pass is in flight and it should
finish the pass and exit cleanly, not lose the batch:

```sh
docker compose stop jobs         # time it right after a tick; do not pass -t
docker compose logs jobs --tail 5
# {"level":"INFO","msg":"shutdown signal received, draining jobs"}
docker inspect --format '{{.State.ExitCode}}' $(docker compose ps -aq jobs)
# => 0
docker compose up -d jobs
```

`0` is a drain. **`137` is a SIGKILL** — Docker's grace period expired mid-pass
and the batch was thrown away. That means `stop_grace_period` (120s) is no longer
above `SHUTDOWN_TIMEOUT` (115s) in `deploy/compose.yaml`; nothing enforces that
ordering mechanically, which is why it is checked here. The log line
`jobs runner shutdown timed out with a job still running` is the same failure seen
from inside the process.

Then put the intervals back and recreate once more:

```sh
# restore LIFECYCLE_GC_INTERVAL=1h and PURGE_DRAIN_INTERVAL=15m in ~/mdfly/.env
docker compose up -d jobs
```

## 8.9 Storage and disk

**Registry side.** Account → **Settings** → **Billing and licensing** → **Plans and
usage** → **Storage for Actions and Packages**
(direct: <https://github.com/settings/billing>). The free private-package
allowance is **500 MB**; a version is roughly 25 MB, so the retained 10 sit near
250 MB.

That the prune ran: **Actions** → the newest publish run → **Publish image** →
**Prune old image versions**. Until there are more than 10 versions the step reads
`Nothing to prune: at most 10 versions exist.`; after that, one
`Deleting version <id> (tags: …)` line per dropped version, then
`Pruned N version(s); newest 10 retained.` A package that publishes fine and never
prunes is the unlinked-package case — step 7 §3.

**Box side.** The deploy's own prune keeps exactly two tags: what is serving and
what rollback would return to.

```sh
docker images ghcr.io/abir66/mdfly --format '{{.Tag}}\t{{.Size}}'   # exactly 2 rows
docker system df
df -h /
```

On a state-B box the locally built image is still there and `deploy.sh` will never
touch it — its prune only knows the `ghcr.io/abir66/mdfly` repository:

```sh
docker images | grep -v ghcr.io          # look for mdfly-server:latest & friends
docker rmi mdfly-server:latest
```

Steady, not growing, is the claim: note `docker system df` here, deploy twice more
over the following days, and check that **Images** total has not moved beyond two
mdfly tags plus caddy, redis and migrate.

## 8.10 Fold every correction back

Anything on this page or in steps 1–7 that did not behave as written is a
documentation bug. Fix it in the repo, on your machine, and deploy the fix like
any other commit.

Do not edit tracked files on the box. `deploy.sh` begins with
`git pull --ff-only`, so a local change to `deploy/compose.yaml` or `deploy/*.sh`
does not just drift — it stops the next deploy from starting at all. `.env`,
`deploy/redis.conf` and `deploy/tls/*` are the only files that live on the box
alone, and they are gitignored.

## Checklist

- [ ] A `read:packages`-only classic token exists, named `mdfly-box-pull`, with an
      expiry on your calendar
- [ ] The box pulls the current tag, and still does after a reboot with no
      re-authentication
- [ ] The pre-pipeline `app` container is gone before `jobs` ever started
- [ ] Caddy was restarted after the pull that changed its config
- [ ] `--dry-run` was read before the first real deploy, and its path matched what
      happened
- [ ] The first deploy left four containers with only `caddy` publishing a port
- [ ] A code-only deploy under a request loop returned `200` and nothing else, on
      both `api.mdfly.dev/healthz` and a real slug with a cache-buster
- [ ] Exactly one web slot remains after the swap
- [ ] Rollback returned to the previous tag with no non-2xx, and rolling forward
      worked
- [ ] The schema deploy printed `schema change — migrate, then recreate the live
      slot`, ran the migration before any container was touched, and cost seconds
- [ ] `deploy.sh rollback` refused to cross that migration, and `--force` was not
      used
- [ ] `lifecycle gc pass` appears in the `jobs` container and zero times in the web
      slot
- [ ] `job_runs` holds a fresh `last_success_at` for both jobs with
      `consecutive_failures = 0`
- [ ] Stopping `jobs` mid-pass exited `0`, not `137`
- [ ] Package storage is under 500 MB and the prune step's output was read
- [ ] `docker images ghcr.io/abir66/mdfly` shows exactly two tags, and the
      locally built image is deleted
- [ ] Every correction is a commit, not box-local knowledge

Back to [6. Operate](6-operate.md) for the day-to-day runbook.
