# Cloudflare setup for the CDN purge queue (ADR-0031)

Every publish/update commit and every delete enqueues its slug to the durable
`purge_queue` table and then invalidates two prefixes at the edge —
`mdfly.dev/<slug>` and `mdfly.dev/llm/<slug>`. The backend needs a zone ID and a
scoped API token to do that. Nothing is pre-provisioned — do this once, by hand,
in the Cloudflare dashboard.

Prefix purge is a **paid-plan feature on some zones**: Free zones get purge by
URL, hostname, tag and prefix on many plans, but if the API rejects the prefix
form with `"Prefix purge is not available for your zone"`, the zone needs a Pro
plan (or the code must fall back to per-URL purge). Verify with step 4 before
relying on it.

## 1. Copy the zone ID

1. Sign in at <https://dash.cloudflare.com>.
2. Pick the **mdfly.dev** zone → **Overview**.
3. Right column, **API** section → **Zone ID**. Copy it.

```
CLOUDFLARE_ZONE_ID=<32 hex chars>
```

## 2. Mint a scoped API token

Use a token, not the Global API Key — the key authorizes everything on the
account and cannot be scoped.

1. **My Profile** → **API Tokens** → **Create Token**.
2. **Create Custom Token** (do not use a template).
   - **Token name**: `mdfly-backend-purge`
   - **Permissions**: `Zone` → `Cache Purge` → `Purge`. That single permission is
     all the backend needs; add nothing else.
   - **Zone Resources**: `Include` → `Specific zone` → `mdfly.dev`.
   - **Client IP Address Filtering** (optional, recommended): `Is in` → the
     Oracle VM's public IP (ADR-0029). Skip it if the VM's IP is not static.
   - **TTL**: leave open-ended.
3. **Continue to summary** → **Create Token**, then copy the token — the
   dashboard shows it once.

```
CLOUDFLARE_API_TOKEN=<token>
```

## 3. Set the env vars on the backend

Add both to the backend's environment (the systemd unit / `docker run
--env-file` on the Oracle VM, per S44). The drain interval is separately
configurable via `PURGE_DRAIN_INTERVAL` (default `15m`); the backoff curve and
batch size are code constants in `internal/server/service/purge`.

Leaving either unset is a supported mode: the server logs
`cloudflare not configured, cdn purges will stay queued` at boot, registers no
`purge-drain` job, and skips the inline attempt. Writes still enqueue their slug
transactionally, so a later correctly-configured process drains the backlog —
nothing is lost, it just goes stale until the 24h `s-maxage` lapses. Production
must set both.

## 4. Verify

Confirm the token can purge prefixes at all, before trusting the backend to:

```sh
curl -s -X POST \
  "https://api.cloudflare.com/client/v4/zones/$CLOUDFLARE_ZONE_ID/purge_cache" \
  -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"prefixes":["mdfly.dev/does-not-exist"]}'
```

Expect `{"result":{"id":"<zone id>"},"success":true,"errors":[],"messages":[]}`.
A purge of a slug that was never cached is a valid no-op. Failures to read:

- `"errors":[{"code":10000,"message":"Authentication error"}]` — wrong token, or
  the token lacks `Cache Purge`.
- `"code":1012` — wrong zone ID.
- a message about prefix purge not being available — see the plan note above.

Then check the end-to-end path against the deployed backend:

```sh
mdfly publish note.md            # note the slug
curl -sD - -o /dev/null https://mdfly.dev/<slug> | grep -i cf-cache-status
# → HIT after a second request

mdfly update note.md             # edit the file first
curl -sD - -o /dev/null https://mdfly.dev/<slug> | grep -i cf-cache-status
# → MISS (or EXPIRED), and the new content is served
```

The backend logs `cdn purge drain purged=<n>` on a tick that cleared rows, and
`inline cdn purge failed, left queued` when the immediate attempt lost. To see
the durable path work on its own, check the queue directly:

```sh
psql "$DATABASE_URL" -c 'SELECT slug, attempts, next_attempt_at FROM purge_queue'
```

A healthy system keeps this table empty or near-empty. Rows with a climbing
`attempts` mean Cloudflare is rejecting the purge — check the token, then the
zone's plan.

## Rate limits to respect

Cloudflare's free-plan purge limits are **5 requests/minute** (token bucket,
burst 25) and **100 prefixes/request**. The backend sends one request per slug
(2 prefixes), so a drain batch of 50 slugs is 50 requests — above the per-minute
bucket if a large backlog drains at once. A 429 is treated like any other
failure: the row stays queued and retries with backoff, so a backlog drains over
several ticks rather than failing. If that becomes routine, coalesce slugs into
one request (the 100-prefix ceiling allows 50 slugs per call).
