# Metadata store: managed Postgres

All non-blob state — Documents (slug → bundle pointer, owner, edit-token hash, timestamps), Users (GitHub identity), and the Asset / Document-Asset tables that drive deduplication and garbage collection — lives in a managed Postgres instance (DigitalOcean Managed Database or AWS RDS). R2 holds the bytes; Postgres holds the index.

We chose Postgres over a KV store (Cloudflare KV, DynamoDB) because mdfly's natural queries are relational — "list this user's documents", "decrement refcount and delete asset where refcount=0", "find orphan blobs" — and over SQLite on a backend disk because horizontal scaling and managed backups matter more than the operational simplicity of a single file. The cost is a separate piece of infra to provision and pay for; mitigated by sticking to a managed provider with point-in-time-recovery rather than self-hosting.
