# 2. Managed Postgres

A `DATABASE_URL` the VM can reach. Postgres is **not** on the VM: ADR-0005 fixes
the `DATABASE_URL` contract and states the host "is deliberately not part of this
decision", so any managed provider works with no code or compose change.

**You need**: the VM from step 1. **You produce**: a verified `DATABASE_URL`, used
in step 4.

## 2.1 Provision

Create a Postgres instance with your provider. Three choices that matter:

- **Region** — same region as the VM, or as close as possible. Every request that
  touches metadata pays this round trip.
- **A dedicated role and database**, not the provider's admin superuser. The role
  needs DDL on the mdfly database (migrations create tables and indexes) and
  nothing beyond it.
- **Backups on**, if the provider offers them. This is the only restore path when a
  migration goes wrong — nothing on the VM keeps a copy of the data.

Version 14 or newer. The schema uses `JSONB`, `BYTEA`, `TIMESTAMPTZ`, `BIGSERIAL`,
and partial indexes — nothing exotic, no extensions.

## 2.2 Allowlist the VM

Most providers firewall by source IP. Get the VM's **outbound** address, which on
Oracle with a reserved public IP is that same address — but check rather than
assume:

```sh
# on the VM
curl -4 -s ifconfig.me; echo
```

Add that address to the provider's firewall as a single-host rule (start IP = end
IP). Do not open `0.0.0.0/0`, even temporarily — a Postgres port open to the
internet is found by scanners in minutes.

## 2.3 Build the connection string

```
postgres://<role>:<password>@<host>:5432/<database>?sslmode=require
```

`sslmode=require` is **not optional**. Unlike Redis, this connection leaves the
machine.

Two different things mangle a password here, and they want opposite fixes.

**The URI.** Reserved characters must be **percent-encoded** — `@` above all, which
otherwise splits the URL at the wrong place and gets you a confusing "host not found".

**The shell.** In double quotes, `bash` expands `$`, `` ` ``, and `\` *before* psql
sees the string. A password containing `$922` silently becomes `22` (bash reads `$9`,
an empty positional parameter, then literal `22`) and you get
`password authentication failed` for a password you know is right. Always put a
connection URI in **single** quotes on the command line. Note the inverse in `.env`:
Compose's `env_file` runs no shell, so there the value goes in raw and **unquoted** —
quotes would become part of the password.

Easiest path by far: generate a password with no punctuation, e.g.
`openssl rand -hex 32`. Neither problem can then occur.

If the provider offers **both a pooled and a direct endpoint**, use the pooled one
for `DATABASE_URL` (each process has its own pool and opens long-lived connections)
and the direct one for migrations, which run DDL. Step 4 shows how to pass the
direct URL to `migrate` without touching `.env`.

Budget for **three** pools, not one: a web slot, the `jobs` container, and — for
the few seconds of a blue-green swap — a second web slot. The plan's connection
limit is the ceiling everything else is fitted under, and a swap is when it is
briefly tightest.

## 2.4 Check

Install the Postgres **client** on the VM — `psql` only, no server:

```sh
sudo apt-get -y install postgresql-client-16
```

Then, before going any further:

```sh
 psql 'postgres://<role>:<pw>@<host>:5432/<db>?sslmode=require' -Atc 'select version(), now();'
```

**Single quotes**, per 2.3 — double quotes let the shell rewrite your password. Test the
**whole string**, password included, since that is what catches an encoding mistake
before it becomes a container that will not boot. Note the **leading space**: Ubuntu's
default `HISTCONTROL=ignoreboth` includes `ignorespace`, so it keeps the password out
of `~/.bash_history`. To prompt instead, and give up the encoding check:
`psql -h <host> -p 5432 -U <role> -d <db> 'sslmode=require'`.

| Symptom | Cause |
|---|---|
| Hangs, then times out | The firewall allowlist from 2.2 is missing or has the wrong IP |
| `password authentication failed` | Double quotes let the shell eat part of the password (2.3), an unencoded reserved character, or genuinely the wrong password — in that order of likelihood |
| `no pg_hba.conf entry ... no encryption` | `sslmode=require` missing |
| `database "..." does not exist` | Wrong database name, or the role has no access to it |

Do not continue until this returns a version string. A wrong `DATABASE_URL` shows
up in step 4 as a container that boots and immediately exits, which is a much worse
place to debug it.

Next: [3. Cloudflare](3-cloudflare.md).
