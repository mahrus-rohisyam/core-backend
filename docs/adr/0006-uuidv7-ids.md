# 0006. Use UUIDv7 primary keys generated in Go

- Status: Accepted
- Date: 2026-11-08

## Context

IDs appear in URLs and API responses, so sequential integers leak counts and invite guessing. Random
UUIDv4 keys fragment B-tree indexes on large tables.

## Decision

Primary keys are UUIDv7 (time-ordered), generated in Go by the core's `id` package (RFC 9562, about forty lines on `crypto/rand`), stored as `uuid`.
Append-only tables with no public IDs (`audit_events`) may use `bigint` identity columns.

## Consequences

- IDs can be created before insert, which keeps related inserts in one round trip.
- IDs reveal their creation time to the millisecond. Do not use them where that matters.
- Index locality is close to that of sequential keys.

## Alternatives considered

- **`bigserial`.** Compact and fast, but guessable and leaks volume.
- **UUIDv4.** Random, but scatters inserts across the index.
- **ULID or KSUID strings.** Similar properties, but not a native Postgres type.
