-- Liveness stamp for the jobs process (ADR-0003). That process has no HTTP
-- surface, so /healthz cannot see it: every tick upserts its outcome here, and
-- this row is the only way "is the GC alive?" gets an answer. Read by hand —
-- nothing serves it.
--
-- One row per job name, not an append-only log: the question is liveness, and a
-- growing history would need a GC pass of its own.
CREATE TABLE job_runs (
    name                 TEXT PRIMARY KEY,
    last_success_at      TIMESTAMPTZ,
    last_failure_at      TIMESTAMPTZ,
    last_error           TEXT,
    consecutive_failures INT NOT NULL DEFAULT 0
);
