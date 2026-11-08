# 0004. Use semantic versioning with changelogs and a deprecation window

- Status: Proposed
- Date: 2026-11-08

## Context

Apps depend on the cores by version ([ADR 0001](0001-core-as-versioned-libraries.md)). People other than
the authors will upgrade them, so they need to know what changed and what they must do.

## Decision

- Both cores follow [Semantic Versioning](https://semver.org). Until `v1.0.0`, minor versions may break
  the API; each break is listed in the changelog with upgrade steps.
- `core-backend` is released by pushing a git tag `vX.Y.Z`. `core-frontend` packages are versioned
  together (one version for all `@mahrus-rohisyam/core-*` packages) and published to the public npm
  registry from CI on the same kind of tag.
- Every merge request that changes behavior adds a line under `Unreleased` in `CHANGELOG.md`.
- From `v1.0.0`, a public API is deprecated for at least one minor release before removal. Go uses a
  `// Deprecated:` comment; TypeScript uses `@deprecated`.
- Database migrations shipped by core modules are never edited after a release; fixes are new migrations.

## Consequences

- Apps can pin a version and upgrade deliberately.
- Maintainers must write upgrade notes and keep old behavior alive during deprecation.
- Versioning the frontend packages together means a release may bump packages that did not change, in
  exchange for one version number to reason about.

## Alternatives considered

- **Independent versions per npm package.** More precise, but apps would need a compatibility matrix.
- **Calendar versioning.** Says when, not whether an upgrade breaks something.
