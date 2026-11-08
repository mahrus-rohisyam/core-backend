# 0002. Use pgx with plain SQL, and a small built-in migration runner

- Status: Accepted
- Date: 2026-11-08

## Context

A Go app on Postgres has to pick a driver (`database/sql` or pgx), a query style (ORM, query builder or
generated code) and a migration tool. Here, modules must also ship their own migrations and run them in
order at startup.

The first draft of this ADR chose `sqlc` for queries and `goose` for migrations. While building the core,
both turned out to cost more than they give at this size: the core has about a hundred short queries, and
the migration needs are "run each module's SQL files once, in order, under a lock".

## Decision

- Driver: `jackc/pgx/v5` with `pgxpool`.
- Queries in the core are plain SQL strings next to the code that uses them, always with `$n`
  parameters. Every query on an organization's data has `org_id = $n` in its `WHERE` clause.
- Migrations: `db.Migrate`, about a hundred lines in the core. Each module embeds `NNNN_name.sql` files.
  They are recorded in one `schema_migrations (module, version)` table, applied one transaction per
  file, under a Postgres advisory lock so that several app copies can start at once. `app` runs the core
  migrations first, then each module in the order given to `app.New`.
- Migrations are forward only. Every migration must be safe while the previous app version is serving:
  add columns as nullable or with defaults, backfill, then constrain in a later release.
- Apps may use `sqlc` or another generator for their own modules; the core does not require one.

## Consequences

- No code generator and no migration library to install, pin or audit.
- Queries are reviewed as SQL. Scanning rows is manual, which is more code than `sqlc` would generate;
  integration tests catch column mismatches.
- No down migrations. A bad migration is fixed by a new one.
- Per-module versions let an app add or remove a module without renumbering others.

## Alternatives considered

- **sqlc.** Typed functions from SQL, but needs a pinned generator and committed generated code.
  Still a good choice for large app modules.
- **goose or golang-migrate.** Mature, but a dependency for a job the core does in one function, and a
  global version table makes per-module migrations awkward in golang-migrate.
- **GORM or ent.** Faster for CRUD, but hide the SQL, make scoping by `org_id` easy to forget, and add
  large dependency trees.
