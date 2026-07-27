# Backend deploy target: DigitalOcean App Platform (v1)

**Status: superseded by ADR-0029** — the deploy target moved to an Oracle Cloud Always Free VM once free-tier-only (no spend) became the hard constraint. The four portability constraints below (stateless, env-var config, single Dockerfile, `/healthz`) are retained by ADR-0029.

The Go HTTP backend is deployed to DigitalOcean App Platform as a containerized service, sitting in the same DO region as the managed Postgres database.

We chose App Platform over a raw Droplet because the project owner has DO student credit covering the higher PaaS markup, and the time saved on operating a VPS (TLS renewal, systemd units, log rotation, deploy pipeline) is more valuable than the dollar difference at this stage. To keep the door open to migrating to ECS Fargate, App Runner, or a Droplet later, the backend is held to four constraints: stateless processes, configuration via environment variables only, a single portable Dockerfile, and a generic `/healthz` endpoint — no App-Platform-specific features (their managed jobs, service discovery, etc.) may be relied on.
