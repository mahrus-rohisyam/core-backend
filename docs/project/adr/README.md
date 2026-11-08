# Architecture decision records

An ADR records one decision: what was decided, why, and what it costs. Decisions that apply to every
repository live here; this folder is identical in every core repository. Each repository's own
decisions are in its `docs/adr/`.

| # | Decision | Status |
|---|---|---|
| [0001](0001-core-as-versioned-libraries.md) | The core ships as versioned libraries; apps start from `app-template` | Proposed |
| [0002](0002-modular-monolith-single-binary.md) | Each app is a modular monolith shipped as one binary | Proposed |
| [0003](0003-postgres-only-datastore.md) | Postgres is the only required datastore | Proposed |
| [0004](0004-versioning-and-releases.md) | Semantic versioning, changelogs and deprecation policy | Proposed |
| [0005](0005-agent-kit-and-supply-chain.md) | Shared agent kit and dependency policy in every repository | Proposed |

## Writing a new ADR

1. Copy the template below to `NNNN-short-title.md` with the next number.
2. Keep it to one page. Link to code or issues instead of pasting them.
3. Open it as `Proposed` in a merge request. It becomes `Accepted` when merged.
4. To change a decision, write a new ADR that supersedes the old one. Do not rewrite accepted ADRs.

```markdown
# NNNN. Title in the imperative ("Use X for Y")

- Status: Proposed | Accepted | Superseded by NNNN
- Date: YYYY-MM-DD

## Context
What problem we have, what constrains the answer.

## Decision
What we will do, stated plainly.

## Consequences
What gets easier, what gets harder, what we must now maintain.

## Alternatives considered
Each option and the reason it lost.
```
