# 1. The Oracle VM

An always-on ARM box with a fixed public IP, Docker, and the repo cloned. ADR-0003
picks always-on-free so the periodic jobs can be in-process tickers with no
external scheduler.

**You need**: an SSH keypair. **You produce**: a reserved public IP, used in steps
2 and 3.

## 1.1 Create the instance

Sign up at <https://cloud.oracle.com>. Pick a **home region** near your users and
pick it carefully — it cannot be changed later, and Always Free ARM capacity varies
a lot by region.

**Compute → Instances → Create instance:**

| Field | Value |
|---|---|
| Image | Canonical Ubuntu 24.04, **aarch64** build |
| Shape | **VM.Standard.A1.Flex** (Ampere ARM) |
| OCPUs / memory | 1 OCPU / 6 GB |
| Boot volume | 50 GB |
| SSH keys | paste your public key |

Confirm the shape row says **"Always Free-eligible"** before creating. An A1 shape
without that label bills hourly.

**`Out of host capacity` is normal.** ARM capacity is scarce. Retry across the
availability domains in your region, over hours rather than seconds. Do not switch
to an E-series micro shape that provisions easily — that is a different allowance
with 1 GB RAM, which will not hold this stack.

## 1.2 Reserve the public IP

A new instance gets an **ephemeral** IP that changes on stop/start. DNS points a
fixed A record at the origin (ADR-0011), so make it static:

**Instance → Attached VNICs → primary VNIC → IPv4 Addresses → the public IP → Edit
→ Reserved public IP → Reserve new.**

Write the address down. Steps 2 and 3 both need it.

## 1.3 Open 443, and only 443

Two firewalls sit between the internet and Caddy. Both must allow 443/TCP and
nothing else.

**Oracle VCN** — **Networking → Virtual cloud networks → your VCN → Security Lists
→ Default Security List → Add Ingress Rule:**

| Field | Value |
|---|---|
| Source CIDR | `0.0.0.0/0` |
| IP Protocol | TCP |
| Destination Port Range | `443` |

Keep the existing SSH (22) rule, and narrow its source to your own IP if you can.
Add nothing for 6379 or 8080. **Do not open 80** — Caddy uses a long-lived
Cloudflare Origin CA certificate, not ACME, so it never needs an HTTP challenge.

**OS firewall.** Oracle's Ubuntu images ship iptables rules that drop everything
but SSH, persisted by `netfilter-persistent`:

```sh
sudo iptables -I INPUT 6 -m state --state NEW -p tcp --dport 443 -j ACCEPT
sudo netfilter-persistent save
sudo iptables -L INPUT -n --line-numbers    # the new rule must sit above the REJECT
```

Do not install `ufw` here. It fights the image's existing iptables rules, and
Docker ignores it anyway (see the warning below).

> **Docker writes its own iptables rules in the `DOCKER` chain, consulted before
> `INPUT`.** Anything under `ports:` is internet-reachable whether or not your
> firewall allows it. Redis stays private because it has **no `ports:` key**, not
> because of the firewall. Never add `6379:6379` "to debug" — use
> `docker compose exec redis redis-cli`.

## 1.4 Install Docker and Git

```sh
ssh ubuntu@<reserved-ip>

sudo apt-get update && sudo apt-get -y upgrade
sudo apt-get -y install ca-certificates curl git gnupg postgresql-client-16

# Docker Engine + Compose v2, arm64
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

sudo usermod -aG docker ubuntu
exit                      # group membership only applies to a new login
```

Reconnect and check:

```sh
ssh ubuntu@<reserved-ip>
docker --version && docker compose version
uname -m                  # => aarch64
```

`postgresql-client-16` is for talking to the managed database in step 2. Use the
official Docker apt repo rather than `curl | sh` so upgrades come through `apt`
with the rest of the system.

## 1.5 Clone the repo

If the repo is **public**:

```sh
git clone https://github.com/Abir66/mdfly.git ~/mdfly
```

If it is **private**, give the box a read-only deploy key:

```sh
ssh-keygen -t ed25519 -C "mdfly-prod" -f ~/.ssh/id_ed25519 -N ""
cat ~/.ssh/id_ed25519.pub
```

Paste that into **GitHub → the repo → Settings → Deploy keys → Add deploy key**,
leaving *Allow write access* **off**. A deploy key is scoped to one repository,
where a personal access token is scoped to your whole account. Then:

```sh
ssh -T git@github.com                     # "successfully authenticated" is expected
git clone git@github.com:Abir66/mdfly.git ~/mdfly
```

The empty passphrase is deliberate — a passphrase would block unattended
`git pull` during redeploys.

Finally:

```sh
cd ~/mdfly && ls deploy/
# => Caddyfile  compose.yaml  redis.conf.example
```

`docker compose` commands in later steps are written from `~/mdfly/deploy`. They
work from anywhere, since every path in the compose file is relative to the file
itself, but staying in one directory is one less thing to think about.

## 1.6 Check

```sh
nproc; free -h            # your OCPU count; ~6 GB
uptime                    # revisit after a day — confirms no scale-to-zero
```

In the console, the instance page should still say Always Free-eligible, and
**Billing → Cost analysis** should read zero.

Next: [2. Managed Postgres](2-managed-postgres.md).
