# Dependencies

Every direct dependency, why it is here and who approved it. Add a row in the same commit that adds
the dependency (see `.agents/skills/dependency-check/`). Remove the row when the dependency goes.

| Package | Version | Why | Approved by | Date |
|---|---|---|---|---|
| `github.com/jackc/pgx/v5` | v5.11.0 | Postgres driver and pool. The standard library has no Postgres driver; pgx is the most used, MIT licensed, maintained, no install scripts. v5.12.0 skipped: under 14 days old when added | maintainer | 2026-11-08 |
| `golang.org/x/crypto` | v0.58.0 | Argon2id password hashing (`argon2` package only). Maintained by the Go team, BSD-3-Clause. govulncheck lists GO-2026-5932 for its `openpgp` package, which the core does not import | maintainer | 2026-11-08 |

Indirect: `jackc/puddle/v2`, `jackc/pgpassfile`, `jackc/pgservicefile` (pulled in by pgx), `golang.org/x/sync`,
`golang.org/x/sys`, `golang.org/x/text`. Tools run with a pinned version and not linked into the binary:
`golang.org/x/vuln/cmd/govulncheck@v1.9.0`.
