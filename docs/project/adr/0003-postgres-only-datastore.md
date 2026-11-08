# 0003. Postgres is the only required datastore

- Status: Proposed
- Date: 2026-11-08

## Context

A typical stack adds Redis for sessions, rate limits and job queues next to the main database. Each
extra datastore costs memory, and needs backups, upgrades and monitoring. That cost matters most on
small servers and for small teams.

## Decision

- Postgres 17 is required. Everything stateful goes there by default: data, sessions, rate-limit
  counters, job queue (River), audit events and settings.
- Object storage (any S3-compatible service) is optional and only needed by apps that store files.
- Redis is not part of the core. An app may add it later for a measured need (for example a hot cache),
  behind an interface it owns.

## Consequences

- A new app needs one container besides itself. Backup is one `pg_dump`.
- Jobs are enqueued in the same transaction as the data that caused them, so no job is lost or sent for
  rolled-back data.
- Postgres carries more load. Session lookups and rate-limit counters are small indexed writes. Revisit
  this if p95 latency for `/api` exceeds 50 ms because of them.

## Alternatives considered

- **Redis for sessions, rate limits and queues (asynq).** Faster for counters, but adds a datastore that
  must be persisted and backed up, and jobs are no longer transactional with data.
- **SQLite.** Simplest to run, but limits an app to one writer process, which blocks running several
  copies behind a proxy.
