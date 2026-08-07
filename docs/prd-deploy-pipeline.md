# PRD: Deploy Pipeline — prebuilt images, blue-green swap, dedicated jobs process

**Triage:** `ready-for-agent`
**Governed by:** ADR-0015 (prebuilt images, operator-triggered blue-green deploy), ADR-0003 (Always-on free-tier VM, in-process tickers in a dedicated jobs process — **rewritten** by this work), ADR-0012 (durable CDN purge queue), ADR-0005 (lifecycle GC), ADR-0011 (Cloudflare edge), ADR-0013 (rate limiting on self-hosted Redis).

> Follows [PRD: Deployment & Production Hardening](prd-deploy-hardening.md), which stood the box up and explicitly left CI/CD out of scope. That PRD's closing line — "CLI distribution and CI/CD are out of scope here" — is what this PRD closes.

## Problem Statement

The backend is deployed and serving, but there is no answer to "how does the next commit get there safely."

- **A redeploy drops in-flight work.** `docker compose up -d --build` stops the only `app` container and starts a new one, so Caddy has no upstream for the boot window and every request in flight dies. There is no second copy to hold traffic.
- **The drain the code already implements can never run.** `App.Run` waits for in-flight requests and running jobs within `SHUTDOWN_TIMEOUT` (15s), but `deploy/compose.yaml` sets no `stop_grace_period`, so Docker's 10s default sends SIGKILL first. The three budgets are inconsistent in the wrong direction: the server permits a 60s request, waits 15s, and is killed at 10s.
- **Housekeeping is welded to the web tier.** The lifecycle GC and purge drain run as tickers inside the process that serves HTTP (ADR-0003 as originally written). Any scheme that keeps two web processes alive to drain requests therefore runs two of every ticker, breaking the single-writer premise `ClaimPurgeDue` is written against, and forces every deploy's tail — including a footer typo — to cover the slowest housekeeping pass.
- **The box compiles its own code.** `up -d --build` spends ~1 minute of a free-tier ARM core compiling while that same box serves visitors, and a broken build is discovered halfway through a deploy rather than before the box is touched.
- **There is no rollback.** No previous artifact exists. Going back means finding the old commit and recompiling on the box.
- **Housekeeping failure is invisible.** The GC and drain log and move on. Nothing records whether a pass ever succeeded, so weeks of silent failure look exactly like weeks of success.
- **The deploy procedure lives only on the box.** `docs/deploy/6-operate.md` recommends a `deploy.sh` explicitly kept out of git, contradicting the same document's claim that nothing on the box is the only copy of anything.

## Solution

Split the backend into two roles, build artifacts off-box, and make the swap overlap.

- **One binary, two subcommands.** `mdfly-server serve` registers routes and no tickers; `mdfly-server jobs` registers tickers and opens no listener. Exactly one jobs container runs, so the lease-free purge claim stays sound while any number of web processes coexist.
- **GitHub Actions builds; the box never compiles.** Every push to `main` cross-compiles the ARM binary, packs it, and publishes a **private** image tagged with the commit SHA to the repo's container registry, pruning to the newest 10 versions.
- **Two fixed web slots.** `app_blue` and `app_green` are both listed permanently in the Caddyfile with active health checks on `/healthz` and retry-onto-the-other-upstream. A code-only deploy starts the idle slot, waits for it to answer, then stops the live one. Caddy's config is never touched by a deploy.
- **A strictly increasing timeout chain.** Request ceiling 30s → in-process drain 35s → container SIGKILL grace 45s, with the jobs container overriding the last two to ~115s and 120s.
- **Schema deploys skip the overlap.** The deploy script compares the highest file in `migrations/` against the applied `schema_migrations` version; on a mismatch it migrates and plainly recreates the single live slot, accepting ~5 seconds of downtime rather than requiring every migration to be readable by both the outgoing and incoming code.
- **Rollback is a bookmark.** The outgoing tag is recorded before each swap, so `deploy.sh rollback` re-swaps to it. Valid for code-only deploys; schema deploys are fixed forward.
- **Housekeeping becomes observable.** Each job stamps its outcome into a `job_runs` table. No endpoint, no page, no alerting — the row is the record, read by hand.

To a publisher, editor, and viewer, nothing changes except that a deploy no longer interrupts them. To the owner, a deploy becomes one command with a known blast radius and a way back.

## User Stories

1. As a **publisher**, I want my in-flight `publish/commit` to complete when the owner deploys, so that I do not lose a Bundle I have already uploaded to R2.
2. As a **publisher**, I want a new request during a deploy to be served by *some* copy of the backend, so that I never see a `502` from the edge.
3. As an **editor**, I want my `update/commit` to finish across a deploy, so that my Document is not left with a stale Bundle Manifest while its blobs are already uploaded.
4. As a **viewer**, I want slug pages to keep resolving during a deploy, so that a link I opened does not break for reasons that have nothing to do with the Document.
5. As a **viewer**, I want the LLM twin at `/llm/<slug>` to stay available during a deploy, so that an agent reading my Document does not get a transport error mid-fetch.
6. As the **owner**, I want a code-only deploy to cause zero downtime, so that I can ship at any hour without picking a window.
7. As the **owner**, I want a deploy that changes the schema to take the site down briefly and predictably, so that I never have to write a migration readable by two versions of the code at once.
8. As the **owner**, I want the deploy script to work out for itself whether the schema moved, so that forgetting a flag cannot expose the old code to a schema it does not understand.
9. As the **owner**, I want migrations applied before the new code starts, so that new code never runs against an old schema.
10. As the **owner**, I want the lifecycle GC and purge drain to run in their own process, so that redeploying the web tier never interrupts a housekeeping pass.
11. As the **owner**, I want exactly one jobs process to exist, so that the purge drain's lease-free claim on `purge_queue` stays correct and Cloudflare is never asked to purge the same slug twice concurrently.
12. As the **owner**, I want a running GC pass to finish when the jobs container is replaced, so that a batch of blob deletions is not abandoned halfway between the R2 delete and the `blobs_deleted_at` stamp.
13. As the **owner**, I want housekeeping to remain safe to kill at any moment, so that an Oracle instance reclaim or a power loss costs at most one batch and the next pass resumes.
14. As the **owner**, I want the three shutdown budgets to increase strictly, so that the drain the code already implements actually gets to run instead of being SIGKILLed first.
15. As the **owner**, I want a request longer than the declared ceiling to be rejected up front rather than silently killed at swap time, so that the promise "in-flight work completes" is honest.
16. As the **owner**, I want GitHub to compile the ARM binary, so that a deploy does not spend a free-tier core competing with live traffic.
17. As the **owner**, I want a broken build to fail in CI, so that the box is never touched by a commit that cannot compile.
18. As the **owner**, I want each build tagged with its commit SHA, so that I can name exactly which artifact is running.
19. As the **owner**, I want the previous artifact to still exist, so that rolling back is starting an image rather than recompiling a commit.
20. As the **owner**, I want the deploy script to bookmark the outgoing tag automatically, so that rollback needs no recall of what was live.
21. As the **owner**, I want rollback to refuse or loudly warn when the deploy being reverted included a migration, so that I do not put old code against a new schema by reflex.
22. As the **owner**, I want the build to delete all but the newest few image versions, so that I never discover the 500 MB private-package cap at the moment I am trying to ship.
23. As the **owner**, I want images kept private, so that the SQL, route names, and internal logic that survive compilation as readable strings are not handed to strangers.
24. As the **owner**, I want the box to authenticate to the registry once and keep working across reboots, so that a restart does not become a login exercise.
25. As the **owner**, I want no SSH key that opens the production box stored in a GitHub account, so that a compromised GitHub account is not a compromised origin.
26. As the **owner**, I want deploys to require me to run them, so that no merge reaches production with nobody watching.
27. As the **owner**, I want one portable `Dockerfile` still to be the whole build definition, so that ADR-0003's portability constraint survives and the box stays swappable.
28. As the **owner**, I want the cross-compile to avoid emulation, so that a build takes seconds rather than minutes.
29. As the **owner**, I want Caddy's configuration written once with both slots listed, so that a deploy never edits or reloads the TLS terminator.
30. As the **owner**, I want Caddy to retry the other slot when one refuses a connection, so that the few seconds between "old slot stopped" and "Caddy noticed" do not surface as errors.
31. As the **owner**, I want `/healthz` to stay free of Postgres and R2 checks, so that one database hiccup cannot mark both slots dead and turn a degraded site into a dark one.
32. As the **owner**, I want the deploy to wait for the new slot to answer `/healthz` before stopping the old one, so that a container that cannot boot never receives traffic.
33. As the **owner**, I want a deploy to abort and leave the old slot serving when the new one never becomes healthy, so that a bad artifact is a non-event.
34. As the **owner**, I want each housekeeping job to record its last successful pass in Postgres, so that "is the GC alive?" has an answer I can query by hand.
35. As the **owner**, I want a failed pass recorded distinctly from a successful one, so that silent repeated failure is visibly different from silence.
36. As the **owner**, I want jobs to report failure to the runner rather than swallowing it, so that the recorder has something truthful to write down.
37. As the **owner**, I want the deploy script tracked in the repo, so that the box rebuild runbook in `docs/deploy/6-operate.md` stays true and the script's history is reviewable.
38. As the **owner**, I want a dry-run mode on the deploy script that prints which path it would take, so that I can check the schema-detection branch without deploying.
39. As the **owner**, I want the box to keep pulling the repo for `compose.yaml`, the Caddyfile, and `migrations/`, so that the artifacts not in the image stay in step with the one that is.
40. As the **owner**, I want old images pruned on the box after a deploy, so that disk use reaches a steady state instead of growing per push.
41. As the **owner**, I want `docs/deploy/` to describe the pipeline that actually exists, so that a rebuild six months from now does not follow instructions that build on the box.
42. As an **AFK agent**, I want the deploy design recorded in ADRs and CONTEXT.md, so that I do not "fix" the deliberate split by folding the tickers back into the web process.
43. As an **AFK agent**, I want the jobs and web assemblies covered by tests asserting the *absence* of the other role's wiring, so that a future refactor cannot quietly re-register tickers in the web tier.

## Implementation Decisions

**Process roles.** One binary, two subcommands. `cmd/mdfly-server` becomes a dispatcher over `serve` and `jobs`; the default with no argument is a hard error rather than an implicit `serve`, so a mistyped compose command fails loudly instead of silently starting the wrong role. `internal/server` gains role-aware assembly: the web role wires the DB pool, R2 client, static assets, domain services, the Redis limiter, and routes, and registers **no** tickers; the jobs role wires the DB pool, R2 client, and the purge and GC services, registers the tickers, and opens **no** listener and **no** Redis connection. The inline post-write CDN purge attempt (ADR-0012) stays in the web role, where the write happens, and remains untracked by the drain's wait group — it is killed at swap, which is harmless because the `purge_queue` row is the durable path.

**Job outcome reporting.** `jobs.Runner`'s registered function signature changes from `func(context.Context)` to `func(context.Context) error`. `gc.Service.Job` and `purge.Service.Job` currently log and discard; they return the error instead and keep their per-pass count logging. The `Runner` gains an optional **Recorder** — a narrow interface taking the job name, the outcome, and the timestamp — invoked after every tick, inside the same panic recovery so a recorder failure cannot take down a ticker. A nil Recorder is valid and means "record nothing", which is what local dev and the existing tests use.

**Schema.** One new migration adding a `job_runs` table keyed by job name, holding the last successful pass timestamp, the last failure timestamp, the last error text, and a consecutive-failure count. Upsert by name — one row per registered job, not an append-only log, because the question is "is it alive" and unbounded growth would need its own GC. No endpoint, page, or alerting is built over it; the row is read by hand. `docs/deploy/6-operate.md` gains a row in its watch-list table naming the query.

**Timeout chain.** `serverWriteTimeout` drops from 60s to 30s, making the declared request ceiling honest. `SHUTDOWN_TIMEOUT` stays env-driven but is set per container in compose — ~35s for the web slots, ~115s for jobs — overriding the shared `.env` value. `stop_grace_period` is set explicitly on all three app services: 45s for web slots, 120s for jobs. The invariant `stop_grace_period > SHUTDOWN_TIMEOUT > request ceiling` is stated in `deploy/compose.yaml` as a comment, since nothing enforces it mechanically.

**Build.** A GitHub Actions workflow on push to `main` uses `docker buildx` with `FROM --platform=$BUILDPLATFORM` on the builder stage and `GOARCH=$TARGETARCH` on the `go build`, so an Intel runner cross-compiles the ARM binary natively — no QEMU emulation. This keeps **one portable `Dockerfile`** that still builds from source anywhere, satisfying ADR-0003's constraint. The image is pushed to the repo's private container registry tagged with the commit SHA, and a prune step keeps the newest 10 versions. Existing CI jobs (lint, test, migrate, build) remain the gate; the publish step runs after them.

**Compose.** `deploy/compose.yaml` replaces the single `app` service with `app_blue` and `app_green` — identical apart from name — plus `jobs`. All three take `image:` from the registry with no `build:` stanza, so the box never compiles. The two web slots are behind a profile or are individually named on the command line so a bare `up -d` does not start both. Redis, Caddy, and the one-shot `migrate` service are unchanged. Steady state is four long-lived containers (one web slot, jobs, Redis, Caddy) with a fifth transiently alive during a swap.

**Caddy.** `deploy/Caddyfile`'s `reverse_proxy` names both slots, with active health checking on `/healthz` and a retry window so a refused connection falls through to the other upstream. The Cloudflare-edge-only header stripping and TLS configuration are untouched. `/healthz` itself stays dependency-free.

**Deploy script.** `deploy/deploy.sh` is tracked in the repo. It pulls the repo, resolves the target tag, pulls the image, compares the highest numbered file in `migrations/` against the applied `schema_migrations` version, and branches: **no schema change** → start idle slot, poll `/healthz`, stop live slot, record the outgoing tag, prune; **schema change** → run `migrate`, then recreate the live slot in place. It supports `rollback`, which re-swaps to the bookmarked tag and warns when the deploy being reverted crossed a migration boundary, and `--dry-run`, which prints the detected path and the tags involved without touching anything. Secrets are never read by the script beyond passing `.env` through to Compose.

**Registry auth.** A one-time `docker login` against the registry with a read-only token, stored in the box's Docker config so it survives reboots. Documented as an external setup step in `docs/deploy/`, with the token's required scope named explicitly.

**Documentation.** ADR-0003 is rewritten in place (per `docs/adr/README.md`'s no-supersession rule) to move the tickers into the jobs process and record the reversal. ADR-0015 is added for the pipeline. CONTEXT.md's `[[Compute]]` term is corrected and a `[[Deploy]]` term added. `docs/deploy/1-oracle-vm.md`, `4-deploy.md`, `5-verify.md`, and `6-operate.md` are updated: no on-box build, two slots, the jobs container, the timeout chain, the registry login, and the `job_runs` query. ADR-0014's v1 scope line gains the pipeline.

## Testing Decisions

A good test here asserts **externally observable behaviour** — what a role wires, what a runner records, what a table holds after a pass — and never reaches for a private field or asserts the shape of an internal call. The existing suite already sets this bar: `internal/server/jobs/jobs_test.go` drives ticks through an injected `Clock` with no sleeping and no real time, and `internal/server/db/purge_test.go` runs against a real Postgres in a container and skips under `-short`. Both patterns are reused rather than reinvented.

**`internal/server/jobs` — Recorder and error propagation.** Unit tests with the existing fake clock and a fake recorder. Covered: a successful tick records success; a returning-error tick records failure with the error text; a panicking tick records failure and the ticker keeps running (extending the existing panic test); a nil recorder is a no-op and changes nothing; `Stop` still drains a running job within its context and still returns `ctx.Err()` on timeout. No infrastructure.

**`internal/server/db` — `job_runs` ops.** Integration tests against a containerised Postgres, mirroring `db/purge_test.go` including the `-short` skip. Covered: first record for a name inserts; a second record for the same name upserts rather than appends; a failure record increments the consecutive-failure count and leaves the last-success timestamp intact; a success record resets the count. Asserted by reading rows back, not by inspecting the query.

**`internal/server` — role assembly.** Extends `app_internal_test.go`, which already asserts that job registration reads its intervals and graces from config. Covered: the web role registers zero tickers and does open a listener; the jobs role registers exactly the expected tickers and opens none; the jobs role does not construct a Redis limiter; both roles construct the DB pool and R2 client. These are the tests that stop a future refactor from quietly folding the tickers back into the web process.

**`deploy/deploy.sh` — path detection.** The `--dry-run` mode exists to make the risky branch checkable: given a migrations directory and a reported applied version, it prints which path it would take. Verified by invoking it against fixture inputs — no container starts, nothing is pulled, nothing swaps.

**Not unit tested:** `deploy/compose.yaml`, `deploy/Caddyfile`, and the GitHub workflow. These are verified by performing a deploy and observing it — a zero-downtime swap under a request loop, and a schema deploy taking the expected brief outage — which belongs in `docs/deploy/5-verify.md` as owner-run verification steps, not in `go test`.

## Out of Scope

- **Automated deploy on merge.** Rejected in ADR-0015. Pull-based deployment from the box remains the documented upgrade path if the manual step ever becomes the bottleneck.
- **More than one web slot at a time.** Two fixed slots do not generalise to N replicas; scaling past one live web process means moving to upstream discovery, which is a separate decision.
- **Pushed failure notification** for housekeeping — mail, webhook, or any outbound channel. Deferred until real users depend on Anonymous Document expiry landing on time.
- **Expand/contract migration discipline.** Explicitly declined; schema deploys take the brief outage instead.
- **Rollback across a migration.** Schema deploys are fixed forward. No down-migration automation is built.
- **A second box, or any multi-host topology.** ADR-0003's single-box trade stands.
- **Moving Redis or Postgres.** Untouched by this work.
- **CLI distribution** (goreleaser, Homebrew tap, install script). Unrelated pipeline, separate concern.
- **Any change to the wire protocols, Bundle Limits, view semantics, or the Edge configuration.**

## Further Notes

The **500 MB private-package allowance** is the one hard external ceiling here. At roughly 25 MB per version it permits about 18 retained images, which is why pruning is part of the build rather than an operational chore. If it ever binds harder, the escape hatch is making the package public — the image carries no secrets, since configuration arrives from `.env` at boot — at the cost of publishing readable internals, which is the trade ADR-0015 declined.

Two facts made the design cheaper than it first looked. First, **no long-running request exists**: `storage.PresignPUT` means CLI clients upload Bundle bytes straight to R2, so a 25 MB Bundle never crosses the origin, and the slowest server call is a commit — one transaction plus up to 50 blob HEADs. Markdown rendering is hard-capped at 2 seconds. That is what makes a 30-second declared ceiling comfortable rather than tight. Second, **housekeeping was already written to be killable**: the GC moves bounded batches and stamps progress per row, and `purge_queue` is a durable table, so "safe to kill at any moment" holds today and the 2-minute grace on the jobs container is politeness rather than the guarantee.

The **timeout chain has no mechanical enforcement.** Nothing fails a build or refuses a boot when `SHUTDOWN_TIMEOUT` exceeds `stop_grace_period`. It is a comment in `deploy/compose.yaml` and a paragraph in ADR-0015. If it is ever silently violated, the symptom is jobs and requests being SIGKILLed mid-flight while every setting looks deliberate — worth a boot-time warning if it bites once.

`SHUTDOWN_TIMEOUT` being **per-container rather than global** is the one place the single-`.env` rule from `[[Compute]]` bends. The value in `.env` remains the default; compose overrides it per service. That is a deliberate exception, not drift, and the reason is that the two roles have genuinely different drain profiles.
