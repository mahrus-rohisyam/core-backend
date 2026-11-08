# Backend decisions

Decisions specific to `core-backend`. Decisions that apply to every repository are in
[../project/adr/](../project/adr/), which also has the template for a new ADR.

| # | Decision | Status |
|---|---|---|
| [0001](0001-stdlib-http.md) | Use the standard library HTTP server and router | Accepted |
| [0002](0002-pgx-plain-sql-own-migrations.md) | Use pgx with plain SQL, and a small built-in migration runner | Accepted |
| [0003](0003-server-side-sessions.md) | Use server-side sessions in a cookie, not JWTs | Accepted |
| [0004](0004-organizations-always-on.md) | Every app has organizations, even single-tenant ones | Accepted |
| [0005](0005-openapi-spec-first.md) | Keep an OpenAPI spec as the contract and test it against the router | Accepted |
| [0006](0006-uuidv7-ids.md) | Use UUIDv7 primary keys generated in Go | Accepted |
