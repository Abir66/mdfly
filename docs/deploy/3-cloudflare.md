# 3. Cloudflare

DNS, the origin TLS certificate, the R2 bucket, and the purge token. One dashboard
session (ADR-0011).

**You need**: the reserved IP from step 1, `mdfly.dev` in a Cloudflare account.
**You produce**: `deploy/tls/origin.{crt,key}` on the VM, the four R2 values, and
`CLOUDFLARE_ZONE_ID` + `CLOUDFLARE_API_TOKEN` — all used in step 4.

## 3.1 Zone ID

**mdfly.dev → Overview → right column, API → Zone ID.** Copy it.

```
CLOUDFLARE_ZONE_ID=<32 hex chars>
```

## 3.2 DNS records

**DNS → Records:**

| Type | Name | Content | Proxy |
|---|---|---|---|
| A | `@` | reserved IP | **Proxied** (orange) |
| A | `api` | reserved IP | **Proxied** (orange) |
| A | `www` | reserved IP | **Proxied** (orange) |
| MX | `@` | `.` priority 0 | — |
| TXT | `@` | `v=spf1 -all` | — |
| TXT | `_dmarc` | `v=DMARC1; p=reject` | — |

Everything pointing at the origin **must** be proxied. A grey-cloud record puts the
origin IP in public DNS and lets callers bypass the edge entirely, which breaks both
the WAF and the rate limiter's client-IP attribution.

`cdn` is created for you in 3.5 — do not add it by hand.

The three email records exist because there is no transactional email in v1 and
spammers probe new domains: null-MX (RFC 7505) plus a hard-fail SPF plus a rejecting
DMARC means any forgery of `@mdfly.dev` gets rejected by receivers.

`app.mdfly.dev` is deliberately **not** provisioned — it is reserved for the v2
dashboard and NXDOMAIN is the truthful state.

**www → apex redirect.** **Rules → Redirect Rules → Create rule:** when hostname
equals `www.mdfly.dev`, redirect to `https://mdfly.dev` with the path preserved,
status 301. (ADR-0011 describes this as a Bulk Redirect; a single rule is the
simpler equivalent at one hostname.)

> **On the apex Worker.** ADR-0011 has the apex Worker-routed between Cloudflare
> Pages and the backend. That split arrives with `apps/landing/` — it does not
> exist yet, so today the apex A record goes straight to the backend and the
> backend serves `/robots.txt` itself. Nothing here changes when the Worker lands;
> a route is added in front.

## 3.3 Origin TLS certificate

Caddy terminates TLS at the origin with a **Cloudflare Origin CA** certificate:
trusted only by Cloudflare, valid 15 years, no renewal cron. This is what makes
Full (strict) possible.

**SSL/TLS → Origin Server → Create Certificate:**

- Private key type **RSA (2048)**
- Hostnames `mdfly.dev` **and** `*.mdfly.dev` (the wildcard covers `api.`)
- Validity 15 years

Copy **both** PEM blocks before closing the dialog — the private key is shown
exactly once. On the VM:

```sh
mkdir -p ~/mdfly/deploy/tls && chmod 700 ~/mdfly/deploy/tls
nano ~/mdfly/deploy/tls/origin.crt   # paste the certificate
nano ~/mdfly/deploy/tls/origin.key   # paste the private key
chmod 600 ~/mdfly/deploy/tls/origin.key
```

Store a copy of the key in your password manager. `deploy/tls/` is gitignored.

## 3.4 SSL/TLS settings

| Setting | Value | Why |
|---|---|---|
| **SSL/TLS → Overview** | **Full (strict)** | "Flexible" sends plaintext to the origin, making Cloudflare the MITM; plain "Full" accepts a self-signed origin cert |
| Edge Certificates → Minimum TLS Version | 1.2 | |
| Edge Certificates → Always Use HTTPS | On | |
| Edge Certificates → HSTS | **Off** | Deliberate in v1 — HSTS sticky-bricks the domain if HTTP is ever served. Enable after a month or two of stable HTTPS |
| Network → HTTP/3, 0-RTT | On | Edge-to-browser only; the origin stays HTTP/1.1–2 over TCP 443 |

## 3.5 R2 bucket

**R2 → Create bucket**, named `mdfly`, in a location near the VM.

**Credentials** — **R2 → Manage API Tokens → Create API Token:**

- Permission **Object Read & Write**
- Scoped to the `mdfly` bucket only
- Copy the Access Key ID and Secret Access Key — shown once

```
R2_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com
R2_ACCESS_KEY_ID=<access key id>
R2_SECRET_ACCESS_KEY=<secret>
R2_BUCKET=mdfly
```

The account ID is in the R2 overview page's S3 API endpoint.

**Public domain** — **the bucket → Settings → Public access → Custom Domains →
Connect Domain**, `cdn.mdfly.dev`. This creates the DNS record for you and serves
GETs from the edge without ever touching the backend.

```
CDN_BASE_URL=https://cdn.mdfly.dev
```

**CORS** — the Viewer's Raw toggle `fetch()`es the source blob from `cdn.` while the
page is on the apex, so add a rule under **the bucket → Settings → CORS Policy**:

```json
[
  {
    "AllowedOrigins": ["https://mdfly.dev"],
    "AllowedMethods": ["GET", "HEAD"],
    "AllowedHeaders": ["*"],
    "MaxAgeSeconds": 86400
  }
]
```

Without it the Raw toggle fails with a CORS error in the browser console while
everything else looks fine.

## 3.6 Cache rules

**Caching → Cache Rules.** Three rules; order does not matter, the hostnames do
not overlap.

| Rule | When | Then |
|---|---|---|
| `api-bypass` | hostname equals `api.mdfly.dev` | **Bypass cache** |
| `cdn-immutable` | hostname equals `cdn.mdfly.dev` | Eligible for cache; **Edge TTL: override to 1 year**; Browser TTL 1 year |
| `apex-documents` | hostname equals `mdfly.dev` | Eligible for cache; Edge TTL **respect origin** |

`api.` must never be cached — every response is either a mutation or carries an
`Authorization`-scoped result.

`cdn.` holds content-addressed blobs at `documents/<slug>/<hash>.<ext>`, so the URL
changes whenever the bytes change and the object can be cached forever. **The Edge
TTL override is doing real work here**: the presigned PUT does not set a
`Cache-Control` header, so without this rule the objects fall back to Cloudflare's
default TTL rather than caching forever.

The apex respects the backend's own `max-age=300, s-maxage=86400` on slug
responses (ADR-0010), which is what keeps the origin off the per-view hot path.
`/_static/*` is content-hashed and inherits the same rule.

Also turn on **Caching → Tiered Cache → Smart Tiered Caching** to funnel edge
misses through one upper tier.

## 3.7 Edge rate limit

**Security → WAF → Rate limiting rules → Create rule.**

The Free plan is tightly boxed in: **one rule**, a **10-second** counting period, a
**10-second** mitigation timeout, **IP** as the only characteristic, and expression
fields limited to **path and verified-bot** — there is no hostname field. So the rule
matches on path alone:

| Field | Value |
|---|---|
| When incoming requests match | URI path starts with `/v1/publish/` **or** URI path starts with `/v1/update/` |
| Characteristics | IP |
| Rate | 10 requests per 10 seconds |
| Action | Block, 10 seconds |

In the expression editor that is:

```
(starts_with(http.request.uri.path, "/v1/publish/") or starts_with(http.request.uri.path, "/v1/update/"))
```

Dropping the hostname costs nothing — only `api.mdfly.dev` serves `/v1/*`, so no other
host can match. On **Pro or above** the same rule takes a 1-minute period and a 60-second
block, which is a closer match to the backend's own 10/min window; on Free the shorter
window is what the plan allows, not a tuning choice.

This is **L1** — crude, IP-only, absorbing floods before they reach the origin. The
backend's own identity-aware limit sits beneath it (ADR-0013) and is verified in
step 5.

## 3.8 Security defaults

| Setting | Value |
|---|---|
| Security → Settings → Security Level | Medium |
| Security → Bots → Bot Fight Mode | On |
| Security → WAF → Managed rules | On (free managed ruleset) |

**Known false-positive risk**: Bot Fight Mode can block legitimate `curl` and agent
traffic against `/llm/*` — bot-shaped by design — once that route ships. Start with
defaults, and watch **Security → Events** for blocks on that path.

There is no per-path exception to fall back on: Bot Fight Mode is all-or-nothing on
Free, and a WAF skip rule does not apply to it (the configurable version, Super Bot
Fight Mode, is Pro and above). Once you have confirmed the false positive in the
events log, the remedy is to **turn Bot Fight Mode off** and lean on Managed Rules
plus the rate limit — or upgrade, if the bot protection is worth the plan.

## 3.9 Purge token

Every commit and delete purges two prefixes — `mdfly.dev/<slug>` and
`mdfly.dev/llm/<slug>` — so the backend needs a token (ADR-0012).

Use a scoped token, not the Global API Key, which authorizes everything on the
account and cannot be narrowed.

**My Profile → API Tokens → Create Token → Create Custom Token:**

| Field | Value |
|---|---|
| Name | `mdfly-backend-purge` |
| Permissions | **Zone → Cache Purge → Purge**, and nothing else |
| Zone Resources | Include → Specific zone → `mdfly.dev` |
| Client IP Filtering | *(recommended)* Is in → the VM's reserved IP |
| TTL | open-ended |

Copy the token — shown once.

```
CLOUDFLARE_API_TOKEN=<token>
```

Prefix purge needs **no plan upgrade**; purge by URL, hostname, tag, prefix, and
purge-everything are all on Free. Only the rate limits differ by plan.

Verify the token before the backend depends on it:

```sh
curl -s -X POST \
  "https://api.cloudflare.com/client/v4/zones/$CLOUDFLARE_ZONE_ID/purge_cache" \
  -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"prefixes":["mdfly.dev/does-not-exist"]}'
```

Expect `"success":true`. Purging a slug that was never cached is a valid no-op.

On failure, read `errors[].message` in the response body — it names the actual
problem, where the numeric codes overlap across causes. `"code":10000
Authentication error` is the one worth memorising: wrong token, or a token without
Cache Purge. Anything else, believe the message: a bad zone ID, a token scoped to a
different zone, and a malformed `prefixes` payload all surface here and are told
apart by the text, not the number.

**Free-plan purge limits**: 5 requests/minute (token bucket, burst 25), and **30
prefixes per request** — prefix purge has its own cap, below the 100-operation
ceiling that applies to purge generally. The backend sends one request per slug, so a
large backlog draining at once will hit 429s. That is handled — a 429 is treated like
any other failure, the row stays queued, and the backlog drains over several ticks. If
it becomes routine, coalesce slugs into one request: each slug costs **two** prefixes
(`/<slug>` and `/llm/<slug>`), so 30 prefixes is **15 slugs** per call.

Next: [4. Deploy](4-deploy.md).
